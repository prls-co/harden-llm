package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-457

import (
	"context"
	"encoding/json"
	"errors"
	hardenllm "github.com/prls-co/harden-llm"
	"strings"
	"testing"
)

func TestRecoveryReasoningValidation(t *testing.T) {
	t.Parallel()
	catalog, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{Presets: catalog, Client: ClientOptions{Connections: []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: "https://cpa.prls.co/v1", APIKey: "fixture-provider-secret"}}, DefaultConnection: "cpa"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range [][]string{{"jsonRepair", "initial"}, {"jsonRepair", "escalation"}, {"rerun", "target"}, {"rerun", "jsonRepair", "initial"}, {"rerun", "jsonRepair", "escalation"}} {
		t.Run(strings.Join(path, "/"), func(t *testing.T) {
			var policy map[string]any
			if err := json.Unmarshal([]byte(`{"maxAttempts":6,"retryOn":[],"backoff":{"baseDelayMs":0,"maxDelayMs":0},"jsonRepair":{"initial":{"source":"generation"},"escalation":{"source":"generation"}},"rerun":{"target":{"source":"profile","profileId":"CPA gpt-6.1-sol"},"jsonRepair":{"initial":{"source":"generation"},"escalation":{"source":"generation"}}}}`), &policy); err != nil {
				t.Fatal(err)
			}
			node := policy
			for _, key := range path[:len(path)-1] {
				node = node[key].(map[string]any)
			}
			target := map[string]any{"source": "profile", "profileId": "CPA claude-sonnet-5-5", "reasoningEffort": "lowest"}
			node[path[len(path)-1]] = target
			raw, _ := json.Marshal(policy)
			input := RunInput{ProfileID: "CPA gpt-6.1-sol", UserPrompt: "fixture", CallType: hardenllm.CallTypeText, ReasoningEffort: "lowest", RecoveryPolicy: raw}
			_, err := service.resolveRun(context.Background(), strings.Repeat("a", 64), input)
			var invalid *ValidationError
			if !errors.As(err, &invalid) || len(invalid.FieldErrors) != 1 || invalid.FieldErrors[0].Field != "recoveryPolicy."+strings.Join(path, ".")+".reasoningEffort" {
				t.Fatalf("recovery validation did not identify its stage: %v", err)
			}
			delete(target, "reasoningEffort")
			input.RecoveryPolicy, _ = json.Marshal(policy)
			resolved, err := service.resolveRun(context.Background(), strings.Repeat("a", 64), input)
			if err != nil {
				t.Fatalf("unmapped reasoning must be omitted, not guessed: %v", err)
			}
			if err = resolved.request.RecoveryPolicy.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
