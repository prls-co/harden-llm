-- ADR-HLLM-020. Historical mapping only: current defaults and validation live
-- in internal/retry. Store.Migrate owns this transaction, lock and version.
DO $$
DECLARE
    item record;
    setting record;
    source jsonb;
    options jsonb;
    nested jsonb;
    migrated jsonb;
    policy jsonb;
    category_enabled jsonb;
    categories jsonb;
    attempts jsonb;
    repair jsonb;
    max_attempts integer;
    base_delay integer;
    max_delay integer;
    field text;
    identity text;
    delay_names text[];
    retry_names text[];
    retry_categories text[] := ARRAY['network','rate_limit','server_error','empty_response','provider_retry'];
    retired text[] := ARRAY[
        'maxAttempts','maxRetries','max_retries','baseDelayMs','maxDelayMs',
        'initialBackoffMs','maximumBackoffMs','enableRetryOn429','enableRetryOn5xx',
        'enableRetryOnNetworkError','enableRetryOnParseError','structuredRepairRetry',
        'structuredRepair','retryNetwork','retryRateLimit','retryServerError',
        'retryEmpty','retryParse','repairEscalation','backupProfiles','retryPolicy','recoveryPolicy'
    ];
    category_index integer;
BEGIN
    FOR item IN
        SELECT 'profile' AS kind, owner_id, profile_id AS id, document FROM llm_profiles
        UNION ALL
        SELECT 'state', owner_id, owner_id, document FROM llm_client_state
        ORDER BY kind, owner_id, id
    LOOP
        identity := item.kind || ' ' || item.owner_id || '/' || item.id;
        migrated := item.document;
        IF migrated->'schemaVersion' IS DISTINCT FROM '1'::jsonb THEN
            RAISE EXCEPTION 'recovery migration: % field schemaVersion must be 1', identity;
        END IF;
        IF migrated ? 'recoveryPolicy' THEN
            RAISE EXCEPTION 'recovery migration: % field recoveryPolicy conflicts with the version 1 document', identity;
        END IF;

        IF item.kind = 'profile' THEN
            options := COALESCE(migrated->'defaultOptions', '{}'::jsonb);
            IF jsonb_typeof(options) <> 'object' THEN
                RAISE EXCEPTION 'recovery migration: % field defaultOptions must be an object', identity;
            END IF;
            nested := options->'structuredRepairRetry';
            repair := 'true'::jsonb;
            IF nested IS NULL THEN
                nested := '{}'::jsonb;
            ELSIF jsonb_typeof(nested) = 'boolean' THEN
                repair := nested;
                nested := '{}'::jsonb;
            ELSIF jsonb_typeof(nested) = 'object' THEN
                repair := COALESCE(nested->'enabled', 'true'::jsonb);
            ELSE
                RAISE EXCEPTION 'recovery migration: % field structuredRepairRetry must be a boolean or object', identity;
            END IF;
            field := 'structuredRepairRetry.enabled';
            delay_names := ARRAY['baseDelayMs','maxDelayMs'];
            retry_names := ARRAY['enableRetryOnNetworkError','enableRetryOn429','enableRetryOn5xx'];
        ELSE
            options := migrated;
            nested := '{}'::jsonb;
            repair := migrated->'structuredRepair';
            field := 'structuredRepair';
            delay_names := ARRAY['initialBackoffMs','maximumBackoffMs'];
            retry_names := ARRAY['retryNetwork','retryRateLimit','retryServerError','retryEmpty'];
        END IF;
        IF jsonb_typeof(repair) IS DISTINCT FROM 'boolean' THEN
            RAISE EXCEPTION 'recovery migration: % field % must be a boolean', identity, field;
        END IF;

        -- Validate every present mapped value, including an overridden nested
        -- value, before applying the fixed flat-over-nested precedence.
        FOREACH source IN ARRAY ARRAY[nested, options] LOOP
            FOR setting IN SELECT key, value FROM jsonb_each(source) LOOP
                IF setting.key = 'maxAttempts' OR setting.key = ANY(delay_names) THEN
                    IF jsonb_typeof(setting.value) <> 'number'
                       OR setting.value::text !~ '^[0-9]+$' THEN
                        RAISE EXCEPTION 'recovery migration: % field % must be an integer', identity, setting.key;
                    END IF;
                    IF (setting.key = 'maxAttempts' AND (setting.value::text::numeric < 1 OR setting.value::text::numeric > 10))
                       OR (setting.key = delay_names[1] AND setting.value::text::numeric > 60000)
                       OR (setting.key = delay_names[2] AND setting.value::text::numeric > 600000) THEN
                        RAISE EXCEPTION 'recovery migration: % field % is out of range', identity, setting.key;
                    END IF;
                ELSIF setting.key = ANY(retry_names)
                      OR setting.key IN ('enableRetryOnParseError','retryParse') THEN
                    IF jsonb_typeof(setting.value) <> 'boolean' THEN
                        RAISE EXCEPTION 'recovery migration: % field % must be a boolean', identity, setting.key;
                    END IF;
                END IF;
            END LOOP;
        END LOOP;
        source := nested || options;
        max_attempts := COALESCE(source->>'maxAttempts', '4')::integer;
        base_delay := COALESCE(source->>delay_names[1], '500')::integer;
        max_delay := COALESCE(source->>delay_names[2], '8000')::integer;
        IF max_delay < base_delay THEN
            RAISE EXCEPTION 'recovery migration: % field % is below %', identity, delay_names[2], delay_names[1];
        END IF;
        categories := '[]'::jsonb;
        FOR category_index IN 1..5 LOOP
            category_enabled := COALESCE(source->retry_names[category_index], 'true'::jsonb);
            IF category_enabled = 'true'::jsonb THEN
                categories := categories || jsonb_build_array(retry_categories[category_index]);
            END IF;
        END LOOP;
        policy := jsonb_build_object(
            'maxAttempts', max_attempts, 'retryOn', categories, 'repairInvalidOutput', repair,
            'backoff', jsonb_build_object('baseDelayMs', base_delay, 'maxDelayMs', max_delay)
        );
        migrated := (migrated - retired) || jsonb_build_object('schemaVersion', 2, 'recoveryPolicy', policy);
        IF item.kind = 'profile' THEN
            IF migrated ? 'defaultOptions' THEN
                migrated := jsonb_set(migrated, '{defaultOptions}', options - retired);
            END IF;
            IF jsonb_typeof(migrated->'reasoningEffortMap') = 'object' THEN
                FOR setting IN SELECT key, value FROM jsonb_each(migrated->'reasoningEffortMap') LOOP
                    migrated := jsonb_set(migrated, ARRAY['reasoningEffortMap',setting.key], setting.value - retired);
                END LOOP;
            END IF;
            UPDATE llm_profiles AS profile SET document = migrated
            WHERE profile.owner_id = item.owner_id AND profile.profile_id = item.id;
        ELSE
            IF jsonb_typeof(migrated->'providerOptions') = 'object' THEN
                migrated := jsonb_set(migrated, '{providerOptions}', (migrated->'providerOptions') - retired);
            END IF;
            UPDATE llm_client_state AS state SET document = migrated
            WHERE state.owner_id = item.owner_id;
        END IF;
    END LOOP;

    -- The request, trace bindings, observations, artifacts, cache entries and
    -- accounting projection columns are historical evidence and stay intact.
    ALTER TABLE llm_runs DROP CONSTRAINT llm_runs_result_schema_version_check;
    FOR item IN SELECT owner_id, run_id, result FROM llm_runs ORDER BY owner_id, run_id LOOP
        identity := 'run ' || item.owner_id || '/' || item.run_id;
        IF item.result->'schemaVersion' IS DISTINCT FROM '2'::jsonb THEN
            RAISE EXCEPTION 'recovery migration: % field schemaVersion must be 2; complete the existing canonical-history cutover first', identity;
        END IF;
        attempts := item.result->'attempts';
        IF attempts = 'null'::jsonb THEN
            attempts := '[]'::jsonb;
        ELSIF jsonb_typeof(attempts) IS DISTINCT FROM 'array' THEN
            RAISE EXCEPTION 'recovery migration: % field attempts must be an array or null', identity;
        END IF;
        IF EXISTS (SELECT 1 FROM jsonb_array_elements(attempts) AS attempt WHERE jsonb_typeof(attempt) <> 'object') THEN
            RAISE EXCEPTION 'recovery migration: % field attempts must contain objects', identity;
        END IF;
        SELECT COALESCE(jsonb_agg(attempt - 'retryLocalNumber' - 'backupIndex' ORDER BY ordinal), '[]'::jsonb)
        INTO attempts FROM jsonb_array_elements(attempts) WITH ORDINALITY AS entries(attempt, ordinal);
        UPDATE llm_runs AS run
        SET result = item.result || jsonb_build_object('schemaVersion',3,'attempts',attempts), result_schema_version = 3
        WHERE run.owner_id = item.owner_id AND run.run_id = item.run_id;
    END LOOP;
    ALTER TABLE llm_runs ADD CONSTRAINT llm_runs_result_schema_version_check CHECK (result_schema_version IN (1,3));
END $$;
