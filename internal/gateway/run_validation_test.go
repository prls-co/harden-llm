package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-012

import (
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
)

func TestValidateRunInputAllowsProviderTokenLimitOptions(t *testing.T) {
	for _, key := range []string{"max_tokens", "max_output_tokens", "max_completion_tokens", "output_tokens"} {
		t.Run(key, func(t *testing.T) {
			err := validateRunInput(RunInput{
				ProfileID:       "CPA GPT-5.6 Luna",
				UserPrompt:      "tell me a joke",
				CallType:        hardenllm.CallTypeText,
				ProviderOptions: map[string]any{key: 16000},
			})
			if err != nil {
				t.Fatalf("provider request option %q rejected: %v", key, err)
			}
		})
	}
}

func TestValidateRunInputRejectsCredentialProviderOptions(t *testing.T) {
	for _, key := range []string{"api_key", "authorization", "credential_id", "password", "secret", "token", "bearer_token"} {
		t.Run(key, func(t *testing.T) {
			err := validateRunInput(RunInput{
				ProfileID:       "CPA GPT-5.6 Luna",
				UserPrompt:      "tell me a joke",
				CallType:        hardenllm.CallTypeText,
				ProviderOptions: map[string]any{key: "must-not-cross-run-boundary"},
			})
			if err == nil {
				t.Fatalf("credential-shaped provider option %q was accepted", key)
			}
		})
	}
}
