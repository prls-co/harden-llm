package reference

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-485

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const searchSources = `[{"url":"https://example.invalid/company","title":"Independent fixture source"}]`

func searchScenario(sources string) string {
	return strings.Replace(fixtureScenario, `"text":"not-json"`, `"text":"not-json","searchSources":`+sources, 1)
}

func TestScenarioSearchSourcesRequireExplicitNativeOutput(t *testing.T) {
	t.Parallel()
	for _, example := range []struct{ name, input string }{
		{"null", searchScenario(`null`)},
		{"empty", searchScenario(`[]`)},
		{"wrong_type", searchScenario(`"private-marker"`)},
		{"null_source", searchScenario(`[null]`)},
		{"missing_url", searchScenario(`[{"title":"private-marker"}]`)},
		{"missing_title", searchScenario(`[{"url":"https://example.invalid"}]`)},
		{"blank_title", searchScenario(`[{"url":"https://example.invalid","title":" "}]`)},
		{"unknown_field", searchScenario(`[{"url":"https://example.invalid","title":"source","private-marker":true}]`)},
		{"non_http", searchScenario(`[{"url":"file:///private-marker","title":"source"}]`)},
		{"credentialed", searchScenario(`[{"url":"https://private-marker@example.invalid","title":"source"}]`)},
		{"duplicate", searchScenario(strings.TrimSuffix(searchSources, "]") + "," + strings.TrimPrefix(searchSources, "["))},
		{"too_many", searchScenario("[" + strings.Repeat(`{"url":"https://example.invalid","title":"source"},`, 50) + `{"url":"https://example.invalid/last","title":"source"}]`)},
		{"chat", strings.Replace(searchScenario(searchSources), "/v1/responses", "/v1/chat/completions", 1)},
		{"http_fault", strings.Replace(searchScenario(searchSources), `"kind":"output","text":"not-json"`, `"kind":"http_error","status":503`, 1)},
		{"disconnect", strings.Replace(searchScenario(`null`), `"kind":"output","text":"not-json"`, `"kind":"disconnect"`, 1)},
	} {
		t.Run(example.name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(example.input)); err == nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatal("invalid search fixture was accepted or disclosed its input")
			}
		})
	}
	loadScenario(t, searchScenario(searchSources))
}

func TestScenarioSearchRequiresNativeToolAndWritesExactEvidence(t *testing.T) {
	t.Parallel()
	for _, example := range []struct {
		name, tools string
		stream      bool
	}{
		{"missing", "", false},
		{"null", `null`, false},
		{"string", `"web_search"`, false},
		{"other_tool", `[{"type":"function","name":"web_search"}]`, false},
		{"native", `[{"type":"web_search"}]`, false},
		{"native_stream", `[{"type":"web_search"}]`, true},
	} {
		t.Run(example.name, func(t *testing.T) {
			scenario := loadScenario(t, searchScenario(searchSources))
			finish, err := scenario.BeginRun("fixture-operation")
			if err != nil {
				t.Fatal(err)
			}
			defer finish()
			body := `{"model":"fixture-model","input":"original marker","stream":false}`
			if example.tools != "" {
				body = strings.TrimSuffix(body, "}") + `,"tools":` + example.tools + `}`
			}
			if example.stream {
				body = strings.Replace(body, `"stream":false`, `"stream":true`, 1)
			}
			recorder := httptest.NewRecorder()
			scenario.Provider().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
			stats := scenario.Stats()
			valid := strings.HasPrefix(example.name, "native")
			if !valid {
				if recorder.Code != http.StatusConflict || stats.UnmatchedProviderCount != 1 || stats.Operations["fixture-operation"].ConsumedSteps != 0 {
					t.Fatal("a request without native search consumed grounded output")
				}
				return
			}
			if recorder.Code != http.StatusOK || stats.ProviderRequestCount != 1 || stats.UnmatchedProviderCount != 0 || stats.Operations["fixture-operation"].ConsumedSteps != 1 {
				t.Fatal("grounded native output accounting differs")
			}
			var response map[string]any
			encoded := recorder.Body.String()
			if example.stream {
				if recorder.Header().Get("Content-Type") != "text/event-stream" {
					t.Fatal("native search lost the requested SSE transport")
				}
				encoded = strings.TrimSuffix(strings.TrimPrefix(encoded, "event: response.completed\ndata: "), "\n\n")
				var event struct {
					Type     string         `json:"type"`
					Response map[string]any `json:"response"`
				}
				if json.Unmarshal([]byte(encoded), &event) != nil || event.Type != "response.completed" {
					t.Fatal("invalid native search completion event")
				}
				response = event.Response
			} else if json.Unmarshal([]byte(encoded), &response) != nil {
				t.Fatal("invalid native search response")
			}
			var expected []any
			_ = json.Unmarshal([]byte(`[{"type":"web_search_call","status":"completed","action":{"type":"search","sources":`+searchSources+`}},{"type":"message","content":[{"type":"output_text","text":"not-json"}]}]`), &expected)
			if !reflect.DeepEqual(response["output"], expected) || response["output_text"] != "not-json" || response["search"] != nil {
				t.Fatal("fixture did not emit exact native source data, or injected normalized search")
			}
		})
	}
}
