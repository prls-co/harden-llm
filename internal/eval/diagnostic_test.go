package eval

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 EVAL-003

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/artifacts"
	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/redaction"
	"github.com/prls-co/harden-llm/internal/retry"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
	otellog "go.opentelemetry.io/otel/log"
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
	spanExporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))
	defer func() { _ = tracerProvider.Shutdown(context.Background()) }()
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	defer func() { _ = meterProvider.Shutdown(context.Background()) }()
	logExporter := &diagnosticLogExporter{}
	loggerProvider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExporter)))
	defer func() { _ = loggerProvider.Shutdown(context.Background()) }()

	runtimeTelemetry, err := coreruntime.NewTelemetry(tracerProvider, meterProvider)
	if err != nil {
		t.Fatal(err)
	}
	profile := coreruntime.Profile{
		ID: "eval-profile", Provider: "openai", ModelID: "eval-model", BaseURL: "https://provider.example.test/v1",
	}
	secrets := []string{
		"eval system prompt", "eval user prompt", "private provider response", "sk-eval-secret-123456",
		"garage-secret-access-key", "eval-owner/trace/private.json", "https://private.example.test/v1?api_key=hidden",
	}
	call := coreruntime.Call{
		SystemPrompt: secrets[0], UserPrompt: secrets[1], CallType: "structured",
		Schema: json.RawMessage(`{"type":"object"}`), StructuredRepair: coreruntime.StructuredRepair{Enabled: true},
		Telemetry: runtimeTelemetry,
		ValidateStructured: func(value any) error {
			object, ok := value.(map[string]any)
			if !ok || object["answer"] != "ok" {
				return errors.New("schema rejected private provider response")
			}
			return nil
		},
	}
	cache := &diagnosticCache{}
	ctx, endCall := runtimeTelemetry.StartCall(context.Background(), coreruntime.CallObservation{
		ProfileID: profile.ID, Provider: profile.Provider, ModelID: profile.ModelID, CallType: call.CallType,
	})
	repaired, err := coreruntime.Execute(
		ctx, diagnosticRepairExecutor{}, diagnosticCredentials(secrets[3]), profile.ID,
		map[string]coreruntime.Profile{profile.ID: profile}, call,
		retry.Config{
			MaxAttempts: 2, BaseDelay: time.Nanosecond, MaxDelay: time.Nanosecond,
			Policy: retry.Policy{ParseError: true}, Random: func() float64 { return 0.5 },
			Wait: func(context.Context, time.Duration) error { return nil },
		},
		cache, cachekey.ModeRefresh, cachekey.DefaultVersion, "call-repaired", "trace-repaired",
	)
	endCall(repaired, err)
	if err != nil {
		t.Fatalf("repaired evaluation call: %v", err)
	}

	ctx, endCall = runtimeTelemetry.StartCall(context.Background(), coreruntime.CallObservation{
		ProfileID: profile.ID, Provider: profile.Provider, ModelID: profile.ModelID, CallType: call.CallType,
	})
	cached, cacheErr := coreruntime.Execute(
		ctx, diagnosticRepairExecutor{}, diagnosticCredentials(secrets[3]), profile.ID,
		map[string]coreruntime.Profile{profile.ID: profile}, call,
		retry.Config{MaxAttempts: 1}, cache, cachekey.ModeCache, cachekey.DefaultVersion,
		"call-cached", "trace-cached",
	)
	endCall(cached, cacheErr)
	if cacheErr != nil {
		t.Fatalf("cached evaluation call: %v", cacheErr)
	}

	failedCall := call
	failedCall.CallType = "text"
	failedCall.Schema = nil
	failedCall.StructuredRepair = coreruntime.StructuredRepair{}
	failedCall.ValidateStructured = nil
	ctx, endCall = runtimeTelemetry.StartCall(context.Background(), coreruntime.CallObservation{
		ProfileID: profile.ID, Provider: profile.Provider, ModelID: profile.ModelID, CallType: failedCall.CallType,
	})
	failed, failedErr := coreruntime.Execute(
		ctx, diagnosticFailureExecutor{secret: secrets[3]}, diagnosticCredentials(secrets[3]), profile.ID,
		map[string]coreruntime.Profile{profile.ID: profile}, failedCall, retry.Config{MaxAttempts: 1},
		nil, cachekey.ModeOff, cachekey.DefaultVersion, "call-failed", "trace-failed",
	)
	endCall(failed, failedErr)
	if failedErr == nil {
		t.Fatal("failed evaluation call unexpectedly succeeded")
	}
	_, endArtifact := runtimeTelemetry.StartArtifact(context.Background(), "trace")
	endArtifact(errors.New("artifact failed with " + secrets[4]))

	gatewayTelemetry, err := gateway.NewTelemetry(tracerProvider, meterProvider)
	if err != nil {
		t.Fatal(err)
	}
	httpContext, endHTTP := gatewayTelemetry.StartHTTP(context.Background(), http.MethodPost)
	for _, operation := range []string{
		gateway.OperationAuthAuthenticate, gateway.OperationAuthLogin, gateway.OperationProfileSave,
		gateway.OperationModelRefresh, gateway.OperationRun,
	} {
		_, endOperation := gatewayTelemetry.StartOperation(httpContext, operation)
		endOperation(nil)
	}
	_, endTracePersistence := gatewayTelemetry.StartPersistence(httpContext, "postgres", gateway.OperationTracePersistence)
	endTracePersistence(nil)
	_, endArtifactIndex := gatewayTelemetry.StartPersistence(httpContext, "postgres", "artifact.index")
	endArtifactIndex(context.DeadlineExceeded)

	queryTelemetry, err := postgres.NewQueryTelemetry(tracerProvider, meterProvider)
	if err != nil {
		t.Fatal(err)
	}
	queryContext := queryTelemetry.TraceQueryStart(httpContext, nil, pgx.TraceQueryStartData{SQL: "SELECT 'sk-eval-secret-123456'"})
	queryTelemetry.TraceQueryEnd(queryContext, nil, pgx.TraceQueryEndData{})

	garage, err := artifacts.NewGarage(artifacts.Config{
		Endpoint: "https://garage.internal", Bucket: "harden-llm-artifacts", Region: "garage",
		AccessKeyID: "GK00000000000000000000000000000001", SecretAccessKey: secrets[4],
		HTTPClient: diagnosticGarageClient{}, TracerProvider: tracerProvider, MeterProvider: meterProvider,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := garage.Put(httpContext, secrets[5], []byte(`{"safe":true}`), "application/json"); err != nil {
		t.Fatalf("Garage evaluation put: %v", err)
	}

	var stdout bytes.Buffer
	logger := gateway.NewStructuredLogger(&stdout, loggerProvider, redaction.New())
	logger.InfoContext(httpContext, "run completed",
		"call_id", "call-eval", "profile", profile.ID, "model", profile.ModelID, "provider", profile.Provider,
		"outcome", "success", "category", "success", "prompt", secrets[0], "response", secrets[2],
		"authorization", "Bearer "+secrets[3], "url", secrets[6], "error", errors.New("provider: "+secrets[3]),
	)
	endHTTP("/api/v1/run", http.StatusOK)

	var metrics metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	spans := spanExporter.GetSpans()
	requiredSpans := []string{
		coreruntime.SpanCall, coreruntime.SpanRuntime, coreruntime.SpanProvider, coreruntime.SpanAttempt,
		coreruntime.SpanRetryWait, coreruntime.SpanSchema, coreruntime.SpanCacheLookup,
		coreruntime.SpanCacheWrite, coreruntime.SpanArtifact,
		"hardenllm.http.request", "hardenllm.auth.authenticate", "hardenllm.auth.login",
		"hardenllm.profile.save", "hardenllm.profile.models.refresh", "hardenllm.run.execute",
		"hardenllm.trace.persist", "hardenllm.artifact.index", "hardenllm.postgres.query", "hardenllm.garage.put",
	}
	requiredMetrics := []string{
		"harden_llm.calls", "harden_llm.call.duration", "harden_llm.provider.attempts",
		"harden_llm.provider.duration", "harden_llm.retries", "harden_llm.cache.operations",
		"harden_llm.schema.operations", "harden_llm.tokens", "harden_llm.cost.usd",
		"harden_llm.artifact.operations", "harden_llm.persistence.failures",
		"harden_llm.http.requests", "harden_llm.http.request.duration", "harden_llm.gateway.operations",
		"harden_llm.persistence.operations", "harden_llm.persistence.duration",
		"harden_llm.postgres.operations", "harden_llm.postgres.duration",
		"harden_llm.garage.operations", "harden_llm.garage.duration",
	}
	covered := 0
	for _, name := range requiredSpans {
		if diagnosticHasSpan(spans, name) {
			covered++
		}
	}
	metricNames := diagnosticMetricNames(metrics)
	for _, name := range requiredMetrics {
		if metricNames[name] {
			covered++
		}
	}
	logs := logExporter.Records()
	if len(logs) == 1 && bytes.Count(bytes.TrimSpace(stdout.Bytes()), []byte("\n")) == 0 && json.Valid(bytes.TrimSpace(stdout.Bytes())) {
		covered++
	}
	langfusePaths, configText := diagnosticLangfusePathCount(t)
	if langfusePaths == 1 {
		covered++
	}
	required := len(requiredSpans) + len(requiredMetrics) + 2

	var exported strings.Builder
	exported.WriteString(fmt.Sprint(spans))
	exported.WriteString(fmt.Sprint(metrics))
	exported.Write(stdout.Bytes())
	exported.WriteString(configText)
	for _, record := range logs {
		exported.WriteString(record.Body().String())
		record.WalkAttributes(func(value otellog.KeyValue) bool {
			exported.WriteString(value.String())
			return true
		})
	}
	leaks := 0
	for _, secret := range secrets {
		if strings.Contains(exported.String(), secret) {
			leaks++
		}
	}
	duplicateExports := diagnosticDuplicateSpanIDs(spans)
	if langfusePaths > 1 {
		duplicateExports += langfusePaths - 1
	}
	report := diagnosticEvalReport{
		RequiredSignalCoverage: float64(covered) / float64(required),
		SecretLeakCount:        leaks, DuplicateExportCount: duplicateExports,
		CoveredSignals: covered, RequiredSignals: required,
		Scenarios: map[string]bool{
			"successful": err == nil, "retried": len(repaired.Attempts) == 2,
			"repaired": len(repaired.Attempts) == 2 && repaired.Attempts[1].Repair,
			"cached":   cached.Cache.Served, "failed": failedErr != nil,
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

func diagnosticCredentials(secret string) coreruntime.CredentialLookup {
	return func(context.Context, coreruntime.Profile) (coreruntime.Credential, error) {
		return coreruntime.Credential{APIKey: secret}, nil
	}
}

type diagnosticRepairExecutor struct{}

func (diagnosticRepairExecutor) Prepare(_ context.Context, _ coreruntime.Profile, _ coreruntime.Credential, call coreruntime.Call) (coreruntime.PreparedOperation, error) {
	return diagnosticOperation(call.Repair != nil), nil
}

func (diagnosticRepairExecutor) Execute(_ context.Context, operation coreruntime.PreparedOperation) (coreruntime.ProviderResult, error) {
	if repair, _ := operation.Opaque.(bool); repair {
		return coreruntime.ProviderResult{
			Output: map[string]any{
				"repair": map[string]any{"explanation": "normalized", "changes": []any{"answer"}},
				"data":   map[string]any{"answer": "ok"},
			},
			Accounting: diagnosticLedger(7, 3, 0.02),
		}, nil
	}
	return coreruntime.ProviderResult{
		Output:     map[string]any{"answer": "private provider response"},
		Accounting: diagnosticLedger(5, 2, 0.01),
	}, nil
}

type diagnosticFailureExecutor struct{ secret string }

func (diagnosticFailureExecutor) Prepare(_ context.Context, _ coreruntime.Profile, _ coreruntime.Credential, _ coreruntime.Call) (coreruntime.PreparedOperation, error) {
	return diagnosticOperation(false), nil
}

func (executor diagnosticFailureExecutor) Execute(context.Context, coreruntime.PreparedOperation) (coreruntime.ProviderResult, error) {
	return coreruntime.ProviderResult{}, &retry.ProviderError{Status: http.StatusUnauthorized, Err: errors.New("provider rejected " + executor.secret)}
}

func diagnosticOperation(repair bool) coreruntime.PreparedOperation {
	return coreruntime.PreparedOperation{Operation: cachekey.Operation{
		SchemaVersion: cachekey.OperationSchemaVersion, Protocol: "fixture",
		Endpoint: cachekey.Endpoint{Identity: "https://provider.example.test:443", Method: http.MethodPost, Path: "/run"},
		Model:    "eval-model", Payload: map[string]any{"repair": repair}, SemanticHeaders: map[string]any{},
		ResponseProjection: cachekey.ResponseProjection{Provider: "openai", Kind: "fixture", Version: "v1"},
	}, Opaque: repair}
}

type diagnosticCache struct {
	record coreruntime.CachedResult
	found  bool
}

func (cache *diagnosticCache) Get(context.Context, string, string) (coreruntime.CachedResult, bool, error) {
	return cache.record, cache.found, nil
}

func (cache *diagnosticCache) Set(_ context.Context, _, _ string, _ cachekey.Operation, result coreruntime.CachedResult) error {
	cache.record = result
	cache.found = true
	return nil
}

func diagnosticLedger(input, output int64, cost float64) coreruntime.Ledger {
	usage, err := accounting.CompleteUsage(input, 0, 0, output, 0)
	if err != nil {
		panic(err)
	}
	return coreruntime.Ledger{Usage: usage, Cost: accounting.ExactCost(cost, "reported")}
}

type diagnosticGarageClient struct{}

func (diagnosticGarageClient) Do(request *http.Request) (*http.Response, error) {
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

func diagnosticLangfusePathCount(t *testing.T) (int, string) {
	t.Helper()
	path := filepath.Join("..", "..", "deploy", "otel", "collector.yaml")
	data, err := os.ReadFile(path)
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
			if exporter == "otlphttp/langfuse" {
				count++
			}
		}
	}
	return count, string(data)
}
