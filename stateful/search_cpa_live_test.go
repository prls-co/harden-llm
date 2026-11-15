//go:build live

package stateful_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-547

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"
	"unicode/utf8"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
)

// TestSearchCPALive qualifies the public CPA route through the actual composed
// library. It performs one model request, never prints output/credentials and
// never retries an unconfirmed request. Native Anthropic/Gemini qualification
// remains separate from this OpenAI/Codex route.
func TestSearchCPALive(t *testing.T) {
	qualifyCPASearch(t, cpaSearchProbe{"openai", "responses", "gpt-5.6-luna", "HLLM_CPA_SEARCH_CONFIRMED", "HLLM_CPA_SEARCH_RECEIPT"})
}

func TestSearchClaudeCPALive(t *testing.T) {
	qualifyCPASearch(t, cpaSearchProbe{"anthropic", "anthropic-messages", "claude-sonnet-5", "HLLM_CLAUDE_SEARCH_CONFIRMED", "HLLM_CLAUDE_SEARCH_RECEIPT"})
}

type cpaSearchProbe struct {
	provider, protocol, model, confirmation, receipt string
}

func qualifyCPASearch(t *testing.T, probe cpaSearchProbe) {
	t.Helper()
	if os.Getenv(probe.confirmation) != "yes" {
		t.Skip("explicit one-call CPA qualification required")
	}
	key := os.Getenv("CPA_API_KEY")
	if key == "" {
		t.Fatal("CPA_API_KEY is required")
	}
	client, err := stateful.NewClient(stateful.ClientOptions{
		Connections:       []hardenllm.Connection{{ID: "cpa", Provider: probe.provider, Protocol: probe.protocol, BaseURL: "https://cpa.prls.co/v1", APIKey: key, SupportsWebSearch: true}},
		DefaultConnection: "cpa",
	})
	if err != nil {
		t.Fatal("CPA client construction failed")
	}
	policy := hardenllm.DefaultRecoveryPolicy()
	policy.MaxAttempts = 1
	policy.RetryOn = []hardenllm.RecoveryCategory{}
	policy.JSONRepair = nil
	policy.Rerun = nil
	if err := policy.Validate(); err != nil {
		t.Fatal("one-attempt qualification policy is invalid; no call made")
	}
	effort := hardenllm.ReasoningEffortLowest
	if probe.provider == "anthropic" {
		effort = ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	started := time.Now()
	result, err := client.Call(ctx, hardenllm.Request{
		ModelID: probe.model, CallType: hardenllm.CallTypeText,
		Messages:  []hardenllm.Message{{Role: "user", Content: json.RawMessage(`"Search the web for the official Go programming language documentation. Briefly describe it and cite the source."`)}},
		WebSearch: true, ToolChoice: hardenllm.ToolChoice{Mode: "required"}, ReasoningEffort: effort,
		CacheMode: hardenllm.CacheModeOff, RecoveryPolicy: policy,
	})
	if err != nil {
		t.Fatal("CPA qualification failed; outcome may be unconfirmed, do not replay automatically")
	}
	if len(result.Attempts) != 1 || result.Cache.Served || result.Search == nil || !result.Search.Executed || len(result.Search.Sources) == 0 || len(result.Search.Citations) == 0 {
		t.Fatal("completed CPA call did not supply qualified search evidence")
	}
	text := result.AssistantText
	sum := sha256.Sum256([]byte(text))
	if hex.EncodeToString(sum[:]) != result.Search.CitationTextSHA256 {
		t.Fatal("citation text hash mismatch")
	}
	for _, c := range result.Search.Citations {
		if c.StartIndex < 0 || c.EndIndex <= c.StartIndex || c.EndIndex > utf8.RuneCountInString(text) {
			t.Fatal("citation range is invalid")
		}
	}
	report := map[string]any{
		"test": "TEST-547", "status": "pass", "observedAt": time.Now().UTC().Format(time.RFC3339),
		"route": "https://cpa.prls.co/v1", "provider": probe.provider, "protocol": probe.protocol, "model": probe.model, "reasoningEffort": string(effort),
		"attempts": len(result.Attempts), "cacheServed": result.Cache.Served, "durationMs": time.Since(started).Milliseconds(),
		"sources": len(result.Search.Sources), "citations": len(result.Search.Citations), "calls": len(result.Search.Calls),
		"incomplete": result.Search.Incomplete, "warnings": result.Search.Warnings, "citationHashVerified": true,
	}
	encoded, _ := json.Marshal(report)
	t.Log(string(encoded))
	if path := os.Getenv(probe.receipt); path != "" {
		if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
			t.Fatal("could not retain metadata-only qualification receipt")
		}
	}
}
