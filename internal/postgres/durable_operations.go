package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var durableDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

var ErrDurableOperationConflict = errors.New("postgres: durable operation input conflicts with existing identity")
var ErrDurableOperationStateConflict = errors.New("postgres: durable operation status changed concurrently")

func (store *Store) AdmitDurableOperation(ctx context.Context, record DurableOperationRecord) (DurableOperationRecord, bool, error) {
	if err := validateDurableOperation(record); err != nil {
		return DurableOperationRecord{}, false, err
	}
	created := record.CreatedAt.UTC()
	updated := record.UpdatedAt.UTC()
	var current DurableOperationRecord
	err := store.pool.QueryRow(ctx, `
		INSERT INTO harden_operations
		(account_id, service_name, operation_id, input_digest, status, request, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (account_id, service_name, operation_id) DO NOTHING
		RETURNING account_id, service_name, operation_id, input_digest, status, request,
			result, error_code, error_message, run_id, trace_id, created_at, updated_at`,
		record.AccountID, record.ServiceName, record.OperationID, record.InputDigest, "accepted", record.Request, created, updated).Scan(
		&current.AccountID, &current.ServiceName, &current.OperationID, &current.InputDigest, &current.Status, &current.Request,
		&current.Result, &current.ErrorCode, &current.ErrorMessage, &current.RunID, &current.TraceID, &current.CreatedAt, &current.UpdatedAt,
	)
	if err == nil {
		return current, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return DurableOperationRecord{}, false, fmt.Errorf("postgres: admit durable operation: %w", err)
	}
	current, err = store.DurableOperation(ctx, record.AccountID, record.ServiceName, record.OperationID)
	if err != nil {
		return DurableOperationRecord{}, false, err
	}
	if current.InputDigest != record.InputDigest {
		return DurableOperationRecord{}, false, ErrDurableOperationConflict
	}
	return current, false, nil
}

func (store *Store) DurableOperation(ctx context.Context, accountID, serviceName, operationID string) (DurableOperationRecord, error) {
	var record DurableOperationRecord
	err := store.pool.QueryRow(ctx, `
		SELECT account_id, service_name, operation_id, input_digest, status, request,
			result, error_code, error_message, run_id, trace_id, created_at, updated_at
		FROM harden_operations
		WHERE account_id=$1 AND service_name=$2 AND operation_id=$3`, accountID, serviceName, operationID).Scan(
		&record.AccountID, &record.ServiceName, &record.OperationID, &record.InputDigest, &record.Status, &record.Request,
		&record.Result, &record.ErrorCode, &record.ErrorMessage, &record.RunID, &record.TraceID, &record.CreatedAt, &record.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return DurableOperationRecord{}, ErrNotFound
	}
	if err != nil {
		return DurableOperationRecord{}, fmt.Errorf("postgres: load durable operation: %w", err)
	}
	return record, nil
}

func (store *Store) UpdateDurableOperation(ctx context.Context, record DurableOperationRecord) error {
	if err := validateDurableOperation(record); err != nil {
		return err
	}
	result, err := store.pool.Exec(ctx, `
		UPDATE harden_operations
		SET status=$4, result=$5, error_code=$6, error_message=$7, run_id=$8, trace_id=$9, updated_at=$10
		WHERE account_id=$1 AND service_name=$2 AND operation_id=$3 AND input_digest=$11`,
		record.AccountID, record.ServiceName, record.OperationID, record.Status, nullableJSON(record.Result), nullableText(record.ErrorCode), nullableText(record.ErrorMessage), nullableText(record.RunID), nullableText(record.TraceID), record.UpdatedAt.UTC(), record.InputDigest)
	if err != nil {
		return fmt.Errorf("postgres: update durable operation: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// UpdateDurableOperationFromStatus publishes a transition only while the
// operation still has the status observed by the caller.  Durable provider
// work can finish after a customer cancellation, so terminal publication must
// be fenced at the database boundary rather than relying on a preceding read.
func (store *Store) UpdateDurableOperationFromStatus(ctx context.Context, record DurableOperationRecord, expectedStatus string) error {
	if err := validateDurableOperation(record); err != nil {
		return err
	}
	if strings.TrimSpace(expectedStatus) == "" {
		return errors.New("postgres: expected durable operation status is required")
	}
	result, err := store.pool.Exec(ctx, `
		UPDATE harden_operations
		SET status=$4, result=$5, error_code=$6, error_message=$7, run_id=$8, trace_id=$9, updated_at=$10
		WHERE account_id=$1 AND service_name=$2 AND operation_id=$3 AND input_digest=$11 AND status=$12`,
		record.AccountID, record.ServiceName, record.OperationID, record.Status, nullableJSON(record.Result), nullableText(record.ErrorCode), nullableText(record.ErrorMessage), nullableText(record.RunID), nullableText(record.TraceID), record.UpdatedAt.UTC(), record.InputDigest, expectedStatus)
	if err != nil {
		return fmt.Errorf("postgres: compare-and-swap durable operation: %w", err)
	}
	if result.RowsAffected() != 1 {
		current, lookupErr := store.DurableOperation(ctx, record.AccountID, record.ServiceName, record.OperationID)
		if errors.Is(lookupErr, ErrNotFound) {
			return ErrNotFound
		}
		if lookupErr != nil {
			return lookupErr
		}
		if current.InputDigest != record.InputDigest {
			return ErrDurableOperationConflict
		}
		return ErrDurableOperationStateConflict
	}
	return nil
}

func (store *Store) CancelDurableOperation(ctx context.Context, accountID, serviceName, operationID string, now time.Time) (DurableOperationRecord, error) {
	var record DurableOperationRecord
	err := store.pool.QueryRow(ctx, `
		UPDATE harden_operations
		SET status = CASE WHEN status IN ('accepted','running') THEN 'cancelled' ELSE status END, updated_at=$4
		WHERE account_id=$1 AND service_name=$2 AND operation_id=$3
		RETURNING account_id, service_name, operation_id, input_digest, status, request,
			result, error_code, error_message, run_id, trace_id, created_at, updated_at`, accountID, serviceName, operationID, now.UTC()).Scan(
		&record.AccountID, &record.ServiceName, &record.OperationID, &record.InputDigest, &record.Status, &record.Request,
		&record.Result, &record.ErrorCode, &record.ErrorMessage, &record.RunID, &record.TraceID, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return DurableOperationRecord{}, ErrNotFound
	}
	if err != nil {
		return DurableOperationRecord{}, fmt.Errorf("postgres: cancel durable operation: %w", err)
	}
	return record, nil
}

func validateDurableOperation(record DurableOperationRecord) error {
	for label, value := range map[string]struct {
		value string
		max   int
	}{
		"account ID":   {value: record.AccountID, max: 128},
		"service name": {value: record.ServiceName, max: 64},
		"operation ID": {value: record.OperationID, max: 128},
	} {
		if strings.TrimSpace(value.value) == "" || len(value.value) > value.max {
			return fmt.Errorf("postgres: durable operation %s is invalid", label)
		}
	}
	var requestObject map[string]any
	if !durableDigestPattern.MatchString(record.InputDigest) || len(record.Request) == 0 || !json.Valid(record.Request) || json.Unmarshal(record.Request, &requestObject) != nil || requestObject == nil {
		return errors.New("postgres: durable operation input is invalid")
	}
	if record.Status != "accepted" && record.Status != "running" && record.Status != "succeeded" && record.Status != "failed" && record.Status != "cancelled" && record.Status != "unresolved" {
		return errors.New("postgres: durable operation status is invalid")
	}
	if record.CreatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) {
		return errors.New("postgres: durable operation timestamps are invalid")
	}
	return nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
