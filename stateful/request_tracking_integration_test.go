//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-492

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRequestTrackingStorage(t *testing.T) {
	t.Parallel()
	t.Run("additive compatible schema preserves data", func(t *testing.T) {
		p, _ := referencePool(t, false)
		execSQL(t, p, referenceSchemaSQL)
		execSQL(t, p, "CREATE TABLE tracking_sentinel(value text); INSERT INTO tracking_sentinel VALUES('preserved')")
		execSQL(t, p, "INSERT INTO profiles VALUES($1,'00000000-0000-4000-8000-000000000001','preserved',NULL,'{}','{}','deployment','cpa',NULL,now())", keyHashA)
		execSQL(t, p, "INSERT INTO history VALUES($1,'00000000-0000-4000-8000-000000000001',1,'succeeded',now(),now(),'{}','{}','{}')", keyHashA)
		execSQL(t, p, "INSERT INTO cache_entries VALUES($1,'preserved','operation',2,'{}',now()+interval '1 day')", keyHashA)
		for i := 0; i < 2; i++ {
			if err := ApplySchema(context.Background(), p); err != nil {
				t.Fatal(err)
			}
		}
		for _, table := range []string{"profiles", "history", "cache_entries"} {
			var rows int
			if err := p.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&rows); err != nil || rows != 1 {
				t.Fatal("additive schema changed accepted data", table, rows, err)
			}
		}
		var value string
		if err := p.QueryRow(context.Background(), "SELECT value FROM tracking_sentinel").Scan(&value); err != nil || value != "preserved" {
			t.Fatal("schema changed existing data", err)
		}
	})
	t.Run("incompatible same named table rejects without repair", func(t *testing.T) {
		p, _ := referencePool(t, true)
		execSQL(t, p, "ALTER TABLE request_tracking DROP CONSTRAINT request_tracking_attempts_used_check")
		if err := ApplySchema(context.Background(), p); err == nil {
			t.Fatal("incompatible constraint accepted")
		}
		var count int
		if err := p.QueryRow(context.Background(), "SELECT count(*) FROM pg_constraint WHERE conrelid='request_tracking'::regclass AND conname='request_tracking_attempts_used_check'").Scan(&count); err != nil || count != 0 {
			t.Fatal("operator schema silently repaired", err)
		}
	})
	t.Run("cross replica uniqueness scope and fenced finalization", func(t *testing.T) {
		p, _ := referencePool(t, true)
		one, _ := serviceFixture(t, p, nil)
		two, err := NewService(one.config)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		r, err := NewTrackingRecord(uuid.NewString(), "/v1/responses", now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		var wins, conflicts atomic.Int32
		var wg sync.WaitGroup
		for _, s := range []*Service{one, two} {
			wg.Add(1)
			go func(s *Service) {
				defer wg.Done()
				err := s.ReserveTracking(ctx, keyHashA, r)
				if err == nil {
					wins.Add(1)
				} else if errors.Is(err, ErrTrackingConflict) {
					conflicts.Add(1)
				} else {
					t.Error(err)
				}
			}(s)
		}
		wg.Wait()
		if wins.Load() != 1 || conflicts.Load() != 1 {
			t.Fatal("duplicate reservation admitted", wins.Load(), conflicts.Load())
		}
		if _, err := two.LookupTracking(ctx, keyHashB, r.ID); !errors.Is(err, ErrTrackingNotFound) {
			t.Fatal("other-key existence leaked", err)
		}
		if err := two.ReserveTracking(ctx, keyHashB, r); err != nil {
			t.Fatal("same UUID not independent across keys", err)
		}
		session := NewTrackingSession(r, time.Now)
		session.Phase("provider_attempt")
		session.Attempt(1)
		session.Finish("completed", "", true)
		terminal := session.Snapshot(false)
		if err := one.UpdateTracking(ctx, keyHashA, terminal); err != nil {
			t.Fatal(err)
		}
		// Repeated identical finish is safe metadata-only readback.
		if err := two.UpdateTracking(ctx, keyHashA, terminal); err != nil {
			t.Fatal(err)
		}
		changed := terminal
		changed.Status = "failed"
		if err := two.UpdateTracking(ctx, keyHashA, changed); !errors.Is(err, ErrTrackingNotFound) {
			t.Fatal("different terminal snapshot received false idempotent acknowledgement", err)
		}
		stale := r
		stale.Revision = terminal.Revision + 10
		if err := two.UpdateTracking(ctx, keyHashA, stale); !errors.Is(err, ErrTrackingNotFound) {
			t.Fatal("terminal revision overwrite accepted", err)
		}
		saved, err := two.LookupTracking(ctx, keyHashA, r.ID)
		if err != nil || saved.Status != "completed" || saved.Revision != terminal.Revision {
			t.Fatal("terminal outcome changed", err, saved)
		}
		wrong := terminal
		wrong.OwnerID = uuid.NewString()
		wrong.Revision++
		if err := two.UpdateTracking(ctx, keyHashA, wrong); !errors.Is(err, ErrTrackingNotFound) {
			t.Fatal("wrong invocation accepted", err)
		}
	})
	t.Run("expiry cleanup requested ID and old nonce", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		ctx := context.Background()
		now := time.Now().UTC()
		past := now.Add(-48 * time.Hour)
		var requested TrackingRecord
		for i := 0; i < 105; i++ {
			r, _ := NewTrackingRecord(uuid.NewString(), "/v1/responses", past, past.Add(time.Minute))
			// Insert through the same SQL with a historical clock; cleanup cannot remove
			// other historical rows until the real observation time advances.
			s.now = func() time.Time { return past }
			if err := s.ReserveTracking(ctx, keyHashA, r); err != nil {
				t.Fatal(err)
			}
			requested = r
		}
		live, _ := NewTrackingRecord(uuid.NewString(), "/v1/responses", now, now.Add(time.Minute))
		if err := s.ReserveTracking(ctx, keyHashA, live); err != nil {
			t.Fatal(err)
		}
		foreign, _ := NewTrackingRecord(uuid.NewString(), "/v1/responses", past, past.Add(time.Minute))
		if err := s.ReserveTracking(ctx, keyHashB, foreign); err != nil {
			t.Fatal(err)
		}
		s.now = time.Now
		if _, err := s.LookupTracking(ctx, keyHashA, requested.ID); !errors.Is(err, ErrTrackingNotFound) {
			t.Fatal("expired row visible before physical cleanup", err)
		}
		fresh, _ := NewTrackingRecord(requested.ID, "/v1/responses", now, now.Add(time.Minute))
		if err := s.ReserveTracking(ctx, keyHashA, fresh); err != nil {
			t.Fatal("requested expired ID blocked behind cleanup limit", err)
		}
		requested.Revision = 100
		if err := s.UpdateTracking(ctx, keyHashA, requested); !errors.Is(err, ErrTrackingNotFound) {
			t.Fatal("old nonce mutated replacement", err)
		}
		var n int
		if err := p.QueryRow(ctx, "SELECT count(*) FROM request_tracking WHERE api_key_hash=$1", keyHashA).Scan(&n); err != nil || n != 7 {
			t.Fatal("cleanup was not bounded to 100 expired rows", n, err)
		}
		if _, err := s.LookupTracking(ctx, keyHashA, live.ID); err != nil {
			t.Fatal("cleanup deleted live metadata", err)
		}
		if err := p.QueryRow(ctx, "SELECT count(*) FROM request_tracking WHERE api_key_hash=$1", keyHashB).Scan(&n); err != nil || n != 1 {
			t.Fatal("cleanup crossed key scope", n, err)
		}
	})
	t.Run("missing pool is unavailable", func(t *testing.T) {
		r, _ := NewTrackingRecord(uuid.NewString(), "/v1/responses", time.Now(), time.Now().Add(time.Minute))
		s := &Service{now: time.Now}
		if !errors.Is(s.ReserveTracking(context.Background(), keyHashA, r), ErrTrackingUnavailable) {
			t.Fatal("nil pool reservation accepted")
		}
		if _, err := s.LookupTracking(context.Background(), keyHashA, r.ID); !errors.Is(err, ErrTrackingUnavailable) {
			t.Fatal("nil pool lookup accepted")
		}
	})
	t.Run("schema constraints and expiry index", func(t *testing.T) {
		p, _ := referencePool(t, true)
		for _, column := range []string{"api_key_hash", "tracking_id", "owner_id", "endpoint", "status", "stage", "reason", "created_at", "deadline_at", "retention_expires_at", "updated_at", "heartbeat_at", "last_progress_at", "finished_at", "next_attempt_at", "error_code", "attempts_used", "revision", "outcome_confirmed"} {
			var exists bool
			if err := p.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='request_tracking' AND column_name=$1)", column).Scan(&exists); err != nil || !exists {
				t.Fatal("missing tracking column", column, err)
			}
		}
	})
}

func TestRequestTrackingQueryFailure(t *testing.T) {
	t.Parallel()
	p, _ := referencePool(t, true)
	s, _ := serviceFixture(t, p, nil)
	id := uuid.NewString()
	execSQL(t, p, "DROP TABLE request_tracking")
	if _, err := s.LookupTracking(context.Background(), keyHashA, id); !errors.Is(err, ErrTrackingUnavailable) {
		t.Fatal("query failure disguised as missing ID", err)
	}
}

func TestRequestTrackingUnknownCommit(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"found", "absent", "readback_failed", "other_owner"} {
		t.Run(kind, func(t *testing.T) {
			p, _ := referencePool(t, true)
			s, _ := serviceFixture(t, p, nil)
			r, _ := NewTrackingRecord(uuid.NewString(), "/v1/responses", time.Now(), time.Now().Add(time.Minute))
			inserts, reads := 0, 0
			opts := trackingWriteOptions{onInsert: func() { inserts++ }, onReadback: func() { reads++ }}
			opts.commit = func(ctx context.Context, tx pgx.Tx) error {
				if kind == "found" || kind == "readback_failed" {
					if err := tx.Commit(ctx); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := tx.Rollback(ctx); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "other_owner" {
					other := r
					other.OwnerID = uuid.NewString()
					if err := s.ReserveTracking(context.Background(), keyHashA, other); err != nil {
						t.Fatal(err)
					}
				}
				return errors.New("synthetic missing COMMIT acknowledgement")
			}
			if kind == "readback_failed" {
				opts.readbackError = errors.New("synthetic failed readback")
			}
			err := s.reserveTracking(context.Background(), keyHashA, r, opts)
			if kind == "found" {
				if err != nil {
					t.Fatal("exact committed owner was not acknowledged", err)
				}
			} else if !errors.Is(err, ErrTrackingUnavailable) {
				t.Fatal("unknown registration authorized inference", kind, err)
			}
			if inserts != 1 || reads != 1 {
				t.Fatal("uncertain registration replaced insert or repeated readback", inserts, reads)
			}
			var n int
			if err := p.QueryRow(context.Background(), "SELECT count(*) FROM request_tracking WHERE tracking_id=$1", r.ID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			want := 1
			if kind == "absent" {
				want = 0
			}
			if n != want {
				t.Fatal("registration count", n, want)
			}
		})
	}
}
