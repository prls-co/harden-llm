package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const postgresInstrumentationName = "github.com/prls-co/harden-llm/internal/postgres"

type queryTelemetry struct {
	tracer              trace.Tracer
	operations          metric.Int64Counter
	duration            metric.Float64Histogram
	persistenceFailures metric.Int64Counter
}

type queryObservation struct {
	span      trace.Span
	startedAt time.Time
	operation string
}

type queryObservationKey struct{}

// NewQueryTelemetry creates the safe pgx query tracer used by Open. It is
// exported only inside the repository's internal tree for composition tests.
func NewQueryTelemetry(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) (*queryTelemetry, error) {
	if tracerProvider == nil {
		tracerProvider = tracenoop.NewTracerProvider()
	}
	if meterProvider == nil {
		meterProvider = metricnoop.NewMeterProvider()
	}
	meter := meterProvider.Meter(postgresInstrumentationName)
	operations, err := meter.Int64Counter("harden_llm.postgres.operations")
	if err != nil {
		return nil, err
	}
	duration, err := meter.Float64Histogram("harden_llm.postgres.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	failures, err := meter.Int64Counter("harden_llm.persistence.failures")
	if err != nil {
		return nil, err
	}
	return &queryTelemetry{
		tracer: tracerProvider.Tracer(postgresInstrumentationName), operations: operations,
		duration: duration, persistenceFailures: failures,
	}, nil
}

func (telemetry *queryTelemetry) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	operation := boundedSQLOperation(data.SQL)
	ctx, span := telemetry.tracer.Start(ctx, "hardenllm.postgres.query", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("db.system.name", "postgresql"), attribute.String("db.operation.name", operation),
	))
	return context.WithValue(ctx, queryObservationKey{}, queryObservation{span: span, startedAt: time.Now(), operation: operation})
}

func (telemetry *queryTelemetry) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	observation, ok := ctx.Value(queryObservationKey{}).(queryObservation)
	if !ok {
		return
	}
	outcome := "success"
	if data.Err != nil {
		outcome = "error"
		observation.span.SetAttributes(attribute.String("error.type", "database"))
		observation.span.SetStatus(codes.Error, "database")
		telemetry.persistenceFailures.Add(ctx, 1, metric.WithAttributes(
			attribute.String("store", "postgres"), attribute.String("operation", observation.operation),
		))
	} else {
		observation.span.SetStatus(codes.Ok, "")
	}
	observation.span.End()
	attributes := []attribute.KeyValue{
		attribute.String("operation", observation.operation), attribute.String("outcome", outcome),
	}
	telemetry.operations.Add(ctx, 1, metric.WithAttributes(attributes...))
	telemetry.duration.Record(ctx, time.Since(observation.startedAt).Seconds(), metric.WithAttributes(attributes...))
}

func boundedSQLOperation(query string) string {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return "other"
	}
	switch strings.ToUpper(fields[0]) {
	case "SELECT":
		return "select"
	case "INSERT":
		return "insert"
	case "UPDATE":
		return "update"
	case "DELETE":
		return "delete"
	case "CREATE":
		return "create"
	case "ALTER":
		return "alter"
	case "DROP":
		return "drop"
	default:
		return "other"
	}
}
