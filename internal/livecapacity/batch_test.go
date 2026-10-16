package livecapacity

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-473
import (
	"encoding/json"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/api"
	"strings"
	"testing"
)

func TestBatchBenchmarkSchemaAndIndependentOracle(t *testing.T) {
	for _, long := range []bool{false, true} {
		raw := BatchRequestBody("fixture-nonce", long)
		var wire struct {
			Text struct {
				Format struct {
					Schema json.RawMessage `json:"schema"`
				} `json:"format"`
			} `json:"text"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if _, err := hardenllm.NormalizeSchema(wire.Text.Format.Schema); err != nil {
			t.Fatal("benchmark bypassed core schema subset", err)
		}
		request, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"custom_id": "fixture-nonce", "request": json.RawMessage(raw)}}})
		if err := api.Validate("BatchJobRequest", request); err != nil {
			t.Fatal(err)
		}
		items := make([]string, 512)
		for i := range items {
			items[i] = "x"
		}
		out, _ := json.Marshal(map[string]any{"nonce": "fixture-nonce", "items": items})
		if err := ValidateBatchOutput("fixture-nonce", string(out), long); err != nil {
			t.Fatal(err)
		}
		if err := ValidateBatchOutput("another", string(out), long); err == nil {
			t.Fatal("wrong nonce accepted")
		}
		short, _ := json.Marshal(map[string]any{"nonce": "fixture-nonce", "items": items[:511]})
		if long && ValidateBatchOutput("fixture-nonce", string(short), true) == nil {
			t.Fatal("short array accepted")
		}
		items[0] = "y"
		bad, _ := json.Marshal(map[string]any{"nonce": "fixture-nonce", "items": items})
		if long && ValidateBatchOutput("fixture-nonce", string(bad), true) == nil {
			t.Fatal("wrong array value accepted")
		}
	}
}

func TestBoundedTextBenchmarkPreservesExactLengthAndCoreContract(t *testing.T) {
	raw := BoundedTextBatchRequestBody("fixture-nonce")
	var wire struct {
		Text struct {
			Format struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"format"`
		} `json:"text"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if _, err := hardenllm.NormalizeSchema(wire.Text.Format.Schema); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"custom_id": "fixture-nonce", "request": json.RawMessage(raw)}}})
	if err := api.Validate("BatchJobRequest", request); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		nonce, word string
		count       int
		valid       bool
	}{
		{"fixture-nonce", "x", 512, true},
		{"another", "x", 512, false},
		{"fixture-nonce", "x", 511, false},
		{"fixture-nonce", "x", 513, false},
		{"fixture-nonce", "y", 512, false},
	} {
		response, _ := json.Marshal(map[string]any{"nonce": tc.nonce, "payload": strings.TrimSpace(strings.Repeat(tc.word+" ", tc.count))})
		if err := ValidateBoundedTextBatchOutput("fixture-nonce", string(response)); (err == nil) != tc.valid {
			t.Fatalf("incorrect length/content qualification: %+v %v", tc, err)
		}
	}
}
