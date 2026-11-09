package providers

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-535

import (
	"context"
	"encoding/json"
	"github.com/prls-co/harden-llm/internal/runtime"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestWebSearchRequestParityPayload(t *testing.T) {
	connection := runtime.Connection{Provider: "openai", APIInferenceType: "responses", SupportsWebSearch: true}
	for _, mode := range []string{"", "auto", "required", "none"} {
		t.Run(mode, func(t *testing.T) {
			call := runtime.Call{ModelID: "fixture", CallType: "text", Messages: providerMessages("", "hello"), WebSearch: true, WebSearchTool: json.RawMessage(`{"type":"web_search_preview","filters":{"blocked_domains":["example.test"]}}`), ToolChoice: runtime.ToolChoice{Mode: mode}}
			_, _, _, payload, _, err := buildPayload(connection, call)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "none" {
				if len(arrayValue(payload["tools"])) != 0 {
					t.Fatal("none enabled search")
				}
				return
			}
			tools := arrayValue(payload["tools"])
			if len(tools) != 1 || objectValue(tools[0])["type"] != "web_search_preview" || objectValue(objectValue(tools[0])["filters"])["blocked_domains"] == nil {
				t.Fatalf("tool not forwarded: %#v", payload)
			}
			if mode == "required" {
				if !reflect.DeepEqual(payload["tool_choice"], map[string]any{"type": "web_search"}) {
					t.Fatalf("required search choice lost: %#v", payload)
				}
			} else if _, ok := payload["tool_choice"]; ok {
				t.Fatalf("auto forced search: %#v", payload)
			}
		})
	}
}

func TestWebSearchRequestParityFallbackHints(t *testing.T) {
	var dispatches atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}`))
	}))
	defer upstream.Close()
	searcher := &recordingSearcher{result: "https://example.test/source"}
	router := newTLSTestRouter(t, upstream, searcher)
	conn := runtime.Connection{ID: "fixture", Provider: "openai", APIInferenceType: "responses", BaseURL: upstream.URL, SupportsWebSearch: false}
	call := runtime.Call{ModelID: "fixture", CallType: "text", Messages: providerMessages("", "hello"), WebSearch: true, WebSearchTool: json.RawMessage(`{"type":"web_search","search_context_size":"low","user_location":{"type":"approximate","country":"US"}}`)}
	prepared, err := router.Prepare(context.Background(), conn, runtime.Credential{APIKey: "synthetic"}, call)
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.Execute(context.Background(), prepared)
	if err != nil || result.Search == nil || !result.Search.Executed || searcher.calls != 1 || dispatches.Load() != 1 {
		t.Fatalf("fallback failed: result=%#v error=%v", result, err)
	}
	call.WebSearchTool = json.RawMessage(`{"type":"web_search","filters":{"allowed_domains":["example.test"]}}`)
	if _, err := router.Prepare(context.Background(), conn, runtime.Credential{APIKey: "synthetic"}, call); err == nil || searcher.calls != 1 || dispatches.Load() != 1 {
		t.Fatalf("constraint dispatched: %v", err)
	}
}
