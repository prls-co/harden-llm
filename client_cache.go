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

const cacheRecordSchemaVersion = 3

type cacheAdapter struct {
	store CacheStore
}

type cachedProviderProjection struct {
	Search           *coreruntime.SearchResult     `json:"search,omitempty"`
	Output           any                           `json:"output"`
	Accounting       coreruntime.Ledger            `json:"accounting"`
	Stream           coreruntime.StreamDiagnostics `json:"stream,omitempty"`
	Producer         coreruntime.ExecutionTarget   `json:"producer"`
	GenerationTarget coreruntime.ExecutionTarget   `json:"generationTarget,omitempty"`
	CompletedBy      string                        `json:"completedBy,omitempty"`
}

func (adapter *cacheAdapter) Get(ctx context.Context, operationHash, cacheVersion string) (coreruntime.CachedResult, bool, error) {
	record, found, err := adapter.store.Get(ctx, operationHash)
	if err != nil || !found {
		return coreruntime.CachedResult{}, found, err
	}
	if record.SchemaVersion != cacheRecordSchemaVersion || record.OperationHash != operationHash || record.CacheVersion != cacheVersion {
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
	if projection.Output == nil {
		return coreruntime.CachedResult{}, false, coreruntime.NewCacheIntegrityError()
	}
	return coreruntime.CachedResult{
		ProviderResult: coreruntime.ProviderResult{
			Search: projection.Search,
			Output: projection.Output, Accounting: projection.Accounting,
			Stream: projection.Stream,
		},
		Producer: projection.Producer, GenerationTarget: projection.GenerationTarget, CompletedBy: projection.CompletedBy,
	}, true, nil
}

func (adapter *cacheAdapter) Set(ctx context.Context, operationHash, cacheVersion string, result coreruntime.CachedResult) error {
	if result.ProviderResult.Output == nil {
		return errors.New("hardenllm: cannot cache a provider result without output")
	}
	providerJSON, err := json.Marshal(cachedProviderProjection{
		Search: result.ProviderResult.Search,
		Output: result.ProviderResult.Output, Accounting: result.ProviderResult.Accounting, Producer: result.Producer,
		Stream:           result.ProviderResult.Stream,
		GenerationTarget: result.GenerationTarget, CompletedBy: result.CompletedBy,
	})
	if err != nil {
		return fmt.Errorf("hardenllm: encode cached provider result: %w", err)
	}
	record := CacheRecord{
		SchemaVersion: cacheRecordSchemaVersion, CacheVersion: cacheVersion, OperationHash: operationHash,
		ProviderResult: append(json.RawMessage(nil), providerJSON...),
		CreatedAt:      time.Now().UTC(),
	}
	if err := adapter.store.Set(ctx, operationHash, record); err != nil {
		return fmt.Errorf("hardenllm: write operation cache: %w", err)
	}
	return nil
}
