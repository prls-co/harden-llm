package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-031

import (
	"context"
	"net/http"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestGatewayHTTPMetricsUseBoundedRouteAndOutcome(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tracerProvider.Shutdown(context.Background()) }()
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = meterProvider.Shutdown(context.Background()) }()
	telemetry, err := NewTelemetry(tracerProvider, meterProvider)
	if err != nil {
		t.Fatal(err)
	}
	ctx, end := telemetry.StartHTTP(context.Background(), http.MethodPost)
	end("/v1/responses", http.StatusUnauthorized)
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("HTTP observation unexpectedly installed a deadline")
	}
	if spans := exporter.GetSpans(); len(spans) != 1 || spans[0].Name != "hardenllm.http.request" {
		t.Fatalf("HTTP request span was not recorded: %#v", spans)
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	if len(metrics.ScopeMetrics) != 1 || len(metrics.ScopeMetrics[0].Metrics) != 2 {
		t.Fatalf("unexpected HTTP metric schema: %#v", metrics.ScopeMetrics)
	}
}
