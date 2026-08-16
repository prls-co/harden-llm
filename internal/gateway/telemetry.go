package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/postgres"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const (
	gatewayInstrumentationName = "github.com/prls-co/harden-llm/internal/gateway"

	OperationAuthAuthenticate = "auth.authenticate"
	OperationAuthLogin        = "auth.login"
	OperationAuthLogout       = "auth.logout"
	OperationProfileSave      = "profile.save"
	OperationModelRefresh     = "profile.models.refresh"
	OperationRun              = "run.execute"
	OperationTracePersistence = "trace.persist"
)

// Telemetry is the gateway's fixed signal schema. Metric dimensions are
// restricted to route templates and finite operation/outcome categories.
type Telemetry struct {
	tracer trace.Tracer

	httpRequests        metric.Int64Counter
	httpDuration        metric.Float64Histogram
	operations          metric.Int64Counter
	persistence         metric.Int64Counter
	persistenceDuration metric.Float64Histogram
	persistenceFailures metric.Int64Counter
}

func NewTelemetry(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) (*Telemetry, error) {
	if tracerProvider == nil {
		tracerProvider = tracenoop.NewTracerProvider()
	}
	if meterProvider == nil {
		meterProvider = metricnoop.NewMeterProvider()
	}
	meter := meterProvider.Meter(gatewayInstrumentationName)
	telemetry := &Telemetry{tracer: tracerProvider.Tracer(gatewayInstrumentationName)}
	var err error
	if telemetry.httpRequests, err = meter.Int64Counter("harden_llm.http.requests"); err != nil {
		return nil, err
	}
	if telemetry.httpDuration, err = meter.Float64Histogram("harden_llm.http.request.duration", metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if telemetry.operations, err = meter.Int64Counter("harden_llm.gateway.operations"); err != nil {
		return nil, err
	}
	if telemetry.persistence, err = meter.Int64Counter("harden_llm.persistence.operations"); err != nil {
		return nil, err
	}
	if telemetry.persistenceDuration, err = meter.Float64Histogram("harden_llm.persistence.duration", metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if telemetry.persistenceFailures, err = meter.Int64Counter("harden_llm.persistence.failures"); err != nil {
		return nil, err
	}
	return telemetry, nil
}

func (telemetry *Telemetry) StartHTTP(ctx context.Context, method string) (context.Context, func(string, int)) {
	startedAt := time.Now()
	method = boundedHTTPMethod(method)
	ctx, span := telemetry.tracer.Start(ctx, "hardenllm.http.request", trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(
		attribute.String("http.request.method", method),
	))
	return ctx, func(route string, status int) {
		route = boundedRoute(route)
		outcome, category := httpOutcome(status)
		span.SetAttributes(
			attribute.String("http.route", route), attribute.Int("http.response.status_code", status),
			attribute.String("harden_llm.outcome", outcome), attribute.String("error.type", category),
		)
		if status >= http.StatusBadRequest {
			span.SetStatus(codes.Error, category)
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
		attributes := []attribute.KeyValue{
			attribute.String("route", route), attribute.String("method", method),
			attribute.String("outcome", outcome), attribute.String("category", category),
		}
		telemetry.httpRequests.Add(ctx, 1, metric.WithAttributes(attributes...))
		telemetry.httpDuration.Record(ctx, time.Since(startedAt).Seconds(), metric.WithAttributes(attributes...))
	}
}

func (telemetry *Telemetry) StartOperation(ctx context.Context, operation string) (context.Context, func(error)) {
	operation = boundedGatewayOperation(operation)
	ctx, span := telemetry.tracer.Start(ctx, "hardenllm."+operation)
	return ctx, func(err error) {
		outcome, category := gatewayOutcome(err)
		span.SetAttributes(attribute.String("harden_llm.outcome", outcome), attribute.String("error.type", category))
		if err != nil {
			span.SetStatus(codes.Error, category)
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
		telemetry.operations.Add(ctx, 1, metric.WithAttributes(
			attribute.String("operation", operation), attribute.String("outcome", outcome), attribute.String("category", category),
		))
	}
}

func (telemetry *Telemetry) StartPersistence(ctx context.Context, store, operation string) (context.Context, func(error)) {
	store = boundedPersistenceStore(store)
	operation = boundedPersistenceOperation(operation)
	startedAt := time.Now()
	ctx, span := telemetry.tracer.Start(ctx, "hardenllm."+operation, trace.WithAttributes(
		attribute.String("harden_llm.persistence.store", store),
	))
	return ctx, func(err error) {
		outcome, category := gatewayOutcome(err)
		if err != nil {
			span.SetAttributes(attribute.String("error.type", category))
			span.SetStatus(codes.Error, category)
			telemetry.persistenceFailures.Add(ctx, 1, metric.WithAttributes(
				attribute.String("store", store), attribute.String("operation", operation),
			))
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
		attributes := []attribute.KeyValue{
			attribute.String("store", store), attribute.String("operation", operation), attribute.String("outcome", outcome),
		}
		telemetry.persistence.Add(ctx, 1, metric.WithAttributes(attributes...))
		telemetry.persistenceDuration.Record(ctx, time.Since(startedAt).Seconds(), metric.WithAttributes(attributes...))
	}
}

func newNoopTelemetry() *Telemetry {
	telemetry, _ := NewTelemetry(nil, nil)
	return telemetry
}

func gatewayOutcome(err error) (string, string) {
	if err == nil {
		return "success", "success"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled", "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", "timeout"
	}
	if errors.Is(err, postgres.ErrNotFound) {
		return "error", "not_found"
	}
	if errors.Is(err, ErrInvalidRequest) || errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrProfileConflict) {
		return "error", "invalid_request"
	}
	return "error", "internal"
}

func httpOutcome(status int) (string, string) {
	switch {
	case status < 400:
		return "success", "success"
	case status == http.StatusUnauthorized:
		return "error", "unauthenticated"
	case status == http.StatusNotFound:
		return "error", "not_found"
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return "timeout", "timeout"
	case status >= 400 && status < 500:
		return "error", "invalid_request"
	case status >= 500:
		return "error", "internal"
	default:
		return "error", "other"
	}
}

// HTTPOutcome returns the finite log fields corresponding to an HTTP status.
func HTTPOutcome(status int) (string, string) { return httpOutcome(status) }

func boundedHTTPMethod(method string) string {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete:
		return strings.ToUpper(method)
	default:
		return "OTHER"
	}
}

func boundedRoute(route string) string {
	if route == "" {
		return "unmatched"
	}
	if len(route) > 128 || !strings.HasPrefix(route, "/") {
		return "unmatched"
	}
	return route
}

func boundedGatewayOperation(operation string) string {
	switch operation {
	case OperationAuthAuthenticate, OperationAuthLogin, OperationAuthLogout, OperationProfileSave, OperationModelRefresh, OperationRun:
		return operation
	default:
		return "gateway.other"
	}
}

func boundedPersistenceStore(store string) string {
	switch store {
	case "postgres", "garage":
		return store
	default:
		return "other"
	}
}

func boundedPersistenceOperation(operation string) string {
	switch operation {
	case OperationTracePersistence:
		return operation
	case "artifact.index":
		return operation
	default:
		return "persistence.other"
	}
}
