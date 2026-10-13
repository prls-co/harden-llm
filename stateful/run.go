package stateful

import (
	"context"
	"encoding/json"
	"errors"
	hardenllm "github.com/prls-co/harden-llm"
	"strings"
	"time"
	"unicode/utf8"
)

type RunInput struct {
	ProfileID       string                         `json:"profileId"`
	ModelID         string                         `json:"modelId,omitempty"`
	SystemPrompt    string                         `json:"systemPrompt,omitempty"`
	UserPrompt      string                         `json:"userPrompt"`
	CallType        hardenllm.CallType             `json:"callType"`
	Schema          json.RawMessage                `json:"schema,omitempty"`
	ReasoningEffort string                         `json:"reasoningEffort,omitempty"`
	WebSearch       bool                           `json:"webSearch,omitempty"`
	ProviderOptions map[string]any                 `json:"providerOptions,omitempty"`
	CacheMode       hardenllm.CacheMode            `json:"cacheMode,omitempty"`
	CacheVersion    string                         `json:"cacheVersion,omitempty"`
	TimeoutMS       int                            `json:"timeoutMs,omitempty"`
	RecoveryPolicy  json.RawMessage                `json:"recoveryPolicy"`
	Origin          hardenllm.Origin               `json:"origin,omitempty"`
	Progress        chan<- hardenllm.ProgressEvent `json:"-"`
	// Ready is an internal admission signal used by the request-bound SSE
	// writer. It is never serialized and is sent exactly once before provider
	// execution/cache lookup begins.
	Ready        chan<- struct{}                  `json:"-"`
	Completed    func(RunOutput, RunState, error) `json:"-"`
	historyWrite historyWriteOptions
}

type RecordedRunOutput struct {
	Search              *hardenllm.SearchResult `json:"search,omitempty"`
	SchemaVersion       int                     `json:"schemaVersion"`
	RunID               string                  `json:"runId"`
	Status              string                  `json:"status"`
	Output              any                     `json:"output"`
	CallID              string                  `json:"callId"`
	TraceID             string                  `json:"traceId"`
	Origin              hardenllm.Origin        `json:"origin,omitempty"`
	SelectedTarget      *ExecutionTarget        `json:"selectedTarget"`
	ResultSource        ResultSource            `json:"resultSource"`
	Accounting          hardenllm.Accounting    `json:"accounting"`
	Attempts            []Attempt               `json:"attempts"`
	Cache               hardenllm.CacheResult   `json:"cache"`
	Artifacts           []ArtifactMetadata      `json:"artifacts"`
	ProviderInvoked     bool                    `json:"providerInvoked"`
	TotalCallDurationMs int64                   `json:"totalCallDurationMs"`
	TotalWaitMs         int64                   `json:"totalWaitMs"`
	TotalActualWaitMs   int64                   `json:"totalActualWaitMs"`
	OverBudgetMs        int64                   `json:"overBudgetMs"`
	UsedRepair          bool                    `json:"usedRepair"`
	Diagnostics         *RunDiagnostics         `json:"diagnostics,omitempty"`
	GenerationTarget    *ExecutionTarget        `json:"generationTarget,omitempty"`
	StopReason          string                  `json:"stopReason,omitempty"`
}

type RunState struct {
	LastRunID   string `json:"lastRunId"`
	LastTraceID string `json:"lastTraceId"`
}

type RunOutput struct {
	RecordedRunOutput
	History            RecordingOutcome `json:"history"`
	ArtifactDiagnostic *Diagnostic      `json:"-"`
}

func cloneRunInput(input RunInput) (RunInput, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return input, validationFailure("run", "Run inputs must be JSON safe.")
	}
	var detached RunInput
	if err = decodeStrict(data, &detached); err != nil {
		return input, validationFailure("run", "Run inputs must be JSON safe.")
	}
	detached.Ready = input.Ready
	detached.Progress = input.Progress
	detached.Completed = input.Completed
	detached.historyWrite = input.historyWrite
	return detached, nil
}
func safeHistoryInput(input RunInput) RunInput {
	remove := func(object map[string]any, key, path string) string {
		if forbiddenDefaultKey.MatchString(key) {
			delete(object, key)
		}
		return ""
	}
	walkOptionFields(input.ProviderOptions, "providerOptions", remove)
	var policy any
	if json.Unmarshal(input.RecoveryPolicy, &policy) == nil {
		walkOptionFields(policy, "recoveryPolicy", remove)
		input.RecoveryPolicy, _ = json.Marshal(policy)
	}
	return input
}

// Run owns reference composition and persistence, never a second execution engine.
func (s *Service) Run(ctx context.Context, hash string, input RunInput) (output RunOutput, state RunState, callErr error) {
	if ctx == nil {
		return output, state, errors.New("stateful: context is required")
	}
	if err := checkScope(hash); err != nil {
		return output, state, err
	}
	runID, err := s.newID()
	if err != nil {
		return output, state, &OperationError{Code: "run_id_failed", Message: "Could not create an execution identifier."}
	}
	started := s.now().UTC()
	duration := 60 * time.Second
	if input.TimeoutMS > 0 && input.TimeoutMS <= 60000 {
		duration = time.Duration(input.TimeoutMS) * time.Millisecond
	}
	callCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	resolved := resolvedRun{}
	result := hardenllm.Result{}
	var capture hardenllm.DiagnosticCapture
	captured := false
	input, callErr = cloneRunInput(input)
	if callErr == nil {
		callErr = validateRunInput(input)
	}
	if callErr == nil {
		resolved, callErr = s.resolveRun(callCtx, hash, input)
	}
	if callErr == nil {
		options := s.effects
		options.Connections = resolved.connections
		options.DefaultConnection = "generation"
		options.Cache = newPostgresCache(s.config.Pool, hash, resolved.cacheNamespace, s.config.CacheTTL, s.now)
		client, err := hardenllm.New(options)
		callErr = err
		if err == nil {
			resolved.request.Context = hardenllm.ObservabilityContext{TaskID: runID, RunID: runID}
			resolved.request.CaptureDiagnostics = func(value hardenllm.DiagnosticCapture) { capture = value; captured = true }
			if input.Ready != nil {
				select {
				case input.Ready <- struct{}{}:
				default:
				}
			}
			result, callErr = client.Call(callCtx, resolved.request)
		}
	}
	finished := s.now().UTC()
	if result.CallID == "" {
		result.CallID, err = s.newID()
		if err != nil {
			return output, state, &OperationError{Code: "run_id_failed", Message: "Could not create a call identifier."}
		}
	}
	if result.TraceID == "" {
		result.TraceID, err = s.newID()
		if err != nil {
			return output, state, &OperationError{Code: "run_id_failed", Message: "Could not create a trace identifier."}
		}
	}
	output.RecordedRunOutput = projectRun(runID, input, resolved, result, callErr, started, finished)
	state = RunState{LastRunID: runID, LastTraceID: output.TraceID}
	if captured {
		refs, diagnostic := s.publishArtifacts(ctx, hash, runID, capture)
		output.Artifacts = refs
		if diagnostic != nil {
			diagnostic.InferenceOutcome = output.Status
			// Reuse the safe typed diagnostic for the caller and correlated logs.
			output.ArtifactDiagnostic = diagnostic
		}
	}
	if input.Completed != nil {
		input.Completed(output, state, callErr)
	}
	output.History = RecordingOutcome{Mode: "disabled", Status: "disabled", RunID: runID}
	if !s.config.History.Enabled {
		return output, state, callErr
	}
	request := struct {
		Input            RunInput                `json:"input"`
		Profile          *ProfileDraft           `json:"profile,omitempty"`
		RecoveryProfiles map[string]ProfileDraft `json:"recoveryProfiles,omitempty"`
	}{Input: safeHistoryInput(input), RecoveryProfiles: map[string]ProfileDraft{}}
	for id, profile := range resolved.profiles {
		draft := profile.ProfileDraft
		if id == input.ProfileID {
			request.Profile = &draft
		} else {
			request.RecoveryProfiles[id] = draft
		}
	}
	requestData, encodeErr := json.Marshal(request)
	resultData, resultEncodeErr := json.Marshal(output.RecordedRunOutput)
	record := HistoryRecord{SchemaVersion: 1, RunID: runID, StartedAt: started, FinishedAt: finished, Status: output.Status, Request: requestData, Result: resultData, Error: ProjectRunError(callErr)}
	record.Summary = historyRunSummary(input, output, started, finished)
	if encodeErr != nil || resultEncodeErr != nil {
		output.History = RecordingOutcome{Mode: "enabled", Status: "not_saved", RunID: runID, Diagnostic: historyDiagnostic(record, "not_saved", historyRecordInvalid(), time.Now())}
		return output, state, callErr
	}
	if input.historyWrite.beforeRecord != nil {
		input.historyWrite.beforeRecord(&record)
	}
	output.History = s.insertHistory(ctx, hash, record, input.historyWrite)
	return output, state, callErr
}

func validateRunInput(input RunInput) error {
	if validateProfileName(input.ProfileID) != nil {
		return validationFailure("profileId", "Select a valid profile ID.")
	}
	if input.UserPrompt == "" || !utf8.ValidString(input.UserPrompt) || !utf8.ValidString(input.SystemPrompt) || !utf8.ValidString(input.ModelID) {
		return validationFailure("userPrompt", "Provide a nonempty UTF-8 prompt and valid text fields.")
	}
	if input.CallType != hardenllm.CallTypeText && input.CallType != hardenllm.CallTypeStructured {
		return validationFailure("callType", "Call type must be text or structured.")
	}
	if len(input.RecoveryPolicy) == 0 || strings.TrimSpace(string(input.RecoveryPolicy)) == "null" {
		return validationFailure("recoveryPolicy", "An explicit recovery policy is required.")
	}
	if len(input.Schema) > 64<<10 || input.CallType == hardenllm.CallTypeStructured && len(input.Schema) == 0 || input.CallType == hardenllm.CallTypeText && len(input.Schema) != 0 && string(input.Schema) != "null" {
		return validationFailure("schema", "Structured calls require an object schema; text calls must omit it.")
	}
	if data, err := json.Marshal(input.ProviderOptions); err != nil || len(data) > 32<<10 {
		return validationFailure("providerOptions", "Request options exceed the 32 KiB JSON bound.")
	}
	values := profileInputs{ModelID: input.ModelID, SystemPrompt: input.SystemPrompt, UserPrompt: input.UserPrompt, CallType: string(input.CallType), Schema: input.Schema, ReasoningEffort: input.ReasoningEffort, ProviderOptions: input.ProviderOptions, CacheMode: string(input.CacheMode), CacheVersion: input.CacheVersion, WebSearch: input.WebSearch, TimeoutMS: input.TimeoutMS, RecoveryPolicy: input.RecoveryPolicy, Origin: input.Origin}
	data, err := json.Marshal(values)
	if err != nil {
		return validationFailure("run", "Run inputs must be JSON safe.")
	}
	return validateInputs(data, Profile{})
}

// ProjectRunError is the one safe application error projection for history and REST.
func ProjectRunError(err error) *RunError {
	if err == nil {
		return nil
	}
	var validation *ValidationError
	if errors.As(err, &validation) {
		return &RunError{Code: validation.Code, Message: "Reference settings are invalid; correct the indicated fields.", FieldErrors: validation.FieldErrors}
	}
	var operation *OperationError
	if errors.As(err, &operation) {
		return &RunError{Code: operation.Code, Message: operation.Message, CurrentRevision: operation.CurrentRevision}
	}
	var core *hardenllm.ValidationError
	if errors.As(err, &core) {
		return &RunError{Code: "invalid_request", Message: "The execution configuration is invalid.", FieldErrors: []FieldError{{Field: core.Field, Message: core.Message}}}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &RunError{Code: "deadline_exceeded", Message: "The execution deadline expired."}
	}
	if errors.Is(err, context.Canceled) {
		return &RunError{Code: "canceled", Message: "The caller canceled execution."}
	}
	return &RunError{Code: "inference_failed", Message: "LLM execution failed; inspect the run's correlated diagnostics."}
}
