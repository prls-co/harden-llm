package schema

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-010

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
)

func TestSchemaContract(t *testing.T) {
	fixture := loadSchemaFixture(t)
	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			input, _ := json.Marshal(testCase.Input)
			normalized, err := Normalize(input)
			if err != nil {
				t.Fatal(err)
			}
			var got any
			if err := json.Unmarshal(normalized, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, testCase.Normalized) {
				t.Fatalf("Normalize() = %#v, want %#v", got, testCase.Normalized)
			}
			if err := ValidateContract(normalized); err != nil {
				t.Fatalf("normalized contracted schema rejected: %v", err)
			}
		})
	}

	invalid := []string{
		`{"type":"array","items":{"type":"string"}}`,
		`{"type":"object","properties":{"answer":{"type":"string","minLength":1}},"required":["answer"],"additionalProperties":false}`,
		`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`,
		`{"type":"object","properties":{"answer":{"type":"string"}},"required":[],"additionalProperties":false}`,
		`{"type":"object","properties":{"values":{"type":"array"}},"required":["values"],"additionalProperties":false}`,
	}
	for _, raw := range invalid {
		if err := ValidateContract(json.RawMessage(raw)); err == nil {
			t.Fatalf("unsupported contracted schema accepted: %s", raw)
		}
	}

	contract := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)
	parsed, diagnostic, err := ParseAndValidate(`{"answer":"ok"}`, contract)
	if err != nil || diagnostic != nil || !reflect.DeepEqual(parsed, map[string]any{"answer": "ok"}) {
		t.Fatalf("valid parse = %#v/%#v/%v", parsed, diagnostic, err)
	}
	for _, testCase := range []struct {
		name string
		raw  string
	}{
		{name: "malformed", raw: `{"answer":`},
		{name: "schema mismatch", raw: `{"answer":42}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, diagnostic, err := ParseAndValidate(testCase.raw, contract)
			if err == nil || diagnostic == nil || diagnostic.Category != "parse_error" || diagnostic.Stage == "" {
				t.Fatalf("unsafe diagnostic result: %#v/%v", diagnostic, err)
			}
			if len(diagnostic.RawTail) > 128 || diagnostic.RawLength != len(testCase.raw) {
				t.Fatalf("diagnostic excerpt is not bounded: %#v", diagnostic)
			}
			if strings.Contains(strings.ToLower(diagnostic.Message), "panic") {
				t.Fatalf("unstable diagnostic message: %q", diagnostic.Message)
			}
		})
	}

	envelope := json.RawMessage(`{"repair":{"explanation":"fixed","changes":["answer type"]},"data":{"answer":"ok"}}`)
	data, _, err := coreruntime.ExtractRepairData(envelope, func(raw json.RawMessage) error {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		return ValidateValue(contract, value)
	})
	if err != nil || string(data) != `{"answer":"ok"}` {
		t.Fatalf("validated repair data = %s/%v", data, err)
	}
	_, _, err = coreruntime.ExtractRepairData(json.RawMessage(`{"repair":{"explanation":"x","changes":[]},"data":{"answer":42}}`), func(raw json.RawMessage) error {
		var value any
		if unmarshalErr := json.Unmarshal(raw, &value); unmarshalErr != nil {
			return unmarshalErr
		}
		return ValidateValue(contract, value)
	})
	if err == nil || !errors.Is(err, ErrValueInvalid) {
		t.Fatalf("invalid repair data error = %v", err)
	}
}

func TestStructuredParserParityCapturedSource(t *testing.T) {
	t.Parallel()
	_, file, _, _ := runtime.Caller(0)
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "fixtures", "parity", "generated", "structured-parser-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		RepairCases []struct {
			Name   string `json:"name"`
			Raw    string `json:"raw"`
			Parsed any    `json:"parsed"`
		} `json:"repairCases"`
		GeminiCases []struct {
			Name   string `json:"name"`
			Raw    string `json:"raw"`
			Parsed any    `json:"parsed"`
		} `json:"geminiCases"`
		RepairFailure struct {
			Succeeded   bool `json:"succeeded"`
			Diagnostics struct {
				RawResponse string `json:"rawResponseTail"`
			} `json:"diagnostics"`
		} `json:"repairFailure"`
	}
	if err = json.Unmarshal(contents, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range fixture.RepairCases {
		testCase := testCase
		t.Run("repair/"+testCase.Name, func(t *testing.T) {
			t.Parallel()
			parsed, diagnostic, parseErr := ParseProviderOutput(testCase.Raw, "openai.responses")
			if parseErr != nil || diagnostic != nil || !reflect.DeepEqual(normalizeParsedNumbers(parsed), normalizeParsedNumbers(testCase.Parsed)) {
				t.Fatalf("parser mismatch: got=%#v diagnostic=%#v error=%v want=%#v", parsed, diagnostic, parseErr, testCase.Parsed)
			}
		})
	}
	for _, testCase := range fixture.GeminiCases {
		testCase := testCase
		t.Run("gemini/"+testCase.Name, func(t *testing.T) {
			t.Parallel()
			parsed, diagnostic, parseErr := ParseProviderOutput(testCase.Raw, "google.gemini.generateContent")
			if parseErr != nil || diagnostic != nil || !reflect.DeepEqual(normalizeParsedNumbers(parsed), normalizeParsedNumbers(testCase.Parsed)) {
				t.Fatalf("Gemini parser mismatch: got=%#v diagnostic=%#v error=%v want=%#v", parsed, diagnostic, parseErr, testCase.Parsed)
			}
		})
	}
	if fixture.RepairFailure.Succeeded {
		t.Fatal("captured source unexpectedly accepted invalid Unicode")
	}
	_, diagnostic, err := ParseProviderOutput(fixture.RepairFailure.Diagnostics.RawResponse, "openai.responses")
	if err == nil || diagnostic == nil || diagnostic.Stage != "json_repair" {
		t.Fatalf("repair failure classification mismatch: %#v %v", diagnostic, err)
	}
}

type schemaFixture struct {
	Cases []struct {
		Name       string         `json:"name"`
		Input      map[string]any `json:"input"`
		Normalized map[string]any `json:"normalized"`
	} `json:"cases"`
}

func loadSchemaFixture(t *testing.T) schemaFixture {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "fixtures", "parity", "generated", "schema-normalization.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture schemaFixture
	if err := json.Unmarshal(contents, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}
