package providers

import (
	"context"
	"encoding/json"
	"net/netip"
	"sort"
	"strings"
	"testing"

	"github.com/prls-co/harden-llm/internal/profiles"
	"github.com/prls-co/harden-llm/internal/runtime"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-017

// TestDefaultProfileCatalogParity translates the source all-profile smoke
// setup into a deterministic provider-prepare matrix. It exercises every
// seeded profile and both text and structured operation construction without
// contacting a paid provider or requiring credentials.
func TestDefaultProfileCatalogParity(t *testing.T) {
	catalog, err := profiles.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	resolver := staticResolver{
		"api.anthropic.com":                 {netip.MustParseAddr("93.184.216.34")},
		"api.novita.ai":                     {netip.MustParseAddr("93.184.216.34")},
		"api.openai.com":                    {netip.MustParseAddr("93.184.216.34")},
		"api.perplexity.ai":                 {netip.MustParseAddr("93.184.216.34")},
		"cpa.prls.co":                       {netip.MustParseAddr("93.184.216.34")},
		"generativelanguage.googleapis.com": {netip.MustParseAddr("93.184.216.34")},
		"openrouter.ai":                     {netip.MustParseAddr("93.184.216.34")},
	}
	router, err := NewRouter(Config{EndpointPolicy: EndpointPolicy{Resolver: resolver}})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	schema := json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"}},"required":["status"],"additionalProperties":false}`)
	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			profile := catalog[name]
			reasoningEffort := ""
			if _, ok := profile.ReasoningEffortMap["lowest"]; ok {
				reasoningEffort = "lowest"
			}
			runtimeProfile := runtime.Profile{
				ID: name, Provider: profile.Provider, APIInferenceType: profile.APIInferenceType,
				CredentialScope: profile.EndpointCredentialScope, BaseURL: profile.BaseURL, ModelID: profile.ModelID,
				DefaultOptions: profile.DefaultOptions, ReasoningEffortMap: profile.ReasoningEffortMap,
				SupportsStructuredOutput: profile.SupportsContractedStructuredOutput,
				TokensParam:              profileStringValue(profile.TokensParam), ResponsesTokensParam: profileStringValue(profile.ResponsesTokensParam),
			}
			if profile.SupportsTemperature != nil {
				runtimeProfile.SupportsTemperature = *profile.SupportsTemperature
			}
			if profile.Pricing != nil {
				runtimeProfile.Pricing = runtime.Pricing{
					Input: profile.Pricing.Input, CacheRead: profile.Pricing.CacheRead, CacheCreation: profile.Pricing.CacheCreation,
					Output: profile.Pricing.Output, Reasoning: profile.Pricing.Reasoning,
				}
			}
			for _, callType := range []string{"text", "structured"} {
				prepared, err := router.Prepare(context.Background(), runtimeProfile, runtime.Credential{APIKey: "fixture-secret"}, runtime.Call{
					SystemPrompt: "Be exact.", UserPrompt: "Return the fixture result.", CallType: callType, Schema: schema,
					ReasoningEffort: reasoningEffort, ProviderOptions: map[string]any{"max_tokens": float64(32)},
				})
				if err != nil {
					t.Fatalf("prepare %s operation: %v", callType, err)
				}
				if prepared.Operation.Endpoint.Identity == "" || prepared.Operation.Protocol == "" {
					t.Fatalf("%s operation lacks endpoint/protocol: %#v", callType, prepared.Operation)
				}
				encoded, err := json.Marshal(prepared.Operation)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(encoded), "fixture-secret") {
					t.Fatalf("%s operation leaked the fixture credential: %s", callType, encoded)
				}
			}
		})
	}
}

func profileStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
