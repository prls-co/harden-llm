package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-452
// Preserves the historical all-profile prepare oracle (TEST-017) at its new owner.

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
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

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
