package batch

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type PoolPublication struct {
	PoolID     string
	Generation int64
	Reason     string
	Next       *time.Time
	After      int64
	Limit      int
}
type PublicationPage struct {
	After   int64
	More    bool
	Updated int
}
type PoolProgressStore interface {
	PublishPool(context.Context, PoolPublication) (PublicationPage, error)
	HeartbeatAttempt(context.Context, Attempt, bool) error
}

func validWaitingReason(reason string) bool {
	switch reason {
	case "", "provider_throttled", "admission_window", "account_restricted", "dispatch_pending", "workflow_rollover", "persistence_wait", "worker_unavailable", "unresolved_execution", "pool_capacity":
		return true
	}
	return false
}
func (s *PostgresStore) PublishPool(ctx context.Context, p PoolPublication) (PublicationPage, error) {
	out := PublicationPage{After: p.After}
	if !validPool(p.PoolID) || p.Limit < 1 || p.Limit > 128 || p.After < 0 || !validWaitingReason(p.Reason) {
		return out, ErrUnavailable
	}
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var generation int64
		err := tx.QueryRow(ctx, `SELECT generation FROM batch_pool_generations WHERE pool_id=$1 FOR SHARE`, p.PoolID).Scan(&generation)
		if err != nil {
			return err
		}
		if generation != p.Generation {
			return ErrGenerationFenced
		}
		rows, err := tx.Query(ctx, `SELECT enqueue_sequence FROM (SELECT j.enqueue_sequence FROM batch_jobs j WHERE j.pool_id=$1 AND j.enqueue_sequence>$2 AND EXISTS(SELECT 1 FROM batch_items i WHERE i.api_key_hash=j.api_key_hash AND i.job_id=j.id AND i.state NOT IN ('succeeded','failed')) UNION ALL SELECT enqueue_sequence FROM batch_request_waiters WHERE pool_id=$1 AND enqueue_sequence>$2 AND closed_at IS NULL AND current_permit IS NULL AND live_until>now()) pending ORDER BY enqueue_sequence LIMIT $3`, p.PoolID, p.After, p.Limit+1)
		if err != nil {
			return err
		}
		sequences := []int64{}
		for rows.Next() {
			var sequence int64
			if err := rows.Scan(&sequence); err != nil {
				rows.Close()
				return err
			}
			sequences = append(sequences, sequence)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(sequences) > p.Limit {
			out.More = true
			sequences = sequences[:p.Limit]
		}
		if len(sequences) == 0 {
			return nil
		}
		out.After = sequences[len(sequences)-1]
		tag, err := tx.Exec(ctx, `UPDATE batch_jobs j SET last_heartbeat=CASE WHEN last_heartbeat IS NULL OR last_heartbeat<=now()-interval '5 seconds' THEN now() ELSE last_heartbeat END,waiting_reason=$3,next_attempt_at=$4 WHERE pool_id=$1 AND enqueue_sequence=ANY($2::bigint[]) AND EXISTS(SELECT 1 FROM batch_items i WHERE i.api_key_hash=j.api_key_hash AND i.job_id=j.id AND i.state NOT IN ('succeeded','failed')) AND (last_heartbeat IS NULL OR last_heartbeat<=now()-interval '5 seconds' OR waiting_reason IS DISTINCT FROM $3 OR next_attempt_at IS DISTINCT FROM $4)`, p.PoolID, sequences, p.Reason, p.Next)
		if err != nil {
			return err
		}
		out.Updated = int(tag.RowsAffected())
		tag, err = tx.Exec(ctx, `UPDATE batch_request_waiters SET pool_heartbeat_at=CASE WHEN pool_heartbeat_at IS NULL OR pool_heartbeat_at<=now()-interval '5 seconds' THEN now() ELSE pool_heartbeat_at END,waiting_reason=$3,next_attempt_at=$4 WHERE pool_id=$1 AND enqueue_sequence=ANY($2::bigint[]) AND closed_at IS NULL AND current_permit IS NULL AND live_until>now() AND (pool_heartbeat_at IS NULL OR pool_heartbeat_at<=now()-interval '5 seconds' OR waiting_reason IS DISTINCT FROM $3 OR next_attempt_at IS DISTINCT FROM $4)`, p.PoolID, sequences, p.Reason, p.Next)
		if err != nil {
			return err
		}
		out.Updated += int(tag.RowsAffected())
		return nil
	}, nil)
	return out, ledgerError(err)
}
func (s *PostgresStore) HeartbeatAttempt(ctx context.Context, a Attempt, changed bool) error {
	if !validPermitID(a.PermitID) || !validPermitID(a.ClaimID) {
		return ErrClaimOwned
	}
	return ledgerError(s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE batch_jobs j SET last_heartbeat=CASE WHEN last_heartbeat IS NULL OR last_heartbeat<=now()-interval '5 seconds' THEN now() ELSE last_heartbeat END,last_progress=CASE WHEN $3 AND (last_progress IS NULL OR last_progress<=now()-interval '5 seconds') THEN now() ELSE last_progress END FROM batch_attempts a WHERE a.permit_id=$1 AND a.claim_id=$2 AND a.state='dispatching' AND a.api_key_hash=j.api_key_hash AND a.job_id=j.id AND (j.last_heartbeat IS NULL OR j.last_heartbeat<=now()-interval '5 seconds' OR ($3 AND (j.last_progress IS NULL OR j.last_progress<=now()-interval '5 seconds')))`, a.PermitID, a.ClaimID, changed)
		return err
	}, nil))
}
func (a *Activities) PublishPool(ctx context.Context, p PoolPublication) (PublicationPage, error) {
	store, ok := a.Store.(PoolProgressStore)
	if !ok || p.PoolID != a.PoolID {
		return PublicationPage{}, ErrUnavailable
	}
	page, err := store.PublishPool(ctx, p)
	if err == nil {
		if requests, ok := a.Store.(RequestQueueStore); ok {
			if _, pruneErr := requests.PruneRequests(ctx, p.PoolID, 128); pruneErr != nil && a.Logger != nil {
				a.Logger.Warn("ordinary admission metadata pruning unavailable")
			}
		}
	}
	return page, err
}
func (s PoolState) Waiting(now time.Time) (string, *time.Time) {
	switch {
	case s.unresolved():
		return "unresolved_execution", nil
	case s.PersistenceWait || (len(s.RequestRetirements) > 0 && len(s.RequestRetirements)+len(s.Outstanding) >= 1000):
		return "persistence_wait", nil
	case s.Quiescing:
		return "workflow_rollover", nil
	case s.Controller.Paused:
		return "account_restricted", nil
	case s.DispatchNeedsReady:
		if s.DispatchBlockedUntil.After(now) {
			at := s.DispatchBlockedUntil
			return "worker_unavailable", &at
		}
		return "worker_unavailable", nil
	case s.Controller.Cooling:
		if !s.Controller.Probing && !s.Controller.NextEligible.IsZero() {
			at := s.Controller.NextEligible
			return "provider_throttled", &at
		}
		return "provider_throttled", nil
	}
	if at := s.Controller.WindowWait(now); !at.IsZero() {
		return "admission_window", &at
	}
	if !s.WindowAnchored && s.Controller.Policy.WindowRequests > 0 && s.Controller.WindowStarts >= s.Controller.Policy.WindowRequests {
		return "admission_window", nil
	}
	if len(s.StartIntents) > 0 {
		return "dispatch_pending", nil
	}
	if len(s.Outstanding) >= s.Controller.Policy.Concurrency {
		return "pool_capacity", nil
	}
	return "", nil
}
