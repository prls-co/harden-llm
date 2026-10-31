package batch

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

//go:embed ledger_schema.sql
var ledgerSchema string

const attemptColumns = `permit_id::text,pool_id,generation,sequence,api_key_hash,kind,job_id::text,item_index,request_id::text,workflow_id,granted_at,start_by,execution_deadline,budget_ms,admission_epoch,policy_revision,probe,ordinal,state,claim_id::text,claimed_at,dispatch_at,local_stopped_at,settlement_revision,observation,generation_deadline,admission_window`

func applyLedgerSchema(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, ledgerSchema); err != nil {
		return err
	}
	for _, table := range []string{"batch_pool_generations", "batch_attempts", "batch_pool_scope_cursors", "batch_request_waiters"} {
		start := strings.Index(ledgerSchema, "CREATE TABLE IF NOT EXISTS "+table+" (")
		end := strings.Index(ledgerSchema[start:], "\n);") + start + 2
		ddl := strings.Replace(ledgerSchema[start:end], "CREATE TABLE IF NOT EXISTS "+table, "CREATE TEMP TABLE expected_"+table, 1)
		// PostgreSQL forbids temporary-to-permanent FKs. Compare their definitions
		// explicitly below; all remaining constraints use the exact same DDL.
		ddl = strings.ReplaceAll(ddl, " REFERENCES batch_pool_generations(pool_id)", "")
		ddl = strings.ReplaceAll(ddl, " FOREIGN KEY(api_key_hash,job_id,item_index) REFERENCES batch_items(api_key_hash,job_id,item_index),\n", "")
		if _, err := tx.Exec(ctx, ddl+" ON COMMIT DROP"); err != nil {
			return err
		}
		const shape = `SELECT (SELECT jsonb_agg(jsonb_build_array(attname,atttypid,atttypmod,attnotnull) ORDER BY attnum) FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped),(SELECT jsonb_agg(def ORDER BY def) FROM (SELECT pg_get_constraintdef(oid) AS def FROM pg_constraint WHERE conrelid=$1::regclass AND contype<>'f') c)`
		var ac, ak, ec, ek string
		if err := tx.QueryRow(ctx, shape, table).Scan(&ac, &ak); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, shape, "expected_"+table).Scan(&ec, &ek); err != nil {
			return err
		}
		if ac != ec || ak != ek {
			return errors.New("batch: incompatible execution ledger schema; operator action required")
		}
	}
	for _, index := range []struct {
		table, name, keys, predicate string
		unique                       bool
	}{
		{"batch_attempts", "batch_attempts_unsettled_item", "api_key_hash,job_id,item_index", "kind='job' AND state<>'settled'", true},
		{"batch_attempts", "batch_attempts_ordinary_request", "pool_id,api_key_hash,request_id", "kind='ordinary'", true},
		{"batch_attempts", "batch_attempts_pool_unsettled", "pool_id,state,permit_id", "", false},
		{"batch_request_waiters", "batch_request_waiters_ready", "pool_id,api_key_hash,enqueue_sequence", "closed_at IS NULL AND current_permit IS NULL", false},
		{"batch_request_waiters", "batch_request_waiters_retention", "pool_id,retention_until,request_id", "", false},
	} {
		unique := ""
		if index.unique {
			unique = "UNIQUE "
		}
		predicate := ""
		if index.predicate != "" {
			predicate = " WHERE " + index.predicate
		}
		if _, err := tx.Exec(ctx, "CREATE "+unique+"INDEX expected_"+index.name+" ON expected_"+index.table+"("+index.keys+")"+predicate); err != nil {
			return err
		}
		const indexShape = `SELECT jsonb_build_array(i.indisunique,i.indisvalid,am.amname,ARRAY(SELECT a.attname::text FROM unnest(i.indkey) WITH ORDINALITY k(n,ord) JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.n ORDER BY ord),pg_get_expr(i.indpred,i.indrelid)) FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_am am ON am.oid=c.relam WHERE i.indexrelid=$1::regclass`
		var actual, expected string
		if err := tx.QueryRow(ctx, indexShape, index.name).Scan(&actual); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, indexShape, "expected_"+index.name).Scan(&expected); err != nil {
			return err
		}
		if actual != expected {
			return errors.New("batch: incompatible execution ledger index; operator action required")
		}
	}
	var foreignKeys []string
	if err := tx.QueryRow(ctx, `SELECT array_agg(pg_get_constraintdef(oid) ORDER BY pg_get_constraintdef(oid)) FROM pg_constraint WHERE conrelid='batch_attempts'::regclass AND contype='f'`).Scan(&foreignKeys); err != nil {
		return err
	}
	if !reflect.DeepEqual(foreignKeys, []string{"FOREIGN KEY (api_key_hash, job_id, item_index) REFERENCES batch_items(api_key_hash, job_id, item_index)", "FOREIGN KEY (pool_id) REFERENCES batch_pool_generations(pool_id)"}) {
		return errors.New("batch: incompatible execution ledger foreign keys; operator action required")
	}
	var cursorForeignKeys []string
	if err := tx.QueryRow(ctx, `SELECT array_agg(pg_get_constraintdef(oid) ORDER BY pg_get_constraintdef(oid)) FROM pg_constraint WHERE conrelid='batch_pool_scope_cursors'::regclass AND contype='f'`).Scan(&cursorForeignKeys); err != nil {
		return err
	}
	if !reflect.DeepEqual(cursorForeignKeys, []string{"FOREIGN KEY (pool_id) REFERENCES batch_pool_generations(pool_id)"}) {
		return errors.New("batch: incompatible scope cursor foreign key; operator action required")
	}
	var waiterForeignKeys []string
	if err := tx.QueryRow(ctx, `SELECT array_agg(pg_get_constraintdef(oid) ORDER BY pg_get_constraintdef(oid)) FROM pg_constraint WHERE conrelid='batch_request_waiters'::regclass AND contype='f'`).Scan(&waiterForeignKeys); err != nil {
		return err
	}
	if !reflect.DeepEqual(waiterForeignKeys, []string{"FOREIGN KEY (pool_id) REFERENCES batch_pool_generations(pool_id)"}) {
		return errors.New("batch: incompatible request waiter foreign key; operator action required")
	}
	for _, column := range []struct {
		table, name, kind string
		required          bool
	}{{"batch_jobs", "pool_id", "text", true}, {"batch_jobs", "enqueue_sequence", "bigint", true}, {"batch_items", "reservation_ordinal", "bigint", true}, {"batch_items", "current_permit", "uuid", false}} {
		var typ string
		var required bool
		if err := tx.QueryRow(ctx, `SELECT format_type(atttypid,atttypmod),attnotnull FROM pg_attribute WHERE attrelid=$1::regclass AND attname=$2 AND NOT attisdropped`, column.table, column.name).Scan(&typ, &required); err != nil {
			return err
		}
		if typ != column.kind || required != column.required {
			return errors.New("batch: incompatible execution ledger columns; operator action required")
		}
	}
	return nil
}

func (s *PostgresStore) ledgerTx(ctx context.Context, fn func(context.Context, pgx.Tx) error, commit func(context.Context, pgx.Tx) error) error {
	if s == nil || s.Pool == nil || ctx == nil {
		return ErrUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 600*time.Millisecond)
	defer cancel()
	tx, err := s.Pool.BeginTx(bounded, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		deadline, _ := bounded.Deadline()
		cleanup, stop := context.WithDeadline(context.WithoutCancel(ctx), minTime(deadline, time.Now().Add(500*time.Millisecond)))
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	if _, err := tx.Exec(bounded, "SELECT set_config('statement_timeout','500ms',true)"); err != nil {
		return err
	}
	if err := fn(bounded, tx); err != nil {
		return err
	}
	if commit != nil {
		return commit(bounded, tx)
	}
	return tx.Commit(bounded)
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func validPermitID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}
func validPool(id string) bool {
	return len(id) > 0 && len(id) <= 128 && strings.TrimSpace(id) == id && !strings.ContainsAny(id, "\r\n\t\x00")
}
func ledgerError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrLegacyExecutionUnresolved, ErrGenerationFenced, ErrClaimOwned, ErrClaimUnconfirmed, ErrSettlementConflict, ErrConflict, ErrNotFound} {
		if errors.Is(err, known) {
			return known
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return ErrUnavailable
}
func poolRow(ctx context.Context, tx pgx.Tx, pool string, lock bool) (PoolGeneration, error) {
	query := `SELECT pool_id,generation,mode,revision FROM batch_pool_generations WHERE pool_id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	var p PoolGeneration
	err := tx.QueryRow(ctx, query, pool).Scan(&p.PoolID, &p.Generation, &p.Mode, &p.Revision)
	return p, err
}
func guardPool(ctx context.Context, tx pgx.Tx, pool string, generation int64) error {
	var got int64
	var mode string
	if err := tx.QueryRow(ctx, `SELECT generation,mode FROM batch_pool_generations WHERE pool_id=$1 FOR SHARE`, pool).Scan(&got, &mode); err != nil {
		return err
	}
	if got != generation || mode != "active" {
		return ErrGenerationFenced
	}
	return nil
}
func (s *PostgresStore) EnsurePool(ctx context.Context, id string, generation int64) error {
	if !validPool(id) || generation < 1 {
		return ErrGenerationFenced
	}
	return ledgerError(s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO batch_pool_generations(pool_id,generation,mode,updated_at) VALUES($1,$2,'active',now()) ON CONFLICT DO NOTHING`, id, generation); err != nil {
			return err
		}
		p, err := poolRow(ctx, tx, id, false)
		if err != nil {
			return err
		}
		if p.Generation != generation || p.Mode != "active" {
			return ErrGenerationFenced
		}
		return nil
	}, nil))
}
func (s *PostgresStore) PoolGeneration(ctx context.Context, id string) (PoolGeneration, error) {
	var p PoolGeneration
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		p, err = poolRow(ctx, tx, id, false)
		return err
	}, nil)
	return p, ledgerError(err)
}
func (s *PostgresStore) QuiescePool(ctx context.Context, id string, generation int64) error {
	return ledgerError(s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		p, err := poolRow(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if p.Generation != generation {
			return ErrGenerationFenced
		}
		_, err = tx.Exec(ctx, `UPDATE batch_pool_generations SET mode='quiescing',revision=revision+1,updated_at=now() WHERE pool_id=$1 AND mode='active'`, id)
		return err
	}, nil))
}
func (s *PostgresStore) AdvancePool(ctx context.Context, id string, from, to int64) error {
	if to != from+1 {
		return ErrGenerationFenced
	}
	return ledgerError(s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		p, err := poolRow(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if p.Generation == to && p.Mode == "active" {
			return nil
		}
		if p.Generation != from || p.Mode != "quiescing" {
			return ErrGenerationFenced
		}
		var outstanding bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM batch_attempts WHERE pool_id=$1 AND (state<>'settled' OR local_stopped_at IS NULL)) OR EXISTS(SELECT 1 FROM batch_items i JOIN batch_jobs j ON j.api_key_hash=i.api_key_hash AND j.id=i.job_id WHERE i.state='active' AND (j.pool_id=$1 OR j.pool_id=''))`, id).Scan(&outstanding); err != nil {
			return err
		}
		if outstanding {
			return ErrGenerationFenced
		}
		_, err = tx.Exec(ctx, `UPDATE batch_pool_generations SET generation=$2,mode='active',revision=revision+1,updated_at=now() WHERE pool_id=$1`, id, to)
		return err
	}, nil))
}
func scanAttempt(row pgx.Row) (Attempt, error) {
	var a Attempt
	var job, request, claim *string
	var index *int
	var raw []byte
	err := row.Scan(&a.PermitID, &a.PoolID, &a.Generation, &a.Sequence, &a.Ref.Scope, &a.Kind, &job, &index, &request, &a.WorkflowID, &a.GrantedAt, &a.StartBy, &a.Deadline, &a.BudgetMS, &a.Epoch, &a.PolicyRevision, &a.Probe, &a.Ordinal, &a.State, &claim, &a.ClaimedAt, &a.DispatchedAt, &a.StoppedAt, &a.SettlementRevision, &raw, &a.GenerationDeadline, &a.WindowID)
	if err != nil {
		return a, err
	}
	if job != nil {
		a.Ref.JobID = *job
	}
	if index != nil {
		a.Ref.Index = *index
	}
	if request != nil {
		a.RequestID = *request
	}
	if claim != nil {
		a.ClaimID = *claim
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a.Observation); err != nil {
			return a, err
		}
	}
	return a, nil
}
func readAttempt(ctx context.Context, tx pgx.Tx, id string, lock bool) (Attempt, error) {
	query := `SELECT ` + attemptColumns + ` FROM batch_attempts WHERE permit_id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	return scanAttempt(tx.QueryRow(ctx, query, id))
}
func (s *PostgresStore) ReadAttempts(ctx context.Context, pool string, ids []string) ([]Attempt, error) {
	if !validPool(pool) || len(ids) > 1000 {
		return nil, ErrUnavailable
	}
	for _, id := range ids {
		if !validPermitID(id) {
			return nil, ErrUnavailable
		}
	}
	result := []Attempt{}
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+attemptColumns+` FROM batch_attempts WHERE pool_id=$1 AND permit_id=ANY($2::uuid[]) ORDER BY sequence`, pool, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAttempt(rows)
			if err != nil {
				return err
			}
			result = append(result, a)
		}
		return rows.Err()
	}, nil)
	return result, ledgerError(err)
}
func sameReservation(a, b Reservation) bool {
	a.GrantedAt = a.GrantedAt.UTC().Truncate(time.Microsecond)
	b.GrantedAt = b.GrantedAt.UTC().Truncate(time.Microsecond)
	a.StartBy = a.StartBy.UTC().Truncate(time.Microsecond)
	b.StartBy = b.StartBy.UTC().Truncate(time.Microsecond)
	a.Deadline = a.Deadline.UTC().Truncate(time.Microsecond)
	b.Deadline = b.Deadline.UTC().Truncate(time.Microsecond)
	return reflect.DeepEqual(a, b)
}

type reservationWriteOptions struct{ afterMissing func() }

func (s *PostgresStore) ReserveAttempt(ctx context.Context, r Reservation) (Attempt, error) {
	return s.reserveAttempt(ctx, r, reservationWriteOptions{})
}
func (s *PostgresStore) reserveAttempt(ctx context.Context, r Reservation, opts reservationWriteOptions) (Attempt, error) {
	var result Attempt
	if !validPermitID(r.PermitID) || !validPool(r.PoolID) || !validScope(r.Ref.Scope) || r.Generation < 1 || r.Sequence < 1 || len(r.WorkflowID) == 0 || len(r.WorkflowID) > 256 || r.BudgetMS < 1 || r.BudgetMS > 900000 || r.PolicyRevision < 1 {
		return result, ErrUnavailable
	}
	if r.Kind != "job" && r.Kind != "ordinary" {
		return result, ErrUnavailable
	}
	if r.Kind == "job" && (!validPermitID(r.Ref.JobID) || r.Ref.Index < 0 || r.Ref.Index >= 1000 || r.RequestID != "") {
		return result, ErrUnavailable
	}
	if r.Kind == "ordinary" && (!validPermitID(r.RequestID) || r.Ref.JobID != "" || r.Ref.Index != 0) {
		return result, ErrUnavailable
	}
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := guardPool(ctx, tx, r.PoolID, r.Generation); err != nil {
			return err
		}
		old, err := readAttempt(ctx, tx, r.PermitID, true)
		if err == nil {
			if !sameReservation(old.Reservation, r) {
				return ErrConflict
			}
			result = old
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if opts.afterMissing != nil {
			opts.afterMissing()
		}
		ordinal := int64(1)
		var job any
		var index any
		var request any
		requestExpired := false
		if r.Kind == "job" {
			var pool, state string
			var current *string
			var raw []byte
			var jobDeadline time.Time
			if err := tx.QueryRow(ctx, `SELECT j.pool_id,i.state,i.current_permit::text,i.reservation_ordinal,i.request,j.deadline FROM batch_items i JOIN batch_jobs j ON j.api_key_hash=i.api_key_hash AND j.id=i.job_id WHERE i.api_key_hash=$1 AND i.job_id=$2 AND i.item_index=$3 FOR UPDATE OF i`, r.Ref.Scope, r.Ref.JobID, r.Ref.Index).Scan(&pool, &state, &current, &ordinal, &raw, &jobDeadline); err != nil {
				return err
			}
			var accepted Generation
			if err := json.Unmarshal(raw, &accepted); err != nil {
				return err
			}
			if !reservationMatchesSnapshot(r, accepted, jobDeadline) {
				return ErrConflict
			}
			if current != nil && *current == r.PermitID {
				existing, err := readAttempt(ctx, tx, r.PermitID, false)
				if err != nil {
					return err
				}
				if !sameReservation(existing.Reservation, r) {
					return ErrConflict
				}
				result = existing
				return nil
			}
			if (pool != "" && pool != r.PoolID) || current != nil || (state != "queued" && state != "waiting") {
				return ErrConflict
			}
			ordinal++
			if _, err := tx.Exec(ctx, `UPDATE batch_jobs SET pool_id=$3 WHERE api_key_hash=$1 AND id=$2 AND pool_id=''`, r.Ref.Scope, r.Ref.JobID, r.PoolID); err != nil {
				return err
			}
			job, index = r.Ref.JobID, r.Ref.Index
		} else {
			waiter, err := readRequestWaiter(ctx, tx, r.RequestID, true)
			if err != nil {
				return err
			}
			if !requestMatchesReservation(waiter, r) || waiter.PermitID != "" {
				return ErrConflict
			}
			now := time.Now().UTC()
			requestExpired = waiter.ClosedAt != nil || !now.Before(waiter.LiveUntil) || !now.Before(waiter.Deadline)
			ordinal = int64(waiter.Ordinal)
			request = r.RequestID
		}
		_, err = tx.Exec(ctx, `INSERT INTO batch_attempts(permit_id,pool_id,generation,sequence,api_key_hash,kind,job_id,item_index,request_id,workflow_id,granted_at,start_by,execution_deadline,budget_ms,admission_epoch,policy_revision,probe,ordinal,state,admission_window) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,'reserved',$19)`, r.PermitID, r.PoolID, r.Generation, r.Sequence, r.Ref.Scope, r.Kind, job, index, request, r.WorkflowID, r.GrantedAt, r.StartBy, r.Deadline, r.BudgetMS, r.Epoch, r.PolicyRevision, r.Probe, ordinal, r.WindowID)
		if err != nil {
			return err
		}
		if r.Kind == "job" {
			if _, err := tx.Exec(ctx, `UPDATE batch_items SET current_permit=$4,reservation_ordinal=$5,state='active' WHERE api_key_hash=$1 AND job_id=$2 AND item_index=$3`, r.Ref.Scope, r.Ref.JobID, r.Ref.Index, r.PermitID, ordinal); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE batch_request_waiters SET current_permit=$2 WHERE request_id=$1`, r.RequestID, r.PermitID); err != nil {
				return err
			}
			if requestExpired {
				// A selection/grant can race disconnection. Retain the pool's exact
				// grant as positively never sent so it can settle capacity safely.
				now := time.Now().UTC()
				o := AttemptObservation{Outcome: Outcome{Kind: "never_dispatched"}, LocalStopped: true, ObservedAt: now}
				raw, _ := json.Marshal(o)
				if _, err := tx.Exec(ctx, `UPDATE batch_attempts SET state='settled',observation=$2,settlement_revision=1,local_stopped_at=$3 WHERE permit_id=$1`, r.PermitID, raw, now); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE batch_request_waiters SET closed_at=COALESCE(closed_at,$2) WHERE request_id=$1`, r.RequestID, now); err != nil {
					return err
				}
			}
		}
		result, err = readAttempt(ctx, tx, r.PermitID, false)
		return err
	}, nil)
	return result, ledgerError(err)
}

func reservationMatchesSnapshot(r Reservation, g Generation, jobDeadline time.Time) bool {
	return generationBudget(g) > 0 && r.BudgetMS == generationBudget(g).Milliseconds() &&
		!r.Deadline.IsZero() && !r.Deadline.After(jobDeadline)
}

// Comparison canonicalizes only storage precision, never inference evidence.
func sameObservation(a, b AttemptObservation) bool {
	canonical := func(o AttemptObservation) AttemptObservation {
		stamp := func(p *time.Time) *time.Time {
			if p == nil {
				return nil
			}
			t := p.UTC().Truncate(time.Microsecond)
			return &t
		}
		o.ObservedAt = o.ObservedAt.UTC().Truncate(time.Microsecond)
		o.Outcome.StartedAt = stamp(o.Outcome.StartedAt)
		o.Outcome.FinishedAt = stamp(o.Outcome.FinishedAt)
		o.Outcome.GenerationDeadline = stamp(o.Outcome.GenerationDeadline)
		return o
	}
	x, _ := json.Marshal(canonical(a))
	y, _ := json.Marshal(canonical(b))
	return bytes.Equal(x, y)
}
