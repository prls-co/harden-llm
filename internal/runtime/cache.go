package runtime

import (
	"context"

	"github.com/prls-co/harden-llm/internal/cachekey"
)

type Cache interface {
	Get(ctx context.Context, operationHash, cacheVersion string) (ProviderResult, bool, error)
	Set(ctx context.Context, operationHash, cacheVersion string, operation cachekey.Operation, result ProviderResult) error
}
