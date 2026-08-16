package hardenllm

import (
	"context"
	"encoding/json"
	"time"
)

// CacheMode controls operation-cache reads and writes.
type CacheMode string

const (
	CacheModeOff     CacheMode = "off"
	CacheModeCache   CacheMode = "cache"
	CacheModeRefresh CacheMode = "refresh"
)

// CacheRecord is the portable operation-cache persistence envelope.
type CacheRecord struct {
	SchemaVersion       int             `json:"schemaVersion"`
	CacheVersion        string          `json:"cacheVersion"`
	OperationHash       string          `json:"operationHash"`
	Operation           json.RawMessage `json:"operation"`
	RawProviderEnvelope json.RawMessage `json:"rawProviderEnvelope"`
	ProviderResult      json.RawMessage `json:"providerResult"`
	CreatedAt           time.Time       `json:"createdAt"`
}

// CacheStore persists operation records by their deterministic hash.
type CacheStore interface {
	Get(ctx context.Context, operationHash string) (CacheRecord, bool, error)
	Set(ctx context.Context, operationHash string, record CacheRecord) error
	Delete(ctx context.Context, operationHash string) error
}

// CacheResult describes the outcome of one cache interaction.
type CacheResult struct {
	Mode          CacheMode `json:"mode"`
	Status        string    `json:"status"`
	OperationHash string    `json:"operationHash,omitempty"`
	Version       string    `json:"version,omitempty"`
	Served        bool      `json:"served"`
	Written       bool      `json:"written"`
}
