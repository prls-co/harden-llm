package profiles

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-017

func TestDefaultCatalogParity(t *testing.T) {
	catalog, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]struct {
		provider  string
		inference string
		baseURL   string
		modelID   string
	}{
		"Claude Haiku 4.5":               {"anthropic", "anthropic-messages", "https://api.anthropic.com/v1", "claude-haiku-4-5-20251001"},
		"Claude Opus 4.7":                {"anthropic", "anthropic-messages", "https://api.anthropic.com/v1", "claude-opus-4-7"},
		"Claude Opus 5":                  {"anthropic", "anthropic-messages", "https://api.anthropic.com/v1", "claude-opus-5"},
		"Claude Sonnet 4.6":              {"anthropic", "anthropic-messages", "https://api.anthropic.com/v1", "claude-sonnet-4-6"},
		"Claude Sonnet 5":                {"anthropic", "anthropic-messages", "https://api.anthropic.com/v1", "claude-sonnet-5"},
		"CPA GPT-5.4":                    {"cpa", "responses", "https://cpa.prls.co/v1", "gpt-5.4"},
		"CPA GPT-5.4 Mini":               {"cpa", "responses", "https://cpa.prls.co/v1", "gpt-5.4-mini"},
		"CPA GPT-5.6 Luna":               {"cpa", "responses", "https://cpa.prls.co/v1", "gpt-5.6-luna"},
		"CPA GPT-5.6 Sol":                {"cpa", "responses", "https://cpa.prls.co/v1", "gpt-5.6-sol"},
		"Gemini 3.1 Flash Lite":          {"google", "gemini-generate-content", "https://generativelanguage.googleapis.com", "gemini-3.1-flash-lite"},
		"Gemini 3.1 Pro Preview":         {"google", "gemini-generate-content", "https://generativelanguage.googleapis.com", "gemini-3.1-pro-preview"},
		"Gemini 3.5 Flash":               {"google", "gemini-generate-content", "https://generativelanguage.googleapis.com", "gemini-3.5-flash"},
		"Gemini 3.5 Flash Lite":          {"google", "gemini-generate-content", "https://generativelanguage.googleapis.com", "gemini-3.5-flash-lite"},
		"Gemini 3.7 Flash":               {"google", "gemini-generate-content", "https://generativelanguage.googleapis.com", "gemini-3.7-flash"},
		"Novita DeepSeek V4 Flash":       {"novita", "chat-completions", "https://api.novita.ai/openai/v1", "deepseek/deepseek-v4-flash"},
		"Novita DeepSeek V4 Pro":         {"novita", "chat-completions", "https://api.novita.ai/openai/v1", "deepseek/deepseek-v4-pro"},
		"OpenAI GPT-5.4":                 {"openai", "responses", "https://api.openai.com/v1", "gpt-5.4"},
		"OpenAI GPT-5.4 Mini":            {"openai", "responses", "https://api.openai.com/v1", "gpt-5.4-mini"},
		"OpenAI GPT-5.5":                 {"openai", "responses", "https://api.openai.com/v1", "gpt-5.5"},
		"OpenAI GPT-5.6 Luna":            {"openai", "responses", "https://api.openai.com/v1", "gpt-5.6-luna"},
		"OpenAI GPT-5.6 Sol":             {"openai", "responses", "https://api.openai.com/v1", "gpt-5.6-sol"},
		"OpenRouter DeepSeek V4 Flash":   {"openrouter", "chat-completions", "https://openrouter.ai/api/v1", "deepseek/deepseek-v4-flash"},
		"OpenRouter DeepSeek V4 Pro":     {"openrouter", "chat-completions", "https://openrouter.ai/api/v1", "deepseek/deepseek-v4-pro"},
		"OpenRouter GPT-OSS 120B":        {"openrouter", "chat-completions", "https://openrouter.ai/api/v1", "openai/gpt-oss-120b"},
		"OpenRouter GPT-OSS 20B":         {"openrouter", "chat-completions", "https://openrouter.ai/api/v1", "openai/gpt-oss-20b"},
		"Perplexity Sonar":               {"perplexity", "chat-completions", "https://api.perplexity.ai", "sonar"},
		"Perplexity Sonar Pro":           {"perplexity", "chat-completions", "https://api.perplexity.ai", "sonar-pro"},
		"Perplexity Sonar Reasoning Pro": {"perplexity", "chat-completions", "https://api.perplexity.ai", "sonar-reasoning-pro"},
	}

	gotNames := make([]string, 0, len(catalog))
	for name := range catalog {
		gotNames = append(gotNames, name)
	}
	slices.Sort(gotNames)
	wantNames := make([]string, 0, len(want))
	for name := range want {
		wantNames = append(wantNames, name)
	}
	slices.Sort(wantNames)
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("default profile names = %v, want %v", gotNames, wantNames)
	}
	if len(catalog) != 28 {
		t.Fatalf("default profile count = %d, want 28", len(catalog))
	}

	for _, name := range gotNames {
		name := name
		t.Run(name, func(t *testing.T) {
			profile := catalog[name]
			expected := want[name]
			if profile.LLMProfile != name || profile.Provider != expected.provider ||
				profile.APIInferenceType != expected.inference || profile.BaseURL != expected.baseURL || profile.ModelID != expected.modelID {
				t.Fatalf("profile transport = %#v, want provider=%q inference=%q base=%q model=%q", profile, expected.provider, expected.inference, expected.baseURL, expected.modelID)
			}
			if profile.EndpointCredentialScope != "global" || !profile.SupportsContractedStructuredOutput {
				t.Fatalf("profile security/capability defaults = scope %q structured=%t", profile.EndpointCredentialScope, profile.SupportsContractedStructuredOutput)
			}
			if len(profile.BackupProfiles) != 0 || len(profile.Models) != 0 {
				t.Fatalf("seeded runtime state was copied into %s: backups=%v models=%v", name, profile.BackupProfiles, profile.Models)
			}
			if fmt.Sprint(profile.DefaultOptions["max_tokens"]) != "16000" {
				t.Fatalf("%s max_tokens = %#v, want 16000", name, profile.DefaultOptions["max_tokens"])
			}
			if expected.provider == "cpa" && profile.DefaultOptions["stream"] != true {
				t.Fatalf("%s must preserve CPA streaming default", name)
			}
			if expected.provider == "openrouter" {
				if profile.Pricing != nil {
					t.Fatal("OpenRouter pricing must remain provider-reported")
				}
			} else {
				assertCompletePricing(t, name, profile.Pricing)
			}
			if profile.Provider == "cpa" || profile.Provider == "openai" || profile.Provider == "google" || profile.Provider == "novita" || profile.Provider == "openrouter" {
				if len(profile.ReasoningEffortMap) != 3 {
					t.Fatalf("%s reasoning levels = %d, want lowest/middle/highest", name, len(profile.ReasoningEffortMap))
				}
			}
			encoded, err := json.Marshal(profile)
			if err != nil {
				t.Fatal(err)
			}
			lower := strings.ToLower(string(encoded))
			for _, forbidden := range []string{"apikey", "authorization", "secret", "ciphertext", "encrypted"} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("seed profile contains credential-shaped field %q: %s", forbidden, encoded)
				}
			}
		})
	}

	encoded, err := MarshalCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCatalog(encoded); err != nil {
		t.Fatalf("default catalog round trip: %v", err)
	}
}

func assertCompletePricing(t *testing.T, name string, pricing *Pricing) {
	t.Helper()
	if pricing == nil {
		t.Fatalf("%s must contain a pricing snapshot", name)
	}
	for field, value := range map[string]*float64{
		"input": pricing.Input, "cacheRead": pricing.CacheRead, "cacheCreation": pricing.CacheCreation,
		"output": pricing.Output, "reasoning": pricing.Reasoning,
	} {
		if value == nil || *value < 0 {
			t.Fatalf("%s pricing.%s = %v, want a non-negative number", name, field, value)
		}
	}
}
