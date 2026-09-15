package providers

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-012 TEST-025

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
)

func TestWebSearchRoutingAndCacheIdentity(t *testing.T) {
	router, err := NewRouter(Config{EndpointPolicy: EndpointPolicy{
		Resolver: staticResolver{
			"api.openai.com": {netip.MustParseAddr("104.18.7.192")},
		},
	}})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	profile := runtime.Profile{
		ID: "openai", Provider: "openai", APIInferenceType: "responses",
		BaseURL: "https://api.openai.com/v1", ModelID: "gpt-5.6-luna",
		SupportsWebSearch: true, ResponsesTokensParam: "max_output_tokens",
		DefaultOptions: map[string]any{},
	}
	searchCall := runtime.Call{
		CallType: "text", UserPrompt: "latest weather in San Francisco", WebSearch: true,
		ProviderOptions: map[string]any{"max_tokens": float64(128)},
	}
	prepared, err := router.Prepare(context.Background(), profile, runtime.Credential{APIKey: "fixture-secret"}, searchCall)
	if err != nil {
		t.Fatalf("Prepare native search: %v", err)
	}
	nativePayload := prepared.Operation.Payload.(map[string]any)
	assertNativeWebSearchPayload(t, nativePayload)
	if prepared.Opaque.(preparedRequest).webSearch != nil {
		t.Fatal("native web search unexpectedly selected the Jina fallback")
	}

	withoutSearch := searchCall
	withoutSearch.WebSearch = false
	withoutSearchPrepared, err := router.Prepare(context.Background(), profile, runtime.Credential{APIKey: "fixture-secret"}, withoutSearch)
	if err != nil {
		t.Fatalf("Prepare without search: %v", err)
	}
	if _, present := withoutSearchPrepared.Operation.Payload.(map[string]any)["tools"]; present {
		t.Fatal("web-search tool was added when search was disabled")
	}

	fallbackProfile := profile
	fallbackProfile.SupportsWebSearch = false
	fallbackPrepared, err := router.Prepare(context.Background(), fallbackProfile, runtime.Credential{APIKey: "fixture-secret"}, searchCall)
	if err != nil {
		t.Fatalf("Prepare fallback search: %v", err)
	}
	fallbackPayload := fallbackPrepared.Operation.Payload.(map[string]any)
	marker, ok := fallbackPayload["__harden_llm_web_search"].(map[string]any)
	if !ok || marker["mode"] != "jina" || marker["query"] != searchCall.UserPrompt {
		t.Fatalf("fallback cache marker = %#v", fallbackPayload["__harden_llm_web_search"])
	}
	if strings.Contains(string(fallbackPrepared.Opaque.(preparedRequest).body), "__harden_llm_web_search") {
		t.Fatal("fallback cache marker leaked into provider payload")
	}

	if _, err := cachekey.Hash(prepared.Operation, cachekey.DefaultVersion); err != nil {
		t.Fatalf("hash native search operation: %v", err)
	}
	if nativeHash, _ := cachekey.Hash(prepared.Operation, cachekey.DefaultVersion); nativeHash == mustOperationHash(t, fallbackPrepared.Operation) {
		t.Fatal("native and fallback web-search operations share a cache identity")
	}
}

func TestNativeWebSearchCanBeForcedOnlyOnResponsesRoute(t *testing.T) {
	profile := runtime.Profile{
		ID: "openai", Provider: "openai", APIInferenceType: "responses",
		ModelID: "gpt-5.6-luna", SupportsWebSearch: true, DefaultOptions: map[string]any{},
	}
	call := runtime.Call{CallType: "text", UserPrompt: "search this", WebSearch: true, ProviderOptions: map[string]any{
		"useResponsesApi": false,
	}}
	_, protocol, _, payload, _, err := buildPayload(profile, call)
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if protocol != "openai.chat.completions" {
		t.Fatalf("protocol = %q, want chat-completions", protocol)
	}
	if _, present := payload["tools"]; present {
		t.Fatal("native Responses web-search tool leaked into forced Chat Completions route")
	}
	if nativeWebSearchEnabled(profile, call) {
		t.Fatal("forced Chat Completions route was reported as native search")
	}
}

func TestJinaFallbackSearchIsExecutedAfterPrepareAndBoundToProviderPayload(t *testing.T) {
	var providerCalls int
	provider := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		providerCalls++
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Errorf("read provider body: %v", readErr)
			return
		}
		if strings.Contains(string(body), "__harden_llm_web_search") {
			t.Error("private fallback marker was sent to provider")
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode provider body: %v", err)
			return
		}
		encodedInput, _ := json.Marshal(payload["input"])
		if !strings.Contains(string(encodedInput), "fixture search result") {
			t.Errorf("provider input omitted bounded search result: %s", encodedInput)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"completed","output_text":"ok"}`))
	}))
	defer provider.Close()

	searcher := &recordingSearcher{result: "fixture search result https://example.test/source"}
	router := newTLSTestRouter(t, provider, searcher)
	profile := runtime.Profile{
		ID: "fallback", Provider: "openai", APIInferenceType: "responses",
		BaseURL: provider.URL + "/v1", ModelID: "gpt-5.6-luna", DefaultOptions: map[string]any{},
		SupportsWebSearch: false,
	}
	call := runtime.Call{CallType: "text", UserPrompt: "find a fixture", WebSearch: true}
	prepared, err := router.Prepare(context.Background(), profile, runtime.Credential{APIKey: "fixture-secret"}, call)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	first, err := router.Execute(context.Background(), prepared)
	if err != nil || first.Output != "ok" {
		t.Fatalf("first Execute = %#v, %v", first, err)
	}
	if searcher.calls != 1 || providerCalls != 1 {
		t.Fatalf("first call counts search/provider = %d/%d, want 1/1", searcher.calls, providerCalls)
	}
	second, err := router.Execute(context.Background(), prepared)
	if err != nil || second.Output != "ok" {
		t.Fatalf("second Execute = %#v, %v", second, err)
	}
	if searcher.calls != 1 || providerCalls != 2 {
		t.Fatalf("prepared retry reused search result incorrectly: search/provider = %d/%d, want 1/2", searcher.calls, providerCalls)
	}
}

func TestWebSearchCacheHitSkipsJinaFallback(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("provider was contacted on a cache hit")
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer provider.Close()
	searcher := &recordingSearcher{result: "must not be requested"}
	router := newTLSTestRouter(t, provider, searcher)
	profile := runtime.Profile{
		ID: "fallback", Provider: "openai", APIInferenceType: "responses",
		BaseURL: provider.URL + "/v1", ModelID: "gpt-5.6-luna", DefaultOptions: map[string]any{},
		SupportsWebSearch: false,
	}
	cache := &alwaysHitCache{result: runtime.CachedResult{
		ProviderResult: runtime.ProviderResult{Output: "cached output"},
		Producer:       runtime.ExecutionTarget{ProfileID: profile.ID, Provider: profile.Provider, ModelID: profile.ModelID},
	}}
	record, err := runtime.Execute(
		context.Background(), router,
		func(context.Context, runtime.Profile) (runtime.Credential, error) {
			return runtime.Credential{APIKey: "fixture-secret"}, nil
		},
		profile.ID, map[string]runtime.Profile{profile.ID: profile},
		runtime.Call{CallType: "text", UserPrompt: "cached search", WebSearch: true},
		retry.Config{Policy: retry.Policy{MaxAttempts: 1, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}}}, cache, cachekey.ModeCache, cachekey.DefaultVersion, "call", "trace",
	)
	if err != nil {
		t.Fatalf("runtime.Execute: %v", err)
	}
	if record.Output != "cached output" || record.ResultSource.Kind != runtime.ResultSourceCache || record.Cache.Status != "hit" {
		t.Fatalf("cache replay = %#v", record)
	}
	if searcher.calls != 0 || cache.gets != 1 || cache.sets != 0 {
		t.Fatalf("cache/search calls = %d/%d/%d, want 1 lookup, no search/set", cache.gets, searcher.calls, cache.sets)
	}
}

func TestJinaSearcherUsesBoundedAuthenticatedSearchRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Query().Get("q") != "latest news & weather" {
			t.Errorf("Jina request = %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer jina-fixture-key" || request.Header.Get("Accept") != "text/plain" {
			t.Errorf("Jina headers = %v", request.Header)
		}
		_, _ = writer.Write([]byte("result from Jina"))
	}))
	defer server.Close()
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	searcher := &jinaSearcher{
		client: server.Client(), apiKey: "jina-fixture-key", baseURL: baseURL,
		timeout: time.Second, maxResponseBytes: 1024,
	}
	result, err := searcher.Search(context.Background(), " latest news & weather ")
	if err != nil || result != "result from Jina" {
		t.Fatalf("Jina Search = %q, %v", result, err)
	}
}

func TestJinaSearcherDoesNotExposeResponseBodyOnHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte("secret upstream diagnostic"))
	}))
	defer server.Close()
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	searcher := &jinaSearcher{client: server.Client(), apiKey: "jina-fixture-key", baseURL: baseURL, maxResponseBytes: 1024}
	_, err = searcher.Search(context.Background(), "query")
	if err == nil || strings.Contains(err.Error(), "secret upstream diagnostic") || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("Jina failure = %v", err)
	}
}

func assertNativeWebSearchPayload(t *testing.T, payload map[string]any) {
	t.Helper()
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("Responses tools = %#v", payload["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok || tool["type"] != "web_search" {
		t.Fatalf("Responses web-search tool = %#v", tools[0])
	}
	if objectValue(payload["tool_choice"])["type"] != "web_search" {
		t.Fatalf("Responses tool_choice = %#v, want specific web_search tool", payload["tool_choice"])
	}
	includes, ok := payload["include"].([]any)
	if !ok || len(includes) != 1 || includes[0] != "web_search_call.action.sources" {
		t.Fatalf("Responses source include = %#v", payload["include"])
	}
}

func newTLSTestRouter(t *testing.T, provider *httptest.Server, searcher Searcher) *Router {
	t.Helper()
	parsed, err := url.Parse(provider.URL)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(provider.Certificate())
	router, err := NewRouter(Config{
		EndpointPolicy: EndpointPolicy{
			PrivateAllowedHosts: []string{parsed.Hostname()},
			TLSConfig:           &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
		},
		WebSearcher: searcher,
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

type recordingSearcher struct {
	mu      sync.Mutex
	calls   int
	queries []string
	result  string
}

func (searcher *recordingSearcher) Search(_ context.Context, query string) (string, error) {
	searcher.mu.Lock()
	defer searcher.mu.Unlock()
	searcher.calls++
	searcher.queries = append(searcher.queries, query)
	return searcher.result, nil
}

type alwaysHitCache struct {
	result runtime.CachedResult
	gets   int
	sets   int
}

func (cache *alwaysHitCache) Get(context.Context, string, string) (runtime.CachedResult, bool, error) {
	cache.gets++
	return cache.result, true, nil
}

func (cache *alwaysHitCache) Set(context.Context, string, string, cachekey.Operation, runtime.CachedResult) error {
	cache.sets++
	return errors.New("cache set should not run on a cache hit")
}

func mustOperationHash(t *testing.T, operation cachekey.Operation) string {
	t.Helper()
	hash, err := cachekey.Hash(operation, cachekey.DefaultVersion)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	return hash
}
