package stateful

import (
	"context"
	_ "embed"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed request_tracking_schema.sql
var trackingSchemaSQL string

const trackingColumns = `tracking_id::text,owner_id::text,endpoint,status,stage,reason,created_at,deadline_at,retention_expires_at,updated_at,heartbeat_at,last_progress_at,finished_at,next_attempt_at,error_code,attempts_used,revision,outcome_confirmed`

func scanTracking(row pgx.Row) (TrackingRecord, error) {
	var r TrackingRecord
	err := row.Scan(&r.ID, &r.OwnerID, &r.Endpoint, &r.Status, &r.Stage, &r.Reason, &r.CreatedAt, &r.DeadlineAt, &r.RetentionExpiresAt, &r.UpdatedAt, &r.LastWorkerHeartbeatAt, &r.LastProgressAt, &r.FinishedAt, &r.NextAttemptAt, &r.ErrorCode, &r.AttemptsUsed, &r.Revision, &r.OutcomeConfirmed)
	return r, err
}

// applyTrackingSchema compares the actual table to a temporary table constructed
// from the exact same DDL. No serving request performs DDL or schema repair.
func applyTrackingSchema(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, trackingSchemaSQL); err != nil {
		return err
	}
	definition := strings.Split(trackingSchemaSQL, ";\nCREATE INDEX")[0]
	definition = strings.Replace(definition, "CREATE TABLE IF NOT EXISTS request_tracking", "CREATE TEMP TABLE hllm_tracking_expected", 1)
	definition = strings.ReplaceAll(definition, "request_tracking_pkey", "hllm_tracking_expected_pkey")
	if _, err := tx.Exec(ctx, definition+" ON COMMIT DROP"); err != nil {
		return err
	}
	// Columns include type, nullability and order. Constraints include their full
	// expressions and key order; merely sharing names is not compatibility proof.
	const shape = `SELECT
 (SELECT jsonb_agg(jsonb_build_array(attname,atttypid,atttypmod,attnotnull) ORDER BY attnum) FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped),
 (SELECT jsonb_agg(def ORDER BY def) FROM (SELECT pg_get_constraintdef(oid) AS def FROM pg_constraint WHERE conrelid=$1::regclass) c)`
	var actualCols, actualChecks, expectedCols, expectedChecks string
	if err := tx.QueryRow(ctx, shape, "request_tracking").Scan(&actualCols, &actualChecks); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, shape, "hllm_tracking_expected").Scan(&expectedCols, &expectedChecks); err != nil {
		return err
	}
	if actualCols != expectedCols || actualChecks != expectedChecks {
		return errors.New("stateful: incompatible request_tracking schema; operator action required")
	}
	var indexOK bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_index i WHERE i.indexrelid=to_regclass('request_tracking_expiry') AND i.indrelid='request_tracking'::regclass AND i.indisvalid AND i.indpred IS NULL AND i.indexprs IS NULL AND i.indnatts=3 AND ARRAY(SELECT a.attname::text FROM unnest(i.indkey) WITH ORDINALITY k(n,ord) JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.n ORDER BY ord)=ARRAY['api_key_hash','retention_expires_at','tracking_id'])`).Scan(&indexOK); err != nil {
		return err
	}
	if !indexOK {
		return errors.New("stateful: incompatible request_tracking expiry index; operator action required")
	}
	return nil
}

// Per-call fault hooks retain real transactions and never alter the provider path.
type trackingWriteOptions struct {
	commit        func(context.Context, pgx.Tx) error
	onInsert      func()
	onReadback    func()
	readbackError error
}

// ReserveTracking acknowledges a unique invocation before inference may start.
// An uncertain COMMIT only succeeds after positive readback of this exact nonce.
func (s *Service) ReserveTracking(ctx context.Context, hash string, r TrackingRecord) error {
	return s.reserveTracking(ctx, hash, r, trackingWriteOptions{})
}

func (s *Service) reserveTracking(ctx context.Context, hash string, r TrackingRecord, opts trackingWriteOptions) error {
	if s == nil || checkScope(hash) != nil || !ValidTrackingID(r.ID) || !ValidTrackingID(r.OwnerID) || r.Revision != 1 || r.Status != "running" || r.FinishedAt != nil {
		return ErrTrackingUnavailable
	}
	inserted := false
	err := withSQLTxCommit(ctx, s.config.Pool, pgx.TxOptions{}, storageTransactionTimeout, storageStatementTimeout, func(ctx context.Context, tx pgx.Tx) error {
		now := s.now().UTC()
		// Requested expired ID takes priority even when the scope has >100 expired rows.
		if _, err := tx.Exec(ctx, `DELETE FROM request_tracking WHERE api_key_hash=$1 AND tracking_id=$2 AND retention_expires_at <= $3`, hash, r.ID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM request_tracking WHERE api_key_hash=$1 AND tracking_id IN (SELECT tracking_id FROM request_tracking WHERE api_key_hash=$1 AND retention_expires_at<=$2 ORDER BY retention_expires_at,tracking_id LIMIT 99)`, hash, now); err != nil {
			return err
		}
		if opts.onInsert != nil {
			opts.onInsert()
		}
		tag, err := tx.Exec(ctx, `INSERT INTO request_tracking(api_key_hash,tracking_id,owner_id,endpoint,status,stage,created_at,deadline_at,retention_expires_at,updated_at,heartbeat_at,attempts_used,revision) VALUES($1,$2,$3,$4,'running','preparing',$5,$6,$7,$5,$5,0,1) ON CONFLICT ON CONSTRAINT request_tracking_pkey DO NOTHING`, hash, r.ID, r.OwnerID, r.Endpoint, r.CreatedAt, r.DeadlineAt, r.RetentionExpiresAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrTrackingConflict
		}
		inserted = true
		return nil
	}, opts.commit)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrTrackingConflict) {
		return err
	}
	if inserted {
		if opts.onReadback != nil {
			opts.onReadback()
		}
		if opts.readbackError != nil {
			return ErrTrackingUnavailable
		}
		saved, readErr := s.LookupTracking(ctx, hash, r.ID)
		if readErr == nil && saved.OwnerID == r.OwnerID {
			return nil
		}
	}
	return ErrTrackingUnavailable
}

// UpdateTracking fences stale revisions, expired owners and terminal rows.
func (s *Service) UpdateTracking(ctx context.Context, hash string, r TrackingRecord) error {
	if s == nil || checkScope(hash) != nil || !ValidTrackingID(r.ID) || !ValidTrackingID(r.OwnerID) {
		return ErrTrackingUnavailable
	}
	err := withStorageTx(ctx, s.config.Pool, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE request_tracking SET status=$4,stage=$5,reason=$6,updated_at=$7,heartbeat_at=$8,last_progress_at=$9,finished_at=$10,next_attempt_at=$11,error_code=$12,attempts_used=$13,revision=$14,outcome_confirmed=$15 WHERE api_key_hash=$1 AND tracking_id=$2 AND owner_id=$3 AND revision<$14 AND finished_at IS NULL AND retention_expires_at>$16`, hash, r.ID, r.OwnerID, r.Status, r.Stage, r.Reason, r.UpdatedAt, r.LastWorkerHeartbeatAt, r.LastProgressAt, r.FinishedAt, r.NextAttemptAt, r.ErrorCode, r.AttemptsUsed, r.Revision, r.OutcomeConfirmed, s.now().UTC())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			saved, err := scanTracking(tx.QueryRow(ctx, `SELECT `+trackingColumns+` FROM request_tracking WHERE api_key_hash=$1 AND tracking_id=$2 AND retention_expires_at>$3`, hash, r.ID, s.now().UTC()))
			if err != nil {
				return err
			}
			if saved.OwnerID != r.OwnerID || saved.Revision < r.Revision || (saved.Revision == r.Revision && !sameTrackingSnapshot(saved, r)) {
				return ErrTrackingNotFound
			}
		}
		return nil
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrTrackingNotFound) {
		return ErrTrackingNotFound
	}
	return ErrTrackingUnavailable
}

func (s *Service) LookupTracking(ctx context.Context, hash, id string) (TrackingRecord, error) {
	if s == nil || checkScope(hash) != nil || !ValidTrackingID(id) {
		return TrackingRecord{}, ErrTrackingUnavailable
	}
	var r TrackingRecord
	err := withStorageTx(ctx, s.config.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		r, err = scanTracking(tx.QueryRow(ctx, `SELECT `+trackingColumns+` FROM request_tracking WHERE api_key_hash=$1 AND tracking_id=$2 AND retention_expires_at>$3`, hash, id, s.now().UTC()))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return TrackingRecord{}, ErrTrackingNotFound
	}
	if err != nil {
		return TrackingRecord{}, ErrTrackingUnavailable
	}
	return r, nil
}

// Keep observation timestamps monotone even under a wall-clock adjustment.
func trackingTime(now, previous time.Time) time.Time {
	if now.Before(previous) {
		return previous
	}
	return now.UTC()
}

// PostgreSQL stores timestamptz at microsecond precision. Compare only stored
// fields on repeated acknowledgements, never GET-derived observation fields.
func sameTrackingSnapshot(a, b TrackingRecord) bool {
	canonical := func(r TrackingRecord) TrackingRecord {
		stamp := func(v time.Time) time.Time { return v.UTC().Truncate(time.Microsecond) }
		r.CreatedAt = stamp(r.CreatedAt)
		r.DeadlineAt = stamp(r.DeadlineAt)
		r.RetentionExpiresAt = stamp(r.RetentionExpiresAt)
		r.UpdatedAt = stamp(r.UpdatedAt)
		r.LastWorkerHeartbeatAt = stamp(r.LastWorkerHeartbeatAt)
		nullable := func(t *time.Time) *time.Time {
			if t == nil {
				return nil
			}
			v := stamp(*t)
			return &v
		}
		r.LastProgressAt = nullable(r.LastProgressAt)
		r.FinishedAt = nullable(r.FinishedAt)
		r.NextAttemptAt = nullable(r.NextAttemptAt)
		r.ObservedAt = time.Time{}
		r.ElapsedMS = 0
		r.RetryAfterMS = nil
		r.ObservationStale = false
		return r
	}
	return reflect.DeepEqual(canonical(a), canonical(b))
}
