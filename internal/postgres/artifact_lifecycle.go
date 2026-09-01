package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const artifactOwnerLockSalt int64 = 1095914578
const artifactReconcileLock int64 = 0x484c4c4d415254

func (store *Store) WithArtifactReconcileLock(ctx context.Context, work func(context.Context) error) (bool, error) {
	if store == nil || store.pool == nil || work == nil {
		return false, errors.New("postgres: artifact reconciliation is not initialized")
	}
	connection, err := store.pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("postgres: acquire artifact reconciliation connection: %w", err)
	}
	defer connection.Release()
	var acquired bool
	if err := connection.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, artifactReconcileLock).Scan(&acquired); err != nil {
		return false, fmt.Errorf("postgres: acquire artifact reconciliation lock: %w", err)
	}
	if !acquired {
		return false, nil
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = connection.Exec(unlockContext, `SELECT pg_advisory_unlock($1)`, artifactReconcileLock)
	}()
	return true, work(ctx)
}

// ArtifactOperationID deterministically binds an operation to immutable typed
// identity and integrity facts. Object-key parsing is deliberately excluded.
func ArtifactOperationID(action string, artifact ArtifactRecord) string {
	values := []string{
		action, artifact.OwnerID, artifact.RunID, artifact.TraceID, artifact.ID,
		artifact.Kind, artifact.ObjectKey, artifact.ContentType,
		strings.ToLower(artifact.SHA256), fmt.Sprintf("%d", artifact.SizeBytes),
	}
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(digest[:])
}

func (store *Store) BeginArtifactPublication(ctx context.Context, operation ArtifactOperation) (ArtifactOperation, error) {
	if err := validateArtifactOperation(operation, "publish"); err != nil {
		return ArtifactOperation{}, err
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO llm_artifact_operations (
			operation_id, batch_id, action, state, owner_id, run_id, trace_id, artifact_id, kind,
			object_key, content_type, sha256, size_bytes, attempt_count, next_attempt_at,
			error_category, created_at, updated_at
		) VALUES ($1,NULL,'publish','pending',$2,$3,$4,$5,$6,$7,$8,$9,$10,0,$11,'',$12,$13)
		ON CONFLICT (operation_id) DO NOTHING`,
		operation.ID, operation.OwnerID, operation.RunID, operation.TraceID, operation.ArtifactID,
		operation.Kind, operation.ObjectKey, operation.ContentType, strings.ToLower(operation.SHA256),
		operation.SizeBytes, operation.NextAttemptAt, operation.CreatedAt, operation.UpdatedAt)
	if err != nil {
		return ArtifactOperation{}, fmt.Errorf("postgres: begin artifact publication: %w", err)
	}
	stored, err := store.ArtifactOperation(ctx, operation.ID)
	if err != nil {
		return ArtifactOperation{}, err
	}
	if !sameArtifactOperation(stored, operation) || stored.Action != "publish" {
		return ArtifactOperation{}, errors.New("postgres: artifact operation identity conflict")
	}
	return stored, nil
}

func (store *Store) ArtifactOperation(ctx context.Context, operationID string) (ArtifactOperation, error) {
	var operation ArtifactOperation
	var batchID *string
	err := store.pool.QueryRow(ctx, `
		SELECT operation_id, batch_id, action, state, owner_id, run_id, trace_id, artifact_id, kind,
			object_key, content_type, sha256, size_bytes, attempt_count, next_attempt_at,
			error_category, created_at, updated_at
		FROM llm_artifact_operations WHERE operation_id=$1`, operationID).Scan(
		&operation.ID, &batchID, &operation.Action, &operation.State, &operation.OwnerID,
		&operation.RunID, &operation.TraceID, &operation.ArtifactID, &operation.Kind,
		&operation.ObjectKey, &operation.ContentType, &operation.SHA256, &operation.SizeBytes,
		&operation.AttemptCount, &operation.NextAttemptAt, &operation.ErrorCategory,
		&operation.CreatedAt, &operation.UpdatedAt)
	if err != nil {
		return ArtifactOperation{}, notFound(err)
	}
	if batchID != nil {
		operation.BatchID = *batchID
	}
	return operation, nil
}

func (store *Store) MarkArtifactOperationApplied(ctx context.Context, operationID string, now time.Time) error {
	result, err := store.pool.Exec(ctx, `
		UPDATE llm_artifact_operations
		SET state='object_applied', attempt_count=attempt_count+1, next_attempt_at=$2, error_category='', updated_at=$2
		WHERE operation_id=$1 AND state IN ('pending','object_applied')`, operationID, now)
	if err != nil {
		return fmt.Errorf("postgres: mark artifact operation applied: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("postgres: artifact operation is not applicable")
	}
	return nil
}

func (store *Store) RecordArtifactOperationFailure(ctx context.Context, operationID, category string, retryAt, now time.Time) error {
	if category == "" || len(category) > 64 {
		category = "unknown"
	}
	result, err := store.pool.Exec(ctx, `
		UPDATE llm_artifact_operations
		SET attempt_count=attempt_count+1, next_attempt_at=$2, error_category=$3, updated_at=$4
		WHERE operation_id=$1 AND state IN ('pending','object_applied')`, operationID, retryAt, category, now)
	if err != nil {
		return fmt.Errorf("postgres: record artifact operation failure: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("postgres: artifact operation is not retryable")
	}
	return nil
}

func (store *Store) CompleteArtifactOperation(ctx context.Context, operationID string, failed bool, now time.Time) error {
	state := "completed"
	if failed {
		state = "failed"
	}
	result, err := store.pool.Exec(ctx, `
		UPDATE llm_artifact_operations SET state=$2, completed_at=$3, updated_at=$3
		WHERE operation_id=$1 AND state IN ('pending','object_applied')`, operationID, state, now)
	if err != nil {
		return fmt.Errorf("postgres: complete artifact operation: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("postgres: artifact operation is not completable")
	}
	return nil
}

func (store *Store) PendingArtifactOperations(ctx context.Context, now time.Time, limit int) ([]ArtifactOperation, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("postgres: artifact operation limit is invalid")
	}
	rows, err := store.pool.Query(ctx, `
		SELECT operation_id FROM llm_artifact_operations
		WHERE state IN ('pending','object_applied') AND next_attempt_at <= $1
		ORDER BY next_attempt_at, created_at, operation_id LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list pending artifact operations: %w", err)
	}
	defer rows.Close()
	var identifiers []string
	for rows.Next() {
		var identifier string
		if err := rows.Scan(&identifier); err != nil {
			return nil, fmt.Errorf("postgres: scan pending artifact operation: %w", err)
		}
		identifiers = append(identifiers, identifier)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	operations := make([]ArtifactOperation, 0, len(identifiers))
	for _, identifier := range identifiers {
		operation, err := store.ArtifactOperation(ctx, identifier)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, nil
}

func (store *Store) ArtifactOperationBacklog(ctx context.Context, now time.Time) (ArtifactOperationBacklog, error) {
	var backlog ArtifactOperationBacklog
	var oldestSeconds float64
	err := store.pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(GREATEST(EXTRACT(EPOCH FROM ($1 - MIN(created_at))), 0), 0)
		FROM llm_artifact_operations WHERE state IN ('pending','object_applied')`, now).Scan(
		&backlog.Pending, &oldestSeconds)
	if err != nil {
		return ArtifactOperationBacklog{}, fmt.Errorf("postgres: inspect artifact operation backlog: %w", err)
	}
	backlog.OldestAge = time.Duration(oldestSeconds * float64(time.Second))
	return backlog, nil
}

func (store *Store) AvailableArtifactsForAudit(ctx context.Context, verifiedBefore time.Time, limit int) ([]ArtifactRecord, error) {
	if verifiedBefore.IsZero() || limit < 1 || limit > 1000 {
		return nil, errors.New("postgres: artifact audit limit is invalid")
	}
	rows, err := store.pool.Query(ctx, `
		SELECT a.owner_id, COALESCE(t.run_id, ''), a.trace_id, a.artifact_id, a.kind,
			a.object_key, a.content_type, a.sha256, a.size_bytes, a.state, a.created_at, a.updated_at
		FROM llm_artifacts a JOIN llm_traces t ON t.owner_id=a.owner_id AND t.trace_id=a.trace_id
		WHERE a.state='available' AND a.verified_at <= $1
		ORDER BY a.verified_at, a.owner_id, a.trace_id, a.artifact_id LIMIT $2`, verifiedBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list artifacts for integrity audit: %w", err)
	}
	defer rows.Close()
	records := make([]ArtifactRecord, 0, limit)
	for rows.Next() {
		var record ArtifactRecord
		if err := rows.Scan(&record.OwnerID, &record.RunID, &record.TraceID, &record.ID, &record.Kind,
			&record.ObjectKey, &record.ContentType, &record.SHA256, &record.SizeBytes,
			&record.State, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan artifact integrity audit: %w", err)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (store *Store) RecordArtifactIntegrity(ctx context.Context, artifact ArtifactRecord, available bool, now time.Time) (bool, error) {
	state := "unavailable"
	if available {
		state = "available"
	}
	result, err := store.pool.Exec(ctx, `
		UPDATE llm_artifacts SET state=$10, verified_at=$11, updated_at=CASE WHEN state=$10 THEN updated_at ELSE $11 END
		WHERE owner_id=$1 AND trace_id=$2 AND artifact_id=$3 AND kind=$4 AND object_key=$5
		AND content_type=$6 AND sha256=$7 AND size_bytes=$8 AND state=$9`,
		artifact.OwnerID, artifact.TraceID, artifact.ID, artifact.Kind, artifact.ObjectKey,
		artifact.ContentType, strings.ToLower(artifact.SHA256), artifact.SizeBytes,
		"available", state, now)
	if err != nil {
		return false, fmt.Errorf("postgres: record artifact integrity: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func (store *Store) ArtifactMetadataMatches(ctx context.Context, operation ArtifactOperation) (bool, error) {
	var matches bool
	err := store.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM llm_artifacts a
		JOIN llm_traces t ON t.owner_id=a.owner_id AND t.trace_id=a.trace_id
		WHERE a.owner_id=$1 AND t.run_id=$2 AND a.trace_id=$3 AND a.artifact_id=$4
		AND a.kind=$5 AND a.object_key=$6 AND a.content_type=$7 AND a.sha256=$8 AND a.size_bytes=$9
		AND a.state='available'
	)`, operation.OwnerID, operation.RunID, operation.TraceID, operation.ArtifactID,
		operation.Kind, operation.ObjectKey, operation.ContentType, operation.SHA256, operation.SizeBytes).Scan(&matches)
	if err != nil {
		return false, fmt.Errorf("postgres: inspect artifact metadata: %w", err)
	}
	return matches, nil
}

func (store *Store) BeginExecutionArtifactDeletion(ctx context.Context, batchID, ownerID, runID, traceID string, now time.Time) (ArtifactDeleteBatch, error) {
	return store.beginArtifactDeletion(ctx, batchID, ownerID, "execution", runID, traceID, "", now)
}

func (store *Store) BeginOwnerArtifactDeletion(ctx context.Context, batchID, ownerID string, now time.Time) (ArtifactDeleteBatch, error) {
	return store.beginArtifactDeletion(ctx, batchID, ownerID, "owner", "", "", "", now)
}

func (store *Store) BeginReconciledTraceDeletion(ctx context.Context, batchID, ownerID, runID, traceID, fingerprint string, now time.Time) (ArtifactDeleteBatch, error) {
	return store.beginArtifactDeletion(ctx, batchID, ownerID, "reconciliation", runID, traceID, fingerprint, now)
}

func (store *Store) beginArtifactDeletion(ctx context.Context, batchID, ownerID, scope, runID, traceID, fingerprint string, now time.Time) (ArtifactDeleteBatch, error) {
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ArtifactDeleteBatch{}, fmt.Errorf("postgres: begin artifact deletion: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, $2))`, ownerID, artifactOwnerLockSalt); err != nil {
		return ArtifactDeleteBatch{}, fmt.Errorf("postgres: lock artifact owner: %w", err)
	}
	existing, err := activeDeleteBatch(ctx, transaction, ownerID, scope, runID, traceID)
	if err == nil {
		return existing, transaction.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ArtifactDeleteBatch{}, err
	}
	if scope == "execution" {
		var found bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM llm_runs WHERE owner_id=$1 AND run_id=$2 AND trace_id=$3
		)`, ownerID, runID, traceID).Scan(&found); err != nil || !found {
			if err != nil {
				return ArtifactDeleteBatch{}, fmt.Errorf("postgres: inspect execution deletion: %w", err)
			}
			return ArtifactDeleteBatch{}, ErrNotFound
		}
	}
	if scope == "reconciliation" {
		candidate, candidateErr := loadRunlessTraceCandidate(ctx, transaction, ownerID, traceID, true)
		if candidateErr != nil {
			return ArtifactDeleteBatch{}, candidateErr
		}
		if fingerprint == "" || candidate.Fingerprint != fingerprint {
			return ArtifactDeleteBatch{}, errors.New("postgres: runless trace changed after reconciliation plan")
		}
	}
	artifacts, err := deletionArtifacts(ctx, transaction, ownerID, scope, runID, traceID)
	if err != nil {
		return ArtifactDeleteBatch{}, err
	}
	_, err = transaction.Exec(ctx, `
		INSERT INTO llm_artifact_delete_batches
			(batch_id, owner_id, scope, run_id, trace_id, state, expected_artifact_count, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,'pending',$6,$7,$7)`,
		batchID, ownerID, scope, nullableString(runID), nullableString(traceID), len(artifacts), now)
	if err != nil {
		return ArtifactDeleteBatch{}, fmt.Errorf("postgres: create artifact deletion batch: %w", err)
	}
	operations := make([]ArtifactOperation, 0, len(artifacts))
	for _, artifact := range artifacts {
		operationID := ArtifactOperationID("delete", artifact)
		_, err := transaction.Exec(ctx, `
			INSERT INTO llm_artifact_operations (
				operation_id, batch_id, action, state, owner_id, run_id, trace_id, artifact_id, kind,
				object_key, content_type, sha256, size_bytes, next_attempt_at, created_at, updated_at
			) VALUES ($1,$2,'delete','pending',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12,$12)`,
			operationID, batchID, artifact.OwnerID, artifact.RunID, artifact.TraceID, artifact.ID,
			artifact.Kind, artifact.ObjectKey, artifact.ContentType, artifact.SHA256, artifact.SizeBytes, now)
		if err != nil {
			return ArtifactDeleteBatch{}, fmt.Errorf("postgres: create artifact deletion operation: %w", err)
		}
		if _, err := transaction.Exec(ctx, `
			UPDATE llm_artifacts SET state='deleting', updated_at=$4
			WHERE owner_id=$1 AND trace_id=$2 AND artifact_id=$3 AND state='available'`,
			artifact.OwnerID, artifact.TraceID, artifact.ID, now); err != nil {
			return ArtifactDeleteBatch{}, fmt.Errorf("postgres: hide deleting artifact: %w", err)
		}
		operations = append(operations, ArtifactOperation{
			ID: operationID, BatchID: batchID, Action: "delete", State: "pending",
			OwnerID: artifact.OwnerID, RunID: artifact.RunID, TraceID: artifact.TraceID,
			ArtifactID: artifact.ID, Kind: artifact.Kind, ObjectKey: artifact.ObjectKey,
			ContentType: artifact.ContentType, SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes,
			NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := transaction.Commit(ctx); err != nil {
		return ArtifactDeleteBatch{}, fmt.Errorf("postgres: commit artifact deletion intent: %w", err)
	}
	return ArtifactDeleteBatch{
		ID: batchID, OwnerID: ownerID, Scope: scope, RunID: runID, TraceID: traceID,
		State: "pending", ExpectedArtifactCount: len(operations), CreatedAt: now, UpdatedAt: now,
		Operations: operations,
	}, nil
}

func (store *Store) ArtifactDeleteBatch(ctx context.Context, batchID string) (ArtifactDeleteBatch, error) {
	var batch ArtifactDeleteBatch
	var runID, traceID *string
	err := store.pool.QueryRow(ctx, `
		SELECT batch_id, owner_id, scope, run_id, trace_id, state, expected_artifact_count,
			deleted_run_count, created_at, updated_at
		FROM llm_artifact_delete_batches WHERE batch_id=$1`, batchID).Scan(
		&batch.ID, &batch.OwnerID, &batch.Scope, &runID, &traceID, &batch.State,
		&batch.ExpectedArtifactCount, &batch.DeletedRunCount, &batch.CreatedAt, &batch.UpdatedAt)
	if err != nil {
		return ArtifactDeleteBatch{}, notFound(err)
	}
	if runID != nil {
		batch.RunID = *runID
	}
	if traceID != nil {
		batch.TraceID = *traceID
	}
	rows, err := store.pool.Query(ctx, `SELECT operation_id FROM llm_artifact_operations WHERE batch_id=$1 ORDER BY operation_id`, batchID)
	if err != nil {
		return ArtifactDeleteBatch{}, fmt.Errorf("postgres: list deletion batch operations: %w", err)
	}
	defer rows.Close()
	var operationIDs []string
	for rows.Next() {
		var operationID string
		if err := rows.Scan(&operationID); err != nil {
			return ArtifactDeleteBatch{}, err
		}
		operationIDs = append(operationIDs, operationID)
	}
	for _, operationID := range operationIDs {
		operation, err := store.ArtifactOperation(ctx, operationID)
		if err != nil {
			return ArtifactDeleteBatch{}, err
		}
		batch.Operations = append(batch.Operations, operation)
	}
	return batch, nil
}

func (store *Store) FinalizeArtifactDeleteBatch(ctx context.Context, batchID string, now time.Time) (int64, error) {
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("postgres: begin artifact deletion finalization: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	var ownerID, scope string
	var runID, traceID *string
	var state string
	if err := transaction.QueryRow(ctx, `
		SELECT owner_id, scope, run_id, trace_id, state FROM llm_artifact_delete_batches WHERE batch_id=$1 FOR UPDATE`, batchID).Scan(
		&ownerID, &scope, &runID, &traceID, &state); err != nil {
		return 0, notFound(err)
	}
	if state == "completed" {
		var count int64
		if err := transaction.QueryRow(ctx, `SELECT deleted_run_count FROM llm_artifact_delete_batches WHERE batch_id=$1`, batchID).Scan(&count); err != nil {
			return 0, err
		}
		return count, transaction.Commit(ctx)
	}
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, $2))`, ownerID, artifactOwnerLockSalt); err != nil {
		return 0, fmt.Errorf("postgres: lock artifact owner: %w", err)
	}
	var incomplete int
	if err := transaction.QueryRow(ctx, `
		SELECT COUNT(*) FROM llm_artifact_operations WHERE batch_id=$1 AND state <> 'object_applied'`, batchID).Scan(&incomplete); err != nil {
		return 0, fmt.Errorf("postgres: inspect artifact deletion batch: %w", err)
	}
	if incomplete != 0 {
		return 0, errors.New("postgres: artifact deletion batch is incomplete")
	}
	var deleted int64
	if scope == "execution" {
		command, err := transaction.Exec(ctx, `DELETE FROM llm_runs WHERE owner_id=$1 AND run_id=$2 AND trace_id=$3`, ownerID, *runID, *traceID)
		if err != nil {
			return 0, fmt.Errorf("postgres: delete journaled execution: %w", err)
		}
		deleted = command.RowsAffected()
		if _, err := transaction.Exec(ctx, `DELETE FROM llm_traces WHERE owner_id=$1 AND trace_id=$2`, ownerID, *traceID); err != nil {
			return 0, fmt.Errorf("postgres: delete journaled trace: %w", err)
		}
	} else if scope == "owner" {
		command, err := transaction.Exec(ctx, `DELETE FROM llm_runs WHERE owner_id=$1`, ownerID)
		if err != nil {
			return 0, fmt.Errorf("postgres: clear journaled executions: %w", err)
		}
		deleted = command.RowsAffected()
		if _, err := transaction.Exec(ctx, `DELETE FROM llm_traces WHERE owner_id=$1`, ownerID); err != nil {
			return 0, fmt.Errorf("postgres: clear journaled traces: %w", err)
		}
	} else {
		command, err := transaction.Exec(ctx, `DELETE FROM llm_traces WHERE owner_id=$1 AND trace_id=$2 AND run_id IS NULL`, ownerID, *traceID)
		if err != nil {
			return 0, fmt.Errorf("postgres: delete reconciled trace: %w", err)
		}
		if command.RowsAffected() != 1 {
			return 0, errors.New("postgres: reconciled trace is no longer deletable")
		}
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE llm_artifact_operations SET state='completed', completed_at=$2, updated_at=$2 WHERE batch_id=$1`, batchID, now); err != nil {
		return 0, fmt.Errorf("postgres: complete deletion operations: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE llm_artifact_delete_batches SET state='completed', deleted_run_count=$2, completed_at=$3, updated_at=$3 WHERE batch_id=$1`,
		batchID, deleted, now); err != nil {
		return 0, fmt.Errorf("postgres: complete deletion batch: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres: commit artifact deletion finalization: %w", err)
	}
	return deleted, nil
}

func activeDeleteBatch(ctx context.Context, transaction pgx.Tx, ownerID, scope, runID, traceID string) (ArtifactDeleteBatch, error) {
	query := `SELECT batch_id FROM llm_artifact_delete_batches WHERE owner_id=$1 AND scope=$2 AND state <> 'completed'`
	arguments := []any{ownerID, scope}
	if scope == "execution" {
		query += ` AND run_id=$3`
		arguments = append(arguments, runID)
	} else if scope == "reconciliation" {
		query += ` AND trace_id=$3`
		arguments = append(arguments, traceID)
	}
	var batchID string
	if err := transaction.QueryRow(ctx, query, arguments...).Scan(&batchID); err != nil {
		return ArtifactDeleteBatch{}, err
	}
	var batch ArtifactDeleteBatch
	var selectedRunID, selectedTraceID *string
	if err := transaction.QueryRow(ctx, `
		SELECT batch_id, owner_id, scope, run_id, trace_id, state, expected_artifact_count,
			deleted_run_count, created_at, updated_at
		FROM llm_artifact_delete_batches WHERE batch_id=$1`, batchID).Scan(
		&batch.ID, &batch.OwnerID, &batch.Scope, &selectedRunID, &selectedTraceID, &batch.State,
		&batch.ExpectedArtifactCount, &batch.DeletedRunCount, &batch.CreatedAt, &batch.UpdatedAt); err != nil {
		return ArtifactDeleteBatch{}, err
	}
	if selectedRunID != nil {
		batch.RunID = *selectedRunID
	}
	if selectedTraceID != nil {
		batch.TraceID = *selectedTraceID
	}
	rows, err := transaction.Query(ctx, `
		SELECT operation_id, batch_id, action, state, owner_id, run_id, trace_id, artifact_id, kind,
			object_key, content_type, sha256, size_bytes, attempt_count, next_attempt_at,
			error_category, created_at, updated_at
		FROM llm_artifact_operations WHERE batch_id=$1 ORDER BY operation_id`, batchID)
	if err != nil {
		return ArtifactDeleteBatch{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var operation ArtifactOperation
		if err := rows.Scan(
			&operation.ID, &operation.BatchID, &operation.Action, &operation.State, &operation.OwnerID,
			&operation.RunID, &operation.TraceID, &operation.ArtifactID, &operation.Kind,
			&operation.ObjectKey, &operation.ContentType, &operation.SHA256, &operation.SizeBytes,
			&operation.AttemptCount, &operation.NextAttemptAt, &operation.ErrorCategory,
			&operation.CreatedAt, &operation.UpdatedAt); err != nil {
			return ArtifactDeleteBatch{}, err
		}
		batch.Operations = append(batch.Operations, operation)
	}
	return batch, rows.Err()
}

func deletionArtifacts(ctx context.Context, transaction pgx.Tx, ownerID, scope, runID, traceID string) ([]ArtifactRecord, error) {
	query := `
		SELECT a.owner_id, COALESCE(t.run_id, ''), a.trace_id, a.artifact_id, a.kind,
			a.object_key, a.content_type, a.sha256, a.size_bytes, a.state, a.created_at, a.updated_at
		FROM llm_artifacts a JOIN llm_traces t ON t.owner_id=a.owner_id AND t.trace_id=a.trace_id
		WHERE a.owner_id=$1 AND a.state='available'`
	arguments := []any{ownerID}
	if scope == "execution" {
		query += ` AND a.trace_id=$2`
		arguments = append(arguments, traceID)
	}
	query += ` ORDER BY a.trace_id, a.artifact_id FOR UPDATE OF a`
	rows, err := transaction.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("postgres: select artifacts for deletion: %w", err)
	}
	defer rows.Close()
	var artifacts []ArtifactRecord
	for rows.Next() {
		var artifact ArtifactRecord
		if err := rows.Scan(&artifact.OwnerID, &artifact.RunID, &artifact.TraceID, &artifact.ID, &artifact.Kind,
			&artifact.ObjectKey, &artifact.ContentType, &artifact.SHA256, &artifact.SizeBytes,
			&artifact.State, &artifact.CreatedAt, &artifact.UpdatedAt); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	if scope == "reconciliation" {
		for index := range artifacts {
			artifacts[index].RunID = runID
		}
	}
	return artifacts, rows.Err()
}

func validateArtifactOperation(operation ArtifactOperation, action string) error {
	artifact := ArtifactRecord{
		OwnerID: operation.OwnerID, RunID: operation.RunID, TraceID: operation.TraceID,
		ID: operation.ArtifactID, Kind: operation.Kind, ObjectKey: operation.ObjectKey,
		ContentType: operation.ContentType, SHA256: operation.SHA256, SizeBytes: operation.SizeBytes,
		State: "available", CreatedAt: operation.CreatedAt, UpdatedAt: operation.UpdatedAt,
	}
	if operation.ID != ArtifactOperationID(action, artifact) || operation.Action != action {
		return errors.New("postgres: artifact operation ID is invalid")
	}
	if operation.RunID == "" || operation.NextAttemptAt.IsZero() {
		return errors.New("postgres: artifact operation lifecycle is invalid")
	}
	return validateArtifact(artifact)
}

func sameArtifactOperation(left, right ArtifactOperation) bool {
	return left.ID == right.ID && left.OwnerID == right.OwnerID && left.RunID == right.RunID &&
		left.TraceID == right.TraceID && left.ArtifactID == right.ArtifactID && left.Kind == right.Kind &&
		left.ObjectKey == right.ObjectKey && left.ContentType == right.ContentType &&
		strings.EqualFold(left.SHA256, right.SHA256) && left.SizeBytes == right.SizeBytes
}
