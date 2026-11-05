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

func TestQueueFIFOIndexMigrationPreservesAcceptedItems(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	job := attachQueueJob(t, s, strings.Repeat("b", 64), 1000)
	// Simulate the preceding schema, then apply the additive index through the
	// explicit schema command. Reapplication must preserve all accepted inputs.
	if _, err := s.Pool.Exec(ctx, "DROP INDEX batch_items_available_fifo"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ApplySchema(ctx, s.Pool); err != nil {
			t.Fatal("additive FIFO index migration", err)
		}
	}
	// Equal queue times exercise the stable item-index tie-breaker. Exclusions
	// and live permits remain authoritative even when the partial index is used.
	if _, err := s.Pool.Exec(ctx, "UPDATE batch_items SET queued_at='2026-01-01T00:00:00Z' WHERE job_id=$1", job.JobID); err != nil {
		t.Fatal(err)
	}
	reservation := permitFixture(Ref{Scope: job.Scope, JobID: job.JobID, Index: 0}, 1)
	if _, err := s.ReserveAttempt(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	page, err := s.SelectReady(ctx, QueueSelection{PoolID: "fixture-pool", Generation: 2, Limit: 128, Exclude: []Ref{{Scope: job.Scope, JobID: job.JobID, Index: 1}}})
	if err != nil || len(page.Items) != 128 {
		t.Fatal("large FIFO selection within unchanged transaction deadline", err, len(page.Items))
	}
	for i, item := range page.Items {
		if item.Ref.JobID != job.JobID || item.Ref.Index != i+2 {
			t.Fatal("FIFO/exclusion changed", i, item.Ref)
		}
	}
	var count int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM batch_items WHERE job_id=$1", job.JobID).Scan(&count); err != nil || count != 1000 {
		t.Fatal("schema/selection changed accepted items", err, count)
	}
	// A same-named but weaker operator-created index must not silently pass the
	// explicit shape verifier or delete accepted work during failed migration.
	if _, err := s.Pool.Exec(ctx, "DROP INDEX batch_items_available_fifo; CREATE INDEX batch_items_available_fifo ON batch_items(api_key_hash,job_id,item_index)"); err != nil {
		t.Fatal(err)
	}
	if err := ApplySchema(ctx, s.Pool); err == nil || !strings.Contains(err.Error(), "incompatible execution ledger index") {
		t.Fatal("incompatible FIFO index accepted", err)
	}
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM batch_items WHERE job_id=$1", job.JobID).Scan(&count); err != nil || count != 1000 {
		t.Fatal("failed schema command lost accepted inputs", err, count)
	}
}

func TestQueueMixedThousandWaitersKeepsBoundedFairPage(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	job := attachQueueJob(t, s, strings.Repeat("b", 64), 1000)
	// Bulk fixture setup isolates selection cost from enqueue latency. Every
	// waiter has the same valid scope/liveness/deadline contract as the API.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO batch_request_waiters(request_id,pool_id,generation,api_key_hash,owner_id,execution_id,dispatch_ordinal,created_at,deadline,budget_ms,live_until,retention_until)
SELECT gen_random_uuid(),'fixture-pool',2,$1,gen_random_uuid(),gen_random_uuid(),1,now(),now()+interval '1 minute',60000,now()+interval '10 seconds',now()+interval '1 minute 24 hours' FROM generate_series(1,1000)`, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	page, err := s.SelectReady(ctx, QueueSelection{PoolID: "fixture-pool", Generation: 2, Limit: 128})
	if err != nil || len(page.Items) != 128 {
		t.Fatal("mixed page exceeded unchanged metadata transaction deadline", err, len(page.Items))
	}
	ids := map[string]bool{}
	for i, item := range page.Items {
		if i%2 == 0 {
			if item.Kind != "ordinary" || ids[item.RequestID] {
				t.Fatal("ordinary rotation lost or repeated", i, item)
			}
			ids[item.RequestID] = true
		} else if item.Ref.JobID != job.JobID || item.Ref.Index != i/2 {
			t.Fatal("durable share/FIFO changed", i, item)
		}
	}
}

func TestQueuePageMatchesIterativeFairnessForUnevenOwners(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	type owner struct {
		job       JobRef
		remaining []int
		sequence  int64
	}
	owners := map[string][]owner{}
	scopes := []string{strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)}
	for scopeIndex, scope := range scopes {
		for jobIndex := 0; jobIndex < 3; jobIndex++ {
			count := 1 + (scopeIndex+2*jobIndex)%5
			job := attachQueueJob(t, s, scope, count)
			var sequence int64
			if err := s.Pool.QueryRow(ctx, "SELECT enqueue_sequence FROM batch_jobs WHERE id=$1", job.JobID).Scan(&sequence); err != nil {
				t.Fatal(err)
			}
			var indices []int
			for i := range count {
				indices = append(indices, i)
			}
			owners[scope] = append(owners[scope], owner{job, indices, sequence})
		}
	}
	for _, after := range []string{"", scopes[1], scopes[2]} {
		// The independent oracle executes the former one-item-at-a-time rule:
		// next nonempty scope, next nonempty owner, first remaining FIFO item.
		left := map[string][]owner{}
		cursors := map[string]int64{}
		for _, scope := range scopes {
			for _, o := range owners[scope] {
				o.remaining = append([]int(nil), o.remaining...)
				left[scope] = append(left[scope], o)
			}
			cursors[scope] = owners[scope][1].sequence
			if _, err := s.Pool.Exec(ctx, `INSERT INTO batch_pool_scope_cursors(pool_id,api_key_hash,last_job_sequence) VALUES('fixture-pool',$1,$2) ON CONFLICT(pool_id,api_key_hash) DO UPDATE SET last_job_sequence=EXCLUDED.last_job_sequence`, scope, cursors[scope]); err != nil {
				t.Fatal(err)
			}
		}
		var expected []Ref
		nextScope := after
		for len(expected) < 17 {
			chosen := ""
			for pass := range 2 {
				for _, scope := range scopes {
					if (pass == 0 && scope <= nextScope) || (pass == 1 && scope > nextScope) {
						continue
					}
					for _, o := range left[scope] {
						if len(o.remaining) > 0 {
							chosen = scope
							break
						}
					}
					if chosen != "" {
						break
					}
				}
				if chosen != "" {
					break
				}
			}
			if chosen == "" {
				break
			}
			selected := -1
			for pass := range 2 {
				for i, o := range left[chosen] {
					if len(o.remaining) == 0 || (pass == 0 && o.sequence <= cursors[chosen]) || (pass == 1 && o.sequence > cursors[chosen]) {
						continue
					}
					selected = i
					break
				}
				if selected >= 0 {
					break
				}
			}
			o := &left[chosen][selected]
			expected = append(expected, Ref{Scope: chosen, JobID: o.job.JobID, Index: o.remaining[0]})
			o.remaining = o.remaining[1:]
			cursors[chosen], nextScope = o.sequence, chosen
		}
		page, err := s.SelectReady(ctx, QueueSelection{PoolID: "fixture-pool", Generation: 2, AfterScope: after, Limit: 17})
		if err != nil || len(page.Items) != len(expected) || page.AfterScope != nextScope {
			t.Fatal("fair page shape/cursor changed", after, err, len(page.Items), len(expected))
		}
		for i, want := range expected {
			if page.Items[i].Ref != want {
				t.Fatal("set-based page changed iterative scope/owner/FIFO order", after, i, page.Items[i].Ref, want)
			}
		}
	}
}
