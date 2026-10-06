package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const gatewayInstrumentationName = "github.com/prls-co/harden-llm/internal/gateway"

type Telemetry struct {
	tracer       trace.Tracer
	httpRequests metric.Int64Counter
	httpDuration metric.Float64Histogram
}

func NewTelemetry(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) (*Telemetry, error) {
	if tracerProvider == nil {
		tracerProvider = tracenoop.NewTracerProvider()
	}
	if meterProvider == nil {
		meterProvider = metricnoop.NewMeterProvider()
	}
	meter := meterProvider.Meter(gatewayInstrumentationName)
	telem := &Telemetry{tracer: tracerProvider.Tracer(gatewayInstrumentationName)}
	var err error
	if telem.httpRequests, err = meter.Int64Counter("harden_llm.http.requests"); err != nil {
		return nil, err
	}
	if telem.httpDuration, err = meter.Float64Histogram("harden_llm.http.request.duration", metric.WithUnit("s")); err != nil {
		return nil, err
	}
	return telem, nil
}

func (telemetry *Telemetry) StartHTTP(ctx context.Context, method string) (context.Context, func(string, int)) {
	startedAt := time.Now()
	method = boundedHTTPMethod(method)
	ctx, span := telemetry.tracer.Start(ctx, "hardenllm.http.request", trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attribute.String("http.request.method", method)))
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

func HTTPOutcome(status int) (string, string) { return httpOutcome(status) }

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

func boundedHTTPMethod(method string) string {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete:
		return strings.ToUpper(method)
	default:
		return "OTHER"
	}
}

func boundedRoute(route string) string {
	if route == "" || len(route) > 128 || !strings.HasPrefix(route, "/") {
		return "unmatched"
	}
	return route
}
