package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-028

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/prls-co/harden-llm/internal/artifacts"
	"github.com/prls-co/harden-llm/internal/postgres"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestGatewayOTelContract(t *testing.T) {
	spanExporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))
	defer func() { _ = tracerProvider.Shutdown(context.Background()) }()
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	defer func() { _ = meterProvider.Shutdown(context.Background()) }()
	telemetry, err := NewTelemetry(tracerProvider, meterProvider)
	if err != nil {
		t.Fatal(err)
	}

	ctx, endHTTP := telemetry.StartHTTP(context.Background(), http.MethodGet)
	for _, operation := range []string{
		OperationAuthAuthenticate, OperationAuthLogin, OperationProfileSave, OperationModelRefresh, OperationRun,
	} {
		_, endOperation := telemetry.StartOperation(ctx, operation)
		endOperation(nil)
	}
	_, endTracePersistence := telemetry.StartPersistence(ctx, "postgres", OperationTracePersistence)
	endTracePersistence(nil)
	_, endArtifactIndex := telemetry.StartPersistence(ctx, "postgres", "artifact.index")
	endArtifactIndex(context.DeadlineExceeded)

	queryTracer, err := postgres.NewQueryTelemetry(tracerProvider, meterProvider)
	if err != nil {
		t.Fatal(err)
	}
	queryContext := queryTracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "SELECT 'database-super-secret'"})
	queryTracer.TraceQueryEnd(queryContext, nil, pgx.TraceQueryEndData{})

	garage, err := artifacts.NewGarage(artifacts.Config{
		Endpoint: "https://garage.internal", Bucket: "harden-llm-artifacts", Region: "garage",
		AccessKeyID: "GK00000000000000000000000000000001", SecretAccessKey: strings.Repeat("a", 64),
		HTTPClient: garageTelemetryHTTPClient{}, TracerProvider: tracerProvider, MeterProvider: meterProvider,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := garage.Put(ctx, "owner/trace/trace.json", []byte(`{"safe":true}`), "application/json"); err != nil {
		t.Fatalf("instrumented Garage put: %v", err)
	}
	endHTTP("/api/v1/state", http.StatusOK)

	spans := spanExporter.GetSpans()
	for _, required := range []string{
		"hardenllm.http.request", "hardenllm.auth.authenticate", "hardenllm.auth.login",
		"hardenllm.profile.save", "hardenllm.profile.models.refresh", "hardenllm.run.execute",
		"hardenllm.trace.persist", "hardenllm.artifact.index", "hardenllm.postgres.query", "hardenllm.garage.put",
	} {
		if !gatewayHasSpan(spans, required) {
			t.Errorf("required gateway span %q was not emitted", required)
		}
	}
	encodedSpans := fmt.Sprint(spans)
	for _, forbidden := range []string{"database-super-secret", strings.Repeat("a", 64), "owner/trace/trace.json", "garage.internal"} {
		if strings.Contains(encodedSpans, forbidden) {
			t.Fatalf("gateway telemetry leaked %q", forbidden)
		}
	}

	var metrics metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	requiredMetrics := map[string]bool{
		"harden_llm.http.requests": false, "harden_llm.http.request.duration": false,
		"harden_llm.gateway.operations": false, "harden_llm.persistence.operations": false,
		"harden_llm.persistence.duration": false, "harden_llm.persistence.failures": false,
		"harden_llm.postgres.operations": false, "harden_llm.postgres.duration": false,
		"harden_llm.garage.operations": false, "harden_llm.garage.duration": false,
	}
	allowedLabels := map[string]bool{
		"route": true, "method": true, "outcome": true, "category": true,
		"operation": true, "store": true,
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, observed := range scope.Metrics {
			if _, ok := requiredMetrics[observed.Name]; ok {
				requiredMetrics[observed.Name] = true
			}
			for _, set := range gatewayMetricAttributeSets(observed.Data) {
				for _, value := range set.ToSlice() {
					if !allowedLabels[string(value.Key)] {
						t.Errorf("metric %s used unbounded label %q", observed.Name, value.Key)
					}
				}
			}
		}
	}
	for name, found := range requiredMetrics {
		if !found {
			t.Errorf("required gateway metric %q was not emitted", name)
		}
	}
}

type garageTelemetryHTTPClient struct{}

func (garageTelemetryHTTPClient) Do(request *http.Request) (*http.Response, error) {
	status := http.StatusOK
	body := []byte{}
	if request.Method == http.MethodHead {
		status = http.StatusNotFound
		body = []byte(`<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`)
	}
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Header: make(http.Header),
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request,
	}, nil
}

func gatewayHasSpan(spans tracetest.SpanStubs, name string) bool {
	for _, span := range spans {
		if span.Name == name {
			return true
		}
	}
	return false
}

func gatewayMetricAttributeSets(data metricdata.Aggregation) []attribute.Set {
	switch value := data.(type) {
	case metricdata.Sum[int64]:
		result := make([]attribute.Set, 0, len(value.DataPoints))
		for _, point := range value.DataPoints {
			result = append(result, point.Attributes)
		}
		return result
	case metricdata.Histogram[float64]:
		result := make([]attribute.Set, 0, len(value.DataPoints))
		for _, point := range value.DataPoints {
			result = append(result, point.Attributes)
		}
		return result
	default:
		return nil
	}
}
