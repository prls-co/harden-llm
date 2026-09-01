package artifacts

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const garageInstrumentationName = "github.com/prls-co/harden-llm/internal/artifacts"

type storeTelemetry struct {
	tracer              trace.Tracer
	operations          metric.Int64Counter
	duration            metric.Float64Histogram
	persistenceFailures metric.Int64Counter
}

func newStoreTelemetry(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) (*storeTelemetry, error) {
	if tracerProvider == nil {
		tracerProvider = tracenoop.NewTracerProvider()
	}
	if meterProvider == nil {
		meterProvider = metricnoop.NewMeterProvider()
	}
	meter := meterProvider.Meter(garageInstrumentationName)
	operations, err := meter.Int64Counter("harden_llm.garage.operations")
	if err != nil {
		return nil, err
	}
	duration, err := meter.Float64Histogram("harden_llm.garage.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	failures, err := meter.Int64Counter("harden_llm.persistence.failures")
	if err != nil {
		return nil, err
	}
	return &storeTelemetry{
		tracer: tracerProvider.Tracer(garageInstrumentationName), operations: operations,
		duration: duration, persistenceFailures: failures,
	}, nil
}

func (telemetry *storeTelemetry) Start(ctx context.Context, operation string) (context.Context, func(error)) {
	operation = boundedGarageOperation(operation)
	startedAt := time.Now()
	ctx, span := telemetry.tracer.Start(ctx, "hardenllm.garage."+operation, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("db.system.name", "garage"), attribute.String("db.operation.name", operation),
	))
	return ctx, func(err error) {
		outcome := "success"
		category := garageErrorCategory(err)
		if err != nil {
			outcome = "error"
			span.SetAttributes(attribute.String("error.type", category))
			span.SetStatus(codes.Error, category)
			telemetry.persistenceFailures.Add(ctx, 1, metric.WithAttributes(
				attribute.String("store", "garage"), attribute.String("operation", operation),
			))
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
		attributes := []attribute.KeyValue{attribute.String("operation", operation), attribute.String("outcome", outcome)}
		telemetry.operations.Add(ctx, 1, metric.WithAttributes(attributes...))
		telemetry.duration.Record(ctx, time.Since(startedAt).Seconds(), metric.WithAttributes(attributes...))
	}
}

func boundedGarageOperation(value string) string {
	switch value {
	case "put", "get", "delete", "presign", "ready", "list":
		return value
	default:
		return "other"
	}
}

func garageErrorCategory(err error) string {
	if err == nil {
		return "success"
	}
	var artifactError *Error
	if !errors.As(err, &artifactError) {
		return "other"
	}
	switch artifactError.Kind {
	case KindInvalid, KindNotFound, KindConflict, KindUnauthorized, KindTimeout, KindUnavailable, KindIntegrity:
		return string(artifactError.Kind)
	default:
		return "other"
	}
}
