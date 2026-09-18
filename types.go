package hardenllm

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Options configures a Client without initializing process-global state.
type Options struct {
	Credentials    CredentialResolver
	Cache          CacheStore
	Artifacts      ArtifactStore
	EndpointPolicy EndpointPolicy
	WebSearch      WebSearchOptions
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	Logger         *slog.Logger
}

// WebSearchOptions configures the server-side fallback used when a selected
// profile does not advertise native web-search support.
type WebSearchOptions struct {
	JinaAPIKey           string
	JinaTimeout          time.Duration
	JinaMaxResponseBytes int64
}

// Request describes one provider-neutral LLM call.
type Request struct {
	ProfileID       string
	Profiles        ProfileCatalog
	SystemPrompt    string
	UserPrompt      string
	CallType        CallType
	Schema          json.RawMessage
	ReasoningEffort ReasoningEffort
	WebSearch       bool
	ProviderOptions map[string]any
	Context         ObservabilityContext
	CacheMode       CacheMode
	CacheVersion    string
	RecoveryPolicy  RecoveryPolicy
	Origin          Origin
	// Progress receives best-effort cumulative snapshots. The runtime never
	// closes this caller-owned channel and never blocks provider execution on it.
	Progress chan<- ProgressEvent
}

// ProgressEvent is a bounded diagnostic snapshot, not a token/output stream.
// Counters are explicitly received bytes/code points/events; token usage stays
// in the final accounting projection.
type ProgressEvent struct {
	SchemaVersion       int         `json:"schemaVersion"`
	Sequence            uint64      `json:"sequence"`
	RunID               string      `json:"runId,omitempty"`
	CallID              string      `json:"callId"`
	TraceID             string      `json:"traceId"`
	Type                string      `json:"type"`
	Stage               string      `json:"stage,omitempty"`
	Branch              string      `json:"branch,omitempty"`
	ProfileID           string      `json:"profileId,omitempty"`
	ReasoningEffort     string      `json:"reasoningEffort,omitempty"`
	Attempt             int         `json:"attempt,omitempty"`
	AttemptsUsed        int         `json:"attemptsUsed"`
	AttemptsRemaining   int         `json:"attemptsRemaining"`
	ElapsedMs           int64       `json:"elapsedMs"`
	DeadlineRemainingMs *int64      `json:"deadlineRemainingMs,omitempty"`
	ReceivedBytes       int64       `json:"receivedBytes,omitempty"`
	EventCount          int64       `json:"eventCount,omitempty"`
	OutputBytes         int64       `json:"outputBytes,omitempty"`
	OutputCodePoints    int64       `json:"outputCodePoints,omitempty"`
	LastActivity        string      `json:"lastActivity,omitempty"`
	StopReason          string      `json:"stopReason,omitempty"`
	Terminal            bool        `json:"terminal"`
	MaxAttempts         int         `json:"maxAttempts,omitempty"`
	EffectiveTimeoutMs  *int64      `json:"effectiveTimeoutMs,omitempty"`
	Origin              *Origin     `json:"origin,omitempty"`
	Attempts            []Attempt   `json:"attempts,omitempty"`
	Accounting          *Accounting `json:"accounting,omitempty"`
}

// Origin correlates a call with its originating client/agent/test without
// becoming part of cache identity or authenticated ownership.
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

// Result is the single detailed result returned by Client.Call.
type Result struct {
	Search           *SearchResult
	Output           any
	CallID           string
	TraceID          string
	Origin           Origin
	SelectedTarget   ExecutionTarget
	GenerationTarget ExecutionTarget
	ResultSource     ResultSource
	Accounting       Accounting
	Attempts         []Attempt
	Cache            CacheResult
	Artifacts        []ArtifactRef
	Diagnostics      Diagnostics
}

type Diagnostics struct {
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

type BranchCache struct {
	Branch           string          `json:"branch"`
	GenerationTarget ExecutionTarget `json:"generationTarget"`
	Cache            CacheResult     `json:"cache"`
}

type SearchResult = runtime.SearchResult
type SearchSource = runtime.SearchSource

// CallType identifies text or contracted structured-output execution.
type CallType string

const (
	CallTypeText       CallType = "text"
	CallTypeStructured CallType = "structured"
)

// ReasoningEffort is a portable reasoning level understood by a profile.
type ReasoningEffort string

const (
	ReasoningEffortLowest  ReasoningEffort = "lowest"
	ReasoningEffortMiddle  ReasoningEffort = "middle"
	ReasoningEffortHighest ReasoningEffort = "highest"
)

// ObservabilityContext carries trace-only correlation dimensions. It is never
// part of cache identity.
type ObservabilityContext struct {
	TaskID         string
	TaskSlug       string
	ItemID         string
	RunID          string
	OrganizationID string
	QuerySetID     string
	Environment    string
	Release        string
	PromptLabels   []string
	Tags           map[string]string
	Metadata       map[string]string
}

// RecoveryPolicy is the complete explicit policy used by every execution path.
type RecoveryPolicy = retry.Policy
type RecoveryBackoff = retry.Backoff
type RecoveryCategory = retry.Category
type RecoveryPolicyError = retry.ValidationError
type RecoveryTarget = retry.RecoveryTarget
type JSONRepairPlan = retry.RepairPlan
type RerunPlan = retry.RerunPlan

// DefaultRecoveryPolicy creates the explicit six-stage preset for new callers.
// Deployments must provision the named CPA Astra profiles before enabling it;
// existing stored policies are not changed implicitly.
func DefaultRecoveryPolicy() RecoveryPolicy { return DefaultStructuredRecoveryPolicy() }

// DefaultStructuredRecoveryPolicy is the explicit six-stage preset for new
// structured callers. It remains named for callers that want to make the
// structured-output requirement obvious at the call site.
func DefaultStructuredRecoveryPolicy() RecoveryPolicy {
	return RecoveryPolicy{
		MaxAttempts: 6,
		RetryOn:     []RecoveryCategory{retry.CategoryNetwork, retry.CategoryRateLimit, retry.CategoryServer, retry.CategoryEmpty, retry.CategoryProvider},
		Backoff:     RecoveryBackoff{BaseDelayMS: 500, MaxDelayMS: 8000},
		JSONRepair: &JSONRepairPlan{
			Initial:    RecoveryTarget{Source: "profile", ProfileID: "CPA GPT-5.6 Luna", ReasoningEffort: string(ReasoningEffortLowest)},
			Escalation: &RecoveryTarget{Source: "profile", ProfileID: "CPA GPT-5.6 Luna", ReasoningEffort: string(ReasoningEffortHighest)},
		},
		Rerun: &RerunPlan{
			Target: RecoveryTarget{Source: "profile", ProfileID: "CPA GPT-6 Astra", ReasoningEffort: string(ReasoningEffortLowest)},
			JSONRepair: &JSONRepairPlan{
				Initial:    RecoveryTarget{Source: "profile", ProfileID: "CPA GPT-6 Astra", ReasoningEffort: string(ReasoningEffortLowest)},
				Escalation: &RecoveryTarget{Source: "profile", ProfileID: "CPA GPT-6 Astra", ReasoningEffort: string(ReasoningEffortHighest)},
			},
		},
	}
}

// Attempt is safe, normalized metadata for one execution attempt. ProviderUsed
// is true only when the local model transport observed request headers being
// written; it does not prove remote execution or billing.
type Attempt struct {
	Number            int                `json:"number"`
	ProfileID         string             `json:"profileId"`
	Target            ExecutionTarget    `json:"target"`
	Category          string             `json:"category,omitempty"`
	HTTPStatus        int                `json:"httpStatus,omitempty"`
	Code              string             `json:"code,omitempty"`
	Type              string             `json:"type,omitempty"`
	ProviderRequestID string             `json:"providerRequestId,omitempty"`
	Retryable         bool               `json:"retryable"`
	Wait              time.Duration      `json:"wait"`
	Duration          time.Duration      `json:"duration"`
	Repair            bool               `json:"repair"`
	ProviderUsed      bool               `json:"providerUsed"`
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

type StreamDiagnostics struct {
	ReceivedBytes    int64      `json:"receivedBytes"`
	EventCount       int64      `json:"eventCount"`
	OutputBytes      int64      `json:"outputBytes"`
	OutputCodePoints int64      `json:"outputCodePoints"`
	FirstEventMs     *int64     `json:"firstEventMs,omitempty"`
	FirstOutputMs    *int64     `json:"firstOutputMs,omitempty"`
	LastEventAt      *time.Time `json:"lastEventAt,omitempty"`
	LastOutputAt     *time.Time `json:"lastOutputAt,omitempty"`
	TerminalState    string     `json:"terminalState,omitempty"`
}

type WaitDiagnostics struct {
	Reason       string `json:"reason,omitempty"`
	PlannedMs    int64  `json:"plannedMs"`
	ActualMs     int64  `json:"actualMs"`
	RetryAfterMs int64  `json:"retryAfterMs,omitempty"`
}

// ExecutionTarget is the immutable prepared provider target for selection,
// invocation, or cache-producer attribution.
type ExecutionTarget struct {
	ProfileID string `json:"profileId"`
	Provider  string `json:"provider"`
	Protocol  string `json:"protocol"`
	Endpoint  string `json:"endpoint"`
	ModelID   string `json:"modelId"`
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

// Usage preserves canonical token groups without provider-native payloads.
type Usage struct {
	InputTokens         int64  `json:"inputTokens"`
	CacheReadTokens     int64  `json:"cacheReadTokens"`
	CacheCreationTokens int64  `json:"cacheCreationTokens"`
	OutputTokens        int64  `json:"outputTokens"`
	ReasoningTokens     int64  `json:"reasoningTokens"`
	PromptTokens        int64  `json:"promptTokens"`
	CompletionTokens    int64  `json:"completionTokens"`
	TotalTokens         int64  `json:"totalTokens"`
	Status              string `json:"status"`
}

// Cost records diagnostic cost and its exact coverage state.
type Cost struct {
	KnownSubtotalUSD    float64 `json:"knownSubtotalUsd"`
	Status              string  `json:"status"`
	Source              string  `json:"source"`
	KnownObservations   int64   `json:"knownObservations"`
	UnknownObservations int64   `json:"unknownObservations"`
}

type AccountingLedger struct {
	Usage Usage `json:"usage"`
	Cost  Cost  `json:"cost"`
}

type Accounting struct {
	Result   AccountingLedger `json:"result"`
	Provider AccountingLedger `json:"provider"`
}

// EndpointPolicy is the single outbound endpoint-security configuration.
type EndpointPolicy struct {
	AllowedHosts          []string
	PrivateAllowedHosts   []string
	PrivateAllowlist      []netip.Prefix
	Resolver              EndpointResolver
	DialContext           func(context.Context, string, string) (net.Conn, error)
	TLSConfig             *tls.Config
	ConnectTimeout        time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
}

// EndpointResolver resolves both IPv4 and IPv6 addresses for policy checks.
type EndpointResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}
