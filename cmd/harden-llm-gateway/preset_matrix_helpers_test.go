package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-455 TEST-548 TEST-549
// Shared by the opt-in live matrix and its credential-free regression tests.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/runtime"
	"github.com/prls-co/harden-llm/stateful"
)

func livePresetInput(id string, preset stateful.Preset, mode hardenllm.CallType, search bool) (stateful.RunInput, string, error) {
	var inputs struct {
		ModelID   string          `json:"modelId"`
		Schema    json.RawMessage `json:"schema"`
		Reasoning string          `json:"reasoningEffort"`
		Options   map[string]any  `json:"providerOptions"`
	}
	if err := json.Unmarshal(preset.Inputs, &inputs); err != nil {
		return stateful.RunInput{}, "", err
	}
	var profile stateful.Profile
	if err := json.Unmarshal(preset.Definition, &profile); err != nil {
		return stateful.RunInput{}, "", err
	}
	input := stateful.RunInput{ProfileID: id, ModelID: inputs.ModelID, UserPrompt: `Reply with exactly {"joke":"ok","explanation":"ok"}. No tools. No extra text.`, CallType: mode, Schema: inputs.Schema, ReasoningEffort: inputs.Reasoning, ProviderOptions: inputs.Options, CacheMode: hardenllm.CacheModeOff, TimeoutMS: 60000, RecoveryPolicy: json.RawMessage(`{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`)}
	if mode == hardenllm.CallTypeText {
		input.Schema = nil
	}
	route := "off"
	if search {
		input.WebSearch = true
		input.UserPrompt = "Search the web for the official Go programming language documentation at go.dev/doc. Briefly describe what it covers and cite the source."
		if mode == hardenllm.CallTypeStructured {
			input.UserPrompt += ` Return only the configured JSON object: a short Go-related joke in "joke" and the documentation description with its URL in "explanation".`
		}
		route = "jina"
		if stateful.NativeWebSearchSupported(profile) {
			switch profile.APIInferenceType {
			case "responses", "gemini-generate-content":
				route = "native"
			case "anthropic-messages":
				if mode == hardenllm.CallTypeText {
					route = "native"
				}
			}
		}
	}
	return input, route, nil
}

func livePresetSearchFacts(output stateful.RunOutput, expected string) (map[string]any, error) {
	s := output.Search
	if s == nil || s.Mode != expected {
		return nil, errors.New("missing search evidence or unexpected native/fallback route")
	}
	if err := runtime.ValidateSearchResult(s); err != nil {
		return nil, errors.New("invalid canonical search evidence")
	}
	if expected == "jina" && !s.Executed {
		return nil, errors.New("configured Jina search did not execute")
	}
	observation := "not_observed"
	if s.Executed {
		observation = "observed"
	}
	facts := map[string]any{"searchMode": s.Mode, "searchExecuted": s.Executed, "searchObservation": observation, "searchCallCount": len(s.Calls), "sourceCount": len(s.Sources), "citationCount": len(s.Citations), "incomplete": s.Incomplete, "warnings": s.Warnings, "citationValidation": "no_citations"}
	if len(s.Citations) > 0 {
		// RunOutput intentionally does not expose the original assistant text
		// for structured output. Never marshal JSON and claim it is that text.
		facts["citationValidation"] = "original_text_unavailable"
		if text, ok := output.Output.(string); ok {
			sum := sha256.Sum256([]byte(text))
			if hex.EncodeToString(sum[:]) != s.CitationTextSHA256 {
				return facts, errors.New("citation hash does not match the returned text")
			}
			for _, citation := range s.Citations {
				if citation.EndIndex > utf8.RuneCountInString(text) {
					return facts, errors.New("citation range exceeds the returned text")
				}
			}
			facts["citationValidation"] = "verified"
		}
	}
	return facts, nil
}

func createLivePresetReport(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.WriteString("[]\n"); err != nil {
		return err
	}
	return f.Sync()
}

func writeLivePresetReport(path string, rows []map[string]any) error {
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".preset-report-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func sha256HexBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newLivePresetRow(id string, preset stateful.Preset, input stateful.RunInput, route string) map[string]any {
	var profile stateful.Profile
	_ = json.Unmarshal(preset.Definition, &profile)
	optionsJSON, _ := json.Marshal(input.ProviderOptions)
	configIdentity := map[string]any{
		"promptSha256":     sha256HexBytes([]byte(input.UserPrompt)),
		"optionsSha256":    sha256HexBytes(optionsJSON),
		"definitionSha256": sha256HexBytes(preset.Definition),
		"timeoutMs":        input.TimeoutMS,
		"reasoningEffort":  input.ReasoningEffort,
		"cacheMode":        string(input.CacheMode),
	}
	if input.SystemPrompt != "" {
		configIdentity["systemPromptSha256"] = sha256HexBytes([]byte(input.SystemPrompt))
	}
	if len(input.Schema) > 0 {
		configIdentity["schemaSha256"] = sha256HexBytes(input.Schema)
	}
	return map[string]any{
		"id":                 id,
		"mode":               string(input.CallType),
		"model":              input.ModelID,
		"provider":           profile.Provider,
		"protocol":           profile.APIInferenceType,
		"status":             "planned",
		"searchRequested":    input.WebSearch,
		"expectedSearchMode": route,
		"configIdentity":     configIdentity,
	}
}

func recordLivePresetOutcome(row map[string]any, input stateful.RunInput, output stateful.RunOutput, callErr error, finishedAt time.Time) bool {
	row["status"], row["providerInvoked"], row["attemptCount"], row["usedRepair"] = output.Status, output.ProviderInvoked, len(output.Attempts), output.UsedRepair
	row["durationMs"], row["cacheServed"], row["finishedAt"] = output.TotalCallDurationMs, output.Cache.Served, finishedAt.UTC().Format(time.RFC3339Nano)
	statuses := []int{}
	attemptRows := make([]map[string]any, 0, len(output.Attempts))
	for _, attempt := range output.Attempts {
		statuses = append(statuses, attempt.HTTPStatus)
		entry := map[string]any{
			"number":            attempt.Number,
			"profileId":         attempt.ProfileID,
			"target":            attempt.Target,
			"category":          attempt.Category,
			"httpStatus":        attempt.HTTPStatus,
			"code":              attempt.Code,
			"type":              attempt.Type,
			"providerRequestId": attempt.ProviderRequestID,
			"retryable":         attempt.Retryable,
			"waitMs":            attempt.Wait.Milliseconds(),
			"durationMs":        attempt.Duration.Milliseconds(),
			"repair":            attempt.Repair,
			"providerUsed":      attempt.ProviderUsed,
			"stage":             attempt.Stage,
			"branch":            attempt.Branch,
			"reasoningEffort":   attempt.ReasoningEffort,
			"dispatchObserved":  attempt.DispatchObserved,
		}
		if attempt.Stream != nil {
			entry["stream"] = attempt.Stream
		}
		attemptRows = append(attemptRows, entry)
		if attempt.Code != "" {
			row["diagnosticCode"] = attempt.Code
		}
		if attempt.Category != "" {
			row["diagnosticCategory"] = attempt.Category
		}
		if attempt.Type != "" {
			row["diagnosticType"] = attempt.Type
		}
		if attempt.Stage != "" {
			row["diagnosticStage"] = attempt.Stage
		}
		if attempt.ProviderRequestID != "" {
			row["providerRequestId"] = attempt.ProviderRequestID
		}
		if callErr != nil && (attempt.DispatchObserved || attempt.ProviderUsed) && (attempt.Category == "network" || attempt.Category == "timeout" || attempt.Category == "canceled") {
			row["status"] = "unconfirmed"
		}
	}
	row["httpStatuses"] = statuses
	row["attempts"] = attemptRows
	if len(output.OperationDiagnostics) > 0 {
		row["operationDiagnostics"] = output.OperationDiagnostics
		last := output.OperationDiagnostics[len(output.OperationDiagnostics)-1]
		if row["diagnosticCode"] == nil {
			if code, ok := last["error_code"].(string); ok && code != "" {
				row["diagnosticCode"] = code
			}
		}
		if row["diagnosticCategory"] == nil {
			if cat, ok := last["error_category"].(string); ok && cat != "" {
				row["diagnosticCategory"] = cat
			}
		}
	}
	if row["diagnosticCode"] == nil && callErr != nil {
		var providerErr *hardenllm.ProviderError
		var opErr *stateful.OperationError
		var valErr *stateful.ValidationError
		var coreValErr *hardenllm.ValidationError
		switch {
		case errors.As(callErr, &providerErr):
			row["diagnosticCode"] = providerErr.Code
			row["diagnosticCategory"] = string(providerErr.Category)
			if providerErr.Type != "" {
				row["diagnosticType"] = providerErr.Type
			}
			if providerErr.ProviderRequestID != "" {
				row["providerRequestId"] = providerErr.ProviderRequestID
			}
		case errors.As(callErr, &opErr):
			row["diagnosticCode"] = opErr.Code
			row["diagnosticCategory"] = "operation"
		case errors.As(callErr, &valErr):
			row["diagnosticCode"] = valErr.Code
			row["diagnosticCategory"] = "validation"
		case errors.As(callErr, &coreValErr):
			row["diagnosticCode"] = "invalid_request"
			row["diagnosticCategory"] = "validation"
		case errors.Is(callErr, context.DeadlineExceeded):
			row["diagnosticCode"] = "DEADLINE_EXCEEDED"
			row["diagnosticCategory"] = "timeout"
		case errors.Is(callErr, context.Canceled):
			row["diagnosticCode"] = "CANCELED"
			row["diagnosticCategory"] = "canceled"
		}
	}
	captureStatus := "not_applicable"
	operationAttempted := output.ProviderInvoked || len(output.Attempts) > 0 || (output.Search != nil && output.Search.Executed)
	if output.ArtifactDiagnostic != nil {
		captureStatus = "publication_failed"
		row["artifactDiagnosticCode"] = output.ArtifactDiagnostic.Code
	} else if output.DiagnosticsClipped {
		captureStatus = "clipped"
	} else if len(output.OperationDiagnostics) > 0 {
		captureStatus = "complete"
	} else if operationAttempted && !output.Cache.Served {
		captureStatus = "missing"
	}
	row["diagnosticCaptureStatus"] = captureStatus

	var evidenceErr error
	if input.WebSearch && (callErr == nil || output.Search != nil) {
		facts, err := livePresetSearchFacts(output, row["expectedSearchMode"].(string))
		for key, value := range facts {
			row[key] = value
		}
		evidenceErr = err
		if err != nil {
			row["evidenceError"] = err.Error()
		}
	}
	if callErr != nil {
		problem := stateful.ProjectRunError(callErr)
		row["errorCode"] = problem.Code
		if problem.Code == "execution_unconfirmed" {
			row["status"] = "unconfirmed"
		}
		return false
	}
	if output.Status != "succeeded" || !output.ProviderInvoked || len(output.Attempts) != 1 || output.UsedRepair || output.Cache.Served {
		row["status"] = "invalid_execution"
		return false
	}
	if evidenceErr != nil {
		row["status"] = "invalid_evidence"
		return false
	}
	if captureStatus != "complete" {
		row["status"] = "invalid_diagnostics"
		return false
	}
	return true
}
