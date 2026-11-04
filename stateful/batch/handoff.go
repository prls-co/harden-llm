package batch

import (
	"context"
	"errors"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"time"

	"github.com/jackc/pgx/v5"
	"go.temporal.io/sdk/temporal"
)

var ErrLegacyExecutionUnresolved = errors.New("batch: legacy local execution remains unresolved")

const legacyUpgradeSignal = "hllm-upgrade-v2"
const legacyQuiesceSignal = "hllm-quiesce-v1"

type LegacyHandoff struct {
	WorkflowID string
	Jobs       []JobRef
}
type PoolFence struct {
	PoolID               string
	Generation, Sequence int64
}
type HandoffStore interface {
	QueueStore
	AdoptLegacyPool(context.Context, string) (PoolFence, error)
}

func (s *PostgresStore) AdoptLegacyPool(ctx context.Context, pool string) (PoolFence, error) {
	out := PoolFence{PoolID: pool}
	if !validPool(pool) {
		return PoolFence{}, ErrGenerationFenced
	}
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		marker, err := poolRow(ctx, tx, pool, true)
		if err != nil {
			return err
		}
		if marker.Generation == 2 && marker.Mode == "active" {
			out.Generation = 2
			return tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0) FROM batch_attempts WHERE pool_id=$1`, pool).Scan(&out.Sequence)
		}
		if marker.Generation != 1 || (marker.Mode != "active" && marker.Mode != "quiescing") {
			return ErrGenerationFenced
		}
		var active, unknown bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM batch_attempts WHERE pool_id=$1 AND (state<>'settled' OR local_stopped_at IS NULL)) OR EXISTS(SELECT 1 FROM batch_items i JOIN batch_jobs j ON j.api_key_hash=i.api_key_hash AND j.id=i.job_id WHERE (j.pool_id=$1 OR j.pool_id='') AND i.state='active'),EXISTS(SELECT 1 FROM batch_items i JOIN batch_jobs j ON j.api_key_hash=i.api_key_hash AND j.id=i.job_id WHERE (j.pool_id=$1 OR j.pool_id='') AND i.outcome->'error'->>'code'='activity_failed')`, pool).Scan(&active, &unknown); err != nil {
			return err
		}
		if unknown {
			return ErrLegacyExecutionUnresolved
		}
		if active {
			return ErrGenerationFenced
		}
		if _, err := tx.Exec(ctx, `UPDATE batch_pool_generations SET generation=2,mode='active',revision=revision+1,updated_at=now() WHERE pool_id=$1`, pool); err != nil {
			return err
		}
		out.Generation = 2
		return tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0) FROM batch_attempts WHERE pool_id=$1`, pool).Scan(&out.Sequence)
	}, nil)
	return out, ledgerError(err)
}
func (a *Activities) PrepareLegacyJobs(ctx context.Context, h LegacyHandoff) error {
	store, ok := a.Store.(QueueStore)
	if !ok || h.WorkflowID != "hllm-cpa-pool-v1/"+a.PoolID || len(h.Jobs) > 16 {
		return ErrGenerationFenced
	}
	marker, err := store.PoolGeneration(ctx, a.PoolID)
	if errors.Is(err, ErrNotFound) {
		if err := store.EnsurePool(ctx, a.PoolID, 1); err != nil {
			return err
		}
		marker, err = store.PoolGeneration(ctx, a.PoolID)
	}
	if err != nil {
		return err
	}
	for _, job := range h.Jobs {
		if err := store.AttachJob(ctx, a.PoolID, marker.Generation, job); err != nil {
			return err
		}
	}
	return nil
}
func (a *Activities) AdvanceLegacyPool(ctx context.Context, h LegacyHandoff) (PoolFence, error) {
	store, ok := a.Store.(HandoffStore)
	if !ok || h.WorkflowID != "hllm-cpa-pool-v1/"+a.PoolID {
		return PoolFence{}, ErrGenerationFenced
	}
	fence, err := store.AdoptLegacyPool(ctx, a.PoolID)
	if errors.Is(err, ErrLegacyExecutionUnresolved) {
		return PoolFence{}, temporal.NewNonRetryableApplicationError("Legacy execution has no positive local-stop evidence.", "legacy_execution_unresolved", nil)
	}
	return fence, err
}

// CheckEmptyPoolBootstrap is read-only. Start the empty Temporal authority before
// creating its generation marker, so a lost start acknowledgement can retry the
// same workflow ID without leaving a database-only controller to reconstruct.
func (s *PostgresStore) CheckEmptyPoolBootstrap(ctx context.Context) error {
	return ledgerError(s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM batch_pool_generations) OR EXISTS(SELECT 1 FROM batch_jobs) OR EXISTS(SELECT 1 FROM batch_attempts)`).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrConflict
		}
		return nil
	}, nil))
}

type UpgradeProgress struct {
	PoolID, WorkflowType string
	Active, Unresolved   int
	Quiescing            bool
}

// WaitPoolUpgrade uses the stable workflow ID. Accepted jobs and provider calls
// remain owned by their workflows if the local command is canceled or restarted.
func WaitPoolUpgrade(ctx context.Context, c client.Client, pool string, progress func(UpgradeProgress)) (PoolState, error) {
	if c == nil || !validPool(pool) {
		return PoolState{}, ErrUnavailable
	}
	id := "hllm-cpa-pool-v1/" + pool
	described, err := c.DescribeWorkflowExecution(ctx, id, "")
	if err != nil {
		return PoolState{}, ErrUnavailable
	}
	if described == nil || described.WorkflowExecutionInfo == nil || described.WorkflowExecutionInfo.Type == nil {
		return PoolState{}, ErrUnavailable
	}
	if described.WorkflowExecutionInfo.Status != enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
		return PoolState{}, ErrGenerationFenced
	}
	name := described.WorkflowExecutionInfo.Type.Name
	if name == DispatcherWorkflowName {
		if err := c.SignalWorkflow(ctx, id, "", legacyUpgradeSignal, true); err != nil {
			return PoolState{}, ErrUnavailable
		}
	} else if name != PoolWorkflowName {
		return PoolState{}, ErrGenerationFenced
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	nextProgress := time.Time{}
	for {
		described, err := c.DescribeWorkflowExecution(ctx, id, "")
		if err != nil {
			return PoolState{}, ErrUnavailable
		}
		if described == nil || described.WorkflowExecutionInfo == nil || described.WorkflowExecutionInfo.Type == nil {
			return PoolState{}, ErrUnavailable
		}
		if described.WorkflowExecutionInfo.Status != enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
			return PoolState{}, ErrGenerationFenced
		}
		name = described.WorkflowExecutionInfo.Type.Name
		encoded, err := c.QueryWorkflow(ctx, id, "", stateQuery)
		if err == nil {
			if name == PoolWorkflowName {
				var state PoolState
				if err := encoded.Get(&state); err != nil {
					return PoolState{}, ErrUnavailable
				}
				if state.PoolID != pool || state.Generation < 2 || state.Quiescing {
					return PoolState{}, ErrGenerationFenced
				}
				return state, nil
			}
			if name != DispatcherWorkflowName {
				return PoolState{}, ErrGenerationFenced
			}
			var state DispatcherState
			if err := encoded.Get(&state); err != nil {
				return PoolState{}, ErrUnavailable
			}
			if state.Unresolved > 0 {
				return PoolState{}, ErrLegacyExecutionUnresolved
			}
			if progress != nil && !time.Now().Before(nextProgress) {
				progress(UpgradeProgress{PoolID: pool, WorkflowType: name, Active: state.Active, Unresolved: state.Unresolved, Quiescing: state.Quiescing})
				nextProgress = time.Now().Add(5 * time.Second)
			}
		}
		select {
		case <-ctx.Done():
			return PoolState{}, ctx.Err()
		case <-ticker.C:
		}
	}
}
