package providers

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-534
// Exercises production provider and runtime paths, without altering owner prompts.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
	"github.com/prls-co/harden-llm/internal/schema"
)

func TestSearchProvenanceRepair(t *testing.T) { testSearchProvenanceRepair(t, false, false) }

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-535
func TestWebSearchRequestParityRequiredRepair(t *testing.T) {
	testSearchProvenanceRepair(t, true, false)
}

func TestWebSearchRequestParityRequiredRepairModel(t *testing.T) {
	testSearchProvenanceRepair(t, true, true)
}

func testSearchProvenanceRepair(t *testing.T, required, alternate bool) {
	for _, stream := range []bool{false, true} {
		for _, malformed := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/malformed=%v", stream, malformed), func(t *testing.T) {
				calls, searched := 0, 0
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					calls++
					if calls == 2 && alternate && body["model"] != "repair-model" {
						t.Errorf("repair target lost: %#v", body["model"])
					}
					output := []any{}
					if len(arrayValue(body["tools"])) > 0 {
						searched++
						output = append(output, map[string]any{"id": "ws_research", "type": "web_search_call", "status": "completed", "action": map[string]any{"type": "search", "query": "test", "sources": []any{map[string]any{"type": "url", "url": "https://example.test/source"}}}})
					}
					text := `{"ok":true}`
					if calls == 1 {
						if malformed {
							text = `{"ok":`
						} else {
							text = `{"ok":"wrong-type"}`
						}
					}
					output = append(output, map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": text}}})
					w.Header().Set("Content-Type", "application/json")
					response := map[string]any{"status": "completed", "output": output}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						event, _ := json.Marshal(map[string]any{"type": "response.completed", "response": response})
						fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", event)
					} else {
						json.NewEncoder(w).Encode(response)
					}
				}))
				defer server.Close()
				router := newTLSTestRouter(t, server, nil)
				conn := runtime.Connection{ID: "research", Provider: "openai", APIInferenceType: "responses", BaseURL: server.URL, SupportsWebSearch: true}
				contract := json.RawMessage(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}},"additionalProperties":false}`)
				call := runtime.Call{ModelID: "research", CallType: "structured", Messages: providerMessages("", "test"), WebSearch: true, Schema: contract, ValidateStructured: func(v any) error { return schema.ValidateValue(contract, v) }}
				if required {
					call.ToolChoice = runtime.ToolChoice{Mode: "required"}
				}
				target := retry.RecoveryTarget{Source: "generation"}
				if alternate {
					target = retry.RecoveryTarget{Source: "model", ModelID: "repair-model"}
				}
				config := retry.Config{Policy: retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{}, JSONRepair: &retry.RepairPlan{Initial: target}}}
				cache := &searchMemoryCache{values: map[string]runtime.CachedResult{}}
				record, err := runtime.Execute(context.Background(), router, conn, runtime.Credential{APIKey: "synthetic"}, call, config, cache, cachekey.ModeCache, cachekey.DefaultVersion, "research", "trace")
				executed := record.Search != nil && record.Search.Executed
				t.Logf("calls=%d searched=%d attempts=%d evidence=%v error=%v", calls, searched, len(record.Attempts), executed, err)
				if err != nil || calls != 2 || searched != 1 {
					t.Fatalf("repair must complete with one generation search: calls=%d searched=%d error=%v", calls, searched, err)
				}
				if !executed {
					t.Error("successful repaired answer lost observed search evidence")
				}
				hit, hitErr := runtime.Execute(context.Background(), router, conn, runtime.Credential{APIKey: "synthetic"}, call, config, cache, cachekey.ModeCache, cachekey.DefaultVersion, "replay", "trace")
				hitEvidence := hit.Search != nil && hit.Search.Executed
				t.Logf("replay_hit=%v replay_evidence=%v calls_after_replay=%d", hit.Cache.Served, hitEvidence, calls)
				if hitErr != nil || !hit.Cache.Served || !hitEvidence || calls != 2 {
					t.Errorf("cache replay must retain evidence with no extra dispatch: error=%v", hitErr)
				}
			})
		}
	}
}

func TestSearchProvenanceJinaDuplicates(t *testing.T) {
	contextText := strings.Repeat("https://example.test/first\n", 50) + "https://example.test/second\n"
	prepared := preparedRequest{searchMode: "jina", webSearch: &preparedWebSearch{state: &webSearchResult{result: contextText}}}
	result := normalizeSearch(prepared, map[string]any{}, "")
	t.Logf("raw_matches=51 distinct_urls=2 retained_sources=%d", len(result.Sources))
	if !result.Executed || len(result.Sources) != 2 {
		t.Fatalf("duplicates must not hide a later distinct URL: got %d sources", len(result.Sources))
	}
}

func TestSearchProvenanceRejectedResponses(t *testing.T) {
	for _, kind := range []string{"http_error", "incomplete", "refusal", "unterminated_stream"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "http_error" {
					w.WriteHeader(503)
				}
				status := "completed"
				if kind == "incomplete" {
					status = "incomplete"
				}
				content := map[string]any{"type": "output_text", "text": `{"ok":`}
				if kind == "refusal" {
					content = map[string]any{"type": "refusal", "refusal": "synthetic refusal"}
				}
				body := map[string]any{"status": status, "output": []any{map[string]any{"type": "web_search_call", "status": "completed", "action": map[string]any{"type": "search"}}, map[string]any{"type": "message", "content": []any{content}}}}
				if kind == "unterminated_stream" {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"web_search_call\",\"status\":\"completed\"}}\n\n")
					return
				}
				json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			router := newTLSTestRouter(t, server, nil)
			conn := runtime.Connection{ID: "research", Provider: "openai", APIInferenceType: "responses", BaseURL: server.URL, SupportsWebSearch: true}
			call := runtime.Call{ModelID: "research", CallType: "structured", Messages: providerMessages("", "test"), WebSearch: true, Schema: json.RawMessage(`{"type":"object"}`)}
			prepared, err := router.Prepare(context.Background(), conn, runtime.Credential{APIKey: "synthetic"}, call)
			if err != nil {
				t.Fatal(err)
			}
			result, err := router.Execute(context.Background(), prepared)
			t.Logf("error_present=%v output_nil=%v evidence_nil=%v", err != nil, result.Output == nil, result.Search == nil)
			if err == nil || result.Output != nil || result.Search != nil {
				t.Fatal("rejected response must not acquire successful output or search evidence")
			}
		})
	}
}

func TestSearchProvenanceCanonicalCalls(t *testing.T) {
	body := []byte(`{"status":"completed","output":[{"id":"ws_a","type":"web_search_call","status":"completed","action":{"type":"search","query":"q","sources":[{"type":"url","url":"https://example.test/a"}]}},{"type":"message","content":[{"type":"output_text","text":"Alpha","annotations":[{"type":"url_citation","url":"https://example.test/a","title":"A","start_index":0,"end_index":5}]}]}]}`)
	for _, callType := range []string{"text", "structured"} {
		fixture := body
		if callType == "structured" {
			fixture = []byte(strings.Replace(string(body), `"text":"Alpha"`, `"text":"12345"`, 1))
		}
		result, err := normalizeResponse(preparedRequest{protocol: "openai.responses", callType: callType, searchMode: "native"}, fixture)
		if err != nil {
			t.Fatal(err)
		}
		if result.Search == nil || len(result.Search.Calls) != 1 || result.Search.Calls[0].ID != "ws_a" || result.Search.Calls[0].Action.Query != "q" || len(result.Search.Calls[0].Sources) != 1 || len(result.Search.Citations) != 1 || result.Search.CitationTextSHA256 == "" {
			t.Fatalf("canonical search provenance missing: %#v", result.Search)
		}
	}
}

func TestSearchProvenanceFixtures(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, callType  string
		calls, sources, citations int
		incomplete                bool
	}{
		{"openai_responses.json", "openai.responses", "text", 2, 2, 2, false},
		{"openai_structured.json", "openai.responses", "structured", 2, 2, 1, false},
		{"anthropic.json", "anthropic.messages", "text", 1, 2, 0, true},
		{"gemini.json", "google.gemini.generateContent", "text", 2, 2, 0, false},
		{"openai_edge.json", "openai.responses", "text", 3, 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/web_search/" + tc.name)
			if err != nil {
				t.Fatal(err)
			}
			result, err := normalizeResponse(preparedRequest{protocol: tc.protocol, callType: tc.callType, searchMode: "native"}, data)
			if err != nil {
				t.Fatal(err)
			}
			s := result.Search
			if s == nil || len(s.Calls) != tc.calls || len(s.Sources) != tc.sources || len(s.Citations) != tc.citations || s.Incomplete != tc.incomplete {
				t.Fatalf("unexpected provenance: %#v", s)
			}
			if tc.citations > 0 {
				sum := sha256.Sum256([]byte(result.AssistantText))
				if s.CitationTextSHA256 != hex.EncodeToString(sum[:]) {
					t.Fatal("citation hash does not match assistant text")
				}
				spans := []string{"café", "🚲"}
				if tc.callType == "structured" {
					spans = []string{"Alpha café"}
				}
				text := []rune(result.AssistantText)
				for i, citation := range s.Citations {
					if citation.StartIndex < 0 || citation.EndIndex > len(text) || string(text[citation.StartIndex:citation.EndIndex]) != spans[i] {
						t.Fatalf("citation %d does not index %q: %#v", i, spans[i], citation)
					}
				}
			}
			if strings.HasPrefix(tc.name, "openai_") && tc.name != "openai_edge.json" {
				if s.Calls[0].ID != "ws_a1" || s.Calls[0].Action.Query != "q one" || len(s.Calls[0].Sources) != 2 || s.Calls[1].ID != "ws_b2" || s.Calls[1].Action.URL != "https://example.org/b" {
					t.Fatalf("upstream call order or source ownership changed: %#v", s.Calls)
				}
			}
			if err := runtime.ValidateSearchResult(s); err != nil {
				t.Fatal(err)
			}
			if tc.name == "gemini.json" && (len(s.Calls[0].Sources) != 0 || len(s.Calls[1].Sources) != 0) {
				t.Fatal("invented source ownership")
			}
			if tc.name == "anthropic.json" && (s.Calls[0].ID != "srvtoolu_1" || len(s.Calls[0].Sources) != 2) {
				t.Fatal("Anthropic call/source association lost")
			}
			if tc.name == "openai_edge.json" && (s.Calls[0].Action != nil || s.Calls[1].Status != "cancelled" || s.Calls[2].Action.Type != "find_in_page") {
				t.Fatal("edge call metadata lost")
			}
		})
	}
}

func TestSearchProvenanceBounds(t *testing.T) {
	output := make([]any, 0, 33)
	for i := 0; i < 33; i++ {
		sources := make([]any, 0, 10)
		for j := 0; j < 10; j++ {
			sources = append(sources, map[string]any{"type": "url", "url": fmt.Sprintf("https://example.test/%d", (i*10+j)%50)})
		}
		output = append(output, map[string]any{"id": fmt.Sprintf("ws_%d", i), "type": "web_search_call", "status": "completed", "action": map[string]any{"type": "search", "query": "q", "sources": sources}})
	}
	s := normalizeSearch(preparedRequest{searchMode: "native"}, map[string]any{"output": output}, "")
	refs := 0
	for _, c := range s.Calls {
		refs += len(c.Sources)
	}
	if len(s.Calls) != 32 || len(s.Sources) != 50 || refs != 200 || !s.Incomplete {
		t.Fatalf("bounds: calls=%d sources=%d refs=%d incomplete=%v", len(s.Calls), len(s.Sources), refs, s.Incomplete)
	}
	if err := runtime.ValidateSearchResult(s); err != nil {
		t.Fatal(err)
	}
}

func TestSearchProvenanceJinaSourceCap(t *testing.T) {
	for _, test := range []struct {
		name, context string
		count         int
		incomplete    bool
	}{
		{"duplicates", strings.Repeat("https://example.test/a\n", 51), 1, false},
		{"over cap", func() string {
			var b strings.Builder
			for i := 0; i < 51; i++ {
				fmt.Fprintf(&b, "https://example.test/%d\n", i)
			}
			return b.String()
		}(), 50, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := normalizeSearch(preparedRequest{searchMode: "jina", webSearch: &preparedWebSearch{state: &webSearchResult{result: test.context}}}, nil, "")
			if len(s.Sources) != test.count || s.Incomplete != test.incomplete {
				t.Fatalf("sources=%d incomplete=%v", len(s.Sources), s.Incomplete)
			}
		})
	}
}

func TestSearchProvenanceDroppedCallRetainsSourceUnion(t *testing.T) {
	response := map[string]any{"output": []any{map[string]any{"type": "web_search_call", "action": map[string]any{"type": "search", "sources": []any{map[string]any{"type": "url", "url": "https://example.test/a"}}}}}}
	s := normalizeSearch(preparedRequest{searchMode: "native"}, response, "")
	if len(s.Calls) != 0 || !s.Incomplete || len(s.Sources) != 1 || s.Sources[0].URL != "https://example.test/a" {
		t.Fatalf("dropped malformed call must not discard known source union: %#v", s)
	}
}
