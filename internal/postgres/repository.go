package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (store *Store) CreateUser(ctx context.Context, user User) error {
	if err := validateIdentifier("user ID", user.ID); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(user.Email))
	if email == "" || len(email) > 320 || !strings.Contains(email, "@") {
		return errors.New("postgres: valid user email is required")
	}
	if !strings.HasPrefix(user.PasswordHash, "$argon2id$") {
		return errors.New("postgres: Argon2id password hash is required")
	}
	if user.CreatedAt.IsZero() || user.UpdatedAt.Before(user.CreatedAt) {
		return errors.New("postgres: valid user timestamps are required")
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)`, user.ID, email, user.PasswordHash, user.CreatedAt, user.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: create user: %w", err)
	}
	return nil
}

func (store *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	var user User
	err := store.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, created_at, updated_at
		FROM users WHERE email = $1`, strings.ToLower(strings.TrimSpace(email))).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		return User{}, notFound(err)
	}
	return user, nil
}

func (store *Store) UserByID(ctx context.Context, id string) (User, error) {
	var user User
	err := store.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, created_at, updated_at
		FROM users WHERE id = $1`, id).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return User{}, notFound(err)
	}
	return user, nil
}

func (store *Store) CreateSession(ctx context.Context, session Session) error {
	if err := validateIdentifier("session ID", session.ID); err != nil {
		return err
	}
	if err := validateIdentifier("owner ID", session.OwnerID); err != nil {
		return err
	}
	if len(session.TokenDigest) != 32 {
		return errors.New("postgres: session token digest must be 32 bytes")
	}
	if session.CreatedAt.IsZero() || !session.ExpiresAt.After(session.CreatedAt) {
		return errors.New("postgres: session expiry must follow creation")
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO user_sessions (id, owner_id, token_digest, expires_at, revoked_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		session.ID, session.OwnerID, session.TokenDigest, session.ExpiresAt, session.RevokedAt, session.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: create session: %w", err)
	}
	return nil
}

func (store *Store) SessionByDigest(ctx context.Context, digest []byte) (Session, error) {
	if len(digest) != 32 {
		return Session{}, ErrNotFound
	}
	var session Session
	var revoked pgtype.Timestamptz
	err := store.pool.QueryRow(ctx, `
		SELECT id, owner_id, token_digest, expires_at, revoked_at, created_at
		FROM user_sessions WHERE token_digest = $1`, digest).Scan(
		&session.ID, &session.OwnerID, &session.TokenDigest, &session.ExpiresAt, &revoked, &session.CreatedAt,
	)
	if err != nil {
		return Session{}, notFound(err)
	}
	if revoked.Valid {
		value := revoked.Time
		session.RevokedAt = &value
	}
	return session, nil
}

func (store *Store) RevokeSession(ctx context.Context, ownerID string, digest []byte, revokedAt time.Time) error {
	if len(digest) != 32 || revokedAt.IsZero() {
		return ErrNotFound
	}
	result, err := store.pool.Exec(ctx, `
		UPDATE user_sessions SET revoked_at = COALESCE(revoked_at, $3)
		WHERE owner_id = $1 AND token_digest = $2`, ownerID, digest, revokedAt)
	if err != nil {
		return fmt.Errorf("postgres: revoke session: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeSessionByID revokes one owner-scoped session without retaining its
// bearer token in HTTP request context.
func (store *Store) RevokeSessionByID(ctx context.Context, ownerID, sessionID string, revokedAt time.Time) error {
	if err := validateIdentifier("owner ID", ownerID); err != nil {
		return ErrNotFound
	}
	if err := validateIdentifier("session ID", sessionID); err != nil || revokedAt.IsZero() {
		return ErrNotFound
	}
	result, err := store.pool.Exec(ctx, `
		UPDATE user_sessions SET revoked_at = COALESCE(revoked_at, $3)
		WHERE owner_id = $1 AND id = $2 AND revoked_at IS NULL AND expires_at > $3`, ownerID, sessionID, revokedAt)
	if err != nil {
		return fmt.Errorf("postgres: revoke session by ID: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (store *Store) SaveProfile(ctx context.Context, profile ProfileRecord, credential *CredentialRecord) error {
	if err := validateProfile(profile); err != nil {
		return err
	}
	if credential == nil && profile.CredentialID != "" {
		if _, err := store.Credential(ctx, profile.OwnerID, profile.CredentialID); err != nil {
			return errors.New("postgres: profile credential is not available")
		}
	}
	if credential != nil {
		if credential.OwnerID != profile.OwnerID || credential.ID != profile.CredentialID {
			return errors.New("postgres: profile credential binding mismatch")
		}
		if err := validateCredential(*credential); err != nil {
			return err
		}
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin profile save: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 1212968013))`, profile.OwnerID); err != nil {
		return fmt.Errorf("postgres: lock profile owner: %w", err)
	}
	if credential != nil {
		var existingOrigin, existingScope string
		bindingErr := transaction.QueryRow(ctx, `
			SELECT c.normalized_origin, c.metadata->>'scope'
			FROM llm_endpoint_credentials c
			WHERE c.owner_id=$1 AND c.credential_id=$2
			AND EXISTS (
				SELECT 1 FROM llm_profiles p
				WHERE p.owner_id=c.owner_id AND p.credential_id=c.credential_id AND p.profile_id<>$3
			)`, credential.OwnerID, credential.ID, profile.ID).Scan(&existingOrigin, &existingScope)
		if bindingErr != nil && !errors.Is(bindingErr, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: inspect shared credential binding: %w", bindingErr)
		}
		var metadata struct {
			Scope string `json:"scope"`
		}
		if bindingErr == nil && (json.Unmarshal(credential.Metadata, &metadata) != nil || existingOrigin != credential.Origin || existingScope != metadata.Scope) {
			return errors.New("postgres: shared credential origin and scope cannot change")
		}
		_, err = transaction.Exec(ctx, `
			INSERT INTO llm_endpoint_credentials
				(owner_id, credential_id, key_id, nonce, ciphertext, normalized_origin, metadata, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (owner_id, credential_id) DO UPDATE SET
				key_id = EXCLUDED.key_id,
				nonce = EXCLUDED.nonce,
				ciphertext = EXCLUDED.ciphertext,
				normalized_origin = EXCLUDED.normalized_origin,
				metadata = EXCLUDED.metadata,
				updated_at = EXCLUDED.updated_at`,
			credential.OwnerID, credential.ID, credential.KeyID, credential.Nonce, credential.Ciphertext,
			credential.Origin, credential.Metadata, credential.CreatedAt, credential.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("postgres: save credential: %w", err)
		}
	}
	var credentialID any
	if profile.CredentialID != "" {
		credentialID = profile.CredentialID
	}
	_, err = transaction.Exec(ctx, `
		INSERT INTO llm_profiles (owner_id, profile_id, credential_id, document, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (owner_id, profile_id) DO UPDATE SET
			credential_id = EXCLUDED.credential_id,
			document = EXCLUDED.document,
			updated_at = EXCLUDED.updated_at`,
		profile.OwnerID, profile.ID, credentialID, profile.Document, profile.CreatedAt, profile.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save profile: %w", err)
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
		WHERE c.owner_id=$1`, profile.OwnerID); err != nil {
		return fmt.Errorf("postgres: reconcile credential inference types: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		DELETE FROM llm_endpoint_credentials c WHERE c.owner_id=$1
		AND NOT EXISTS (
			SELECT 1 FROM llm_profiles p WHERE p.owner_id=c.owner_id AND p.credential_id=c.credential_id
		)`, profile.OwnerID); err != nil {
		return fmt.Errorf("postgres: delete orphan credentials: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit profile save: %w", err)
	}
	return nil
}

// SeedProfiles inserts a credential-free profile catalog only when the owner
// has no profiles. The advisory lock makes concurrent first-use requests
// converge on one immutable seed without overwriting operator edits.
func (store *Store) SeedProfiles(ctx context.Context, ownerID string, profileRecords []ProfileRecord) error {
	if err := validateIdentifier("owner ID", ownerID); err != nil {
		return err
	}
	if len(profileRecords) == 0 {
		return nil
	}
	for _, record := range profileRecords {
		if record.OwnerID != ownerID {
			return errors.New("postgres: seeded profile owner mismatch")
		}
		if record.CredentialID != "" {
			return errors.New("postgres: seeded profiles must not bind credentials")
		}
		if err := validateProfile(record); err != nil {
			return err
		}
	}

	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin profile seed: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 1212968013))`, ownerID); err != nil {
		return fmt.Errorf("postgres: lock profile seed owner: %w", err)
	}
	var hasProfiles bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM llm_profiles WHERE owner_id = $1)`, ownerID).Scan(&hasProfiles); err != nil {
		return fmt.Errorf("postgres: inspect profile seed: %w", err)
	}
	if hasProfiles {
		if err := transaction.Commit(ctx); err != nil {
			return fmt.Errorf("postgres: commit skipped profile seed: %w", err)
		}
		return nil
	}
	for _, record := range profileRecords {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO llm_profiles (owner_id, profile_id, credential_id, document, created_at, updated_at)
			VALUES ($1,$2,NULL,$3,$4,$5)`,
			record.OwnerID, record.ID, record.Document, record.CreatedAt, record.UpdatedAt); err != nil {
			return fmt.Errorf("postgres: seed profile %q: %w", record.ID, err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit profile seed: %w", err)
	}
	return nil
}

func (store *Store) Profile(ctx context.Context, ownerID, profileID string) (ProfileRecord, error) {
	var profile ProfileRecord
	var credentialID *string
	err := store.pool.QueryRow(ctx, `
		SELECT owner_id, profile_id, credential_id, document, created_at, updated_at
		FROM llm_profiles WHERE owner_id = $1 AND profile_id = $2`, ownerID, profileID).Scan(
		&profile.OwnerID, &profile.ID, &credentialID, &profile.Document, &profile.CreatedAt, &profile.UpdatedAt,
	)
	if err != nil {
		return ProfileRecord{}, notFound(err)
	}
	if credentialID != nil {
		profile.CredentialID = *credentialID
	}
	return profile, nil
}

func (store *Store) Credential(ctx context.Context, ownerID, credentialID string) (CredentialRecord, error) {
	var credential CredentialRecord
	err := store.pool.QueryRow(ctx, `
		SELECT owner_id, credential_id, key_id, nonce, ciphertext, normalized_origin, metadata, created_at, updated_at
		FROM llm_endpoint_credentials WHERE owner_id = $1 AND credential_id = $2`, ownerID, credentialID).Scan(
		&credential.OwnerID, &credential.ID, &credential.KeyID, &credential.Nonce, &credential.Ciphertext,
		&credential.Origin, &credential.Metadata, &credential.CreatedAt, &credential.UpdatedAt,
	)
	if err != nil {
		return CredentialRecord{}, notFound(err)
	}
	return credential, nil
}

func (store *Store) SaveClientState(ctx context.Context, state ClientState) error {
	if err := validateIdentifier("owner ID", state.OwnerID); err != nil {
		return err
	}
	if err := validateJSONObject("client state", state.Document); err != nil {
		return err
	}
	if state.UpdatedAt.IsZero() {
		return errors.New("postgres: client state timestamp is required")
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO llm_client_state (owner_id, document, updated_at) VALUES ($1,$2,$3)
		ON CONFLICT (owner_id) DO UPDATE SET document = EXCLUDED.document, updated_at = EXCLUDED.updated_at`,
		state.OwnerID, state.Document, state.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save client state: %w", err)
	}
	return nil
}

func (store *Store) ClientState(ctx context.Context, ownerID string) (ClientState, error) {
	var state ClientState
	err := store.pool.QueryRow(ctx, `SELECT owner_id, document, updated_at FROM llm_client_state WHERE owner_id = $1`, ownerID).Scan(
		&state.OwnerID, &state.Document, &state.UpdatedAt,
	)
	if err != nil {
		return ClientState{}, notFound(err)
	}
	return state, nil
}

func (store *Store) SaveRun(ctx context.Context, record RunRecord) error {
	if err := validateIdentifier("owner ID", record.OwnerID); err != nil {
		return err
	}
	for name, value := range map[string]string{"run ID": record.ID, "trace ID": record.TraceID} {
		if err := validateIdentifier(name, value); err != nil {
			return err
		}
	}
	if err := validateProfileIdentifier(record.ProfileID); err != nil {
		return err
	}
	if err := validateJSONObject("run request", record.Request); err != nil {
		return err
	}
	if err := validateJSONObject("run result", record.Result); err != nil {
		return err
	}
	if record.StartedAt.IsZero() || record.CompletedAt.Before(record.StartedAt) {
		return errors.New("postgres: valid run timestamps are required")
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO llm_runs (owner_id, run_id, profile_id, trace_id, status, request, result, started_at, completed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		record.OwnerID, record.ID, record.ProfileID, record.TraceID, record.Status, record.Request, record.Result, record.StartedAt, record.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save run: %w", err)
	}
	return nil
}

func (store *Store) Run(ctx context.Context, ownerID, runID string) (RunRecord, error) {
	var record RunRecord
	err := store.pool.QueryRow(ctx, `
		SELECT owner_id, run_id, profile_id, trace_id, status, request, result, started_at, completed_at
		FROM llm_runs WHERE owner_id = $1 AND run_id = $2`, ownerID, runID).Scan(
		&record.OwnerID, &record.ID, &record.ProfileID, &record.TraceID, &record.Status,
		&record.Request, &record.Result, &record.StartedAt, &record.CompletedAt,
	)
	if err != nil {
		return RunRecord{}, notFound(err)
	}
	return record, nil
}

func (store *Store) SaveTrace(ctx context.Context, trace TraceRecord, observations []ObservationRecord) error {
	if err := validateIdentifier("owner ID", trace.OwnerID); err != nil {
		return err
	}
	if err := validateIdentifier("trace ID", trace.TraceID); err != nil {
		return err
	}
	if err := validateJSONObject("trace record", trace.Record); err != nil {
		return err
	}
	if trace.CreatedAt.IsZero() || trace.UpdatedAt.Before(trace.CreatedAt) {
		return errors.New("postgres: valid trace timestamps are required")
	}
	for index, observation := range observations {
		if observation.OwnerID != trace.OwnerID || observation.TraceID != trace.TraceID || observation.Sequence != index {
			return errors.New("postgres: trace observations must be owner-bound and contiguous")
		}
		if observation.Type == "" || len(observation.Type) > 64 || observation.CreatedAt.IsZero() {
			return errors.New("postgres: valid trace observation metadata is required")
		}
		if err := validateJSONObject("trace observation", observation.Data); err != nil {
			return err
		}
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin trace save: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	_, err = transaction.Exec(ctx, `
		INSERT INTO llm_traces (owner_id, trace_id, record, created_at, updated_at) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (owner_id, trace_id) DO UPDATE SET record = EXCLUDED.record, updated_at = EXCLUDED.updated_at`,
		trace.OwnerID, trace.TraceID, trace.Record, trace.CreatedAt, trace.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save trace: %w", err)
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM llm_trace_observations WHERE owner_id = $1 AND trace_id = $2`, trace.OwnerID, trace.TraceID); err != nil {
		return fmt.Errorf("postgres: replace trace observations: %w", err)
	}
	for _, observation := range observations {
		_, err := transaction.Exec(ctx, `
			INSERT INTO llm_trace_observations (owner_id, trace_id, sequence, observation_type, data, created_at)
			VALUES ($1,$2,$3,$4,$5,$6)`, observation.OwnerID, observation.TraceID, observation.Sequence, observation.Type, observation.Data, observation.CreatedAt)
		if err != nil {
			return fmt.Errorf("postgres: save trace observation: %w", err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit trace save: %w", err)
	}
	return nil
}

func (store *Store) Trace(ctx context.Context, ownerID, traceID string) (TraceRecord, []ObservationRecord, error) {
	var trace TraceRecord
	err := store.pool.QueryRow(ctx, `
		SELECT owner_id, trace_id, record, created_at, updated_at
		FROM llm_traces WHERE owner_id = $1 AND trace_id = $2`, ownerID, traceID).Scan(
		&trace.OwnerID, &trace.TraceID, &trace.Record, &trace.CreatedAt, &trace.UpdatedAt,
	)
	if err != nil {
		return TraceRecord{}, nil, notFound(err)
	}
	rows, err := store.pool.Query(ctx, `
		SELECT owner_id, trace_id, sequence, observation_type, data, created_at
		FROM llm_trace_observations WHERE owner_id = $1 AND trace_id = $2 ORDER BY sequence`, ownerID, traceID)
	if err != nil {
		return TraceRecord{}, nil, fmt.Errorf("postgres: query trace observations: %w", err)
	}
	defer rows.Close()
	var observations []ObservationRecord
	for rows.Next() {
		var observation ObservationRecord
		if err := rows.Scan(&observation.OwnerID, &observation.TraceID, &observation.Sequence, &observation.Type, &observation.Data, &observation.CreatedAt); err != nil {
			return TraceRecord{}, nil, fmt.Errorf("postgres: scan trace observation: %w", err)
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return TraceRecord{}, nil, fmt.Errorf("postgres: iterate trace observations: %w", err)
	}
	return trace, observations, nil
}

func (store *Store) SaveArtifact(ctx context.Context, artifact ArtifactRecord) error {
	if !artifact.Available {
		return errors.New("postgres: only successfully uploaded artifacts can be indexed")
	}
	for name, value := range map[string]string{"owner ID": artifact.OwnerID, "trace ID": artifact.TraceID, "artifact ID": artifact.ID} {
		if err := validateIdentifier(name, value); err != nil {
			return err
		}
	}
	if artifact.ObjectKey == "" || strings.Contains(artifact.ObjectKey, "..") || artifact.ContentType != "application/json" || len(artifact.SHA256) != 64 || artifact.SizeBytes <= 0 {
		return errors.New("postgres: invalid artifact metadata")
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO llm_artifacts
			(owner_id, trace_id, artifact_id, kind, object_key, content_type, sha256, size_bytes, available, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, artifact.OwnerID, artifact.TraceID, artifact.ID, artifact.Kind,
		artifact.ObjectKey, artifact.ContentType, strings.ToLower(artifact.SHA256), artifact.SizeBytes, artifact.Available, artifact.CreatedAt, artifact.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save artifact: %w", err)
	}
	return nil
}

func (store *Store) Artifact(ctx context.Context, ownerID, traceID, artifactID string) (ArtifactRecord, error) {
	var artifact ArtifactRecord
	err := store.pool.QueryRow(ctx, `
		SELECT owner_id, trace_id, artifact_id, kind, object_key, content_type, sha256, size_bytes, available, created_at, updated_at
		FROM llm_artifacts WHERE owner_id = $1 AND trace_id = $2 AND artifact_id = $3 AND available`, ownerID, traceID, artifactID).Scan(
		&artifact.OwnerID, &artifact.TraceID, &artifact.ID, &artifact.Kind, &artifact.ObjectKey, &artifact.ContentType,
		&artifact.SHA256, &artifact.SizeBytes, &artifact.Available, &artifact.CreatedAt, &artifact.UpdatedAt,
	)
	if err != nil {
		return ArtifactRecord{}, notFound(err)
	}
	return artifact, nil
}

func (store *Store) SaveStats(ctx context.Context, stats StatsRecord) error {
	if err := validateIdentifier("owner ID", stats.OwnerID); err != nil {
		return err
	}
	if stats.Scope == "" || len(stats.Scope) > 128 || stats.UpdatedAt.IsZero() {
		return errors.New("postgres: valid stats metadata is required")
	}
	if err := validateJSONObject("stats totals", stats.Totals); err != nil {
		return err
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO llm_stats_totals (owner_id, scope, totals, updated_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (owner_id, scope) DO UPDATE SET totals = EXCLUDED.totals, updated_at = EXCLUDED.updated_at`,
		stats.OwnerID, stats.Scope, stats.Totals, stats.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save stats: %w", err)
	}
	return nil
}

func (store *Store) Stats(ctx context.Context, ownerID, scope string) (StatsRecord, error) {
	var stats StatsRecord
	err := store.pool.QueryRow(ctx, `
		SELECT owner_id, scope, totals, updated_at FROM llm_stats_totals WHERE owner_id = $1 AND scope = $2`, ownerID, scope).Scan(
		&stats.OwnerID, &stats.Scope, &stats.Totals, &stats.UpdatedAt,
	)
	if err != nil {
		return StatsRecord{}, notFound(err)
	}
	return stats, nil
}

func validateProfile(profile ProfileRecord) error {
	if err := validateIdentifier("owner ID", profile.OwnerID); err != nil {
		return err
	}
	if err := validateProfileIdentifier(profile.ID); err != nil {
		return err
	}
	if profile.CredentialID != "" {
		if err := validateIdentifier("credential ID", profile.CredentialID); err != nil {
			return err
		}
	}
	if err := validateJSONObject("profile document", profile.Document); err != nil {
		return err
	}
	if profile.CreatedAt.IsZero() || profile.UpdatedAt.Before(profile.CreatedAt) {
		return errors.New("postgres: valid profile timestamps are required")
	}
	return nil
}

func validateCredential(credential CredentialRecord) error {
	for name, value := range map[string]string{"owner ID": credential.OwnerID, "credential ID": credential.ID} {
		if err := validateIdentifier(name, value); err != nil {
			return err
		}
	}
	if credential.KeyID == "" || len(credential.KeyID) > 64 || len(credential.Nonce) != 12 || len(credential.Ciphertext) < 16 || !strings.HasPrefix(credential.Origin, "https://") {
		return errors.New("postgres: invalid encrypted credential metadata")
	}
	if err := validateJSONObject("credential metadata", credential.Metadata); err != nil {
		return err
	}
	if credential.CreatedAt.IsZero() || credential.UpdatedAt.Before(credential.CreatedAt) {
		return errors.New("postgres: valid credential timestamps are required")
	}
	return nil
}

func validateIdentifier(name, value string) error {
	if len(value) == 0 || len(value) > 128 || strings.TrimSpace(value) != value {
		return fmt.Errorf("postgres: valid %s is required", name)
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' || (index > 0 && character == '.') {
			continue
		}
		return fmt.Errorf("postgres: valid %s is required", name)
	}
	return nil
}

func validateProfileIdentifier(value string) error {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || len(value) > 1500 || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return errors.New("postgres: valid profile ID is required")
	}
	return nil
}

func validateJSONObject(name string, value json.RawMessage) error {
	if !json.Valid(value) {
		return fmt.Errorf("postgres: %s must be valid JSON", name)
	}
	var object map[string]any
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		return fmt.Errorf("postgres: %s must be a JSON object", name)
	}
	return nil
}
