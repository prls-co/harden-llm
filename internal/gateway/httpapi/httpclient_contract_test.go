package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-482

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/httpclient"
	"github.com/prls-co/harden-llm/internal/testfixture/native"
	"github.com/prls-co/harden-llm/stateful"
)

func TestHTTPClientUsesActualReferenceGatewayAndRecovery(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var body map[string]any
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != "fixture-generation-model" || n > 3 {
			http.Error(w, "unexpected native request", http.StatusConflict)
			return
		}
		encoded, _ := json.Marshal(body["input"])
		marker := "Return the fixture object."
		if n == 2 {
			marker = "not-json"
		}
		if n == 3 {
			marker = "Return a grounded fixture object."
		}
		if !strings.Contains(string(encoded), marker) {
			http.Error(w, "unexpected native input", http.StatusConflict)
			return
		}
		text := `{"ok":true}`
		if n == 1 {
			text = "not-json"
		}
		if n == 3 {
			tools, _ := body["tools"].([]any)
			found := false
			for _, tool := range tools {
				value, _ := tool.(map[string]any)
				found = found || value["type"] == "web_search"
			}
			if !found {
				http.Error(w, "missing native search tool", http.StatusConflict)
				return
			}
			native.WriteResponse(w, r, body, text, hardenllm.SearchSource{URL: "https://example.invalid/company", Title: "Independent fixture source"})
			return
		}
		native.WriteResponse(w, r, body, text)
	}))
	t.Cleanup(provider.Close)
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	catalog, err := stateful.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	const profileID = "CPA gpt-6.1-sol"
	preset := catalog[profileID]
	var definition map[string]any
	if err := json.Unmarshal(preset.Definition, &definition); err != nil {
		t.Fatal(err)
	}
	definition["baseUrl"], definition["modelId"] = provider.URL+"/v1", "fixture-generation-model"
	preset.Definition, err = json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	service, err := stateful.NewService(stateful.Config{
		Presets: stateful.Catalog{profileID: preset},
		Client: stateful.ClientOptions{
			Connections:       []hardenllm.Connection{{ID: preset.Binding.DeploymentUpstreamID, Provider: definition["provider"].(string), Protocol: definition["apiInferenceType"].(string), BaseURL: provider.URL + "/v1", APIKey: "synthetic-provider-key"}},
			DefaultConnection: preset.Binding.DeploymentUpstreamID,
			EndpointPolicy:    hardenllm.EndpointPolicy{AllowedHosts: []string{"127.0.0.1"}, PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("fixture-client-key-", 3)
	gateway, err := New(Config{APIKeys: []string{key}, Service: service})
	if err != nil {
		t.Fatal(err)
	}
	var inferencePOSTs atomic.Int32
	handler := gateway.Handler()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/run" {
			inferencePOSTs.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := httpclient.New(httpclient.Config{BaseURL: server.URL, APIKey: key, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	input := httpclient.RunInput{
		ProfileID: profileID, UserPrompt: "Return the fixture object.", CallType: hardenllm.CallTypeStructured,
		Schema:         json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`),
		CacheMode:      hardenllm.CacheModeOff,
		RecoveryPolicy: json.RawMessage(`{"maxAttempts":2,"retryOn":[],"jsonRepair":{"initial":{"source":"generation"},"escalation":null},"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`),
		Origin:         hardenllm.Origin{Client: "query-analytics", OperationID: "fixture-gateway-operation"},
	}
	outcome, err := client.Run(context.Background(), input)
	if err != nil || string(outcome.Result.Output) != `{"ok":true}` || outcome.Evidence == nil || outcome.Evidence.ProviderAttemptCount != 2 || !outcome.Result.UsedRepair || outcome.Result.SelectedTarget.ModelID != "fixture-generation-model" || outcome.Recording.Status != "disabled" || calls.Load() != 2 || inferencePOSTs.Load() != 1 {
		t.Fatalf("client/gateway recovery contract failed: status=%s attempts=%d recording=%s calls=%d posts=%d err=%v", outcome.Result.Status, len(outcome.Result.Attempts), outcome.Recording.Status, calls.Load(), inferencePOSTs.Load(), err)
	}
	// A real pre-stream owner rejection must not invent complete attempt
	// evidence or trigger another inference inside the client.
	input.ProfileID = "missing-fixture-profile"
	_, err = client.Run(context.Background(), input)
	var rejected *httpclient.RejectedError
	if !errors.As(err, &rejected) || calls.Load() != 2 || inferencePOSTs.Load() != 2 {
		t.Fatalf("owner rejection misclassified or replayed: posts=%d calls=%d err=%v", inferencePOSTs.Load(), calls.Load(), err)
	}
	// Native search data crosses the real provider parser and streamed gateway;
	// the fixture never supplies a normalized SearchResult or complete outcome.
	input.ProfileID, input.UserPrompt = profileID, "Return a grounded fixture object."
	input.Origin.OperationID, input.WebSearch = "fixture-grounded-operation", true
	grounded, err := client.Run(context.Background(), input)
	expectedSources := []hardenllm.SearchSource{{URL: "https://example.invalid/company", Title: "Independent fixture source"}}
	if err != nil || string(grounded.Result.Output) != `{"ok":true}` || grounded.Result.Search == nil || !grounded.Result.Search.Executed || grounded.Result.Search.Mode != "native" || !reflect.DeepEqual(grounded.Result.Search.Sources, expectedSources) || grounded.Evidence == nil || grounded.Evidence.ProviderAttemptCount != 1 || grounded.Recording.Status != "disabled" || calls.Load() != 3 || inferencePOSTs.Load() != 3 {
		t.Fatal("actual parser/client failed to retain native grounded evidence or changed dispatch count")
	}
}
