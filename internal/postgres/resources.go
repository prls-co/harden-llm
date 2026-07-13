package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type RunCursor struct {
	StartedAt time.Time
	ID        string
}

func (store *Store) Profiles(ctx context.Context, ownerID string) ([]ProfileRecord, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT owner_id, profile_id, credential_id, document, created_at, updated_at
		FROM llm_profiles WHERE owner_id = $1 ORDER BY profile_id`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list profiles: %w", err)
	}
	defer rows.Close()
	var records []ProfileRecord
	for rows.Next() {
		var record ProfileRecord
		var credentialID *string
		if err := rows.Scan(&record.OwnerID, &record.ID, &credentialID, &record.Document, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan profile: %w", err)
		}
		if credentialID != nil {
			record.CredentialID = *credentialID
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (store *Store) Credentials(ctx context.Context, ownerID string) ([]CredentialRecord, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT owner_id, credential_id, key_id, nonce, ciphertext, normalized_origin, metadata, created_at, updated_at
		FROM llm_endpoint_credentials WHERE owner_id = $1 ORDER BY credential_id`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list credentials: %w", err)
	}
	defer rows.Close()
	var records []CredentialRecord
	for rows.Next() {
		var record CredentialRecord
		if err := rows.Scan(&record.OwnerID, &record.ID, &record.KeyID, &record.Nonce, &record.Ciphertext, &record.Origin, &record.Metadata, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan credential: %w", err)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (store *Store) ReplaceProfileBundle(ctx context.Context, ownerID string, profileRecords []ProfileRecord, credentialRecords []CredentialRecord) error {
	if err := validateIdentifier("owner ID", ownerID); err != nil {
		return err
	}
	profileIDs := make([]string, 0, len(profileRecords))
	profileSeen := make(map[string]struct{}, len(profileRecords))
	for _, record := range profileRecords {
		if record.OwnerID != ownerID {
			return errors.New("postgres: bundle profile owner mismatch")
		}
		if err := validateProfile(record); err != nil {
			return err
		}
		if _, duplicate := profileSeen[record.ID]; duplicate {
			return errors.New("postgres: duplicate bundle profile")
		}
		profileSeen[record.ID] = struct{}{}
		profileIDs = append(profileIDs, record.ID)
	}
	credentialIDs := make([]string, 0, len(credentialRecords))
	credentialSeen := make(map[string]struct{}, len(credentialRecords))
	for _, record := range credentialRecords {
		if record.OwnerID != ownerID {
			return errors.New("postgres: bundle credential owner mismatch")
		}
		if err := validateCredential(record); err != nil {
			return err
		}
		if _, duplicate := credentialSeen[record.ID]; duplicate {
			return errors.New("postgres: duplicate bundle credential")
		}
		credentialSeen[record.ID] = struct{}{}
		credentialIDs = append(credentialIDs, record.ID)
	}
	for _, record := range profileRecords {
		if record.CredentialID != "" {
			if _, ok := credentialSeen[record.CredentialID]; !ok {
				return errors.New("postgres: bundle profile credential is missing")
			}
		}
	}

	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin profile bundle replacement: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 1212968013))`, ownerID); err != nil {
		return fmt.Errorf("postgres: lock profile bundle owner: %w", err)
	}
	for _, record := range credentialRecords {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO llm_endpoint_credentials
				(owner_id, credential_id, key_id, nonce, ciphertext, normalized_origin, metadata, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (owner_id, credential_id) DO UPDATE SET
				key_id=EXCLUDED.key_id, nonce=EXCLUDED.nonce, ciphertext=EXCLUDED.ciphertext,
				normalized_origin=EXCLUDED.normalized_origin, metadata=EXCLUDED.metadata,
				created_at=EXCLUDED.created_at, updated_at=EXCLUDED.updated_at`,
			record.OwnerID, record.ID, record.KeyID, record.Nonce, record.Ciphertext,
			record.Origin, record.Metadata, record.CreatedAt, record.UpdatedAt); err != nil {
			return fmt.Errorf("postgres: replace bundle credential: %w", err)
		}
	}
	for _, record := range profileRecords {
		var credentialID any
		if record.CredentialID != "" {
			credentialID = record.CredentialID
		}
		if _, err := transaction.Exec(ctx, `
			INSERT INTO llm_profiles (owner_id, profile_id, credential_id, document, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (owner_id, profile_id) DO UPDATE SET
				credential_id=EXCLUDED.credential_id, document=EXCLUDED.document,
				created_at=EXCLUDED.created_at, updated_at=EXCLUDED.updated_at`,
			record.OwnerID, record.ID, credentialID, record.Document, record.CreatedAt, record.UpdatedAt); err != nil {
			return fmt.Errorf("postgres: replace bundle profile: %w", err)
		}
	}
	if len(profileIDs) == 0 {
		if _, err := transaction.Exec(ctx, `DELETE FROM llm_profiles WHERE owner_id = $1`, ownerID); err != nil {
			return fmt.Errorf("postgres: delete replaced profiles: %w", err)
		}
	} else if _, err := transaction.Exec(ctx, `DELETE FROM llm_profiles WHERE owner_id = $1 AND NOT (profile_id = ANY($2))`, ownerID, profileIDs); err != nil {
		return fmt.Errorf("postgres: delete replaced profiles: %w", err)
	}
	if len(credentialIDs) == 0 {
		if _, err := transaction.Exec(ctx, `DELETE FROM llm_endpoint_credentials WHERE owner_id = $1`, ownerID); err != nil {
			return fmt.Errorf("postgres: delete replaced credentials: %w", err)
		}
	} else if _, err := transaction.Exec(ctx, `DELETE FROM llm_endpoint_credentials WHERE owner_id = $1 AND NOT (credential_id = ANY($2))`, ownerID, credentialIDs); err != nil {
		return fmt.Errorf("postgres: delete replaced credentials: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE llm_endpoint_credentials c SET metadata = jsonb_set(
			c.metadata, '{apiInferenceTypes}',
			COALESCE((
				SELECT jsonb_agg(value ORDER BY value) FROM (
					SELECT DISTINCT p.document->>'apiInferenceType' AS value
					FROM llm_profiles p
					WHERE p.owner_id=c.owner_id AND p.credential_id=c.credential_id
				) inference_types WHERE value IS NOT NULL AND value <> ''
			), '[]'::jsonb), true
		)
		WHERE c.owner_id=$1`, ownerID); err != nil {
		return fmt.Errorf("postgres: reconcile bundled credential inference types: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit profile bundle replacement: %w", err)
	}
	return nil
}

func (store *Store) DeleteProfile(ctx context.Context, ownerID, profileID string) error {
	if err := validateIdentifier("owner ID", ownerID); err != nil {
		return ErrNotFound
	}
	if err := validateProfileIdentifier(profileID); err != nil {
		return ErrNotFound
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin profile deletion: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 1212968013))`, ownerID); err != nil {
		return fmt.Errorf("postgres: lock profile owner: %w", err)
	}
	var credentialID *string
	if err := transaction.QueryRow(ctx, `DELETE FROM llm_profiles WHERE owner_id=$1 AND profile_id=$2 RETURNING credential_id`, ownerID, profileID).Scan(&credentialID); err != nil {
		return notFound(err)
	}
	if credentialID != nil {
		if _, err := transaction.Exec(ctx, `
			DELETE FROM llm_endpoint_credentials c WHERE c.owner_id=$1 AND c.credential_id=$2
			AND NOT EXISTS (SELECT 1 FROM llm_profiles p WHERE p.owner_id=c.owner_id AND p.credential_id=c.credential_id)`, ownerID, *credentialID); err != nil {
			return fmt.Errorf("postgres: delete orphan credential: %w", err)
		}
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE llm_endpoint_credentials c SET metadata = jsonb_set(
			c.metadata, '{apiInferenceTypes}',
			COALESCE((
				SELECT jsonb_agg(value ORDER BY value) FROM (
					SELECT DISTINCT p.document->>'apiInferenceType' AS value
					FROM llm_profiles p
					WHERE p.owner_id=c.owner_id AND p.credential_id=c.credential_id
				) inference_types WHERE value IS NOT NULL AND value <> ''
			), '[]'::jsonb), true
		)
		WHERE c.owner_id=$1`, ownerID); err != nil {
		return fmt.Errorf("postgres: reconcile credential inference types after profile deletion: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit profile deletion: %w", err)
	}
	return nil
}

func (store *Store) Runs(ctx context.Context, ownerID string, limit int, cursor *RunCursor) ([]RunRecord, error) {
	if limit < 1 || limit > 101 {
		return nil, errors.New("postgres: history limit is invalid")
	}
	query := `
		SELECT owner_id, run_id, profile_id, trace_id, status, request, result, started_at, completed_at
		FROM llm_runs WHERE owner_id = $1`
	arguments := []any{ownerID}
	if cursor != nil {
		if cursor.StartedAt.IsZero() || validateIdentifier("run cursor ID", cursor.ID) != nil {
			return nil, errors.New("postgres: history cursor is invalid")
		}
		query += ` AND (started_at, run_id) < ($2, $3)`
		arguments = append(arguments, cursor.StartedAt, cursor.ID)
	}
	arguments = append(arguments, limit)
	query += fmt.Sprintf(` ORDER BY started_at DESC, run_id DESC LIMIT $%d`, len(arguments))
	rows, err := store.pool.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list history: %w", err)
	}
	defer rows.Close()
	var records []RunRecord
	for rows.Next() {
		var record RunRecord
		if err := rows.Scan(&record.OwnerID, &record.ID, &record.ProfileID, &record.TraceID, &record.Status, &record.Request, &record.Result, &record.StartedAt, &record.CompletedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan history: %w", err)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (store *Store) DeleteRun(ctx context.Context, ownerID, runID string) error {
	result, err := store.pool.Exec(ctx, `DELETE FROM llm_runs WHERE owner_id=$1 AND run_id=$2`, ownerID, runID)
	if err != nil {
		return fmt.Errorf("postgres: delete history record: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (store *Store) ClearRuns(ctx context.Context, ownerID string) (int64, error) {
	result, err := store.pool.Exec(ctx, `DELETE FROM llm_runs WHERE owner_id=$1`, ownerID)
	if err != nil {
		return 0, fmt.Errorf("postgres: clear history: %w", err)
	}
	return result.RowsAffected(), nil
}

func (store *Store) Artifacts(ctx context.Context, ownerID, traceID string) ([]ArtifactRecord, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT owner_id, trace_id, artifact_id, kind, object_key, content_type, sha256, size_bytes, available, created_at, updated_at
		FROM llm_artifacts WHERE owner_id=$1 AND trace_id=$2 AND available ORDER BY created_at, artifact_id`, ownerID, traceID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list trace artifacts: %w", err)
	}
	defer rows.Close()
	var records []ArtifactRecord
	for rows.Next() {
		var record ArtifactRecord
		if err := rows.Scan(&record.OwnerID, &record.TraceID, &record.ID, &record.Kind, &record.ObjectKey, &record.ContentType, &record.SHA256, &record.SizeBytes, &record.Available, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan trace artifact: %w", err)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (store *Store) LatestCache(ctx context.Context, ownerID, operationHash string) (CacheRecord, error) {
	var record CacheRecord
	err := store.pool.QueryRow(ctx, `
		SELECT owner_id, cache_version, operation_hash, operation, result, usage, cost, provider_envelope, created_at, updated_at
		FROM llm_operation_cache WHERE owner_id=$1 AND operation_hash=$2 ORDER BY updated_at DESC, cache_version DESC LIMIT 1`, ownerID, operationHash).Scan(
		&record.OwnerID, &record.Version, &record.OperationHash, &record.Operation, &record.Result,
		&record.Usage, &record.Cost, &record.Envelope, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		return CacheRecord{}, notFound(err)
	}
	return record, nil
}

func (store *Store) DeleteCache(ctx context.Context, ownerID, operationHash string) error {
	if _, err := store.pool.Exec(ctx, `DELETE FROM llm_operation_cache WHERE owner_id=$1 AND operation_hash=$2`, ownerID, operationHash); err != nil {
		return fmt.Errorf("postgres: delete cache record: %w", err)
	}
	return nil
}

// SaveExecution atomically commits one normalized run, domain trace, and its
// contiguous observations after the provider call has completed.
func (store *Store) SaveExecution(ctx context.Context, run RunRecord, trace TraceRecord, observations []ObservationRecord) error {
	if run.OwnerID != trace.OwnerID || run.TraceID != trace.TraceID {
		return errors.New("postgres: execution run and trace binding mismatch")
	}
	if err := validateIdentifier("owner ID", run.OwnerID); err != nil {
		return err
	}
	for name, value := range map[string]string{"run ID": run.ID, "trace ID": run.TraceID} {
		if err := validateIdentifier(name, value); err != nil {
			return err
		}
	}
	if err := validateProfileIdentifier(run.ProfileID); err != nil {
		return err
	}
	if run.Status != "succeeded" && run.Status != "failed" && run.Status != "timeout" {
		return errors.New("postgres: execution status is invalid")
	}
	if err := validateJSONObject("run request", run.Request); err != nil {
		return err
	}
	if err := validateJSONObject("run result", run.Result); err != nil {
		return err
	}
	if run.StartedAt.IsZero() || run.CompletedAt.Before(run.StartedAt) || trace.CreatedAt.IsZero() || trace.UpdatedAt.Before(trace.CreatedAt) {
		return errors.New("postgres: execution timestamps are invalid")
	}
	if err := validateJSONObject("trace record", trace.Record); err != nil {
		return err
	}
	for index, observation := range observations {
		if observation.OwnerID != trace.OwnerID || observation.TraceID != trace.TraceID || observation.Sequence != index || observation.Type == "" || len(observation.Type) > 64 || observation.CreatedAt.IsZero() {
			return errors.New("postgres: execution observations are invalid")
		}
		if err := validateJSONObject("trace observation", observation.Data); err != nil {
			return err
		}
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin execution save: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `
		INSERT INTO llm_traces (owner_id, trace_id, record, created_at, updated_at) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (owner_id, trace_id) DO UPDATE SET record=EXCLUDED.record, updated_at=EXCLUDED.updated_at`,
		trace.OwnerID, trace.TraceID, trace.Record, trace.CreatedAt, trace.UpdatedAt); err != nil {
		return fmt.Errorf("postgres: save execution trace: %w", err)
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM llm_trace_observations WHERE owner_id=$1 AND trace_id=$2`, trace.OwnerID, trace.TraceID); err != nil {
		return fmt.Errorf("postgres: replace execution observations: %w", err)
	}
	for _, observation := range observations {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO llm_trace_observations (owner_id, trace_id, sequence, observation_type, data, created_at)
			VALUES ($1,$2,$3,$4,$5,$6)`, observation.OwnerID, observation.TraceID, observation.Sequence, observation.Type, observation.Data, observation.CreatedAt); err != nil {
			return fmt.Errorf("postgres: save execution observation: %w", err)
		}
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO llm_runs (owner_id, run_id, profile_id, trace_id, status, request, result, started_at, completed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, run.OwnerID, run.ID, run.ProfileID, run.TraceID, run.Status, run.Request, run.Result, run.StartedAt, run.CompletedAt); err != nil {
		return fmt.Errorf("postgres: save execution run: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit execution save: %w", err)
	}
	return nil
}

func (store *Store) UpdateRunResult(ctx context.Context, ownerID, runID string, result json.RawMessage) error {
	if err := validateJSONObject("run result", result); err != nil {
		return err
	}
	command, err := store.pool.Exec(ctx, `UPDATE llm_runs SET result=$3 WHERE owner_id=$1 AND run_id=$2`, ownerID, runID, result)
	if err != nil {
		return fmt.Errorf("postgres: update run result: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
