package stateful_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-445

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestClientComposition(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-provider-secret" {
			t.Error("provider key changed")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-a","object":"model","owned_by":"fixture"}]}`))
			return
		}
		if r.URL.Path != "/v1/responses" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		_, _ = w.Write([]byte(`{"id":"response-fixture","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`))
	}))
	defer provider.Close()
	pool := x509.NewCertPool()
	pool.AddCert(provider.Certificate())
	exporter := tracetest.NewInMemoryExporter()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tracer.Shutdown(context.Background()) }()
	options := stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "fixture", Provider: "openai", Protocol: "responses", BaseURL: provider.URL + "/v1", APIKey: "fixture-provider-secret"}}, DefaultConnection: "fixture", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}, TracerProvider: tracer}
	client, err := stateful.NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	request := hardenllm.Request{ModelID: "model-a", CallType: hardenllm.CallTypeText, Messages: []hardenllm.Message{{Role: "user", Content: json.RawMessage(`"prompt-secret-sentinel"`)}}, CacheMode: hardenllm.CacheModeOff, RecoveryPolicy: hardenllm.DefaultRecoveryPolicy()}
	result, err := client.Call(context.Background(), request)
	if err != nil || result.Output != "ok" || calls.Load() != 1 {
		t.Fatalf("concrete composition: %v %+v calls=%d", err, result, calls.Load())
	}
	models, err := client.Models(context.Background(), "")
	if err != nil || len(models) != 1 || models[0].ID != "model-a" {
		t.Fatalf("model discovery: %v %v", models, err)
	}
	spans := exporter.GetSpans()
	names := map[string]bool{}
	for _, span := range spans {
		names[span.Name] = true
		encoded, _ := json.Marshal(span.Attributes)
		for _, secret := range []string{"fixture-provider-secret", "prompt-secret-sentinel"} {
			if strings.Contains(string(encoded), secret) {
				t.Errorf("observer leaked %s", secret)
			}
		}
	}
	for _, name := range []string{"hardenllm.call", "hardenllm.runtime.execute", "hardenllm.provider.call", "hardenllm.provider.attempt"} {
		if !names[name] {
			t.Errorf("missing preserved span %s", name)
		}
	}
	options.TracerProvider = nil
	withoutObserver, err := stateful.NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	other, err := withoutObserver.Call(context.Background(), request)
	if err != nil || other.Output != result.Output {
		t.Fatalf("nil telemetry changed inference: %v %+v", err, other)
	}
}
