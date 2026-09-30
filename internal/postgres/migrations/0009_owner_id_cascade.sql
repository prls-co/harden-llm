ALTER TABLE user_sessions
    DROP CONSTRAINT user_sessions_owner_id_fkey,
    ADD CONSTRAINT user_sessions_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE llm_endpoint_credentials
    DROP CONSTRAINT llm_endpoint_credentials_owner_id_fkey,
    ADD CONSTRAINT llm_endpoint_credentials_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE llm_profiles
    DROP CONSTRAINT llm_profiles_owner_id_fkey,
    ADD CONSTRAINT llm_profiles_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE,
    DROP CONSTRAINT llm_profiles_owner_id_credential_id_fkey,
    ADD CONSTRAINT llm_profiles_owner_id_credential_id_fkey
        FOREIGN KEY (owner_id, credential_id)
        REFERENCES llm_endpoint_credentials(owner_id, credential_id)
        ON DELETE RESTRICT ON UPDATE CASCADE;

ALTER TABLE llm_client_state
    DROP CONSTRAINT llm_client_state_owner_id_fkey,
    ADD CONSTRAINT llm_client_state_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE llm_runs
    DROP CONSTRAINT llm_runs_owner_id_fkey,
    ADD CONSTRAINT llm_runs_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE llm_traces
    DROP CONSTRAINT llm_traces_owner_id_fkey,
    ADD CONSTRAINT llm_traces_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE,
    DROP CONSTRAINT llm_traces_execution_fk,
    ADD CONSTRAINT llm_traces_execution_fk
        FOREIGN KEY (owner_id, run_id, trace_id)
        REFERENCES llm_runs(owner_id, run_id, trace_id)
        ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE llm_trace_observations
    DROP CONSTRAINT llm_trace_observations_owner_id_trace_id_fkey,
    ADD CONSTRAINT llm_trace_observations_owner_id_trace_id_fkey
        FOREIGN KEY (owner_id, trace_id)
        REFERENCES llm_traces(owner_id, trace_id)
        ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE llm_artifacts
    DROP CONSTRAINT llm_artifacts_owner_id_trace_id_fkey,
    ADD CONSTRAINT llm_artifacts_owner_id_trace_id_fkey
        FOREIGN KEY (owner_id, trace_id)
        REFERENCES llm_traces(owner_id, trace_id)
        ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE llm_operation_cache
    DROP CONSTRAINT llm_operation_cache_owner_id_fkey,
    ADD CONSTRAINT llm_operation_cache_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE llm_artifact_delete_batches
    DROP CONSTRAINT llm_artifact_delete_batches_owner_id_fkey,
    ADD CONSTRAINT llm_artifact_delete_batches_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE llm_artifact_operations
    DROP CONSTRAINT llm_artifact_operations_owner_id_fkey,
    ADD CONSTRAINT llm_artifact_operations_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE ON UPDATE CASCADE;
