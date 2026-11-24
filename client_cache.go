package hardenllm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
)

const cacheRecordSchemaVersion = 4

type cacheAdapter struct {
	store CacheStore
}

// Keep the complete accepted result in one record. The typed alternative distinguishes
// an assistant turn from structured JSON that happens to contain a tool_calls field.
type cachedProviderProjection struct {
	coreruntime.CachedResult
	Assistant *AssistantOutput `json:"assistant,omitempty"`
}

func (adapter *cacheAdapter) Get(ctx context.Context, operationHash, cacheVersion string) (coreruntime.CachedResult, bool, error) {
	record, found, err := adapter.store.Get(ctx, operationHash)
	if err != nil || !found {
		return coreruntime.CachedResult{}, found, err
	}
	if record.OperationHash != operationHash || record.CacheVersion != cacheVersion {
		return coreruntime.CachedResult{}, false, coreruntime.NewCacheIntegrityError()
	}
	// Custom executors own their operation keys and may still address a v3 record.
	// That format cannot distinguish a function call from ordinary JSON.
	if record.SchemaVersion == 3 {
		return coreruntime.CachedResult{}, false, nil
	}
	if record.SchemaVersion != cacheRecordSchemaVersion {
		return coreruntime.CachedResult{}, false, coreruntime.NewCacheIntegrityError()
	}
	var projection cachedProviderProjection
	decoder := json.NewDecoder(bytes.NewReader(record.ProviderResult))
	decoder.UseNumber()
	if err := decoder.Decode(&projection); err != nil {
		return coreruntime.CachedResult{}, false, coreruntime.NewCacheIntegrityError()
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return coreruntime.CachedResult{}, false, coreruntime.NewCacheIntegrityError()
	}
	if projection.Assistant != nil {
		if projection.Output != nil {
			return coreruntime.CachedResult{}, false, coreruntime.NewCacheIntegrityError()
		}
		projection.Output = *projection.Assistant
	}
	if projection.Output == nil {
		return coreruntime.CachedResult{}, false, coreruntime.NewCacheIntegrityError()
	}
	return projection.CachedResult, true, nil
}

func (adapter *cacheAdapter) Set(ctx context.Context, operationHash, cacheVersion string, result coreruntime.CachedResult) error {
	projection := cachedProviderProjection{CachedResult: result}
	switch output := projection.Output.(type) {
	case AssistantOutput:
		projection.Assistant, projection.Output = &output, nil
	case *AssistantOutput:
		projection.Assistant, projection.Output = output, nil
	}
	if projection.Output == nil && projection.Assistant == nil {
		return errors.New("hardenllm: cannot cache a provider result without output")
	}
	providerJSON, err := json.Marshal(projection)
	if err != nil {
		return fmt.Errorf("hardenllm: encode cached provider result: %w", err)
	}
	record := CacheRecord{
		SchemaVersion: cacheRecordSchemaVersion, CacheVersion: cacheVersion, OperationHash: operationHash,
		ProviderResult: providerJSON,
		CreatedAt:      time.Now().UTC(),
	}
	if err := adapter.store.Set(ctx, operationHash, record); err != nil {
		return fmt.Errorf("hardenllm: write operation cache: %w", err)
	}
	return nil
}
