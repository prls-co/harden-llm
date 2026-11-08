package httpclient

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-422 / NER migration case 502.
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/prls-co/harden-llm/api"
)

func TestResponsesClientContract(t *testing.T) {
	input := ResponsesInput{Model: "test-model", Input: []ResponseMessage{{"system", "rules"}, {"user", "input"}, {"assistant", "invalid"}, {"user", "correct"}}, Text: ResponseText{Format: ResponseFormat{Type: "json_schema", Name: "entity", Schema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`), Strict: true}}, Harden: ResponseControls{Diagnostics: true}}
	client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected HTTP request %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if err := api.Validate("ResponseRequest", body); err != nil {
			t.Error(err)
		}
		var got ResponsesInput
		if err := json.Unmarshal(body, &got); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(got, input) {
			t.Errorf("request changed: %#v", got)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_x","object":"response","created_at":1,"status":"completed","model":"test-model","output":[],"output_text":"{}","parallel_tool_calls":false,"harden":{}}`)
	})
	got, err := client.Responses(context.Background(), input)
	if err != nil {
		t.Fatalf("Responses: %v", err)
	}
	if got.OutputText != "{}" || got.Model != "test-model" || len(got.Harden) == 0 || calls.Load() != 1 {
		t.Fatalf("result=%+v calls=%d", got, calls.Load())
	}
}

func TestResponsesClientOutcomeClassification(t *testing.T) {
	input := ResponsesInput{Model: "test-model", Input: []ResponseMessage{{"user", "private prompt"}}, Text: ResponseText{Format: ResponseFormat{Type: "json_schema", Name: "entity", Schema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`), Strict: true}}, Harden: ResponseControls{Diagnostics: true}}
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		known       bool
	}{
		{"rejection", 400, "application/json", `{"error":{"message":"private prompt","type":"invalid_request_error","param":null,"code":"invalid_request"}}`, true},
		{"terminal failure with evidence", 502, "application/json", `{"error":{"message":"failure","type":"server_error","param":null,"code":"upstream_error"},"output_text":"private output","harden":{"execution_id":"call-1"}}`, true},
		{"ambiguous server error", 502, "application/json", `{"error":{"message":"proxy error","type":"server_error","param":null,"code":"bad_gateway"}}`, false},
		{"invalid success", 200, "application/json", `{"output_text":"{}"}`, false},
		{"wrong media", 200, "text/plain", `ok`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			_, err := client.Responses(context.Background(), input)
			if err == nil {
				t.Fatal("expected failure")
			}
			var failure *ResponsesFailure
			var unknown *UnknownOutcomeError
			if tc.known != errors.As(err, &failure) || tc.known == errors.As(err, &unknown) {
				t.Fatalf("unexpected classification %T", err)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("error leaked remote content")
			}
			if calls.Load() != 1 {
				t.Fatalf("POST count=%d", calls.Load())
			}
			if failure != nil && tc.status == 502 && (failure.OutputText != "private output" || len(failure.Harden) == 0) {
				t.Fatal("terminal facts were lost")
			}
		})
	}
}

// TEST-422: Execution identity never overrides an explicit uncertain outcome.
func TestResponsesClientPreservesUncertainty(t *testing.T) {
	t.Parallel()
	input := ResponsesInput{Model: "test-model", Input: []ResponseMessage{{Role: "user", Content: "private prompt"}}, Text: ResponseText{Format: ResponseFormat{Type: "json_schema", Name: "entity", Schema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`), Strict: true}}, Harden: ResponseControls{Diagnostics: true}}
	for _, tc := range []struct {
		name        string
		status      int
		code, retry string
		diagnostics bool
		unknown     bool
	}{
		{"network with evidence", 502, "NETWORK_ERROR", "false", true, true},
		{"network without evidence", 502, "NETWORK_ERROR", "false", false, true},
		{"timeout with evidence", 504, "request_timeout", "false", true, true},
		{"timeout without evidence", 504, "request_timeout", "false", false, true},
		{"partial refusal with evidence", 429, "rate_limit_exceeded", "false", true, true},
		{"partial refusal without evidence", 429, "rate_limit_exceeded", "false", false, true},
		{"explicit uncertain code", 502, "execution_unconfirmed", "", true, true},
		{"explicit uncertain code without evidence", 502, "execution_unconfirmed", "", false, true},
		{"validation rejection", 400, "invalid_request", "", false, false},
		{"authentication rejection", 401, "invalid_api_key", "", false, false},
		{"authorization rejection", 403, "forbidden", "", false, false},
		{"tracking conflict", 409, "tracking_id_conflict", "false", false, false},
		{"definitive terminal error", 502, "upstream_error", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := map[string]any{"error": map[string]any{"message": "private remote content", "type": "server_error", "param": nil, "code": tc.code}, "output_text": "private partial output"}
			if tc.diagnostics {
				envelope["harden"] = map[string]any{"execution_id": "fixture-call", "trace_id": "fixture-trace"}
			}
			client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tc.retry != "" {
					w.Header().Set("x-should-retry", tc.retry)
				}
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(envelope)
			})
			_, err := client.Responses(context.Background(), input)
			var unknown *UnknownOutcomeError
			var terminal *ResponsesFailure
			isUnknown, isTerminal := errors.As(err, &unknown), errors.As(err, &terminal)
			if isUnknown != tc.unknown || isTerminal == tc.unknown {
				t.Fatalf("uncertainty classification: error=%T unknown=%t terminal=%t", err, unknown != nil, terminal != nil)
			}
			if tc.unknown {
				var facts *ResponsesUnconfirmed
				if !errors.As(unknown, &facts) || facts.StatusCode != tc.status || facts.Code != tc.code || facts.OutputText != "private partial output" || strings.Contains(facts.Error(), "private") {
					t.Fatalf("unknown response facts lost or exposed: cause=%T", unknown.Cause)
				}
				if tc.diagnostics && !strings.Contains(string(facts.Harden), "fixture-call") || !tc.diagnostics && len(facts.Harden) != 0 {
					t.Fatal("unknown response diagnostics changed")
				}
			}
			if calls.Load() != 1 || strings.Contains(err.Error(), "private") {
				t.Fatalf("replayed or leaked content: calls=%d error=%v", calls.Load(), err)
			}
		})
	}
}

func TestResponsesClientRejectsInvalidBeforeDispatch(t *testing.T) {
	client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected request") })
	_, err := client.Responses(context.Background(), ResponsesInput{Model: "test", Input: []ResponseMessage{{"user", string([]byte{0xff})}}})
	if err == nil || calls.Load() != 0 {
		t.Fatalf("invalid request err=%v calls=%d", err, calls.Load())
	}
}

func TestResponsesClientTransportLossNeverReplays(t *testing.T) {
	input := ResponsesInput{Model: "test-model", Input: []ResponseMessage{{"user", "input"}}, Text: ResponseText{Format: ResponseFormat{Type: "json_schema", Name: "entity", Schema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`), Strict: true}}}
	client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	})
	_, err := client.Responses(context.Background(), input)
	var unknown *UnknownOutcomeError
	if !errors.As(err, &unknown) || calls.Load() != 1 {
		t.Fatalf("error=%T calls=%d", err, calls.Load())
	}
}

func TestResponsesClientEscapedInputWithinBudget(t *testing.T) {
	text := strings.Repeat("\x00", 32<<10)
	input := ResponsesInput{Model: "test-model", Input: []ResponseMessage{{"system", "rules"}, {"user", text}, {"assistant", `{}`}, {"user", "correct"}}, Text: ResponseText{Format: ResponseFormat{Type: "json_schema", Name: "entity", Schema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`), Strict: true}}}
	client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var got ResponsesInput
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
			return
		}
		if len(got.Input) != 4 || got.Input[1].Content != text {
			t.Error("source input did not survive transport")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_x","object":"response","created_at":1,"status":"completed","model":"test-model","output":[],"output_text":"{}","parallel_tool_calls":false}`)
	})
	if _, err := client.Responses(context.Background(), input); err != nil || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

// TEST-422: The retained 256 KiB encoded bound rejects oversized input before HTTP dispatch.
func TestResponsesClientRejectsEncodedRequestOverBudget(t *testing.T) {
	for _, text := range []string{strings.Repeat("x", 256<<10), strings.Repeat("\x00", 256<<10)} {
		input := ResponsesInput{Model: "test-model", Input: []ResponseMessage{{Role: "user", Content: text}}, Text: ResponseText{Format: ResponseFormat{Type: "json_schema", Name: "entity", Schema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`), Strict: true}}}
		client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			t.Error("oversized request reached HTTP transport")
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"resp_x","object":"response","created_at":1,"status":"completed","model":"test-model","output":[],"output_text":"{}","parallel_tool_calls":false}`)
		})
		_, err := client.Responses(context.Background(), input)
		var protocol *ProtocolError
		if !errors.As(err, &protocol) || calls.Load() != 0 {
			t.Fatalf("oversized request err=%T HTTP calls=%d", err, calls.Load())
		}
	}
}

func TestResponsesClientRejectsOutputOutsideRequestedSchema(t *testing.T) {
	input := ResponsesInput{Model: "test-model", Input: []ResponseMessage{{Role: "user", Content: "input"}}, Text: ResponseText{Format: ResponseFormat{Type: "json_schema", Name: "entity", Schema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`), Strict: true}}}
	client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_x","object":"response","created_at":1,"status":"completed","model":"test-model","output":[],"output_text":"{\"name\":1}","parallel_tool_calls":false,"harden":{"execution_id":"fixture-call","trace_id":"fixture-trace"}}`)
	})
	_, err := client.Responses(context.Background(), input)
	var invalid *ResponsesInvalidOutput
	if !errors.As(err, &invalid) || invalid.Model != "test-model" || !strings.Contains(string(invalid.Harden), "fixture-trace") || calls.Load() != 1 {
		t.Fatalf("invalid structured output error=%T calls=%d", err, calls.Load())
	}
}
