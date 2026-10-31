CREATE TABLE IF NOT EXISTS batch_pool_generations (
 pool_id text PRIMARY KEY CHECK(length(pool_id) BETWEEN 1 AND 128),
 generation bigint NOT NULL CHECK(generation>0),
 mode text NOT NULL CHECK(mode IN ('active','quiescing')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 updated_at timestamptz NOT NULL
);
ALTER TABLE batch_jobs ADD COLUMN IF NOT EXISTS pool_id text NOT NULL DEFAULT '';
ALTER TABLE batch_jobs ADD COLUMN IF NOT EXISTS enqueue_sequence bigserial;
ALTER TABLE batch_items ADD COLUMN IF NOT EXISTS reservation_ordinal bigint NOT NULL DEFAULT 0 CHECK(reservation_ordinal>=0);
ALTER TABLE batch_items ADD COLUMN IF NOT EXISTS current_permit uuid;
CREATE INDEX IF NOT EXISTS batch_jobs_pool_queue ON batch_jobs(pool_id,api_key_hash,enqueue_sequence,id);
CREATE INDEX IF NOT EXISTS batch_items_ready ON batch_items(api_key_hash,job_id,state,item_index);
CREATE TABLE IF NOT EXISTS batch_attempts (
 permit_id uuid PRIMARY KEY,
 pool_id text NOT NULL REFERENCES batch_pool_generations(pool_id),
 generation bigint NOT NULL CHECK(generation>0),
 sequence bigint NOT NULL CHECK(sequence>0),
 api_key_hash text NOT NULL CHECK(api_key_hash ~ '^[0-9a-f]{64}$'),
 kind text NOT NULL CHECK(kind IN ('job','ordinary')),
 job_id uuid,
 item_index integer,
 request_id uuid,
 workflow_id text NOT NULL UNIQUE CHECK(length(workflow_id) BETWEEN 1 AND 256),
 granted_at timestamptz NOT NULL,
 start_by timestamptz NOT NULL,
 execution_deadline timestamptz NOT NULL,
 budget_ms bigint NOT NULL CHECK(budget_ms BETWEEN 1 AND 900000),
 admission_epoch bigint NOT NULL CHECK(admission_epoch>=0),
 policy_revision bigint NOT NULL CHECK(policy_revision>0),
 probe boolean NOT NULL,
 ordinal bigint NOT NULL CHECK(ordinal>0),
 state text NOT NULL CHECK(state IN ('reserved','dispatching','settled')),
 claim_id uuid,
 claimed_at timestamptz,
 dispatch_at timestamptz,
 local_stopped_at timestamptz,
 settlement_revision bigint NOT NULL DEFAULT 0 CHECK(settlement_revision>=0),
 observation jsonb CHECK(jsonb_typeof(observation)='object' AND octet_length(observation::text)<=65536),
 UNIQUE(pool_id,sequence),
 UNIQUE(api_key_hash,job_id,item_index,ordinal),
 FOREIGN KEY(api_key_hash,job_id,item_index) REFERENCES batch_items(api_key_hash,job_id,item_index),
 CHECK((kind='job' AND job_id IS NOT NULL AND item_index BETWEEN 0 AND 999 AND request_id IS NULL) OR (kind='ordinary' AND job_id IS NULL AND item_index IS NULL AND request_id IS NOT NULL)),
 CHECK((claim_id IS NULL AND claimed_at IS NULL AND dispatch_at IS NULL) OR (claim_id IS NOT NULL AND claimed_at IS NOT NULL)),
 CHECK(state<>'reserved' OR claim_id IS NULL),
 CHECK(state<>'settled' OR (settlement_revision>0 AND observation IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS batch_attempts_unsettled_item ON batch_attempts(api_key_hash,job_id,item_index) WHERE kind='job' AND state<>'settled';
CREATE UNIQUE INDEX IF NOT EXISTS batch_attempts_ordinary_request ON batch_attempts(pool_id,api_key_hash,request_id) WHERE kind='ordinary';
CREATE INDEX IF NOT EXISTS batch_attempts_pool_unsettled ON batch_attempts(pool_id,state,permit_id);
