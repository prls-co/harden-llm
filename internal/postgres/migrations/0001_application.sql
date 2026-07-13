CREATE TABLE users (
    id text PRIMARY KEY CHECK (id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'),
    email text NOT NULL UNIQUE CHECK (email = lower(email) AND email <> '' AND length(email) <= 320),
    password_hash text NOT NULL CHECK (password_hash LIKE '$argon2id$%'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at)
);

CREATE TABLE user_sessions (
    id text PRIMARY KEY CHECK (id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'),
    owner_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_digest bytea NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    CHECK (expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);
CREATE INDEX user_sessions_owner_expiry_idx ON user_sessions (owner_id, expires_at DESC);

CREATE TABLE llm_endpoint_credentials (
    owner_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id text NOT NULL CHECK (credential_id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'),
    key_id text NOT NULL CHECK (key_id <> '' AND length(key_id) <= 64),
    nonce bytea NOT NULL CHECK (octet_length(nonce) = 12),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) >= 16),
    normalized_origin text NOT NULL CHECK (normalized_origin LIKE 'https://%' AND length(normalized_origin) <= 2048),
    metadata jsonb NOT NULL CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
    PRIMARY KEY (owner_id, credential_id)
);

CREATE TABLE llm_profiles (
    owner_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    profile_id text NOT NULL CHECK (profile_id <> '' AND octet_length(profile_id) <= 1500),
    credential_id text,
    document jsonb NOT NULL CHECK (jsonb_typeof(document) = 'object'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
    PRIMARY KEY (owner_id, profile_id),
    FOREIGN KEY (owner_id, credential_id) REFERENCES llm_endpoint_credentials(owner_id, credential_id) ON DELETE RESTRICT
);
CREATE INDEX llm_profiles_owner_updated_idx ON llm_profiles (owner_id, updated_at DESC, profile_id);

CREATE TABLE llm_client_state (
    owner_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    document jsonb NOT NULL CHECK (jsonb_typeof(document) = 'object'),
    updated_at timestamptz NOT NULL
);

CREATE TABLE llm_runs (
    owner_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    run_id text NOT NULL CHECK (run_id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'),
    profile_id text NOT NULL,
    trace_id text NOT NULL CHECK (trace_id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'),
    status text NOT NULL CHECK (status IN ('succeeded', 'failed', 'timeout')),
    request jsonb NOT NULL CHECK (jsonb_typeof(request) = 'object'),
    result jsonb NOT NULL CHECK (jsonb_typeof(result) = 'object'),
    started_at timestamptz NOT NULL,
    completed_at timestamptz NOT NULL CHECK (completed_at >= started_at),
    PRIMARY KEY (owner_id, run_id)
);
CREATE UNIQUE INDEX llm_runs_owner_trace_idx ON llm_runs (owner_id, trace_id);
CREATE INDEX llm_runs_owner_history_idx ON llm_runs (owner_id, started_at DESC, run_id DESC);

CREATE TABLE llm_traces (
    owner_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    trace_id text NOT NULL CHECK (trace_id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'),
    record jsonb NOT NULL CHECK (jsonb_typeof(record) = 'object'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
    PRIMARY KEY (owner_id, trace_id)
);
CREATE INDEX llm_traces_owner_updated_idx ON llm_traces (owner_id, updated_at DESC, trace_id);

CREATE TABLE llm_trace_observations (
    owner_id text NOT NULL,
    trace_id text NOT NULL,
    sequence integer NOT NULL CHECK (sequence >= 0),
    observation_type text NOT NULL CHECK (observation_type <> '' AND length(observation_type) <= 64),
    data jsonb NOT NULL CHECK (jsonb_typeof(data) = 'object'),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (owner_id, trace_id, sequence),
    FOREIGN KEY (owner_id, trace_id) REFERENCES llm_traces(owner_id, trace_id) ON DELETE CASCADE
);

CREATE TABLE llm_artifacts (
    owner_id text NOT NULL,
    trace_id text NOT NULL,
    artifact_id text NOT NULL CHECK (artifact_id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'),
    kind text NOT NULL CHECK (kind IN ('trace', 'parse-failure-response', 'diagnostic-event')),
    object_key text NOT NULL UNIQUE CHECK (object_key <> '' AND length(object_key) <= 768 AND position('..' in object_key) = 0),
    content_type text NOT NULL CHECK (content_type = 'application/json'),
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    available boolean NOT NULL CHECK (available),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
    PRIMARY KEY (owner_id, trace_id, artifact_id),
    FOREIGN KEY (owner_id, trace_id) REFERENCES llm_traces(owner_id, trace_id) ON DELETE CASCADE
);
CREATE INDEX llm_artifacts_owner_trace_idx ON llm_artifacts (owner_id, trace_id, created_at, artifact_id);

CREATE TABLE llm_operation_cache (
    owner_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cache_version text NOT NULL CHECK (cache_version <> '' AND length(cache_version) <= 64),
    operation_hash text NOT NULL CHECK (operation_hash <> '' AND length(operation_hash) <= 128),
    operation jsonb NOT NULL CHECK (jsonb_typeof(operation) = 'object'),
    result jsonb NOT NULL CHECK (jsonb_typeof(result) = 'object'),
    usage jsonb NOT NULL CHECK (jsonb_typeof(usage) = 'object'),
    cost jsonb NOT NULL CHECK (jsonb_typeof(cost) = 'object'),
    provider_envelope jsonb NOT NULL CHECK (jsonb_typeof(provider_envelope) = 'object'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
    PRIMARY KEY (owner_id, cache_version, operation_hash)
);
CREATE INDEX llm_operation_cache_owner_updated_idx ON llm_operation_cache (owner_id, updated_at DESC);

CREATE TABLE llm_stats_totals (
    owner_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope <> '' AND length(scope) <= 128),
    totals jsonb NOT NULL CHECK (jsonb_typeof(totals) = 'object'),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (owner_id, scope)
);
