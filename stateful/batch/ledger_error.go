package batch

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Only safe facts cross the ledger boundary. Keep errors.Is(ErrUnavailable)
// without unwrapping the original driver error or changing retry decisions.
type unavailableError struct{ class string }

func (e *unavailableError) Error() string { return ErrUnavailable.Error() }
func (e *unavailableError) Unwrap() error { return ErrUnavailable }

// MetadataErrorClass is the one payload-free classification used by both
// admission recovery and terminal failure logs. It never inspects error text.
func MetadataErrorClass(err error) string {
	var unavailable *unavailableError
	if errors.As(err, &unavailable) {
		return unavailable.class
	}
	switch {
	case errors.Is(err, ErrClaimOwned):
		return "claim_not_available"
	case errors.Is(err, ErrClaimUnconfirmed):
		return "claim_unconfirmed"
	case errors.Is(err, ErrGenerationFenced):
		return "generation_fenced"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) && len(postgres.Code) == 5 {
		for _, c := range postgres.Code {
			if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z') {
				return "unavailable"
			}
		}
		return "postgres_" + postgres.Code
	}
	var connect *pgconn.ConnectError
	if errors.As(err, &connect) {
		return "postgres_connect"
	}
	return "unavailable"
}
