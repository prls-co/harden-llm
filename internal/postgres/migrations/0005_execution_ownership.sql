DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM llm_traces WHERE run_id IS NULL) THEN
        RAISE EXCEPTION 'execution ownership migration requires reconcile-history before migration 5';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM llm_traces AS trace
        LEFT JOIN llm_runs AS run
          ON run.owner_id = trace.owner_id
         AND run.run_id = trace.run_id
         AND run.trace_id = trace.trace_id
        WHERE run.run_id IS NULL
    ) THEN
        RAISE EXCEPTION 'execution ownership migration found a mismatched trace and run binding';
    END IF;
END $$;

ALTER TABLE llm_traces DROP CONSTRAINT llm_traces_run_fk;
ALTER TABLE llm_runs ADD CONSTRAINT llm_runs_owner_run_trace_key
    UNIQUE (owner_id, run_id, trace_id);
ALTER TABLE llm_traces ALTER COLUMN run_id SET NOT NULL;
ALTER TABLE llm_traces ADD CONSTRAINT llm_traces_execution_fk
    FOREIGN KEY (owner_id, run_id, trace_id)
    REFERENCES llm_runs(owner_id, run_id, trace_id)
    ON DELETE CASCADE;
