package batch

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const requestLiveness = 10 * time.Second
const maximumRequestWaiters = 10000

// RequestWaiter retains scheduling metadata for one live ordinary dispatch.
// RequestID is server-generated per dispatch; ExecutionID groups the dispatches
// of one incoming call. No inference input or output belongs in this record.
type RequestWaiter struct {
	WaitingReason                                  string
	NextAttemptAt, PoolHeartbeatAt                 *time.Time
	RequestID, PoolID, Scope, OwnerID, ExecutionID string
	Generation                                     int64
	Ordinal                                        int
	Sequence                                       int64
	CreatedAt, Deadline, LiveUntil, RetentionUntil time.Time
	BudgetMS                                       int64
	ClosedAt, PoolObservedAt                       *time.Time
	PermitID                                       string
}

type RequestQueueStore interface {
	ExecutionStore
	EnqueueRequest(context.Context, RequestWaiter) (RequestWaiter, error)
	ReadRequest(context.Context, string, string, string, bool) (RequestWaiter, error)
	CancelRequest(context.Context, string, string, string) error
	AcknowledgeRequests(context.Context, PoolRead) error
	PruneRequests(context.Context, string, int) (int, error)
}

const requestWaiterColumns = `request_id::text,pool_id,generation,api_key_hash,owner_id::text,execution_id::text,dispatch_ordinal,enqueue_sequence,created_at,deadline,budget_ms,live_until,closed_at,current_permit::text,pool_observed_at,retention_until,waiting_reason,next_attempt_at,pool_heartbeat_at`

func scanRequestWaiter(row pgx.Row) (RequestWaiter, error) {
	var w RequestWaiter
	var permit *string
	err := row.Scan(&w.RequestID, &w.PoolID, &w.Generation, &w.Scope, &w.OwnerID, &w.ExecutionID, &w.Ordinal, &w.Sequence, &w.CreatedAt, &w.Deadline, &w.BudgetMS, &w.LiveUntil, &w.ClosedAt, &permit, &w.PoolObservedAt, &w.RetentionUntil, &w.WaitingReason, &w.NextAttemptAt, &w.PoolHeartbeatAt)
	if permit != nil {
		w.PermitID = *permit
	}
	return w, err
}

func readRequestWaiter(ctx context.Context, tx pgx.Tx, id string, lock bool) (RequestWaiter, error) {
	query := `SELECT ` + requestWaiterColumns + ` FROM batch_request_waiters WHERE request_id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanRequestWaiter(tx.QueryRow(ctx, query, id))
}

func (s *PostgresStore) EnqueueRequest(ctx context.Context, w RequestWaiter) (RequestWaiter, error) {
	if !validPermitID(w.RequestID) || !validPool(w.PoolID) || !validScope(w.Scope) || !validPermitID(w.OwnerID) || !validPermitID(w.ExecutionID) || w.Generation < 2 || w.Ordinal < 1 || w.Ordinal > 1024 || w.BudgetMS < 1 || w.BudgetMS > 900000 {
		return RequestWaiter{}, ErrConflict
	}
	var result RequestWaiter
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Serialize admission of metadata only so concurrent API replicas cannot
		// exceed the pending-row bound. This lock never spans provider execution.
		p, err := poolRow(ctx, tx, w.PoolID, true)
		if err != nil {
			return err
		}
		if p.Generation != w.Generation || p.Mode != "active" {
			return ErrGenerationFenced
		}
		old, err := readRequestWaiter(ctx, tx, w.RequestID, false)
		if err == nil {
			if old.PoolID != w.PoolID || old.Generation != w.Generation || old.Scope != w.Scope || old.OwnerID != w.OwnerID || old.ExecutionID != w.ExecutionID || old.Ordinal != w.Ordinal || old.BudgetMS != w.BudgetMS || !old.Deadline.Equal(w.Deadline.UTC().Truncate(time.Microsecond)) {
				return ErrConflict
			}
			result = old
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		now := time.Now().UTC()
		if !now.Before(w.Deadline) || w.Deadline.After(now.Add(MaximumGenerationBudget)) {
			return ErrConflict
		}
		var pending int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM batch_request_waiters WHERE pool_id=$1 AND closed_at IS NULL AND live_until>$2`, w.PoolID, now).Scan(&pending); err != nil {
			return err
		}
		if pending >= maximumRequestWaiters {
			return ErrUnavailable
		}
		_, err = tx.Exec(ctx, `INSERT INTO batch_request_waiters(request_id,pool_id,generation,api_key_hash,owner_id,execution_id,dispatch_ordinal,created_at,deadline,budget_ms,live_until,retention_until) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$9::timestamptz+interval '24 hours')`, w.RequestID, w.PoolID, w.Generation, w.Scope, w.OwnerID, w.ExecutionID, w.Ordinal, now, w.Deadline, w.BudgetMS, minTime(now.Add(requestLiveness), w.Deadline))
		if err != nil {
			return err
		}
		result, err = readRequestWaiter(ctx, tx, w.RequestID, false)
		return err
	}, nil)
	return result, ledgerError(err)
}

func (s *PostgresStore) ReadRequest(ctx context.Context, scope, id, owner string, refresh bool) (RequestWaiter, error) {
	var out RequestWaiter
	if !validScope(scope) || !validPermitID(id) || !validPermitID(owner) {
		return out, ErrNotFound
	}
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = readRequestWaiter(ctx, tx, id, refresh)
		if err != nil {
			return err
		}
		if out.Scope != scope || out.OwnerID != owner {
			return ErrNotFound
		}
		now := time.Now().UTC()
		// An expired live owner cannot resurrect itself. Active claims retain
		// capacity even when this liveness evidence disappears.
		if refresh && out.ClosedAt == nil && now.Before(out.LiveUntil) && now.Before(out.Deadline) && out.LiveUntil.Before(minTime(now.Add(5*time.Second), out.Deadline)) {
			out.LiveUntil = minTime(now.Add(requestLiveness), out.Deadline)
			_, err = tx.Exec(ctx, `UPDATE batch_request_waiters SET live_until=$2 WHERE request_id=$1`, id, out.LiveUntil)
		}
		return err
	}, nil)
	if err != nil {
		return RequestWaiter{}, ledgerError(err)
	}
	return out, ledgerError(err)
}

func (s *PostgresStore) CancelRequest(ctx context.Context, scope, id, owner string) error {
	if !validScope(scope) || !validPermitID(id) || !validPermitID(owner) {
		return ErrNotFound
	}
	return ledgerError(s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE batch_request_waiters SET closed_at=COALESCE(closed_at,now()) WHERE request_id=$1 AND api_key_hash=$2 AND owner_id=$3`, id, scope, owner)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		// Cancellation proves only that the live caller is gone. It never marks
		// a claimed provider invocation stopped or releases its pool capacity.
		return nil
	}, nil))
}

func requestMatchesReservation(w RequestWaiter, r Reservation) bool {
	return w.PoolID == r.PoolID && w.Generation == r.Generation && w.Scope == r.Ref.Scope && w.RequestID == r.RequestID && w.BudgetMS == r.BudgetMS && w.Deadline.Equal(r.Deadline.UTC().Truncate(time.Microsecond))
}

func guardRequestClaim(ctx context.Context, tx pgx.Tx, a Attempt, nonce string) error {
	if a.Kind != "ordinary" {
		return nil
	}
	w, err := readRequestWaiter(ctx, tx, a.RequestID, true)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if !requestMatchesReservation(w, a.Reservation) || w.OwnerID != nonce || w.PermitID != a.PermitID || w.ClosedAt != nil || !now.Before(w.LiveUntil) || !now.Before(w.Deadline) {
		return ErrClaimOwned
	}
	return nil
}

// AcknowledgeRequests is called only after the pool applied terminal settlement
// and removed a positively stopped permit. A GET/read alone is not that proof.
func (s *PostgresStore) AcknowledgeRequests(ctx context.Context, read PoolRead) error {
	if !validPool(read.PoolID) || read.Generation < 2 || len(read.IDs) > 128 {
		return ErrConflict
	}
	for _, id := range read.IDs {
		if !validPermitID(id) {
			return ErrConflict
		}
	}
	return ledgerError(s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := guardPool(ctx, tx, read.PoolID, read.Generation); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE batch_request_waiters w SET pool_observed_at=COALESCE(w.pool_observed_at,now()) FROM batch_attempts a WHERE a.pool_id=$1 AND a.generation=$2 AND a.permit_id=ANY($3::uuid[]) AND a.kind='ordinary' AND a.state='settled' AND a.local_stopped_at IS NOT NULL AND w.current_permit=a.permit_id`, read.PoolID, read.Generation, read.IDs)
		return err
	}, nil))
}

// PruneRequests bounds retained ordinary metadata. Unknown/unstopped or not yet
// applied pool settlements are never removed, even after the retention time.
func (s *PostgresStore) PruneRequests(ctx context.Context, pool string, limit int) (int, error) {
	if !validPool(pool) || limit < 1 || limit > 128 {
		return 0, ErrConflict
	}
	deleted := 0
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT request_id::text,current_permit::text FROM batch_request_waiters WHERE pool_id=$1 AND retention_until<now() AND (current_permit IS NULL OR pool_observed_at IS NOT NULL) ORDER BY retention_until,request_id LIMIT $2 FOR UPDATE SKIP LOCKED`, pool, limit)
		if err != nil {
			return err
		}
		type expired struct {
			id     string
			permit *string
		}
		var expiredRows []expired
		for rows.Next() {
			var row expired
			if err := rows.Scan(&row.id, &row.permit); err != nil {
				rows.Close()
				return err
			}
			expiredRows = append(expiredRows, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, row := range expiredRows {
			if row.permit != nil {
				tag, err := tx.Exec(ctx, `DELETE FROM batch_attempts WHERE permit_id=$1 AND pool_id=$2 AND kind='ordinary' AND state='settled' AND local_stopped_at IS NOT NULL`, *row.permit, pool)
				if err != nil {
					return err
				}
				if tag.RowsAffected() != 1 {
					continue
				}
			}
			if _, err := tx.Exec(ctx, `DELETE FROM batch_request_waiters WHERE request_id=$1`, row.id); err != nil {
				return err
			}
			deleted++
		}
		return nil
	}, nil)
	return deleted, ledgerError(err)
}
