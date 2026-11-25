package runtime

import (
	"context"
	"errors"

	"github.com/prls-co/harden-llm/internal/cachekey"
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
	record.setResultPayload(cached.ProviderResult)
	record.ProjectionCallID = cached.ProjectionCallID
	record.ResponseCreatedAt = cached.ResponseCreatedAt
	record.ResultSource = ResultSource{Kind: ResultSourceCache, Producer: &cached.Producer}
	record.Cache.Status, record.Cache.Served = "hit", true
}

func (record *CallRecord) rememberBranchCache(branch string, target ExecutionTarget) {
	record.Diagnostics.BranchCaches = append(record.Diagnostics.BranchCaches, BranchCache{
		Branch: branch, GenerationTarget: target, Cache: record.Cache,
	})
}

func (record *CallRecord) writeCachedResult(ctx context.Context, cache Cache, observer Observer, result ProviderResult, generationHash string, generationTarget ExecutionTarget, completedBy string) {
	if record.Cache.Mode == cachekey.ModeOff {
		return
	}
	cacheContext, finish := startCache(ctx, observer, "write")
	err := cache.Set(cacheContext, generationHash, record.Cache.Version, CachedResult{
		ProjectionCallID: record.CallID, ResponseCreatedAt: record.ResponseCreatedAt, ProviderResult: result,
		Producer: *record.ResultSource.Producer, GenerationTarget: generationTarget, CompletedBy: completedBy,
	})
	if err != nil {
		record.Cache.Status = "write_failed"
		finish(record.Cache.Status, err)
		return
	}
	finish(record.Cache.Status, nil)
	record.Cache.Written = true
}
