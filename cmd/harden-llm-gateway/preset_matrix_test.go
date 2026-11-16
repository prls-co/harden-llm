package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-548

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
)

func TestPresetSearchMatrixInputs(t *testing.T) {
	t.Parallel()
	catalog, err := stateful.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for id, preset := range catalog {
		for _, mode := range []hardenllm.CallType{hardenllm.CallTypeText, hardenllm.CallTypeStructured} {
			baseline, off, err := livePresetInput(id, preset, mode, false)
			if err != nil || off != "off" || baseline.WebSearch {
				t.Fatalf("%s/%s baseline: %s %v", id, mode, off, err)
			}
			searched, route, err := livePresetInput(id, preset, mode, true)
			if err != nil || !searched.WebSearch || !strings.Contains(searched.UserPrompt, "Search the web") {
				t.Fatalf("%s/%s search input: %v", id, mode, err)
			}
			// Search changes intent and prompt, never preset options, schema,
			// model, reasoning, or the one-attempt/cache-off policy.
			searched.WebSearch, searched.UserPrompt = baseline.WebSearch, baseline.UserPrompt
			if !reflect.DeepEqual(searched, baseline) || baseline.CacheMode != hardenllm.CacheModeOff || baseline.TimeoutMS != 60000 {
				t.Fatalf("%s/%s search changed execution settings", id, mode)
			}
			var policy hardenllm.RecoveryPolicy
			if json.Unmarshal(baseline.RecoveryPolicy, &policy) != nil || policy.MaxAttempts != 1 || len(policy.RetryOn) != 0 || policy.JSONRepair != nil || policy.Rerun != nil {
				t.Fatal("live matrix could repeat inference")
			}
			if (mode == hardenllm.CallTypeText) != (baseline.Schema == nil) {
				t.Fatalf("%s/%s schema selection", id, mode)
			}
			var profile stateful.Profile
			if err := json.Unmarshal(preset.Definition, &profile); err != nil {
				t.Fatal(err)
			}
			want := "jina"
			if stateful.NativeWebSearchSupported(profile) && (profile.APIInferenceType == "responses" || profile.APIInferenceType == "gemini-generate-content" || profile.APIInferenceType == "anthropic-messages" && mode == hardenllm.CallTypeText) {
				want = "native"
			}
			if route != want {
				t.Fatalf("%s/%s: got %s, want %s", id, mode, route, want)
			}
		}
	}
}

func TestPresetSearchMatrixEvidence(t *testing.T) {
	t.Parallel()
	text := "Go 🌍"
	sum := sha256.Sum256([]byte(text))
	for _, test := range []struct {
		name, expected string
		search         *hardenllm.SearchResult
		wantError      bool
	}{
		{"optional native search not observed", "native", &hardenllm.SearchResult{Mode: "native", Sources: []hardenllm.SearchSource{}, CostStatus: "unavailable"}, false},
		{"successful search with no results", "native", &hardenllm.SearchResult{Mode: "native", Executed: true, Sources: []hardenllm.SearchSource{}, CostStatus: "unavailable"}, false},
		{"fallback evidence", "jina", &hardenllm.SearchResult{Mode: "jina", Executed: true, Sources: []hardenllm.SearchSource{{URL: "https://go.dev/doc/"}}, CostStatus: "unavailable"}, false},
		{"missing evidence", "native", nil, true},
		{"unexpected fallback", "native", &hardenllm.SearchResult{Mode: "jina", Sources: []hardenllm.SearchSource{}, CostStatus: "unavailable"}, true},
		{"fallback did not run", "jina", &hardenllm.SearchResult{Mode: "jina", Sources: []hardenllm.SearchSource{}, CostStatus: "unavailable"}, true},
		{"malformed metadata", "native", &hardenllm.SearchResult{Mode: "native", Sources: []hardenllm.SearchSource{{URL: "javascript:bad"}}, CostStatus: "unavailable"}, true},
		{"valid Unicode citation", "native", &hardenllm.SearchResult{Mode: "native", Executed: true, Sources: []hardenllm.SearchSource{}, CostStatus: "unavailable", CitationTextSHA256: hex.EncodeToString(sum[:]), Citations: []hardenllm.SearchCitation{{URL: "https://go.dev/doc/", StartIndex: 3, EndIndex: 4}}}, false},
		{"citation outside returned text", "native", &hardenllm.SearchResult{Mode: "native", Executed: true, Sources: []hardenllm.SearchSource{}, CostStatus: "unavailable", CitationTextSHA256: hex.EncodeToString(sum[:]), Citations: []hardenllm.SearchCitation{{URL: "https://go.dev/doc/", StartIndex: 3, EndIndex: 5}}}, true},
		{"citation bound to different answer", "native", &hardenllm.SearchResult{Mode: "native", Executed: true, Sources: []hardenllm.SearchSource{}, CostStatus: "unavailable", CitationTextSHA256: strings.Repeat("0", 64), Citations: []hardenllm.SearchCitation{{URL: "https://go.dev/doc/", StartIndex: 0, EndIndex: 2}}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := stateful.RunOutput{RecordedRunOutput: stateful.RecordedRunOutput{Output: text, Search: test.search}}
			facts, err := livePresetSearchFacts(output, test.expected)
			if (err != nil) != test.wantError {
				t.Fatalf("facts=%v err=%v", facts, err)
			}
			if err == nil && (facts["searchExecuted"] != test.search.Executed || facts["sourceCount"] != len(test.search.Sources)) {
				t.Fatal("search outcome was fabricated")
			}
		})
	}
	// Structured output cannot prove the original serialized assistant text.
	// Keep that limit explicit rather than hashing a reserialized map.
	output := stateful.RunOutput{RecordedRunOutput: stateful.RecordedRunOutput{
		Output: map[string]any{"answer": "Go"},
		Search: &hardenllm.SearchResult{Mode: "native", Sources: []hardenllm.SearchSource{}, CostStatus: "unavailable", CitationTextSHA256: strings.Repeat("0", 64), Citations: []hardenllm.SearchCitation{{URL: "https://go.dev/doc/", StartIndex: 0, EndIndex: 2}}},
	}}
	facts, err := livePresetSearchFacts(output, "native")
	if err != nil || facts["citationValidation"] != "original_text_unavailable" {
		t.Fatal("structured citation verification was overstated", facts, err)
	}
}

func TestPresetMatrixReportPreservesExistingEvidence(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "report.json")
	if err := createLivePresetReport(path); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{{"id": "fixture", "status": "started"}}
	if err := writeLivePresetReport(path, rows); err != nil {
		t.Fatal(err)
	}
	if err := createLivePresetReport(path); err == nil {
		t.Fatal("existing potentially unconfirmed campaign was overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "started") {
		t.Fatal("started outcome was lost", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("report must remain private", err)
	}
}
