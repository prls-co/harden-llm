package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"time"

	"github.com/jackc/pgx/v5"
)

type RunlessTraceCandidate struct {
	Trace        TraceRecord
	Observations []ObservationRecord
	Artifacts    []ArtifactRecord
	Fingerprint  string
}

// RunlessTraceCandidates returns a bounded, stable snapshot of legacy trace
// subtrees that have no aggregate-root run binding. Empty ownerID means an
// explicitly authorized all-owner administrative scan.
func (store *Store) RunlessTraceCandidates(ctx context.Context, ownerID string, limit int) ([]RunlessTraceCandidate, bool, error) {
	if store == nil || store.pool == nil || limit < 1 || limit > 10_000 {
		return nil, false, errors.New("postgres: runless trace scan is invalid")
	}
	query := `SELECT owner_id, trace_id FROM llm_traces WHERE run_id IS NULL`
	arguments := []any{}
	if ownerID != "" {
		query += ` AND owner_id=$1`
		arguments = append(arguments, ownerID)
	}
	query += ` ORDER BY owner_id, trace_id LIMIT $` + fmt.Sprint(len(arguments)+1)
	arguments = append(arguments, limit+1)
	rows, err := store.pool.Query(ctx, query, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("postgres: list runless traces: %w", err)
	}
	defer rows.Close()
	type identity struct{ ownerID, traceID string }
	identities := make([]identity, 0, limit+1)
	for rows.Next() {
		var item identity
		if err := rows.Scan(&item.ownerID, &item.traceID); err != nil {
			return nil, false, fmt.Errorf("postgres: scan runless trace identity: %w", err)
		}
		identities = append(identities, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(identities) > limit
	if truncated {
		identities = identities[:limit]
	}
	candidates := make([]RunlessTraceCandidate, 0, len(identities))
	for _, identity := range identities {
		candidate, err := loadRunlessTraceCandidate(ctx, store.pool, identity.ownerID, identity.traceID, false)
		if err != nil {
			return nil, false, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, truncated, nil
}

func (store *Store) RunIdentityExists(ctx context.Context, ownerID, runID string) (bool, error) {
	var exists bool
	if err := store.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM llm_runs WHERE owner_id=$1 AND run_id=$2)`, ownerID, runID).Scan(&exists); err != nil {
		return false, fmt.Errorf("postgres: inspect run identity: %w", err)
	}
	return exists, nil
}

type candidateQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadRunlessTraceCandidate(ctx context.Context, query candidateQuerier, ownerID, traceID string, lock bool) (RunlessTraceCandidate, error) {
	var candidate RunlessTraceCandidate
	statement := `
		SELECT owner_id, trace_id, COALESCE(run_id, ''), record, created_at, updated_at
		FROM llm_traces WHERE owner_id=$1 AND trace_id=$2 AND run_id IS NULL`
	if lock {
		statement += ` FOR UPDATE`
	}
	if err := query.QueryRow(ctx, statement, ownerID, traceID).Scan(
		&candidate.Trace.OwnerID, &candidate.Trace.TraceID, &candidate.Trace.RunID,
		&candidate.Trace.Record, &candidate.Trace.CreatedAt, &candidate.Trace.UpdatedAt); err != nil {
		return RunlessTraceCandidate{}, notFound(err)
	}
	observationRows, err := query.Query(ctx, `
		SELECT owner_id, trace_id, sequence, observation_type, data, created_at
		FROM llm_trace_observations WHERE owner_id=$1 AND trace_id=$2 ORDER BY sequence`, ownerID, traceID)
	if err != nil {
		return RunlessTraceCandidate{}, fmt.Errorf("postgres: list runless trace observations: %w", err)
	}
	for observationRows.Next() {
		var observation ObservationRecord
		if err := observationRows.Scan(&observation.OwnerID, &observation.TraceID, &observation.Sequence,
			&observation.Type, &observation.Data, &observation.CreatedAt); err != nil {
			observationRows.Close()
			return RunlessTraceCandidate{}, fmt.Errorf("postgres: scan runless trace observation: %w", err)
		}
		candidate.Observations = append(candidate.Observations, observation)
	}
	if err := observationRows.Err(); err != nil {
		observationRows.Close()
		return RunlessTraceCandidate{}, err
	}
	observationRows.Close()
	artifactRows, err := query.Query(ctx, `
		SELECT owner_id, ''::text, trace_id, artifact_id, kind, object_key, content_type,
			sha256, size_bytes, state, created_at, updated_at
		FROM llm_artifacts WHERE owner_id=$1 AND trace_id=$2 ORDER BY artifact_id`, ownerID, traceID)
	if err != nil {
		return RunlessTraceCandidate{}, fmt.Errorf("postgres: list runless trace artifacts: %w", err)
	}
	for artifactRows.Next() {
		var artifact ArtifactRecord
		if err := artifactRows.Scan(&artifact.OwnerID, &artifact.RunID, &artifact.TraceID, &artifact.ID,
			&artifact.Kind, &artifact.ObjectKey, &artifact.ContentType, &artifact.SHA256,
			&artifact.SizeBytes, &artifact.State, &artifact.CreatedAt, &artifact.UpdatedAt); err != nil {
			artifactRows.Close()
			return RunlessTraceCandidate{}, fmt.Errorf("postgres: scan runless trace artifact: %w", err)
		}
		candidate.Artifacts = append(candidate.Artifacts, artifact)
	}
	if err := artifactRows.Err(); err != nil {
		artifactRows.Close()
		return RunlessTraceCandidate{}, err
	}
	artifactRows.Close()
	candidate.Fingerprint = runlessTraceFingerprint(candidate)
	return candidate, nil
}

func runlessTraceFingerprint(candidate RunlessTraceCandidate) string {
	digest := sha256.New()
	writeFingerprint(digest, candidate.Trace.OwnerID, candidate.Trace.TraceID, string(candidate.Trace.Record),
		candidate.Trace.CreatedAt.UTC().Format(time.RFC3339Nano), candidate.Trace.UpdatedAt.UTC().Format(time.RFC3339Nano))
	for _, observation := range candidate.Observations {
		writeFingerprint(digest, fmt.Sprint(observation.Sequence), observation.Type, string(observation.Data),
			observation.CreatedAt.UTC().Format(time.RFC3339Nano))
	}
	for _, artifact := range candidate.Artifacts {
		writeFingerprint(digest, artifact.ID, artifact.Kind, artifact.ObjectKey, artifact.ContentType,
			artifact.SHA256, fmt.Sprint(artifact.SizeBytes), artifact.State,
			artifact.CreatedAt.UTC().Format(time.RFC3339Nano), artifact.UpdatedAt.UTC().Format(time.RFC3339Nano))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeFingerprint(digest hash.Hash, values ...string) {
	for _, value := range values {
		_, _ = fmt.Fprintf(digest, "%d:%s|", len(value), value)
	}
}
