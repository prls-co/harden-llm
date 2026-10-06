package eval

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 EVAL-003

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"gopkg.in/yaml.v3"
)

type diagnosticEvalReport struct {
	RequiredSignalCoverage float64
	SecretLeakCount        int
	DuplicateExportCount   int
	CoveredSignals         int
	RequiredSignals        int
	Scenarios              map[string]bool
}

func TestDiagnosticCompletenessEval(t *testing.T) {
	const providerKey = "sk-eval-secret-123456"
	const prompt = "eval private prompt"
	const output = "eval private provider response"
	provider := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer "+providerKey {
			http.Error(writer, "missing test credential", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"fixture-response","status":"completed","output_text":"{\"answer\":\"ok\"}","output":[{"type":"message","content":[{"type":"output_text","text":"{\"answer\":\"ok\"}"}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`)
	}))
	defer provider.Close()

	spanExporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))
	defer func() { _ = tracerProvider.Shutdown(context.Background()) }()
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	defer func() { _ = meterProvider.Shutdown(context.Background()) }()
	logExporter := &diagnosticLogExporter{}
	loggerProvider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExporter)))
	defer func() { _ = loggerProvider.Shutdown(context.Background()) }()

	certificatePool := x509.NewCertPool()
	certificatePool.AddCert(provider.Certificate())
	client, err := hardenllm.New(hardenllm.Options{
		Connections: []hardenllm.Connection{{
			ID: "eval-upstream", Provider: "openai", Protocol: "responses", BaseURL: provider.URL + "/v1",
			CacheDomain: "eval-credential-domain", APIKey: providerKey,
		}}, DefaultConnection: "eval-upstream",
		EndpointPolicy: hardenllm.EndpointPolicy{
			PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
			TLSConfig: &tls.Config{RootCAs: certificatePool, MinVersion: tls.VersionTLS12},
		},
		TracerProvider: tracerProvider, MeterProvider: meterProvider,
	})
	if err != nil {
		t.Fatal(err)
	}
	telemetry, err := gateway.NewTelemetry(tracerProvider, meterProvider)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	logger := gateway.NewStructuredLogger(&stdout, loggerProvider, nil)
	httpContext, endHTTP := telemetry.StartHTTP(context.Background(), http.MethodPost)
	result, callErr := client.Call(httpContext, hardenllm.Request{
		ModelID: "eval-model", CallType: hardenllm.CallTypeStructured, Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`),
		Messages:       []hardenllm.Message{{Role: "user", Content: mustDiagnosticJSON(prompt)}},
		RecoveryPolicy: hardenllm.DefaultRecoveryPolicy(),
	})
	endHTTP("/v1/responses", http.StatusOK)
	if callErr != nil {
		t.Fatalf("local provider evaluation call: %v", callErr)
	}
	if result.Output.(map[string]any)["answer"] != "ok" || result.Accounting.Provider.Usage.Status != "complete" || result.Accounting.Provider.Usage.TotalTokens != 8 {
		t.Fatalf("provider result and accounting changed: %#v", result)
	}
	logger.InfoContext(httpContext, "inference completed",
		slog.String("call_id", result.CallID), slog.String("route", "/v1/responses"),
		slog.String("prompt", prompt), slog.String("provider_response", output),
		slog.String("authorization", "Bearer "+providerKey), slog.String("url", provider.URL+"?api_key="+providerKey),
	)

	var metrics metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	spans := spanExporter.GetSpans()
	requiredSpans := []string{"hardenllm.http.request", "hardenllm.call", "hardenllm.runtime.execute"}
	// The public root package intentionally does not export instrumentation
	// symbols, so the engine span names are fixed here as the telemetry contract.
	requiredSpans = append(requiredSpans, "hardenllm.provider.call", "hardenllm.provider.attempt", "hardenllm.schema.validate")
	covered := 0
	for _, name := range requiredSpans {
		if diagnosticHasSpan(spans, name) {
			covered++
		}
	}
	metricNames := diagnosticMetricNames(metrics)
	requiredMetrics := []string{
		"harden_llm.http.requests", "harden_llm.http.request.duration", "harden_llm.calls",
		"harden_llm.call.duration", "harden_llm.provider.attempts", "harden_llm.provider.duration", "harden_llm.tokens",
	}
	for _, name := range requiredMetrics {
		if metricNames[name] {
			covered++
		}
	}
	logs := logExporter.Records()
	if len(logs) == 1 && json.Valid(bytes.TrimSpace(stdout.Bytes())) {
		covered++
	}
	laminarPaths, configText := diagnosticLaminarPathCount(t)
	if laminarPaths == 1 {
		covered++
	}
	required := len(requiredSpans) + len(requiredMetrics) + 2

	var exported strings.Builder
	exported.WriteString(strings.TrimSpace(string(mustDiagnosticJSON(spans))))
	exported.WriteString(strings.TrimSpace(string(mustDiagnosticJSON(metrics))))
	exported.WriteString(stdout.String())
	exported.WriteString(configText)
	for _, record := range logs {
		exported.WriteString(record.Body().String())
		record.WalkAttributes(func(value attribute.KeyValue) bool {
			exported.WriteString(string(value.Key))
			exported.WriteString("=")
			exported.WriteString(strings.TrimSpace(strings.ReplaceAll(value.Value.String(), "\n", " ")))
			return true
		})
	}
	secretSources := map[string]string{
		"provider credential": providerKey, "user prompt": prompt, "provider response": output,
	}
	leaks := 0
	for name, secret := range secretSources {
		if strings.Contains(exported.String(), secret) {
			leaks++
			for source, candidate := range map[string]string{
				"spans": string(mustDiagnosticJSON(spans)), "metrics": string(mustDiagnosticJSON(metrics)),
				"stdout": stdout.String(), "collector config": configText,
			} {
				if strings.Contains(candidate, secret) {
					t.Logf("sensitive test value %s appeared in %s", name, source)
				}
			}
			for _, record := range logs {
				if strings.Contains(record.Body().String(), secret) {
					t.Logf("sensitive test value %s appeared in OpenTelemetry log body", name)
				}
				record.WalkAttributes(func(value attribute.KeyValue) bool {
					if strings.Contains(value.Value.String(), secret) {
						t.Logf("sensitive test value %s appeared in OpenTelemetry log attribute %s", name, value.Key)
					}
					return true
				})
			}
		}
	}
	report := diagnosticEvalReport{
		RequiredSignalCoverage: float64(covered) / float64(required),
		SecretLeakCount:        leaks, DuplicateExportCount: diagnosticDuplicateSpanIDs(spans),
		CoveredSignals: covered, RequiredSignals: required,
		Scenarios: map[string]bool{
			"successful_structured_call": callErr == nil && result.ResultSource.Kind == hardenllm.ResultSourceProvider,
			"complete_usage":             result.Accounting.Provider.Usage.Status == "complete" && result.Accounting.Provider.Usage.TotalTokens == 8,
			"redacted_diagnostics":       leaks == 0,
		},
	}
	if report.RequiredSignalCoverage != 1 || report.SecretLeakCount != 0 || report.DuplicateExportCount != 0 {
		t.Fatalf("EVAL-003 thresholds failed: %#v", report)
	}
	for scenario, observed := range report.Scenarios {
		if !observed {
			t.Errorf("diagnostic scenario %q was not exercised: %#v", scenario, report)
		}
	}
}

func mustDiagnosticJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

type diagnosticLogExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (exporter *diagnosticLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	for _, record := range records {
		exporter.records = append(exporter.records, record.Clone())
	}
	return nil
}

func (*diagnosticLogExporter) ForceFlush(context.Context) error { return nil }
func (*diagnosticLogExporter) Shutdown(context.Context) error   { return nil }

func (exporter *diagnosticLogExporter) Records() []sdklog.Record {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	result := make([]sdklog.Record, len(exporter.records))
	for index := range exporter.records {
		result[index] = exporter.records[index].Clone()
	}
	return result
}

func diagnosticHasSpan(spans tracetest.SpanStubs, name string) bool {
	for _, span := range spans {
		if span.Name == name {
			return true
		}
	}
	return false
}

func diagnosticMetricNames(metrics metricdata.ResourceMetrics) map[string]bool {
	result := make(map[string]bool)
	for _, scope := range metrics.ScopeMetrics {
		for _, observed := range scope.Metrics {
			result[observed.Name] = true
		}
	}
	return result
}

func diagnosticDuplicateSpanIDs(spans tracetest.SpanStubs) int {
	seen := make(map[string]bool, len(spans))
	duplicates := 0
	for _, span := range spans {
		key := span.SpanContext.TraceID().String() + "/" + span.SpanContext.SpanID().String()
		if seen[key] {
			duplicates++
		}
		seen[key] = true
	}
	return duplicates
}

func diagnosticLaminarPathCount(t *testing.T) (int, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "otel", "collector.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Service struct {
			Pipelines map[string]struct {
				Exporters []string `yaml:"exporters"`
			} `yaml:"pipelines"`
		} `yaml:"service"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, pipeline := range config.Service.Pipelines {
		for _, exporter := range pipeline.Exporters {
			if exporter == "otlp/harden_llm_laminar" {
				count++
			}
		}
	}
	return count, string(data)
}
