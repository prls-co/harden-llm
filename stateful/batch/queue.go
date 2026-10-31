package batch

import (
	"context"
	"encoding/json"
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

// Compute one bounded round-robin page in one statement. Repeating the whole
// eligible-owner scan once per permit grows with both page size and live callers,
// and can hold the generation lock past the metadata transaction deadline.
const readyPageSQL = `WITH excluded AS MATERIALIZED (
 SELECT * FROM jsonb_to_recordset($3::jsonb) AS x(scope text,job_id uuid,item_index integer)
), candidates AS MATERIALIZED (
 SELECT j.api_key_hash,j.id,j.enqueue_sequence,j.deadline,'job'::text AS kind,
 i.item_index,i.queued_at,i.timeout_ms,NULL::bigint AS budget_ms,''::text AS request_id,i.item_turn
 FROM batch_jobs j JOIN LATERAL (
  SELECT i.item_index,i.queued_at,i.request->>'timeout_ms' AS timeout_ms,
   row_number() OVER(ORDER BY i.queued_at,i.item_index) AS item_turn
  FROM batch_items i WHERE i.api_key_hash=j.api_key_hash AND i.job_id=j.id
   AND i.state IN ('queued','waiting') AND i.current_permit IS NULL
   AND NOT EXISTS(SELECT 1 FROM excluded x WHERE x.scope=i.api_key_hash AND x.job_id=i.job_id AND x.item_index=i.item_index)
  ORDER BY i.queued_at,i.item_index LIMIT $7
 ) i ON true WHERE j.pool_id=$1 AND j.deadline>$4
 UNION ALL
 SELECT api_key_hash,request_id,enqueue_sequence,deadline,'ordinary',0,created_at,NULL,budget_ms,request_id::text,1
 FROM batch_request_waiters WHERE pool_id=$1 AND generation=$6 AND deadline>$4 AND live_until>$4
  AND closed_at IS NULL AND current_permit IS NULL AND NOT(request_id=ANY($5::uuid[]))
), turns AS (
 SELECT q.*,row_number() OVER(PARTITION BY q.api_key_hash ORDER BY q.item_turn,
  (q.enqueue_sequence<=COALESCE(c.last_job_sequence,0)),q.enqueue_sequence) AS scope_turn
 FROM candidates q LEFT JOIN batch_pool_scope_cursors c ON c.pool_id=$1 AND c.api_key_hash=q.api_key_hash
)
SELECT api_key_hash,CASE WHEN kind='job' THEN id::text ELSE NULL END,enqueue_sequence,deadline,
 item_index,queued_at,timeout_ms,kind,request_id,budget_ms
FROM turns ORDER BY scope_turn,(api_key_hash<=$2),api_key_hash LIMIT $7`

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
		raw, _ := json.Marshal(excluded)
		rows, err := tx.Query(ctx, readyPageSQL, selection.PoolID, selection.AfterScope, raw, time.Now().UTC(), requests, selection.Generation, selection.Limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		type cursor struct {
			Scope    string `json:"scope"`
			Sequence int64  `json:"sequence"`
		}
		cursors := map[string]int64{}
		for rows.Next() {
			var item ReadyItem
			var sequence int64
			var timeout *string
			var job *string
			var requestBudget *int64
			if err := rows.Scan(&item.Ref.Scope, &job, &sequence, &item.Deadline, &item.Ref.Index, &item.ReadyAt, &timeout, &item.Kind, &item.RequestID, &requestBudget); err != nil {
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
			cursors[item.Ref.Scope] = sequence
			result.Items = append(result.Items, item)
			result.AfterScope = item.Ref.Scope
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// Only the final selected owner per scope advances its durable cursor.
		// Ordering the bounded writes also avoids cross-selector lock inversion.
		updates := make([]cursor, 0, len(cursors))
		for scope, sequence := range cursors {
			updates = append(updates, cursor{scope, sequence})
		}
		if len(updates) > 0 {
			raw, _ := json.Marshal(updates)
			if _, err := tx.Exec(ctx, `INSERT INTO batch_pool_scope_cursors(pool_id,api_key_hash,last_job_sequence)
SELECT $1,scope,sequence FROM jsonb_to_recordset($2::jsonb) AS x(scope text,sequence bigint) ORDER BY scope
ON CONFLICT(pool_id,api_key_hash) DO UPDATE SET last_job_sequence=EXCLUDED.last_job_sequence`, selection.PoolID, raw); err != nil {
				return err
			}
		}
		return nil
	}, nil)
	if err != nil {
		return ReadyPage{}, ledgerError(err)
	}
	return result, nil
}
