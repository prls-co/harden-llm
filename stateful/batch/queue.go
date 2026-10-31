package batch

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// QueueSelection carries only references already granted but not durably reserved.
// Ready manifests stay in PostgreSQL, not in pool workflow snapshots.
type QueueSelection struct {
	PoolID          string
	Generation      int64
	AfterScope      string
	Limit           int
	Exclude         []Ref
	ExcludeRequests []string `json:",omitempty"`
}
type ReadyItem struct {
	Kind      string `json:",omitempty"`
	RequestID string `json:",omitempty"`
	Ref       Ref
	BudgetMS  int64
	Deadline  time.Time
	ReadyAt   time.Time
}
type ReadyPage struct {
	Items      []ReadyItem
	AfterScope string
}
type QueueStore interface {
	ExecutionStore
	AttachJob(context.Context, string, int64, JobRef) error
	SelectReady(context.Context, QueueSelection) (ReadyPage, error)
}

func (s *PostgresStore) AttachJob(ctx context.Context, pool string, generation int64, job JobRef) error {
	if !validPool(pool) || !validScope(job.Scope) || !validPermitID(job.JobID) {
		return ErrConflict
	}
	return ledgerError(s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := guardPool(ctx, tx, pool, generation); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE batch_jobs SET pool_id=$3 WHERE api_key_hash=$1 AND id=$2 AND (pool_id='' OR pool_id=$3)`, job.Scope, job.JobID, pool)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		return nil
	}, nil))
}

const nextReadySQL = `WITH excluded AS MATERIALIZED (
 SELECT * FROM jsonb_to_recordset($3::jsonb) AS x(scope text,job_id uuid,item_index integer)
), eligible AS NOT MATERIALIZED (
 SELECT j.api_key_hash,j.id,j.enqueue_sequence,j.deadline,'job'::text AS kind,NULL::bigint AS budget_ms,NULL::timestamptz AS created_at FROM batch_jobs j
 WHERE j.pool_id=$1 AND j.deadline>$4 AND EXISTS (
  SELECT 1 FROM batch_items i WHERE i.api_key_hash=j.api_key_hash AND i.job_id=j.id
  AND i.state IN ('queued','waiting') AND i.current_permit IS NULL
  AND NOT EXISTS(SELECT 1 FROM excluded x WHERE x.scope=i.api_key_hash AND x.job_id=i.job_id AND x.item_index=i.item_index)
 )
 UNION ALL
 SELECT api_key_hash,request_id,enqueue_sequence,deadline,'ordinary',budget_ms,created_at FROM batch_request_waiters
 WHERE pool_id=$1 AND generation=$6 AND deadline>$4 AND live_until>$4 AND closed_at IS NULL AND current_permit IS NULL AND NOT(request_id=ANY($5::uuid[]))
), next_scope AS (
 SELECT api_key_hash FROM eligible GROUP BY api_key_hash ORDER BY (api_key_hash<=$2),api_key_hash LIMIT 1
), next_job AS (
 SELECT e.* FROM eligible e JOIN next_scope s USING(api_key_hash)
 ORDER BY (e.enqueue_sequence<=COALESCE((SELECT last_job_sequence FROM batch_pool_scope_cursors c WHERE c.pool_id=$1 AND c.api_key_hash=e.api_key_hash),0)),e.enqueue_sequence LIMIT 1
)
SELECT j.api_key_hash,CASE WHEN j.kind='job' THEN j.id::text ELSE NULL END,j.enqueue_sequence,j.deadline,COALESCE(i.item_index,0),COALESCE(i.queued_at,j.created_at),i.request->>'timeout_ms',j.kind,CASE WHEN j.kind='ordinary' THEN j.id::text ELSE '' END,j.budget_ms
FROM next_job j LEFT JOIN LATERAL (
 SELECT i.item_index,i.queued_at,i.request FROM batch_items i
 WHERE j.kind='job' AND i.api_key_hash=j.api_key_hash AND i.job_id=j.id AND i.state IN ('queued','waiting') AND i.current_permit IS NULL
 AND NOT EXISTS(SELECT 1 FROM excluded x WHERE x.scope=i.api_key_hash AND x.job_id=i.job_id AND x.item_index=i.item_index)
 ORDER BY i.queued_at,i.item_index LIMIT 1
) i ON true WHERE j.kind='ordinary' OR i.item_index IS NOT NULL`

type excludedItem struct {
	Scope string `json:"scope"`
	JobID string `json:"job_id"`
	Index int    `json:"item_index"`
}

func (s *PostgresStore) SelectReady(ctx context.Context, selection QueueSelection) (ReadyPage, error) {
	result := ReadyPage{Items: []ReadyItem{}, AfterScope: selection.AfterScope}
	if !validPool(selection.PoolID) || selection.Limit < 1 || selection.Limit > 128 || len(selection.Exclude)+len(selection.ExcludeRequests) > 128 || (selection.AfterScope != "" && !validScope(selection.AfterScope)) {
		return result, ErrUnavailable
	}
	requests := append([]string{}, selection.ExcludeRequests...)
	for _, id := range requests {
		if !validPermitID(id) {
			return result, ErrUnavailable
		}
	}
	excluded := make([]excludedItem, 0, len(selection.Exclude)+selection.Limit)
	for _, ref := range selection.Exclude {
		if !validScope(ref.Scope) || !validPermitID(ref.JobID) || ref.Index < 0 || ref.Index >= MaximumItems {
			return result, ErrUnavailable
		}
		excluded = append(excluded, excludedItem{ref.Scope, ref.JobID, ref.Index})
	}
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := guardPool(ctx, tx, selection.PoolID, selection.Generation); err != nil {
			return err
		}
		now := time.Now().UTC()
		for range selection.Limit {
			raw, _ := json.Marshal(excluded)
			var item ReadyItem
			var sequence int64
			var timeout *string
			var job *string
			var requestBudget *int64
			err := tx.QueryRow(ctx, nextReadySQL, selection.PoolID, result.AfterScope, raw, now, requests, selection.Generation).Scan(&item.Ref.Scope, &job, &sequence, &item.Deadline, &item.Ref.Index, &item.ReadyAt, &timeout, &item.Kind, &item.RequestID, &requestBudget)
			if errors.Is(err, pgx.ErrNoRows) {
				break
			}
			if err != nil {
				return err
			}
			if job != nil {
				item.Ref.JobID = *job
			}
			ms := int64(0)
			if timeout != nil {
				ms, err = strconv.ParseInt(*timeout, 10, 64)
				if err != nil {
					return ErrConflict
				}
			}
			budget := generationBudget(Generation{TimeoutMS: ms})
			if budget <= 0 {
				return ErrConflict
			}
			item.BudgetMS = budget.Milliseconds()
			if item.Kind == "ordinary" {
				if requestBudget == nil || *requestBudget < 1 || *requestBudget > 900000 {
					return ErrConflict
				}
				item.BudgetMS = *requestBudget
			} else {
				item.Kind = ""
			}
			if _, err := tx.Exec(ctx, `INSERT INTO batch_pool_scope_cursors(pool_id,api_key_hash,last_job_sequence) VALUES($1,$2,$3) ON CONFLICT(pool_id,api_key_hash) DO UPDATE SET last_job_sequence=EXCLUDED.last_job_sequence`, selection.PoolID, item.Ref.Scope, sequence); err != nil {
				return err
			}
			result.Items = append(result.Items, item)
			result.AfterScope = item.Ref.Scope
			if item.Kind == "ordinary" {
				requests = append(requests, item.RequestID)
			} else {
				excluded = append(excluded, excludedItem{item.Ref.Scope, item.Ref.JobID, item.Ref.Index})
			}
		}
		return nil
	}, nil)
	if err != nil {
		return ReadyPage{}, ledgerError(err)
	}
	return result, nil
}
