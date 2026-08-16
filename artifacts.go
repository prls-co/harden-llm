package hardenllm

import (
	"context"
	"time"
)

// ArtifactStore persists redacted canonical artifacts and presigns reads.
type ArtifactStore interface {
	Put(ctx context.Context, key string, content []byte, contentType string) (ArtifactRef, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// ArtifactRef is immutable metadata for a stored artifact.
type ArtifactRef struct {
	Key         string `json:"key"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"sizeBytes"`
	ContentType string `json:"contentType"`
}
