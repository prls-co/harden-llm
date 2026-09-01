package runtime

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/prls-co/harden-llm/internal/retry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const (
	InstrumentationName = "github.com/prls-co/harden-llm/internal/runtime"
	SpanCall            = "hardenllm.call"
	SpanRuntime         = "hardenllm.runtime.execute"
	SpanProvider        = "hardenllm.provider.call"
	SpanAttempt         = "hardenllm.provider.attempt"
	SpanRetryWait       = "hardenllm.retry.wait"
	SpanSchema          = "hardenllm.schema.validate"
	SpanCacheLookup     = "hardenllm.cache.lookup"
	SpanCacheWrite      = "hardenllm.cache.write"
	SpanArtifact        = "hardenllm.artifact.persist"
)

// Telemetry owns the fixed, bounded runtime signal schema. It never records
// prompts, responses, credentials, endpoint URLs, raw errors, or metric labels
// derived from profile/model/user identifiers.
type Telemetry struct {
	tracer trace.Tracer

	calls               metric.Int64Counter
	callDuration        metric.Float64Histogram
	providerAttempts    metric.Int64Counter
	providerDuration    metric.Float64Histogram
	retries             metric.Int64Counter
	cacheOperations     metric.Int64Counter
	schemaOperations    metric.Int64Counter
	tokens              metric.Int64Counter
	costUSD             metric.Float64Counter
	artifactOperations  metric.Int64Counter
	persistenceFailures metric.Int64Counter
}

type CallObservation struct {
	ProfileID string
	Provider  string
	ModelID   string
	CallType  string
}

func NewTelemetry(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) (*Telemetry, error) {
	if tracerProvider == nil {
		tracerProvider = tracenoop.NewTracerProvider()
	}
	if meterProvider == nil {
		meterProvider = metricnoop.NewMeterProvider()
	}
	meter := meterProvider.Meter(InstrumentationName)
	telemetry := &Telemetry{tracer: tracerProvider.Tracer(InstrumentationName)}
	var err error
	if telemetry.calls, err = meter.Int64Counter("harden_llm.calls", metric.WithDescription("Completed Harden-LLM calls.")); err != nil {
		return nil, err
	}
	if telemetry.callDuration, err = meter.Float64Histogram("harden_llm.call.duration", metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if telemetry.providerAttempts, err = meter.Int64Counter("harden_llm.provider.attempts"); err != nil {
		return nil, err
	}
	if telemetry.providerDuration, err = meter.Float64Histogram("harden_llm.provider.duration", metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if telemetry.retries, err = meter.Int64Counter("harden_llm.retries"); err != nil {
		return nil, err
	}
	if telemetry.cacheOperations, err = meter.Int64Counter("harden_llm.cache.operations"); err != nil {
		return nil, err
	}
	if telemetry.schemaOperations, err = meter.Int64Counter("harden_llm.schema.operations"); err != nil {
		return nil, err
	}
	if telemetry.tokens, err = meter.Int64Counter("harden_llm.tokens", metric.WithUnit("{token}")); err != nil {
		return nil, err
	}
	if telemetry.costUSD, err = meter.Float64Counter("harden_llm.cost.usd", metric.WithUnit("USD")); err != nil {
		return nil, err
	}
	if telemetry.artifactOperations, err = meter.Int64Counter("harden_llm.artifact.operations"); err != nil {
		return nil, err
	}
	if telemetry.persistenceFailures, err = meter.Int64Counter("harden_llm.persistence.failures"); err != nil {
		return nil, err
	}
	return telemetry, nil
}

func (telemetry *Telemetry) StartCall(ctx context.Context, observation CallObservation) (context.Context, func(CallRecord, error)) {
	started := time.Now()
	provider := providerFamily(observation.Provider, "")
	callType := boundedCallType(observation.CallType)
	ctx, span := telemetry.tracer.Start(ctx, SpanCall, trace.WithAttributes(
		attribute.String("gen_ai.provider.name", provider),
		attribute.String("gen_ai.request.model", boundedSpanValue(observation.ModelID)),
		attribute.String("gen_ai.operation.name", callType),
		attribute.String("harden_llm.profile.id", boundedSpanValue(observation.ProfileID)),
	))
	return ctx, func(record CallRecord, terminalErr error) {
		provider = providerFromRecord(record, provider)
		providerUsage := record.Accounting.Provider.Usage
		outcome, category := outcomeAndCategory(terminalErr)
		attributes := []attribute.KeyValue{
			attribute.String("provider", provider), attribute.String("call_type", callType),
			attribute.String("outcome", outcome), attribute.String("category", category),
			attribute.String("cache_outcome", boundedCacheOutcome(record.Cache.Status)),
		}
		span.SetAttributes(
			attribute.String("harden_llm.outcome", outcome), attribute.String("error.type", category),
			attribute.String("harden_llm.cache.outcome", boundedCacheOutcome(record.Cache.Status)),
			attribute.Int64("gen_ai.usage.input_tokens", providerUsage.PromptTokens()),
			attribute.Int64("gen_ai.usage.output_tokens", providerUsage.CompletionTokens()),
		)
		if record.CallID != "" {
			span.SetAttributes(attribute.String("harden_llm.call.id", boundedSpanValue(record.CallID)))
		}
		if record.TraceID != "" {
			span.SetAttributes(attribute.String("harden_llm.trace.id", boundedSpanValue(record.TraceID)))
		}
		setSpanStatus(span, terminalErr, category)
		span.End()
		telemetry.calls.Add(ctx, 1, metric.WithAttributes(attributes...))
		telemetry.callDuration.Record(ctx, durationSeconds(time.Since(started)), metric.WithAttributes(attributes...))
		telemetry.recordAccounting(ctx, provider, callType, record)
	}
}

func (telemetry *Telemetry) StartRuntime(ctx context.Context, observation CallObservation) (context.Context, func(error)) {
	ctx, span := telemetry.tracer.Start(ctx, SpanRuntime, trace.WithAttributes(
		attribute.String("gen_ai.provider.name", providerFamily(observation.Provider, "")),
		attribute.String("gen_ai.operation.name", boundedCallType(observation.CallType)),
	))
	return ctx, func(err error) {
		_, category := outcomeAndCategory(err)
		setSpanStatus(span, err, category)
		span.End()
	}
}

func (telemetry *Telemetry) StartProvider(ctx context.Context, target ExecutionTarget, callType string) (context.Context, func(error)) {
	started := time.Now()
	provider := providerFamily(target.Provider, target.Protocol)
	callType = boundedCallType(callType)
	ctx, span := telemetry.tracer.Start(ctx, SpanProvider, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("gen_ai.provider.name", provider),
		attribute.String("gen_ai.request.model", boundedSpanValue(target.ModelID)),
		attribute.String("gen_ai.operation.name", callType),
		attribute.String("harden_llm.profile.id", boundedSpanValue(target.ProfileID)),
	))
	return ctx, func(err error) {
		_, category := outcomeAndCategory(err)
		setSpanStatus(span, err, category)
		span.End()
		telemetry.providerAttempts.Add(ctx, 1, metric.WithAttributes(
			attribute.String("provider", provider), attribute.String("call_type", callType),
			attribute.String("outcome", outcomeValue(err)), attribute.String("category", category),
		))
		telemetry.providerDuration.Record(ctx, durationSeconds(time.Since(started)), metric.WithAttributes(
			attribute.String("provider", provider), attribute.String("call_type", callType),
			attribute.String("outcome", outcomeValue(err)), attribute.String("category", category), attribute.String("scope", "attempt"),
		))
	}
}

func (telemetry *Telemetry) RetryHooks(callType string, policy retry.Policy, attemptOffset int, targetFor func(int) ExecutionTarget) retry.Hooks {
	callType = boundedCallType(callType)
	lastProvider := "unknown"
	return retry.Hooks{
		Attempt: func(ctx context.Context, number int, work func(context.Context) error) error {
			globalNumber := attemptOffset + number
			attemptContext, span := telemetry.tracer.Start(ctx, SpanAttempt, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
				attribute.String("gen_ai.operation.name", callType),
				attribute.Int("harden_llm.attempt.number", globalNumber),
			))
			err := work(attemptContext)
			target := targetFor(number)
			lastProvider = providerFamily(target.Provider, target.Protocol)
			span.SetAttributes(
				attribute.String("gen_ai.provider.name", lastProvider),
				attribute.String("gen_ai.request.model", boundedSpanValue(target.ModelID)),
				attribute.String("harden_llm.profile.id", boundedSpanValue(target.ProfileID)),
			)
			_, category := outcomeAndCategoryWithPolicy(err, policy)
			setSpanStatus(span, err, category)
			span.End()
			return err
		},
		Wait: func(ctx context.Context, classification retry.Classification, delay time.Duration, wait func(context.Context, time.Duration) error) error {
			category := boundedCategory(string(classification.Category))
			waitContext, span := telemetry.tracer.Start(ctx, SpanRetryWait, trace.WithAttributes(
				attribute.String("gen_ai.provider.name", lastProvider), attribute.String("error.type", category),
				attribute.Int64("harden_llm.retry.delay_ms", delay.Milliseconds()),
			))
			telemetry.retries.Add(ctx, 1, metric.WithAttributes(
				attribute.String("provider", lastProvider), attribute.String("call_type", callType), attribute.String("category", category),
			))
			err := wait(waitContext, delay)
			setSpanStatus(span, err, boundedCategory(string(retry.Classify(err, policy).Category)))
			span.End()
			return err
		},
	}
}

func (telemetry *Telemetry) ValidateSchema(ctx context.Context, profile Profile, repair bool, validate func(context.Context) error) error {
	validationContext, span := telemetry.tracer.Start(ctx, SpanSchema, trace.WithAttributes(
		attribute.String("gen_ai.provider.name", providerFamily(profile.Provider, profile.APIInferenceType)),
		attribute.Bool("harden_llm.schema.repair", repair),
	))
	err := validate(validationContext)
	outcome := outcomeValue(err)
	_, category := outcomeAndCategory(err)
	setSpanStatus(span, err, category)
	span.End()
	telemetry.schemaOperations.Add(ctx, 1, metric.WithAttributes(
		attribute.String("outcome", outcome), attribute.Bool("repair", repair),
	))
	return err
}

func (telemetry *Telemetry) StartCache(ctx context.Context, operation string) (context.Context, func(string, error)) {
	spanName := SpanCacheLookup
	if operation == "write" {
		spanName = SpanCacheWrite
	}
	cacheContext, span := telemetry.tracer.Start(ctx, spanName, trace.WithAttributes(attribute.String("harden_llm.cache.operation", operation)))
	return cacheContext, func(cacheOutcome string, err error) {
		_, category := outcomeAndCategory(err)
		cacheOutcome = boundedCacheOutcome(cacheOutcome)
		span.SetAttributes(attribute.String("harden_llm.cache.outcome", cacheOutcome))
		setSpanStatus(span, err, category)
		span.End()
		telemetry.cacheOperations.Add(ctx, 1, metric.WithAttributes(
			attribute.String("operation", boundedCacheOperation(operation)), attribute.String("cache_outcome", cacheOutcome),
			attribute.String("outcome", outcomeValue(err)),
		))
	}
}

func (telemetry *Telemetry) StartArtifact(ctx context.Context, kind string) (context.Context, func(error)) {
	artifactContext, span := telemetry.tracer.Start(ctx, SpanArtifact, trace.WithAttributes(
		attribute.String("harden_llm.artifact.kind", boundedArtifactKind(kind)),
	))
	return artifactContext, func(err error) {
		_, category := outcomeAndCategory(err)
		setSpanStatus(span, err, category)
		span.End()
		telemetry.artifactOperations.Add(ctx, 1, metric.WithAttributes(
			attribute.String("store", "artifact"), attribute.String("operation", "put"),
			attribute.String("outcome", outcomeValue(err)), attribute.String("kind", boundedArtifactKind(kind)),
		))
		if err != nil {
			telemetry.persistenceFailures.Add(ctx, 1, metric.WithAttributes(
				attribute.String("store", "artifact"), attribute.String("operation", "put"),
			))
		}
	}
}

func (telemetry *Telemetry) recordAccounting(ctx context.Context, provider, callType string, record CallRecord) {
	base := []attribute.KeyValue{attribute.String("provider", provider), attribute.String("call_type", callType)}
	usage := record.Accounting.Provider.Usage
	for tokenType, value := range map[string]int64{
		"input": usage.InputTokens, "cache_read": usage.CacheReadTokens,
		"cache_creation": usage.CacheCreationTokens, "output": usage.OutputTokens,
		"reasoning": usage.ReasoningTokens,
	} {
		if value > 0 {
			telemetry.tokens.Add(ctx, value, metric.WithAttributes(append(base, attribute.String("token_type", tokenType))...))
		}
	}
	cost := record.Accounting.Provider.Cost
	if cost.KnownObservations > 0 && cost.KnownSubtotalUSD >= 0 {
		telemetry.costUSD.Add(ctx, cost.KnownSubtotalUSD, metric.WithAttributes(
			attribute.String("provider", provider), attribute.String("call_type", callType),
			attribute.String("source", boundedCostSource(cost.Source)),
			attribute.String("coverage", boundedCostSource(string(cost.Status))),
		))
	}
}

func providerFromRecord(record CallRecord, fallback string) string {
	for index := len(record.Attempts) - 1; index >= 0; index-- {
		attempt := record.Attempts[index]
		if attempt.ProviderUsed {
			return providerFamily(attempt.Target.Provider, attempt.Target.Protocol)
		}
	}
	return fallback
}

func setSpanStatus(span trace.Span, err error, category string) {
	if err == nil {
		span.SetStatus(codes.Ok, "")
		return
	}
	span.SetAttributes(attribute.String("error.type", boundedCategory(category)))
	span.SetStatus(codes.Error, boundedCategory(category))
}

func outcomeAndCategory(err error) (string, string) {
	return outcomeAndCategoryWithPolicy(err, retry.DefaultPolicy())
}

func outcomeAndCategoryWithPolicy(err error, policy retry.Policy) (string, string) {
	if err == nil {
		return "success", "success"
	}
	classification := retry.Classify(err, policy)
	return outcomeValue(err), boundedCategory(string(classification.Category))
}

func outcomeValue(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "error"
}

func providerFamily(provider, inferenceType string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch strings.TrimSpace(inferenceType) {
	case "gemini-generate-content":
		return "gemini"
	case "anthropic-messages":
		return "anthropic"
	}
	switch provider {
	case "openai", "gemini", "anthropic", "azure", "xai", "groq", "nvidia":
		return provider
	default:
		return "openai_compatible"
	}
}

func boundedCallType(value string) string {
	switch value {
	case "text", "structured":
		return value
	default:
		return "unknown"
	}
}

func boundedCacheOutcome(value string) string {
	switch value {
	case "hit", "miss", "refresh", "skipped":
		return value
	default:
		return "unknown"
	}
}

func boundedCacheOperation(value string) string {
	if value == "write" {
		return "write"
	}
	return "lookup"
}

func boundedCategory(value string) string {
	switch value {
	case "success", "network", "rate_limit", "server_error", "empty_response", "provider_retry", "parse_error", "refusal", "timeout", "canceled", "other":
		return value
	default:
		return "other"
	}
}

func boundedCostSource(value string) string {
	switch value {
	case "reported", "profile", "mixed", "unknown":
		return value
	default:
		return "unknown"
	}
}

func boundedArtifactKind(value string) string {
	switch value {
	case "trace", "parse-failure-response", "diagnostic-event":
		return value
	default:
		return "unknown"
	}
}

func boundedSpanValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "unknown"
	}
	if len(value) <= 256 {
		return value
	}
	value = value[:256]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func durationSeconds(duration time.Duration) float64 {
	if duration < 0 {
		return 0
	}
	return duration.Seconds()
}
