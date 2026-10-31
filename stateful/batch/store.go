package batch

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

type PostgresStore struct{ Pool *pgxpool.Pool }

func ApplySchema(ctx context.Context, p *pgxpool.Pool) error {
	if p == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT to_regclass('hllm_batch_schema_version') IS NOT NULL").Scan(&exists); err != nil {
		return err
	}
	if exists {
		var version int
		if err = tx.QueryRow(ctx, "SELECT version FROM hllm_batch_schema_version WHERE singleton").Scan(&version); err != nil {
			return err
		}
		if version != 1 {
			return errors.New("batch: incompatible schema")
		}
	} else {
		if _, err = tx.Exec(ctx, schema); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *PostgresStore) transaction(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	if s == nil || s.Pool == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT set_config('statement_timeout','2000ms',true)"); err != nil {
		return err
	}
	if err = fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PostgresStore) Create(ctx context.Context, scope, key string, items []Item, duration time.Duration) (ref JobRef, err error) {
	if !validScope(scope) || key == "" || len(items) < 1 || len(items) > MaximumItems || duration <= 0 {
		return ref, errors.New("batch: invalid manifest")
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return ref, err
	}
	digest := sha256.Sum256(raw)
	keyHash := sha256.Sum256([]byte(key))
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope+"\x00"+key)).String()
	now := time.Now().UTC()
	ref = JobRef{Scope: scope, JobID: id, Count: len(items), CreatedAt: now, Deadline: now.Add(duration), WorkflowID: "hllm-batch-v1/" + id}
	err = s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `INSERT INTO batch_jobs(api_key_hash,id,idempotency_hash,input_digest,item_count,workflow_id,created_at,deadline) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(api_key_hash,idempotency_hash) DO NOTHING`, scope, id, hex.EncodeToString(keyHash[:]), hex.EncodeToString(digest[:]), len(items), ref.WorkflowID, now, ref.Deadline)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			var priorDigest string
			e = tx.QueryRow(ctx, `SELECT id::text,input_digest,item_count,workflow_id,created_at,deadline FROM batch_jobs WHERE api_key_hash=$1 AND idempotency_hash=$2`, scope, hex.EncodeToString(keyHash[:])).Scan(&ref.JobID, &priorDigest, &ref.Count, &ref.WorkflowID, &ref.CreatedAt, &ref.Deadline)
			if e != nil {
				return e
			}
			if priorDigest != hex.EncodeToString(digest[:]) {
				return ErrConflict
			}
			return nil
		}
		rows := make([][]any, len(items))
		for i, item := range items {
			encoded, e := json.Marshal(item.Generation)
			if e != nil {
				return e
			}
			rows[i] = []any{scope, id, i, item.CustomID, encoded}
		}
		_, e = tx.CopyFrom(ctx, pgx.Identifier{"batch_items"}, []string{"api_key_hash", "job_id", "item_index", "custom_id", "request"}, pgx.CopyFromRows(rows))
		return e
	})
	return ref, err
}
func counts(ctx context.Context, tx pgx.Tx, scope, id string) (c Counts, err error) {
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE state='queued'),count(*) FILTER(WHERE state='active'),count(*) FILTER(WHERE state='waiting'),count(*) FILTER(WHERE state='succeeded'),count(*) FILTER(WHERE state='failed') FROM batch_items WHERE api_key_hash=$1 AND job_id=$2`, scope, id).Scan(&c.Total, &c.Queued, &c.Active, &c.Waiting, &c.Succeeded, &c.Failed)
	return
}
func (s *PostgresStore) Status(ctx context.Context, scope, id string) (v Status, err error) {
	if !validScope(scope) {
		return v, ErrNotFound
	}
	if _, e := uuid.Parse(id); e != nil {
		return v, ErrNotFound
	}
	err = s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		e := tx.QueryRow(ctx, `SELECT id::text,created_at,deadline,last_heartbeat,last_progress,waiting_reason,next_attempt_at FROM batch_jobs WHERE api_key_hash=$1 AND id=$2`, scope, id).Scan(&v.JobID, &v.CreatedAt, &v.Deadline, &v.LastWorkerHeartbeat, &v.LastProgressAt, &v.WaitingReason, &v.NextAttemptAt)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		v.Counts, e = counts(ctx, tx, scope, id)
		return e
	})
	v.Status = "queued"
	if v.Counts.Active > 0 || v.Counts.Succeeded+v.Counts.Failed > 0 {
		v.Status = "running"
	}
	if v.WaitingReason != "" && v.Counts.Active == 0 {
		v.Status = "waiting"
	}
	if v.Counts.Done() {
		v.Status = "completed"
		if v.Counts.Failed > 0 {
			v.Status = "completed_with_errors"
		}
		v.WaitingReason = ""
		v.NextAttemptAt = nil
	}
	return
}
func load(ctx context.Context, tx pgx.Tx, r Ref) (v StoredItem, err error) {
	var request, outcome []byte
	err = tx.QueryRow(ctx, `SELECT i.custom_id,i.request,i.state,i.attempts,i.outcome,j.created_at,j.deadline,i.queued_at FROM batch_items i JOIN batch_jobs j ON j.api_key_hash=i.api_key_hash AND j.id=i.job_id WHERE i.api_key_hash=$1 AND i.job_id=$2 AND i.item_index=$3`, r.Scope, r.JobID, r.Index).Scan(&v.Item.CustomID, &request, &v.State, &v.Attempt, &outcome, &v.CreatedAt, &v.Deadline, &v.ReadyAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return
	}
	if err = json.Unmarshal(request, &v.Item.Generation); err != nil {
		return
	}
	err = json.Unmarshal(outcome, &v.Outcome)
	return
}
func (s *PostgresStore) Load(ctx context.Context, r Ref) (v StoredItem, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error { var e error; v, e = load(ctx, tx, r); return e })
	return
}
func (s *PostgresStore) Begin(ctx context.Context, r Ref) (v StoredItem, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var lock string
		if e := tx.QueryRow(ctx, `SELECT id::text FROM batch_jobs WHERE api_key_hash=$1 AND id=$2 FOR UPDATE`, r.Scope, r.JobID).Scan(&lock); e != nil {
			return e
		}
		var e error
		v, e = load(ctx, tx, r)
		if e != nil {
			return e
		}
		if v.State == "succeeded" || v.State == "failed" {
			return nil
		}
		if v.State == "active" {
			return ErrConflict
		}
		if _, e = tx.Exec(ctx, `UPDATE batch_items SET state='active',attempts=attempts+1 WHERE api_key_hash=$1 AND job_id=$2 AND item_index=$3`, r.Scope, r.JobID, r.Index); e != nil {
			return e
		}
		v.WasWaiting = v.State == "waiting"
		v.State = "active"
		v.Attempt++
		_, e = tx.Exec(ctx, `UPDATE batch_jobs SET last_heartbeat=now(),last_progress=now() WHERE api_key_hash=$1 AND id=$2`, r.Scope, r.JobID)
		return e
	})
	return
}
func (s *PostgresStore) Record(ctx context.Context, r Ref, o Outcome) (c Counts, err error) {
	state := "failed"
	if o.Kind == "completed" {
		state = "succeeded"
	}
	if o.Kind == "rejected" {
		state = "waiting"
	}
	raw, e := json.Marshal(o)
	if e != nil {
		return c, e
	}
	err = s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var lock string
		if e := tx.QueryRow(ctx, `SELECT id::text FROM batch_jobs WHERE api_key_hash=$1 AND id=$2 FOR UPDATE`, r.Scope, r.JobID).Scan(&lock); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `UPDATE batch_items SET state=$4,outcome=$5,queued_at=CASE WHEN $4='waiting' THEN now() ELSE queued_at END WHERE api_key_hash=$1 AND job_id=$2 AND item_index=$3 AND state NOT IN ('succeeded','failed')`, r.Scope, r.JobID, r.Index, state, raw); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `UPDATE batch_jobs SET last_heartbeat=now(),last_progress=now() WHERE api_key_hash=$1 AND id=$2`, r.Scope, r.JobID); e != nil {
			return e
		}
		var e error
		c, e = counts(ctx, tx, r.Scope, r.JobID)
		return e
	})
	return
}
func (s *PostgresStore) Heartbeat(ctx context.Context, r Ref, progress bool) error {
	return s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE batch_jobs SET last_heartbeat=now(),last_progress=CASE WHEN $3 THEN now() ELSE last_progress END WHERE api_key_hash=$1 AND id=$2`, r.Scope, r.JobID, progress)
		return e
	})
}
func (s *PostgresStore) Cooldown(ctx context.Context, jobs []JobRef, reason string, next *time.Time) error {
	return s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		for _, j := range jobs {
			if _, e := tx.Exec(ctx, `UPDATE batch_jobs SET last_heartbeat=now(),waiting_reason=$3,next_attempt_at=$4 WHERE api_key_hash=$1 AND id=$2`, j.Scope, j.JobID, reason, next); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *PostgresStore) Expire(ctx context.Context, j JobRef) error {
	raw, _ := json.Marshal(Outcome{Kind: "failed", Error: &ItemError{Code: "job_deadline_exceeded", Message: "The job deadline expired before this item completed."}})
	return s.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var lock string
		if e := tx.QueryRow(ctx, `SELECT id::text FROM batch_jobs WHERE api_key_hash=$1 AND id=$2 FOR UPDATE`, j.Scope, j.JobID).Scan(&lock); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `UPDATE batch_items SET state='failed',outcome=$3 WHERE api_key_hash=$1 AND job_id=$2 AND state NOT IN ('succeeded','failed')`, j.Scope, j.JobID, raw)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `UPDATE batch_jobs SET last_progress=now(),waiting_reason='',next_attempt_at=NULL WHERE api_key_hash=$1 AND id=$2`, j.Scope, j.JobID)
		return e
	})
}
