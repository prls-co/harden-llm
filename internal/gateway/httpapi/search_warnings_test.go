package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-546

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/api"
	"github.com/prls-co/harden-llm/stateful"
)

func TestSearchWarningsGatewayProjection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, provider, protocol, body string }{
		{"anthropic", "anthropic", "anthropic-messages", `{"stop_reason":"end_turn","content":[{"type":"text","text":"café","citations":[{"type":"web_search_result_location","url":"https://example.test/a","title":"A"},{"type":"future_citation"}]}]}`},
		{"gemini", "google", "gemini-generate-content", `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"café"}]},"groundingMetadata":{"webSearchQueries":["q"],"groundingChunks":[{"web":{"uri":"https://example.test/a","title":"A"}}],"groundingSupports":[{"segment":{"endIndex":5,"text":"café"},"groundingChunkIndices":[0,99]}]}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dispatches atomic.Int32
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				dispatches.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			}))
			t.Cleanup(upstream.Close)
			roots := x509.NewCertPool()
			roots.AddCert(upstream.Certificate())
			service, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{
				Connections:       []hardenllm.Connection{{ID: "native", Provider: tc.provider, Protocol: tc.protocol, BaseURL: upstream.URL, APIKey: "synthetic", SupportsWebSearch: true}},
				DefaultConnection: "native", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			gateway, err := New(Config{APIKeys: []string{proxyTestToken}, Service: service})
			if err != nil {
				t.Fatal(err)
			}
			for i, stream := range []bool{false, true} {
				body := fmt.Sprintf(`{"model":"fixture","input":"hello","stream":%v,"tools":[{"type":"web_search"}],"harden":{"diagnostics":true}}`, stream)
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer "+proxyTestToken)
				recorder := httptest.NewRecorder()
				gateway.Handler().ServeHTTP(recorder, request)
				if recorder.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
				}
				var output map[string]any
				if !stream {
					if err := json.Unmarshal(recorder.Body.Bytes(), &output); err != nil {
						t.Fatal(err)
					}
				} else {
					for _, line := range strings.Split(recorder.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: {") {
							continue
						}
						var event map[string]any
						if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
							t.Fatal(err)
						}
						if event["type"] == "response.completed" {
							output = event["response"].(map[string]any)
						}
					}
				}
				if output == nil {
					t.Fatal("terminal answer missing")
				}
				encoded, _ := json.Marshal(output)
				if err := api.Validate("Response", encoded); err != nil {
					t.Fatal(err)
				}
				if output["output_text"] != "café" {
					t.Fatal("answer changed")
				}
				status := output["harden"].(map[string]any)["search"].(map[string]any)
				if status["incomplete"] != true || len(status["warnings"].([]any)) != 1 {
					t.Fatalf("omission status lost: %#v", status)
				}
				found := false
				for _, item := range output["output"].([]any) {
					message := item.(map[string]any)
					if message["type"] != "message" {
						continue
					}
					annotations := message["content"].([]any)[0].(map[string]any)["annotations"].([]any)
					found = len(annotations) == 1 && annotations[0].(map[string]any)["start_index"] == float64(0) && annotations[0].(map[string]any)["end_index"] == float64(4)
				}
				if !found || dispatches.Load() != int32(i+1) {
					t.Fatalf("valid citation lost or inference repeated: found=%v dispatches=%d", found, dispatches.Load())
				}
			}
		})
	}
}

func TestSearchWarningsDiagnosticsStayBounded(t *testing.T) {
	t.Parallel()
	result := hardenllm.Result{Search: &hardenllm.SearchResult{Incomplete: true, Warnings: []string{"invalid_citation"}, Sources: []hardenllm.SearchSource{{URL: "https://example.test/a", Title: strings.Repeat("title", 20000)}}}}
	extension := hardenExtension(result)
	if err := validateHardenSize(extension); err != nil {
		t.Fatal("optional status duplicated large provider evidence")
	}
	encoded, _ := json.Marshal(extension)
	if strings.Contains(string(encoded), "titletitle") {
		t.Fatal("diagnostics duplicated source content")
	}
}
