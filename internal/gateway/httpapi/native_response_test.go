package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-403

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/api"
	"github.com/prls-co/harden-llm/stateful"
)

func TestOpenAIContractNativeResponseReplay(t *testing.T) {
	const response = `{"id":"resp_original","model":"provider-model","created_at":123,"object":"response","status":"completed","parallel_tool_calls":false,"extension":{"n":9007199254740993,"optional":null},"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5,"extra":true},"output":[{"type":"reasoning","id":"rs_original","summary":[{"type":"summary_text","text":"why"}]},{"type":"future_item","future":true},{"type":"message","id":"msg_original","status":"completed","role":"assistant","content":[{"type":"output_text","annotations":[],"text":"answer","extra":false}]}]}`
	const chat = `{"id":"chatcmpl-original","object":"chat.completion","created":456,"model":"provider-model","system_fingerprint":"fp_original","extension":{"n":9007199254740993,"optional":null},"choices":[{"index":0,"finish_reason":"stop","logprobs":null,"message":{"role":"assistant","content":"answer","extra":false}}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5,"extra":true}}`
	for _, tc := range []struct {
		name, protocol, path, input, reply, schema string
		upstreamSSE                                bool
	}{
		{"responses-sse", "responses", "/v1/responses", `"input":"hello","reasoning":{"summary":"auto","effort":"low"}`, response, "Response", true},
		{"responses-json", "responses", "/v1/responses", `"input":"hello"`, response, "Response", false},
		{"chat", "chat-completions", "/v1/chat/completions", `"messages":[{"role":"user","content":"hello"}]`, chat, "ChatCompletion", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != tc.path {
					t.Errorf("upstream path: %s", r.URL.Path)
				}
				var input map[string]any
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if tc.upstreamSSE && !reflect.DeepEqual(input["reasoning"], map[string]any{"summary": "auto", "effort": "low"}) {
					t.Errorf("upstream reasoning = %v", input["reasoning"])
				}
				reply := strings.Replace(tc.reply, `"extension":`, `"harden":{"spoof":true},"extension":`, 1)
				reply = strings.Replace(reply, `"id":"rs_original"`, `"id":"rs_original","encrypted_content":"opaque_marker"`, 1)
				if tc.upstreamSSE {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", reply)
				} else {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, reply)
				}
			}))
			defer upstream.Close()
			cache := &countedContractCache{projectionCache: projectionCache{records: map[string]hardenllm.CacheRecord{}}}
			opts := contractClientOptions(upstream)
			opts.Connections[0].Protocol = tc.protocol
			opts.Cache = cache
			client, err := stateful.NewClient(opts)
			if err != nil {
				t.Fatal(err)
			}
			gateway := newContractAPI(t, upstream)
			gateway.clientForKey = func(string) (*hardenllm.Client, error) { return client, nil }
			send := func(stream, diag bool) *httptest.ResponseRecorder {
				body := fmt.Sprintf(`{"model":"fixture",%s,"stream":%t,"harden":{"cache":"cache","diagnostics":%t}}`, tc.input, stream, diag)
				w := postOrdinary(gateway, tc.path, body)
				if w.Code != 200 {
					t.Fatalf("HTTP %d: %s", w.Code, w.Body)
				}
				return w
			}
			decode := func(body string) map[string]any {
				t.Helper()
				var v map[string]any
				dec := json.NewDecoder(strings.NewReader(body))
				dec.UseNumber()
				if err := dec.Decode(&v); err != nil {
					t.Fatal(err)
				}
				return v
			}
			want := decode(tc.reply)
			if tc.protocol == "responses" {
				want["output_text"] = "answer"
			}
			var firstExec string
			for i := 0; i < 2; i++ {
				w := send(false, true)
				if err := api.Validate(tc.schema, w.Body.Bytes()); err != nil {
					t.Fatal(err)
				}
				got := decode(w.Body.String())
				d := got["harden"].(map[string]any)
				delete(got, "harden")
				id, _ := d["execution_id"].(string)
				if !reflect.DeepEqual(want, got) || id == "" || id == firstExec || d["spoof"] != nil || d["cache"].(map[string]any)["served"] != (i == 1) {
					t.Fatalf("delivery %d: %s", i, w.Body)
				}
				firstExec = id
			}
			if public := send(false, false); !reflect.DeepEqual(decode(public.Body.String()), want) {
				t.Fatal(public.Body)
			}
			stream := send(true, false)
			if tc.protocol == "responses" {
				var completed map[string]any
				var done []any
				var summaryText strings.Builder
				for _, line := range strings.Split(stream.Body.String(), "\n") {
					if strings.HasPrefix(line, "data: ") {
						event := decode(strings.TrimPrefix(line, "data: "))
						switch event["type"] {
						case "response.reasoning_summary_text.delta":
							summaryText.WriteString(event["delta"].(string))
						case "response.completed":
							completed = event["response"].(map[string]any)
						case "response.output_item.done":
							done = append(done, event["item"])
						}
					}
				}
				if summaryText.String() != "why" || !reflect.DeepEqual(completed, want) || !reflect.DeepEqual(done, want["output"]) {
					t.Fatalf("stream mismatch: %s", stream.Body)
				}
			} else if !strings.Contains(stream.Body.String(), "chatcmpl-original") || !strings.Contains(stream.Body.String(), "[DONE]") {
				t.Fatal(stream.Body)
			}
			if calls.Load() != 1 || cache.writes != 1 || stream.Header().Get("Cache-Status") != "harden-llm; hit" {
				t.Fatalf("calls=%d writes=%d", calls.Load(), cache.writes)
			}
			for _, record := range cache.records {
				if strings.Contains(string(record.ProviderResult), "opaque_marker") || strings.Contains(string(record.ProviderResult), "spoof") {
					t.Fatal("cached excluded provider fields")
				}
			}
		})
	}
}

func TestResponsesSummaryOnIncompatibleTarget(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	cache := &countedContractCache{projectionCache: projectionCache{records: map[string]hardenllm.CacheRecord{}}}
	opts := contractClientOptions(upstream)
	opts.Connections[0].Protocol = "chat-completions"
	opts.Cache = cache
	client, err := stateful.NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	gateway := newContractAPI(t, upstream)
	gateway.clientForKey = func(string) (*hardenllm.Client, error) { return client, nil }
	w := postOrdinary(gateway, "/v1/responses", `{"model":"fixture","input":"hi","reasoning":{"summary":"auto"},"harden":{"cache":"cache"}}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"param":"reasoning.summary"`) || calls.Load() != 0 || cache.reads != 0 || cache.writes != 0 {
		t.Fatalf("incompatible target: %d %s, dispatch=%d cache=%d/%d", w.Code, w.Body, calls.Load(), cache.reads, cache.writes)
	}
}
