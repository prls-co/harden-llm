//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-544.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestWaiterReadRetainsSafeSQLClass(t *testing.T) {
	t.Parallel()
	store, ref := ledgerFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// The standard lease owns a unique database. Exercise the actual pgx read
	// error rather than substituting a sentinel or touching a shared database.
	if _, err := store.Pool.Exec(ctx, "ALTER TABLE batch_request_waiters RENAME TO diagnostic_waiters"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := store.Pool.Exec(cleanup, "ALTER TABLE diagnostic_waiters RENAME TO batch_request_waiters"); err != nil {
			t.Error(err)
		}
	})
	_, err := store.ReadRequest(ctx, ref.Scope, uuid.NewString(), uuid.NewString(), true)
	if !errors.Is(err, ErrUnavailable) || MetadataErrorClass(err) != "postgres_42P01" || err.Error() != ErrUnavailable.Error() {
		t.Fatal("actual unavailable waiter read lost safe SQL class", err)
	}
}
