package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-031 TEST-433

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/prls-co/harden-llm/internal/gateway"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestHTTPObservationUsesMatchedRoute(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		method, path, route string
		status              int
	}{
		{"GET", "/readyz", "/readyz", 200},
		{"POST", "/v1/responses", "/v1/responses", 401},
		{"GET", "/api/v1/history/private-record?secret=private", "/api/v1/history/{runID}", 401},
		{"GET", "/unknown-private-path", "unmatched", 404},
	} {
		t.Run(test.path, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			telemetry, err := gateway.NewTelemetry(provider, nil)
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			api := &API{telemetry: telemetry, propagator: propagation.TraceContext{}, logger: slog.New(slog.NewJSONHandler(&logs, nil))}
			response := httptest.NewRecorder()
			api.router().ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if response.Code != test.status {
				t.Fatalf("HTTP status = %d, want %d", response.Code, test.status)
			}
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry["route"] != test.route {
				t.Errorf("log route = %v, want %s", entry["route"], test.route)
			}
			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("HTTP spans = %d, want 1", len(spans))
			}
			route := ""
			for _, attribute := range spans[0].Attributes {
				if attribute.Key == "http.route" {
					route = attribute.Value.AsString()
				}
			}
			if route != test.route {
				t.Errorf("span route = %s, want %s", route, test.route)
			}
		})
	}
}
