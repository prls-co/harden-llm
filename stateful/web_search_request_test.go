package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-535

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
)

func TestWebSearchRequestParityReferenceRun(t *testing.T) {
	captured := make(chan map[string]any, 1)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		captured <- body
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}`)
	}))
	defer upstream.Close()
	catalog := fixtureCatalog(t)
	preset := catalog["CPA Responses"]
	var profile Profile
	if err := json.Unmarshal(preset.Definition, &profile); err != nil {
		t.Fatal(err)
	}
	profile.BaseURL = upstream.URL + "/v1"
	preset.Definition, _ = json.Marshal(profile)
	preset.Binding = ProfileCredentialBinding{Source: "deployment", DeploymentUpstreamID: "fixture"}
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	svc, err := NewService(Config{Client: ClientOptions{Connections: []hardenllm.Connection{{ID: "fixture", Provider: "cpa", Protocol: "responses", BaseURL: profile.BaseURL, APIKey: "synthetic", SupportsWebSearch: true}}, DefaultConnection: "fixture", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}}, Presets: Catalog{"CPA Responses": preset}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.Run(context.Background(), strings.Repeat("a", 64), RunInput{ProfileID: "CPA Responses", UserPrompt: "hello", CallType: hardenllm.CallTypeText, WebSearch: true, RecoveryPolicy: json.RawMessage(`{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`)})
	if err != nil {
		t.Fatal(err)
	}
	got := <-captured
	choice, ok := got["tool_choice"].(map[string]any)
	if !ok || choice["type"] != "web_search" {
		t.Fatalf("search was not forced: %#v", got)
	}
}
