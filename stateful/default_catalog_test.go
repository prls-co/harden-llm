package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-452 TEST-549
// Preserves the historical all-profile prepare oracle (TEST-017) at its new owner.

import (
	"context"
	"encoding/json"
	"math"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/runtime"
	"github.com/prls-co/harden-llm/internal/schema"
)

type catalogResolver struct{}

func (catalogResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

func TestDefaultProfileCatalogParity(t *testing.T) {
	t.Parallel()
	catalog, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	connections := map[string]hardenllm.Connection{}
	for _, preset := range catalog {
		profile, err := parseDefinition(preset.Definition)
		if err != nil {
			t.Fatal(err)
		}
		connections[preset.Binding.DeploymentUpstreamID] = hardenllm.Connection{
			ID: preset.Binding.DeploymentUpstreamID, Provider: profile.Provider,
			Protocol: profile.APIInferenceType, BaseURL: profile.BaseURL, APIKey: "fixture-secret",
		}
	}
	options := ClientOptions{DefaultConnection: "cpa", EndpointPolicy: hardenllm.EndpointPolicy{Resolver: catalogResolver{}}}
	for _, c := range connections {
		options.Connections = append(options.Connections, c)
	}
	service, err := NewService(Config{Client: options, Presets: catalog})
	if err != nil {
		t.Fatal(err)
	}
	for name, preset := range catalog {
		t.Run(name, func(t *testing.T) {
			var inputs profileInputs
			if err := json.Unmarshal(preset.Inputs, &inputs); err != nil {
				t.Fatal(err)
			}
			// Use the shipped schema rather than an independently valid test schema.
			normalized, err := schema.Normalize(inputs.Schema)
			if err != nil {
				t.Fatalf("shipped structured schema: %v", err)
			}
			for _, callType := range []hardenllm.CallType{hardenllm.CallTypeText, hardenllm.CallTypeStructured} {
				resolved, err := service.resolveRun(context.Background(), strings.Repeat("a", 64), RunInput{
					ProfileID: name, UserPrompt: "Return the configured fixture.", CallType: callType,
					Schema: normalized, ReasoningEffort: inputs.ReasoningEffort,
					ProviderOptions: inputs.ProviderOptions, RecoveryPolicy: json.RawMessage(`{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`),
				})
				if err != nil {
					t.Fatalf("resolve %s: %v", callType, err)
				}
				c := resolved.connections["generation"]
				prepared, err := service.effects.Executor.Prepare(context.Background(), hardenllm.ProviderConnection{
					ID: c.ID, Provider: c.Provider, APIInferenceType: c.Protocol, BaseURL: c.BaseURL,
				}, hardenllm.ProviderCredential{APIKey: c.APIKey}, hardenllm.ProviderCall{
					ModelID: resolved.request.ModelID, Messages: []runtime.Message{{Role: "user", Content: json.RawMessage(`"Return the configured fixture."`)}},
					CallType: string(callType), Schema: normalized, ProviderOptions: resolved.request.ProviderOptions,
				})
				if err != nil {
					t.Fatalf("prepare %s: %v", callType, err)
				}
				if prepared.Operation.Endpoint.Identity == "" || prepared.Operation.Protocol == "" {
					t.Fatalf("%s lacks endpoint/protocol", callType)
				}
				if c.Protocol == "gemini-generate-content" && prepared.Operation.Endpoint.Path != "/v1beta/models/"+resolved.request.ModelID+":generateContent" {
					t.Fatalf("Gemini model path: %s", prepared.Operation.Endpoint.Path)
				}
				if c.Provider == "perplexity" && prepared.Operation.Endpoint.Path != "/agent" {
					t.Fatalf("Perplexity Agent path: %s", prepared.Operation.Endpoint.Path)
				}
				payload := prepared.Operation.Payload.(map[string]any)
				if c.Provider == "perplexity" {
					if payload["model"] != resolved.request.ModelID || payload["max_output_tokens"] == nil {
						t.Fatal("Perplexity Agent model/output limit missing")
					}
					for _, key := range []string{"messages", "disable_search", "enable_search_classifier", "max_tokens"} {
						if _, exists := payload[key]; exists {
							t.Fatalf("Agent retained %s", key)
						}
					}
				}
				if name == "OpenRouter DeepSeek V4 Pro" && !sameJSON(mustJSON(t, payload["provider"]), []byte(`{"order":["deepinfra/fp8"],"allow_fallbacks":false}`)) {
					t.Fatal("DeepInfra routing changed or fallback enabled")
				}
				encoded, err := json.Marshal(prepared.Operation)
				if err != nil || strings.Contains(string(encoded), "fixture-secret") {
					t.Fatal("canonical operation is invalid or exposes credential")
				}
			}
		})
	}
}

// TEST-452 also preserves Query Set TEST-159's exact shipped-model oracle.
// This is an optional preset, not a seeded profile or a consumer default.
func TestCPALunaPresetPreservesModelSchemaAndReasoning(t *testing.T) {
	t.Parallel()
	catalog, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	preset, exists := catalog["CPA GPT-5.6 Luna"]
	if !exists {
		t.Fatal("the shipped CPA Luna preset is missing")
	}
	profile, err := parseDefinition(preset.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ModelID != "gpt-5.6-luna" || !profile.SupportsContractedStructuredOutput {
		t.Fatal("CPA Luna model identity or contracted structured output changed")
	}
	for _, test := range []struct{ level, providerEffort string }{
		{level: "lowest", providerEffort: "low"},
		{level: "highest", providerEffort: "xhigh"},
	} {
		t.Run(test.level, func(t *testing.T) {
			options, err := profileOptions(profile, test.level, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := mustJSON(t, map[string]string{"effort": test.providerEffort})
			if !sameJSON(mustJSON(t, options["reasoning"]), want) {
				t.Fatalf("%s reasoning no longer maps to provider effort %s", test.level, test.providerEffort)
			}
		})
	}
}

func TestIssue184CatalogSearchAndPricingCorrections(t *testing.T) {
	t.Parallel()
	catalog, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := catalog["CPA gpt-5.5"]; exists {
		t.Fatal("unsupported CPA gpt-5.5 starting preset must be removed until upstream restores that exact model")
	}
	// Saved immutable setups referencing gpt-5.5 remain valid.
	savedSnapshot := SaveProfileInput{
		Name:       "Saved GPT-5.5",
		PresetID:   "CPA gpt-5.5",
		Definition: json.RawMessage(`{"schemaVersion":4,"provider":"cpa","apiInferenceType":"responses","baseUrl":"https://cpa.prls.co/v1","modelId":"gpt-5.5","pricing":{"input_cost_per_token":5e-06,"output_cost_per_token":3e-05,"cache_read_input_token_cost":5e-07},"supportsTemperature":false,"supportsContractedStructuredOutput":true,"tokensParam":null,"responsesTokensParam":"max_output_tokens","defaultOptions":{"max_tokens":16000,"stream":true},"supportsWebSearch":true,"recoveryPolicy":{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}}`),
		Inputs:     json.RawMessage(`{"modelId":"gpt-5.5","callType":"text","userPrompt":"ok"}`),
		Binding:    ProfileCredentialBinding{Source: "deployment", DeploymentUpstreamID: "cpa"},
	}
	if _, err := validateSnapshot(savedSnapshot); err != nil {
		t.Fatalf("saved immutable setup with gpt-5.5 was rejected: %v", err)
	}

	haikuPreset, exists := catalog["CPA claude-haiku-4-5-20251001"]
	if !exists {
		t.Fatal("CPA claude-haiku-4-5-20251001 preset is missing")
	}
	haikuProfile, err := parseDefinition(haikuPreset.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if haikuProfile.Provider != "cpa" || haikuProfile.APIInferenceType != "anthropic-messages" || haikuProfile.BaseURL != "https://cpa.prls.co/v1" || haikuProfile.ModelID != "claude-haiku-4-5-20251001" || haikuProfile.ResponsesTokensParam != nil || haikuPreset.Binding.DeploymentUpstreamID != "cpa" {
		t.Fatalf("Haiku preset definition or binding mismatch: %+v binding=%+v", haikuProfile, haikuPreset.Binding)
	}
	if _, hasStream := haikuProfile.DefaultOptions["stream"]; hasStream {
		t.Fatalf("Haiku preset retained Responses stream option: %#v", haikuProfile.DefaultOptions)
	}
	service, err := NewService(Config{
		Presets: Catalog{"CPA claude-haiku-4-5-20251001": haikuPreset},
		Client: ClientOptions{
			Connections:       []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: "https://cpa.prls.co/v1", APIKey: "fixture-cpa-secret"}},
			DefaultConnection: "cpa",
			EndpointPolicy:    hardenllm.EndpointPolicy{Resolver: catalogResolver{}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var haikuInputs profileInputs
	if err := json.Unmarshal(haikuPreset.Inputs, &haikuInputs); err != nil {
		t.Fatal(err)
	}
	normalizedSchema, err := schema.Normalize(haikuInputs.Schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, callType := range []hardenllm.CallType{hardenllm.CallTypeText, hardenllm.CallTypeStructured} {
		resolved, err := service.resolveRun(context.Background(), strings.Repeat("a", 64), RunInput{
			ProfileID: "CPA claude-haiku-4-5-20251001", UserPrompt: "Search for Go docs", CallType: callType,
			Schema: normalizedSchema, WebSearch: true,
			RecoveryPolicy: json.RawMessage(`{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`),
		})
		if err != nil {
			t.Fatalf("resolve Haiku %s: %v", callType, err)
		}
		conn := resolved.connections["generation"]
		if conn.Provider != "cpa" || conn.Protocol != "anthropic-messages" || admissionOrigin(conn.BaseURL) != "https://cpa.prls.co" {
			t.Fatalf("Haiku connection lost CPA label or admission origin: %+v", conn)
		}
		prepared, err := service.effects.Executor.Prepare(context.Background(), hardenllm.ProviderConnection{
			ID: conn.ID, Provider: conn.Provider, APIInferenceType: conn.Protocol, BaseURL: conn.BaseURL, SupportsWebSearch: conn.SupportsWebSearch,
		}, hardenllm.ProviderCredential{APIKey: conn.APIKey}, hardenllm.ProviderCall{
			ModelID: resolved.request.ModelID, Messages: []runtime.Message{{Role: "user", Content: json.RawMessage(`"Search for Go docs"`)}},
			CallType: string(callType), Schema: normalizedSchema, WebSearch: true, ToolChoice: runtime.ToolChoice{Mode: "required"},
			ProviderOptions: resolved.request.ProviderOptions,
		})
		if err != nil {
			t.Fatalf("prepare Haiku %s: %v", callType, err)
		}
		if prepared.Operation.ResponseProjection.Provider != "cpa" || prepared.Operation.Protocol != "anthropic.messages" || prepared.Operation.Endpoint.Path != "/messages" {
			t.Fatalf("Haiku prepared operation lost CPA provider label or Messages protocol: %+v", prepared.Operation)
		}
		payload := prepared.Operation.Payload.(map[string]any)
		if callType == hardenllm.CallTypeText {
			if !reflect.DeepEqual(payload["tool_choice"], map[string]any{"type": "auto"}) {
				t.Fatalf("Haiku text search did not select optional native auto tool_choice: %#v", payload)
			}
		} else {
			marker, ok := payload["__harden_llm_web_search"].(map[string]any)
			if !ok || marker["mode"] != "jina" {
				t.Fatalf("Haiku structured search did not select Jina fallback: %#v", payload)
			}
			format := payload["output_config"].(map[string]any)["format"].(map[string]any)
			if format["type"] != "json_schema" || format["schema"] == nil {
				t.Fatalf("Haiku structured search lost native output_config.format schema: %#v", payload["output_config"])
			}
		}
	}

	for _, name := range []string{"Gemini 3.1 Flash Lite", "Gemini 3.1 Pro Preview", "Gemini 3.5 Flash", "Gemini 3.5 Flash Lite", "Gemini 3.7 Flash"} {
		geminiPreset, exists := catalog[name]
		if !exists {
			t.Fatalf("%s preset is missing", name)
		}
		geminiProfile, err := parseDefinition(geminiPreset.Definition)
		if err != nil {
			t.Fatal(err)
		}
		if geminiProfile.Pricing == nil || geminiProfile.Pricing.Output == nil || geminiProfile.Pricing.Reasoning == nil || *geminiProfile.Pricing.Reasoning <= 0 || *geminiProfile.Pricing.Reasoning != *geminiProfile.Pricing.Output {
			t.Fatalf("%s reasoning token rate must match documented output token rate: %+v", name, geminiProfile.Pricing)
		}
	}
	gemini37, _ := parseDefinition(catalog["Gemini 3.7 Flash"].Definition)
	usage, err := accounting.CompleteUsage(1000, 0, 0, 200, 400)
	if err != nil {
		t.Fatal(err)
	}
	cost, err := accounting.ResolveCost(usage, accounting.Pricing{
		Input: gemini37.Pricing.Input, CacheRead: gemini37.Pricing.CacheRead,
		CacheCreation: gemini37.Pricing.CacheCreation, Output: gemini37.Pricing.Output,
		Reasoning: gemini37.Pricing.Reasoning,
	}, nil)
	if err != nil || cost.Status != accounting.CostExact || math.Abs(cost.KnownSubtotalUSD-0.003) > 1e-12 {
		t.Fatalf("Gemini 3.7 Flash reasoning cost arithmetic mismatch: cost=%+v err=%v", cost, err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
