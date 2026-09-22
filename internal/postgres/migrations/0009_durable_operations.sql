-- Durable Harden operation admission and publication. The operation identity is
-- account + service + operation ID; the customer token never enters this table.
CREATE TABLE harden_operations (
    account_id text NOT NULL,
    service_name text NOT NULL,
    operation_id text NOT NULL,
    input_digest text NOT NULL,
    status text NOT NULL,
    request jsonb NOT NULL,
    result jsonb,
    error_code text,
    error_message text,
    run_id text,
    trace_id text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, service_name, operation_id),
    CONSTRAINT harden_operations_status_check CHECK (status IN ('accepted', 'running', 'succeeded', 'failed', 'cancelled', 'unresolved')),
    CONSTRAINT harden_operations_digest_check CHECK (input_digest ~ '^[a-f0-9]{64}$'),
    CONSTRAINT harden_operations_identity_check CHECK (length(account_id) BETWEEN 1 AND 128 AND length(service_name) BETWEEN 1 AND 64 AND length(operation_id) BETWEEN 1 AND 128),
    CONSTRAINT harden_operations_times_check CHECK (updated_at >= created_at)
);
CREATE INDEX harden_operations_status_idx ON harden_operations (account_id, service_name, status, updated_at DESC);
