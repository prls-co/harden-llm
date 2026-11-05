package reference

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-485

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixtureScenario = `{
  "schemaVersion":"prls.reference-gateway-fixture.scenario.v1",
  "operations":[{
    "operationPattern":"^fixture-operation$",
    "steps":[
      {"match":{"path":"/v1/responses","modelId":"fixture-model","contains":"original marker"},"kind":"output","text":"not-json"},
      {"match":{"path":"/v1/responses","modelId":"fixture-model","contains":"not-json"},"kind":"output","text":"{\"ok\":true}"}
    ]
  }]
}`

func loadScenario(t *testing.T, input string) *Scenario {
	t.Helper()
	scenario, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	return scenario
}

func providerRequest(scenario *Scenario, path, model, marker string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"model": model, "input": []any{map[string]any{"content": marker}}})
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	scenario.Provider().ServeHTTP(recorder, request)
	return recorder
}

func TestScenarioStrictContract(t *testing.T) {
	t.Parallel()
	for _, example := range []struct{ name, input string }{
		{"unknown_root", strings.Replace(fixtureScenario, `"operations":`, `"secret":"private-marker","operations":`, 1)},
		{"unknown_step", strings.Replace(fixtureScenario, `"kind":"output"`, `"unexpected":true,"kind":"output"`, 1)},
		{"wrong_version", strings.Replace(fixtureScenario, "scenario.v1", "scenario.v2", 1)},
		{"empty_operations", `{"schemaVersion":"prls.reference-gateway-fixture.scenario.v1","operations":[]}`},
		{"unanchored_identity", strings.Replace(fixtureScenario, "^fixture-operation$", "fixture-operation", 1)},
		{"invalid_identity", strings.Replace(fixtureScenario, "^fixture-operation$", "^[$", 1)},
		{"no_steps", `{"schemaVersion":"prls.reference-gateway-fixture.scenario.v1","operations":[{"operationPattern":"^fixture-operation$","steps":[]}]}`},
		{"unknown_match", strings.Replace(fixtureScenario, `"modelId":"fixture-model"`, `"private-marker":true,"modelId":"fixture-model"`, 1)},
		{"empty_marker", strings.Replace(fixtureScenario, "original marker", "", 1)},
		{"empty_model", strings.Replace(fixtureScenario, `"modelId":"fixture-model"`, `"modelId":""`, 1)},
		{"external_path", strings.Replace(fixtureScenario, "/v1/responses", "https://private-marker.invalid/v1/responses", 1)},
		{"query_path", strings.Replace(fixtureScenario, "/v1/responses", "/v1/responses?secret=private-marker", 1)},
		{"unknown_kind", strings.Replace(fixtureScenario, `"kind":"output"`, `"kind":"private-marker"`, 1)},
		{"missing_output", strings.Replace(fixtureScenario, `,"text":"not-json"`, "", 1)},
		{"null_output", strings.Replace(fixtureScenario, `"text":"not-json"`, `"text":null`, 1)},
		{"output_with_status", strings.Replace(fixtureScenario, `"kind":"output"`, `"kind":"output","status":503`, 1)},
		{"output_with_zero_status", strings.Replace(fixtureScenario, `"kind":"output"`, `"kind":"output","status":0`, 1)},
		{"output_with_null_status", strings.Replace(fixtureScenario, `"kind":"output"`, `"kind":"output","status":null`, 1)},
		{"http_success_is_not_fault", strings.Replace(fixtureScenario, `"kind":"output","text":"not-json"`, `"kind":"http_error","status":200`, 1)},
		{"fault_with_output", strings.Replace(fixtureScenario, `"kind":"output"`, `"kind":"disconnect"`, 1)},
		{"fault_with_null_output", strings.Replace(fixtureScenario, `"kind":"output","text":"not-json"`, `"kind":"disconnect","text":null`, 1)},
		{"fault_with_null_status", strings.Replace(fixtureScenario, `"kind":"output","text":"not-json"`, `"kind":"disconnect","status":null`, 1)},
		{"trailing_json", fixtureScenario + `{}`},
		{"trailing_junk", fixtureScenario + `private-marker`},
		{"null_root", `null`},
	} {
		t.Run(example.name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(example.input)); err == nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("invalid scenario accepted or private input exposed: %v", err)
			}
		})
	}
	// Empty model output is a deliberate scenario, not an absent default.
	loadScenario(t, strings.Replace(fixtureScenario, `"text":"not-json"`, `"text":""`, 1))
}

func TestScenarioOrderedDispatchAndExactStats(t *testing.T) {
	t.Parallel()
	scenario := loadScenario(t, fixtureScenario)
	finish, err := scenario.BeginRun("fixture-operation")
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	for index, marker := range []string{"original marker", "not-json"} {
		response := providerRequest(scenario, "/v1/responses", "fixture-model", marker)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), []string{"not-json", `ok`}[index]) {
			t.Fatalf("ordered native response %d: status=%d body=%s", index, response.Code, response.Body)
		}
	}
	expected := `{"schemaVersion":"prls.reference-gateway-fixture.stats.v1","runPostCount":1,"providerRequestCount":2,"unmatchedRunCount":0,"unmatchedProviderCount":0,"operations":{"fixture-operation":{"runPostCount":1,"providerDispatchCount":2,"consumedSteps":2}}}`
	encoded, err := json.Marshal(scenario.Stats())
	if err != nil || string(encoded) != expected {
		t.Fatalf("stats JSON differs: %s %v", encoded, err)
	}
	snapshot := scenario.Stats()
	delete(snapshot.Operations, "fixture-operation")
	if len(scenario.Stats().Operations) != 1 {
		t.Fatal("caller mutated scenario counters")
	}
	// Duplicate completion and a cache/rejection Run with zero provider work
	// must not consume or invent a third native step.
	finish()
	finish()
	finishAgain, err := scenario.BeginRun("fixture-operation")
	if err != nil {
		t.Fatal(err)
	}
	finishAgain()
	stats := scenario.Stats()
	if stats.RunPostCount != 2 || stats.ProviderRequestCount != 2 || stats.Operations["fixture-operation"].ConsumedSteps != 2 {
		t.Fatalf("logical POST and provider attempts conflated: %#v", stats)
	}
}

func TestScenarioRejectsUnknownAmbiguousAndExhaustedDispatch(t *testing.T) {
	t.Parallel()
	for _, example := range []struct{ name, path, model, marker string }{
		{"wrong_path", "/v1/chat/completions", "fixture-model", "original marker"},
		{"wrong_model", "/v1/responses", "other-model", "original marker"},
		{"wrong_marker", "/v1/responses", "fixture-model", "unrelated private-marker"},
		{"out_of_order", "/v1/responses", "fixture-model", "not-json"},
	} {
		t.Run(example.name, func(t *testing.T) {
			scenario := loadScenario(t, fixtureScenario)
			finish, err := scenario.BeginRun("fixture-operation")
			if err != nil {
				t.Fatal(err)
			}
			defer finish()
			response := providerRequest(scenario, example.path, example.model, example.marker)
			stats := scenario.Stats()
			if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), "private-marker") || stats.UnmatchedProviderCount != 1 || stats.Operations["fixture-operation"].ConsumedSteps != 0 {
				t.Fatal("unknown/out-of-order native request received a default output or consumed a step")
			}
		})
	}
	scenario := loadScenario(t, fixtureScenario)
	if _, err := scenario.BeginRun("unconfigured-private-marker"); err == nil || strings.Contains(err.Error(), "private-marker") {
		t.Fatal("unknown logical operation accepted or exposed")
	}
	finish, err := scenario.BeginRun("fixture-operation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scenario.BeginRun("fixture-operation"); err == nil {
		t.Fatal("overlapping identical logical operation accepted")
	}
	providerRequest(scenario, "/v1/responses", "fixture-model", "original marker")
	providerRequest(scenario, "/v1/responses", "fixture-model", "not-json")
	if providerRequest(scenario, "/v1/responses", "fixture-model", "original marker").Code != http.StatusConflict {
		t.Fatal("exhausted scenario returned a default valid response")
	}
	finish()
	if providerRequest(scenario, "/v1/responses", "fixture-model", "original marker").Code != http.StatusConflict {
		t.Fatal("provider request without an active Run was accepted")
	}
	stats := scenario.Stats()
	if stats.RunPostCount != 3 || stats.UnmatchedRunCount != 2 || stats.ProviderRequestCount != 4 || stats.UnmatchedProviderCount != 2 {
		t.Fatalf("rejected request counters differ: %#v", stats)
	}
	// Two rule matches cannot arbitrarily choose a scenario.
	ambiguous := strings.Replace(fixtureScenario, `"^fixture-operation$"`, `"^fixture-.*$"`, 1)
	var document map[string]any
	_ = json.Unmarshal([]byte(ambiguous), &document)
	rules := document["operations"].([]any)
	document["operations"] = append(rules, rules[0])
	encoded, _ := json.Marshal(document)
	if _, err := loadScenario(t, string(encoded)).BeginRun("fixture-operation"); err == nil {
		t.Fatal("ambiguous logical operation accepted")
	}
}

func TestScenarioConcurrentOperationsCannotMisattributeAttempts(t *testing.T) {
	t.Parallel()
	input := strings.Replace(fixtureScenario, "^fixture-operation$", "^fixture-[ab]$", 1)
	scenario := loadScenario(t, input)
	finishA, _ := scenario.BeginRun("fixture-a")
	finishB, _ := scenario.BeginRun("fixture-b")
	defer finishA()
	defer finishB()
	if providerRequest(scenario, "/v1/responses", "fixture-model", "original marker").Code != http.StatusConflict {
		t.Fatal("ambiguous active native request was attributed arbitrarily")
	}
	finishB()
	if providerRequest(scenario, "/v1/responses", "fixture-model", "original marker").Code != http.StatusOK {
		t.Fatal("unique active request rejected")
	}
	finishA()
	// Parallel reads/counters are race checked. Each operation has its own cursor.
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() { _ = scenario.Stats() })
	}
	group.Wait()
	stats := scenario.Stats()
	if stats.Operations["fixture-a"].ProviderDispatchCount != 1 || stats.Operations["fixture-b"].ProviderDispatchCount != 0 {
		t.Fatalf("cross-operation attribution: %#v", stats)
	}
	var first, second map[string]any
	_ = json.Unmarshal([]byte(strings.ReplaceAll(fixtureScenario, "fixture-operation", "fixture-a")), &first)
	_ = json.Unmarshal([]byte(strings.ReplaceAll(strings.ReplaceAll(fixtureScenario, "fixture-operation", "fixture-b"), "fixture-model", "fixture-b-model")), &second)
	first["operations"] = append(first["operations"].([]any), second["operations"].([]any)...)
	encoded, _ := json.Marshal(first)
	independent := loadScenario(t, string(encoded))
	finishA, err := independent.BeginRun("fixture-a")
	if err != nil {
		t.Fatal(err)
	}
	defer finishA()
	finishB, err = independent.BeginRun("fixture-b")
	if err != nil {
		t.Fatal(err)
	}
	defer finishB()
	for _, model := range []string{"fixture-model", "fixture-b-model"} {
		group.Go(func() {
			for _, marker := range []string{"original marker", "not-json"} {
				if providerRequest(independent, "/v1/responses", model, marker).Code != http.StatusOK {
					t.Error("unique concurrent native request rejected")
				}
			}
		})
	}
	group.Wait()
	stats = independent.Stats()
	if stats.UnmatchedProviderCount != 0 || stats.RunPostCount != 2 || stats.ProviderRequestCount != 4 || stats.Operations["fixture-a"].ConsumedSteps != 2 || stats.Operations["fixture-b"].ConsumedSteps != 2 {
		t.Fatalf("independent concurrent cursors/counts differ: %#v", stats)
	}
}

func TestScenarioRejectsMalformedNativeRequestsWithoutConsumingOutput(t *testing.T) {
	t.Parallel()
	for _, example := range []struct{ name, method, body string }{
		{"wrong_method", http.MethodGet, `{"model":"fixture-model","input":"original marker"}`},
		{"null", http.MethodPost, `null`},
		{"trailing_json", http.MethodPost, `{"model":"fixture-model","input":"original marker"}{}`},
		{"marker_is_only_a_property_name", http.MethodPost, `{"model":"fixture-model","original marker":true}`},
		{"oversized", http.MethodPost, `{"model":"fixture-model","input":"original marker"}` + strings.Repeat(" ", 256<<10)},
	} {
		t.Run(example.name, func(t *testing.T) {
			scenario := loadScenario(t, fixtureScenario)
			finish, err := scenario.BeginRun("fixture-operation")
			if err != nil {
				t.Fatal(err)
			}
			defer finish()
			request := httptest.NewRequest(example.method, "/v1/responses", strings.NewReader(example.body))
			response := httptest.NewRecorder()
			scenario.Provider().ServeHTTP(response, request)
			stats := scenario.Stats()
			if response.Code != http.StatusConflict || stats.UnmatchedProviderCount != 1 || stats.ProviderRequestCount != 1 || stats.Operations["fixture-operation"].ConsumedSteps != 0 {
				t.Fatalf("invalid native request accepted or consumed a step: %#v", stats)
			}
		})
	}
}

func TestScenarioOutputDelayStrictContract(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"null", "0", "-1", "60001", "1.5", `"1"`, "true", "{}"} {
		t.Run(value, func(t *testing.T) {
			input := strings.Replace(fixtureScenario, `"kind":"output"`, `"kind":"output","delayMs":`+value, 1)
			if _, err := Decode(strings.NewReader(input)); err == nil {
				t.Fatal("invalid explicit provider latency was accepted")
			}
		})
	}
	for _, value := range []string{"1", "35000", "60000"} {
		input := strings.Replace(fixtureScenario, `"kind":"output"`, `"kind":"output","delayMs":`+value, 1)
		if _, err := Decode(strings.NewReader(input)); err != nil {
			t.Fatal("valid bounded native output latency was rejected")
		}
	}
	if _, err := Decode(strings.NewReader(strings.Replace(strings.Replace(fixtureScenario, `"kind":"output"`, `"kind":"output","delayMs":1`, 1), "/v1/responses", "/v1/chat/completions", 1))); err == nil {
		t.Fatal("Responses-only latency was accepted for another native protocol")
	}
	for _, kind := range []string{"http_error", "disconnect", "wait_for_cancel"} {
		step := `"kind":"` + kind + `","delayMs":1`
		if kind == "http_error" {
			step += `,"status":503`
		}
		input := strings.Replace(fixtureScenario, `"kind":"output","text":"not-json"`, step, 1)
		if _, err := Decode(strings.NewReader(input)); err == nil {
			t.Fatal("output latency was accepted on a transport/HTTP fault")
		}
	}
}

func TestScenarioOutputDelayCompletesOrCancelsWithoutReplay(t *testing.T) {
	t.Parallel()
	for _, cancelEarly := range []bool{false, true} {
		name, delay := "complete", "10"
		if cancelEarly {
			name, delay = "cancel", "50"
		}
		t.Run(name, func(t *testing.T) {
			scenario := loadScenario(t, strings.Replace(fixtureScenario, `"kind":"output"`, `"kind":"output","delayMs":`+delay, 1))
			finish, err := scenario.BeginRun("fixture-operation")
			if err != nil {
				t.Fatal(err)
			}
			defer finish()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelEarly {
				cancel()
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"original marker"}`)).WithContext(ctx)
			recorder := httptest.NewRecorder()
			start := time.Now()
			scenario.Provider().ServeHTTP(recorder, request)
			elapsed := time.Since(start)
			if cancelEarly {
				if recorder.Body.Len() != 0 || elapsed > time.Second {
					t.Fatal("cancelled provider latency emitted output or waited for the timer")
				}
			} else if !recorder.Flushed || elapsed < 10*time.Millisecond || recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "not-json") {
				t.Fatal("native output did not retain the explicit provider latency and payload")
			}
			stats := scenario.Stats()
			if stats.ProviderRequestCount != 1 || stats.UnmatchedProviderCount != 0 || stats.Operations["fixture-operation"].ProviderDispatchCount != 1 || stats.Operations["fixture-operation"].ConsumedSteps != 1 {
				t.Fatal("provider latency/cancellation repeated or unconsumed its operation")
			}
		})
	}
}

type cancelOnHeaders struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (writer cancelOnHeaders) Flush() {
	writer.ResponseRecorder.Flush()
	writer.cancel() // A real header signal, never a readiness/timing sleep.
}

func TestScenarioOutputDelayFlushesNativeHeadersBeforeBody(t *testing.T) {
	t.Parallel()
	for _, example := range []struct{ stream, contentType string }{{"false", "application/json"}, {"true", "text/event-stream"}} {
		t.Run(example.stream, func(t *testing.T) {
			scenario := loadScenario(t, strings.Replace(fixtureScenario, `"kind":"output"`, `"kind":"output","delayMs":60000`, 1))
			finish, err := scenario.BeginRun("fixture-operation")
			if err != nil {
				t.Fatal(err)
			}
			defer finish()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"original marker","stream":`+example.stream+`}`)).WithContext(ctx)
			recorder := httptest.NewRecorder()
			scenario.Provider().ServeHTTP(cancelOnHeaders{recorder, cancel}, request)
			if !recorder.Flushed || recorder.Code != http.StatusOK || recorder.Body.Len() != 0 || recorder.Result().Header.Get("Content-Type") != example.contentType {
				t.Fatal("quiet native output did not emit exact HTTP headers before its body wait")
			}
			stats := scenario.Stats()
			if stats.ProviderRequestCount != 1 || stats.Operations["fixture-operation"].ConsumedSteps != 1 || stats.UnmatchedProviderCount != 0 {
				t.Fatal("header-only cancellation replayed or unconsumed the provider step")
			}
		})
	}
}

func TestScenarioNativeFaultsAndGatewayCounting(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"http_error", "disconnect", "wait_for_cancel"} {
		t.Run(kind, func(t *testing.T) {
			step := `"kind":"` + kind + `"`
			if kind == "http_error" {
				step += `,"status":503`
			}
			scenario := loadScenario(t, strings.Replace(fixtureScenario, `"kind":"output","text":"not-json"`, step, 1))
			finish, _ := scenario.BeginRun("fixture-operation")
			defer finish()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			handler := scenario.Provider()
			if kind == "wait_for_cancel" {
				// A deterministic provider-entry signal, not a readiness sleep.
				entered := make(chan struct{})
				handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					close(entered)
					scenario.Provider().ServeHTTP(w, r)
				})
				go func() { <-entered; cancel() }()
			}
			server := httptest.NewServer(handler)
			defer server.Close()
			request, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"original marker"}`))
			client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second}
			defer client.CloseIdleConnections()
			response, err := client.Do(request)
			if response != nil {
				defer response.Body.Close()
			}
			if (kind == "http_error" && (err != nil || response.StatusCode != 503)) || (kind != "http_error" && err == nil) {
				t.Fatalf("fault %s not injected: %v", kind, err)
			}
		})
	}
	scenario := loadScenario(t, fixtureScenario)
	const key = "synthetic-fixture-client-key-not-a-real-credential"
	var received []string
	owner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = append(received, string(body))
		w.WriteHeader(http.StatusAccepted)
	})
	handler := scenario.Gateway(key, owner)
	const run = `{"origin":{"operationId":"fixture-operation"},"profileId":"fixture-profile"}`
	for _, token := range []string{"wrong-key", key} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/run", strings.NewReader(run))
		request.Header.Set("Authorization", "Bearer "+token)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	if !reflect.DeepEqual(received, []string{run, run}) || scenario.Stats().RunPostCount != 1 {
		t.Fatal("fixture changed owner body/authorization or counted an unauthorized Run")
	}
	for _, token := range []string{"", key} {
		request := httptest.NewRequest(http.MethodGet, "/fixture/stats", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if token == "" && response.Code != http.StatusUnauthorized || token == key && response.Code != http.StatusOK {
			t.Fatalf("stats authorization differs: status=%d", response.Code)
		}
	}
}
