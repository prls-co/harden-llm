package schema

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-010

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/prls-co/harden-llm/internal/retry"
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
	// ADR-HLLM-020 intentionally rejects source salvage and preserves numeric strings.
	for _, testCase := range fixture.RepairCases {
		t.Run("repair/"+testCase.Name, func(t *testing.T) {
			parsed, diagnostic, err := ParseProviderOutput(testCase.Raw)
			if testCase.Name == "numeric-string-normalization" {
				if err != nil || diagnostic != nil || !reflect.DeepEqual(parsed, map[string]any{"answer": "ok", "count": "42"}) {
					t.Fatalf("numeric string changed: %#v/%#v/%v", parsed, diagnostic, err)
				}
			} else if err == nil || diagnostic == nil || diagnostic.Stage != "json_parse" {
				t.Fatalf("source salvage accepted: %#v/%#v/%v", parsed, diagnostic, err)
			}
		})
	}
	for _, testCase := range fixture.GeminiCases {
		t.Run("gemini/"+testCase.Name, func(t *testing.T) {
			parsed, diagnostic, err := ParseProviderOutput(testCase.Raw)
			if err == nil || diagnostic == nil || diagnostic.Stage != "json_parse" {
				t.Fatalf("provider-specific salvage accepted: %#v/%#v/%v", parsed, diagnostic, err)
			}
		})
	}
	if fixture.RepairFailure.Succeeded {
		t.Fatal("captured source unexpectedly accepted invalid Unicode")
	}
	_, diagnostic, err := ParseProviderOutput(fixture.RepairFailure.Diagnostics.RawResponse)
	if err == nil || diagnostic == nil || diagnostic.Stage != "json_parse" {
		t.Fatalf("repair failure classification mismatch: %#v %v", diagnostic, err)
	}
}

func TestJSONRepairIncidentParityCapturedSource(t *testing.T) {
	contents, err := os.ReadFile("../../fixtures/parity/source/evals/jsonrepair-object-key-expected-incident.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Attempts []struct {
			Error struct {
				RawResponse string `json:"rawResponse"`
			} `json:"error"`
		} `json:"attempts"`
		Expected struct {
			AttemptCount  int    `json:"attemptCountForRegression"`
			ParseStage    string `json:"parseStage"`
			ParserLibrary string `json:"parserLibrary"`
			RawLength     int    `json:"rawResponseLength"`
			RawTail       string `json:"rawResponseTail"`
			RawTailLimit  int    `json:"rawResponseTailLimit"`
			RetryCategory string `json:"retryCategory"`
			Retryable     bool   `json:"retryableBeforeBudgetExhaustion"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(contents, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Attempts) != fixture.Expected.AttemptCount || len(fixture.Attempts) == 0 {
		t.Fatalf("captured attempt count = %d, want %d", len(fixture.Attempts), fixture.Expected.AttemptCount)
	}
	raw := fixture.Attempts[len(fixture.Attempts)-1].Error.RawResponse
	if utf16Length(raw) != fixture.Expected.RawLength || fixture.Expected.RawTailLimit != 512 || !strings.HasSuffix(raw, fixture.Expected.RawTail) {
		t.Fatalf("captured raw-response evidence changed: utf16Length=%d tailLimit=%d", utf16Length(raw), fixture.Expected.RawTailLimit)
	}
	if fixture.Expected.ParseStage != "repair" || fixture.Expected.ParserLibrary != "jsonrepair" || fixture.Expected.RetryCategory != string(retry.CategoryParse) || !fixture.Expected.Retryable {
		t.Fatalf("captured incident expectation changed: %#v", fixture.Expected)
	}
	_, diagnostic, parseErr := ParseProviderOutput(raw)
	if parseErr == nil || diagnostic == nil || diagnostic.Stage != "json_parse" || diagnostic.Category != string(retry.CategoryParse) || diagnostic.RawLength != fixture.Expected.RawLength {
		t.Fatalf("target incident classification mismatch: diagnostic=%#v error=%v", diagnostic, parseErr)
	}
	wantTail := safeTail(raw, 128)
	if diagnostic.RawTail != wantTail {
		t.Fatalf("bounded raw tail mismatch: got %q want %q", diagnostic.RawTail, wantTail)
	}
	classification := retry.Classify(&retry.ProviderError{Err: parseErr, Category: retry.CategoryParse}, retry.DefaultPolicy())
	if classification.Category != retry.CategoryParse || classification.Retryable {
		t.Fatalf("target incident retry classification mismatch: %#v", classification)
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

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-203
func TestRecoveryValues(t *testing.T) {
	t.Parallel()
	t.Run("null JSON value", func(t *testing.T) {
		value, diagnostic, err := ParseProviderOutput("null")
		if value != nil || diagnostic != nil || err != nil {
			t.Fatalf("null JSON changed: %#v/%#v/%v", value, diagnostic, err)
		}
	})
	for _, test := range []struct {
		name, value, property string
		invalid               bool
	}{
		{"postal code", `"02139"`, `{"type":"string"}`, false},
		{"numeric string enum", `"007"`, `{"type":"string","enum":["007"]}`, false},
		{"large integer", `9007199254740993`, `{"type":"integer"}`, false},
		{"precise decimal", `0.123456789123456789`, `{"type":"number"}`, false},
		{"large exponent", `1e1000001`, `{"type":"integer","enum":[10e1000000]}`, false},
		{"small exponent", `1e-1000001`, `{"type":"number","enum":[10e-1000002]}`, false},
		{"small exponent is fractional", `1e-1000001`, `{"type":"integer"}`, true},
		{"negative zero", `-0e-1000001`, `{"type":"integer","enum":[0]}`, false},
		{"nested strings", `[{"zip":"02139"}]`, `{"type":"array","items":{"type":"object","properties":{"zip":{"type":"string"}},"required":["zip"],"additionalProperties":false}}`, false},
		{"null fails string schema", `null`, `{"type":"string"}`, true},
		{"number cannot become string", `42`, `{"type":"string"}`, true},
		{"string cannot become number", `"42"`, `{"type":"number"}`, true},
		{"large fractional integer", `9007199254740992.5`, `{"type":"integer"}`, true},
		{"adjacent large integer enum", `9007199254740993`, `{"type":"integer","enum":[9007199254740992]}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := `{"value":` + test.value + `}`
			contract := json.RawMessage(`{"type":"object","properties":{"value":` + test.property + `},"required":["value"],"additionalProperties":false}`)
			parsed, diagnostic, err := ParseAndValidate(raw, contract)
			if test.invalid {
				if err == nil || diagnostic == nil {
					t.Fatalf("invalid value accepted: %#v, diagnostic=%#v, error=%v", parsed, diagnostic, err)
				}
				return
			}
			if err != nil || diagnostic != nil {
				t.Fatalf("valid value rejected: diagnostic=%#v, error=%v", diagnostic, err)
			}
			encoded, err := json.Marshal(parsed)
			if err != nil || string(encoded) != raw {
				t.Fatalf("value changed: got %s/%v, want %s", encoded, err, raw)
			}
		})
	}
	contract := json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)
	for _, raw := range []string{
		`{"value":"ok",}`, `{'value':'ok'}`, "```json\n{\"value\":\"ok\"}\n```",
		`Here is the JSON: {"value":"ok"}`, `{"value":"ok"} {}`, `{"value":`,
	} {
		t.Run("invalid syntax/"+raw, func(t *testing.T) {
			if value, diagnostic, err := ParseAndValidate(raw, contract); err == nil || diagnostic == nil {
				t.Fatalf("invalid JSON accepted: value=%#v diagnostic=%#v error=%v", value, diagnostic, err)
			}
		})
	}
}
