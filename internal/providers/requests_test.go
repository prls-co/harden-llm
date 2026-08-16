package providers

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"reflect"
	"testing"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/runtime"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-012

func TestProviderRequestParity(t *testing.T) {
	t.Parallel()
	router, err := NewRouter(Config{EndpointPolicy: EndpointPolicy{
		Resolver: staticResolver{
			"api.openai.com":                    {netip.MustParseAddr("104.18.7.192")},
			"generativelanguage.googleapis.com": {netip.MustParseAddr("142.250.72.234")},
			"api.anthropic.com":                 {netip.MustParseAddr("160.79.104.10")},
			"api.vendor.example":                {netip.MustParseAddr("93.184.216.34")},
		},
	}})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)
	baseCall := runtime.Call{
		SystemPrompt: "Be exact.", UserPrompt: "Answer.", CallType: "structured", Schema: schema,
		ReasoningEffort: "highest", ProviderOptions: map[string]any{
			"max_tokens": float64(42), "timeout": float64(5000), "maxRetries": float64(9),
			"tools": []any{map[string]any{"type": "function", "name": "lookup"}}, "provider_native": "preserve-where-supported",
		},
	}

	tests := []struct {
		name           string
		profile        runtime.Profile
		wantProtocol   string
		wantPath       string
		wantProvider   string
		assertPayload  func(*testing.T, map[string]any)
		assertPrepared func(*testing.T, preparedRequest)
	}{
		{
			name:         "OpenAI Responses",
			profile:      runtime.Profile{ID: "openai", Provider: "openai", APIInferenceType: "responses", BaseURL: "https://api.openai.com/v1", ModelID: "gpt-5.4", SupportsStructuredOutput: true, SupportsTemperature: false, ResponsesTokensParam: "max_output_tokens", ReasoningEffortMap: map[string]map[string]any{"highest": {"reasoning": map[string]any{"effort": "high"}}}},
			wantProtocol: "openai.responses", wantPath: "/responses", wantProvider: "openai",
			assertPayload: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if payload["model"] != "gpt-5.4" || payload["max_output_tokens"] != float64(42) {
					t.Fatalf("unexpected Responses payload: %#v", payload)
				}
				if _, ok := payload["max_tokens"]; ok {
					t.Fatal("max_tokens was not remapped")
				}
				if !reflect.DeepEqual(payload["reasoning"], map[string]any{"effort": "high"}) {
					t.Fatalf("reasoning was not mapped: %#v", payload["reasoning"])
				}
				if _, ok := payload["temperature"]; ok {
					t.Fatal("temperature leaked to unsupported model")
				}
				if _, ok := payload["tools"]; !ok || payload["provider_native"] != "preserve-where-supported" {
					t.Fatalf("Responses native options were dropped: %#v", payload)
				}
			},
		},
		{
			name:         "OpenAI Chat",
			profile:      runtime.Profile{ID: "chat", Provider: "openai", APIInferenceType: "chat-completions", BaseURL: "https://api.openai.com/v1", ModelID: "gpt-4.1", SupportsStructuredOutput: true, SupportsTemperature: true, TokensParam: "max_completion_tokens", ReasoningEffortMap: map[string]map[string]any{"highest": {}}},
			wantProtocol: "openai-compatible.chat.completions", wantPath: "/chat/completions", wantProvider: "openai",
			assertPayload: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if payload["max_completion_tokens"] != float64(42) || payload["temperature"] != 0.3 {
					t.Fatalf("unexpected Chat payload: %#v", payload)
				}
				if _, ok := payload["response_format"]; !ok {
					t.Fatal("structured response_format is missing")
				}
				if _, ok := payload["tools"]; !ok {
					t.Fatal("Chat tools were dropped")
				}
			},
		},
		{
			name:         "Gemini GenerateContent",
			profile:      runtime.Profile{ID: "gemini", Provider: "google", APIInferenceType: "gemini-generate-content", BaseURL: "https://generativelanguage.googleapis.com", ModelID: "models/gemini-2.5-flash", SupportsStructuredOutput: true, SupportsTemperature: true, ReasoningEffortMap: map[string]map[string]any{"highest": {}}},
			wantProtocol: "google.gemini.generateContent", wantPath: "/v1beta/models/gemini-2.5-flash:generateContent", wantProvider: "google",
			assertPayload: func(t *testing.T, payload map[string]any) {
				t.Helper()
				config := payload["generationConfig"].(map[string]any)
				if config["maxOutputTokens"] != float64(42) || config["response_mime_type"] != "application/json" {
					t.Fatalf("unexpected Gemini config: %#v", config)
				}
				if _, ok := payload["provider_native"]; ok {
					t.Fatal("unknown OpenAI-native option leaked into Gemini")
				}
			},
			assertPrepared: func(t *testing.T, request preparedRequest) {
				t.Helper()
				if request.headers.Get("X-Goog-Api-Key") != "test-secret" || request.url.RawQuery != "" {
					t.Fatalf("Gemini credential must be header-only: %s %#v", request.url.String(), request.headers)
				}
			},
		},
		{
			name:         "Anthropic Messages",
			profile:      runtime.Profile{ID: "anthropic", Provider: "anthropic", APIInferenceType: "anthropic-messages", BaseURL: "https://api.anthropic.com/v1", ModelID: "claude-sonnet-4-5", SupportsStructuredOutput: true, SupportsTemperature: true, ReasoningEffortMap: map[string]map[string]any{"highest": {}}},
			wantProtocol: "anthropic.messages", wantPath: "/messages", wantProvider: "anthropic",
			assertPayload: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if payload["max_tokens"] != float64(42) || payload["system"] != "Be exact." {
					t.Fatalf("unexpected Anthropic payload: %#v", payload)
				}
				if _, ok := payload["tools"]; !ok {
					t.Fatal("Anthropic-native tools were dropped")
				}
			},
			assertPrepared: func(t *testing.T, request preparedRequest) {
				t.Helper()
				if request.headers.Get("Anthropic-Version") != "2023-06-01" || request.headers.Get("X-Api-Key") != "test-secret" {
					t.Fatalf("Anthropic headers missing: %#v", request.headers)
				}
			},
		},
		{
			name:         "Generic OpenAI compatible",
			profile:      runtime.Profile{ID: "vendor", Provider: "vendor", APIInferenceType: "chat-completions", BaseURL: "https://api.vendor.example/v1", ModelID: "vendor/model", SupportsStructuredOutput: true, SupportsTemperature: false, DefaultOptions: map[string]any{"provider": map[string]any{"only": []any{"fast"}}}, ReasoningEffortMap: map[string]map[string]any{"highest": {}}},
			wantProtocol: "openai-compatible.chat.completions", wantPath: "/chat/completions", wantProvider: "vendor",
			assertPayload: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if _, ok := payload["temperature"]; ok {
					t.Fatal("temperature leaked to unsupported generic provider")
				}
				if _, ok := payload["provider"]; !ok {
					t.Fatalf("provider-native option was dropped: %#v", payload)
				}
				if payload["provider_native"] != "preserve-where-supported" {
					t.Fatalf("generic native option was dropped: %#v", payload)
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared, prepareErr := router.Prepare(context.Background(), test.profile, runtime.Credential{APIKey: "test-secret"}, baseCall)
			if prepareErr != nil {
				t.Fatalf("Prepare: %v", prepareErr)
			}
			if prepared.Operation.Protocol != test.wantProtocol || prepared.Operation.Endpoint.Path != test.wantPath || prepared.Operation.ResponseProjection.Provider != test.wantProvider {
				t.Fatalf("unexpected operation: %#v", prepared.Operation)
			}
			payload := prepared.Operation.Payload.(map[string]any)
			for _, runtimeKey := range []string{"timeout", "maxRetries", "reasoningEffort", "cacheMode"} {
				if _, ok := payload[runtimeKey]; ok {
					t.Fatalf("runtime option %q leaked into payload", runtimeKey)
				}
			}
			test.assertPayload(t, payload)
			request := prepared.Opaque.(preparedRequest)
			if request.url.User != nil || request.url.RawQuery != "" || request.headers.Get("Authorization") == "Bearer " {
				t.Fatalf("unsafe prepared request: %#v", request)
			}
			if test.assertPrepared != nil {
				test.assertPrepared(t, request)
			}
		})
	}
}

func TestProviderRequestParityCapturedSource(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../fixtures/parity/generated/provider-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name                string             `json:"name"`
			Operation           cachekey.Operation `json:"operation"`
			StructuredOperation cachekey.Operation `json:"structuredOperation"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{EndpointPolicy: EndpointPolicy{Resolver: staticResolver{
		"api.openai.com": {netip.MustParseAddr("104.18.7.192")}, "api.vendor.example": {netip.MustParseAddr("93.184.216.34")},
		"generativelanguage.googleapis.com": {netip.MustParseAddr("142.250.72.234")}, "api.anthropic.com": {netip.MustParseAddr("160.79.104.10")},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	request := runtime.Call{
		SystemPrompt: "Be exact.", UserPrompt: "Answer the deterministic fixture.", CallType: "text",
		ProviderOptions: map[string]any{"max_tokens": float64(42), "timeout": float64(5000), "maxRetries": float64(9)},
	}
	profiles := map[string]runtime.Profile{
		"openai-responses": {
			ID: "responses", Provider: "openai", APIInferenceType: "responses", BaseURL: "https://api.openai.com/v1", ModelID: "gpt-5.4",
			SupportsTemperature: false, SupportsStructuredOutput: true, ResponsesTokensParam: "max_output_tokens", DefaultOptions: map[string]any{"reasoning": map[string]any{"effort": "high"}},
		},
		"openai-chat": {
			ID: "chat", Provider: "openai", APIInferenceType: "responses", BaseURL: "https://api.openai.com/v1", ModelID: "gpt-4.1",
			SupportsTemperature: true, SupportsStructuredOutput: true, TokensParam: "max_completion_tokens", DefaultOptions: map[string]any{},
		},
		"generic-openai-compatible": {
			ID: "vendor", Provider: "vendor", APIInferenceType: "chat-completions", BaseURL: "https://api.vendor.example/v1", ModelID: "vendor/model",
			SupportsTemperature: false, SupportsStructuredOutput: true, DefaultOptions: map[string]any{"provider": map[string]any{"only": []any{"fast"}}},
		},
		"gemini-generate-content": {
			ID: "gemini", Provider: "google", APIInferenceType: "gemini-generate-content", BaseURL: "https://generativelanguage.googleapis.com", ModelID: "models/gemini-2.5-flash",
			SupportsTemperature: true, SupportsStructuredOutput: true, DefaultOptions: map[string]any{},
		},
		"anthropic-messages": {
			ID: "anthropic", Provider: "anthropic", APIInferenceType: "anthropic-messages", BaseURL: "https://api.anthropic.com/v1", ModelID: "claude-sonnet-4-5",
			SupportsTemperature: true, SupportsStructuredOutput: true, DefaultOptions: map[string]any{},
		},
	}
	for _, captured := range fixture.Cases {
		captured := captured
		t.Run(captured.Name, func(t *testing.T) {
			t.Parallel()
			call := request
			call.ProviderOptions = cloneMap(request.ProviderOptions)
			if captured.Name == "openai-chat" {
				call.ProviderOptions["useResponsesApi"] = false
			}
			prepared, prepareErr := router.Prepare(context.Background(), profiles[captured.Name], runtime.Credential{APIKey: "fixture-secret"}, call)
			if prepareErr != nil {
				t.Fatalf("Prepare: %v", prepareErr)
			}
			if !jsonEquivalent(prepared.Operation, captured.Operation) {
				got, _ := json.MarshalIndent(prepared.Operation, "", "  ")
				want, _ := json.MarshalIndent(captured.Operation, "", "  ")
				t.Fatalf("captured operation mismatch:\n got %s\nwant %s", got, want)
			}

			structuredCall := call
			structuredCall.CallType = "structured"
			structuredCall.Schema = json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"},"count":{"type":"number"}},"required":["answer","count"],"additionalProperties":false}`)
			structured, structuredErr := router.Prepare(context.Background(), profiles[captured.Name], runtime.Credential{APIKey: "fixture-secret"}, structuredCall)
			if structuredErr != nil {
				t.Fatalf("Prepare structured: %v", structuredErr)
			}
			if !jsonEquivalent(structured.Operation, captured.StructuredOperation) {
				got, _ := json.MarshalIndent(structured.Operation, "", "  ")
				want, _ := json.MarshalIndent(captured.StructuredOperation, "", "  ")
				t.Fatalf("captured structured operation mismatch:\n got %s\nwant %s", got, want)
			}
		})
	}
}

func TestCPAResponsesEvalRequestParityCapturedSource(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/parity/source/evals/cpa-gpt-5.4-mini-responses-call.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ProviderRequest struct {
			APIInferenceType  string         `json:"apiInferenceType"`
			BaseURL           string         `json:"baseUrl"`
			Body              map[string]any `json:"body"`
			ForbiddenBodyKeys []string       `json:"forbiddenBodyKeys"`
			Method            string         `json:"method"`
			Path              string         `json:"path"`
			Provider          string         `json:"provider"`
			URL               string         `json:"url"`
		} `json:"providerRequest"`
		UtilityCall struct {
			Arguments struct {
				ModelID      string `json:"modelId"`
				SystemPrompt string `json:"systemPrompt"`
				UserPrompt   string `json:"userPrompt"`
			} `json:"arguments"`
		} `json:"utilityCall"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{EndpointPolicy: EndpointPolicy{Resolver: staticResolver{
		"cpa.prls.co": {netip.MustParseAddr("93.184.216.34")},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	profile := runtime.Profile{
		ID: "cpa-eval", Provider: fixture.ProviderRequest.Provider,
		APIInferenceType: fixture.ProviderRequest.APIInferenceType,
		BaseURL:          fixture.ProviderRequest.BaseURL, ModelID: fixture.UtilityCall.Arguments.ModelID,
		SupportsTemperature: false, ResponsesTokensParam: "max_output_tokens",
		DefaultOptions: map[string]any{"max_output_tokens": float64(16000), "stream": true},
	}
	prepared, err := router.Prepare(context.Background(), profile, runtime.Credential{APIKey: "fixture-secret"}, runtime.Call{
		CallType: "text", SystemPrompt: fixture.UtilityCall.Arguments.SystemPrompt, UserPrompt: fixture.UtilityCall.Arguments.UserPrompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := prepared.Opaque.(preparedRequest)
	if prepared.Operation.Endpoint.Method != fixture.ProviderRequest.Method || prepared.Operation.Endpoint.Path != fixture.ProviderRequest.Path ||
		prepared.Operation.ResponseProjection.Provider != fixture.ProviderRequest.Provider || request.url.String() != fixture.ProviderRequest.URL {
		t.Fatalf("CPA request routing mismatch: operation=%#v url=%s", prepared.Operation, request.url)
	}
	if !jsonEquivalent(prepared.Operation.Payload, fixture.ProviderRequest.Body) {
		got, _ := json.MarshalIndent(prepared.Operation.Payload, "", "  ")
		want, _ := json.MarshalIndent(fixture.ProviderRequest.Body, "", "  ")
		t.Fatalf("CPA request body mismatch:\n got %s\nwant %s", got, want)
	}
	for _, key := range fixture.ProviderRequest.ForbiddenBodyKeys {
		if _, present := prepared.Operation.Payload.(map[string]any)[key]; present {
			t.Errorf("forbidden CPA request key %q is present", key)
		}
	}
	if request.headers.Get("Content-Type") != "application/json" || request.headers.Get("Authorization") != "Bearer fixture-secret" {
		t.Fatalf("CPA request headers mismatch: %#v", request.headers)
	}
}

func TestReasoningEffortParityCapturedSource(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../fixtures/parity/generated/reasoning-effort-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ContractedEfforts []string       `json:"contractedEfforts"`
		Mapped            map[string]any `json:"mapped"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture.ContractedEfforts, []string{"lowest", "middle", "highest"}) {
		t.Fatalf("captured reasoning contract changed: %#v", fixture.ContractedEfforts)
	}
	profile := runtime.Profile{
		ID: "fixture-model", APIInferenceType: "responses", DefaultOptions: map[string]any{},
		ReasoningEffortMap: map[string]map[string]any{"highest": {"reasoning": map[string]any{"effort": "high"}, "budget": float64(9)}},
	}
	options, err := mergedOptions(profile, runtime.Call{
		ReasoningEffort: "highest", ProviderOptions: map[string]any{"budget": float64(1), "providerOption": true},
	})
	if err != nil || !jsonEquivalent(options, fixture.Mapped) {
		t.Fatalf("mapped reasoning mismatch: %#v %v", options, err)
	}
	for name, call := range map[string]runtime.Call{
		"native-without-portable": {ProviderOptions: map[string]any{"thinking_budget": float64(10)}},
		"unsupported-alias":       {ReasoningEffort: "high", ProviderOptions: map[string]any{}},
	} {
		if _, err = mergedOptions(profile, call); err == nil {
			t.Fatalf("%s reasoning contract violation was accepted", name)
		}
	}
}

func TestProviderOptionEdgeParity(t *testing.T) {
	t.Parallel()
	profile := runtime.Profile{
		ID: "responses", APIInferenceType: "responses", ModelID: "gpt", ResponsesTokensParam: "max_output_tokens",
		DefaultOptions: map[string]any{}, ReasoningEffortMap: map[string]map[string]any{"highest": {}},
	}
	options, err := mergedOptions(profile, runtime.Call{ProviderOptions: map[string]any{"reasoning": nil}})
	if err != nil || options["reasoning"] != nil {
		t.Fatalf("nil native reasoning option should remain non-conflicting: %#v %v", options, err)
	}
	payload := buildResponsesPayload(profile, runtime.Call{}, map[string]any{
		"max_output_tokens": float64(9), "max_tokens": float64(8), "max_completion_tokens": float64(7),
	}, nil)
	for key, want := range map[string]any{"max_output_tokens": float64(9), "max_tokens": float64(8), "max_completion_tokens": float64(7)} {
		if payload[key] != want {
			t.Fatalf("Responses token option %q = %#v, want %#v; source retains legacy options when target exists", key, payload[key], want)
		}
	}

	gemini := runtime.Profile{
		ID: "gemini", APIInferenceType: "gemini-generate-content",
		DefaultOptions:     map[string]any{"thinkingConfig": map[string]any{"thinkingBudget": float64(128), "includeThoughts": true}},
		ReasoningEffortMap: map[string]map[string]any{"highest": {"thinkingConfig": map[string]any{"thinkingLevel": "HIGH"}}},
	}
	options, err = mergedOptions(gemini, runtime.Call{ReasoningEffort: "highest", ProviderOptions: map[string]any{}})
	if err != nil || !jsonEquivalent(options["thinkingConfig"], map[string]any{
		"thinkingBudget": float64(128), "thinkingLevel": "HIGH", "includeThoughts": true,
	}) {
		t.Fatalf("Gemini thinkingConfig merge mismatch: %#v %v", options, err)
	}

	maximum := positiveIntegerOption(map[string]any{"max_tokens": json.Number("42.9")}, "max_tokens")
	if maximum != float64(42) {
		t.Fatalf("Anthropic decimal max token truncation = %#v, want 42", maximum)
	}
}

func jsonEquivalent(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	var leftValue, rightValue any
	if json.Unmarshal(leftBytes, &leftValue) != nil || json.Unmarshal(rightBytes, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
