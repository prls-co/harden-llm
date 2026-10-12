package stateful

import (
	"context"
	_ "embed"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

const storageTransactionTimeout = 600 * time.Millisecond
const storageStatementTimeout = 500 * time.Millisecond

//go:embed schema.sql
var referenceSchemaSQL string

type OperationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	cause   error
}

func (e *OperationError) Error() string { return e.Code + ": " + e.Message }
func (e *OperationError) Unwrap() error { return e.cause }

type SchemaVersionError struct{ Actual int }

func (*SchemaVersionError) Error() string {
	return "stateful: incompatible reference schema; operator action required"
}

// ApplySchema initializes a fresh database once. An incompatible existing
// schema is an operator error; serving never resets or converts application data.
func ApplySchema(ctx context.Context, pool *pgxpool.Pool) error {
	return withStorageTx(ctx, pool, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT to_regclass('hllm_schema_version') IS NOT NULL").Scan(&exists); err != nil {
			return err
		}
		if exists {
			var version int
			if err := tx.QueryRow(ctx, "SELECT version FROM hllm_schema_version WHERE singleton").Scan(&version); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return &SchemaVersionError{Actual: 0}
				}
				return err
			}
			if version != 1 {
				return &SchemaVersionError{Actual: version}
			}
			return nil
		}
		_, err := tx.Exec(ctx, referenceSchemaSQL)
		return err
	})
}

// withStorageTx owns a short SQL transaction. It is never used around provider
// or object-store I/O. One parent budget covers acquisition, statements and commit.
func withStorageTx(ctx context.Context, pool *pgxpool.Pool, options pgx.TxOptions, fn func(context.Context, pgx.Tx) error) error {
	if ctx == nil {
		return errors.New("stateful: context is required")
	}
	if pool == nil {
		return &OperationError{Code: "reference_configuration_error", Message: "Reference PostgreSQL is not configured."}
	}
	bounded, cancel := context.WithTimeout(ctx, storageTransactionTimeout)
	defer cancel()
	tx, err := pool.BeginTx(bounded, options)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), storageStatementTimeout)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(bounded, "SET LOCAL statement_timeout = '500ms'"); err != nil {
		return err
	}
	if err = fn(bounded, tx); err != nil {
		return err
	}
	return tx.Commit(bounded)
}
