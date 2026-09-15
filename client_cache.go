package hardenllm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/prls-co/harden-llm/internal/cachekey"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
)

const cacheRecordSchemaVersion = 2

type cacheAdapter struct {
	store CacheStore
}

type cachedProviderProjection struct {
	Search     *coreruntime.SearchResult   `json:"search,omitempty"`
	Output     any                         `json:"output"`
	Accounting coreruntime.Ledger          `json:"accounting"`
	Producer   coreruntime.ExecutionTarget `json:"producer"`
}

func (adapter *cacheAdapter) Get(ctx context.Context, operationHash, cacheVersion string) (coreruntime.CachedResult, bool, error) {
	record, found, err := adapter.store.Get(ctx, operationHash)
	if err != nil || !found {
		return coreruntime.CachedResult{}, found, err
	}
	if record.SchemaVersion != cacheRecordSchemaVersion || record.OperationHash != operationHash || record.CacheVersion != cacheVersion {
		return coreruntime.CachedResult{}, false, errors.New("hardenllm: invalid operation cache record identity")
	}
	var projection cachedProviderProjection
	if err := json.Unmarshal(record.ProviderResult, &projection); err != nil {
		return coreruntime.CachedResult{}, false, fmt.Errorf("hardenllm: decode operation cache record: %w", err)
	}
	if !json.Valid(record.RawProviderEnvelope) {
		return coreruntime.CachedResult{}, false, errors.New("hardenllm: invalid cached provider envelope")
	}
	if projection.Output == nil {
		return coreruntime.CachedResult{}, false, errors.New("hardenllm: cached provider result has no accepted output")
	}
	return coreruntime.CachedResult{
		ProviderResult: coreruntime.ProviderResult{
			Search: projection.Search,
			Output: projection.Output, Accounting: projection.Accounting,
			RawProviderEnvelope: append(json.RawMessage(nil), record.RawProviderEnvelope...),
		},
		Producer: projection.Producer,
	}, true, nil
}

func (adapter *cacheAdapter) Set(ctx context.Context, operationHash, cacheVersion string, operation cachekey.Operation, result coreruntime.CachedResult) error {
	if result.ProviderResult.Output == nil {
		return errors.New("hardenllm: cannot cache a provider result without output")
	}
	if !json.Valid(result.ProviderResult.RawProviderEnvelope) {
		return errors.New("hardenllm: cannot cache an invalid provider envelope")
	}
	operationJSON, err := cachekey.StableJSON(operation)
	if err != nil {
		return fmt.Errorf("hardenllm: encode cached operation: %w", err)
	}
	providerJSON, err := json.Marshal(cachedProviderProjection{
		Search: result.ProviderResult.Search,
		Output: result.ProviderResult.Output, Accounting: result.ProviderResult.Accounting, Producer: result.Producer,
	})
	if err != nil {
		return fmt.Errorf("hardenllm: encode cached provider result: %w", err)
	}
	record := CacheRecord{
		SchemaVersion: cacheRecordSchemaVersion, CacheVersion: cacheVersion, OperationHash: operationHash,
		Operation:           append(json.RawMessage(nil), operationJSON...),
		RawProviderEnvelope: append(json.RawMessage(nil), result.ProviderResult.RawProviderEnvelope...),
		ProviderResult:      append(json.RawMessage(nil), providerJSON...),
		CreatedAt:           time.Now().UTC(),
	}
	if err := adapter.store.Set(ctx, operationHash, record); err != nil {
		return fmt.Errorf("hardenllm: write operation cache: %w", err)
	}
	return nil
}
