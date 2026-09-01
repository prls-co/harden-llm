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

// ArtifactPublisher is the typed publication boundary used by hosts that must
// coordinate object storage with durable metadata. Client falls back to Put
// only when the configured store does not implement this interface.
type ArtifactPublisher interface {
	PublishArtifact(context.Context, ArtifactPublication) (ArtifactRef, error)
}

// ArtifactPublication carries immutable ownership and integrity facts without
// requiring a storage adapter to recover them from an object key.
type ArtifactPublication struct {
	OwnerID     string
	RunID       string
	TraceID     string
	ArtifactID  string
	Kind        string
	ObjectKey   string
	Content     []byte
	ContentType string
}

// ArtifactRef is immutable metadata for a stored artifact.
type ArtifactRef struct {
	ArtifactID  string `json:"artifactId,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Key         string `json:"key"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"sizeBytes"`
	ContentType string `json:"contentType"`
}
