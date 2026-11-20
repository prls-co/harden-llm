package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"

	"github.com/prls-co/harden-llm/internal/redaction"
	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
)

const (
	maxOperationDiagnosticRecords = 32
	maxOperationDiagnosticLineLen = 4096
)

type operationDiagnosticsKey struct{}
type diagnosticAttemptKey struct{}

type diagnosticAttempt struct {
	number   int
	redactor *redaction.Redactor
}

// WithDiagnosticAttempt binds the runtime attempt to its external operations.
func WithDiagnosticAttempt(ctx context.Context, number int, redactor *redaction.Redactor) context.Context {
	return context.WithValue(ctx, diagnosticAttemptKey{}, diagnosticAttempt{number, redactor})
}

// OperationDiagnostics collects bounded, request-owned operation records for one
// logical call without global state or wire-schema changes.
type OperationDiagnostics struct {
	redactor *redaction.Redactor
	mu       sync.Mutex
	records  []map[string]any
	lines    []string
	clipped  bool
}

// WithOperationDiagnostics attaches a fresh request-scoped operation collector.
func WithOperationDiagnostics(ctx context.Context, secrets ...string) (context.Context, *OperationDiagnostics) {
	if ctx == nil {
		ctx = context.Background()
	}
	collector := &OperationDiagnostics{redactor: redaction.New(secrets...)}
	return context.WithValue(ctx, operationDiagnosticsKey{}, collector), collector
}

func (c *OperationDiagnostics) record(entry map[string]any, line []byte, clipped bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if clipped || len(c.records) >= maxOperationDiagnosticRecords {
		c.clipped = true
		return
	}
	c.records = append(c.records, entry)
	c.lines = append(c.lines, string(line))
}

// Snapshot renders the same redacted operation records for reports and support bundles.
func (c *OperationDiagnostics) Snapshot() ([]map[string]any, []string, bool) {
	if c == nil {
		return nil, nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	records := make([]map[string]any, len(c.records))
	for i, rec := range c.records {
		records[i] = maps.Clone(rec)
	}
	lines := append([]string(nil), c.lines...)
	if c.clipped {
		lines = append(lines, `{"msg":"diagnostic capture clipped","clipped":true}`)
	}
	return records, lines, c.clipped
}

func emitOperationDiagnostic(ctx context.Context, logger *slog.Logger, level slog.Level, msg string, attrs ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	collector, _ := ctx.Value(operationDiagnosticsKey{}).(*OperationDiagnostics)
	attempt, _ := ctx.Value(diagnosticAttemptKey{}).(diagnosticAttempt)
	scrubber := attempt.redactor
	if collector != nil {
		scrubber = collector.redactor
	}
	if collector == nil && (logger == nil || !logger.Enabled(ctx, level)) {
		return
	}
	if scrubber == nil {
		scrubber = redaction.New()
	}
	if attempt.number > 0 {
		attrs = append(attrs, "attempt_number", attempt.number)
	}
	for i := 0; i < len(attrs); i += 2 {
		attrs[i+1] = scrubber.Attribute(attrs[i].(string), attrs[i+1])
	}
	if collector != nil {
		entry := map[string]any{"msg": msg}
		for i := 0; i < len(attrs); i += 2 {
			entry[attrs[i].(string)] = attrs[i+1]
		}
		line, err := json.Marshal(entry)
		clipped := err != nil || len(line) > maxOperationDiagnosticLineLen
		collector.record(entry, line, clipped)
		if clipped {
			for i := 1; i < len(attrs); i += 2 {
				if value, ok := attrs[i].(string); ok {
					attrs[i] = boundedWebSearchText(value, 128)
				}
			}
			attrs = append(attrs, "clipped", true)
		}
	}
	if logger != nil {
		logger.Log(ctx, level, msg, attrs...)
	}
}

type modelOperationFacts struct {
	httpStatus        int
	headersReceived   bool
	upstreamProvider  string
	providerRequestID string
	upstreamErrorCode string
	finishReason      string
	finalTextPresent  bool
	finalTextBytes    int64
	reasoningPresent  bool
	toolCallCount     int
	responseObserved  bool
}

func (facts *modelOperationFacts) observeUpstream(response map[string]any) {
	facts.upstreamProvider = boundedProviderLabel(stringValue(response["provider"]))
	if facts.providerRequestID == "" {
		facts.providerRequestID = boundedRequestID(stringValue(response["id"]))
	}
	if errObj := objectValue(response["error"]); len(errObj) > 0 {
		if facts.providerRequestID == "" {
			facts.providerRequestID = boundedRequestID(stringValue(errObj["request_id"]))
		}
		if facts.upstreamProvider == "" {
			meta := objectValue(errObj["metadata"])
			facts.upstreamProvider = boundedProviderLabel(stringValue(meta["provider_name"]))
		}
		facts.upstreamErrorCode = extractUpstreamErrorCode(errObj)
	}
}

func (facts *modelOperationFacts) observeResponse(protocol string, response map[string]any, extraction providerOutputExtraction) {
	facts.observeUpstream(response)
	if len(response) == 0 {
		return
	}
	facts.responseObserved = true
	switch protocol {
	case "openai.responses":
		status := boundedCode(stringValue(response["status"]))
		if strings.EqualFold(status, "incomplete") {
			if reason := boundedCode(stringValue(objectValue(response["incomplete_details"])["reason"])); reason != "" {
				status = "incomplete:" + reason
			}
		}
		facts.finishReason = status
		if extraction.output != nil {
			if assistant, ok := extraction.output.(runtime.AssistantOutput); ok {
				facts.finalTextBytes = int64(len(assistant.Content))
				facts.finalTextPresent = strings.TrimSpace(assistant.Content) != ""
			} else {
				facts.finalTextBytes = outputByteCount(extraction.output)
				facts.finalTextPresent = !extraction.empty
			}
		} else {
			facts.finalTextBytes = int64(len(extraction.text))
			facts.finalTextPresent = strings.TrimSpace(extraction.text) != ""
		}
		for _, item := range arrayValue(response["output"]) {
			obj := objectValue(item)
			switch stringValue(obj["type"]) {
			case "reasoning":
				facts.reasoningPresent = true
			case "function_call":
				facts.toolCallCount++
			}
		}
		if usage, err := normalizeOpenAIUsage(response); err == nil && usage.ReasoningTokens > 0 {
			facts.reasoningPresent = true
		}
	case "openai.chat.completions", "openai-compatible.chat.completions":
		if choices := arrayValue(response["choices"]); len(choices) > 0 {
			choice := objectValue(choices[0])
			facts.finishReason = boundedCode(stringValue(choice["finish_reason"]))
			msg := objectValue(choice["message"])
			text := stringValue(msg["content"])
			facts.finalTextBytes = int64(len(text))
			facts.finalTextPresent = strings.TrimSpace(text) != ""
			facts.toolCallCount = len(arrayValue(msg["tool_calls"]))
			if strings.TrimSpace(stringValue(msg["reasoning"])) != "" || strings.TrimSpace(stringValue(msg["reasoning_content"])) != "" || len(arrayValue(msg["reasoning_details"])) > 0 {
				facts.reasoningPresent = true
			}
		}
		if usage, err := normalizeOpenAIUsage(response); err == nil && usage.ReasoningTokens > 0 {
			facts.reasoningPresent = true
		}
	case "google.gemini.generateContent":
		if feedback := objectValue(response["promptFeedback"]); stringValue(feedback["blockReason"]) != "" {
			facts.finishReason = boundedCode(stringValue(feedback["blockReason"]))
		}
		if candidates := arrayValue(response["candidates"]); len(candidates) > 0 {
			cand := objectValue(candidates[0])
			if fr := boundedCode(stringValue(cand["finishReason"])); fr != "" {
				facts.finishReason = fr
			}
			for _, partValue := range arrayValue(objectValue(cand["content"])["parts"]) {
				part := objectValue(partValue)
				if _, ok := part["functionCall"]; ok {
					facts.toolCallCount++
					continue
				}
				if part["thought"] == true {
					facts.reasoningPresent = true
					continue
				}
				if t := stringValue(part["text"]); t != "" {
					facts.finalTextBytes += int64(len(t))
					if strings.TrimSpace(t) != "" {
						facts.finalTextPresent = true
					}
				}
			}
		}
		if usage, err := normalizeGeminiUsage(response); err == nil && usage.ReasoningTokens > 0 {
			facts.reasoningPresent = true
		}
	case "anthropic.messages":
		facts.finishReason = boundedCode(stringValue(response["stop_reason"]))
		for _, blockValue := range arrayValue(response["content"]) {
			block := objectValue(blockValue)
			switch stringValue(block["type"]) {
			case "text":
				t := stringValue(block["text"])
				facts.finalTextBytes += int64(len(t))
				if strings.TrimSpace(t) != "" {
					facts.finalTextPresent = true
				}
			case "tool_use":
				facts.toolCallCount++
			case "thinking", "redacted_thinking":
				facts.reasoningPresent = true
			}
		}
	}
}

func extractUpstreamErrorCode(errObj map[string]any) string {
	if len(errObj) == 0 {
		return ""
	}
	meta := objectValue(errObj["metadata"])
	if raw, ok := meta["raw"].(string); ok && raw != "" {
		lower := strings.ToLower(raw)
		if strings.Contains(lower, "tool choice is none, but model called a tool") {
			return "model_called_tool_when_none_enabled"
		}
		var rawPayload map[string]any
		if json.Unmarshal([]byte(raw), &rawPayload) == nil {
			if rawErr := objectValue(rawPayload["error"]); len(rawErr) > 0 {
				if msg := strings.ToLower(stringValue(rawErr["message"])); strings.Contains(msg, "tool choice is none, but model called a tool") {
					return "model_called_tool_when_none_enabled"
				}
				if code := boundedCode(stringValue(rawErr["code"])); code != "" && code != "provider_error" {
					return code
				}
			}
		}
	}
	if msg := strings.ToLower(stringValue(errObj["message"])); strings.Contains(msg, "tool choice is none, but model called a tool") {
		return "model_called_tool_when_none_enabled"
	}
	if code := boundedCode(stringValue(errObj["code"])); code != "" && code != "provider_error" {
		return code
	}
	return ""
}

func boundedProviderLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, r := range value {
		if !(r == ' ' || r == '_' || r == '-' || r == '.' || r == '/' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return ""
		}
	}
	return value
}

func boundedRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if !(r == '_' || r == '-' || r == '.' || r == ':' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return ""
		}
	}
	return value
}

func responseHeaderRequestID(headers http.Header, cpaID string) string {
	if cpaID != "" {
		return cpaID
	}
	for _, key := range []string{"X-Request-Id", "Request-Id", "Anthropic-Request-Id", "X-Openrouter-Id"} {
		if id := boundedRequestID(headers.Get(key)); id != "" {
			return id
		}
	}
	return ""
}

func classifyDiagnosticError(err error) (code string, typeName string, category string) {
	if err == nil {
		return "", "", string(retry.CategorySuccess)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "DEADLINE_EXCEEDED", "", string(retry.CategoryTimeout)
	}
	if errors.Is(err, context.Canceled) {
		return "CANCELED", "", string(retry.CategoryCanceled)
	}
	var providerErr *retry.ProviderError
	if errors.As(err, &providerErr) {
		c := retry.Classify(err, retry.Policy{MaxAttempts: 1})
		return boundedCode(providerErr.Code), boundedCode(providerErr.Type), string(c.Category)
	}
	var validationErr *retry.ValidationError
	if errors.As(err, &validationErr) {
		return "INVALID_REQUEST", "", string(retry.CategoryOther)
	}
	c := retry.Classify(err, retry.Policy{MaxAttempts: 1})
	return boundedCode(c.Code), boundedCode(c.Type), string(c.Category)
}

// Capture only finite labels before transport normalization discards the raw
// error. Never retain Error(), addresses, URLs or provider/credential material.
// These labels describe local observations, not the identity of a failed hop.
func captureTransportFailure(d *runtime.StreamDiagnostics, parent context.Context, err error, phase string) {
	d.TransportErrorClass = transportErrorClass(err)
	// net/http's header timeout can wrap DeadlineExceeded while the logical
	// context is still live. Only the parent's own expiry proves that deadline.
	if errors.Is(err, context.DeadlineExceeded) && parent != nil && errors.Is(parent.Err(), context.DeadlineExceeded) {
		d.TransportErrorClass = "deadline"
	}
	d.TransportErrorPhase = phase
}

func transportErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	case errors.Is(err, io.EOF):
		return "eof"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.EPIPE):
		return "broken_pipe"
	case errors.Is(err, net.ErrClosed):
		return "connection_closed"
	case permanentTransportError(err):
		return "tls_or_transport_configuration"
	}
	var policy *endpointPolicyError
	if errors.As(err, &policy) {
		return "endpoint_policy"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return "network_error"
}
