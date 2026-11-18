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

// OperationDiagnostics collects bounded, request-owned operation records for one
// logical call without global state or wire-schema changes.
type OperationDiagnostics struct {
	mu      sync.Mutex
	records []map[string]any
	clipped bool
}

// WithOperationDiagnostics attaches a fresh request-scoped operation collector.
func WithOperationDiagnostics(ctx context.Context) (context.Context, *OperationDiagnostics) {
	if ctx == nil {
		ctx = context.Background()
	}
	collector := &OperationDiagnostics{}
	return context.WithValue(ctx, operationDiagnosticsKey{}, collector), collector
}

// OperationDiagnosticsFromContext returns the active collector when present.
func OperationDiagnosticsFromContext(ctx context.Context) *OperationDiagnostics {
	if ctx == nil {
		return nil
	}
	collector, _ := ctx.Value(operationDiagnosticsKey{}).(*OperationDiagnostics)
	return collector
}

// Record appends one structured diagnostic event to the collector.
func (c *OperationDiagnostics) Record(msg string, attrs ...any) {
	if c == nil {
		return
	}
	entry := make(map[string]any, 1+len(attrs)/2)
	entry["msg"] = msg
	for i := 0; i+1 < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		if !ok || key == "" {
			continue
		}
		entry[key] = attrs[i+1]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.records) >= maxOperationDiagnosticRecords {
		c.clipped = true
		return
	}
	c.records = append(c.records, entry)
}

// ReconcileAttempt updates the nth provider_model record when post-Execute
// validation or accounting (such as STRUCTURED_SCHEMA or ACCOUNTING_INVALID)
// rejects a response after transport/normalization completed.
func (c *OperationDiagnostics) ReconcileAttempt(attemptIndex int, code, category, stage string) {
	if c == nil || code == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	modelIdx := 0
	for _, rec := range c.records {
		if rec["stage"] != "provider_model" {
			continue
		}
		if modelIdx == attemptIndex {
			if existing, _ := rec["error_code"].(string); existing == "" {
				rec["error_code"] = boundedCode(code)
			}
			if existing, _ := rec["error_category"].(string); existing == "" || existing == "success" {
				rec["error_category"] = boundedCode(category)
			}
			if stage != "" {
				rec["attempt_stage"] = boundedCode(stage)
			}
			return
		}
		modelIdx++
	}
}

// Records returns a detached copy of the captured records and clipping state.
func (c *OperationDiagnostics) Records() ([]map[string]any, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, len(c.records))
	for i, rec := range c.records {
		out[i] = maps.Clone(rec)
	}
	return out, c.clipped
}

// RedactedSnapshot returns scrubbed records, JSON log lines for support bundles,
// and whether capture was clipped.
func (c *OperationDiagnostics) RedactedSnapshot(redactor *redaction.Redactor) ([]map[string]any, []string, bool) {
	records, clipped := c.Records()
	if len(records) == 0 && !clipped {
		return nil, nil, false
	}
	scrubbed := make([]map[string]any, 0, len(records))
	lines := make([]string, 0, len(records)+1)
	for _, rec := range records {
		clean := redactRecordMap(redactor, rec)
		scrubbed = append(scrubbed, clean)
		if encoded, err := json.Marshal(clean); err == nil {
			line := string(encoded)
			if redactor != nil {
				line = redactor.Text(line)
			}
			if len(line) > maxOperationDiagnosticLineLen {
				line = line[:maxOperationDiagnosticLineLen]
				clipped = true
			}
			lines = append(lines, line)
		}
	}
	if clipped {
		lines = append(lines, `{"msg":"diagnostic capture clipped","clipped":true}`)
	}
	return scrubbed, lines, clipped
}

func redactRecordMap(redactor *redaction.Redactor, input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for k, v := range input {
		if text, ok := v.(string); ok && redactor != nil {
			out[k] = redactor.Text(text)
			continue
		}
		out[k] = v
	}
	return out
}

func emitOperationDiagnostic(ctx context.Context, logger *slog.Logger, level slog.Level, msg string, attrs ...any) {
	if logger != nil {
		logger.Log(ctx, level, msg, attrs...)
	}
	if collector := OperationDiagnosticsFromContext(ctx); collector != nil {
		collector.Record(msg, attrs...)
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

func inspectResponseShape(protocol string, response map[string]any) modelOperationFacts {
	if len(response) == 0 {
		return modelOperationFacts{}
	}
	facts := modelOperationFacts{
		responseObserved:  true,
		upstreamProvider:  boundedProviderLabel(stringValue(response["provider"])),
		providerRequestID: boundedRequestID(stringValue(response["id"])),
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
	switch protocol {
	case "openai.responses":
		status := boundedCode(stringValue(response["status"]))
		if strings.EqualFold(status, "incomplete") {
			if reason := boundedCode(stringValue(objectValue(response["incomplete_details"])["reason"])); reason != "" {
				status = "incomplete:" + reason
			}
		}
		facts.finishReason = status
		extraction := extractProviderOutputState(protocol, response)
		if extraction.output != nil {
			if assistant, ok := extraction.output.(runtime.AssistantOutput); ok {
				facts.toolCallCount = len(assistant.ToolCalls)
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
				if facts.toolCallCount == 0 {
					facts.toolCallCount++
				}
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
	return facts
}

func extractUpstreamErrorCode(errObj map[string]any) string {
	if len(errObj) == 0 {
		return ""
	}
	meta := objectValue(errObj["metadata"])
	if raw, ok := meta["raw"].(string); ok && raw != "" {
		lower := strings.ToLower(raw)
		if strings.Contains(lower, "tool choice is none, but model called a tool") || strings.Contains(lower, "called a tool") && strings.Contains(lower, "none") {
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
