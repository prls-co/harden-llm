package hardenllm

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-008 TEST-011

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/retry"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
)

type memoryCache struct {
	mu      sync.Mutex
	records map[string]CacheRecord
	gets    int
	sets    int
}

// Regression for a deployed cache replay dropping search evidence in the
// public persistence projection, despite the runtime cache retaining it.
func TestSearchCachePersistenceProjection(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"native", "jina"} {
		t.Run(mode, func(t *testing.T) {
			cache := &memoryCache{records: make(map[string]CacheRecord)}
			fixture := fixtureProviderResult()
			fixture.Search = &SearchResult{Mode: mode, Executed: true, CostStatus: "unavailable", Sources: []SearchSource{{URL: "https://example.test/source", Title: "Source"}}, Citations: []coreruntime.SearchCitation{{URL: "https://example.test/source", Title: "Source", StartIndex: 0, EndIndex: 2}}}
			executor := &fixedExecutor{result: fixture}
			client, err := New(Options{Credentials: fixedCredentialResolver{}, Cache: cache})
			if err != nil {
				t.Fatal(err)
			}
			client.executor = executor
			request := Request{ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "search fixture", WebSearch: true, CallType: CallTypeText, CacheMode: CacheModeCache, CacheVersion: "operation-v2", RecoveryPolicy: RecoveryPolicy{MaxAttempts: 1, RetryOn: []RecoveryCategory{"network", "rate_limit", "server_error", "empty_response", "provider_retry"}, Backoff: RecoveryBackoff{}}}
			fresh, err := client.Call(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			replayed, err := client.Call(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Search == nil || !reflect.DeepEqual(fresh.Search, replayed.Search) || !replayed.Cache.Served || executor.executed != 1 {
				t.Fatalf("search evidence lost during persisted replay: fresh=%#v cached=%#v calls=%d", fresh.Search, replayed.Search, executor.executed)
			}
			for _, record := range cache.records {
				var projection map[string]any
				if err := json.Unmarshal(record.ProviderResult, &projection); err != nil {
					t.Fatal(err)
				}
				if projection["search"] == nil {
					t.Fatal("serialized cache projection has no search")
				}
			}
		})
	}
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
		RecoveryPolicy: RecoveryPolicy{MaxAttempts: 1, RetryOn: []RecoveryCategory{"network", "rate_limit", "server_error", "empty_response", "provider_retry"}, Backoff: RecoveryBackoff{}},
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
	if second.Output != first.Output || second.Accounting.Result != first.Accounting.Result || len(second.Attempts) != 0 {
		t.Fatalf("cache replay diverged: first=%#v second=%#v", first, second)
	}
	if first.ResultSource.Kind != ResultSourceProvider || second.ResultSource.Kind != ResultSourceCache ||
		first.ResultSource.Producer == nil || second.ResultSource.Producer == nil ||
		*second.ResultSource.Producer != *first.ResultSource.Producer || second.Accounting.Provider.Usage.Status != "unavailable" ||
		second.Accounting.Provider.Cost.Status != "unavailable" {
		t.Fatalf("cache provenance/accounting = first=%#v second=%#v", first, second)
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

func TestCacheV2RejectsV1Envelope(t *testing.T) {
	// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-058
	t.Parallel()

	cache := &memoryCache{records: make(map[string]CacheRecord)}
	client, err := New(Options{Credentials: fixedCredentialResolver{}, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	client.executor = &fixedExecutor{result: fixtureProviderResult()}
	client.newID = sequenceIDs()
	request := Request{
		ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "v2-only",
		CallType: CallTypeText, CacheMode: CacheModeCache, CacheVersion: "operation-v2",
		RecoveryPolicy: RecoveryPolicy{MaxAttempts: 1, RetryOn: []RecoveryCategory{"network", "rate_limit", "server_error", "empty_response", "provider_retry"}, Backoff: RecoveryBackoff{}},
	}
	if _, err := client.Call(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	for key, record := range cache.records {
		record.SchemaVersion = 1
		cache.records[key] = record
	}
	cache.mu.Unlock()
	if _, err := client.Call(context.Background(), request); err == nil {
		t.Fatal("cache v1 envelope was accepted after the v2 cut")
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-224
func TestRecoveryIntegrityCacheAdmission(t *testing.T) {
	t.Parallel()
	baseRequest := func() Request {
		return Request{
			ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "cache admission",
			CallType: CallTypeText, CacheMode: CacheModeCache, CacheVersion: "operation-v2",
			RecoveryPolicy: RecoveryPolicy{MaxAttempts: 2, RetryOn: []RecoveryCategory{"network", "rate_limit", "server_error", "empty_response", "provider_retry"}, RepairInvalidOutput: true, Backoff: RecoveryBackoff{}},
		}
	}
	newClient := func(t *testing.T, cache CacheStore, result coreruntime.ProviderResult) (*Client, *fixedExecutor) {
		t.Helper()
		executor := &fixedExecutor{result: result}
		client, err := New(Options{Credentials: fixedCredentialResolver{}, Cache: cache})
		if err != nil {
			t.Fatal(err)
		}
		client.executor = executor
		client.newID = sequenceIDs()
		return client, executor
	}
	mutate := func(t *testing.T, cache *memoryCache, mutation func(*cachedProviderProjection)) {
		t.Helper()
		cache.mu.Lock()
		defer cache.mu.Unlock()
		for key, record := range cache.records {
			var projection cachedProviderProjection
			decoder := json.NewDecoder(strings.NewReader(string(record.ProviderResult)))
			decoder.UseNumber()
			if err := decoder.Decode(&projection); err != nil {
				t.Fatalf("decode projection: %v", err)
			}
			mutation(&projection)
			encoded, err := json.Marshal(projection)
			if err != nil {
				t.Fatalf("encode projection: %v", err)
			}
			record.ProviderResult = encoded
			cache.records[key] = record
			return
		}
		t.Fatal("cache record was not written")
	}
	assertRejected := func(t *testing.T, client *Client, request Request, executor *fixedExecutor) {
		t.Helper()
		before := executor.executed
		result, err := client.Call(context.Background(), request)
		if err == nil || !strings.Contains(err.Error(), "CACHE_INTEGRITY") || result.Output != nil || result.ResultSource.Kind != ResultSourceNone || result.Cache.Served || result.Cache.Written || executor.executed != before {
			t.Fatalf("cache admission result=%#v error=%v executions=%d want=%d", result, err, executor.executed, before)
		}
	}

	t.Run("precision survives fresh and cached replay", func(t *testing.T) {
		cache := &memoryCache{records: make(map[string]CacheRecord)}
		fixture := fixtureProviderResult()
		fixture.Output = map[string]any{"id": json.Number("9007199254740993"), "fraction": json.Number("0.12345678901234567890123456789")}
		client, executor := newClient(t, cache, fixture)
		request := baseRequest()
		fresh, err := client.Call(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := client.Call(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		value, ok := replayed.Output.(map[string]any)
		if !ok || value["id"] != json.Number("9007199254740993") || value["fraction"] != json.Number("0.12345678901234567890123456789") || !reflect.DeepEqual(fresh.Output, replayed.Output) || executor.executed != 1 {
			t.Fatalf("precision replay fresh=%#v cached=%#v calls=%d", fresh.Output, replayed.Output, executor.executed)
		}
	})

	t.Run("text zero and structured scalar values remain present", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			output any
			kind   CallType
			schema json.RawMessage
		}{
			{name: "text string with leading zero", output: "0012", kind: CallTypeText},
			{name: "structured zero", output: map[string]any{"value": json.Number("0")}, kind: CallTypeStructured, schema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`)},
			{name: "structured false", output: map[string]any{"value": false}, kind: CallTypeStructured, schema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"boolean"}},"required":["value"],"additionalProperties":false}`)},
		} {
			test := test
			t.Run(test.name, func(t *testing.T) {
				cache := &memoryCache{records: make(map[string]CacheRecord)}
				fixture := fixtureProviderResult()
				fixture.Output = test.output
				client, executor := newClient(t, cache, fixture)
				request := baseRequest()
				request.CallType, request.Schema = test.kind, test.schema
				fresh, err := client.Call(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				replayed, err := client.Call(context.Background(), request)
				if err != nil || !reflect.DeepEqual(fresh.Output, replayed.Output) || executor.executed != 1 {
					t.Fatalf("scalar replay fresh=%#v cached=%#v calls=%d error=%v", fresh.Output, replayed.Output, executor.executed, err)
				}
			})
		}
	})

	t.Run("whitespace output is rejected before provider work", func(t *testing.T) {
		cache := &memoryCache{records: make(map[string]CacheRecord)}
		client, executor := newClient(t, cache, fixtureProviderResult())
		request := baseRequest()
		if _, err := client.Call(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		mutate(t, cache, func(projection *cachedProviderProjection) { projection.Output = " \n\t" })
		assertRejected(t, client, request, executor)
	})

	t.Run("invalid accounting is rejected without defaulting", func(t *testing.T) {
		cache := &memoryCache{records: make(map[string]CacheRecord)}
		client, executor := newClient(t, cache, fixtureProviderResult())
		request := baseRequest()
		if _, err := client.Call(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		mutate(t, cache, func(projection *cachedProviderProjection) {
			projection.Accounting.Usage = accounting.Usage{InputTokens: -1, Status: accounting.UsagePartial}
		})
		assertRejected(t, client, request, executor)
	})

	t.Run("invalid search metadata is rejected without trimming", func(t *testing.T) {
		cache := &memoryCache{records: make(map[string]CacheRecord)}
		fixture := fixtureProviderResult()
		fixture.Search = &SearchResult{Mode: "native", CostStatus: "unavailable", Sources: []SearchSource{{URL: "https://example.test/source", Title: "source"}}}
		client, executor := newClient(t, cache, fixture)
		request := baseRequest()
		if _, err := client.Call(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		mutate(t, cache, func(projection *cachedProviderProjection) {
			projection.Search.EntryPointHTML = strings.Repeat("x", 32769)
		})
		assertRejected(t, client, request, executor)
	})

	t.Run("structured cache output is revalidated against the original schema", func(t *testing.T) {
		cache := &memoryCache{records: make(map[string]CacheRecord)}
		fixture := fixtureProviderResult()
		fixture.Output = map[string]any{"id": float64(7)}
		client, executor := newClient(t, cache, fixture)
		request := baseRequest()
		request.CallType = CallTypeStructured
		request.Schema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"],"additionalProperties":false}`)
		if _, err := client.Call(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		mutate(t, cache, func(projection *cachedProviderProjection) { projection.Output = map[string]any{"id": "wrong"} })
		assertRejected(t, client, request, executor)
	})

	t.Run("producer identity is checked while profile aliases remain valid", func(t *testing.T) {
		cache := &memoryCache{records: make(map[string]CacheRecord)}
		client, executor := newClient(t, cache, fixtureProviderResult())
		request := baseRequest()
		if _, err := client.Call(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		mutate(t, cache, func(projection *cachedProviderProjection) { projection.Producer.ProfileID = "retired-profile" })
		if _, err := client.Call(context.Background(), request); err != nil {
			t.Fatalf("same target alias rejected: %v", err)
		}
		mutate(t, cache, func(projection *cachedProviderProjection) { projection.Producer.Provider = "different-provider" })
		assertRejected(t, client, request, executor)
	})

	t.Run("trailing projection JSON is rejected", func(t *testing.T) {
		cache := &memoryCache{records: make(map[string]CacheRecord)}
		client, executor := newClient(t, cache, fixtureProviderResult())
		request := baseRequest()
		if _, err := client.Call(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		cache.mu.Lock()
		for key, record := range cache.records {
			record.ProviderResult = append(record.ProviderResult, []byte(` {"trailing":true}`)...)
			cache.records[key] = record
		}
		cache.mu.Unlock()
		assertRejected(t, client, request, executor)
	})

	t.Run("cache lookup errors do not become misses", func(t *testing.T) {
		sentinel := errors.New("cache read failed")
		cache := &failingCache{err: sentinel}
		client, executor := newClient(t, cache, fixtureProviderResult())
		result, err := client.Call(context.Background(), baseRequest())
		if !errors.Is(err, sentinel) || result.Output != nil || result.ResultSource.Kind != ResultSourceNone || executor.executed != 0 {
			t.Fatalf("cache read failure result=%#v error=%v executions=%d", result, err, executor.executed)
		}
	})
}

type failingCache struct{ err error }

func (cache *failingCache) Get(context.Context, string) (CacheRecord, bool, error) {
	return CacheRecord{}, false, cache.err
}

func (*failingCache) Set(context.Context, string, CacheRecord) error { return nil }

func (*failingCache) Delete(context.Context, string) error { return nil }

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-228
func TestRecoveryIntegrityCacheWrite(t *testing.T) {
	t.Parallel()
	for _, mode := range []CacheMode{CacheModeCache, CacheModeRefresh} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			cache := &failingWriteCache{memoryCache: memoryCache{records: make(map[string]CacheRecord)}, err: errors.New("cache write unavailable")}
			executor := &fixedExecutor{result: fixtureProviderResult()}
			client, err := New(Options{Credentials: fixedCredentialResolver{}, Cache: cache})
			if err != nil {
				t.Fatal(err)
			}
			client.executor, client.newID = executor, sequenceIDs()
			request := Request{
				ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "cache write failure", CallType: CallTypeText,
				CacheMode: mode, CacheVersion: "operation-v2", RecoveryPolicy: RecoveryPolicy{MaxAttempts: 1, RetryOn: []RecoveryCategory{}, Backoff: RecoveryBackoff{}},
			}
			result, err := client.Call(context.Background(), request)
			if err != nil || result.Output != "Apples, bananas" || result.Accounting.Result.Usage.TotalTokens != 15 || result.Cache.Status != "write_failed" || result.Cache.Written || result.Cache.Served || executor.executed != 1 || cache.sets != 1 {
				t.Fatalf("cache write failure result=%#v error=%v executions=%d sets=%d", result, err, executor.executed, cache.sets)
			}
		})
	}
	t.Run("accepted result survives cache write deadline", func(t *testing.T) {
		cache := &failingWriteCache{memoryCache: memoryCache{records: make(map[string]CacheRecord)}, err: context.DeadlineExceeded}
		executor := &fixedExecutor{result: fixtureProviderResult()}
		client, err := New(Options{Credentials: fixedCredentialResolver{}, Cache: cache})
		if err != nil {
			t.Fatal(err)
		}
		client.executor, client.newID = executor, sequenceIDs()
		result, callErr := client.Call(context.Background(), Request{
			ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "cache deadline", CallType: CallTypeText,
			CacheMode: CacheModeCache, CacheVersion: "operation-v2",
			RecoveryPolicy: RecoveryPolicy{MaxAttempts: 1, RetryOn: []RecoveryCategory{}, Backoff: RecoveryBackoff{}},
		})
		if callErr != nil || result.Output == nil || result.Cache.Status != "write_failed" || result.Cache.Written || executor.executed != 1 || cache.sets != 1 {
			t.Fatalf("cache write deadline result=%#v error=%v executions=%d sets=%d", result, callErr, executor.executed, cache.sets)
		}
	})
}

type failingWriteCache struct {
	memoryCache
	err error
}

func (cache *failingWriteCache) Set(context.Context, string, CacheRecord) error {
	cache.mu.Lock()
	cache.sets++
	cache.mu.Unlock()
	return cache.err
}

func TestEmptyProviderResponseRetriesSameOperationBeforeCaching(t *testing.T) {
	cache := &memoryCache{records: make(map[string]CacheRecord)}
	executor := &fixedExecutor{
		result: fixtureProviderResult(),
		sequence: []error{
			&retry.ProviderError{Code: "empty_response", Category: retry.CategoryEmpty, RawResponse: `{"output_text":""}`},
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
		CallType: CallTypeText, CacheMode: CacheModeCache, RecoveryPolicy: RecoveryPolicy{MaxAttempts: 2, RetryOn: []RecoveryCategory{"network", "rate_limit", "server_error", "empty_response", "provider_retry"}, Backoff: RecoveryBackoff{}},
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
		Output:     "Apples, bananas",
		Accounting: testLedger(12, 0, 0, 3, 0, accounting.ExactCost(0.0000225, "calculated")),
	}
}

func sequenceIDs() func() (string, error) {
	next := 0
	return func() (string, error) {
		next++
		return string(rune('a' + next)), nil
	}
}
