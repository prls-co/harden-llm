package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-403 / TEST-535.
// Historical observations on 9554f5f. Run explicitly through the adjacent overlay.
import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
)

type issue186Counts struct {
	projectionCache
	reads, writes atomic.Int32
}

func (c *issue186Counts) Get(ctx context.Context, key string) (hardenllm.CacheRecord, bool, error) {
	c.reads.Add(1)
	return c.projectionCache.Get(ctx, key)
}

func (c *issue186Counts) Set(ctx context.Context, key string, record hardenllm.CacheRecord) error {
	c.writes.Add(1)
	return c.projectionCache.Set(ctx, key, record)
}

func TestIssue186Baseline(t *testing.T) {
	const function = `{"type":"function","name":"lookup","parameters":{"type":"object"}}`
	captured := make(chan map[string]any, 16)
	var calls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		captured <- body
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if _, ok := body["tools"]; ok {
			io.WriteString(w, `{"status":"completed","output":[{"type":"function_call","id":"fc_fixture","call_id":"call_fixture","status":"completed","name":"lookup","arguments":"{}"}]}`)
			return
		}
		io.WriteString(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer provider.Close()
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	cache := &issue186Counts{projectionCache: projectionCache{records: make(map[string]hardenllm.CacheRecord)}}
	client, err := stateful.NewClient(stateful.ClientOptions{
		DefaultConnection: "fixture", Cache: cache,
		Connections:    []hardenllm.Connection{{ID: "fixture", Provider: "openai", Protocol: "responses", BaseURL: provider.URL + "/v1", APIKey: "synthetic", CacheDomain: "fixture", SupportsWebSearch: true}},
		EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}},
	})
	if err != nil {
		t.Fatal(err)
	}
	api := newContractAPI(t, provider)
	api.clientForKey = func(string) (*hardenllm.Client, error) { return client, nil }
	send := func(path string, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+proxyTestToken)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, req)
		return w
	}

	for _, setting := range []string{"omitted", "false", "true"} {
		body := map[string]any{"model": "plain", "input": "hello"}
		if setting != "omitted" {
			body["store"] = setting == "true"
		}
		before := calls.Load()
		w := send("/v1/responses", body)
		if setting == "true" {
			if w.Code != 400 || calls.Load() != before {
				t.Fatalf("store=true: HTTP %d, dispatches %d", w.Code, calls.Load()-before)
			}
			continue
		}
		if w.Code != 200 || calls.Load() != before+1 {
			t.Fatalf("store=%s: HTTP %d, dispatches %d", setting, w.Code, calls.Load()-before)
		}
		if _, present := (<-captured)["store"]; present {
			t.Fatalf("store=%s unexpectedly reached fixture", setting)
		}
		t.Logf("store=%s -> upstream field omitted", setting)
	}
	before := calls.Load()
	w := send("/v1/responses", map[string]any{"model": "selector", "input": "hello", "tools": []json.RawMessage{json.RawMessage(function)}, "tool_choice": map[string]string{"type": "web_search"}})
	if w.Code != 200 || calls.Load() != before+1 || !bytes.Contains(w.Body.Bytes(), []byte(`"type":"function_call"`)) {
		t.Fatalf("selector: HTTP %d: %s", w.Code, w.Body.String())
	}
	if got := (<-captured)["tool_choice"]; got != "required" {
		t.Fatalf("selector = %v", got)
	}
	t.Log("search selector with only lookup declared -> upstream required -> HTTP 200 function_call")

	for _, path := range []string{"/v1/responses", "/v1/chat/completions"} {
		body := map[string]any{"model": path, "input": "hello", "tools": []json.RawMessage{json.RawMessage(function)}}
		if path == "/v1/chat/completions" {
			delete(body, "input")
			body["messages"] = []map[string]string{{"role": "user", "content": "hello"}}
			body["tools"] = []json.RawMessage{json.RawMessage(`{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}`)}
		}
		for _, tc := range []struct {
			mode                      string
			reads, writes, dispatches int32
		}{
			{"omitted", 0, 0, 1}, {"off", 0, 0, 1}, {"refresh", 0, 1, 1}, {"cache", 1, 0, 0},
		} {
			if tc.mode != "omitted" {
				body["harden"] = map[string]string{"cache": tc.mode}
			}
			reads, writes, dispatches := cache.reads.Load(), cache.writes.Load(), calls.Load()
			w := send(path, body)
			if w.Code != 200 || cache.reads.Load()-reads != tc.reads || cache.writes.Load()-writes != tc.writes || calls.Load()-dispatches != tc.dispatches {
				t.Fatalf("%s %s: HTTP %d, reads/writes/dispatches %d/%d/%d", path, tc.mode, w.Code, cache.reads.Load()-reads, cache.writes.Load()-writes, calls.Load()-dispatches)
			}
			if tc.dispatches > 0 {
				<-captured
			}
			hit := tc.mode == "cache"
			if (w.Header().Get("Cache-Status") == "harden-llm; hit") != hit {
				t.Fatalf("%s %s cache header: %q", path, tc.mode, w.Header().Get("Cache-Status"))
			}
			var out struct {
				Output []struct {
					Type    string
					Content []struct{ Text string }
				}
				Choices []struct {
					Finish  string `json:"finish_reason"`
					Message struct {
						Content string
						Tools   []json.RawMessage `json:"tool_calls"`
					}
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			var shape, text string
			if path == "/v1/responses" {
				if len(out.Output) != 1 {
					t.Fatal(w.Body.String())
				}
				item := out.Output[0]
				shape = item.Type
				if hit {
					if shape != "message" || len(item.Content) != 1 {
						t.Fatal(w.Body.String())
					}
					text = item.Content[0].Text
				} else if shape != "function_call" {
					t.Fatal(w.Body.String())
				}
			} else {
				if len(out.Choices) != 1 {
					t.Fatal(w.Body.String())
				}
				choice := out.Choices[0]
				shape = choice.Finish
				if hit {
					if shape != "stop" || len(choice.Message.Tools) != 0 {
						t.Fatal(w.Body.String())
					}
					text = choice.Message.Content
				} else if shape != "tool_calls" || len(choice.Message.Tools) != 1 {
					t.Fatal(w.Body.String())
				}
			}
			if hit {
				var lost hardenllm.AssistantOutput
				if json.Unmarshal([]byte(text), &lost) != nil || len(lost.ToolCalls) != 1 || lost.ToolCalls[0].Function.Name != "lookup" {
					t.Fatalf("cached text lacks serialized function: %q", text)
				}
			}
			t.Logf("%s %s -> %d/%d/%d reads/writes/model, %s", path, tc.mode, tc.reads, tc.writes, tc.dispatches, shape)
		}
	}
}
