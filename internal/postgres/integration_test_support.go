//go:build integration

package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// SeedLegacyRunlessTraceForTest creates the retained pre-migration shape used
// to prove that history reconciliation is safe. It is unavailable in normal
// production builds.
func (store *Store) SeedLegacyRunlessTraceForTest(ctx context.Context, trace TraceRecord, observations []ObservationRecord) error {
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `
		INSERT INTO llm_traces (owner_id, trace_id, run_id, record, created_at, updated_at)
		VALUES ($1,$2,NULL,$3,$4,$5)`, trace.OwnerID, trace.TraceID, trace.Record, trace.CreatedAt, trace.UpdatedAt); err != nil {
		return fmt.Errorf("postgres test fixture: seed legacy trace: %w", err)
	}
	for _, observation := range observations {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO llm_trace_observations (owner_id, trace_id, sequence, observation_type, data, created_at)
			VALUES ($1,$2,$3,$4,$5,$6)`, observation.OwnerID, observation.TraceID, observation.Sequence,
			observation.Type, observation.Data, observation.CreatedAt); err != nil {
			return fmt.Errorf("postgres test fixture: seed legacy observation: %w", err)
		}
	}
	return transaction.Commit(ctx)
}

// SeedArtifactMetadataForTest installs metadata for an object already created
// by an integration fixture. Production publication must use the coordinator.
func (store *Store) SeedArtifactMetadataForTest(ctx context.Context, artifact ArtifactRecord) error {
	if err := validateArtifact(artifact); err != nil {
		return err
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO llm_artifacts
			(owner_id, trace_id, artifact_id, kind, object_key, content_type, sha256, size_bytes, state, created_at, updated_at, verified_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)`, artifact.OwnerID, artifact.TraceID, artifact.ID, artifact.Kind,
		artifact.ObjectKey, artifact.ContentType, strings.ToLower(artifact.SHA256), artifact.SizeBytes, artifact.State, artifact.CreatedAt, artifact.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres test fixture: seed artifact metadata: %w", err)
	}
	return nil
}
