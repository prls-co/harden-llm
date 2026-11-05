//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-518

import (
	"context"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestCPAAdmissionTrackingSchemaMigratesExactLegacyWithoutReset(t *testing.T) {
	t.Parallel()
	p, _ := referencePool(t, true)
	ctx := context.Background()
	execSQL(t, p, "DROP TABLE request_tracking")
	execSQL(t, p, trackingLegacySchemaSQL)
	execSQL(t, p, "CREATE TABLE admission_migration_sentinel(value text); INSERT INTO admission_migration_sentinel VALUES('preserved')")
	s, _ := serviceFixture(t, p, nil)
	now := time.Now().UTC()
	r, err := NewTrackingRecord(uuid.NewString(), "/v1/responses", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveTracking(ctx, keyHashA, r); err != nil {
		t.Fatal(err)
	}
	session := NewTrackingSession(r, time.Now)
	session.Wait("retry_backoff", time.Second)
	if err := s.UpdateTracking(ctx, keyHashA, session.Snapshot(false)); err != nil {
		t.Fatal(err)
	}
	before, err := s.LookupTracking(ctx, keyHashA, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ApplySchema(ctx, p); err != nil {
			t.Fatal("compatible admission migration failed", err)
		}
	}
	after, err := s.LookupTracking(ctx, keyHashA, r.ID)
	if err != nil || !sameTrackingSnapshot(before, after) {
		t.Fatal("admission migration rewrote accepted owner/status/deadline", err)
	}
	session.AdmissionWait("pool_capacity", nil)
	if err := s.UpdateTracking(ctx, keyHashA, session.Snapshot(false)); err != nil {
		t.Fatal("unknown admission ETA rejected", err)
	}
	got, err := s.LookupTracking(ctx, keyHashA, r.ID)
	if err != nil || got.Stage == nil || *got.Stage != "admission_wait" || got.NextAttemptAt != nil || got.Observe(time.Now()).RetryAfterMS != nil {
		t.Fatal("admission status invented an eligibility time", got, err)
	}
	var sentinel string
	if err := p.QueryRow(ctx, "SELECT value FROM admission_migration_sentinel").Scan(&sentinel); err != nil || sentinel != "preserved" {
		t.Fatal("migration changed unrelated data", sentinel, err)
	}
}
