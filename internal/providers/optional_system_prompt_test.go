package providers

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/prls-co/harden-llm/internal/runtime"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-012
func TestOptionalSystemPrompt(t *testing.T) {
	t.Parallel()
	for _, protocol := range []string{"chat-completions", "responses"} {
		for _, callType := range []string{"text", "structured"} {
			for _, systemPrompt := range []string{"", "Be exact.", "  Preserve whitespace.  "} {
				t.Run(protocol+"/"+callType+"/"+systemPrompt, func(t *testing.T) {
					t.Parallel()
					_, _, _, body, _, err := buildPayload(runtime.Profile{Provider: "fixture", APIInferenceType: protocol}, runtime.Call{
						SystemPrompt: systemPrompt, UserPrompt: "Answer.", CallType: callType,
						Schema: json.RawMessage(`{"type":"object"}`),
					})
					if err != nil {
						t.Fatal(err)
					}
					field := "messages"
					if protocol == "responses" {
						field = "input"
					}
					var messages []map[string]any
					for _, raw := range arrayValue(body[field]) {
						message := objectValue(raw)
						content := message["content"]
						if protocol == "responses" {
							content = objectValue(arrayValue(content)[0])["text"]
						}
						messages = append(messages, map[string]any{"role": message["role"], "content": content})
					}
					want := []map[string]any{{"role": "user", "content": "Answer."}}
					if systemPrompt != "" {
						want = append([]map[string]any{{"role": "system", "content": systemPrompt}}, want...)
					}
					if !reflect.DeepEqual(messages, want) {
						t.Fatalf("messages = %#v, want %#v", messages, want)
					}
				})
			}
		}
	}
}

func TestPerplexityUsesAgentEndpoint(t *testing.T) {
	t.Parallel()
	profile := runtime.Profile{Provider: "perplexity", APIInferenceType: "responses", ModelID: "perplexity/sonar"}
	_, protocol, path, _, _, err := buildPayload(profile, runtime.Call{CallType: "text", UserPrompt: "Answer."})
	if err != nil || protocol != "openai.responses" || path != "/agent" {
		t.Fatalf("Agent route: protocol=%q path=%q error=%v", protocol, path, err)
	}
	_, _, _, _, _, err = buildPayload(profile, runtime.Call{CallType: "text", ProviderOptions: map[string]any{"useResponsesApi": false}})
	if err == nil {
		t.Fatal("Perplexity Agent API accepted a legacy chat override")
	}
}
