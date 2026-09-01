package runtime

import (
	"context"

	"github.com/prls-co/harden-llm/internal/cachekey"
)

type Cache interface {
	Get(ctx context.Context, operationHash, cacheVersion string) (CachedResult, bool, error)
	Set(ctx context.Context, operationHash, cacheVersion string, operation cachekey.Operation, result CachedResult) error
}

type CachedResult struct {
	ProviderResult ProviderResult  `json:"providerResult"`
	Producer       ExecutionTarget `json:"producer"`
}
