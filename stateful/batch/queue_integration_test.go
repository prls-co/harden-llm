//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-503

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func attachQueueJob(t *testing.T, s *PostgresStore, scope string, count int) JobRef {
	t.Helper()
	items := make([]Item, count)
	for i := range items {
		items[i] = Item{CustomID: uuid.NewString(), Generation: Generation{ModelID: "gpt-6-luna", TimeoutMS: 300000, Messages: nil}}
	}
	job, err := s.Create(context.Background(), scope, uuid.NewString(), items, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachJob(context.Background(), "fixture-pool", 2, job); err != nil {
		t.Fatal("attach job to admission authority", err)
	}
	return job
}
func TestQueueScopeThenJobFairnessAndBoundedMetadata(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	first := attachQueueJob(t, s, a, 3)
	second := attachQueueJob(t, s, a, 3)
	third := attachQueueJob(t, s, b, 4)
	page, err := s.SelectReady(ctx, QueueSelection{PoolID: "fixture-pool", Generation: 2, Limit: 8})
	if err != nil || len(page.Items) != 8 {
		t.Fatal("bounded ready selection", err, len(page.Items))
	}
	for i, want := range []string{first.JobID, third.JobID, second.JobID, third.JobID, first.JobID, third.JobID, second.JobID, third.JobID} {
		if page.Items[i].Ref.JobID != want || page.Items[i].BudgetMS != 300000 {
			t.Fatal("more jobs bought a key more share or lost job rotation", i, page.Items[i])
		}
	}
	// The selector never consumes/reserves an item or loads a prompt into its return.
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "messages") || strings.Contains(string(raw), "model") || len(raw) > 4096 {
		t.Fatal("queue projection leaked execution input or unbounded state")
	}
	for _, job := range []JobRef{first, second, third} {
		status, err := s.Status(ctx, job.Scope, job.JobID)
		if err != nil || status.Counts.Queued != job.Count || status.Counts.Active != 0 {
			t.Fatal("selection minted capacity", err, status.Counts)
		}
	}
	// Carry the scope cursor through a new activity/process and SQL job cursors.
	page2, err := (&PostgresStore{Pool: s.Pool}).SelectReady(ctx, QueueSelection{PoolID: "fixture-pool", Generation: 2, AfterScope: page.AfterScope, Limit: 1})
	if err != nil || len(page2.Items) != 1 || page2.Items[0].Ref.JobID != first.JobID {
		t.Fatal("cursor did not survive activity/process handoff", err, page2)
	}
}
func TestQueueExcludesIntentsAndCurrentPermitsAndExpiredJobs(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	job := attachQueueJob(t, s, strings.Repeat("b", 64), 3)
	dead := attachQueueJob(t, s, strings.Repeat("c", 64), 1)
	if _, err := s.Pool.Exec(ctx, "UPDATE batch_jobs SET deadline=now()-interval '1 second' WHERE id=$1", dead.JobID); err != nil {
		t.Fatal(err)
	}
	reservation := permitFixture(Ref{Scope: job.Scope, JobID: job.JobID, Index: 0}, 1)
	if _, err := s.ReserveAttempt(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	page, err := s.SelectReady(ctx, QueueSelection{PoolID: "fixture-pool", Generation: 2, Limit: 64, Exclude: []Ref{{Scope: job.Scope, JobID: job.JobID, Index: 1}}})
	if err != nil || len(page.Items) != 1 || page.Items[0].Ref.Index != 2 || page.Items[0].Ref.JobID != job.JobID {
		t.Fatal("selector returned an expired/active/unpersisted intent", err, page)
	}
	if _, err := s.SelectReady(ctx, QueueSelection{PoolID: "fixture-pool", Generation: 1, Limit: 1}); !errors.Is(err, ErrGenerationFenced) {
		t.Fatal("stale authority selected queue", err)
	}
	if _, err := s.SelectReady(ctx, QueueSelection{PoolID: "fixture-pool", Generation: 2, Limit: 129}); err == nil {
		t.Fatal("unbounded page accepted")
	}
}
