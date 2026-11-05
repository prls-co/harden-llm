//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-497

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prls-co/harden-llm/internal/integrationtest"
)

func TestAcceptedGenerationDefaultDoesNotRewriteOrConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, dsn := integrationtest.PostgresLease(t)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := ApplySchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := &PostgresStore{Pool: pool}
	scope := strings.Repeat("a", 64)
	for _, legacy := range []int64{0, 180000} {
		key := "legacy-explicit"
		if legacy == 0 {
			key = "legacy-zero"
		}
		old := []Item{{CustomID: "one", Generation: Generation{ModelID: "gpt-6-luna", TimeoutMS: legacy, ProviderOptions: map[string]any{"seed": json.Number("9007199254740993")}, Schema: []byte(`{"type":"object","properties":{"b":{"type":"string"},"a":{"type":"integer"}}}`)}}}
		accepted, err := store.Create(ctx, scope, key, old, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		current := append([]Item(nil), old...)
		current[0].Generation.TimeoutMS = DefaultGenerationBudget.Milliseconds()
		current[0].Generation.TimeoutDefaulted = true
		repeated, err := store.Create(ctx, scope, key, current, time.Hour)
		if err != nil || repeated.JobID != accepted.JobID {
			t.Fatal("a new default invalidated an accepted idempotent submission", legacy, err)
		}
		stored, err := store.Load(ctx, Ref{Scope: scope, JobID: accepted.JobID, Index: 0})
		if err != nil || stored.Item.Generation.TimeoutMS != legacy {
			t.Fatal("accepted snapshot silently rewritten", legacy, err)
		}
		current[0].Generation.TimeoutDefaulted = false
		if _, err := store.Create(ctx, scope, key, current, time.Hour); !errors.Is(err, ErrConflict) {
			t.Fatal("explicit different budget accepted under old idempotency key", legacy, err)
		}
		current[0].Generation.TimeoutDefaulted = true
		current[0].CustomID = "different"
		if _, err := store.Create(ctx, scope, key, current, time.Hour); !errors.Is(err, ErrConflict) {
			t.Fatal("default resolution concealed different input", err)
		}
		current[0].CustomID = "one"
		current[0].Generation.ProviderOptions = map[string]any{"seed": json.Number("9007199254740992")}
		if _, err := store.Create(ctx, scope, key, current, time.Hour); !errors.Is(err, ErrConflict) {
			t.Fatal("idempotency comparison rounded different JSON integers", err)
		}
	}
}
