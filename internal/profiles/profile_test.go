package profiles

import (
	"encoding/json"
	"github.com/prls-co/harden-llm/internal/retry"
	"os"
	"reflect"
	"strings"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-017

func TestProfileParityRoundTripAndValidation(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../fixtures/contracts/profile-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Input json.RawMessage `json:"input"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	catalog, err := ParseCatalog(fixture.Input)
	if err != nil {
		t.Fatalf("ParseCatalog: %v", err)
	}
	encoded, err := MarshalCatalog(catalog)
	if err != nil {
		t.Fatalf("MarshalCatalog: %v", err)
	}
	var got, want any
	_ = json.Unmarshal(encoded, &got)
	_ = json.Unmarshal(fixture.Input, &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog round trip mismatch:\n got %s\nwant %s", encoded, fixture.Input)
	}

	models := NormalizeModels([]Model{{ID: "b"}, {ID: "a", Label: "A"}, {ID: "a", Label: "A replacement"}, {ID: ""}})
	if !reflect.DeepEqual(models, []Model{{ID: "a", Label: "A replacement"}, {ID: "b", Label: "b"}}) {
		t.Fatalf("model normalization mismatch: %#v", models)
	}
}

func TestParseCatalogRejectsLegacyProfileSchema(t *testing.T) {
	t.Parallel()
	profile := fixtureProfile("Primary")
	profile.SchemaVersion = 2
	input, err := json.Marshal(Catalog{"Primary": profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCatalog(input); err == nil || !strings.Contains(err.Error(), "Primary.schemaVersion") {
		t.Fatalf("legacy profile schema should fail with a schemaVersion error, got %v", err)
	}
}

func TestProfileRejectsInvalidShapeAndRecovery(t *testing.T) {
	t.Parallel()
	base := fixtureProfile("Primary")
	tests := []struct {
		name    string
		catalog Catalog
		field   string
	}{
		{"missing policy", Catalog{"Primary": withProfile(base, func(profile *Profile) { profile.RecoveryPolicy = retry.Policy{} })}, "Primary.recoveryPolicy.maxAttempts"},
		{"key mismatch", Catalog{"Primary": withProfile(base, func(profile *Profile) { profile.LLMProfile = "Other" })}, "Primary.llmProfile"},
		{"invalid name", Catalog{"Bad/Profile": fixtureProfile("Bad/Profile")}, "llmProfile"},
		{"control character in name", Catalog{"Bad\nProfile": fixtureProfile("Bad\nProfile")}, "llmProfile"},
		{"insecure endpoint", Catalog{"Primary": withProfile(base, func(profile *Profile) { profile.BaseURL = "http://provider.example/v1" })}, "Primary.baseUrl"},
		{"secret default", Catalog{"Primary": withProfile(base, func(profile *Profile) {
			profile.DefaultOptions = map[string]any{"nested": map[string]any{"apiKey": "secret"}}
		})}, "Primary.defaultOptions.nested.apiKey"},
		{"oversized model", Catalog{"Primary": withProfile(base, func(profile *Profile) {
			profile.Models = []Model{{ID: strings.Repeat("x", MaxModelIDBytes+1), Label: "x"}}
		})}, "Primary.models[0].id"},
		{"duplicate model", Catalog{"Primary": withProfile(base, func(profile *Profile) {
			profile.Models = []Model{{ID: "same", Label: "Same"}, {ID: "same", Label: "Same"}}
		})}, "Primary.models[1].id"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateCatalog(test.catalog)
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("expected field %q, got %v", test.field, err)
			}
		})
	}

	if _, err := ParseCatalog([]byte(`{"Primary":{"schemaVersion":1,"llmProfile":"Primary","backupProfiles":[{"llmProfile":"Backup"}]}}`)); err == nil {
		t.Fatal("nested backup compatibility shape was accepted")
	}
}

func fixtureProfile(name string) Profile {
	noTemperature := false
	return Profile{
		RecoveryPolicy: retry.DefaultPolicy(), SchemaVersion: 3, LLMProfile: name, Provider: "openai", APIInferenceType: "responses",
		EndpointCredentialScope: "global", BaseURL: "https://api.openai.com/v1", ModelID: "gpt-test",
		Pricing: &Pricing{}, SupportsTemperature: &noTemperature, SupportsContractedStructuredOutput: true,
		DefaultOptions: map[string]any{},
	}
}

func withProfile(profile Profile, change func(*Profile)) Profile {
	change(&profile)
	return profile
}
