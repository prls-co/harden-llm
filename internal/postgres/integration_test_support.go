//go:build integration

package postgres

import (
	"context"
	"fmt"
	"strings"
)

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
