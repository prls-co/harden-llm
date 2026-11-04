CREATE TABLE hllm_batch_schema_version(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton), version integer NOT NULL CHECK(version=1));
CREATE TABLE batch_jobs (
 api_key_hash text NOT NULL CHECK(api_key_hash ~ '^[0-9a-f]{64}$'),
 id uuid NOT NULL,
 idempotency_hash text NOT NULL,
 input_digest text NOT NULL,
 item_count integer NOT NULL CHECK(item_count BETWEEN 1 AND 1000),
 workflow_id text NOT NULL,
 created_at timestamptz NOT NULL,
 deadline timestamptz NOT NULL,
 last_heartbeat timestamptz,
 last_progress timestamptz,
 waiting_reason text NOT NULL DEFAULT '',
 next_attempt_at timestamptz,
 PRIMARY KEY(api_key_hash,id), UNIQUE(api_key_hash,idempotency_hash)
);
CREATE TABLE batch_items (
 api_key_hash text NOT NULL,
 job_id uuid NOT NULL,
 item_index integer NOT NULL CHECK(item_index BETWEEN 0 AND 999),
 custom_id text NOT NULL CHECK(length(custom_id) BETWEEN 1 AND 128),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','active','waiting','succeeded','failed')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 queued_at timestamptz NOT NULL DEFAULT now(),
 outcome jsonb NOT NULL DEFAULT '{}',
 PRIMARY KEY(api_key_hash,job_id,item_index), UNIQUE(api_key_hash,job_id,custom_id),
 FOREIGN KEY(api_key_hash,job_id) REFERENCES batch_jobs(api_key_hash,id) ON DELETE CASCADE
);
INSERT INTO hllm_batch_schema_version VALUES(true,1);
