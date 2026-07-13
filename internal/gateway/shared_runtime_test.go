package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-025

import (
	"context"
	"errors"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

func TestSharedRuntimeBindingsAreOwnerScoped(t *testing.T) {
	credentials := &bindingCredentialResolver{}
	cache := &bindingCacheStore{}
	artifacts := &bindingArtifactStore{}
	binding := sharedRuntimeBinding{ownerID: "owner-a", credentials: credentials, cache: cache, artifacts: artifacts}
	ctx := context.WithValue(context.Background(), runtimeBindingContextKey{}, binding)

	credential, err := (dynamicCredentialResolver{}).ResolveCredential(ctx, hardenllm.CredentialRequest{OwnerID: "owner-a"})
	if err != nil || credential.APIKey != "bound-key" || credentials.calls != 1 {
		t.Fatalf("credential = %#v, calls=%d, %v", credential, credentials.calls, err)
	}
	if _, err := (dynamicCredentialResolver{}).ResolveCredential(ctx, hardenllm.CredentialRequest{OwnerID: "owner-b"}); err == nil {
		t.Fatal("cross-owner credential binding was accepted")
	}
	if _, _, err := (dynamicCacheStore{}).Get(ctx, "hash"); err != nil || cache.calls != 1 {
		t.Fatalf("cache binding calls=%d err=%v", cache.calls, err)
	}
	if _, err := (dynamicArtifactStore{}).Put(ctx, "key", []byte(`{}`), "application/json"); err != nil || artifacts.calls != 1 {
		t.Fatalf("artifact binding calls=%d err=%v", artifacts.calls, err)
	}
	if _, _, err := (dynamicCacheStore{}).Get(context.Background(), "hash"); err == nil {
		t.Fatal("unbound cache operation was accepted")
	}

	factory, err := NewSharedRuntimeCallerFactory(hardenllm.Options{})
	if err != nil {
		t.Fatal(err)
	}
	caller, err := factory(RuntimeClientConfig{OwnerID: "owner-a", Credentials: credentials, Cache: cache, Artifacts: artifacts})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := caller.Call(context.Background(), hardenllm.Request{Context: hardenllm.ObservabilityContext{OrganizationID: "owner-b"}}); err == nil {
		t.Fatal("bound caller accepted a different owner")
	}
}

type bindingCredentialResolver struct{ calls int }

func (resolver *bindingCredentialResolver) ResolveCredential(context.Context, hardenllm.CredentialRequest) (hardenllm.Credential, error) {
	resolver.calls++
	return hardenllm.Credential{APIKey: "bound-key"}, nil
}

type bindingCacheStore struct{ calls int }

func (store *bindingCacheStore) Get(context.Context, string) (hardenllm.CacheRecord, bool, error) {
	store.calls++
	return hardenllm.CacheRecord{}, false, nil
}
func (*bindingCacheStore) Set(context.Context, string, hardenllm.CacheRecord) error { return nil }
func (*bindingCacheStore) Delete(context.Context, string) error                     { return nil }

type bindingArtifactStore struct{ calls int }

func (store *bindingArtifactStore) Put(context.Context, string, []byte, string) (hardenllm.ArtifactRef, error) {
	store.calls++
	return hardenllm.ArtifactRef{}, nil
}
func (*bindingArtifactStore) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", errors.New("unused")
}
