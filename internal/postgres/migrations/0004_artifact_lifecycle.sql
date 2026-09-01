ALTER TABLE llm_artifacts
    ADD COLUMN state text;
UPDATE llm_artifacts SET state = 'available';
ALTER TABLE llm_artifacts
    ALTER COLUMN state SET NOT NULL,
    ADD CONSTRAINT llm_artifacts_state_check CHECK (state IN ('available', 'deleting', 'unavailable')),
    DROP COLUMN available;
ALTER TABLE llm_artifacts ADD COLUMN verified_at timestamptz;
UPDATE llm_artifacts SET verified_at = updated_at;
ALTER TABLE llm_artifacts ALTER COLUMN verified_at SET NOT NULL;
CREATE INDEX llm_artifacts_integrity_audit_idx
    ON llm_artifacts (verified_at, owner_id, trace_id, artifact_id)
    WHERE state = 'available';

CREATE TABLE llm_artifact_delete_batches (
    batch_id text PRIMARY KEY CHECK (batch_id <> '' AND length(batch_id) <= 256),
    owner_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope IN ('execution', 'owner', 'reconciliation')),
    run_id text,
    trace_id text,
    state text NOT NULL CHECK (state IN ('pending', 'object_applied', 'completed')),
    expected_artifact_count integer NOT NULL CHECK (expected_artifact_count >= 0),
    deleted_run_count bigint NOT NULL DEFAULT 0 CHECK (deleted_run_count >= 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
    completed_at timestamptz,
    CHECK (
        (scope = 'execution' AND run_id IS NOT NULL AND trace_id IS NOT NULL) OR
        (scope = 'owner' AND run_id IS NULL AND trace_id IS NULL) OR
        (scope = 'reconciliation' AND run_id IS NOT NULL AND trace_id IS NOT NULL)
    )
);
CREATE UNIQUE INDEX llm_artifact_delete_batches_active_execution_idx
    ON llm_artifact_delete_batches (owner_id, run_id)
    WHERE scope = 'execution' AND state <> 'completed';
CREATE UNIQUE INDEX llm_artifact_delete_batches_active_owner_idx
    ON llm_artifact_delete_batches (owner_id)
    WHERE scope = 'owner' AND state <> 'completed';
CREATE UNIQUE INDEX llm_artifact_delete_batches_active_reconciliation_idx
    ON llm_artifact_delete_batches (owner_id, trace_id)
    WHERE scope = 'reconciliation' AND state <> 'completed';

CREATE TABLE llm_artifact_operations (
    operation_id text PRIMARY KEY CHECK (operation_id ~ '^[0-9a-f]{64}$'),
    batch_id text REFERENCES llm_artifact_delete_batches(batch_id) ON DELETE CASCADE,
    action text NOT NULL CHECK (action IN ('publish', 'delete')),
    state text NOT NULL CHECK (state IN ('pending', 'object_applied', 'completed', 'failed')),
    owner_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    run_id text NOT NULL CHECK (run_id <> '' AND length(run_id) <= 256),
    trace_id text NOT NULL CHECK (trace_id <> '' AND length(trace_id) <= 256),
    artifact_id text NOT NULL CHECK (artifact_id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'),
    kind text NOT NULL CHECK (kind IN ('trace', 'parse-failure-response', 'diagnostic-event')),
    object_key text NOT NULL CHECK (object_key <> '' AND length(object_key) <= 768 AND position('..' in object_key) = 0),
    content_type text NOT NULL CHECK (content_type = 'application/json'),
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL,
    error_category text NOT NULL DEFAULT '' CHECK (length(error_category) <= 64),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
    completed_at timestamptz,
    CHECK ((action = 'publish' AND batch_id IS NULL) OR (action = 'delete' AND batch_id IS NOT NULL)),
    UNIQUE (action, owner_id, object_key)
);
CREATE INDEX llm_artifact_operations_reconcile_idx
    ON llm_artifact_operations (state, next_attempt_at, created_at, operation_id)
    WHERE state IN ('pending', 'object_applied');
CREATE INDEX llm_artifact_operations_batch_idx
    ON llm_artifact_operations (batch_id, state, operation_id)
    WHERE batch_id IS NOT NULL;
