package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-536

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/api"
	"github.com/prls-co/harden-llm/httpclient"
	"github.com/prls-co/harden-llm/stateful"
)

type projectionCache struct {
	mu      sync.Mutex
	records map[string]hardenllm.CacheRecord
}

func (c *projectionCache) Get(_ context.Context, key string) (hardenllm.CacheRecord, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.records[key]
	return v, ok, nil
}
func (c *projectionCache) Set(_ context.Context, key string, v hardenllm.CacheRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records[key] = v
	return nil
}
func (c *projectionCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.records, key)
	return nil
}

func TestResponsesSearchProjection(t *testing.T) {
	fixture, err := os.ReadFile("../../providers/testdata/web_search/openai_responses.json")
	if err != nil {
		t.Fatal(err)
	}
	structured, err := os.ReadFile("../../providers/testdata/web_search/openai_structured.json")
	if err != nil {
		t.Fatal(err)
	}
	edge, err := os.ReadFile("../../providers/testdata/web_search/openai_edge.json")
	if err != nil {
		t.Fatal(err)
	}
	gemini, err := os.ReadFile("../../providers/testdata/web_search/gemini.json")
	if err != nil {
		t.Fatal(err)
	}
	var dispatches atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatches.Add(1)
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, ":generateContent") {
			_, _ = w.Write(gemini)
			return
		}
		if strings.Contains(fmt.Sprint(input["input"]), "edge request") {
			_, _ = w.Write(edge)
			return
		}
		if input["text"] != nil {
			_, _ = w.Write(structured)
			return
		}
		w.Write(fixture)
	}))
	defer upstream.Close()
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	clientOptions := stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "native", Provider: "openai", Protocol: "responses", BaseURL: upstream.URL + "/v1", CacheDomain: "test", APIKey: "synthetic", SupportsWebSearch: true}, {ID: "gemini", Provider: "google", Protocol: "gemini-generate-content", BaseURL: upstream.URL, CacheDomain: "gemini", APIKey: "synthetic", SupportsWebSearch: true}}, DefaultConnection: "native", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	svc, err := stateful.NewService(stateful.Config{Client: clientOptions})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Config{APIKeys: []string{proxyTestToken}, Service: svc})
	if err != nil {
		t.Fatal(err)
	}
	for _, include := range []bool{false, true} {
		t.Run(fmt.Sprintf("include=%v", include), func(t *testing.T) {
			body := `{"model":"fixture","input":"hello","tools":[{"type":"web_search"}]`
			if include {
				body += `,"include":["web_search_call.action.sources"]`
			}
			body += `}`
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+proxyTestToken)
			w := httptest.NewRecorder()
			handler.Handler().ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if err := api.Validate("Response", w.Body.Bytes()); err != nil {
				t.Fatalf("public response violates OpenAPI: %v", err)
			}
			var result map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			items, ok := result["output"].([]any)
			if !ok || len(items) != 3 {
				t.Fatalf("search calls missing: %#v", result["output"])
			}
			first := items[0].(map[string]any)
			second := items[1].(map[string]any)
			if first["id"] != "ws_a1" || second["id"] != "ws_b2" {
				t.Fatalf("search call identity/order lost: %#v", items)
			}
			action := first["action"].(map[string]any)
			_, hasSources := action["sources"]
			if hasSources != include {
				t.Fatalf("source inclusion mismatch: %#v", action)
			}
			msg := items[2].(map[string]any)
			parts := msg["content"].([]any)
			annotations := parts[0].(map[string]any)["annotations"].([]any)
			if len(annotations) != 2 {
				t.Fatalf("citations lost: %#v", annotations)
			}
			streamBody := strings.TrimSuffix(body, "}") + `,"stream":true}`
			streamReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(streamBody))
			streamReq.Header.Set("Content-Type", "application/json")
			streamReq.Header.Set("Authorization", "Bearer "+proxyTestToken)
			stream := httptest.NewRecorder()
			handler.Handler().ServeHTTP(stream, streamReq)
			if stream.Code != 200 {
				t.Fatalf("stream status=%d body=%s", stream.Code, stream.Body.String())
			}
			var completed map[string]any
			var finished []any
			var names []string
			seq := 0
			for _, line := range strings.Split(stream.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				var event map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
					t.Fatal(err)
				}
				if event["sequence_number"] != float64(seq) {
					t.Fatalf("bad event sequence: %#v", event)
				}
				seq++
				name, _ := event["type"].(string)
				names = append(names, name)
				if name == "response.output_item.done" {
					finished = append(finished, event["item"])
				}
				if strings.HasPrefix(name, "response.output_text.") || name == "response.content_part.added" || name == "response.content_part.done" {
					if event["item_id"] == nil {
						t.Fatalf("missing item_id: %#v", event)
					}
				}
				if name == "response.completed" {
					completed, _ = event["response"].(map[string]any)
				}
			}
			if completed == nil || !reflect.DeepEqual(finished, completed["output"]) {
				t.Fatalf("stream output disagrees with completed response: %v / %#v", names, completed)
			}
			joined := strings.Join(names, ",")
			if !strings.Contains(joined, "response.web_search_call.in_progress,response.web_search_call.searching,response.web_search_call.completed") || !strings.Contains(joined, "response.output_text.annotation.added") {
				t.Fatalf("missing search or citation events: %v", names)
			}
		})
	}
	t.Run("edge calls", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"fixture","input":"edge request","tools":[{"type":"web_search"}]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+proxyTestToken)
		w := httptest.NewRecorder()
		handler.Handler().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var body map[string]any
		json.Unmarshal(w.Body.Bytes(), &body)
		items := body["output"].([]any)
		if len(items) != 4 || items[0].(map[string]any)["id"] != "ws_empty" || items[1].(map[string]any)["id"] != "ws_cancelled" || items[2].(map[string]any)["id"] != "ws_find" || items[3].(map[string]any)["type"] != "message" {
			t.Fatalf("edge ordering changed: %#v", items)
		}
		if _, ok := items[0].(map[string]any)["action"]; ok {
			t.Fatal("unreported action invented")
		}
	})
	t.Run("Gemini generated IDs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"fixture","input":"hello","tools":[{"type":"web_search"}],"harden":{"upstream":"gemini"}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+proxyTestToken)
		w := httptest.NewRecorder()
		handler.Handler().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var body map[string]any
		json.Unmarshal(w.Body.Bytes(), &body)
		items := body["output"].([]any)
		if len(items) != 3 {
			t.Fatalf("items=%#v", items)
		}
		callID := strings.TrimPrefix(items[2].(map[string]any)["id"].(string), "msg_")
		if items[0].(map[string]any)["id"] != "ws_"+callID+"_0" || items[1].(map[string]any)["id"] != "ws_"+callID+"_1" {
			t.Fatalf("generated IDs incorrect: %#v", items)
		}
	})
	t.Run("typed structured client", func(t *testing.T) {
		proxy := httptest.NewServer(handler.Handler())
		defer proxy.Close()
		client, err := httpclient.New(httpclient.Config{BaseURL: proxy.URL, APIKey: proxyTestToken, HTTPClient: proxy.Client()})
		if err != nil {
			t.Fatal(err)
		}
		input := httpclient.ResponsesInput{Model: "fixture", Input: []httpclient.ResponseMessage{{Role: "user", Content: "hello"}}, Text: httpclient.ResponseText{Format: httpclient.ResponseFormat{Type: "json_schema", Name: "answer", Strict: true, Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"},"n":{"type":"integer"}},"required":["answer","n"],"additionalProperties":false}`)}}, Tools: []json.RawMessage{json.RawMessage(`{"type":"web_search"}`)}, Include: []string{"web_search_call.action.sources"}}
		result, err := client.Responses(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Output) != 3 || result.Output[0].ID != "ws_a1" || len(result.Output[0].Action.Sources) != 2 || len(result.Output[2].Content[0].Annotations) != 1 {
			t.Fatalf("typed search result changed: %#v", result.Output)
		}
	})
	t.Run("cache replay", func(t *testing.T) {
		clientOptions.Cache = &projectionCache{records: make(map[string]hardenllm.CacheRecord)}
		client, err := stateful.NewClient(clientOptions)
		if err != nil {
			t.Fatal(err)
		}
		handler.clientForKey = func(string) (*hardenllm.Client, error) { return client, nil }
		before := dispatches.Load()
		send := func() *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"fixture","input":"hello","tools":[{"type":"web_search"}],"include":["web_search_call.action.sources"],"harden":{"cache":"cache"}}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+proxyTestToken)
			w := httptest.NewRecorder()
			handler.Handler().ServeHTTP(w, req)
			return w
		}
		first, second := send(), send()
		if first.Code != 200 || second.Code != 200 {
			t.Fatalf("status first=%d second=%d body=%s", first.Code, second.Code, second.Body.String())
		}
		if second.Header().Get("Cache-Status") != "harden-llm; hit" || dispatches.Load() != before+1 {
			t.Fatalf("cache was not served: header=%q dispatches=%d", second.Header().Get("Cache-Status"), dispatches.Load()-before)
		}
		var a, b map[string]any
		_ = json.Unmarshal(first.Body.Bytes(), &a)
		_ = json.Unmarshal(second.Body.Bytes(), &b)
		if a["id"] == b["id"] {
			t.Fatal("cache replay must retain a distinct response identity")
		}
		if !reflect.DeepEqual(a["output"], b["output"]) {
			t.Fatalf("cache changed output: first=%#v second=%#v", a["output"], b["output"])
		}
	})
}

func TestResponsesSearchProjectionGeneratedIDsOnCache(t *testing.T) {
	first := hardenllm.Result{CallID: "producer", Output: "answer", Search: &hardenllm.SearchResult{Calls: []hardenllm.SearchCall{{Status: "completed", Action: &hardenllm.SearchAction{Type: "search", Query: "q"}}}}}
	hit := first
	hit.CallID = "replay"
	hit.ProjectionCallID = "producer"
	hit.Cache.Served = true
	a, err := responsesResponse("model", first, 1, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := responsesResponse("model", hit, 2, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if a["id"] == b["id"] || !reflect.DeepEqual(a["output"], b["output"]) {
		t.Fatalf("generated IDs do not preserve the producing output: first=%#v replay=%#v", a, b)
	}
	items := b["output"].([]any)
	if items[0].(map[string]any)["id"] != "ws_producer_0" || items[1].(map[string]any)["id"] != "msg_producer" {
		t.Fatalf("generated IDs changed: %#v", items)
	}
}

func TestResponsesSearchProjectionStatusEvents(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   []string
	}{
		{"in_progress", []string{"in_progress"}}, {"searching", []string{"in_progress", "searching"}}, {"completed", []string{"in_progress", "searching", "completed"}}, {"failed", []string{"in_progress"}}, {"cancelled", []string{"in_progress"}},
	} {
		t.Run(tc.status, func(t *testing.T) {
			result := hardenllm.Result{CallID: "fixture", Output: "answer", Search: &hardenllm.SearchResult{Calls: []hardenllm.SearchCall{{ID: "ws_1", Status: tc.status}}}}
			final, err := responsesResponse("model", result, 1, false, false, false)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			(&API{}).streamResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", nil), final)
			var actual []string
			for _, line := range strings.Split(w.Body.String(), "\n") {
				if !strings.HasPrefix(line, "event: response.web_search_call.") {
					continue
				}
				actual = append(actual, strings.TrimPrefix(line, "event: response.web_search_call."))
			}
			if !reflect.DeepEqual(actual, tc.want) {
				t.Fatalf("events=%v want=%v", actual, tc.want)
			}
		})
	}
}

func TestResponsesSearchProjectionRepairAndReplay(t *testing.T) {
	for _, tc := range []struct{ name, initial string }{
		{"malformed", `{"answer":`},
		{"schema_invalid", `{"answer":"wrong","n":"bad"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := calls.Add(1)
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				text := `{"answer":"fixed","n":1}`
				output := []any{}
				if count == 1 {
					if len(request["tools"].([]any)) != 1 {
						t.Error("generation omitted search")
					}
					text = tc.initial
					output = append(output, map[string]any{"id": "ws_1", "type": "web_search_call", "status": "completed", "action": map[string]any{"type": "search", "query": "q", "sources": []any{map[string]any{"type": "url", "url": "https://example.test/a"}}}})
				} else if _, ok := request["tools"]; ok {
					t.Error("repair unexpectedly performed search")
				}
				annotation := []any{}
				if count == 1 {
					annotation = append(annotation, map[string]any{"type": "url_citation", "start_index": 0, "end_index": 1, "url": "https://example.test/a", "title": "A"})
				}
				output = append(output, map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": annotation}}})
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": output})
			}))
			defer upstream.Close()
			roots := x509.NewCertPool()
			roots.AddCert(upstream.Certificate())
			opts := stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "native", Provider: "openai", Protocol: "responses", BaseURL: upstream.URL + "/v1", CacheDomain: "test", APIKey: "synthetic", SupportsWebSearch: true}}, DefaultConnection: "native", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Cache: &projectionCache{records: make(map[string]hardenllm.CacheRecord)}}
			svc, err := stateful.NewService(stateful.Config{Client: opts})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := New(Config{APIKeys: []string{proxyTestToken}, Service: svc})
			if err != nil {
				t.Fatal(err)
			}
			client, err := stateful.NewClient(opts)
			if err != nil {
				t.Fatal(err)
			}
			handler.clientForKey = func(string) (*hardenllm.Client, error) { return client, nil }
			request := `{"model":"fixture","input":"hello","tools":[{"type":"web_search"}],"tool_choice":"required","include":["web_search_call.action.sources"],"harden":{"cache":"cache"},"text":{"format":{"type":"json_schema","name":"answer","strict":true,"schema":{"type":"object","properties":{"answer":{"type":"string"},"n":{"type":"integer"}},"required":["answer","n"],"additionalProperties":false}}}}`
			send := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+proxyTestToken)
				w := httptest.NewRecorder()
				handler.Handler().ServeHTTP(w, req)
				return w
			}
			first, second := send(), send()
			if first.Code != 200 || second.Code != 200 {
				t.Fatalf("first=%d %s; second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
			}
			if calls.Load() != 2 || second.Header().Get("Cache-Status") != "harden-llm; hit" {
				t.Fatalf("unexpected dispatch or cache: calls=%d header=%s", calls.Load(), second.Header().Get("Cache-Status"))
			}
			var a, b map[string]any
			json.Unmarshal(first.Body.Bytes(), &a)
			json.Unmarshal(second.Body.Bytes(), &b)
			if !reflect.DeepEqual(a["output"], b["output"]) {
				t.Fatal("cache lost repaired provenance")
			}
			items := a["output"].([]any)
			if len(items) != 2 || items[0].(map[string]any)["id"] != "ws_1" {
				t.Fatalf("search item lost: %#v", items)
			}
			action := items[0].(map[string]any)["action"].(map[string]any)
			if len(action["sources"].([]any)) != 1 {
				t.Fatal("search sources lost")
			}
			part := items[1].(map[string]any)["content"].([]any)[0].(map[string]any)
			if len(part["annotations"].([]any)) != 0 || !json.Valid([]byte(part["text"].(string))) {
				t.Fatalf("repaired JSON/citations invalid: %#v", part)
			}
		})
	}
}

func TestResponsesSearchProjectionNoReportedSearch(t *testing.T) {
	for _, search := range []*hardenllm.SearchResult{nil, {Mode: "native", Sources: []hardenllm.SearchSource{}, CostStatus: "unavailable"}} {
		result := hardenllm.Result{CallID: "fixture", Output: "answer", Search: search}
		got, err := responsesResponse("model", result, 1, false, true, false)
		if err != nil {
			t.Fatal(err)
		}
		expected := `{"created_at":1,"id":"resp_fixture","model":"model","object":"response","output":[{"content":[{"annotations":[],"text":"answer","type":"output_text"}],"id":"msg_fixture","role":"assistant","status":"completed","type":"message"}],"output_text":"answer","parallel_tool_calls":true,"status":"completed"}`
		raw, err := json.Marshal(got)
		if err != nil || string(raw) != expected {
			t.Fatalf("no-search golden changed: %s %v", raw, err)
		}
	}
}
