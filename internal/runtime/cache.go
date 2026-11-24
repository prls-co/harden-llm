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
	ProjectionCallID  string `json:"projectionCallId,omitempty"`
	ResponseCreatedAt int64  `json:"responseCreatedAt"`
	ProviderResult
	Producer         ExecutionTarget `json:"producer"`
	GenerationTarget ExecutionTarget `json:"generationTarget,omitempty"`
	CompletedBy      string          `json:"completedBy,omitempty"`
}

// serveCached restores the producing response while keeping this execution's identity and accounting.
func (record *CallRecord) serveCached(cached CachedResult) {
	record.Output, record.Search = cached.Output, cached.Search
	record.AssistantText = cached.AssistantText
	record.ProjectionCallID = cached.ProjectionCallID
	record.ResponseCreatedAt = cached.ResponseCreatedAt
	record.Accounting.Result = cached.Accounting
	record.ResultSource = ResultSource{Kind: ResultSourceCache, Producer: &cached.Producer}
	record.Cache.Status, record.Cache.Served = "hit", true
}
