package providers

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-401
func TestProviderToolCallHistoryPreserved(t *testing.T) {
	t.Parallel()
	function := runtime.FunctionTool{
		Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
	}
	call := runtime.Call{
		ModelID: "native-model", CallType: "text", Tools: []runtime.FunctionTool{function},
		ToolChoice: runtime.ToolChoice{Mode: "auto"},
		Messages: []runtime.Message{
			{Role: "system", Content: json.RawMessage(`"Be helpful."`)},
			{Role: "user", Content: json.RawMessage(`"What is the weather?"`)},
			{Role: "assistant", Content: json.RawMessage(`""`), ToolCalls: []runtime.ToolCall{{
				ID: "call-1", Type: "function", Function: runtime.FunctionCall{Name: "lookup", Arguments: `{"city":"San Francisco"}`},
			}}},
			{Role: "tool", ToolCallID: "call-1", Content: json.RawMessage(`"{\"temperature\":21}"`)},
			{Role: "user", Content: json.RawMessage(`"Thanks."`)},
		},
	}
	tests := []struct {
		name       string
		connection runtime.Connection
	}{
		{name: "Responses", connection: runtime.Connection{Provider: "openai", APIInferenceType: "responses"}},
		{name: "Chat Completions", connection: runtime.Connection{Provider: "openai", APIInferenceType: "chat-completions"}},
		{name: "Gemini", connection: runtime.Connection{Provider: "google", APIInferenceType: "gemini-generate-content"}},
		{name: "Anthropic", connection: runtime.Connection{Provider: "anthropic", APIInferenceType: "anthropic-messages"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, _, _, payload, _, err := buildPayload(test.connection, call)
			if err != nil {
				t.Fatalf("buildPayload: %v", err)
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatal(err)
			}
			if !containsJSONValue(wire, "call-1") || !containsJSONValue(wire, "lookup") || !containsJSONValue(wire, "San Francisco") {
				t.Fatalf("function call identity or arguments were lost: %s", encoded)
			}
			if !containsJSONValue(wire, `{"temperature":21}`) || !containsJSONValue(wire, "Thanks.") {
				t.Fatalf("tool result or following user message was lost: %s", encoded)
			}
			switch test.connection.APIInferenceType {
			case "responses":
				input := wire["input"].([]any)
				if input[2].(map[string]any)["type"] != "function_call" || input[3].(map[string]any)["type"] != "function_call_output" {
					t.Fatalf("Responses function history order changed: %s", encoded)
				}
			case "chat-completions":
				messages := wire["messages"].([]any)
				assistant := messages[2].(map[string]any)
				tool := messages[3].(map[string]any)
				if !containsJSONValue(assistant, "call-1") || tool["tool_call_id"] != "call-1" {
					t.Fatalf("Chat function call linkage changed: %s", encoded)
				}
			case "gemini-generate-content":
				contents := wire["contents"].([]any)
				model := contents[1].(map[string]any)
				functionCall := model["parts"].([]any)[0].(map[string]any)["functionCall"].(map[string]any)
				response := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
				if model["role"] != "model" || functionCall["name"] != "lookup" || response["id"] != "call-1" || response["name"] != "lookup" {
					t.Fatalf("Gemini function call linkage changed: %s", encoded)
				}
				instruction := wire["system_instruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)
				if instruction["text"] != "Be helpful." {
					t.Fatalf("Gemini system instruction was lost: %s", encoded)
				}
			case "anthropic-messages":
				messages := wire["messages"].([]any)
				assistant := messages[1].(map[string]any)
				toolResult := messages[2].(map[string]any)
				use := assistant["content"].([]any)[0].(map[string]any)
				result := toolResult["content"].([]any)[0].(map[string]any)
				if use["type"] != "tool_use" || use["id"] != "call-1" || result["type"] != "tool_result" || result["tool_use_id"] != "call-1" {
					t.Fatalf("Anthropic function call linkage changed: %s", encoded)
				}
				if wire["system"].([]any)[0].(map[string]any)["text"] != "Be helpful." {
					t.Fatalf("Anthropic system instruction was lost: %s", encoded)
				}
			}
		})
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-401
func TestProviderOptionsAreMappedOrRejected(t *testing.T) {
	t.Parallel()
	gemini := runtime.Connection{Provider: "google", APIInferenceType: "gemini-generate-content"}
	call := runtime.Call{
		ModelID: "gemini-test", CallType: "text", Messages: []runtime.Message{{Role: "user", Content: json.RawMessage(`"hello"`)}},
		ProviderOptions: map[string]any{
			"max_tokens": float64(55), "stop": "END", "frequency_penalty": float64(0.2),
			"presence_penalty": float64(0.3), "seed": float64(17),
		},
	}
	_, _, _, payload, _, err := buildPayload(gemini, call)
	if err != nil {
		t.Fatalf("build Gemini payload: %v", err)
	}
	config := payload["generationConfig"].(map[string]any)
	want := map[string]any{
		"maxOutputTokens": float64(55), "stopSequences": []string{"END"},
		"frequencyPenalty": float64(0.2), "presencePenalty": float64(0.3), "seed": float64(17),
	}
	if !reflect.DeepEqual(config, want) {
		t.Fatalf("Gemini generation controls changed: got %#v want %#v", config, want)
	}

	unsupported := []struct {
		name       string
		connection runtime.Connection
		options    map[string]any
		tools      []runtime.FunctionTool
		choice     runtime.ToolChoice
	}{
		{name: "Responses penalty", connection: runtime.Connection{APIInferenceType: "responses"}, options: map[string]any{"frequency_penalty": float64(0.2)}},
		{name: "Chat Responses-only field", connection: runtime.Connection{APIInferenceType: "chat-completions"}, options: map[string]any{"include": []string{"reasoning.encrypted_content"}}},
		{name: "Gemini Responses-only field", connection: gemini, options: map[string]any{"parallel_tool_calls": true}},
		{name: "Anthropic seed", connection: runtime.Connection{APIInferenceType: "anthropic-messages"}, options: map[string]any{"seed": float64(17)}},
		{name: "unknown function selection", connection: gemini, tools: []runtime.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}, choice: runtime.ToolChoice{Mode: "function", Name: "missing"}},
	}
	for _, test := range unsupported {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			invalid := runtime.Call{
				ModelID: "native-model", CallType: "text", Messages: call.Messages,
				ProviderOptions: test.options, Tools: test.tools, ToolChoice: test.choice,
			}
			_, _, _, _, _, err := buildPayload(test.connection, invalid)
			var validation *retry.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("unsupported request was not rejected before dispatch: %v", err)
			}
		})
	}
}

func containsJSONValue(value any, expected string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == expected {
				return true
			}
			if containsJSONValue(child, expected) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsJSONValue(child, expected) {
				return true
			}
		}
	case string:
		return strings.Contains(typed, expected)
	}
	return false
}
