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

const cacheRecordSchemaVersion = 1

type cacheAdapter struct {
	store CacheStore
}

type cachedProviderProjection struct {
	Output any               `json:"output"`
	Usage  coreruntime.Usage `json:"usage"`
	Cost   coreruntime.Cost  `json:"cost"`
}

func (adapter *cacheAdapter) Get(ctx context.Context, operationHash, cacheVersion string) (coreruntime.ProviderResult, bool, error) {
	record, found, err := adapter.store.Get(ctx, operationHash)
	if err != nil || !found {
		return coreruntime.ProviderResult{}, found, err
	}
	if record.SchemaVersion != cacheRecordSchemaVersion || record.OperationHash != operationHash || record.CacheVersion != cacheVersion {
		return coreruntime.ProviderResult{}, false, errors.New("hardenllm: invalid operation cache record identity")
	}
	var projection cachedProviderProjection
	if err := json.Unmarshal(record.ProviderResult, &projection); err != nil {
		return coreruntime.ProviderResult{}, false, fmt.Errorf("hardenllm: decode operation cache record: %w", err)
	}
	if !json.Valid(record.RawProviderEnvelope) {
		return coreruntime.ProviderResult{}, false, errors.New("hardenllm: invalid cached provider envelope")
	}
	return coreruntime.ProviderResult{
		Output: projection.Output, Usage: projection.Usage, Cost: projection.Cost,
		RawProviderEnvelope: append(json.RawMessage(nil), record.RawProviderEnvelope...),
	}, true, nil
}

func (adapter *cacheAdapter) Set(ctx context.Context, operationHash, cacheVersion string, operation cachekey.Operation, result coreruntime.ProviderResult) error {
	operationJSON, err := cachekey.StableJSON(operation)
	if err != nil {
		return fmt.Errorf("hardenllm: encode cached operation: %w", err)
	}
	providerJSON, err := json.Marshal(cachedProviderProjection{Output: result.Output, Usage: result.Usage, Cost: result.Cost})
	if err != nil {
		return fmt.Errorf("hardenllm: encode cached provider result: %w", err)
	}
	record := CacheRecord{
		SchemaVersion: cacheRecordSchemaVersion, CacheVersion: cacheVersion, OperationHash: operationHash,
		Operation:           append(json.RawMessage(nil), operationJSON...),
		RawProviderEnvelope: append(json.RawMessage(nil), result.RawProviderEnvelope...),
		ProviderResult:      append(json.RawMessage(nil), providerJSON...),
		CreatedAt:           time.Now().UTC(),
	}
	if err := adapter.store.Set(ctx, operationHash, record); err != nil {
		return fmt.Errorf("hardenllm: write operation cache: %w", err)
	}
	return nil
}
