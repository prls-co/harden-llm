//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-522

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ledgerAcquireTrace struct {
	started chan struct{}
	queries atomic.Int32
}

func (s *ledgerAcquireTrace) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	select {
	case s.started <- struct{}{}:
	default:
	}
	return ctx
}
func (*ledgerAcquireTrace) TraceAcquireEnd(context.Context, *pgxpool.Pool, pgxpool.TraceAcquireEndData) {
}
func (s *ledgerAcquireTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	s.queries.Add(1)
	return ctx
}
func (*ledgerAcquireTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestLedgerPoolQueueDoesNotConsumeTransactionBudget(t *testing.T) {
	t.Parallel()
	original, _ := ledgerFixture(t)
	trace := &ledgerAcquireTrace{started: make(chan struct{}, 1)}
	cfg := original.Pool.Config()
	cfg.MaxConns = 1
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := &PostgresStore{Pool: pool}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	<-trace.started // Ignore the test-owned connection acquisition.
	type result struct {
		generation PoolGeneration
		err        error
	}
	done := make(chan result, 1)
	go func() { g, e := store.PoolGeneration(ctx, "fixture-pool"); done <- result{g, e} }()
	select {
	case <-trace.started:
	case <-ctx.Done():
		t.Fatal("metadata did not enter the actual pgx acquire queue")
	}
	// This timed hold is the assertion: queueing lasts longer than the 600 ms
	// SQL transaction budget. The pgx trace establishes the start boundary.
	timer := time.NewTimer(750 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	held.Release()
	out := <-done
	if out.err != nil || out.generation.Generation != 2 || out.generation.Mode != "active" {
		t.Fatal("connection queue consumed the SQL execution budget", out.err, out.generation)
	}

	held, err = pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	<-trace.started
	before := trace.queries.Load()
	short, stop := context.WithTimeout(ctx, 25*time.Millisecond)
	defer stop()
	if _, err := store.PoolGeneration(short, "fixture-pool"); err == nil || trace.queries.Load() != before {
		t.Fatal("connection queue ignored original deadline or entered SQL after cancellation", err, trace.queries.Load(), before)
	}
}
