package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-548 TEST-549

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestPresetMatrixCauseRetentionAndCaptureStatus(t *testing.T) {
	t.Parallel()
	catalog, err := stateful.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	preset := catalog["CPA claude-haiku-4-5-20251001"]
	input, route, err := livePresetInput("CPA claude-haiku-4-5-20251001", preset, hardenllm.CallTypeStructured, true)
	if err != nil {
		t.Fatal(err)
	}
	target := stateful.ExecutionTarget{ProfileID: input.ProfileID, Provider: "cpa", Protocol: "anthropic.messages", Endpoint: "https://cpa.prls.co/v1", ModelID: input.ModelID}
	started := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	finished := started.Add(1500 * time.Millisecond)

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
				OperationDiagnostics: []map[string]any{{"msg": "provider operation observed", "stage": "provider_model", "http_status": 200, "error_code": "STRUCTURED_PARSE"}},
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
				OperationDiagnostics: []map[string]any{{"msg": "provider operation observed", "stage": "provider_model", "http_status": 200, "error_code": "STRUCTURED_SCHEMA"}},
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
				OperationDiagnostics: []map[string]any{{"msg": "provider operation observed", "stage": "provider_model", "http_status": 200, "error_code": "ACCOUNTING_INVALID"}},
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
				OperationDiagnostics: []map[string]any{{"msg": "provider operation observed", "stage": "provider_model", "http_status": 200, "error_code": "empty_response", "reasoning_present": true, "final_text_present": false}},
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
				OperationDiagnostics: []map[string]any{{"msg": "provider operation observed", "stage": "provider_model", "http_status": 400, "error_code": "model_not_found"}},
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
				OperationDiagnostics: []map[string]any{{"msg": "web search operation observed", "stage": "web_search", "provider": "jina", "http_status": 200, "received_bytes": int64(524289), "error_code": "RESPONSE_TOO_LARGE"}},
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
				OperationDiagnostics: []map[string]any{{"msg": "provider operation observed", "stage": "provider_model", "dispatched": true, "headers_received": false, "error_code": "ETIMEDOUT", "error_class": "timeout", "error_phase": "response_headers"}},
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
			wantStatus:        "invalid_diagnostics",
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
			row := newLivePresetRow(input.ProfileID, preset, input, route)
			ok := recordLivePresetOutcome(row, input, tc.output, tc.callErr, finished)
			if ok != tc.wantValid || row["status"] != tc.wantStatus || row["diagnosticCaptureStatus"] != tc.wantCaptureStatus {
				t.Fatalf("row=%#v ok=%v wantValid=%v", row, ok, tc.wantValid)
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
