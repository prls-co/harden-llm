package gateway

import (
	"context"
	"errors"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

type runtimeBindingContextKey struct{}

type sharedRuntimeBinding struct {
	ownerID     string
	credentials hardenllm.CredentialResolver
	cache       hardenllm.CacheStore
	artifacts   hardenllm.ArtifactStore
}

type boundRuntimeCaller struct {
	client  *hardenllm.Client
	binding sharedRuntimeBinding
}

// NewSharedRuntimeCallerFactory builds one root Client and reuses its hardened
// provider transports across requests. Per-owner adapters are bound only to the
// call context, preventing connection-pool churn and cross-owner state.
func NewSharedRuntimeCallerFactory(options hardenllm.Options) (RuntimeCallerFactory, error) {
	if options.Credentials != nil || options.Cache != nil || options.Artifacts != nil {
		return nil, errors.New("gateway: shared runtime owns its dynamic adapters")
	}
	options.Credentials = dynamicCredentialResolver{}
	options.Cache = dynamicCacheStore{}
	options.Artifacts = dynamicArtifactStore{}
	client, err := hardenllm.New(options)
	if err != nil {
		return nil, err
	}
	return func(config RuntimeClientConfig) (RuntimeCaller, error) {
		if config.OwnerID == "" || config.Credentials == nil || config.Cache == nil {
			return nil, errors.New("gateway: runtime owner, credentials, and cache are required")
		}
		return &boundRuntimeCaller{client: client, binding: sharedRuntimeBinding{
			ownerID: config.OwnerID, credentials: config.Credentials, cache: config.Cache, artifacts: config.Artifacts,
		}}, nil
	}, nil
}

func (caller *boundRuntimeCaller) Call(ctx context.Context, request hardenllm.Request) (hardenllm.Result, error) {
	if caller == nil || caller.client == nil || ctx == nil || request.Context.OrganizationID != caller.binding.ownerID {
		return hardenllm.Result{}, errors.New("gateway: runtime caller binding is invalid")
	}
	return caller.client.Call(context.WithValue(ctx, runtimeBindingContextKey{}, caller.binding), request)
}

func runtimeBinding(ctx context.Context) (sharedRuntimeBinding, error) {
	if ctx == nil {
		return sharedRuntimeBinding{}, errors.New("gateway: runtime binding is unavailable")
	}
	binding, ok := ctx.Value(runtimeBindingContextKey{}).(sharedRuntimeBinding)
	if !ok || binding.ownerID == "" {
		return sharedRuntimeBinding{}, errors.New("gateway: runtime binding is unavailable")
	}
	return binding, nil
}

type dynamicCredentialResolver struct{}

func (dynamicCredentialResolver) ResolveCredential(ctx context.Context, request hardenllm.CredentialRequest) (hardenllm.Credential, error) {
	binding, err := runtimeBinding(ctx)
	if err != nil || binding.credentials == nil || request.OwnerID != binding.ownerID {
		return hardenllm.Credential{}, errors.New("gateway: runtime credential binding is unavailable")
	}
	return binding.credentials.ResolveCredential(ctx, request)
}

type dynamicCacheStore struct{}

func (dynamicCacheStore) Get(ctx context.Context, operationHash string) (hardenllm.CacheRecord, bool, error) {
	binding, err := runtimeBinding(ctx)
	if err != nil || binding.cache == nil {
		return hardenllm.CacheRecord{}, false, errors.New("gateway: runtime cache binding is unavailable")
	}
	return binding.cache.Get(ctx, operationHash)
}

func (dynamicCacheStore) Set(ctx context.Context, operationHash string, record hardenllm.CacheRecord) error {
	binding, err := runtimeBinding(ctx)
	if err != nil || binding.cache == nil {
		return errors.New("gateway: runtime cache binding is unavailable")
	}
	return binding.cache.Set(ctx, operationHash, record)
}

func (dynamicCacheStore) Delete(ctx context.Context, operationHash string) error {
	binding, err := runtimeBinding(ctx)
	if err != nil || binding.cache == nil {
		return errors.New("gateway: runtime cache binding is unavailable")
	}
	return binding.cache.Delete(ctx, operationHash)
}

type dynamicArtifactStore struct{}

func (dynamicArtifactStore) Put(ctx context.Context, key string, content []byte, contentType string) (hardenllm.ArtifactRef, error) {
	binding, err := runtimeBinding(ctx)
	if err != nil || binding.artifacts == nil {
		return hardenllm.ArtifactRef{}, errors.New("gateway: runtime artifact binding is unavailable")
	}
	return binding.artifacts.Put(ctx, key, content, contentType)
}

func (dynamicArtifactStore) PublishArtifact(ctx context.Context, publication hardenllm.ArtifactPublication) (hardenllm.ArtifactRef, error) {
	binding, err := runtimeBinding(ctx)
	if err != nil || binding.artifacts == nil {
		return hardenllm.ArtifactRef{}, errors.New("gateway: runtime artifact binding is unavailable")
	}
	publisher, ok := binding.artifacts.(hardenllm.ArtifactPublisher)
	if !ok {
		return hardenllm.ArtifactRef{}, errors.New("gateway: runtime artifact publisher is unavailable")
	}
	return publisher.PublishArtifact(ctx, publication)
}

func (dynamicArtifactStore) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	binding, err := runtimeBinding(ctx)
	if err != nil || binding.artifacts == nil {
		return "", errors.New("gateway: runtime artifact binding is unavailable")
	}
	return binding.artifacts.PresignGet(ctx, key, ttl)
}
