package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-535

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
	"net/http/httptest"
	"testing"
)

func TestWebSearchRequestParityPreparation(t *testing.T) {
	for _, toolType := range []string{"web_search", "web_search_2025_08_26", "web_search_preview", "web_search_preview_2025_03_11"} {
		t.Run(toolType, func(t *testing.T) {
			tool, _ := json.Marshal(map[string]any{"type": toolType, "filters": map[string]any{"blocked_domains": []string{"example.test"}}})
			req := responsesRequest{Model: "fixture", Input: json.RawMessage(`"hello"`), Tools: []json.RawMessage{tool}, Include: []string{"web_search_call.action.sources"}}
			recorder := httptest.NewRecorder()
			prepared, ok := prepareResponses(recorder, req)
			if !ok || !prepared.Request.WebSearch {
				t.Fatalf("search request rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestWebSearchRequestParityOutputAsInput(t *testing.T) {
	input := json.RawMessage(`[{"id":"ws_1","type":"web_search_call","status":"completed","action":{"type":"search","query":"q"}},{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[{"type":"url_citation","url":"https://example.test"}]}]},{"role":"user","content":"next"}]`)
	messages, err := decodeResponsesInput(input, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Role != "assistant" || string(messages[0].Content) != `"answer"` || messages[1].Role != "user" {
		t.Fatalf("invalid multi-turn projection: %#v", messages)
	}
}

func TestWebSearchRequestParityGatewayToProvider(t *testing.T) {
	received := make(chan map[string]any, 16)
	var dispatches atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		dispatches.Add(1)
		received <- input
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}`)
	}))
	defer upstream.Close()
	certs := x509.NewCertPool()
	certs.AddCert(upstream.Certificate())
	svc, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "native", Provider: "openai", Protocol: "responses", BaseURL: upstream.URL + "/v1", CacheDomain: "test", APIKey: "synthetic", SupportsWebSearch: true}, {ID: "fallback", Provider: "openai", Protocol: "responses", BaseURL: upstream.URL + "/v1", CacheDomain: "fallback", APIKey: "synthetic"}}, DefaultConnection: "native", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, TLSConfig: &tls.Config{RootCAs: certs, MinVersion: tls.VersionTLS12}}}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{APIKeys: []string{proxyTestToken}, Service: svc})
	if err != nil {
		t.Fatal(err)
	}
	send := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+proxyTestToken)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, req)
		return w
	}
	for _, kind := range []string{"web_search", "web_search_2025_08_26", "web_search_preview", "web_search_preview_2025_03_11"} {
		t.Run(kind, func(t *testing.T) {
			body := fmt.Sprintf(`{"model":"fixture","input":"hello","tools":[{"type":%q,"filters":{"blocked_domains":["example.test"]},"external_web_access":false}],"include":["web_search_call.action.sources"]}`, kind)
			w := send(body)
			if w.Code != 200 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			got := <-received
			tools, ok := got["tools"].([]any)
			if !ok || len(tools) != 1 {
				t.Fatalf("tools=%#v", got["tools"])
			}
			tool := tools[0].(map[string]any)
			if tool["type"] != kind || tool["external_web_access"] != false || tool["filters"].(map[string]any)["blocked_domains"] == nil {
				t.Fatalf("tool changed: %#v", tool)
			}
			if _, ok := got["tool_choice"]; ok {
				t.Fatalf("auto was forced: %#v", got)
			}
		})
	}
	for _, choice := range []struct {
		value      string
		wantChoice bool
		wantTool   bool
	}{{`"auto"`, false, true}, {`"required"`, true, true}, {`{"type":"web_search_preview"}`, true, true}, {`"none"`, false, false}} {
		t.Run(choice.value, func(t *testing.T) {
			w := send(fmt.Sprintf(`{"model":"fixture","input":"hello","tools":[{"type":"web_search"}],"tool_choice":%s}`, choice.value))
			if w.Code != 200 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			got := <-received
			if value, ok := got["tool_choice"]; ok != choice.wantChoice {
				t.Fatalf("choice=%#v", got)
			} else if ok {
				selected, valid := value.(map[string]any)
				if !valid || len(selected) != 1 || selected["type"] != "web_search" {
					t.Fatalf("required search must select the search tool: %#v", value)
				}
			}
			if _, ok := got["tools"]; ok != choice.wantTool {
				t.Fatalf("tools=%#v", got)
			}
		})
	}
	t.Run("prior output becomes conversation", func(t *testing.T) {
		w := send(`{"model":"fixture","input":[{"id":"ws_1","type":"web_search_call","status":"completed","action":{"type":"search","query":"q"}},{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[]}]},{"role":"user","content":"next"}]}`)
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		got := <-received
		items, ok := got["input"].([]any)
		if !ok || len(items) != 2 || items[0].(map[string]any)["role"] != "assistant" || items[1].(map[string]any)["role"] != "user" {
			t.Fatalf("unexpected upstream conversation: %#v", got["input"])
		}
	})
	t.Run("fallback constraint is rejected", func(t *testing.T) {
		before := dispatches.Load()
		w := send(`{"model":"fixture","input":"hello","tools":[{"type":"web_search","filters":{"allowed_domains":["example.test"]}}],"harden":{"upstream":"fallback"}}`)
		if w.Code != 400 || dispatches.Load() != before || !strings.Contains(w.Body.String(), `"param":"tools"`) {
			t.Fatalf("constraint silently ignored: status=%d body=%s", w.Code, w.Body.String())
		}
	})
	before := dispatches.Load()
	w := send(`{"model":"fixture","input":"hello","include":["web_search_call.results"]}`)
	if w.Code != 400 || dispatches.Load() != before || !strings.Contains(w.Body.String(), "include") {
		t.Fatalf("invalid include: status=%d body=%s", w.Code, w.Body.String())
	}
}
