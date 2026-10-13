package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-457

import (
	"bytes"
	"github.com/prls-co/harden-llm/stateful"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReferenceRejectionCorrelation(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	api := &API{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	output := stateful.RunOutput{RecordedRunOutput: stateful.RecordedRunOutput{RunID: "00000000-0000-4000-8000-000000000001", TraceID: "00000000-0000-4000-8000-000000000002", Status: "failed"}}
	problem := &stateful.ValidationError{Code: "profile_invalid", FieldErrors: []stateful.FieldError{{Field: "reasoningEffort", Message: "do-not-log-provider-secret"}}}
	writer := httptest.NewRecorder()
	api.referenceRunFailure(writer, httptest.NewRequest("POST", "/api/v1/run", nil), problem, output)
	text := logs.String()
	if writer.Code != 400 || !strings.Contains(text, output.RunID) || !strings.Contains(text, output.TraceID) || !strings.Contains(text, "profile_invalid") || strings.Contains(text, "do-not-log-provider-secret") {
		t.Fatalf("rejection must log correlation without payload/messages: code=%d logs=%s", writer.Code, text)
	}
}
