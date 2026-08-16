package postgres

import (
	"context"
	"errors"
	"fmt"
)

func (store *Store) PutCache(ctx context.Context, record CacheRecord) error {
	for name, value := range map[string]string{"owner ID": record.OwnerID, "cache version": record.Version, "operation hash": record.OperationHash} {
		if err := validateIdentifier(name, value); err != nil {
			return err
		}
	}
	for name, value := range map[string][]byte{
		"cache operation":   record.Operation,
		"cache result":      record.Result,
		"cache usage":       record.Usage,
		"cache cost":        record.Cost,
		"provider envelope": record.Envelope,
	} {
		if err := validateJSONObject(name, value); err != nil {
			return err
		}
	}
	if record.CreatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) {
		return errors.New("postgres: valid cache timestamps are required")
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO llm_operation_cache
			(owner_id, cache_version, operation_hash, operation, result, usage, cost, provider_envelope, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (owner_id, cache_version, operation_hash) DO UPDATE SET
			operation = EXCLUDED.operation,
			result = EXCLUDED.result,
			usage = EXCLUDED.usage,
			cost = EXCLUDED.cost,
			provider_envelope = EXCLUDED.provider_envelope,
			updated_at = EXCLUDED.updated_at`,
		record.OwnerID, record.Version, record.OperationHash, record.Operation, record.Result,
		record.Usage, record.Cost, record.Envelope, record.CreatedAt, record.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: upsert cache record: %w", err)
	}
	return nil
}

func (store *Store) Cache(ctx context.Context, ownerID, version, operationHash string) (CacheRecord, error) {
	var record CacheRecord
	err := store.pool.QueryRow(ctx, `
		SELECT owner_id, cache_version, operation_hash, operation, result, usage, cost, provider_envelope, created_at, updated_at
		FROM llm_operation_cache
		WHERE owner_id = $1 AND cache_version = $2 AND operation_hash = $3`, ownerID, version, operationHash).Scan(
		&record.OwnerID, &record.Version, &record.OperationHash, &record.Operation, &record.Result,
		&record.Usage, &record.Cost, &record.Envelope, &record.CreatedAt, &record.UpdatedAt,
	)
	if err != nil {
		return CacheRecord{}, notFound(err)
	}
	return record, nil
}

func (store *Store) CountCache(ctx context.Context, ownerID, version, operationHash string) (int, error) {
	var count int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*) FROM llm_operation_cache
		WHERE owner_id = $1 AND cache_version = $2 AND operation_hash = $3`, ownerID, version, operationHash).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres: count cache records: %w", err)
	}
	return count, nil
}
