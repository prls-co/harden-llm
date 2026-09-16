-- ADR-HLLM-022. The canonical provider result is the only cache payload.
-- Store.Migrate owns the transaction and migration-version record.
ALTER TABLE llm_operation_cache
    DROP COLUMN operation,
    DROP COLUMN provider_envelope,
    DROP COLUMN usage,
    DROP COLUMN cost;
