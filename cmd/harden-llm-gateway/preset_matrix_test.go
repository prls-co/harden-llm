package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-548 TEST-549

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/providers"
	"github.com/prls-co/harden-llm/internal/runtime"
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
			baseline, _, off, err := livePresetInput(id, preset, mode, false)
			if err != nil || off != "off" || baseline.WebSearch {
				t.Fatalf("%s/%s baseline: %s %v", id, mode, off, err)
			}
			searched, _, route, err := livePresetInput(id, preset, mode, true)
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

// The matrix consumes the in-process operation records before JSON encoding.
func matrixOperationFixture(stage string, status int, category string) map[string]any {
	rec := map[string]any{
		"stage": stage, "attempt_number": 1, "http_status": status, "headers_received": status > 0,
		"dispatched": true, "error_category": category, "received_bytes": int64(0), "max_response_bytes": int64(512 << 10),
		"duration_ms": int64(1), "parent_context_expired": false, "request_context_expired": false,
	}
	if status > 0 {
		rec["headers_ms"] = int64(1)
		rec["received_bytes"] = int64(16)
	}
	if stage == "web_search" {
		rec["msg"] = "web search operation observed"
		rec["provider"] = "jina"
		rec["protocol"] = "jina.search"
		rec["timeout_ms"] = int64(20000)
	} else {
		rec["msg"] = "provider operation observed"
		rec["provider"] = "cpa"
		rec["protocol"] = "anthropic.messages"
		rec["model"] = "claude-haiku-4-5-20251001"
		rec["call_type"] = "structured"
		rec["search_mode"] = "jina"
		rec["event_count"] = 0
		rec["output_bytes"] = int64(2)
		rec["output_code_points"] = int64(2)
		if status > 0 {
			rec["terminal_state"] = "not_streaming"
			rec["final_text_present"] = true
			rec["final_text_bytes"] = int64(2)
			rec["reasoning_present"] = false
			rec["tool_call_count"] = 0
		}
	}
	return rec
}
func matrixFallbackFixture(status int, category, code string) []map[string]any {
	model := matrixOperationFixture("provider_model", status, category)
	if code != "" {
		model["error_code"] = code
	}
	if code == "empty_response" {
		model["reasoning_present"] = true
		model["final_text_present"] = false
		model["final_text_bytes"] = int64(0)
	}
	if code == "ETIMEDOUT" {
		model["error_class"] = "timeout"
		model["error_phase"] = "response_headers"
	}
	return []map[string]any{matrixOperationFixture("web_search", 200, "success"), model}
}

func TestPresetMatrixCauseRetentionAndCaptureStatus(t *testing.T) {
	t.Parallel()
	catalog, err := stateful.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	preset := catalog["CPA claude-haiku-4-5-20251001"]
	input, profile, route, err := livePresetInput("CPA claude-haiku-4-5-20251001", preset, hardenllm.CallTypeStructured, true)
	if err != nil {
		t.Fatal(err)
	}
	target := stateful.ExecutionTarget{ProfileID: input.ProfileID, Provider: "cpa", Protocol: "anthropic.messages", Endpoint: "https://cpa.prls.co/v1", ModelID: input.ModelID}
	started := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	finished := started.Add(1500 * time.Millisecond)

	oversize := matrixOperationFixture("web_search", 200, "other")
	oversize["error_code"] = "RESPONSE_TOO_LARGE"
	oversize["received_bytes"] = int64(524289)
	cases := []struct {
		name               string
		output             stateful.RunOutput
		callErr            error
		wantStatus         string
		wantErrorCode      string
		wantDiagnosticCode string
		wantCategory       string
		wantCaptureStatus  string
		wantValid          bool
	}{
		{
			name: "structured parse failure",
			output: stateful.RunOutput{
				RecordedRunOutput: stateful.RecordedRunOutput{
					Status: "failed", ProviderInvoked: true, TotalCallDurationMs: 1500,
					Attempts: []stateful.Attempt{{Number: 1, ProfileID: input.ProfileID, Target: target, Category: "parse_error", Code: "STRUCTURED_PARSE", HTTPStatus: 200, DispatchObserved: true, ProviderUsed: true, Duration: 1500 * time.Millisecond, StartedAt: &started, FinishedAt: &finished}},
				},
				OperationDiagnostics: matrixFallbackFixture(200, "parse_error", "STRUCTURED_PARSE"),
			},
			callErr:            &hardenllm.ProviderError{Code: "STRUCTURED_PARSE", Category: "parse_error", Err: errors.New("parse failed")},
			wantStatus:         "failed",
			wantErrorCode:      "inference_failed",
			wantDiagnosticCode: "STRUCTURED_PARSE",
			wantCategory:       "parse_error",
			wantCaptureStatus:  "complete",
			wantValid:          false,
		},
		{
			name: "structured schema failure",
			output: stateful.RunOutput{
				RecordedRunOutput: stateful.RecordedRunOutput{
					Status: "failed", ProviderInvoked: true, TotalCallDurationMs: 1200,
					Attempts: []stateful.Attempt{{Number: 1, ProfileID: input.ProfileID, Target: target, Category: "parse_error", Code: "STRUCTURED_SCHEMA", HTTPStatus: 200, DispatchObserved: true, ProviderUsed: true, Duration: 1200 * time.Millisecond}},
				},
				OperationDiagnostics: matrixFallbackFixture(200, "success", ""),
			},
			callErr:            &hardenllm.ProviderError{Code: "STRUCTURED_SCHEMA", Category: "parse_error", Err: errors.New("schema failed")},
			wantStatus:         "failed",
			wantErrorCode:      "inference_failed",
			wantDiagnosticCode: "STRUCTURED_SCHEMA",
			wantCategory:       "parse_error",
			wantCaptureStatus:  "complete",
			wantValid:          false,
		},
		{
			name: "accounting invalid failure",
			output: stateful.RunOutput{
				RecordedRunOutput: stateful.RecordedRunOutput{
					Status: "failed", ProviderInvoked: true, TotalCallDurationMs: 900,
					Attempts: []stateful.Attempt{{Number: 1, ProfileID: input.ProfileID, Target: target, Category: "other", Code: "ACCOUNTING_INVALID", HTTPStatus: 200, DispatchObserved: true, ProviderUsed: true}},
				},
				OperationDiagnostics: matrixFallbackFixture(200, "success", ""),
			},
			callErr:            &hardenllm.ProviderError{Code: "ACCOUNTING_INVALID", Category: "other", Err: errors.New("accounting invalid")},
			wantStatus:         "failed",
			wantErrorCode:      "inference_failed",
			wantDiagnosticCode: "ACCOUNTING_INVALID",
			wantCategory:       "other",
			wantCaptureStatus:  "complete",
			wantValid:          false,
		},
		{
			name: "reasoning-only empty output",
			output: stateful.RunOutput{
				RecordedRunOutput: stateful.RecordedRunOutput{
					Status: "failed", ProviderInvoked: true, TotalCallDurationMs: 800,
					Attempts: []stateful.Attempt{{Number: 1, ProfileID: input.ProfileID, Target: target, Category: "empty_response", Code: "empty_response", HTTPStatus: 200, DispatchObserved: true, ProviderUsed: true}},
				},
				OperationDiagnostics: matrixFallbackFixture(200, "empty_response", "empty_response"),
			},
			callErr:            &hardenllm.ProviderError{Code: "empty_response", Category: "empty_response", Err: errors.New("empty response")},
			wantStatus:         "failed",
			wantErrorCode:      "inference_failed",
			wantDiagnosticCode: "empty_response",
			wantCategory:       "empty_response",
			wantCaptureStatus:  "complete",
			wantValid:          false,
		},
		{
			name: "upstream HTTP 400 model_not_found rejection",
			output: stateful.RunOutput{
				RecordedRunOutput: stateful.RecordedRunOutput{
					Status: "failed", ProviderInvoked: true, TotalCallDurationMs: 300,
					Attempts: []stateful.Attempt{{Number: 1, ProfileID: input.ProfileID, Target: target, Category: "other", Code: "model_not_found", Type: "invalid_request_error", HTTPStatus: 400, DispatchObserved: true, ProviderUsed: true}},
				},
				OperationDiagnostics: matrixFallbackFixture(400, "other", "model_not_found"),
			},
			callErr:            &hardenllm.ProviderError{Code: "model_not_found", Type: "invalid_request_error", Status: 400, Category: "other", Err: errors.New("HTTP 400")},
			wantStatus:         "failed",
			wantErrorCode:      "inference_failed",
			wantDiagnosticCode: "model_not_found",
			wantCategory:       "other",
			wantCaptureStatus:  "complete",
			wantValid:          false,
		},
		{
			name: "Jina response too large before model",
			output: stateful.RunOutput{
				RecordedRunOutput: stateful.RecordedRunOutput{
					Status: "failed", ProviderInvoked: false, TotalCallDurationMs: 3700,
					Attempts: []stateful.Attempt{{Number: 1, ProfileID: input.ProfileID, Target: target, Category: "other", Code: "RESPONSE_TOO_LARGE", HTTPStatus: 0, DispatchObserved: false, ProviderUsed: false}},
				},
				OperationDiagnostics: []map[string]any{oversize},
			},
			callErr:            &hardenllm.ProviderError{Code: "RESPONSE_TOO_LARGE", Category: "other", Err: errors.New("response too large")},
			wantStatus:         "failed",
			wantErrorCode:      "inference_failed",
			wantDiagnosticCode: "RESPONSE_TOO_LARGE",
			wantCategory:       "other",
			wantCaptureStatus:  "complete",
			wantValid:          false,
		},
		{
			name: "dispatched header timeout remains unconfirmed",
			output: stateful.RunOutput{
				RecordedRunOutput: stateful.RecordedRunOutput{
					Status: "failed", ProviderInvoked: true, TotalCallDurationMs: 30032,
					Attempts: []stateful.Attempt{{Number: 1, ProfileID: input.ProfileID, Target: target, Category: "network", Code: "ETIMEDOUT", HTTPStatus: 0, DispatchObserved: true, ProviderUsed: true}},
				},
				OperationDiagnostics: matrixFallbackFixture(0, "network", "ETIMEDOUT"),
			},
			callErr:            &hardenllm.ProviderError{Code: "ETIMEDOUT", Category: "network", Err: errors.New("timed out")},
			wantStatus:         "unconfirmed",
			wantErrorCode:      "inference_failed",
			wantDiagnosticCode: "ETIMEDOUT",
			wantCategory:       "network",
			wantCaptureStatus:  "complete",
			wantValid:          false,
		},
		{
			name: "missing diagnostics invalidates nominal pass without fabricating failure errorCode",
			output: stateful.RunOutput{
				RecordedRunOutput: stateful.RecordedRunOutput{
					Status: "succeeded", ProviderInvoked: true, TotalCallDurationMs: 500,
					Output:   map[string]any{"joke": "ok", "explanation": "https://go.dev/doc/"},
					Search:   &hardenllm.SearchResult{Mode: route, Executed: true, Sources: []hardenllm.SearchSource{{URL: "https://go.dev/doc/"}}, CostStatus: "unavailable"},
					Attempts: []stateful.Attempt{{Number: 1, ProfileID: input.ProfileID, Target: target, Category: "success", HTTPStatus: 200, DispatchObserved: true, ProviderUsed: true}},
				},
			},
			callErr:           nil,
			wantStatus:        "succeeded",
			wantCaptureStatus: "missing",
			wantValid:         false,
		},
		{
			name: "clipped diagnostics on failed call preserves failed status",
			output: stateful.RunOutput{
				RecordedRunOutput: stateful.RecordedRunOutput{
					Status: "failed", ProviderInvoked: true, TotalCallDurationMs: 500,
					Attempts: []stateful.Attempt{{Number: 1, ProfileID: input.ProfileID, Target: target, Category: "other", Code: "empty_response", HTTPStatus: 200, DispatchObserved: true, ProviderUsed: true}},
				},
				OperationDiagnostics: []map[string]any{{"msg": "provider operation observed", "stage": "provider_model"}},
				DiagnosticsClipped:   true,
			},
			callErr:            context.DeadlineExceeded,
			wantStatus:         "failed",
			wantErrorCode:      "deadline_exceeded",
			wantDiagnosticCode: "empty_response",
			wantCategory:       "other",
			wantCaptureStatus:  "clipped",
			wantValid:          false,
		},
	}

	seenCodes := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := newLivePresetRow(preset, profile, input, route)
			tc.output.RunID, tc.output.CallID, tc.output.TraceID = "fixture-run", "fixture-call", "fixture-trace"
			ok := recordLivePresetOutcome(row, input, tc.output, tc.callErr, finished)
			if ok != tc.wantValid || row["status"] != tc.wantStatus || row["diagnosticCaptureStatus"] != tc.wantCaptureStatus {
				t.Fatalf("row=%#v ok=%v wantValid=%v", row, ok, tc.wantValid)
			}
			wantVerdict := "failed"
			if tc.callErr == nil {
				wantVerdict = "invalid_diagnostics"
			}
			if row["qualificationStatus"] != wantVerdict {
				t.Fatalf("qualification=%v want=%s", row["qualificationStatus"], wantVerdict)
			}
			if tc.wantErrorCode != "" && row["errorCode"] != tc.wantErrorCode {
				t.Fatalf("public errorCode changed: got=%v want=%s", row["errorCode"], tc.wantErrorCode)
			}
			if tc.wantDiagnosticCode != "" {
				if row["diagnosticCode"] != tc.wantDiagnosticCode || row["diagnosticCategory"] != tc.wantCategory {
					t.Fatalf("diagnostic cause lost: got=%v/%v want=%s/%s", row["diagnosticCode"], row["diagnosticCategory"], tc.wantDiagnosticCode, tc.wantCategory)
				}
				seenCodes[tc.wantDiagnosticCode] = true
			}
			cfg, ok := row["configIdentity"].(map[string]any)
			if !ok || cfg["promptSha256"] == "" || cfg["definitionSha256"] == "" || cfg["optionsSha256"] == "" {
				t.Fatalf("configIdentity missing from row: %#v", row["configIdentity"])
			}
		})
	}
	for _, required := range []string{"STRUCTURED_PARSE", "STRUCTURED_SCHEMA", "ACCOUNTING_INVALID", "empty_response", "model_not_found", "RESPONSE_TOO_LARGE", "ETIMEDOUT"} {
		if !seenCodes[required] {
			t.Fatalf("missing cause witness %s", required)
		}
	}
}

func TestPresetMatrixValidatesOperationSequence(t *testing.T) {
	t.Parallel()
	catalog, err := stateful.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	const id = "CPA claude-haiku-4-5-20251001"
	p := catalog[id]
	input, profile, route, err := livePresetInput(id, p, hardenllm.CallTypeStructured, true)
	if err != nil {
		t.Fatal(err)
	}
	target := stateful.ExecutionTarget{Provider: "cpa", ModelID: input.ModelID, Protocol: "anthropic.messages"}
	base := stateful.RecordedRunOutput{Status: "succeeded", RunID: "run1", CallID: "call1", TraceID: "trace1", ProviderInvoked: true, Attempts: []stateful.Attempt{{Number: 1, Category: "success", Target: target, HTTPStatus: 200, ProviderUsed: true, DispatchObserved: true}}, Search: &hardenllm.SearchResult{Mode: "jina", Executed: true, Sources: []hardenllm.SearchSource{}, CostStatus: "unavailable"}}
	pair := matrixFallbackFixture(200, "success", "")
	search, model := pair[0], pair[1]
	wrong := maps.Clone(model)
	wrong["attempt_number"] = 2
	contradiction := maps.Clone(model)
	contradiction["dispatched"] = false
	absent := maps.Clone(model)
	delete(absent, "headers_received")
	noShape := maps.Clone(model)
	delete(noShape, "final_text_bytes")
	wrongStatus := maps.Clone(model)
	wrongStatus["http_status"] = 201
	noDuration := maps.Clone(search)
	delete(noDuration, "duration_ms")
	for _, mode := range []string{"native", "off"} {
		t.Run(mode, func(t *testing.T) {
			in := input
			in.WebSearch = mode == "native"
			out := stateful.RunOutput{RecordedRunOutput: base}
			m := maps.Clone(model)
			delete(m, "search_mode")
			out.Search = nil
			if in.WebSearch {
				m["search_mode"] = mode
				out.Search = &hardenllm.SearchResult{Mode: mode, Sources: []hardenllm.SearchSource{}, CostStatus: "unavailable"}
			}
			out.OperationDiagnostics = []map[string]any{m}
			row := newLivePresetRow(p, profile, in, mode)
			if !recordLivePresetOutcome(row, in, out, nil, time.Now()) || row["qualificationStatus"] != "passed" {
				t.Fatalf("%s generation without search invocation failed qualification: %v", mode, row)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		records []map[string]any
		capture string
	}{
		{"complete", pair, "complete"}, {"none", nil, "missing"}, {"missing model", pair[:1], "incomplete"}, {"missing search", pair[1:], "incomplete"},
		{"duplicated", []map[string]any{search, search, model}, "incomplete"}, {"reordered", []map[string]any{model, search}, "incomplete"},
		{"wrong attempt", []map[string]any{search, wrong}, "incomplete"}, {"dispatch contradiction", []map[string]any{search, contradiction}, "incomplete"},
		{"missing terminal fact", []map[string]any{search, absent}, "incomplete"}, {"missing response shape", []map[string]any{search, noShape}, "incomplete"},
		{"conflicting model status", []map[string]any{search, wrongStatus}, "incomplete"},
		{"missing search latency", []map[string]any{noDuration, model}, "incomplete"},
		{"missing correlation", pair, "incomplete"}, {"clipped", pair, "clipped"}, {"publication failed", pair, "publication_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := stateful.RunOutput{RecordedRunOutput: base, OperationDiagnostics: tc.records, DiagnosticsClipped: tc.capture == "clipped"}
			if tc.name == "missing correlation" {
				out.TraceID = ""
			}
			if tc.capture == "publication_failed" {
				out.ArtifactDiagnostic = &stateful.Diagnostic{Code: "artifact_write_failed"}
			}
			row := newLivePresetRow(p, profile, input, route)
			passed := recordLivePresetOutcome(row, input, out, nil, time.Now())
			path := filepath.Join(t.TempDir(), "report.json")
			if err := writeLivePresetReport(path, []map[string]any{row}); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			if err = json.Unmarshal(b, &rows); err != nil {
				t.Fatal(err)
			}
			r := rows[0]
			verdict := "invalid_diagnostics"
			if tc.capture == "complete" {
				verdict = "passed"
			}
			if r["status"] != "succeeded" || r["runId"] != base.RunID || r["traceId"] != out.TraceID || r["callId"] != base.CallID || r["qualificationStatus"] != verdict || r["diagnosticCaptureStatus"] != tc.capture || passed != (verdict == "passed") {
				t.Fatalf("status=%v qualification=%v capture=%v IDs=%v/%v/%v passed=%t", r["status"], r["qualificationStatus"], r["diagnosticCaptureStatus"], r["runId"], r["callId"], r["traceId"], passed)
			}
		})
	}
}

func TestPresetMatrixRejectsMalformedDefinition(t *testing.T) {
	t.Parallel()
	for _, def := range []string{"{", "null", "{}"} {
		p := stateful.Preset{Definition: json.RawMessage(def), Inputs: json.RawMessage("{}")}
		if _, _, _, err := livePresetInput("fixture", p, hardenllm.CallTypeText, false); err == nil {
			t.Errorf("accepted malformed preset definition %q", def)
		}
	}
}

func TestPresetMatrixRetainsMissingJinaCredential(t *testing.T) {
	t.Parallel()
	router, err := providers.NewRouter(providers.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, capture := providers.WithOperationDiagnostics(t.Context())
	ctx = providers.WithDiagnosticAttempt(ctx, 1, nil)
	conn := runtime.Connection{ID: "fixture", Provider: "fixture", APIInferenceType: "chat-completions", BaseURL: "https://example.invalid"}
	call := runtime.Call{ModelID: "fixture", CallType: "text", WebSearch: true, Messages: []runtime.Message{{Role: "user", Content: json.RawMessage(`"query"`)}}}
	op, err := router.Prepare(ctx, conn, runtime.Credential{APIKey: "synthetic"}, call)
	if err != nil {
		t.Fatal(err)
	}
	out, err := router.Execute(ctx, op)
	records, _, _ := capture.Snapshot()
	if err == nil || out.ProviderDispatched || len(records) != 1 || records[0]["error_code"] != "WEB_SEARCH_NOT_CONFIGURED" || !matrixTerminalFacts(records[0], "web_search", 1) {
		t.Fatalf("missing credential: records=%v err=%v", records, err)
	}
}

func TestPresetMatrixStreamTerminalFacts(t *testing.T) {
	t.Parallel()
	const complete = "data: " + `{"type":"response.completed","response":{"output_text":"ok"}}` + "\n\ndata: [DONE]\n\n"
	for _, tc := range []struct {
		name, contentType, body, terminal, code string
		limit                                   int64
	}{
		{"sniffed success", "application/json", complete, "completed", "", 1024},
		{"sniffed delta", "text/plain", "data: " + `{"type":"response.output_text.delta","delta":"ok"}` + "\n\n" + complete, "completed", "", 1024},
		{"sniffed normalization error", "text/plain", "data: " + `{"type":"response.completed","response":{"output_text":""}}` + "\n\n", "completed", "empty_response", 1024},
		{"sniffed incomplete", "text/plain", "data: " + `{"type":"response.incomplete","response":{"status":"incomplete"}}`, "incomplete", "COMPLETION_INCOMPLETE", 1024},
		{"sniffed error after completed", "text/plain", complete + "data: " + `{"type":"response.failed","error":{"code":"fixture_error"}}` + "\n\n", "failed", "fixture_error", 1024},
		{"sniffed error", "text/plain", "data: " + `{"type":"response.failed","error":{"code":"fixture_error"}}` + "\n\n", "failed", "fixture_error", 1024},
		{"stream overflow", "text/event-stream", complete, "missing", "RESPONSE_TOO_LARGE", 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			pool := x509.NewCertPool()
			pool.AddCert(server.Certificate())
			router, err := providers.NewRouter(providers.Config{MaxResponseBytes: tc.limit, EndpointPolicy: providers.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, capture := providers.WithOperationDiagnostics(t.Context())
			ctx = providers.WithDiagnosticAttempt(ctx, 1, nil)
			conn := runtime.Connection{ID: "test", Provider: "openai", APIInferenceType: "responses", BaseURL: server.URL}
			call := runtime.Call{ModelID: "test", CallType: "text", Messages: []runtime.Message{{Role: "user", Content: json.RawMessage(`"hello"`)}}}
			op, err := router.Prepare(ctx, conn, runtime.Credential{APIKey: "fixture"}, call)
			if err != nil {
				t.Fatal(err)
			}
			result, err := router.Execute(ctx, op)
			records, _, clipped := capture.Snapshot()
			wantBytes := min(int64(len(tc.body)), tc.limit)
			if (err != nil) != (tc.code != "") || clipped || len(records) != 1 ||
				result.Stream.ReceivedBytes != wantBytes || result.Stream.TerminalState != tc.terminal ||
				records[0]["received_bytes"] != wantBytes || !matrixTerminalFacts(records[0], "provider_model", 1) {
				t.Fatalf("stream=%+v records=%v err=%v", result.Stream, records, err)
			}
			if tc.code != "" && records[0]["error_code"] != tc.code {
				t.Fatalf("error code=%v", records[0]["error_code"])
			}
			if tc.contentType != "text/event-stream" && (result.Stream.FirstEventMs != nil || result.Stream.LastEventAt != nil ||
				result.Stream.FirstOutputMs != nil || result.Stream.LastOutputAt != nil) {
				t.Fatalf("buffered body has unobserved arrival times: %+v", result.Stream)
			}
		})
	}
}

func TestConfigurationRedactorPreservesRoutingFacts(t *testing.T) {
	t.Parallel()
	config := serverConfig{connections: []hardenllm.Connection{{Provider: "cpa", Headers: map[string]string{"X-Provider": "cpa", "X-Use-Cache": "1", "X-Custom-Auth": "fixture-header-value", "X-Unknown": "fixture-other-value"}}}}
	got := configurationRedactor(config).Text("cpa request-123 fixture-header-value fixture-other-value")
	if got != "cpa request-123 [REDACTED] [REDACTED]" {
		t.Fatal("redaction lost safe routing facts or exposed a custom credential")
	}
}
