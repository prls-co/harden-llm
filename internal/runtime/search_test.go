package runtime

import (
	"strings"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-224
func TestRecoveryIntegritySearchAdmission(t *testing.T) {
	t.Parallel()
	valid := func() *SearchResult {
		return &SearchResult{
			Mode: "native", Sources: []SearchSource{{URL: "https://example.test/source", Title: "Source"}},
			Citations: []SearchCitation{{URL: "https://example.test/source", Title: "Source", StartIndex: 0, EndIndex: 4}}, CostStatus: "unavailable",
		}
	}
	if err := ValidateSearchResult(nil); err != nil {
		t.Fatalf("nil search result rejected: %v", err)
	}
	if err := ValidateSearchResult(valid()); err != nil {
		t.Fatalf("valid search result rejected: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*SearchResult)
	}{
		{name: "mode", mutate: func(result *SearchResult) { result.Mode = "other" }},
		{name: "cost status", mutate: func(result *SearchResult) { result.CostStatus = "exact" }},
		{name: "nil sources", mutate: func(result *SearchResult) { result.Sources = nil }},
		{name: "source limit", mutate: func(result *SearchResult) { result.Sources = make([]SearchSource, 51) }},
		{name: "source URL", mutate: func(result *SearchResult) { result.Sources[0].URL = "javascript:alert(1)" }},
		{name: "citation URL", mutate: func(result *SearchResult) { result.Citations[0].URL = "https://user:pass@example.test/source" }},
		{name: "citation range", mutate: func(result *SearchResult) { result.Citations[0].EndIndex = result.Citations[0].StartIndex }},
		{name: "entry point code points", mutate: func(result *SearchResult) { result.EntryPointHTML = strings.Repeat("é", 32769) }},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			result := valid()
			test.mutate(result)
			if err := ValidateSearchResult(result); err == nil {
				t.Fatal("invalid search result was accepted")
			}
		})
	}
	if !ValidSearchURL("https://example.test/path?q=1") || !ValidSearchURL("http://example.test/path") {
		t.Fatal("valid search URLs rejected")
	}
	for _, value := range []string{"", "javascript:alert(1)", "https://user:pass@example.test", "https:///missing-host"} {
		if ValidSearchURL(value) {
			t.Fatalf("invalid search URL accepted: %q", value)
		}
	}
}
