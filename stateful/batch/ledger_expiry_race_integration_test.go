//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-498

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type expiryRowLockTrace struct{ entered chan uint32 }

func (s *expiryRowLockTrace) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, " FROM batch_attempts WHERE permit_id=$1 FOR UPDATE") {
		s.entered <- conn.PgConn().PID()
	}
	return ctx
}
func (*expiryRowLockTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestAttemptExpiryAndStaleClaimSerializeAtRealRowLock(t *testing.T) {
	t.Parallel()
	store, ref := ledgerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := permitFixture(ref, 1)
	r.GrantedAt, r.StartBy = time.Now().Add(-2*time.Second), time.Now().Add(-time.Second)
	if _, err := store.ReserveAttempt(ctx, r); err != nil {
		t.Fatal(err)
	}
	stale, err := store.ReadAttempts(ctx, r.PoolID, []string{r.PermitID})
	if err != nil || len(stale) != 1 || stale[0].State != "reserved" {
		t.Fatal("test did not retain a genuinely stale reservation snapshot", err, stale)
	}
	held, err := store.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(context.Background())
	var id string
	if err := held.QueryRow(ctx, `SELECT permit_id::text FROM batch_attempts WHERE permit_id=$1 FOR UPDATE`, r.PermitID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	trace := &expiryRowLockTrace{entered: make(chan uint32, 4)}
	cfg := store.Pool.Config()
	cfg.MaxConns, cfg.ConnConfig.Tracer = 2, trace
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	competitor := &PostgresStore{Pool: pool}
	// Establish each actual PostgreSQL lock wait before starting the next caller.
	// The short poll observes a lock boundary, not an assumed sleep duration.
	waitBlocked := func() {
		var pid uint32
		select {
		case pid = <-trace.entered:
		case <-ctx.Done():
			t.Fatal("competitor never entered the row-lock query", ctx.Err())
		}
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			var blocked bool
			if err := store.Pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("competitor was not observed waiting for its real row lock", ctx.Err())
			case <-tick.C:
			}
		}
	}
	type result struct {
		attempt Attempt
		err     error
	}
	expired, claimed := make(chan result, 1), make(chan result, 1)
	var owned sync.WaitGroup
	t.Cleanup(owned.Wait)
	owned.Go(func() { a, e := competitor.ExpireReservation(ctx, r.PermitID); expired <- result{a, e} })
	waitBlocked()
	owned.Go(func() {
		a, e := competitor.ClaimAttempt(ctx, stale[0].PermitID, uuid.NewString())
		claimed <- result{a, e}
	})
	waitBlocked()
	if err := held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	expiry, claim := <-expired, <-claimed
	if expiry.err != nil || expiry.attempt.State != "settled" || expiry.attempt.Observation == nil || expiry.attempt.Observation.Outcome.Kind != "never_dispatched" || expiry.attempt.StoppedAt == nil || !errors.Is(claim.err, ErrClaimOwned) {
		t.Fatal("stale blocked claim obtained execution after expiry", expiry.err, expiry.attempt, claim.err)
	}
	item, err := store.Load(ctx, ref)
	if err != nil || item.State != "queued" || item.Attempt != 0 {
		t.Fatal("expiry race consumed a provider attempt or lost queued work", err, item.State, item.Attempt)
	}
	next := permitFixture(ref, 2)
	if a, err := store.ReserveAttempt(ctx, next); err != nil || a.Ordinal != 2 {
		t.Fatal("expiry did not release the original fenced reservation", err, a.Ordinal)
	}
}
