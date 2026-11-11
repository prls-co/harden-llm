package httpclient

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-536

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestResponsesSearchProjectionTypedClient(t *testing.T) {
	client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_x","object":"response","created_at":1,"status":"completed","model":"fixture","output":[{"id":"ws_1","type":"web_search_call","status":"completed","action":{"type":"search","query":"q","sources":[{"type":"url","url":"https://example.test"}]}},{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"{}","annotations":[{"type":"url_citation","start_index":0,"end_index":2,"url":"https://example.test","title":"A"}]}]}],"output_text":"{}","parallel_tool_calls":false}`)
	})
	input := ResponsesInput{Model: "fixture", Input: []ResponseMessage{{Role: "user", Content: "hello"}}, Text: ResponseText{Format: ResponseFormat{Type: "json_schema", Name: "entity", Schema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`), Strict: true}}}
	result, err := client.Responses(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(result.Output) != 2 || result.Output[0].Action.Query != "q" || result.Output[0].Action.Sources[0].URL != "https://example.test" || result.Output[1].Content[0].Annotations[0].EndIndex != 2 {
		t.Fatalf("typed provenance lost: %#v", result)
	}
}
