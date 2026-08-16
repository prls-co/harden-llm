package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-029

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/prls-co/harden-llm/internal/redaction"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

func TestStructuredLogging(t *testing.T) {
	exporter := &capturingLogExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	var stdout bytes.Buffer
	secrets := []string{
		"provider-secret-value", "adversarial system prompt", "adversarial response body",
		"https://user:password@example.test/v1?api_key=query-secret",
	}
	logger := NewStructuredLogger(&stdout, provider, redaction.New(secrets...))
	traceID := trace.TraceID{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	spanID := trace.SpanID{0, 1, 2, 3, 4, 5, 6, 7}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))
	logger.InfoContext(ctx, "run completed",
		"request_id", "request-1", "run_id", "run-1", "call_id", "call-1",
		"profile", "Primary", "model", "gpt-fixture", "provider", "openai",
		"outcome", "success", "category", "success",
		"authorization", "Bearer provider-secret-value",
		"prompt", "adversarial system prompt", "response", "adversarial response body",
		"url", "https://user:password@example.test/v1?api_key=query-secret",
		"error", errors.New("provider failed with provider-secret-value"),
	)

	lines := bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n"))
	if len(lines) != 1 || !json.Valid(lines[0]) {
		t.Fatalf("structured stdout records = %q", stdout.String())
	}
	var record map[string]any
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"trace_id": traceID.String(), "span_id": spanID.String(), "request_id": "request-1",
		"run_id": "run-1", "call_id": "call-1", "profile": "Primary", "model": "gpt-fixture",
		"provider": "openai", "outcome": "success", "category": "success",
	} {
		if record[key] != want {
			t.Errorf("stdout %s = %#v, want %#v", key, record[key], want)
		}
	}
	if record["authorization"] != redaction.Replacement || record["prompt"] != redaction.Replacement ||
		record["response"] != redaction.Replacement || record["url"] != redaction.Replacement ||
		record["error"] != "provider failed with "+redaction.Replacement {
		t.Fatalf("structured redaction = %#v", record)
	}

	records := exporter.Records()
	if len(records) != 1 {
		t.Fatalf("one slog call exported %d OTel records", len(records))
	}
	if records[0].TraceID() != traceID || records[0].SpanID() != spanID {
		t.Fatalf("OTel correlation = %s/%s", records[0].TraceID(), records[0].SpanID())
	}
	var exported strings.Builder
	exported.WriteString(records[0].Body().String())
	records[0].WalkAttributes(func(value otellog.KeyValue) bool {
		exported.WriteString(value.String())
		return true
	})
	for _, forbidden := range secrets {
		if strings.Contains(stdout.String(), forbidden) || strings.Contains(exported.String(), forbidden) {
			t.Fatalf("structured logs leaked %q", forbidden)
		}
	}
}

type capturingLogExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (exporter *capturingLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	for _, record := range records {
		exporter.records = append(exporter.records, record.Clone())
	}
	return nil
}

func (*capturingLogExporter) ForceFlush(context.Context) error { return nil }
func (*capturingLogExporter) Shutdown(context.Context) error   { return nil }

func (exporter *capturingLogExporter) Records() []sdklog.Record {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	result := make([]sdklog.Record, len(exporter.records))
	for index := range exporter.records {
		result[index] = exporter.records[index].Clone()
	}
	return result
}
