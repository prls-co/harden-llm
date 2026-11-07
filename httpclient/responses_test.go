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

func TestResponsesClientFullEscapedSourceInput(t *testing.T) {
	text := strings.Repeat("\x00", 256<<10)
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
