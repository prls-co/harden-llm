ALTER TABLE llm_runs
    ADD COLUMN result_schema_version smallint NOT NULL DEFAULT 1 CHECK (result_schema_version IN (1, 2)),
    ADD COLUMN selected_provider text,
    ADD COLUMN selected_protocol text,
    ADD COLUMN selected_endpoint text,
    ADD COLUMN selected_model_id text,
    ADD COLUMN result_source text CHECK (result_source IN ('provider', 'cache', 'none')),
    ADD COLUMN producer_profile_id text,
    ADD COLUMN producer_provider text,
    ADD COLUMN producer_protocol text,
    ADD COLUMN producer_endpoint text,
    ADD COLUMN producer_model_id text,
    ADD COLUMN provider_invoked boolean,
    ADD COLUMN result_usage_status text CHECK (result_usage_status IN ('complete', 'partial', 'unavailable', 'inconsistent')),
    ADD COLUMN result_input_tokens bigint CHECK (result_input_tokens >= 0),
    ADD COLUMN result_cache_read_tokens bigint CHECK (result_cache_read_tokens >= 0),
    ADD COLUMN result_cache_creation_tokens bigint CHECK (result_cache_creation_tokens >= 0),
    ADD COLUMN result_output_tokens bigint CHECK (result_output_tokens >= 0),
    ADD COLUMN result_reasoning_tokens bigint CHECK (result_reasoning_tokens >= 0),
    ADD COLUMN provider_usage_status text CHECK (provider_usage_status IN ('complete', 'partial', 'unavailable', 'inconsistent')),
    ADD COLUMN provider_input_tokens bigint CHECK (provider_input_tokens >= 0),
    ADD COLUMN provider_cache_read_tokens bigint CHECK (provider_cache_read_tokens >= 0),
    ADD COLUMN provider_cache_creation_tokens bigint CHECK (provider_cache_creation_tokens >= 0),
    ADD COLUMN provider_output_tokens bigint CHECK (provider_output_tokens >= 0),
    ADD COLUMN provider_reasoning_tokens bigint CHECK (provider_reasoning_tokens >= 0),
    ADD COLUMN result_cost_status text CHECK (result_cost_status IN ('exact', 'partial', 'unknown', 'unavailable')),
    ADD COLUMN result_known_cost_usd double precision CHECK (result_known_cost_usd >= 0),
    ADD COLUMN result_known_cost_observations bigint CHECK (result_known_cost_observations >= 0),
    ADD COLUMN result_unknown_cost_observations bigint CHECK (result_unknown_cost_observations >= 0),
    ADD COLUMN provider_cost_status text CHECK (provider_cost_status IN ('exact', 'partial', 'unknown', 'unavailable')),
    ADD COLUMN provider_known_cost_usd double precision CHECK (provider_known_cost_usd >= 0),
    ADD COLUMN provider_known_cost_observations bigint CHECK (provider_known_cost_observations >= 0),
    ADD COLUMN provider_unknown_cost_observations bigint CHECK (provider_unknown_cost_observations >= 0),
    ADD COLUMN cache_served boolean,
    ADD COLUMN total_call_duration_ms bigint CHECK (total_call_duration_ms >= 0),
    ADD COLUMN over_budget_ms bigint CHECK (over_budget_ms >= 0);

-- Retained v1 data receives only values that were actually captured. Missing
-- or malformed usage remains unavailable and unknown cost remains unknown.
UPDATE llm_runs
SET
    selected_provider = NULLIF(result->>'provider', ''),
    selected_protocol = NULLIF(result->>'apiInferenceType', ''),
    selected_endpoint = NULLIF(result->>'providerBaseUrl', ''),
    selected_model_id = NULLIF(result->>'modelId', ''),
    result_source = CASE
        WHEN COALESCE(result #>> '{cache,served}', 'false') = 'true' THEN 'cache'
        WHEN jsonb_array_length(CASE WHEN jsonb_typeof(result->'attempts') = 'array' THEN result->'attempts' ELSE '[]'::jsonb END) > 0 THEN 'provider'
        ELSE 'none'
    END,
    provider_invoked = jsonb_array_length(CASE WHEN jsonb_typeof(result->'attempts') = 'array' THEN result->'attempts' ELSE '[]'::jsonb END) > 0,
    result_usage_status = CASE
        WHEN jsonb_typeof(result->'usage') = 'object'
         AND result #>> '{usage,inputTokens}' ~ '^[0-9]{1,18}$'
         AND result #>> '{usage,cacheReadTokens}' ~ '^[0-9]{1,18}$'
         AND result #>> '{usage,cacheCreationTokens}' ~ '^[0-9]{1,18}$'
         AND result #>> '{usage,outputTokens}' ~ '^[0-9]{1,18}$'
         AND result #>> '{usage,reasoningTokens}' ~ '^[0-9]{1,18}$'
        THEN 'complete' ELSE 'unavailable' END,
    result_input_tokens = CASE WHEN result #>> '{usage,inputTokens}' ~ '^[0-9]{1,18}$' THEN (result #>> '{usage,inputTokens}')::bigint ELSE 0 END,
    result_cache_read_tokens = CASE WHEN result #>> '{usage,cacheReadTokens}' ~ '^[0-9]{1,18}$' THEN (result #>> '{usage,cacheReadTokens}')::bigint ELSE 0 END,
    result_cache_creation_tokens = CASE WHEN result #>> '{usage,cacheCreationTokens}' ~ '^[0-9]{1,18}$' THEN (result #>> '{usage,cacheCreationTokens}')::bigint ELSE 0 END,
    result_output_tokens = CASE WHEN result #>> '{usage,outputTokens}' ~ '^[0-9]{1,18}$' THEN (result #>> '{usage,outputTokens}')::bigint ELSE 0 END,
    result_reasoning_tokens = CASE WHEN result #>> '{usage,reasoningTokens}' ~ '^[0-9]{1,18}$' THEN (result #>> '{usage,reasoningTokens}')::bigint ELSE 0 END,
    result_cost_status = CASE
        WHEN COALESCE(result #>> '{cost,known}', 'false') = 'true'
         AND result #>> '{cost,totalUsd}' ~ '^[0-9]{1,12}([.][0-9]{1,18})?$'
        THEN 'exact' ELSE 'unknown' END,
    result_known_cost_usd = CASE
        WHEN COALESCE(result #>> '{cost,known}', 'false') = 'true'
         AND result #>> '{cost,totalUsd}' ~ '^[0-9]{1,12}([.][0-9]{1,18})?$'
        THEN (result #>> '{cost,totalUsd}')::double precision ELSE 0 END,
    result_known_cost_observations = CASE
        WHEN COALESCE(result #>> '{cost,known}', 'false') = 'true'
         AND result #>> '{cost,totalUsd}' ~ '^[0-9]{1,12}([.][0-9]{1,18})?$'
        THEN 1 ELSE 0 END,
    result_unknown_cost_observations = CASE
        WHEN COALESCE(result #>> '{cost,known}', 'false') = 'true'
         AND result #>> '{cost,totalUsd}' ~ '^[0-9]{1,12}([.][0-9]{1,18})?$'
        THEN 0 ELSE 1 END,
    cache_served = COALESCE(result #>> '{cache,served}', 'false') = 'true',
    total_call_duration_ms = CASE WHEN result->>'totalCallDurationMs' ~ '^[0-9]{1,18}$' THEN (result->>'totalCallDurationMs')::bigint ELSE 0 END,
    over_budget_ms = CASE WHEN result->>'overBudgetMs' ~ '^[0-9]{1,18}$' THEN (result->>'overBudgetMs')::bigint ELSE 0 END;

UPDATE llm_runs
SET
    provider_usage_status = CASE WHEN provider_invoked THEN result_usage_status ELSE 'unavailable' END,
    provider_input_tokens = CASE WHEN provider_invoked THEN result_input_tokens ELSE 0 END,
    provider_cache_read_tokens = CASE WHEN provider_invoked THEN result_cache_read_tokens ELSE 0 END,
    provider_cache_creation_tokens = CASE WHEN provider_invoked THEN result_cache_creation_tokens ELSE 0 END,
    provider_output_tokens = CASE WHEN provider_invoked THEN result_output_tokens ELSE 0 END,
    provider_reasoning_tokens = CASE WHEN provider_invoked THEN result_reasoning_tokens ELSE 0 END,
    provider_cost_status = CASE WHEN provider_invoked THEN result_cost_status ELSE 'unavailable' END,
    provider_known_cost_usd = CASE WHEN provider_invoked THEN result_known_cost_usd ELSE 0 END,
    provider_known_cost_observations = CASE WHEN provider_invoked THEN result_known_cost_observations ELSE 0 END,
    provider_unknown_cost_observations = CASE WHEN provider_invoked THEN result_unknown_cost_observations ELSE 0 END;

ALTER TABLE llm_runs ADD CONSTRAINT llm_runs_v2_execution_fields CHECK (
    result_schema_version = 1 OR (
        selected_provider IS NOT NULL AND selected_protocol IS NOT NULL AND selected_endpoint IS NOT NULL AND selected_model_id IS NOT NULL AND
        result_source IS NOT NULL AND provider_invoked IS NOT NULL AND
        result_usage_status IS NOT NULL AND result_input_tokens IS NOT NULL AND result_cache_read_tokens IS NOT NULL AND result_cache_creation_tokens IS NOT NULL AND result_output_tokens IS NOT NULL AND result_reasoning_tokens IS NOT NULL AND
        provider_usage_status IS NOT NULL AND provider_input_tokens IS NOT NULL AND provider_cache_read_tokens IS NOT NULL AND provider_cache_creation_tokens IS NOT NULL AND provider_output_tokens IS NOT NULL AND provider_reasoning_tokens IS NOT NULL AND
        result_cost_status IS NOT NULL AND result_known_cost_usd IS NOT NULL AND result_known_cost_observations IS NOT NULL AND result_unknown_cost_observations IS NOT NULL AND
        provider_cost_status IS NOT NULL AND provider_known_cost_usd IS NOT NULL AND provider_known_cost_observations IS NOT NULL AND provider_unknown_cost_observations IS NOT NULL AND
        cache_served IS NOT NULL AND total_call_duration_ms IS NOT NULL AND over_budget_ms IS NOT NULL
    )
);

ALTER TABLE llm_traces ADD COLUMN run_id text;
UPDATE llm_traces AS trace
SET run_id = run.run_id
FROM llm_runs AS run
WHERE trace.owner_id = run.owner_id AND trace.trace_id = run.trace_id;
CREATE UNIQUE INDEX llm_traces_owner_run_idx ON llm_traces (owner_id, run_id) WHERE run_id IS NOT NULL;
ALTER TABLE llm_traces ADD CONSTRAINT llm_traces_run_fk
    FOREIGN KEY (owner_id, run_id) REFERENCES llm_runs(owner_id, run_id)
    ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED;
