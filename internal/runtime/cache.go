package runtime

import (
	"context"
	"errors"
)

// ErrCacheIntegrity is the bounded error returned when a persisted cache
// record cannot be trusted. It intentionally carries no stored payload or
// validation detail.
var ErrCacheIntegrity = errors.New("CACHE_INTEGRITY")

func NewCacheIntegrityError() error { return ErrCacheIntegrity }

type Cache interface {
	Get(ctx context.Context, operationHash, cacheVersion string) (CachedResult, bool, error)
	Set(ctx context.Context, operationHash, cacheVersion string, result CachedResult) error
}

type CachedResult struct {
	ProviderResult   ProviderResult  `json:"providerResult"`
	Producer         ExecutionTarget `json:"producer"`
	GenerationTarget ExecutionTarget `json:"generationTarget,omitempty"`
	CompletedBy      string          `json:"completedBy,omitempty"`
}
