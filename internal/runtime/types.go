package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
)

type Profile struct {
	ID                       string
	Provider                 string
	APIInferenceType         string
	CredentialScope          string
	BaseURL                  string
	ModelID                  string
	DefaultOptions           map[string]any
	ReasoningEffortMap       map[string]map[string]any
	SupportsStructuredOutput bool
	SupportsTemperature      bool
	SupportsWebSearch        bool
	TokensParam              string
	ResponsesTokensParam     string
	Pricing                  Pricing
}

type Pricing = accounting.Pricing

type Credential struct {
	APIKey  string
	Headers map[string]string
}

type Call struct {
	SystemPrompt    string
	UserPrompt      string
	CallType        string
	Schema          json.RawMessage
	ReasoningEffort string
	WebSearch       bool
	// SearchMemo is owned by one logical call and shared by retries and repairs.
	SearchMemo         *sync.Map
	ProviderOptions    map[string]any
	Context            ObservabilityContext
	Origin             Origin
	ValidateStructured func(any) error
	Repair             *RepairRequest
	Telemetry          *Telemetry
	Progress           func(ProgressSnapshot)
}

// Origin is the bounded, non-authoritative source correlation attached by a
// caller. It is kept separate from authenticated owner and generated IDs.
type Origin struct {
	Client         string `json:"client,omitempty"`
	Component      string `json:"component,omitempty"`
	OperationID    string `json:"operationId,omitempty"`
	ParentRunID    string `json:"parentRunId,omitempty"`
	JobID          string `json:"jobId,omitempty"`
	TestRunID      string `json:"testRunId,omitempty"`
	TestID         string `json:"testId,omitempty"`
	SourceRevision string `json:"sourceRevision,omitempty"`
}

type ProgressSnapshot struct {
	Sequence            uint64
	RunID               string
	CallID              string
	TraceID             string
	Type                string
	Stage               string
	Branch              string
	ProfileID           string
	ReasoningEffort     string
	Attempt             int
	AttemptsUsed        int
	AttemptsRemaining   int
	ElapsedMs           int64
	DeadlineRemainingMs *int64
	ReceivedBytes       int64
	EventCount          int64
	OutputBytes         int64
	OutputCodePoints    int64
	LastActivity        time.Time
	StopReason          string
	Terminal            bool
	MaxAttempts         int
	EffectiveTimeout    *time.Duration
	Origin              Origin
	Attempts            []AttemptRecord
	Accounting          *Accounting
}

type RepairRequest struct {
	Attempt      int
	MaxAttempts  int
	Stage        string
	Branch       string
	TargetSchema json.RawMessage
	History      []RepairHistoryEntry
}

// RepairHistoryEntry is flat branch-local evidence supplied to a JSON repair
// model. Prior prompts are intentionally excluded to prevent recursive growth.
type RepairHistoryEntry struct {
	Stage           string `json:"stage"`
	Attempt         int    `json:"attempt"`
	Output          string `json:"output"`
	ValidationError string `json:"validationError"`
}

type PreparedOperation struct {
	Operation cachekey.Operation
	Opaque    any
	// StreamProgress is installed by the single runtime execution loop for
	// the active attempt. Providers may report bounded cumulative transport
	// measurements through it; it never controls retry or recovery decisions.
	StreamProgress func(StreamDiagnostics)
}

type Usage = accounting.Usage
type Cost = accounting.Cost
type Ledger = accounting.Ledger
type Accounting = accounting.Accounting

type ProviderResult struct {
	Search     *SearchResult     `json:"search,omitempty"`
	Output     any               `json:"output"`
	Accounting Ledger            `json:"accounting"`
	Stream     StreamDiagnostics `json:"stream,omitempty"`
	// ProviderDispatched records the local transport's observed request-header
	// write. It is internal and is never persisted on the wire.
	ProviderDispatched bool `json:"-"`
}

type StreamDiagnostics struct {
	ReceivedBytes    int64      `json:"receivedBytes,omitempty"`
	EventCount       int64      `json:"eventCount,omitempty"`
	OutputBytes      int64      `json:"outputBytes,omitempty"`
	OutputCodePoints int64      `json:"outputCodePoints,omitempty"`
	FirstEventMs     *int64     `json:"firstEventMs,omitempty"`
	FirstOutputMs    *int64     `json:"firstOutputMs,omitempty"`
	LastEventAt      *time.Time `json:"lastEventAt,omitempty"`
	LastOutputAt     *time.Time `json:"lastOutputAt,omitempty"`
	TerminalState    string     `json:"terminalState,omitempty"`
}

type WaitDiagnostics struct {
	Reason     string        `json:"reason,omitempty"`
	Planned    time.Duration `json:"plannedMs"`
	Actual     time.Duration `json:"actualMs"`
	RetryAfter time.Duration `json:"retryAfterMs,omitempty"`
}

func (wait WaitDiagnostics) MarshalJSON() ([]byte, error) {
	type wire struct {
		Reason       string `json:"reason,omitempty"`
		PlannedMs    int64  `json:"plannedMs"`
		ActualMs     int64  `json:"actualMs"`
		RetryAfterMs int64  `json:"retryAfterMs,omitempty"`
	}
	return json.Marshal(wire{
		Reason: wait.Reason, PlannedMs: wait.Planned.Milliseconds(),
		ActualMs: wait.Actual.Milliseconds(), RetryAfterMs: wait.RetryAfter.Milliseconds(),
	})
}

type Executor interface {
	Prepare(ctx context.Context, profile Profile, credential Credential, call Call) (PreparedOperation, error)
	Execute(ctx context.Context, operation PreparedOperation) (ProviderResult, error)
}

type CredentialLookup func(context.Context, Profile) (Credential, error)

type ExecutionTarget struct {
	ProfileID string `json:"profileId"`
	Provider  string `json:"provider"`
	Protocol  string `json:"protocol"`
	Endpoint  string `json:"endpoint"`
	ModelID   string `json:"modelId"`
}

type AttemptRecord struct {
	Number            int                `json:"number"`
	ProfileID         string             `json:"profileId"`
	Target            ExecutionTarget    `json:"target"`
	ProviderUsed      bool               `json:"providerUsed"`
	Category          retry.Category     `json:"category,omitempty"`
	Status            int                `json:"httpStatus,omitempty"`
	Retryable         bool               `json:"retryable"`
	Delay             time.Duration      `json:"wait"`
	Duration          time.Duration      `json:"duration"`
	Repair            bool               `json:"repair"`
	Code              string             `json:"code,omitempty"`
	Type              string             `json:"type,omitempty"`
	ProviderRequestID string             `json:"providerRequestId,omitempty"`
	Stage             string             `json:"stage,omitempty"`
	Branch            string             `json:"branch,omitempty"`
	TriggerAttempt    int                `json:"triggerAttemptNumber,omitempty"`
	InputAttempts     []int              `json:"inputAttemptNumbers,omitempty"`
	TransportRetryOf  int                `json:"transportRetryOfAttempt,omitempty"`
	StartedAt         *time.Time         `json:"startedAt,omitempty"`
	FinishedAt        *time.Time         `json:"finishedAt,omitempty"`
	ReasoningEffort   string             `json:"reasoningEffort,omitempty"`
	DispatchObserved  bool               `json:"dispatchObserved"`
	Stream            *StreamDiagnostics `json:"stream,omitempty"`
	WaitDiagnostics   *WaitDiagnostics   `json:"waitDiagnostics,omitempty"`
}

type ResultSourceKind string

const (
	ResultSourceNone     ResultSourceKind = "none"
	ResultSourceProvider ResultSourceKind = "provider"
	ResultSourceCache    ResultSourceKind = "cache"
)

type ResultSource struct {
	Kind          ResultSourceKind `json:"kind"`
	AttemptNumber int              `json:"attemptNumber,omitempty"`
	Producer      *ExecutionTarget `json:"producer,omitempty"`
}

type CallRecord struct {
	Search               *SearchResult
	CallID               string
	TraceID              string
	Output               any
	SelectedTarget       ExecutionTarget
	ResultSource         ResultSource
	Accounting           Accounting
	Attempts             []AttemptRecord
	ParseFailureResponse json.RawMessage
	PreparedOperation    PreparedOperation
	GenerationTarget     ExecutionTarget
	Branch               string
	StopReason           string
	Diagnostics          Diagnostics
	Origin               Origin
	Cache                CacheFacts
}

type Diagnostics struct {
	Stage             string
	Branch            string
	StopReason        string
	AttemptsUsed      int
	AttemptsRemaining int
	Elapsed           time.Duration
	TotalActualWait   time.Duration
	ReceivedBytes     int64
	EventCount        int64
	OutputBytes       int64
	OutputCodePoints  int64
	EffectiveTimeout  *time.Duration
	DeadlineAt        *time.Time
	BranchCaches      []BranchCache
}

// MarshalJSON keeps internal duration storage precise while exposing the same
// bounded millisecond diagnostics shape used by the public REST result and
// trace artifact. Runtime code should continue to use time.Duration values.
func (diagnostics Diagnostics) MarshalJSON() ([]byte, error) {
	type wire struct {
		Stage              string        `json:"stage,omitempty"`
		Branch             string        `json:"branch,omitempty"`
		StopReason         string        `json:"stopReason,omitempty"`
		AttemptsUsed       int           `json:"attemptsUsed"`
		AttemptsRemaining  int           `json:"attemptsRemaining"`
		ElapsedMs          int64         `json:"elapsedMs"`
		TotalActualWaitMs  int64         `json:"totalActualWaitMs"`
		ReceivedBytes      int64         `json:"receivedBytes"`
		EventCount         int64         `json:"eventCount"`
		OutputBytes        int64         `json:"outputBytes"`
		OutputCodePoints   int64         `json:"outputCodePoints"`
		EffectiveTimeoutMs *int64        `json:"effectiveTimeoutMs,omitempty"`
		DeadlineAt         *time.Time    `json:"deadlineAt,omitempty"`
		BranchCaches       []BranchCache `json:"branchCaches,omitempty"`
	}
	var effectiveTimeoutMs *int64
	if diagnostics.EffectiveTimeout != nil {
		value := diagnostics.EffectiveTimeout.Milliseconds()
		effectiveTimeoutMs = &value
	}
	return json.Marshal(wire{
		Stage: diagnostics.Stage, Branch: diagnostics.Branch, StopReason: diagnostics.StopReason,
		AttemptsUsed: diagnostics.AttemptsUsed, AttemptsRemaining: diagnostics.AttemptsRemaining,
		ElapsedMs: diagnostics.Elapsed.Milliseconds(), TotalActualWaitMs: diagnostics.TotalActualWait.Milliseconds(),
		ReceivedBytes: diagnostics.ReceivedBytes, EventCount: diagnostics.EventCount,
		OutputBytes: diagnostics.OutputBytes, OutputCodePoints: diagnostics.OutputCodePoints,
		EffectiveTimeoutMs: effectiveTimeoutMs, DeadlineAt: diagnostics.DeadlineAt,
		BranchCaches: diagnostics.BranchCaches,
	})
}

type BranchCache struct {
	Branch           string
	GenerationTarget ExecutionTarget
	Cache            CacheFacts
}

func durationPointer(value time.Duration) *time.Duration {
	return &value
}

type CacheFacts struct {
	Mode                  cachekey.Mode `json:"mode"`
	Status                string        `json:"status"`
	OperationHash         string        `json:"operationHash,omitempty"`
	OriginalOperationHash string        `json:"originalOperationHash,omitempty"`
	RerunOperationHash    string        `json:"rerunOperationHash,omitempty"`
	Version               string        `json:"version,omitempty"`
	Served                bool          `json:"served"`
	Written               bool          `json:"written"`
}
