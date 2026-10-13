package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-442
func BenchmarkReferenceResultValidation(b *testing.B) {
	data, err := os.ReadFile("../test/fixtures/reference/workspace.json")
	if err != nil {
		b.Fatal(err)
	}
	var fixture map[string]any
	if err = json.Unmarshal(data, &fixture); err != nil {
		b.Fatal(err)
	}
	result := fixture["record"].(map[string]any)["result"].(map[string]any)
	result["output"] = strings.Repeat("x", 17<<20)
	data, err = json.Marshal(result)
	if err != nil {
		b.Fatal(err)
	}
	if err = Validate("RecordedRunOutput", data); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		if err := Validate("RecordedRunOutput", data); err != nil {
			b.Fatal(err)
		}
	}
}
