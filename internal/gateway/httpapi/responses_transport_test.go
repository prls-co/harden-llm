package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-422 / TEST-474.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/httpclient"
	"github.com/prls-co/harden-llm/stateful"
)

// NER uses the ordinary typed Responses client and expects one buffered JSON
// result. CPA can take longer to finish than the transport's header budget.
// Scale those two real wire delays down, retaining their production ordering.
func TestOrdinaryCPAResponsesCompletesAfterHeaderBudgetWithoutIncreasingDeadline(t *testing.T) {
	t.Parallel()
	const headerBudget = 100 * time.Millisecond
	var calls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var input struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		if input.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n")
			w.(http.Flusher).Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(4 * headerBudget):
		}
		response := `{"id":"resp-ner","status":"completed","output_text":"{\"ok\":true}","usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}`
		if input.Stream {
			fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, response)
		}
	}))
	t.Cleanup(provider.Close)
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	service, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{
		Connections:       []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: provider.URL + "/v1", CacheDomain: "fixture", APIKey: "synthetic"}},
		DefaultConnection: "cpa",
		EndpointPolicy:    hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: headerBudget},
	}})
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(Config{APIKeys: []string{proxyTestToken}, Service: service})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(api.Handler())
	t.Cleanup(gateway.Close)
	client, err := httpclient.New(httpclient.Config{BaseURL: gateway.URL, APIKey: proxyTestToken, HTTPClient: gateway.Client()})
	if err != nil {
		t.Fatal(err)
	}
	policy := hardenllm.DefaultRecoveryPolicy()
	policy.MaxAttempts, policy.JSONRepair, policy.Rerun = 1, nil, nil
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	result, err := client.Responses(ctx, httpclient.ResponsesInput{
		Model: "fixture-model", Input: []httpclient.ResponseMessage{{Role: "user", Content: "extract"}},
		Text:   httpclient.ResponseText{Format: httpclient.ResponseFormat{Type: "json_schema", Name: "ner", Schema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`), Strict: true}},
		Harden: httpclient.ResponseControls{Cache: hardenllm.CacheModeOff, TimeoutMS: 1000, Recovery: &policy, Diagnostics: true},
	})
	if err != nil || result.OutputText != `{"ok":true}` || calls.Load() != 1 {
		t.Fatalf("ordinary CPA response stopped before its generation budget: output=%q calls=%d error=%v", result.OutputText, calls.Load(), err)
	}
}
