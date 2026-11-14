package providers

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-545

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/prls-co/harden-llm/internal/runtime"
)

func normalizeCitationFixture(t *testing.T, protocol string, body any) runtime.ProviderResult {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	result, err := normalizeResponse(preparedRequest{protocol: protocol, callType: "text", searchMode: "native"}, raw)
	if err != nil {
		t.Fatalf("optional search metadata rejected the answer: %v", err)
	}
	if err := runtime.ValidateSearchResult(result.Search); err != nil {
		t.Fatalf("normalizer produced invalid canonical evidence: %v", err)
	}
	return result
}

func citationWarnings(t *testing.T, search *runtime.SearchResult) []string {
	t.Helper()
	raw, err := json.Marshal(search)
	if err != nil {
		t.Fatal(err)
	}
	var value struct{ Warnings []string }
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value.Warnings
}

func assertCitationSpans(t *testing.T, result runtime.ProviderResult, want ...string) {
	t.Helper()
	if len(result.Search.Citations) != len(want) {
		t.Fatalf("citations = %#v, want spans %q", result.Search.Citations, want)
	}
	text := []rune(result.AssistantText)
	for i, citation := range result.Search.Citations {
		if got := string(text[citation.StartIndex:citation.EndIndex]); got != want[i] {
			t.Fatalf("citation %d indexes %q, want %q", i, got, want[i])
		}
	}
	if len(want) > 0 {
		sum := sha256.Sum256([]byte(result.AssistantText))
		if result.Search.CitationTextSHA256 != hex.EncodeToString(sum[:]) {
			t.Fatal("citations are not bound to exact emitted text")
		}
	}
}

func TestSearchCitationAnthropicBlocks(t *testing.T) {
	t.Parallel()
	citation := map[string]any{"type": "web_search_result_location", "url": "https://example.test/a", "title": "A", "cited_text": "Source quotation, not answer text."}
	result := normalizeCitationFixture(t, "anthropic.messages", map[string]any{
		"stop_reason": "end_turn",
		"content": []any{
			map[string]any{"type": "text", "text": "🌐 "},
			map[string]any{"type": "text", "text": "café🙂", "citations": []any{citation, citation}},
			map[string]any{"type": "server_tool_use", "name": "unrelated_tool"},
			map[string]any{"type": "text", "text": "café🙂", "citations": []any{citation}},
		},
	})
	if result.AssistantText != "🌐 café🙂café🙂" || result.Output != result.AssistantText {
		t.Fatal("answer extraction changed")
	}
	assertCitationSpans(t, result, "café🙂", "café🙂")
	if result.Search.Citations[0].StartIndex != 2 || result.Search.Citations[1].StartIndex != 7 || result.Search.Incomplete {
		t.Fatalf("block association changed: %#v", result.Search)
	}
}

func geminiCitationBody(segment any, indices any) map[string]any {
	return map[string]any{"candidates": []any{map[string]any{
		"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": "🌐 café🙂"}, map[string]any{"text": "not returned"}}},
		"groundingMetadata": map[string]any{
			"webSearchQueries":  []any{"query"},
			"groundingChunks":   []any{map[string]any{"web": map[string]any{"uri": "https://example.test/a", "title": "A"}}, map[string]any{"web": map[string]any{"uri": "https://example.test/b", "title": "B"}}},
			"groundingSupports": []any{map[string]any{"segment": segment, "groundingChunkIndices": indices}},
		},
	}}}
}

func TestSearchCitationGeminiByteOffsets(t *testing.T) {
	t.Parallel()
	for _, segment := range []map[string]any{
		{"startIndex": 5, "endIndex": 10, "text": "café"},
		{"partIndex": 0, "startIndex": 5, "endIndex": 10},
	} {
		result := normalizeCitationFixture(t, "google.gemini.generateContent", geminiCitationBody(segment, []any{0, 1, 0}))
		assertCitationSpans(t, result, "café", "café")
		if result.Search.Incomplete || result.Search.Citations[0].StartIndex != 2 || result.Search.Citations[0].EndIndex != 6 {
			t.Fatalf("byte indices were not mapped to code points: %#v", result.Search)
		}
	}
	result := normalizeCitationFixture(t, "google.gemini.generateContent", geminiCitationBody(map[string]any{"endIndex": 4, "text": "🌐"}, []any{0}))
	assertCitationSpans(t, result, "🌐")
}

func TestSearchCitationGeminiRejectsUnverifiedRanges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		segment any
		indices any
	}{
		{"inside UTF8", map[string]any{"startIndex": 1, "endIndex": 4}, []any{0}},
		{"fractional", map[string]any{"startIndex": 5.5, "endIndex": 10}, []any{0}},
		{"negative", map[string]any{"startIndex": -1, "endIndex": 10}, []any{0}},
		{"out of range", map[string]any{"endIndex": 100}, []any{0}},
		{"mismatched text", map[string]any{"startIndex": 5, "endIndex": 10, "text": "wrong"}, []any{0}},
		{"other part", map[string]any{"partIndex": 1, "endIndex": 3}, []any{0}},
		{"unknown part", map[string]any{"partIndex": 10, "endIndex": 3}, []any{0}},
		{"missing source", map[string]any{"endIndex": 4}, []any{20}},
		{"negative source", map[string]any{"endIndex": 4}, []any{-1}},
		{"wrong source type", map[string]any{"endIndex": 4}, []any{"0"}},
		{"malformed segment", "new-provider-shape", []any{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := normalizeCitationFixture(t, "google.gemini.generateContent", geminiCitationBody(tc.segment, tc.indices))
			if result.Output != "🌐 café🙂" || len(result.Search.Citations) != 0 || len(result.Search.Sources) != 2 || !result.Search.Incomplete || len(citationWarnings(t, result.Search)) == 0 {
				t.Fatalf("unverified metadata was guessed or silently lost: %#v", result)
			}
		})
	}
}

func TestSearchCitationPartialOpenAI(t *testing.T) {
	t.Parallel()
	result := normalizeCitationFixture(t, "openai.responses", map[string]any{"status": "completed", "output": []any{
		map[string]any{"type": "web_search_call", "status": "completed", "action": map[string]any{"type": "search", "sources": []any{map[string]any{"type": "future_type", "url": "https://example.test/a"}}}},
		map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "answer", "annotations": []any{
			map[string]any{"type": "url_citation", "url": "https://example.test/a", "start_index": 0, "end_index": 6},
			map[string]any{"type": "url_citation", "url": "https://example.test/a", "start_index": -1, "end_index": 6},
			map[string]any{"type": "future_citation", "url": "https://example.test/a"},
		}}}},
	}})
	assertCitationSpans(t, result, "answer")
	if result.Output != "answer" || !result.Search.Incomplete || !reflect.DeepEqual(citationWarnings(t, result.Search), []string{"unsupported_source_type", "invalid_citation", "unsupported_citation_type"}) {
		t.Fatalf("partial result or warnings changed: %#v", result)
	}
}

func TestSearchCitationBoundAndMalformedMetadata(t *testing.T) {
	t.Parallel()
	annotations := make([]any, 0, 201)
	for i := 1; i <= 201; i++ {
		annotations = append(annotations, map[string]any{"type": "url_citation", "url": "https://example.test/a", "start_index": 0, "end_index": i})
	}
	result := normalizeCitationFixture(t, "openai.responses", map[string]any{"status": "completed", "output": []any{
		map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": strings.Repeat("a", 201), "annotations": annotations}}},
	}})
	if len(result.Search.Citations) != 200 || !reflect.DeepEqual(citationWarnings(t, result.Search), []string{"citation_limit"}) {
		t.Fatalf("citation bound is not explicit: %#v", result.Search)
	}
	for _, metadata := range []any{"new-provider-shape", map[string]any{"groundingSupports": map[string]any{"new": "shape"}}} {
		body := geminiCitationBody(nil, nil)
		body["candidates"].([]any)[0].(map[string]any)["groundingMetadata"] = metadata
		result := normalizeCitationFixture(t, "google.gemini.generateContent", body)
		if result.Output != "🌐 café🙂" || !reflect.DeepEqual(citationWarnings(t, result.Search), []string{"malformed_metadata"}) {
			t.Fatalf("metadata shape drift did not preserve answer with warning: %#v", result)
		}
	}
}

func TestSearchCitationGeminiDoesNotBindOtherCandidate(t *testing.T) {
	t.Parallel()
	body := geminiCitationBody(map[string]any{"endIndex": 4}, []any{0})
	candidate := body["candidates"].([]any)[0]
	body["candidates"] = []any{
		map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": "🌐 café🙂"}}}},
		candidate,
	}
	result := normalizeCitationFixture(t, "google.gemini.generateContent", body)
	if len(result.Search.Citations) != 0 || !reflect.DeepEqual(citationWarnings(t, result.Search), []string{"citation_text_unavailable"}) {
		t.Fatal("another candidate's metadata was attached to the selected answer")
	}
}
