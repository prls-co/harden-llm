package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-012 TEST-220

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/profiles"
)

func TestValidateRunInputAllowsProviderTokenLimitOptions(t *testing.T) {
	for _, key := range []string{"max_tokens", "max_output_tokens", "max_completion_tokens", "output_tokens"} {
		t.Run(key, func(t *testing.T) {
			err := validateRunInput(RunInput{RecoveryPolicy: hardenllm.DefaultRecoveryPolicy(),
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

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-207
func TestRecoveryContractImports(t *testing.T) {
	data, err := os.ReadFile("../../config/llm-profiles.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var example struct {
		Profiles profiles.Catalog `json:"profiles"`
	}
	if err := json.Unmarshal(data, &example); err != nil {
		t.Fatal(err)
	}
	if err := profiles.ValidateCatalog(example.Profiles); err != nil {
		t.Fatalf("current configuration example: %v", err)
	}
	// Retired bundle formats fail before any vault, provider, or store is needed.
	service := &ProfileService{}
	for _, version := range []int{1, 2} {
		_, err = service.ReplaceBundle(context.Background(), "owner", ProfileBundle{
			SchemaVersion: version, BundleID: "retired", CreatedAt: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC),
			Profiles: profiles.Catalog{}, CredentialIDs: map[string]string{},
		})
		var invalid *profiles.ValidationError
		if !errors.As(err, &invalid) || len(invalid.FieldErrors) != 1 || invalid.FieldErrors[0].Field != "schemaVersion" || !strings.Contains(invalid.FieldErrors[0].Message, "3") {
			t.Fatalf("bundle schema %d must identify the current format, got %v", version, err)
		}
	}

	vault, err := profiles.NewCredentialVault("test-key", map[string][]byte{"test-key": make([]byte, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.vault = vault
	legacyProfile := profiles.Profile{
		SchemaVersion: 2, LLMProfile: "Legacy", Provider: "openai", APIInferenceType: "responses",
		EndpointCredentialScope: "global", BaseURL: "https://api.openai.com/v1", ModelID: "gpt-test",
		Pricing: &profiles.Pricing{}, SupportsTemperature: new(false), SupportsContractedStructuredOutput: true,
		DefaultOptions: map[string]any{}, RecoveryPolicy: hardenllm.DefaultRecoveryPolicy(),
	}
	_, err = service.ReplaceBundle(context.Background(), "owner", ProfileBundle{
		SchemaVersion: 3, BundleID: "current-bundle", CreatedAt: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC),
		Profiles: profiles.Catalog{"Legacy": legacyProfile}, CredentialIDs: map[string]string{},
	})
	var invalid *profiles.ValidationError
	if !errors.As(err, &invalid) || len(invalid.FieldErrors) != 1 || invalid.FieldErrors[0].Field != "Legacy.schemaVersion" {
		t.Fatalf("v3 bundle must reject a v2 profile before credential validation, got %v", err)
	}
	catalog, err := profiles.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for id, profile := range catalog {
		profile.SchemaVersion = 1
		if err := profiles.ValidateCatalog(profiles.Catalog{id: profile}); err == nil {
			t.Fatal("retired profile accepted")
		}
		break
	}
}

func TestValidateRunInputRejectsCredentialProviderOptions(t *testing.T) {
	for _, key := range []string{"api_key", "authorization", "credential_id", "password", "secret", "token", "bearer_token"} {
		t.Run(key, func(t *testing.T) {
			err := validateRunInput(RunInput{RecoveryPolicy: hardenllm.DefaultRecoveryPolicy(),
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

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-207
func TestRecoveryContractInput(t *testing.T) {
	for _, test := range []struct {
		name, policy string
		invalid      bool
	}{
		{"complete", `{"maxAttempts":4,"retryOn":["network"],"jsonRepair":{"initial":{"source":"generation"},"escalation":{"source":"generation"}},"rerun":null,"backoff":{"baseDelayMs":500,"maxDelayMs":8000}}`, false},
		{"false empty zero", `{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, false},
		{"retired repair boolean", `{"maxAttempts":1,"retryOn":[],"repairInvalidOutput":false,"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, true},
		{"missing", "", true}, {"null", "null", true}, {"partial", `{"maxAttempts":4}`, true},
		{"unknown category", `{"maxAttempts":4,"retryOn":["parse_error"],"jsonRepair":{"initial":{"source":"generation"},"escalation":{"source":"generation"}},"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, true},
		{"duplicate category", `{"maxAttempts":4,"retryOn":["network","network"],"jsonRepair":{"initial":{"source":"generation"},"escalation":{"source":"generation"}},"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, true},
		{"missing false", `{"maxAttempts":4,"retryOn":[],"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, true},
		{"missing zero", `{"maxAttempts":4,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"maxDelayMs":0}}`, true},
		{"negative delay", `{"maxAttempts":4,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":-1,"maxDelayMs":0}}`, true},
		{"inverted delays", `{"maxAttempts":4,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":2,"maxDelayMs":1}}`, true},
		{"zero budget", `{"maxAttempts":0,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, true},
		{"excess budget", `{"maxAttempts":11,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := `{"profileId":"fixture","userPrompt":"fixture","callType":"text"`
			if test.policy != "" {
				raw += `,"recoveryPolicy":` + test.policy
			}
			raw += "}"
			var input RunInput
			decoder := json.NewDecoder(strings.NewReader(raw))
			decoder.DisallowUnknownFields()
			err := decoder.Decode(&input)
			if err == nil {
				err = validateRunInput(input)
			}
			if (err != nil) != test.invalid {
				t.Fatalf("invalid=%t error=%v", test.invalid, err)
			}
			if !test.invalid {
				encoded, err := json.Marshal(input)
				if err != nil {
					t.Fatalf("marshal normalized input: %v", err)
				}
				var wire map[string]any
				if err := json.Unmarshal(encoded, &wire); err != nil {
					t.Fatalf("decode normalized input: %v", err)
				}
				policy, ok := wire["recoveryPolicy"].(map[string]any)
				if !ok {
					t.Fatalf("normalized recovery policy is not an object: %s", encoded)
				}
				if _, legacy := policy["repairInvalidOutput"]; legacy {
					t.Fatalf("current run writer emitted legacy recovery policy: %s", encoded)
				}
				if _, present := policy["jsonRepair"]; !present {
					t.Fatalf("normalized recovery policy omitted jsonRepair: %s", encoded)
				}
				if _, present := policy["rerun"]; !present {
					t.Fatalf("normalized recovery policy omitted rerun: %s", encoded)
				}
			}
		})
	}
	for _, key := range []string{"maxAttempts", "structuredRepair", "retryParse", "repairEscalation"} {
		t.Run("retired field/"+key, func(t *testing.T) {
			raw := `{"profileId":"fixture","userPrompt":"fixture","callType":"text","` + key + `":null}`
			var input RunInput
			decoder := json.NewDecoder(strings.NewReader(raw))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err == nil {
				t.Errorf("retired field %s is accepted", key)
			}
		})
	}
}
