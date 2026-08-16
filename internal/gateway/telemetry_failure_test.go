package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-031

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const telemetryFailureRecordingBudget = 2 * time.Second

func TestTelemetryFailureIsolation(t *testing.T) {
	const exporterSecret = "collector-export-secret"
	traceState := newBlockingExportState(exporterSecret)
	metricState := newBlockingExportState(exporterSecret)
	logState := newBlockingExportState(exporterSecret)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	telemetryRuntime, err := NewTelemetryRuntime(context.Background(), TelemetryRuntimeConfig{
		ServiceName: "harden-llm-gateway", Environment: "test", Release: "test",
		Stdout: &stdout, Stderr: &stderr,
		TraceExporter:  &blockingTraceExporter{state: traceState},
		MetricExporter: &blockingMetricExporter{state: metricState},
		LogExporter:    &blockingLogExporter{state: logState},
		ExportInterval: 10 * time.Millisecond,
		BatchInterval:  10 * time.Millisecond,
		ExportTimeout:  75 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	applicationTelemetry, err := NewTelemetry(telemetryRuntime.TracerProvider(), telemetryRuntime.MeterProvider())
	if err != nil {
		t.Fatal(err)
	}
	ctx, endRun := applicationTelemetry.StartOperation(context.Background(), OperationRun)
	providerResult, callErr := failureIsolationProvider(ctx)
	endRun(callErr)
	if callErr != nil || providerResult != "stable-provider-result" {
		t.Fatalf("provider result changed by unavailable telemetry: %#v, %v", providerResult, callErr)
	}
	telemetryRuntime.Logger().InfoContext(ctx, "call completed", "call_id", "call-1", "outcome", "success")

	for name, state := range map[string]*blockingExportState{
		"traces": traceState, "metrics": metricState, "logs": logState,
	} {
		select {
		case <-state.started:
		case <-time.After(time.Second):
			t.Fatalf("%s exporter did not start", name)
		}
	}

	// Hold all exporters in a timed-out delivery while filling more than twice
	// the configured queue. Recording must remain non-blocking and each export
	// batch must stay within the explicit process bounds.
	floodStarted := time.Now()
	tracer := telemetryRuntime.TracerProvider().Tracer("failure-isolation")
	logger := telemetryRuntime.Logger()
	counter, err := telemetryRuntime.MeterProvider().Meter("failure-isolation").Int64Counter("failure_isolation.events")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < TelemetryQueueSize*2+1; index++ {
		_, span := tracer.Start(context.Background(), "queued-span")
		span.End()
		logger.Info("queued-log", "sequence_bucket", index%4)
		counter.Add(context.Background(), 1, metric.WithAttributes())
	}
	if elapsed := time.Since(floodStarted); elapsed > telemetryFailureRecordingBudgetForTest() {
		t.Fatalf("telemetry recording blocked application work for %v", elapsed)
	} else {
		t.Logf("telemetry failure-isolation flood completed in %v", elapsed)
	}

	shutdownStarted := time.Now()
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
	shutdownErr := telemetryRuntime.Shutdown(shutdownContext)
	cancelShutdown()
	if shutdownErr == nil {
		t.Fatal("shutdown succeeded despite unavailable exporters")
	}
	if elapsed := time.Since(shutdownStarted); elapsed > 2250*time.Millisecond {
		t.Fatalf("telemetry shutdown exceeded fixed budget: %v", elapsed)
	}
	for name, state := range map[string]*blockingExportState{
		"traces": traceState, "metrics": metricState, "logs": logState,
	} {
		if active := state.active.Load(); active != 0 {
			t.Errorf("%s exporter retained %d blocked calls after shutdown", name, active)
		}
		if batch := state.maxBatch.Load(); batch > TelemetryBatchSize {
			t.Errorf("%s batch size = %d, want <= %d", name, batch, TelemetryBatchSize)
		}
	}
	if !bytes.Contains(stderr.Bytes(), []byte("telemetry delivery failed")) {
		t.Fatalf("safe fallback was not reported: %s", stderr.String())
	}
	if bytes.Contains(stderr.Bytes(), []byte(exporterSecret)) || bytes.Contains(stdout.Bytes(), []byte(exporterSecret)) {
		t.Fatalf("telemetry failure leaked exporter details: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	secondStarted := time.Now()
	if secondErr := telemetryRuntime.Shutdown(context.Background()); !errors.Is(secondErr, shutdownErr) && secondErr.Error() != shutdownErr.Error() {
		t.Fatalf("idempotent shutdown error = %v, want %v", secondErr, shutdownErr)
	}
	if elapsed := time.Since(secondStarted); elapsed > 50*time.Millisecond {
		t.Fatalf("idempotent shutdown took %v", elapsed)
	}
}

func failureIsolationProvider(context.Context) (string, error) {
	return "stable-provider-result", nil
}

type blockingExportState struct {
	secret   string
	started  chan struct{}
	start    sync.Once
	active   atomic.Int64
	maxBatch atomic.Int64
}

func newBlockingExportState(secret string) *blockingExportState {
	return &blockingExportState{secret: secret, started: make(chan struct{})}
}

func (state *blockingExportState) block(ctx context.Context, batchSize int) error {
	state.start.Do(func() { close(state.started) })
	state.active.Add(1)
	defer state.active.Add(-1)
	for current := state.maxBatch.Load(); int64(batchSize) > current; current = state.maxBatch.Load() {
		if state.maxBatch.CompareAndSwap(current, int64(batchSize)) {
			break
		}
	}
	<-ctx.Done()
	return fmt.Errorf("%s: %w", state.secret, ctx.Err())
}

type blockingTraceExporter struct{ state *blockingExportState }

func (exporter *blockingTraceExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return exporter.state.block(ctx, len(spans))
}
func (*blockingTraceExporter) Shutdown(context.Context) error { return nil }

type blockingMetricExporter struct{ state *blockingExportState }

func (*blockingMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}
func (*blockingMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}
func (exporter *blockingMetricExporter) Export(ctx context.Context, _ *metricdata.ResourceMetrics) error {
	return exporter.state.block(ctx, 1)
}
func (*blockingMetricExporter) ForceFlush(context.Context) error { return nil }
func (*blockingMetricExporter) Shutdown(context.Context) error   { return nil }

type blockingLogExporter struct{ state *blockingExportState }

func (exporter *blockingLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	return exporter.state.block(ctx, len(records))
}
func (*blockingLogExporter) ForceFlush(context.Context) error { return nil }
func (*blockingLogExporter) Shutdown(context.Context) error   { return nil }
