package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-442
func TestCanonicalDecode(t *testing.T) {
	t.Parallel()
	data := `{"userPrompt":"\u03bb\n\ud83d\ude80","providerOptions":{"counter":9007199254740993,"decimal":1.234567890123456789,"exponent":1e20,"zero":-0,"nested":[null,false,true,{},[],"escaped \\\""],"duplicate":1,"duplicate":2}}`
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	var expected any
	if err := decoder.Decode(&expected); err != nil {
		t.Fatal(err)
	}
	got, err := Decode("ProfileInputs", []byte(data))
	if err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatalf("canonical decoder changed complete JSON values: %v\ngot %#v\nwant %#v", err, got, expected)
	}
	for _, invalid := range []string{
		``, `null`, `[]`, `{`, `{} {}`, `{} trailing`,
		`{"userPrompt":""}`, `{"userPrompt":false}`, `{"unknown":true}`,
		`{"providerOptions":{"number":01}}`, `{"providerOptions":{"number":NaN}}`,
		`{"providerOptions":{"number":1.}}`, `{"providerOptions":{"number":1e}}`,
		`{"userPrompt":"bad\q"}`, `{"userPrompt":"bad` + "\n" + `"}`,
		`{"providerOptions":{"nested":[1,]}}`,
	} {
		if _, err := Decode("ProfileInputs", []byte(invalid)); err == nil {
			t.Errorf("invalid syntax or canonical schema was accepted: %q", invalid)
		}
	}
}

func TestCanonicalProfileIdentifiers(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"CPA Responses", "OpenRouter 123", "Λ model 🚀", "00000000-0000-4000-8000-000000000001"} {
		data, _ := json.Marshal(id)
		if err := Validate("ReferenceProfileID", data); err != nil {
			t.Errorf("valid profile selector %q rejected: %v", id, err)
		}
	}
	for _, id := range []string{"bad/name", "bad\x00", "bad\u009f"} {
		data, _ := json.Marshal(id)
		if err := Validate("ReferenceProfileID", data); err == nil {
			t.Errorf("invalid profile selector %q accepted", id)
		}
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-534
func TestSearchProvenanceReferenceContract(t *testing.T) {
	for _, body := range []string{
		`{"mode":"native","executed":true,"sources":[],"costStatus":"unavailable"}`,
		`{"mode":"native","executed":true,"sources":[{"url":"https://example.test","title":"Source"}],"costStatus":"unavailable","calls":[{"id":"ws_1","status":"completed","action":{"type":"search","query":"q"},"sources":[{"url":"https://example.test","title":"Source"}]}],"citationTextSha256":"abcd","incomplete":true}`,
	} {
		if err := Validate("ReferenceWebSearchResult", []byte(body)); err != nil {
			t.Errorf("valid search evidence rejected: %v", err)
		}
	}
	if err := Validate("ReferenceWebSearchResult", []byte(`{"mode":"native","executed":true,"sources":[],"costStatus":"unavailable","calls":[{"status":"completed","unexpected":true}]}`)); err == nil {
		t.Fatal("unknown call field accepted")
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-535
func TestWebSearchRequestParitySchema(t *testing.T) {
	body := []byte(`{"model":"fixture","input":[{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"q"}},{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[]}]},{"role":"user","content":"next"}],"tools":[{"type":"web_search_preview","filters":{"blocked_domains":["example.test"]}}],"tool_choice":{"type":"web_search_preview"},"include":["web_search_call.action.sources"]}`)
	if err := Validate("ResponseRequest", body); err != nil {
		t.Fatal(err)
	}
	invalid := []byte(`{"model":"fixture","input":[{"role":"user","content":[{"type":"input_text","text":"hello","annotations":[]}]}]}`)
	if err := Validate("ResponseRequest", invalid); err == nil {
		t.Fatal("annotations are only supported on output_text")
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-600
func TestOrdinaryOptionContract(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		schema, fields string
		valid          bool
	}{
		{"ResponseRequest", `"temperature":0,"max_output_tokens":128,"fixture_option":{"values":[null,false,9007199254740993]}`, true},
		{"ResponseRequest", `"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"tool_choice":{"type":"web_search"}`, false},
		{"ResponseRequest", `"tools":[{"type":"web_search"}],"tool_choice":{"type":"web_search_preview"}`, true},
		{"ResponseRequest", `"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"harden":{"cache":"cache"}`, false},
		{"ResponseRequest", `"store":false`, true}, {"ResponseRequest", `"store":true`, false},
		{"ResponseRequest", `"text":{"verbosity":"low"}`, true}, {"ResponseRequest", `"text":{"verbosity":"low","format":{"type":"json_object"}}`, true},
		{"ResponseRequest", `"reasoning":null`, true}, {"ResponseRequest", `"reasoning":{"effort":"low"}`, true}, {"ResponseRequest", `"reasoning":{"effort":"low","fixture_sibling":false}`, true},
		{"ResponseRequest", `"reasoning":{"summary":"auto"}`, false}, {"ResponseRequest", `"reasoning":{"summary":"auto","effort":"low"}`, false},
		{"ResponseRequest", `"harden":{"typo":true}`, false}, {"ResponseRequest", `"previous_response_id":null`, false},
		{"ResponseRequest", `"timeout":1200`, false}, {"ResponseRequest", `"cacheMode":"off"`, false},
		{"ResponseRequest", `"__harden_llm_web_search":false`, false}, {"ResponseRequest", `"useResponsesApi":true`, false},
		{"ResponseRequest", `"api_key":"fixture"`, false}, {"ResponseRequest", `"ownerId":"fixture"`, false},
		{"ChatCompletionRequest", `"max_completion_tokens":128,"store":false,"fixture_option":{"values":[null,false,9007199254740993]}`, true},
		{"ChatCompletionRequest", `"logprobs":false`, true}, {"ChatCompletionRequest", `"logprobs":true`, false}, {"ChatCompletionRequest", `"top_logprobs":2`, false},
		{"ChatCompletionRequest", `"store":true`, false}, {"ChatCompletionRequest", `"conversation":{}`, false},
		{"BatchGenerationRequest", `"text":{"format":{"type":"json_object"}}`, true},
		{"BatchGenerationRequest", `"text":{"verbosity":"low"}`, false}, {"BatchGenerationRequest", `"fixture_option":false`, false},
	} {
		input := `"input":"hello"`
		if tc.schema == "ChatCompletionRequest" {
			input = `"messages":[{"role":"user","content":"hello"}]`
		}
		err := Validate(tc.schema, []byte(`{"model":"gpt-6-luna",`+input+","+tc.fields+"}"))
		if (err == nil) != tc.valid {
			t.Errorf("%s %s: want valid=%v, got %v", tc.schema, tc.fields, tc.valid, err)
		}
	}
}
