package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/prls-co/harden-llm/internal/redaction"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	TelemetryQueueSize = 2048
	TelemetryBatchSize = 512

	defaultTelemetryExportInterval = 15 * time.Second
	defaultTelemetryBatchInterval  = 2 * time.Second
	defaultTelemetryExportTimeout  = 5 * time.Second
)

type TelemetryRuntimeConfig struct {
	Endpoint    string
	ServiceName string
	Environment string
	Release     string
	Stdout      io.Writer
	Stderr      io.Writer
	Redactor    *redaction.Redactor

	TraceExporter  sdktrace.SpanExporter
	MetricExporter sdkmetric.Exporter
	LogExporter    sdklog.Exporter
	ExportInterval time.Duration
	BatchInterval  time.Duration
	ExportTimeout  time.Duration
}

// TelemetryRuntime owns process-local SDKs, bounded batch queues, the composed
// logger, and ordered shutdown. Library callers continue to receive only API
// providers and never initialize exporters or global providers.
type TelemetryRuntime struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
	loggerProvider *sdklog.LoggerProvider
	logger         *slog.Logger
	reporter       *telemetryFailureReporter

	shutdownOnce sync.Once
	shutdownErr  error
}

func NewTelemetryRuntime(ctx context.Context, config TelemetryRuntimeConfig) (*TelemetryRuntime, error) {
	if ctx == nil {
		return nil, errors.New("gateway: telemetry context is required")
	}
	config.ServiceName = strings.TrimSpace(config.ServiceName)
	config.Environment = strings.TrimSpace(config.Environment)
	config.Release = strings.TrimSpace(config.Release)
	if config.ServiceName == "" || len(config.ServiceName) > 128 || config.Environment == "" || len(config.Environment) > 64 || len(config.Release) > 128 {
		return nil, errors.New("gateway: telemetry resource identity is invalid")
	}
	if config.ExportInterval == 0 {
		config.ExportInterval = defaultTelemetryExportInterval
	}
	if config.BatchInterval == 0 {
		config.BatchInterval = defaultTelemetryBatchInterval
	}
	if config.ExportTimeout == 0 {
		config.ExportTimeout = defaultTelemetryExportTimeout
	}
	if config.ExportInterval < 10*time.Millisecond || config.ExportInterval > time.Minute ||
		config.BatchInterval < 10*time.Millisecond || config.BatchInterval > 10*time.Second ||
		config.ExportTimeout < 10*time.Millisecond || config.ExportTimeout > 30*time.Second {
		return nil, errors.New("gateway: telemetry timing is outside the supported range")
	}
	if config.Endpoint != "" {
		if err := validateOTLPEndpoint(config.Endpoint); err != nil {
			return nil, err
		}
	}
	reporter := newTelemetryFailureReporter(config.Stderr)
	otel.SetErrorHandler(reporter)

	createdExporters := make([]func(context.Context) error, 0, 3)
	cleanupExporters := func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		for index := len(createdExporters) - 1; index >= 0; index-- {
			_ = createdExporters[index](cleanupContext)
		}
	}
	if config.Endpoint != "" && config.TraceExporter == nil {
		exporter, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpointURL(config.Endpoint),
			otlptracegrpc.WithTimeout(config.ExportTimeout),
			otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{
				Enabled: true, InitialInterval: 500 * time.Millisecond, MaxInterval: 2 * time.Second, MaxElapsedTime: 4 * time.Second,
			}),
		)
		if err != nil {
			return nil, fmt.Errorf("gateway: initialize OTLP trace exporter: %w", err)
		}
		config.TraceExporter = exporter
		createdExporters = append(createdExporters, exporter.Shutdown)
	}
	if config.Endpoint != "" && config.MetricExporter == nil {
		exporter, err := otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpointURL(config.Endpoint),
			otlpmetricgrpc.WithTimeout(config.ExportTimeout),
			otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetryConfig{
				Enabled: true, InitialInterval: 500 * time.Millisecond, MaxInterval: 2 * time.Second, MaxElapsedTime: 4 * time.Second,
			}),
		)
		if err != nil {
			cleanupExporters()
			return nil, fmt.Errorf("gateway: initialize OTLP metric exporter: %w", err)
		}
		config.MetricExporter = exporter
		createdExporters = append(createdExporters, exporter.Shutdown)
	}
	if config.Endpoint != "" && config.LogExporter == nil {
		exporter, err := otlploggrpc.New(ctx,
			otlploggrpc.WithEndpointURL(config.Endpoint),
			otlploggrpc.WithTimeout(config.ExportTimeout),
			otlploggrpc.WithRetry(otlploggrpc.RetryConfig{
				Enabled: true, InitialInterval: 500 * time.Millisecond, MaxInterval: 2 * time.Second, MaxElapsedTime: 4 * time.Second,
			}),
		)
		if err != nil {
			cleanupExporters()
			return nil, fmt.Errorf("gateway: initialize OTLP log exporter: %w", err)
		}
		config.LogExporter = exporter
		createdExporters = append(createdExporters, exporter.Shutdown)
	}

	processResource := resource.NewSchemaless(
		attribute.String("service.name", config.ServiceName),
		attribute.String("service.version", config.Release),
		attribute.String("deployment.environment.name", config.Environment),
	)
	traceOptions := []sdktrace.TracerProviderOption{sdktrace.WithResource(processResource)}
	if config.TraceExporter != nil {
		exporter := &reportingSpanExporter{next: config.TraceExporter, reporter: reporter}
		traceOptions = append(traceOptions, sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(exporter,
			sdktrace.WithMaxQueueSize(TelemetryQueueSize), sdktrace.WithMaxExportBatchSize(TelemetryBatchSize),
			sdktrace.WithBatchTimeout(config.BatchInterval), sdktrace.WithExportTimeout(config.ExportTimeout),
		)))
	}
	tracerProvider := sdktrace.NewTracerProvider(traceOptions...)

	meterOptions := []sdkmetric.Option{sdkmetric.WithResource(processResource)}
	if config.MetricExporter != nil {
		exporter := &reportingMetricExporter{next: config.MetricExporter, reporter: reporter}
		reader := sdkmetric.NewPeriodicReader(exporter,
			sdkmetric.WithInterval(config.ExportInterval), sdkmetric.WithTimeout(config.ExportTimeout),
		)
		meterOptions = append(meterOptions, sdkmetric.WithReader(reader))
	}
	meterProvider := sdkmetric.NewMeterProvider(meterOptions...)

	logOptions := []sdklog.LoggerProviderOption{sdklog.WithResource(processResource)}
	if config.LogExporter != nil {
		exporter := &reportingLogExporter{next: config.LogExporter, reporter: reporter}
		logOptions = append(logOptions, sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter,
			sdklog.WithMaxQueueSize(TelemetryQueueSize), sdklog.WithExportMaxBatchSize(TelemetryBatchSize),
			sdklog.WithExportInterval(config.BatchInterval), sdklog.WithExportTimeout(config.ExportTimeout),
		)))
	}
	loggerProvider := sdklog.NewLoggerProvider(logOptions...)
	runtime := &TelemetryRuntime{
		tracerProvider: tracerProvider, meterProvider: meterProvider, loggerProvider: loggerProvider,
		reporter: reporter,
	}
	runtime.logger = NewStructuredLogger(config.Stdout, loggerProvider, config.Redactor)
	return runtime, nil
}

func (runtime *TelemetryRuntime) TracerProvider() trace.TracerProvider {
	if runtime == nil {
		return nil
	}
	return runtime.tracerProvider
}

func (runtime *TelemetryRuntime) MeterProvider() metric.MeterProvider {
	if runtime == nil {
		return nil
	}
	return runtime.meterProvider
}

func (runtime *TelemetryRuntime) LoggerProvider() otellog.LoggerProvider {
	if runtime == nil {
		return nil
	}
	return runtime.loggerProvider
}

func (runtime *TelemetryRuntime) Logger() *slog.Logger {
	if runtime == nil || runtime.logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return runtime.logger
}

func (runtime *TelemetryRuntime) Shutdown(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("gateway: telemetry shutdown context is required")
	}
	runtime.shutdownOnce.Do(func() {
		var shutdownErrors []error
		for _, provider := range []struct {
			signal string
			flush  func(context.Context) error
			stop   func(context.Context) error
		}{
			{signal: "logs", flush: runtime.loggerProvider.ForceFlush, stop: runtime.loggerProvider.Shutdown},
			{signal: "metrics", flush: runtime.meterProvider.ForceFlush, stop: runtime.meterProvider.Shutdown},
			{signal: "traces", flush: runtime.tracerProvider.ForceFlush, stop: runtime.tracerProvider.Shutdown},
		} {
			if err := provider.flush(ctx); err != nil {
				runtime.reporter.report(provider.signal, "flush")
				shutdownErrors = append(shutdownErrors, err)
			}
			if err := provider.stop(ctx); err != nil {
				runtime.reporter.report(provider.signal, "shutdown")
				shutdownErrors = append(shutdownErrors, err)
			}
		}
		runtime.shutdownErr = errors.Join(shutdownErrors...)
	})
	return runtime.shutdownErr
}

func validateOTLPEndpoint(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("gateway: OTLP endpoint must be an HTTP or HTTPS origin")
	}
	return nil
}

type telemetryFailureReporter struct{ logger *slog.Logger }

func newTelemetryFailureReporter(stderr io.Writer) *telemetryFailureReporter {
	if stderr == nil {
		stderr = io.Discard
	}
	return &telemetryFailureReporter{logger: slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: slog.LevelError}))}
}

func (reporter *telemetryFailureReporter) Handle(error) { reporter.report("sdk", "callback") }

func (reporter *telemetryFailureReporter) report(signal, operation string) {
	reporter.logger.ErrorContext(context.Background(), "telemetry delivery failed",
		"signal", signal, "operation", operation,
	)
}

type reportingSpanExporter struct {
	next     sdktrace.SpanExporter
	reporter *telemetryFailureReporter
}

func (exporter *reportingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := exporter.next.ExportSpans(ctx, spans)
	if err != nil {
		exporter.reporter.report("traces", "export")
	}
	return err
}

func (exporter *reportingSpanExporter) Shutdown(ctx context.Context) error {
	err := exporter.next.Shutdown(ctx)
	if err != nil {
		exporter.reporter.report("traces", "shutdown")
	}
	return err
}

type reportingLogExporter struct {
	next     sdklog.Exporter
	reporter *telemetryFailureReporter
}

func (exporter *reportingLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	err := exporter.next.Export(ctx, records)
	if err != nil {
		exporter.reporter.report("logs", "export")
	}
	return err
}

func (exporter *reportingLogExporter) ForceFlush(ctx context.Context) error {
	err := exporter.next.ForceFlush(ctx)
	if err != nil {
		exporter.reporter.report("logs", "flush")
	}
	return err
}

func (exporter *reportingLogExporter) Shutdown(ctx context.Context) error {
	err := exporter.next.Shutdown(ctx)
	if err != nil {
		exporter.reporter.report("logs", "shutdown")
	}
	return err
}

type reportingMetricExporter struct {
	next     sdkmetric.Exporter
	reporter *telemetryFailureReporter
}

func (exporter *reportingMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return exporter.next.Temporality(kind)
}

func (exporter *reportingMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return exporter.next.Aggregation(kind)
}

func (exporter *reportingMetricExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	err := exporter.next.Export(ctx, metrics)
	if err != nil {
		exporter.reporter.report("metrics", "export")
	}
	return err
}

func (exporter *reportingMetricExporter) ForceFlush(ctx context.Context) error {
	err := exporter.next.ForceFlush(ctx)
	if err != nil {
		exporter.reporter.report("metrics", "flush")
	}
	return err
}

func (exporter *reportingMetricExporter) Shutdown(ctx context.Context) error {
	err := exporter.next.Shutdown(ctx)
	if err != nil {
		exporter.reporter.report("metrics", "shutdown")
	}
	return err
}
