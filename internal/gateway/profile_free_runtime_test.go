package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-402 PLAN-HLLM-PROXY-REFERENCE-001

import (
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
)

func TestProfileFreeRuntime(t *testing.T) {
	const providerCredential = "synthetic-provider-secret-0123456789"
	var dispatches atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer "+providerCredential {
			http.Error(writer, "invalid fixture credential", http.StatusUnauthorized)
			return
		}
		var input struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		dispatches.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"fixture-response","status":"completed","output_text":"ok","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer provider.Close()

	certificatePool := x509.NewCertPool()
	certificatePool.AddCert(provider.Certificate())
	client, err := NewStaticClient(hardenllm.Options{
		Connections: []hardenllm.Connection{{
			ID: "cpa", Provider: "openai", Protocol: "responses", BaseURL: provider.URL + "/v1",
			CacheDomain: "cpa-deployment-credential", APIKey: providerCredential,
		}}, DefaultConnection: "cpa",
		EndpointPolicy: hardenllm.EndpointPolicy{
			PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
			TLSConfig: &tls.Config{RootCAs: certificatePool, MinVersion: tls.VersionTLS12},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"model-a", "model-b"} {
		result, callErr := client.Call(context.Background(), hardenllm.Request{
			ModelID: model, CallType: hardenllm.CallTypeText,
			Messages:       []hardenllm.Message{{Role: "user", Content: json.RawMessage(`"hello"`)}},
			RecoveryPolicy: hardenllm.DefaultRecoveryPolicy(),
		})
		if callErr != nil {
			t.Fatalf("call model %s through startup client: %v", model, callErr)
		}
		if result.Output != "ok" || result.GenerationTarget.ConnectionID != "cpa" || result.GenerationTarget.ModelID != model {
			t.Fatalf("static target or output changed for %s: %#v", model, result)
		}
		if result.Cache.Mode != hardenllm.CacheModeOff || result.Cache.Served || result.Cache.Written {
			t.Fatalf("cache-off call reported storage activity: %#v", result.Cache)
		}
	}
	if got := dispatches.Load(); got != 2 {
		t.Fatalf("provider dispatches = %d, want exactly one per call", got)
	}
}
