package httpclient

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-535

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestWebSearchRequestParityClient(t *testing.T) {
	input := ResponsesInput{Model: "test-model", Input: []ResponseMessage{{Role: "user", Content: "input"}}, Text: ResponseText{Format: ResponseFormat{Type: "json_schema", Name: "entity", Schema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`), Strict: true}}, Tools: []json.RawMessage{json.RawMessage(`{"type":"web_search","filters":{"blocked_domains":["example.test"]}}`)}, ToolChoice: json.RawMessage(`"required"`), Include: []string{"web_search_call.action.sources"}}
	client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var got ResponsesInput
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		if len(got.Tools) != 1 || len(got.Include) != 1 || string(got.ToolChoice) != `"required"` {
			t.Errorf("search fields lost: %#v", got)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_x","object":"response","created_at":1,"status":"completed","model":"test-model","output":[],"output_text":"{}","parallel_tool_calls":false}`)
	})
	if _, err := client.Responses(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("request not dispatched")
	}
	for _, bad := range []string{"include", "tool"} {
		t.Run(bad, func(t *testing.T) {
			request := input
			if bad == "include" {
				request.Include = []string{string([]byte{0xff})}
			} else {
				request.Tools = []json.RawMessage{append(append([]byte(`{"type":"web_search","x":"`), 0xff), []byte(`"}`)...)}
			}
			_, err := client.Responses(context.Background(), request)
			var protocol *ProtocolError
			if !errors.As(err, &protocol) || calls.Load() != 1 {
				t.Fatalf("invalid UTF-8 dispatched or misclassified: %v", err)
			}
		})
	}
}
