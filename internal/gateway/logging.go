package gateway

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/prls-co/harden-llm/internal/redaction"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	otellog "go.opentelemetry.io/otel/log"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/trace"
)

const loggingInstrumentationName = "github.com/prls-co/harden-llm/internal/gateway"

// NewStructuredLogger creates the process's single application logging path:
// one redacted JSON stdout record and one equivalent OTel log record.
func NewStructuredLogger(stdout io.Writer, provider otellog.LoggerProvider, redactor *redaction.Redactor) *slog.Logger {
	if stdout == nil {
		stdout = io.Discard
	}
	if provider == nil {
		provider = lognoop.NewLoggerProvider()
	}
	if redactor == nil {
		redactor = redaction.New()
	}
	jsonHandler := slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	otelHandler := otelslog.NewHandler(loggingInstrumentationName, otelslog.WithLoggerProvider(provider))
	multi := slog.NewMultiHandler(jsonHandler, otelHandler)
	return slog.New(&correlationHandler{next: &redactingHandler{next: multi, redactor: redactor}})
}

type correlationHandler struct{ next slog.Handler }

func (handler *correlationHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

func (handler *correlationHandler) Handle(ctx context.Context, record slog.Record) error {
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		record = record.Clone()
		record.AddAttrs(
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}
	return handler.next.Handle(ctx, record)
}

func (handler *correlationHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	return &correlationHandler{next: handler.next.WithAttrs(attributes)}
}

func (handler *correlationHandler) WithGroup(name string) slog.Handler {
	return &correlationHandler{next: handler.next.WithGroup(name)}
}

type redactingHandler struct {
	next     slog.Handler
	redactor *redaction.Redactor
}

func (handler *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

func (handler *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	record = record.Clone()
	record.Message = handler.redactor.Text(record.Message)
	attributes := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attribute slog.Attr) bool {
		attributes = append(attributes, redactLogAttribute(handler.redactor, attribute))
		return true
	})
	replacement := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	replacement.AddAttrs(attributes...)
	return handler.next.Handle(ctx, replacement)
}

func (handler *redactingHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attributes))
	for index, attribute := range attributes {
		redacted[index] = redactLogAttribute(handler.redactor, attribute)
	}
	return &redactingHandler{next: handler.next.WithAttrs(redacted), redactor: handler.redactor}
}

func (handler *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: handler.next.WithGroup(name), redactor: handler.redactor}
}

func redactLogAttribute(redactor *redaction.Redactor, attribute slog.Attr) slog.Attr {
	attribute.Value = attribute.Value.Resolve()
	if attribute.Value.Kind() == slog.KindGroup {
		members := attribute.Value.Group()
		redacted := make([]slog.Attr, len(members))
		for index, member := range members {
			redacted[index] = redactLogAttribute(redactor, member)
		}
		return slog.Attr{Key: attribute.Key, Value: slog.GroupValue(redacted...)}
	}
	value := attribute.Value.Any()
	// slog's JSON handler serializes error and Stringer values through their
	// text methods. Normalize that text before either output handler sees it.
	// Without this branch an otherwise redacted error could disclose a provider
	// response, credential, or endpoint through KindAny.
	switch typed := value.(type) {
	case error:
		value = redactor.Text(typed.Error())
	case fmt.Stringer:
		value = redactor.Text(typed.String())
	}
	safe := redactor.Attribute(attribute.Key, value)
	if safeString, ok := safe.(string); ok && safeString == redaction.Replacement {
		return slog.String(attribute.Key, redaction.Replacement)
	}
	if attribute.Value.Kind() == slog.KindString {
		return slog.String(attribute.Key, safe.(string))
	}
	if attribute.Value.Kind() == slog.KindAny {
		return slog.Any(attribute.Key, safe)
	}
	return attribute
}
