package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-012

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
	// The old bundle fails before any vault, provider or store is needed.
	service := &ProfileService{}
	_, err = service.ReplaceBundle(context.Background(), "owner", ProfileBundle{
		SchemaVersion: 1, BundleID: "retired", CreatedAt: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC),
		Profiles: profiles.Catalog{}, CredentialIDs: map[string]string{},
	})
	var invalid *profiles.ValidationError
	if !errors.As(err, &invalid) || len(invalid.FieldErrors) != 1 || invalid.FieldErrors[0].Field != "schemaVersion" || !strings.Contains(invalid.FieldErrors[0].Message, "2") {
		t.Fatalf("retired bundle must identify the current format, got %v", err)
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
		{"complete", `{"maxAttempts":4,"retryOn":["network"],"repairInvalidOutput":true,"backoff":{"baseDelayMs":500,"maxDelayMs":8000}}`, false},
		{"false empty zero", `{"maxAttempts":1,"retryOn":[],"repairInvalidOutput":false,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, false},
		{"missing", "", true}, {"null", "null", true}, {"partial", `{"maxAttempts":4}`, true},
		{"unknown category", `{"maxAttempts":4,"retryOn":["parse_error"],"repairInvalidOutput":true,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, true},
		{"duplicate category", `{"maxAttempts":4,"retryOn":["network","network"],"repairInvalidOutput":true,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, true},
		{"missing false", `{"maxAttempts":4,"retryOn":[],"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, true},
		{"missing zero", `{"maxAttempts":4,"retryOn":[],"repairInvalidOutput":false,"backoff":{"maxDelayMs":0}}`, true},
		{"negative delay", `{"maxAttempts":4,"retryOn":[],"repairInvalidOutput":false,"backoff":{"baseDelayMs":-1,"maxDelayMs":0}}`, true},
		{"inverted delays", `{"maxAttempts":4,"retryOn":[],"repairInvalidOutput":false,"backoff":{"baseDelayMs":2,"maxDelayMs":1}}`, true},
		{"zero budget", `{"maxAttempts":0,"retryOn":[],"repairInvalidOutput":false,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, true},
		{"excess budget", `{"maxAttempts":11,"retryOn":[],"repairInvalidOutput":false,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`, true},
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
				if err != nil || !strings.Contains(string(encoded), `"recoveryPolicy":`+test.policy) {
					t.Fatalf("policy changed: %s/%v", encoded, err)
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
