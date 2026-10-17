//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-477
import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"strings"
	"testing"
	"time"
)

func TestPostgresBatchIsolationIdempotencyAndImmutableResults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, dsn := integrationtest.PostgresLease(t)
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = ApplySchema(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err = ApplySchema(ctx, p); err != nil {
		t.Fatal(err)
	}
	s := &PostgresStore{Pool: p}
	scope := strings.Repeat("a", 64)
	items := []Item{{CustomID: "one", Generation: Generation{ModelID: "gpt-6-luna"}}, {CustomID: "two", Generation: Generation{ModelID: "gpt-6-luna"}}}
	j, err := s.Create(ctx, scope, "idempotency", items, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := s.Create(ctx, scope, "idempotency", items, time.Hour)
	if err != nil || j.JobID != j2.JobID {
		t.Fatalf("not idempotent: %v", err)
	}
	items[0].CustomID = "changed"
	if _, err = s.Create(ctx, scope, "idempotency", items, time.Hour); !errors.Is(err, ErrConflict) {
		t.Fatalf("key reuse: %v", err)
	}
	if _, err = s.Status(ctx, strings.Repeat("b", 64), j.JobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("scope leaked: %v", err)
	}
	r := Ref{Scope: scope, JobID: j.JobID, Index: 0}
	item, err := s.Begin(ctx, r)
	if err != nil || item.Attempt != 1 {
		t.Fatal(err)
	}
	if _, err = s.Begin(ctx, r); !errors.Is(err, ErrConflict) {
		t.Fatal("active generation dispatched twice")
	}
	if _, err = s.Record(ctx, r, Outcome{Kind: "rejected", Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	item, err = s.Begin(ctx, r)
	if err != nil || item.Attempt != 2 {
		t.Fatal(err)
	}
	if _, err = s.Record(ctx, r, Outcome{Kind: "completed", ArtifactID: "result.json", Attempt: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Record(ctx, r, Outcome{Kind: "failed", Error: &ItemError{Code: "late"}}); err != nil {
		t.Fatal(err)
	}
	item, err = s.Load(ctx, r)
	if err != nil || item.State != "succeeded" || item.Outcome.ArtifactID != "result.json" {
		t.Fatal("terminal result overwritten", err)
	}
	if err = s.Expire(ctx, j); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx, scope, j.JobID)
	if err != nil || status.Counts.Succeeded != 1 || status.Counts.Failed != 1 || status.Status != "completed_with_errors" {
		t.Fatalf("wrong terminal counts: %+v %v", status, err)
	}
}
