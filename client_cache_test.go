package hardenllm

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-008 TEST-011

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/prls-co/harden-llm/internal/retry"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
)

type memoryCache struct {
	mu      sync.Mutex
	records map[string]CacheRecord
	gets    int
	sets    int
}

func (cache *memoryCache) Get(_ context.Context, key string) (CacheRecord, bool, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.gets++
	record, ok := cache.records[key]
	return record, ok, nil
}

func (cache *memoryCache) Set(_ context.Context, key string, record CacheRecord) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.sets++
	cache.records[key] = record
	return nil
}

func (cache *memoryCache) Delete(_ context.Context, key string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	delete(cache.records, key)
	return nil
}

func TestCacheReplay(t *testing.T) {
	cache := &memoryCache{records: make(map[string]CacheRecord)}
	executor := &fixedExecutor{result: fixtureProviderResult()}
	client, _ := New(Options{Credentials: fixedCredentialResolver{}, Cache: cache})
	client.executor = executor
	client.newID = sequenceIDs()
	request := Request{
		ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "deterministic fixture",
		CallType: CallTypeText, CacheMode: CacheModeCache, CacheVersion: "operation-v2",
		RetryPolicy: RetryPolicy{MaxAttempts: 1},
	}

	first, err := client.Call(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Call(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if executor.prepared != 2 {
		t.Fatalf("provider preparation count = %d, want 2 before both lookups", executor.prepared)
	}
	if executor.executed != 1 {
		t.Fatalf("provider execution count = %d, want 1", executor.executed)
	}
	if cache.gets != 2 || cache.sets != 1 {
		t.Fatalf("cache gets/sets = %d/%d, want 2/1", cache.gets, cache.sets)
	}
	if first.Cache.Status != "miss" || !first.Cache.Written || second.Cache.Status != "hit" || !second.Cache.Served {
		t.Fatalf("cache facts first=%#v second=%#v", first.Cache, second.Cache)
	}
	if second.Output != first.Output || second.Usage != first.Usage || second.Cost != first.Cost || len(second.Attempts) != 0 {
		t.Fatalf("cache replay diverged: first=%#v second=%#v", first, second)
	}
	if second.CallID == first.CallID || second.TraceID == first.TraceID {
		t.Fatal("cache replay reused call or trace identity")
	}

	request.CacheMode = CacheModeRefresh
	if _, err := client.Call(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if executor.executed != 2 || cache.gets != 2 || cache.sets != 2 {
		t.Fatalf("refresh behavior executed/gets/sets = %d/%d/%d", executor.executed, cache.gets, cache.sets)
	}

	request.CacheMode = CacheModeOff
	if _, err := client.Call(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if executor.executed != 3 || cache.gets != 2 || cache.sets != 2 {
		t.Fatalf("off behavior executed/gets/sets = %d/%d/%d", executor.executed, cache.gets, cache.sets)
	}
}

func TestEmptyProviderResponseRetriesSameOperationBeforeCaching(t *testing.T) {
	cache := &memoryCache{records: make(map[string]CacheRecord)}
	executor := &fixedExecutor{
		result: fixtureProviderResult(),
		sequence: []error{
			&retry.ProviderError{Code: "empty_response", Empty: true, RawResponse: `{"output_text":""}`},
			nil,
		},
	}
	client, err := New(Options{Credentials: fixedCredentialResolver{}, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	client.executor = executor
	client.newID = sequenceIDs()
	result, err := client.Call(context.Background(), Request{
		ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "retry empty output",
		CallType: CallTypeText, CacheMode: CacheModeCache, RetryPolicy: RetryPolicy{MaxAttempts: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "Apples, bananas" || executor.executed != 2 || cache.gets != 1 || cache.sets != 1 || len(result.Attempts) != 2 {
		t.Fatalf("empty response retry = result=%#v executed=%d gets=%d sets=%d", result, executor.executed, cache.gets, cache.sets)
	}
	if result.Attempts[0].Category != "empty_response" || !result.Attempts[0].Retryable || result.Attempts[0].Code != "empty_response" {
		t.Fatalf("first attempt metadata = %#v", result.Attempts[0])
	}
}

func fixtureProviderResult() coreruntime.ProviderResult {
	return coreruntime.ProviderResult{
		Output:              "Apples, bananas",
		Usage:               coreruntime.Usage{InputTokens: 12, OutputTokens: 3, TotalTokens: 15},
		Cost:                coreruntime.Cost{TotalUSD: 0.0000225, Known: true, Source: "calculated"},
		RawProviderEnvelope: json.RawMessage(`{"id":"fixture-response"}`),
	}
}

func sequenceIDs() func() (string, error) {
	next := 0
	return func() (string, error) {
		next++
		return string(rune('a' + next)), nil
	}
}
