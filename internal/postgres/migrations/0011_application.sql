-- Fresh product-only schema. Versions 1-10 are retired; existing installations require a data reset.




CREATE TABLE public.llm_artifact_delete_batches (
    batch_id text NOT NULL,
    owner_id text NOT NULL,
    scope text NOT NULL,
    run_id text,
    trace_id text,
    state text NOT NULL,
    expected_artifact_count integer NOT NULL,
    deleted_run_count bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    completed_at timestamp with time zone,
    CONSTRAINT llm_artifact_delete_batches_batch_id_check CHECK (((batch_id <> ''::text) AND (length(batch_id) <= 256))),
    CONSTRAINT llm_artifact_delete_batches_check CHECK ((updated_at >= created_at)),
    CONSTRAINT llm_artifact_delete_batches_check1 CHECK ((((scope = 'execution'::text) AND (run_id IS NOT NULL) AND (trace_id IS NOT NULL)) OR ((scope = 'owner'::text) AND (run_id IS NULL) AND (trace_id IS NULL)) OR ((scope = 'reconciliation'::text) AND (run_id IS NOT NULL) AND (trace_id IS NOT NULL)))),
    CONSTRAINT llm_artifact_delete_batches_deleted_run_count_check CHECK ((deleted_run_count >= 0)),
    CONSTRAINT llm_artifact_delete_batches_expected_artifact_count_check CHECK ((expected_artifact_count >= 0)),
    CONSTRAINT llm_artifact_delete_batches_scope_check CHECK ((scope = ANY (ARRAY['execution'::text, 'owner'::text, 'reconciliation'::text]))),
    CONSTRAINT llm_artifact_delete_batches_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'object_applied'::text, 'completed'::text])))
);

CREATE TABLE public.llm_artifact_operations (
    operation_id text NOT NULL,
    batch_id text,
    action text NOT NULL,
    state text NOT NULL,
    owner_id text NOT NULL,
    run_id text NOT NULL,
    trace_id text NOT NULL,
    artifact_id text NOT NULL,
    kind text NOT NULL,
    object_key text NOT NULL,
    content_type text NOT NULL,
    sha256 text NOT NULL,
    size_bytes bigint NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    next_attempt_at timestamp with time zone NOT NULL,
    error_category text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    completed_at timestamp with time zone,
    CONSTRAINT llm_artifact_operations_action_check CHECK ((action = ANY (ARRAY['publish'::text, 'delete'::text]))),
    CONSTRAINT llm_artifact_operations_artifact_id_check CHECK ((artifact_id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'::text)),
    CONSTRAINT llm_artifact_operations_attempt_count_check CHECK ((attempt_count >= 0)),
    CONSTRAINT llm_artifact_operations_check CHECK ((updated_at >= created_at)),
    CONSTRAINT llm_artifact_operations_check1 CHECK ((((action = 'publish'::text) AND (batch_id IS NULL)) OR ((action = 'delete'::text) AND (batch_id IS NOT NULL)))),
    CONSTRAINT llm_artifact_operations_content_type_check CHECK ((content_type = 'application/json'::text)),
    CONSTRAINT llm_artifact_operations_error_category_check CHECK ((length(error_category) <= 64)),
    CONSTRAINT llm_artifact_operations_kind_check CHECK ((kind = ANY (ARRAY['trace'::text, 'parse-failure-response'::text, 'diagnostic-event'::text]))),
    CONSTRAINT llm_artifact_operations_object_key_check CHECK (((object_key <> ''::text) AND (length(object_key) <= 768) AND (POSITION(('..'::text) IN (object_key)) = 0))),
    CONSTRAINT llm_artifact_operations_operation_id_check CHECK ((operation_id ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT llm_artifact_operations_run_id_check CHECK (((run_id <> ''::text) AND (length(run_id) <= 256))),
    CONSTRAINT llm_artifact_operations_sha256_check CHECK ((sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT llm_artifact_operations_size_bytes_check CHECK ((size_bytes > 0)),
    CONSTRAINT llm_artifact_operations_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'object_applied'::text, 'completed'::text, 'failed'::text]))),
    CONSTRAINT llm_artifact_operations_trace_id_check CHECK (((trace_id <> ''::text) AND (length(trace_id) <= 256)))
);

CREATE TABLE public.llm_artifacts (
    owner_id text NOT NULL,
    trace_id text NOT NULL,
    artifact_id text NOT NULL,
    kind text NOT NULL,
    object_key text NOT NULL,
    content_type text NOT NULL,
    sha256 text NOT NULL,
    size_bytes bigint NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    state text NOT NULL,
    verified_at timestamp with time zone NOT NULL,
    CONSTRAINT llm_artifacts_artifact_id_check CHECK ((artifact_id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'::text)),
    CONSTRAINT llm_artifacts_check CHECK ((updated_at >= created_at)),
    CONSTRAINT llm_artifacts_content_type_check CHECK ((content_type = 'application/json'::text)),
    CONSTRAINT llm_artifacts_kind_check CHECK ((kind = ANY (ARRAY['trace'::text, 'parse-failure-response'::text, 'diagnostic-event'::text]))),
    CONSTRAINT llm_artifacts_object_key_check CHECK (((object_key <> ''::text) AND (length(object_key) <= 768) AND (POSITION(('..'::text) IN (object_key)) = 0))),
    CONSTRAINT llm_artifacts_sha256_check CHECK ((sha256 ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT llm_artifacts_size_bytes_check CHECK ((size_bytes > 0)),
    CONSTRAINT llm_artifacts_state_check CHECK ((state = ANY (ARRAY['available'::text, 'deleting'::text, 'unavailable'::text])))
);

CREATE TABLE public.llm_client_state (
    owner_id text NOT NULL,
    document jsonb NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT llm_client_state_document_check CHECK ((jsonb_typeof(document) = 'object'::text))
);

CREATE TABLE public.llm_endpoint_credentials (
    owner_id text NOT NULL,
    credential_id text NOT NULL,
    key_id text NOT NULL,
    nonce bytea NOT NULL,
    ciphertext bytea NOT NULL,
    normalized_origin text NOT NULL,
    metadata jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT llm_endpoint_credentials_check CHECK ((updated_at >= created_at)),
    CONSTRAINT llm_endpoint_credentials_ciphertext_check CHECK ((octet_length(ciphertext) >= 16)),
    CONSTRAINT llm_endpoint_credentials_credential_id_check CHECK ((credential_id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'::text)),
    CONSTRAINT llm_endpoint_credentials_key_id_check CHECK (((key_id <> ''::text) AND (length(key_id) <= 64))),
    CONSTRAINT llm_endpoint_credentials_metadata_check CHECK ((jsonb_typeof(metadata) = 'object'::text)),
    CONSTRAINT llm_endpoint_credentials_nonce_check CHECK ((octet_length(nonce) = 12)),
    CONSTRAINT llm_endpoint_credentials_normalized_origin_check CHECK (((normalized_origin ~~ 'https://%'::text) AND (length(normalized_origin) <= 2048)))
);

CREATE TABLE public.llm_operation_cache (
    owner_id text NOT NULL,
    cache_version text NOT NULL,
    operation_hash text NOT NULL,
    result jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT llm_operation_cache_cache_version_check CHECK (((cache_version <> ''::text) AND (length(cache_version) <= 64))),
    CONSTRAINT llm_operation_cache_check CHECK ((updated_at >= created_at)),
    CONSTRAINT llm_operation_cache_operation_hash_check CHECK (((operation_hash <> ''::text) AND (length(operation_hash) <= 128))),
    CONSTRAINT llm_operation_cache_result_check CHECK ((jsonb_typeof(result) = 'object'::text))
);

CREATE TABLE public.llm_profiles (
    owner_id text NOT NULL,
    profile_id text NOT NULL,
    credential_id text,
    document jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT llm_profiles_check CHECK ((updated_at >= created_at)),
    CONSTRAINT llm_profiles_document_check CHECK ((jsonb_typeof(document) = 'object'::text)),
    CONSTRAINT llm_profiles_profile_id_check CHECK (((profile_id <> ''::text) AND (octet_length(profile_id) <= 1500)))
);

CREATE TABLE public.llm_runs (
    owner_id text NOT NULL,
    run_id text NOT NULL,
    profile_id text NOT NULL,
    trace_id text NOT NULL,
    status text NOT NULL,
    request jsonb NOT NULL,
    result jsonb NOT NULL,
    started_at timestamp with time zone NOT NULL,
    completed_at timestamp with time zone NOT NULL,
    result_schema_version smallint DEFAULT 1 NOT NULL,
    selected_provider text,
    selected_protocol text,
    selected_endpoint text,
    selected_model_id text,
    result_source text,
    producer_profile_id text,
    producer_provider text,
    producer_protocol text,
    producer_endpoint text,
    producer_model_id text,
    provider_invoked boolean,
    result_usage_status text,
    result_input_tokens bigint,
    result_cache_read_tokens bigint,
    result_cache_creation_tokens bigint,
    result_output_tokens bigint,
    result_reasoning_tokens bigint,
    provider_usage_status text,
    provider_input_tokens bigint,
    provider_cache_read_tokens bigint,
    provider_cache_creation_tokens bigint,
    provider_output_tokens bigint,
    provider_reasoning_tokens bigint,
    result_cost_status text,
    result_known_cost_usd double precision,
    result_known_cost_observations bigint,
    result_unknown_cost_observations bigint,
    provider_cost_status text,
    provider_known_cost_usd double precision,
    provider_known_cost_observations bigint,
    provider_unknown_cost_observations bigint,
    cache_served boolean,
    total_call_duration_ms bigint,
    over_budget_ms bigint,
    CONSTRAINT llm_runs_check CHECK ((completed_at >= started_at)),
    CONSTRAINT llm_runs_over_budget_ms_check CHECK ((over_budget_ms >= 0)),
    CONSTRAINT llm_runs_provider_cache_creation_tokens_check CHECK ((provider_cache_creation_tokens >= 0)),
    CONSTRAINT llm_runs_provider_cache_read_tokens_check CHECK ((provider_cache_read_tokens >= 0)),
    CONSTRAINT llm_runs_provider_cost_status_check CHECK ((provider_cost_status = ANY (ARRAY['exact'::text, 'partial'::text, 'unknown'::text, 'unavailable'::text]))),
    CONSTRAINT llm_runs_provider_input_tokens_check CHECK ((provider_input_tokens >= 0)),
    CONSTRAINT llm_runs_provider_known_cost_observations_check CHECK ((provider_known_cost_observations >= 0)),
    CONSTRAINT llm_runs_provider_known_cost_usd_check CHECK ((provider_known_cost_usd >= (0)::double precision)),
    CONSTRAINT llm_runs_provider_output_tokens_check CHECK ((provider_output_tokens >= 0)),
    CONSTRAINT llm_runs_provider_reasoning_tokens_check CHECK ((provider_reasoning_tokens >= 0)),
    CONSTRAINT llm_runs_provider_unknown_cost_observations_check CHECK ((provider_unknown_cost_observations >= 0)),
    CONSTRAINT llm_runs_provider_usage_status_check CHECK ((provider_usage_status = ANY (ARRAY['complete'::text, 'partial'::text, 'unavailable'::text, 'inconsistent'::text]))),
    CONSTRAINT llm_runs_request_check CHECK ((jsonb_typeof(request) = 'object'::text)),
    CONSTRAINT llm_runs_result_cache_creation_tokens_check CHECK ((result_cache_creation_tokens >= 0)),
    CONSTRAINT llm_runs_result_cache_read_tokens_check CHECK ((result_cache_read_tokens >= 0)),
    CONSTRAINT llm_runs_result_check CHECK ((jsonb_typeof(result) = 'object'::text)),
    CONSTRAINT llm_runs_result_cost_status_check CHECK ((result_cost_status = ANY (ARRAY['exact'::text, 'partial'::text, 'unknown'::text, 'unavailable'::text]))),
    CONSTRAINT llm_runs_result_input_tokens_check CHECK ((result_input_tokens >= 0)),
    CONSTRAINT llm_runs_result_known_cost_observations_check CHECK ((result_known_cost_observations >= 0)),
    CONSTRAINT llm_runs_result_known_cost_usd_check CHECK ((result_known_cost_usd >= (0)::double precision)),
    CONSTRAINT llm_runs_result_output_tokens_check CHECK ((result_output_tokens >= 0)),
    CONSTRAINT llm_runs_result_reasoning_tokens_check CHECK ((result_reasoning_tokens >= 0)),
    CONSTRAINT llm_runs_result_schema_version_check CHECK ((result_schema_version = ANY (ARRAY[1, 4]))),
    CONSTRAINT llm_runs_result_source_check CHECK ((result_source = ANY (ARRAY['provider'::text, 'cache'::text, 'none'::text]))),
    CONSTRAINT llm_runs_result_unknown_cost_observations_check CHECK ((result_unknown_cost_observations >= 0)),
    CONSTRAINT llm_runs_result_usage_status_check CHECK ((result_usage_status = ANY (ARRAY['complete'::text, 'partial'::text, 'unavailable'::text, 'inconsistent'::text]))),
    CONSTRAINT llm_runs_run_id_check CHECK ((run_id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'::text)),
    CONSTRAINT llm_runs_status_check CHECK ((status = ANY (ARRAY['succeeded'::text, 'failed'::text, 'timeout'::text]))),
    CONSTRAINT llm_runs_total_call_duration_ms_check CHECK ((total_call_duration_ms >= 0)),
    CONSTRAINT llm_runs_trace_id_check CHECK ((trace_id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'::text)),
    CONSTRAINT llm_runs_v2_execution_fields CHECK (((result_schema_version = 1) OR ((selected_provider IS NOT NULL) AND (selected_protocol IS NOT NULL) AND (selected_endpoint IS NOT NULL) AND (selected_model_id IS NOT NULL) AND (result_source IS NOT NULL) AND (provider_invoked IS NOT NULL) AND (result_usage_status IS NOT NULL) AND (result_input_tokens IS NOT NULL) AND (result_cache_read_tokens IS NOT NULL) AND (result_cache_creation_tokens IS NOT NULL) AND (result_output_tokens IS NOT NULL) AND (result_reasoning_tokens IS NOT NULL) AND (provider_usage_status IS NOT NULL) AND (provider_input_tokens IS NOT NULL) AND (provider_cache_read_tokens IS NOT NULL) AND (provider_cache_creation_tokens IS NOT NULL) AND (provider_output_tokens IS NOT NULL) AND (provider_reasoning_tokens IS NOT NULL) AND (result_cost_status IS NOT NULL) AND (result_known_cost_usd IS NOT NULL) AND (result_known_cost_observations IS NOT NULL) AND (result_unknown_cost_observations IS NOT NULL) AND (provider_cost_status IS NOT NULL) AND (provider_known_cost_usd IS NOT NULL) AND (provider_known_cost_observations IS NOT NULL) AND (provider_unknown_cost_observations IS NOT NULL) AND (cache_served IS NOT NULL) AND (total_call_duration_ms IS NOT NULL) AND (over_budget_ms IS NOT NULL))))
);

CREATE TABLE public.llm_trace_observations (
    owner_id text NOT NULL,
    trace_id text NOT NULL,
    sequence integer NOT NULL,
    observation_type text NOT NULL,
    data jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT llm_trace_observations_data_check CHECK ((jsonb_typeof(data) = 'object'::text)),
    CONSTRAINT llm_trace_observations_observation_type_check CHECK (((observation_type <> ''::text) AND (length(observation_type) <= 64))),
    CONSTRAINT llm_trace_observations_sequence_check CHECK ((sequence >= 0))
);

CREATE TABLE public.llm_traces (
    owner_id text NOT NULL,
    trace_id text NOT NULL,
    record jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    run_id text NOT NULL,
    CONSTRAINT llm_traces_check CHECK ((updated_at >= created_at)),
    CONSTRAINT llm_traces_record_check CHECK ((jsonb_typeof(record) = 'object'::text)),
    CONSTRAINT llm_traces_trace_id_check CHECK ((trace_id ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$'::text))
);

ALTER TABLE ONLY public.llm_artifact_delete_batches
    ADD CONSTRAINT llm_artifact_delete_batches_pkey PRIMARY KEY (batch_id);

ALTER TABLE ONLY public.llm_artifact_operations
    ADD CONSTRAINT llm_artifact_operations_action_owner_id_object_key_key UNIQUE (action, owner_id, object_key);

ALTER TABLE ONLY public.llm_artifact_operations
    ADD CONSTRAINT llm_artifact_operations_pkey PRIMARY KEY (operation_id);

ALTER TABLE ONLY public.llm_artifacts
    ADD CONSTRAINT llm_artifacts_object_key_key UNIQUE (object_key);

ALTER TABLE ONLY public.llm_artifacts
    ADD CONSTRAINT llm_artifacts_pkey PRIMARY KEY (owner_id, trace_id, artifact_id);

ALTER TABLE ONLY public.llm_client_state
    ADD CONSTRAINT llm_client_state_pkey PRIMARY KEY (owner_id);

ALTER TABLE ONLY public.llm_endpoint_credentials
    ADD CONSTRAINT llm_endpoint_credentials_pkey PRIMARY KEY (owner_id, credential_id);

ALTER TABLE ONLY public.llm_operation_cache
    ADD CONSTRAINT llm_operation_cache_pkey PRIMARY KEY (owner_id, cache_version, operation_hash);

ALTER TABLE ONLY public.llm_profiles
    ADD CONSTRAINT llm_profiles_pkey PRIMARY KEY (owner_id, profile_id);

ALTER TABLE ONLY public.llm_runs
    ADD CONSTRAINT llm_runs_owner_run_trace_key UNIQUE (owner_id, run_id, trace_id);

ALTER TABLE ONLY public.llm_runs
    ADD CONSTRAINT llm_runs_pkey PRIMARY KEY (owner_id, run_id);

ALTER TABLE ONLY public.llm_trace_observations
    ADD CONSTRAINT llm_trace_observations_pkey PRIMARY KEY (owner_id, trace_id, sequence);

ALTER TABLE ONLY public.llm_traces
    ADD CONSTRAINT llm_traces_pkey PRIMARY KEY (owner_id, trace_id);

CREATE UNIQUE INDEX llm_artifact_delete_batches_active_execution_idx ON public.llm_artifact_delete_batches USING btree (owner_id, run_id) WHERE ((scope = 'execution'::text) AND (state <> 'completed'::text));

CREATE UNIQUE INDEX llm_artifact_delete_batches_active_owner_idx ON public.llm_artifact_delete_batches USING btree (owner_id) WHERE ((scope = 'owner'::text) AND (state <> 'completed'::text));

CREATE UNIQUE INDEX llm_artifact_delete_batches_active_reconciliation_idx ON public.llm_artifact_delete_batches USING btree (owner_id, trace_id) WHERE ((scope = 'reconciliation'::text) AND (state <> 'completed'::text));

CREATE INDEX llm_artifact_operations_batch_idx ON public.llm_artifact_operations USING btree (batch_id, state, operation_id) WHERE (batch_id IS NOT NULL);

CREATE INDEX llm_artifact_operations_reconcile_idx ON public.llm_artifact_operations USING btree (state, next_attempt_at, created_at, operation_id) WHERE (state = ANY (ARRAY['pending'::text, 'object_applied'::text]));

CREATE INDEX llm_artifacts_integrity_audit_idx ON public.llm_artifacts USING btree (verified_at, owner_id, trace_id, artifact_id) WHERE (state = 'available'::text);

CREATE INDEX llm_artifacts_owner_trace_idx ON public.llm_artifacts USING btree (owner_id, trace_id, created_at, artifact_id);

CREATE INDEX llm_operation_cache_owner_updated_idx ON public.llm_operation_cache USING btree (owner_id, updated_at DESC);

CREATE INDEX llm_profiles_owner_updated_idx ON public.llm_profiles USING btree (owner_id, updated_at DESC, profile_id);

CREATE INDEX llm_runs_owner_history_idx ON public.llm_runs USING btree (owner_id, started_at DESC, run_id DESC);

CREATE UNIQUE INDEX llm_runs_owner_trace_idx ON public.llm_runs USING btree (owner_id, trace_id);

CREATE UNIQUE INDEX llm_traces_owner_run_idx ON public.llm_traces USING btree (owner_id, run_id) WHERE (run_id IS NOT NULL);

CREATE INDEX llm_traces_owner_updated_idx ON public.llm_traces USING btree (owner_id, updated_at DESC, trace_id);

ALTER TABLE ONLY public.llm_artifact_operations
    ADD CONSTRAINT llm_artifact_operations_batch_id_fkey FOREIGN KEY (batch_id) REFERENCES public.llm_artifact_delete_batches(batch_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.llm_artifacts
    ADD CONSTRAINT llm_artifacts_owner_id_trace_id_fkey FOREIGN KEY (owner_id, trace_id) REFERENCES public.llm_traces(owner_id, trace_id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE ONLY public.llm_profiles
    ADD CONSTRAINT llm_profiles_owner_id_credential_id_fkey FOREIGN KEY (owner_id, credential_id) REFERENCES public.llm_endpoint_credentials(owner_id, credential_id) ON UPDATE CASCADE ON DELETE RESTRICT;

ALTER TABLE ONLY public.llm_trace_observations
    ADD CONSTRAINT llm_trace_observations_owner_id_trace_id_fkey FOREIGN KEY (owner_id, trace_id) REFERENCES public.llm_traces(owner_id, trace_id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE ONLY public.llm_traces
    ADD CONSTRAINT llm_traces_execution_fk FOREIGN KEY (owner_id, run_id, trace_id) REFERENCES public.llm_runs(owner_id, run_id, trace_id) ON UPDATE CASCADE ON DELETE CASCADE;
