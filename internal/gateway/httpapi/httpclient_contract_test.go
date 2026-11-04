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
	"strings"
	"sync/atomic"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/httpclient"
	"github.com/prls-co/harden-llm/stateful"
)

func TestHTTPClientUsesActualReferenceGatewayAndRecovery(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid native provider request")
			return
		}
		text := `{"ok":true}`
		if calls.Add(1) == 1 {
			text = "not-json"
		}
		writePresetNativeResponse(w, r, body, text)
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
			EndpointPolicy:    hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}},
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
}
