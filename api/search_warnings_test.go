package api

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-546

import "testing"

func TestSearchWarningsReferenceContract(t *testing.T) {
	t.Parallel()
	valid := `{"mode":"native","executed":true,"sources":[],"costStatus":"unavailable","incomplete":true,"warnings":["invalid_citation","unsupported_source_type"]}`
	if err := Validate("ReferenceWebSearchResult", []byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`{"mode":"native","executed":true,"sources":[],"costStatus":"unavailable","warnings":["unbounded provider text"]}`,
		`{"mode":"native","executed":true,"sources":[],"costStatus":"unavailable","warnings":["invalid_citation","invalid_citation"]}`,
		`{"mode":"native","executed":true,"sources":[],"costStatus":"unavailable","warnings":"invalid_citation"}`,
	} {
		if err := Validate("ReferenceWebSearchResult", []byte(invalid)); err == nil {
			t.Fatal("invalid warnings accepted")
		}
	}
}
