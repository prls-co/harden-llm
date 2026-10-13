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
