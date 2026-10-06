package providers

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/prls-co/harden-llm/internal/runtime"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-012
func TestOrderedMessagesPreserveOptionalSystemInstruction(t *testing.T) {
	t.Parallel()
	for _, protocol := range []string{"chat-completions", "responses"} {
		for _, callType := range []string{"text", "structured"} {
			for _, systemPrompt := range []string{"", "Be exact.", "  Preserve whitespace.  "} {
				t.Run(protocol+"/"+callType+"/"+systemPrompt, func(t *testing.T) {
					t.Parallel()
					inputMessages := providerMessages(systemPrompt, "Answer.")
					_, _, _, body, _, err := buildPayload(runtime.Connection{Provider: "fixture", APIInferenceType: protocol}, runtime.Call{
						ModelID: "fixture", Messages: inputMessages, CallType: callType,
						Schema: json.RawMessage(`{"type":"object"}`),
					})
					if err != nil {
						t.Fatal(err)
					}
					field := "messages"
					if protocol == "responses" {
						field = "input"
					}
					var gotMessages []map[string]any
					for _, raw := range arrayValue(body[field]) {
						message := objectValue(raw)
						content := message["content"]
						gotMessages = append(gotMessages, map[string]any{"role": message["role"], "content": content})
					}
					userContent := any("Answer.")
					systemContent := any(systemPrompt)
					if protocol == "responses" {
						userContent = []any{map[string]any{"type": "input_text", "text": "Answer."}}
						systemContent = []any{map[string]any{"type": "input_text", "text": systemPrompt}}
					}
					want := []map[string]any{{"role": "user", "content": userContent}}
					if systemPrompt != "" {
						want = append([]map[string]any{{"role": "system", "content": systemContent}}, want...)
					}
					if !reflect.DeepEqual(gotMessages, want) {
						t.Fatalf("messages = %#v, want %#v", gotMessages, want)
					}
				})
			}
		}
	}
}

func TestPerplexityUsesAgentEndpoint(t *testing.T) {
	t.Parallel()
	connection := runtime.Connection{Provider: "perplexity", APIInferenceType: "responses"}
	call := runtime.Call{ModelID: "openai/gpt-6.1-sol", CallType: "text", Messages: providerMessages("", "Answer.")}
	_, protocol, path, _, _, err := buildPayload(connection, call)
	if err != nil || protocol != "openai.responses" || path != "/agent" {
		t.Fatalf("Agent route: protocol=%q path=%q error=%v", protocol, path, err)
	}
	call.ProviderOptions = map[string]any{"useResponsesApi": false}
	_, _, _, _, _, err = buildPayload(connection, call)
	if err == nil {
		t.Fatal("Perplexity Agent API accepted a legacy chat override")
	}
}
