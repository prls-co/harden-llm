package providers

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-012 TEST-025 TEST-216

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
)

func TestNativeSearchProtocols(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		provider, protocol string
		choice             any
		tool               map[string]any
	}{
		{"cpa", "responses", map[string]any{"type": "web_search"}, map[string]any{"type": "web_search"}},
		{"openai", "responses", map[string]any{"type": "web_search"}, map[string]any{"type": "web_search"}},
		{"google", "gemini-generate-content", nil, map[string]any{"google_search": map[string]any{}}},
		{"anthropic", "anthropic-messages", map[string]any{"type": "auto"}, map[string]any{"type": "web_search_20250305", "name": "web_search", "max_uses": 3}},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			p := runtime.Profile{Provider: tc.provider, APIInferenceType: tc.protocol, SupportsWebSearch: true}
			c := runtime.Call{CallType: "text", UserPrompt: "search", WebSearch: true}
			_, _, _, body, _, err := buildPayload(p, c)
			if err != nil {
				t.Fatal(err)
			}
			if !nativeWebSearchEnabled(p, c) || !reflect.DeepEqual(body["tools"], []any{tc.tool}) || !reflect.DeepEqual(body["tool_choice"], tc.choice) {
				t.Fatalf("native payload: %#v", body)
			}
			c.WebSearch = false
			c.ProviderOptions = map[string]any{"tools": []any{tc.tool}, "tool_choice": tc.choice}
			_, _, _, body, _, err = buildPayload(p, c)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := body["tools"]; ok {
				t.Fatalf("off leaked search: %#v", body)
			}
		})
	}
}

func TestSearchEvidenceAndToolFailures(t *testing.T) {
	t.Parallel()
	r, err := normalizeResponse(preparedRequest{protocol: "openai.responses", callType: "text", searchMode: "native"}, []byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Searching..."}]},{"type":"web_search_call","status":"completed"},{"type":"message","content":[{"type":"output_text","text":"Final answer"}]}]}`))
	if err != nil || r.Output != "Final answer" {
		t.Fatalf("search preamble replaced answer: %#v %v", r, err)
	}
	_, err = normalizeResponse(preparedRequest{protocol: "anthropic.messages", callType: "text", searchMode: "native"}, []byte(`{"stop_reason":"end_turn","content":[{"type":"web_search_tool_result","content":{"type":"web_search_tool_result_error","error_code":"too_many_requests"}},{"type":"text","text":"Guess"}]}`))
	if err == nil {
		t.Fatal("native tool error was hidden by an unsourced answer")
	}
	for _, tc := range []struct{ protocol, body string }{
		{"openai.responses", `{"status":"completed","output":[{"type":"web_search_call","status":"completed"},{"type":"message","content":[{"type":"output_text","text":"a source","annotations":[{"type":"url_citation","url":"https://example.test","title":"Source","start_index":2,"end_index":8},{"type":"url_citation","url":"javascript:alert(1)"}]}]}]}`},
		{"google.gemini.generateContent", `{"candidates":[{"content":{"parts":[{"text":"a source"}]},"finishReason":"STOP","groundingMetadata":{"webSearchQueries":["query"],"groundingChunks":[{"web":{"uri":"https://example.test","title":"Source"}}],"searchEntryPoint":{"renderedContent":"<a href='https://example.test'>Search</a>"}}}]}`},
		{"anthropic.messages", `{"stop_reason":"end_turn","content":[{"type":"web_search_tool_result","content":[]},{"type":"text","text":"a source","citations":[{"type":"web_search_result_location","url":"https://example.test","title":"Source"}]}]}`},
	} {
		r, err := normalizeResponse(preparedRequest{protocol: tc.protocol, callType: "text", searchMode: "native"}, []byte(tc.body))
		if err != nil || r.Search == nil || !r.Search.Executed || len(r.Search.Sources) != 1 || r.Output != "a source" {
			t.Fatalf("%s: %#v %v", tc.protocol, r, err)
		}
		if tc.protocol == "openai.responses" && (len(r.Search.Citations) != 1 || r.Search.Citations[0].StartIndex != 2) {
			t.Fatal("inline citation lost")
		}
	}
	p := runtime.Profile{APIInferenceType: "anthropic-messages", SupportsWebSearch: true}
	if nativeWebSearchEnabled(p, runtime.Call{WebSearch: true, CallType: "structured"}) {
		t.Fatal("strict Claude output with native citations is not supported")
	}
}

func TestJinaMemoIsPerCallAndConcurrentAcrossPreparedOperations(t *testing.T) {
	t.Parallel()
	call := runtime.Call{WebSearch: true, UserPrompt: "same prompt", SearchMemo: &sync.Map{}}
	searcher := &recordingSearcher{result: "result"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := fallbackWebSearch(runtime.Profile{}, call)
			if _, err := s.results(context.Background(), searcher); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if searcher.calls != 1 {
		t.Fatalf("same call repeated search %d times", searcher.calls)
	}
	call.SearchMemo = &sync.Map{}
	if _, err := fallbackWebSearch(runtime.Profile{}, call).results(context.Background(), searcher); err != nil {
		t.Fatal(err)
	}
	if searcher.calls != 2 {
		t.Fatal("memo leaked across calls")
	}
}

func TestJinaBoundsCancellationAndMissingKey(t *testing.T) {
	t.Parallel()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("x", 128))) }))
	defer s.Close()
	u, _ := url.Parse(s.URL)
	search := &jinaSearcher{client: s.Client(), baseURL: u, apiKey: "fixture", timeout: time.Second, maxResponseBytes: 32}
	if _, err := search.Search(context.Background(), "query"); err == nil {
		t.Fatal("unbounded response accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := search.Search(ctx, "query"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	search.apiKey = ""
	if _, err := search.Search(context.Background(), "query"); err == nil {
		t.Fatal("missing Jina key accepted")
	}
	query := boundedWebSearchQuery(strings.Repeat("界", 2000))
	if len(query) > maxWebSearchQueryBytes || strings.Contains(query, "�") {
		t.Fatal("invalid UTF-8 query truncation")
	}
}

func TestSonarToggleAndUnrelatedTools(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{false, true} {
		_, _, _, body, _, err := buildPayload(runtime.Profile{Provider: "perplexity", APIInferenceType: "chat-completions", SupportsWebSearch: true}, runtime.Call{WebSearch: on, CallType: "text", ProviderOptions: map[string]any{"disable_search": on, "enable_search_classifier": true}})
		if err != nil || body["disable_search"] != !on || body["enable_search_classifier"] != false {
			t.Fatalf("Sonar toggle %#v %v", body, err)
		}
	}
	fn := map[string]any{"type": "function", "name": "custom"}
	_, _, _, body, _, err := buildPayload(runtime.Profile{Provider: "cpa", APIInferenceType: "responses", SupportsWebSearch: true}, runtime.Call{WebSearch: true, CallType: "text", ProviderOptions: map[string]any{"tools": []any{fn, map[string]any{"type": "web_search_preview"}}, "tool_choice": "none"}})
	if err != nil || len(arrayValue(body["tools"])) != 2 || !reflect.DeepEqual(arrayValue(body["tools"])[0], fn) || objectValue(body["tool_choice"])["type"] != "web_search" {
		t.Fatalf("specific native choice %#v %v", body, err)
	}
}

func TestWebSearchRejectsMalformedTools(t *testing.T) {
	t.Parallel()
	_, _, _, _, _, err := buildPayload(runtime.Profile{Provider: "cpa", APIInferenceType: "responses", SupportsWebSearch: true}, runtime.Call{WebSearch: true, ProviderOptions: map[string]any{"tools": "invalid"}})
	if err == nil {
		t.Fatal("malformed tools silently disabled native search")
	}
}

type searchMemoryCache struct {
	values map[string]runtime.CachedResult
}

func (c *searchMemoryCache) Get(_ context.Context, key, version string) (runtime.CachedResult, bool, error) {
	v, ok := c.values[key+version]
	return v, ok, nil
}
func (c *searchMemoryCache) Set(_ context.Context, key, version string, v runtime.CachedResult) error {
	c.values[key+version] = v
	return nil
}

func TestWebSearchCacheLifecycle(t *testing.T) {
	t.Parallel()
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "jina", true: "native"}[native], func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "web_search_call", "status": "completed"}, map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Answer", "annotations": []any{map[string]any{"type": "url_citation", "url": "https://example.test/source", "title": "Source"}}}}}}})
			}))
			defer server.Close()
			searcher := &recordingSearcher{result: "https://example.test/source evidence"}
			router := newTLSTestRouter(t, server, searcher)
			p := runtime.Profile{ID: "p", Provider: "cpa", APIInferenceType: "responses", BaseURL: server.URL, ModelID: "fixture", SupportsWebSearch: native}
			cache := &searchMemoryCache{values: map[string]runtime.CachedResult{}}
			run := func(search bool, mode cachekey.Mode) runtime.CallRecord {
				r, err := runtime.Execute(context.Background(), router, func(context.Context, runtime.Profile) (runtime.Credential, error) {
					return runtime.Credential{APIKey: "fixture-key"}, nil
				}, p.ID, map[string]runtime.Profile{p.ID: p}, runtime.Call{CallType: "text", UserPrompt: "query", WebSearch: search}, retry.Config{Policy: retry.Policy{MaxAttempts: 1, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}}}, cache, mode, cachekey.DefaultVersion, "call", "trace")
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			first := run(true, cachekey.ModeCache)
			if !first.Cache.Written || first.Search == nil || !first.Search.Executed || len(first.Search.Sources) != 1 {
				t.Fatalf("miss metadata: %#v", first)
			}
			hit := run(true, cachekey.ModeCache)
			if !hit.Cache.Served || calls != 1 || len(hit.Attempts) != 0 || !reflect.DeepEqual(first.Search, hit.Search) {
				t.Fatalf("hit: %#v calls=%d", hit, calls)
			}
			run(false, cachekey.ModeCache)
			if calls != 2 {
				t.Fatal("search and non-search collide")
			}
			refresh := run(true, cachekey.ModeRefresh)
			if !refresh.Cache.Written || refresh.Cache.Served || calls != 3 {
				t.Fatal("refresh did not replace same entry")
			}
			run(true, cachekey.ModeCache)
			if calls != 3 {
				t.Fatal("refresh result not cached")
			}
			wantSearches := 0
			if !native {
				wantSearches = 2
			}
			if searcher.calls != wantSearches {
				t.Fatalf("Jina calls %d want %d", searcher.calls, wantSearches)
			}
		})
	}
}

func TestJinaFailureIsNotAnLLMInvocation(t *testing.T) {
	t.Parallel()
	s := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("LLM called after empty search") }))
	defer s.Close()
	router := newTLSTestRouter(t, s, &recordingSearcher{})
	p := runtime.Profile{ID: "p", Provider: "cpa", APIInferenceType: "responses", BaseURL: s.URL, ModelID: "fixture"}
	r, err := runtime.Execute(context.Background(), router, func(context.Context, runtime.Profile) (runtime.Credential, error) {
		return runtime.Credential{APIKey: "fixture-key"}, nil
	}, p.ID, map[string]runtime.Profile{p.ID: p}, runtime.Call{CallType: "text", UserPrompt: "query", WebSearch: true}, retry.Config{Policy: retry.Policy{MaxAttempts: 1, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}}}, nil, cachekey.ModeOff, cachekey.DefaultVersion, "call", "trace")
	if err == nil || len(r.Attempts) != 1 || r.Attempts[0].ProviderUsed {
		t.Fatalf("pre-provider failure: %#v %v", r, err)
	}
}
