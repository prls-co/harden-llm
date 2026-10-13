package stateful

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	hardenllm "github.com/prls-co/harden-llm"
	"time"
)

const defaultCacheTTL = 24 * time.Hour

type PostgresCache struct {
	pool      *pgxpool.Pool
	hash      string
	namespace string
	ttl       time.Duration
	now       func() time.Time
}

func newPostgresCache(pool *pgxpool.Pool, hash, namespace string, ttl time.Duration, now func() time.Time) *PostgresCache {
	return &PostgresCache{pool: pool, hash: hash, namespace: namespace, ttl: ttl, now: now}
}
func (c *PostgresCache) Get(ctx context.Context, key string) (hardenllm.CacheRecord, bool, error) {
	if c.pool == nil {
		return hardenllm.CacheRecord{}, false, &OperationError{Code: "reference_configuration_error", Message: "Reference cache PostgreSQL is not configured."}
	}
	bounded, cancel := context.WithTimeout(ctx, storageStatementTimeout)
	defer cancel()
	var data []byte
	err := c.pool.QueryRow(bounded, "SELECT record FROM cache_entries WHERE api_key_hash=$1 AND namespace=$2 AND operation_hash=$3 AND expires_at>$4", c.hash, c.namespace, key, c.now().UTC()).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return hardenllm.CacheRecord{}, false, nil
	}
	if err != nil {
		return hardenllm.CacheRecord{}, false, storageFailure(err)
	}
	var record hardenllm.CacheRecord
	if err = decodeStrict(data, &record); err != nil {
		return hardenllm.CacheRecord{}, false, &OperationError{Code: "cache_record_invalid", Message: "Stored cache envelope cannot be decoded."}
	}
	return record, true, nil
}
func (c *PostgresCache) Set(ctx context.Context, key string, record hardenllm.CacheRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return &OperationError{Code: "cache_record_invalid", Message: "Cache envelope is not JSON safe."}
	}
	now := c.now().UTC()
	err = withStorageTx(ctx, c.pool, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM cache_entries WHERE api_key_hash=$1 AND (namespace,operation_hash) IN (SELECT namespace,operation_hash FROM cache_entries WHERE api_key_hash=$1 AND expires_at<=$2 ORDER BY expires_at,namespace,operation_hash LIMIT 100)`, c.hash, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO cache_entries(api_key_hash,namespace,operation_hash,schema_version,record,expires_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(api_key_hash,namespace,operation_hash) DO UPDATE SET schema_version=EXCLUDED.schema_version,record=EXCLUDED.record,expires_at=EXCLUDED.expires_at`, c.hash, c.namespace, key, record.SchemaVersion, data, now.Add(c.ttl))
		return err
	})
	return storageFailure(err)
}
func (c *PostgresCache) Delete(ctx context.Context, key string) error {
	err := withStorageTx(ctx, c.pool, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "DELETE FROM cache_entries WHERE api_key_hash=$1 AND namespace=$2 AND operation_hash=$3", c.hash, c.namespace, key)
		return err
	})
	return storageFailure(err)
}
func credentialNamespace(connections hardenllm.ConnectionCatalog, revisions map[string]int64) string {
	type snapshot struct {
		Provider, Protocol, Origin, CacheDomain, Fingerprint string
		Revision                                             int64
	}
	values := make(map[string]snapshot, len(connections))
	for id, c := range connections {
		// Hash explicit secret values in memory. JSON framing includes headers and
		// prevents ambiguous boundaries; persisted namespace contains only the digest.
		secretBytes, _ := json.Marshal(CredentialPayload{APIKey: c.APIKey, Headers: c.Headers})
		digest := sha256.Sum256(secretBytes)
		domain := c.CacheDomain
		if domain == "" {
			domain = c.ID
		}
		values[id] = snapshot{Provider: c.Provider, Protocol: c.Protocol, Origin: c.BaseURL, CacheDomain: domain, Fingerprint: hex.EncodeToString(digest[:]), Revision: revisions[id]}
	}
	encoded, _ := json.Marshal(values)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
