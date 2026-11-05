//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-510

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func legacyHandoffFixture(t *testing.T) (*PostgresStore, JobRef) {
	t.Helper()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	if err := s.EnsurePool(ctx, "legacy", 1); err != nil {
		t.Fatal(err)
	}
	job, err := s.Create(ctx, strings.Repeat("b", 64), uuid.NewString(), []Item{{CustomID: "legacy-zero", Generation: Generation{ModelID: "gpt-6-luna"}}, {CustomID: "legacy-explicit", Generation: Generation{ModelID: "gpt-6-luna", TimeoutMS: 180000}}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachJob(ctx, "legacy", 1, job); err != nil {
		t.Fatal(err)
	}
	return s, job
}
func TestLegacyHandoffBlocksActiveAndPreservesAcceptedBudgets(t *testing.T) {
	t.Parallel()
	s, job := legacyHandoffFixture(t)
	ctx := context.Background()
	ref := Ref{Scope: job.Scope, JobID: job.JobID, Index: 0}
	if _, err := s.Begin(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdoptLegacyPool(ctx, "legacy"); !errors.Is(err, ErrGenerationFenced) {
		t.Fatal("active legacy call did not block authority transfer", err)
	}
	finished := time.Now().UTC()
	if _, err := s.Record(ctx, ref, Outcome{Kind: "completed", ArtifactID: "legacy-result.json", FinishedAt: &finished}); err != nil {
		t.Fatal(err)
	}
	fence, err := s.AdoptLegacyPool(ctx, "legacy")
	if err != nil || fence.Generation != 2 || fence.PoolID != "legacy" {
		t.Fatal("drained legacy pool did not advance", fence, err)
	}
	repeated, err := s.AdoptLegacyPool(ctx, "legacy")
	if err != nil || repeated != fence {
		t.Fatal("repeated handoff reset authority", repeated, err)
	}
	a, err := s.Load(ctx, ref)
	if err != nil || a.Item.Generation.TimeoutMS != 0 || a.State != "succeeded" || a.Outcome.ArtifactID != "legacy-result.json" {
		t.Fatal("handoff changed accepted snapshot or artifact", a, err)
	}
	ref.Index = 1
	b, err := s.Load(ctx, ref)
	if err != nil || b.Item.Generation.TimeoutMS != 180000 || b.State != "queued" {
		t.Fatal("handoff lost queued legacy snapshot", b, err)
	}
	p := permitFixture(ref, 1)
	p.PoolID = "legacy"
	p.Generation = 1
	p.BudgetMS = 180000
	if _, err := s.ReserveAttempt(ctx, p); !errors.Is(err, ErrGenerationFenced) {
		t.Fatal("obsolete generation started after handoff", err)
	}
}
func TestLegacyHandoffDoesNotAssumeFailedActivityTransportStopped(t *testing.T) {
	t.Parallel()
	s, job := legacyHandoffFixture(t)
	ctx := context.Background()
	ref := Ref{Scope: job.Scope, JobID: job.JobID, Index: 0}
	if _, err := s.Begin(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Record(ctx, ref, Outcome{Kind: "failed", Error: &ItemError{Code: "activity_failed", Message: "Synthetic missing worker evidence."}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdoptLegacyPool(ctx, "legacy"); !errors.Is(err, ErrLegacyExecutionUnresolved) {
		t.Fatal("SDK activity failure became local-stop evidence", err)
	}
	generation, err := s.PoolGeneration(ctx, "legacy")
	if err != nil || generation.Generation != 1 || generation.Mode != "active" {
		t.Fatal("rejected handoff mutated authority", generation, err)
	}
}

func TestLegacyBootstrapNeverResetsExistingAuthorityOrWork(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	if err := s.CheckEmptyPoolBootstrap(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatal("bootstrap reset existing metadata", err)
	}
}
