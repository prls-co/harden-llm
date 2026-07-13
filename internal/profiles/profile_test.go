package profiles

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-017

func TestProfileParityRoundTripAndValidation(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../fixtures/parity/generated/profile-catalog.json")
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

func TestProfileParityRejectsInvalidShapeAndGraphs(t *testing.T) {
	t.Parallel()
	base := fixtureProfile("Primary")
	tests := []struct {
		name    string
		catalog Catalog
		field   string
	}{
		{"key mismatch", Catalog{"Primary": withProfile(base, func(profile *Profile) { profile.LLMProfile = "Other" })}, "Primary.llmProfile"},
		{"invalid name", Catalog{"Bad/Profile": fixtureProfile("Bad/Profile")}, "llmProfile"},
		{"insecure endpoint", Catalog{"Primary": withProfile(base, func(profile *Profile) { profile.BaseURL = "http://provider.example/v1" })}, "Primary.baseUrl"},
		{"secret default", Catalog{"Primary": withProfile(base, func(profile *Profile) {
			profile.DefaultOptions = map[string]any{"nested": map[string]any{"apiKey": "secret"}}
		})}, "Primary.defaultOptions.nested.apiKey"},
		{"duplicate", Catalog{"Primary": withProfile(base, func(profile *Profile) { profile.BackupProfiles = []string{"Backup", "Backup"} }), "Backup": fixtureProfile("Backup")}, "Primary.backupProfiles[1]"},
		{"missing", Catalog{"Primary": withProfile(base, func(profile *Profile) { profile.BackupProfiles = []string{"Missing"} })}, "Primary.backupProfiles[0]"},
		{"cycle", Catalog{"Primary": withProfile(base, func(profile *Profile) { profile.BackupProfiles = []string{"Backup"} }), "Backup": withProfile(fixtureProfile("Backup"), func(profile *Profile) { profile.BackupProfiles = []string{"Primary"} })}, "Primary.backupProfiles[0]"},
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

	depth := Catalog{}
	for index := 0; index <= 6; index++ {
		name := string(rune('A' + index))
		profile := fixtureProfile(name)
		if index < 6 {
			profile.BackupProfiles = []string{string(rune('A' + index + 1))}
		}
		depth[name] = profile
	}
	if err := ValidateCatalog(depth); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("depth-six graph was accepted: %v", err)
	}

	if _, err := ParseCatalog([]byte(`{"Primary":{"schemaVersion":1,"llmProfile":"Primary","backupProfiles":[{"llmProfile":"Backup"}]}}`)); err == nil {
		t.Fatal("nested backup compatibility shape was accepted")
	}
}

func fixtureProfile(name string) Profile {
	noTemperature := false
	return Profile{
		SchemaVersion: 1, LLMProfile: name, Provider: "openai", APIInferenceType: "responses",
		EndpointCredentialScope: "global", BaseURL: "https://api.openai.com/v1", ModelID: "gpt-test",
		Pricing: &Pricing{}, SupportsTemperature: &noTemperature, SupportsContractedStructuredOutput: true,
		DefaultOptions: map[string]any{},
	}
}

func withProfile(profile Profile, change func(*Profile)) Profile {
	change(&profile)
	return profile
}
