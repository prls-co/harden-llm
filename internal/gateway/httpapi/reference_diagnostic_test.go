package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-457

import (
	"bytes"
	"context"
	"encoding/json"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
	"github.com/prls-co/harden-llm/stateful/batch"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-540.
func TestReferenceUnconfirmedErrorProjection(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{&hardenllm.ProviderError{Code: "NETWORK_ERROR", Category: "network"}, context.DeadlineExceeded, context.Canceled, &hardenllm.ProviderError{Code: "execution_unconfirmed", Category: "other"}} {
		_, err := batch.ObserveProviderDispatch(hardenllm.ProviderResult{ProviderDispatched: true}, cause)
		writer := httptest.NewRecorder()
		api := &API{logger: slog.Default()}
		api.referenceRunFailure(writer, httptest.NewRequest("POST", "/api/v1/run", nil), err, stateful.RunOutput{RecordedRunOutput: stateful.RecordedRunOutput{Status: "failed"}, History: stateful.RecordingOutcome{Status: "saved"}})
		var envelope referenceErrorEnvelope
		if json.Unmarshal(writer.Body.Bytes(), &envelope) != nil || envelope.Error.Code != "execution_unconfirmed" || envelope.Error.Diagnostic.InferenceOutcome != "unconfirmed" || envelope.Error.Diagnostic.PersistenceOutcome != "saved" || envelope.Error.Diagnostic.Cause != "unknown" || writer.Header().Get("x-should-retry") != "false" {
			t.Fatalf("reference projection lost uncertainty: status=%d headers=%v body=%s", writer.Code, writer.Header(), writer.Body)
		}
	}
}

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
