-- ADR-HLLM-024. Convert mutable v2 policy documents and canonical v3 results
-- in one ordinary migration transaction. Store.Migrate supplies the lock and
-- records this file's version only after the transaction commits.
DO $$
DECLARE
    item record;
    migrated_document jsonb;
    policy jsonb;
    migrated_policy jsonb;
    repair_enabled boolean;
    identity text;
BEGIN
    FOR item IN
        SELECT 'profile' AS kind, owner_id, profile_id AS id, document
        FROM llm_profiles
        UNION ALL
        SELECT 'state', owner_id, owner_id, document
        FROM llm_client_state
        ORDER BY kind, owner_id, id
    LOOP
        identity := item.kind || ' ' || item.owner_id || '/' || item.id;
        migrated_document := item.document;
        IF migrated_document->'schemaVersion' IS DISTINCT FROM '2'::jsonb THEN
            RAISE EXCEPTION 'recovery stages migration: % field schemaVersion must be 2', identity;
        END IF;
        policy := migrated_document->'recoveryPolicy';
        IF jsonb_typeof(policy) IS DISTINCT FROM 'object' THEN
            RAISE EXCEPTION 'recovery stages migration: % field recoveryPolicy must be an object', identity;
        END IF;
        IF policy ? 'repairInvalidOutput'
           AND (policy ? 'jsonRepair' OR policy ? 'rerun') THEN
            RAISE EXCEPTION 'recovery stages migration: % field recoveryPolicy mixes repairInvalidOutput with jsonRepair or rerun', identity;
        END IF;
        IF policy ? 'repairInvalidOutput' THEN
            IF jsonb_typeof(policy->'repairInvalidOutput') IS DISTINCT FROM 'boolean' THEN
                RAISE EXCEPTION 'recovery stages migration: % field recoveryPolicy.repairInvalidOutput must be boolean', identity;
            END IF;
            repair_enabled := (policy->>'repairInvalidOutput')::boolean;
            migrated_policy := policy - 'repairInvalidOutput';
            IF repair_enabled THEN
                migrated_policy := migrated_policy || jsonb_build_object(
                    'jsonRepair', jsonb_build_object(
                        'initial', jsonb_build_object('source', 'generation'),
                        'escalation', jsonb_build_object('source', 'generation')
                    ),
                    'rerun', 'null'::jsonb
                );
            ELSE
                migrated_policy := migrated_policy || jsonb_build_object('jsonRepair', 'null'::jsonb, 'rerun', 'null'::jsonb);
            END IF;
        ELSE
            IF NOT (policy ? 'jsonRepair') OR NOT (policy ? 'rerun') THEN
                RAISE EXCEPTION 'recovery stages migration: % policy must contain jsonRepair and rerun', identity;
            END IF;
            migrated_policy := policy;
        END IF;
        migrated_document := jsonb_set(migrated_document, '{schemaVersion}', '3'::jsonb, true);
        migrated_document := jsonb_set(migrated_document, '{recoveryPolicy}', migrated_policy, true);
        IF item.kind = 'profile' THEN
            UPDATE llm_profiles
            SET document = migrated_document
            WHERE owner_id = item.owner_id AND profile_id = item.id;
        ELSE
            UPDATE llm_client_state
            SET document = migrated_document
            WHERE owner_id = item.owner_id;
        END IF;
    END LOOP;

    ALTER TABLE llm_runs DROP CONSTRAINT llm_runs_result_schema_version_check;
    FOR item IN SELECT owner_id, run_id, result FROM llm_runs ORDER BY owner_id, run_id LOOP
        identity := 'run ' || item.owner_id || '/' || item.run_id;
        IF item.result->'schemaVersion' IS DISTINCT FROM '3'::jsonb THEN
            RAISE EXCEPTION 'recovery stages migration: % field schemaVersion must be 3', identity;
        END IF;
        UPDATE llm_runs
        -- The old projection has planned wait totals but no measured wait.
        -- Preserve that fact explicitly instead of manufacturing a zero.
        SET result = item.result || jsonb_build_object('schemaVersion', 4, 'totalActualWaitMs', NULL), result_schema_version = 4
        WHERE owner_id = item.owner_id AND run_id = item.run_id;
    END LOOP;
    ALTER TABLE llm_runs ADD CONSTRAINT llm_runs_result_schema_version_check CHECK (result_schema_version IN (1, 4));
END $$;
