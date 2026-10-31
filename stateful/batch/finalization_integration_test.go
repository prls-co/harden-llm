//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-513

import (
	"context"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestBatchExpiryCannotOverwriteClaimedAttempt(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	ctx := context.Background()
	row, err := s.Load(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	job := JobRef{Scope: ref.Scope, JobID: ref.JobID, Count: 1, Deadline: row.Deadline}
	r := permitFixture(ref, 1)
	nonce := uuid.NewString()
	if _, err := s.ReserveAttempt(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimAttempt(ctx, r.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDispatch(ctx, r.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	if err := s.Expire(ctx, job); err != nil {
		t.Fatal(err)
	}
	stillOwned, err := s.Load(ctx, ref)
	if err != nil || stillOwned.State != "active" {
		t.Fatal("job expiry overwrote exclusive attempt ownership", stillOwned.State, err)
	}
	finished := time.Now().UTC()
	o := AttemptObservation{Outcome: Outcome{Kind: "completed", ArtifactID: "before-deadline.json", FinishedAt: &finished}, NativeEntered: true, LocalStopped: true, ObservedAt: finished}
	settled, err := s.SettleAttempt(ctx, r.PermitID, nonce, o)
	if err != nil || settled.Counts.Succeeded != 1 {
		t.Fatal("bounded completion could not settle after job expiry command", settled, err)
	}
}
func TestBatchStatusSeparatesExecutionAndFinalizationDeadlines(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	ctx := context.Background()
	job := JobRef{Scope: ref.Scope, JobID: ref.JobID}
	if err := s.AttachJob(ctx, "fixture-pool", 2, job); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx, ref.Scope, ref.JobID)
	if err != nil || status.ExecutionDeadline == nil || status.FinalizationDeadline == nil || !status.ExecutionDeadline.Equal(status.Deadline) || !status.FinalizationDeadline.Equal(status.Deadline.Add(30*time.Second)) {
		t.Fatal("execution deadline was hidden or extended by cleanup allowance", status, err)
	}
}
