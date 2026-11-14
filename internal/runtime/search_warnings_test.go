package runtime

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-546

import "testing"

func TestSearchWarningsValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		warnings   []string
		incomplete bool
		valid      bool
	}{
		{"legacy complete", nil, false, true},
		{"legacy partial", nil, true, true},
		{"known partial", []string{"invalid_citation", "unsupported_source_type"}, true, true},
		{"without incomplete", []string{"invalid_citation"}, false, false},
		{"unknown code", []string{"raw provider text"}, true, false},
		{"duplicate", []string{"invalid_citation", "invalid_citation"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &SearchResult{Mode: "native", Sources: []SearchSource{}, CostStatus: "unavailable", Warnings: tc.warnings, Incomplete: tc.incomplete}
			if err := ValidateSearchResult(result); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
		})
	}
}
