CREATE TABLE hllm_schema_version (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  version integer NOT NULL CHECK (version = 1)
);
CREATE TABLE credentials (
  api_key_hash text NOT NULL CHECK (api_key_hash ~ '^[0-9a-f]{64}$'),
  id text NOT NULL,
  origin text NOT NULL,
  key_id text NOT NULL CHECK (key_id = 'primary'),
  nonce bytea NOT NULL CHECK (octet_length(nonce) = 12),
  ciphertext bytea NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (api_key_hash, id)
);
CREATE TABLE profiles (
  api_key_hash text NOT NULL CHECK (api_key_hash ~ '^[0-9a-f]{64}$'),
  id uuid NOT NULL,
  name text NOT NULL,
  preset_id text,
  definition jsonb NOT NULL CHECK (jsonb_typeof(definition) = 'object'),
  inputs jsonb NOT NULL CHECK (jsonb_typeof(inputs) = 'object'),
  credential_source text NOT NULL CHECK (credential_source IN ('deployment', 'entered')),
  deployment_upstream_id text,
  entered_credential_id text,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (api_key_hash, id),
  FOREIGN KEY (api_key_hash, entered_credential_id) REFERENCES credentials (api_key_hash, id),
  CHECK ((credential_source = 'deployment' AND deployment_upstream_id IS NOT NULL AND entered_credential_id IS NULL)
      OR (credential_source = 'entered' AND entered_credential_id IS NOT NULL AND deployment_upstream_id IS NULL))
);
CREATE TABLE hidden_presets (
  api_key_hash text NOT NULL CHECK (api_key_hash ~ '^[0-9a-f]{64}$'),
  preset_id text NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (api_key_hash, preset_id)
);
CREATE TABLE drafts (
  api_key_hash text NOT NULL CHECK (api_key_hash ~ '^[0-9a-f]{64}$'),
  draft_key text NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  updated_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (api_key_hash, draft_key)
);
CREATE TABLE cache_entries (
  api_key_hash text NOT NULL CHECK (api_key_hash ~ '^[0-9a-f]{64}$'),
  namespace text NOT NULL,
  operation_hash text NOT NULL,
  schema_version integer NOT NULL,
  record jsonb NOT NULL CHECK (jsonb_typeof(record) = 'object'),
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (api_key_hash, namespace, operation_hash)
);
CREATE INDEX cache_entries_expiry ON cache_entries (api_key_hash, expires_at);
CREATE INDEX drafts_expiry ON drafts (api_key_hash, expires_at);
CREATE TABLE history (
  api_key_hash text NOT NULL CHECK (api_key_hash ~ '^[0-9a-f]{64}$'),
  run_id uuid NOT NULL,
  schema_version integer NOT NULL CHECK (schema_version = 1),
  status text NOT NULL CHECK (status IN ('succeeded', 'failed', 'timeout')),
  started_at timestamptz NOT NULL,
  finished_at timestamptz NOT NULL CHECK (finished_at >= started_at),
  summary jsonb NOT NULL CHECK (jsonb_typeof(summary) = 'object'),
  request json NOT NULL CHECK (json_typeof(request) = 'object'),
  result json NOT NULL CHECK (json_typeof(result) = 'object'),
  PRIMARY KEY (api_key_hash, run_id)
);
CREATE INDEX history_page ON history (api_key_hash, started_at DESC, run_id DESC);
INSERT INTO hllm_schema_version (singleton, version) VALUES (true, 1);
