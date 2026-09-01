package hardenllm

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Options configures a Client without initializing process-global state.
type Options struct {
	Credentials    CredentialResolver
	Cache          CacheStore
	Artifacts      ArtifactStore
	EndpointPolicy EndpointPolicy
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	Logger         *slog.Logger
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
	ProviderOptions map[string]any
	Context         ObservabilityContext
	CacheMode       CacheMode
	CacheVersion    string
	RetryPolicy     RetryPolicy
}

// Result is the single detailed result returned by Client.Call.
type Result struct {
	Output         any
	CallID         string
	TraceID        string
	SelectedTarget ExecutionTarget
	ResultSource   ResultSource
	Accounting     Accounting
	Attempts       []Attempt
	Cache          CacheResult
	Artifacts      []ArtifactRef
}

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

// RetryPolicy controls the total provider-attempt budget.
type RetryPolicy struct {
	MaxAttempts      int
	InitialBackoff   time.Duration
	MaximumBackoff   time.Duration
	RetryNetwork     *bool
	RetryRateLimit   *bool
	RetryServerError *bool
	RetryEmpty       *bool
	RetryParse       *bool
	StructuredRepair StructuredRepairPolicy
}

type StructuredRepairPolicy struct {
	Enabled    bool
	Escalation *RepairEscalation
}

type RepairEscalation struct {
	Attempt         int
	ProfileID       string
	ModelID         string
	ReasoningEffort ReasoningEffort
}

// Attempt is safe, normalized metadata for one provider invocation.
type Attempt struct {
	Number            int             `json:"number"`
	RetryLocalNumber  int             `json:"retryLocalNumber"`
	ProfileID         string          `json:"profileId"`
	Target            ExecutionTarget `json:"target"`
	Category          string          `json:"category,omitempty"`
	HTTPStatus        int             `json:"httpStatus,omitempty"`
	Code              string          `json:"code,omitempty"`
	Type              string          `json:"type,omitempty"`
	ProviderRequestID string          `json:"providerRequestId,omitempty"`
	Retryable         bool            `json:"retryable"`
	Wait              time.Duration   `json:"wait"`
	Duration          time.Duration   `json:"duration"`
	Repair            bool            `json:"repair"`
	BackupIndex       int             `json:"backupIndex"`
	ProviderUsed      bool            `json:"providerUsed"`
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
