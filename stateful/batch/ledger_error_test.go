package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-543.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestLedgerErrorRetainsSafeClass(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		err   error
		class string
	}{
		{"SQLSTATE", &pgconn.PgError{Code: "57P01", Message: "driver-secret-sentinel", Detail: "SQL arguments and credentials"}, "postgres_57P01"},
		{"query cancellation", &pgconn.PgError{Code: "57014", Message: "private SQL statement"}, "postgres_57014"},
		{"deadline", context.DeadlineExceeded, "deadline"},
		{"cancellation", context.Canceled, "canceled"},
		{"unknown", errors.New("driver-secret-sentinel"), "unavailable"},
		{"invalid SQLSTATE", &pgconn.PgError{Code: "secret\nSQL", Message: "private"}, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ledgerError(fmt.Errorf("unsafe connection details: %w", tc.err))
			if !errors.Is(err, ErrUnavailable) || MetadataErrorClass(err) != tc.class {
				t.Fatalf("safe class lost: want=%s err=%v", tc.class, err)
			}
			if err.Error() != ErrUnavailable.Error() || errors.Is(err, tc.err) || ledgerError(err) != err {
				t.Fatal("raw driver cause escaped or repeated mapping lost safe metadata")
			}
		})
	}
	for _, sentinel := range []error{nil, ErrUnavailable, ErrNotFound, ErrConflict, ErrGenerationFenced, ErrClaimOwned, ErrClaimUnconfirmed, ErrSettlementConflict, ErrLegacyExecutionUnresolved} {
		if ledgerError(sentinel) != sentinel {
			t.Fatal("existing sentinel mapping changed", sentinel)
		}
	}
	if ledgerError(pgx.ErrNoRows) != ErrNotFound {
		t.Fatal("missing row mapping changed")
	}
}
