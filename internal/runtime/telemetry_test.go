package runtime

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-028

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestOTelContract(t *testing.T) {
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

	profile := Profile{
		ID: "adversarial-profile", Provider: "openai", APIInferenceType: "responses",
		BaseURL: "https://api.openai.com/v1", ModelID: "gpt-fixture",
	}
	call := Call{
		SystemPrompt: "adversarial system prompt", UserPrompt: "adversarial user prompt",
		CallType: "structured", Schema: json.RawMessage(`{"type":"object"}`),
		StructuredRepair: StructuredRepair{Enabled: true}, Telemetry: telemetry,
		ValidateStructured: func(value any) error {
			object, ok := value.(map[string]any)
			if !ok || object["answer"] != "ok" {
				return errors.New("adversarial response failed schema")
			}
			return nil
		},
	}
	cache := &telemetryCache{}
	ctx, endCall := telemetry.StartCall(context.Background(), CallObservation{
		ProfileID: profile.ID, Provider: profile.Provider, ModelID: profile.ModelID, CallType: call.CallType,
	})
	record, err := Execute(ctx, &telemetryExecutor{}, func(context.Context, Profile) (Credential, error) {
		return Credential{APIKey: "super-secret-api-key"}, nil
	}, profile.ID, map[string]Profile{profile.ID: profile}, call, retry.Config{
		MaxAttempts: 2, BaseDelay: 1, MaxDelay: 1, Policy: retry.Policy{ParseError: true},
		Random: func() float64 { return 0 }, Wait: func(context.Context, time.Duration) error { return nil },
	}, cache, cachekey.ModeRefresh, "v1", "call-fixture", "trace-fixture")
	endCall(record, err)
	if err != nil || len(record.Attempts) != 2 || !record.Cache.Written {
		t.Fatalf("instrumented repaired call = %#v, %v", record, err)
	}

	cache.found = true
	ctx, endCall = telemetry.StartCall(context.Background(), CallObservation{
		ProfileID: profile.ID, Provider: profile.Provider, ModelID: profile.ModelID, CallType: call.CallType,
	})
	cached, cacheErr := Execute(ctx, &telemetryExecutor{}, func(context.Context, Profile) (Credential, error) {
		return Credential{APIKey: "super-secret-api-key"}, nil
	}, profile.ID, map[string]Profile{profile.ID: profile}, call, retry.Config{MaxAttempts: 1}, cache,
		cachekey.ModeCache, "v1", "call-cache", "trace-cache")
	endCall(cached, cacheErr)
	if cacheErr != nil || !cached.Cache.Served {
		t.Fatalf("instrumented cache hit = %#v, %v", cached, cacheErr)
	}

	_, endArtifact := telemetry.StartArtifact(context.Background(), "trace")
	endArtifact(errors.New("artifact backend unavailable with super-secret-api-key"))

	spans := spanExporter.GetSpans()
	requiredSpans := []string{
		SpanCall, SpanRuntime, SpanProvider, SpanAttempt, SpanRetryWait,
		SpanSchema, SpanCacheLookup, SpanCacheWrite, SpanArtifact,
	}
	for _, required := range requiredSpans {
		if !hasSpan(spans, required) {
			t.Errorf("required span %q was not emitted", required)
		}
	}
	assertSpanParent(t, spans, SpanAttempt, SpanProvider)
	assertSpanParent(t, spans, SpanSchema, SpanAttempt)
	encodedSpans := fmt.Sprint(spans)
	for _, forbidden := range []string{"super-secret-api-key", "adversarial system prompt", "adversarial user prompt", "adversarial response"} {
		if strings.Contains(encodedSpans, forbidden) {
			t.Fatalf("span attributes leaked %q", forbidden)
		}
	}

	var metrics metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	requiredMetrics := map[string]bool{
		"harden_llm.calls": false, "harden_llm.call.duration": false,
		"harden_llm.provider.attempts": false, "harden_llm.provider.duration": false,
		"harden_llm.retries": false, "harden_llm.cache.operations": false,
		"harden_llm.schema.operations": false, "harden_llm.tokens": false, "harden_llm.cost.usd": false,
		"harden_llm.artifact.operations": false, "harden_llm.persistence.failures": false,
	}
	allowedLabels := map[string]bool{
		"provider": true, "call_type": true, "outcome": true, "category": true,
		"cache_outcome": true, "operation": true, "repair": true, "token_type": true,
		"source": true, "coverage": true, "store": true, "kind": true, "scope": true,
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, observed := range scope.Metrics {
			if _, ok := requiredMetrics[observed.Name]; ok {
				requiredMetrics[observed.Name] = true
			}
			for _, set := range metricAttributeSets(observed.Data) {
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
			t.Errorf("required metric %q was not emitted", name)
		}
	}
}

type telemetryExecutor struct{}

func (*telemetryExecutor) Prepare(_ context.Context, _ Profile, _ Credential, call Call) (PreparedOperation, error) {
	return PreparedOperation{Operation: cachekey.Operation{
		SchemaVersion: cachekey.OperationSchemaVersion, Protocol: "fixture",
		Endpoint: cachekey.Endpoint{Identity: "https://example.test:443", Method: "POST", Path: "/run"},
		Model:    "gpt-fixture", Payload: map[string]any{"repair": call.Repair != nil},
		SemanticHeaders:    map[string]any{},
		ResponseProjection: cachekey.ResponseProjection{Provider: "openai", Kind: "fixture", Version: "v1"},
	}, Opaque: call.Repair != nil}, nil
}

func (*telemetryExecutor) Execute(_ context.Context, operation PreparedOperation) (ProviderResult, error) {
	if repair, _ := operation.Opaque.(bool); repair {
		return ProviderResult{
			Output: map[string]any{
				"repair": map[string]any{"explanation": "fixed", "changes": []any{"answer"}},
				"data":   map[string]any{"answer": "ok"},
			},
			Accounting: Ledger{
				Usage: completeUsageWithoutTest(5, 0, 0, 2, 0), Cost: accounting.ExactCost(0.01, "reported"),
			},
		}, nil
	}
	return ProviderResult{
		Output: map[string]any{"answer": float64(42)},
		Accounting: Ledger{
			Usage: completeUsageWithoutTest(4, 0, 0, 1, 0), Cost: accounting.ExactCost(0.01, "reported"),
		},
	}, nil
}

type telemetryCache struct {
	record CachedResult
	found  bool
}

func (cache *telemetryCache) Get(context.Context, string, string) (CachedResult, bool, error) {
	return cache.record, cache.found, nil
}

func (cache *telemetryCache) Set(_ context.Context, _, _ string, _ cachekey.Operation, result CachedResult) error {
	cache.record = result
	return nil
}

func hasSpan(spans tracetest.SpanStubs, name string) bool {
	for _, span := range spans {
		if span.Name == name {
			return true
		}
	}
	return false
}

func assertSpanParent(t *testing.T, spans tracetest.SpanStubs, childName, parentName string) {
	t.Helper()
	for _, child := range spans {
		if child.Name != childName {
			continue
		}
		for _, parent := range spans {
			if parent.Name == parentName && child.Parent.SpanID() == parent.SpanContext.SpanID() {
				return
			}
		}
	}
	t.Errorf("span %q was not a child of %q", childName, parentName)
}

func metricAttributeSets(data metricdata.Aggregation) []attribute.Set {
	switch value := data.(type) {
	case metricdata.Sum[int64]:
		result := make([]attribute.Set, 0, len(value.DataPoints))
		for _, point := range value.DataPoints {
			result = append(result, point.Attributes)
		}
		return result
	case metricdata.Sum[float64]:
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
