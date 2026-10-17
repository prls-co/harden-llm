// Package httpclient consumes Harden's published key-scoped reference API.
// It performs HTTP encoding/validation only, never provider execution, retries,
// profile persistence or product workflow recovery.
package httpclient

import (
	"encoding/json"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

// RunInput is the reference RunInput wire contract, not the engine Request.
// Account ownership and secrets are deliberately absent.
type RunInput struct {
	ProfileID       string              `json:"profileId"`
	ModelID         string              `json:"modelId,omitempty"`
	SystemPrompt    string              `json:"systemPrompt,omitempty"`
	UserPrompt      string              `json:"userPrompt"`
	CallType        hardenllm.CallType  `json:"callType"`
	Schema          json.RawMessage     `json:"schema,omitempty"`
	ReasoningEffort string              `json:"reasoningEffort,omitempty"`
	WebSearch       bool                `json:"webSearch,omitempty"`
	ProviderOptions map[string]any      `json:"providerOptions,omitempty"`
	CacheMode       hardenllm.CacheMode `json:"cacheMode,omitempty"`
	CacheVersion    string              `json:"cacheVersion,omitempty"`
	TimeoutMS       int                 `json:"timeoutMs,omitempty"`
	RecoveryPolicy  json.RawMessage     `json:"recoveryPolicy"`
	Origin          hardenllm.Origin    `json:"origin,omitempty"`
}

type CredentialStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
	ID         string `json:"id,omitempty"`
	Revision   int64  `json:"revision,omitempty"`
	Message    string `json:"message,omitempty"`
}

type ProfileDefinition struct {
	SchemaVersion                      int                        `json:"schemaVersion"`
	Provider                           string                     `json:"provider"`
	APIInferenceType                   string                     `json:"apiInferenceType"`
	BaseURL                            string                     `json:"baseUrl"`
	ModelID                            string                     `json:"modelId"`
	Pricing                            json.RawMessage            `json:"pricing"`
	SupportsTemperature                *bool                      `json:"supportsTemperature"`
	SupportsContractedStructuredOutput bool                       `json:"supportsContractedStructuredOutput"`
	SupportsWebSearch                  bool                       `json:"supportsWebSearch,omitempty"`
	TokensParam                        *string                    `json:"tokensParam"`
	ResponsesTokensParam               *string                    `json:"responsesTokensParam"`
	DefaultOptions                     map[string]json.RawMessage `json:"defaultOptions"`
	ReasoningEffortMap                 map[string]json.RawMessage `json:"reasoningEffortMap,omitempty"`
	Models                             []json.RawMessage          `json:"models,omitempty"`
	LastModelRefreshAt                 *time.Time                 `json:"lastModelRefreshAt,omitempty"`
	RecoveryPolicy                     json.RawMessage            `json:"recoveryPolicy"`
}

type ProfileView struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Kind             string            `json:"kind"`
	PresetID         string            `json:"presetId,omitempty"`
	CreatedAt        *time.Time        `json:"createdAt,omitempty"`
	Definition       ProfileDefinition `json:"definition"`
	CredentialStatus CredentialStatus  `json:"credentialStatus"`
}

type ProfilesEnvelope struct {
	Profiles []ProfileView `json:"profiles"`
}

type ProfileDraft struct {
	SourceProfileID   string            `json:"sourceProfileId,omitempty"`
	PresetID          string            `json:"presetId,omitempty"`
	Definition        ProfileDefinition `json:"definition"`
	Inputs            json.RawMessage   `json:"inputs"`
	CredentialBinding json.RawMessage   `json:"credentialBinding,omitempty"`
	CredentialStatus  CredentialStatus  `json:"credentialStatus"`
}

// ExecutionTarget uses profileId; the stateless core's connectionId type is
// intentionally not used for this different wire contract.
type ExecutionTarget struct {
	ProfileID string `json:"profileId"`
	Provider  string `json:"provider"`
	Protocol  string `json:"protocol"`
	Endpoint  string `json:"endpoint"`
	ModelID   string `json:"modelId"`
}

type ResultSource struct {
	Kind          hardenllm.ResultSourceKind `json:"kind"`
	AttemptNumber int                        `json:"attemptNumber,omitempty"`
	Producer      *ExecutionTarget           `json:"producer,omitempty"`
}

type Attempt struct {
	Number            int                          `json:"number"`
	ProfileID         string                       `json:"profileId"`
	Target            ExecutionTarget              `json:"target"`
	Category          string                       `json:"category,omitempty"`
	HTTPStatus        int                          `json:"httpStatus,omitempty"`
	Code              string                       `json:"code,omitempty"`
	Type              string                       `json:"type,omitempty"`
	ProviderRequestID string                       `json:"providerRequestId,omitempty"`
	Retryable         bool                         `json:"retryable"`
	Wait              time.Duration                `json:"wait"`
	Duration          time.Duration                `json:"duration"`
	Repair            bool                         `json:"repair"`
	ProviderUsed      bool                         `json:"providerUsed"`
	Stage             string                       `json:"stage,omitempty"`
	Branch            string                       `json:"branch,omitempty"`
	TriggerAttempt    int                          `json:"triggerAttemptNumber,omitempty"`
	InputAttempts     []int                        `json:"inputAttemptNumbers,omitempty"`
	TransportRetryOf  int                          `json:"transportRetryOfAttempt,omitempty"`
	StartedAt         *time.Time                   `json:"startedAt,omitempty"`
	FinishedAt        *time.Time                   `json:"finishedAt,omitempty"`
	ReasoningEffort   string                       `json:"reasoningEffort,omitempty"`
	DispatchObserved  bool                         `json:"dispatchObserved,omitempty"`
	Stream            *hardenllm.StreamDiagnostics `json:"stream,omitempty"`
	WaitDiagnostics   *hardenllm.WaitDiagnostics   `json:"waitDiagnostics,omitempty"`
}

// RecordedRunOutput preserves the entire validated inference projection. Raw
// retains the exact wire numbers/bytes independently of the convenient DTOs.
type RecordedRunOutput struct {
	SchemaVersion       int                     `json:"schemaVersion"`
	RunID               string                  `json:"runId"`
	Status              string                  `json:"status"`
	Output              json.RawMessage         `json:"output"`
	CallID              string                  `json:"callId"`
	TraceID             string                  `json:"traceId"`
	Origin              hardenllm.Origin        `json:"origin,omitempty"`
	SelectedTarget      *ExecutionTarget        `json:"selectedTarget"`
	ResultSource        ResultSource            `json:"resultSource"`
	Accounting          hardenllm.Accounting    `json:"accounting"`
	Attempts            []Attempt               `json:"attempts"`
	Cache               hardenllm.CacheResult   `json:"cache"`
	Artifacts           []json.RawMessage       `json:"artifacts"`
	ProviderInvoked     bool                    `json:"providerInvoked"`
	TotalCallDurationMS int64                   `json:"totalCallDurationMs"`
	TotalWaitMS         int64                   `json:"totalWaitMs"`
	TotalActualWaitMS   *int64                  `json:"totalActualWaitMs"`
	OverBudgetMS        int64                   `json:"overBudgetMs"`
	UsedRepair          bool                    `json:"usedRepair"`
	Diagnostics         json.RawMessage         `json:"diagnostics,omitempty"`
	GenerationTarget    *ExecutionTarget        `json:"generationTarget,omitempty"`
	StopReason          string                  `json:"stopReason,omitempty"`
	Search              *hardenllm.SearchResult `json:"search,omitempty"`
	Raw                 json.RawMessage         `json:"-"`
}

type RunState struct {
	LastRunID   string `json:"lastRunId"`
	LastTraceID string `json:"lastTraceId"`
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}
type Diagnostic struct {
	Code               string `json:"code"`
	Component          string `json:"component"`
	Operation          string `json:"operation"`
	RequestID          string `json:"requestId"`
	RunID              string `json:"runId,omitempty"`
	TraceID            string `json:"traceId,omitempty"`
	InferenceOutcome   string `json:"inferenceOutcome"`
	PersistenceOutcome string `json:"persistenceOutcome"`
	Cause              string `json:"cause"`
	DurationMS         int64  `json:"durationMs"`
	Message            string `json:"message"`
	Action             string `json:"action"`
}
type RunError struct {
	Code            string       `json:"code"`
	Message         string       `json:"message"`
	FieldErrors     []FieldError `json:"fieldErrors,omitempty"`
	CurrentRevision *int64       `json:"currentRevision,omitempty"`
	Diagnostic      *Diagnostic  `json:"diagnostic,omitempty"`
}
type RecordingOutcome struct {
	Mode       string      `json:"mode"`
	Status     string      `json:"status"`
	RunID      string      `json:"runId"`
	Diagnostic *Diagnostic `json:"diagnostic,omitempty"`
}

// RunEvidence is a product-safe projection of current terminal facts. The
// operation ID is client correlation, not a durable Harden operation handle.
type RunEvidence struct {
	OperationID          string                `json:"operationId"`
	TraceID              string                `json:"traceId"`
	Cache                hardenllm.CacheResult `json:"cache"`
	ProviderInvoked      bool                  `json:"providerInvoked"`
	ProviderAttemptCount int                   `json:"providerAttemptCount"`
}

type RunOutcome struct {
	Result    RecordedRunOutput
	State     RunState
	Recording RecordingOutcome
	Evidence  *RunEvidence
}

// RejectedError has only the published compact error, not invented zero usage.
// Error deliberately excludes remote messages/fields, which may contain input.
type RejectedError struct {
	StatusCode int
	Problem    RunError
}

func (*RejectedError) Error() string { return "harden HTTP request was rejected" }

type RunFailure struct {
	Problem  RunError
	Evidence *RunEvidence
}

func (*RunFailure) Error() string { return "harden inference failed with a definitive terminal result" }

type UnknownOutcomeError struct{ Cause error }

func (*UnknownOutcomeError) Error() string {
	return "harden inference outcome is unknown; do not replay inference"
}
func (failure *UnknownOutcomeError) Unwrap() error { return failure.Cause }

// ProtocolError exposes no untrusted response body or transport credentials.
type ProtocolError struct{}

func (*ProtocolError) Error() string { return "harden response violates the published contract" }
