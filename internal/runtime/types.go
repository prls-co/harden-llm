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
	ValidateStructured func(any) error
	Repair             *RepairRequest
	Telemetry          *Telemetry
}

type RepairRequest struct {
	Attempt            int
	MaxAttempts        int
	PreviousOutput     string
	ValidationFeedback string
	TargetSchema       json.RawMessage
}

type PreparedOperation struct {
	Operation cachekey.Operation
	Opaque    any
}

type Usage = accounting.Usage
type Cost = accounting.Cost
type Ledger = accounting.Ledger
type Accounting = accounting.Accounting

type ProviderResult struct {
	Search              *SearchResult   `json:"search,omitempty"`
	Output              any             `json:"output"`
	Accounting          Ledger          `json:"accounting"`
	RawProviderEnvelope json.RawMessage `json:"rawProviderEnvelope"`
	// ProviderDispatched records the local transport's observed request-header
	// write. It is internal and is never persisted on the wire.
	ProviderDispatched bool `json:"-"`
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
	Number            int             `json:"number"`
	ProfileID         string          `json:"profileId"`
	Target            ExecutionTarget `json:"target"`
	ProviderUsed      bool            `json:"providerUsed"`
	Category          retry.Category  `json:"category,omitempty"`
	Status            int             `json:"httpStatus,omitempty"`
	Retryable         bool            `json:"retryable"`
	Delay             time.Duration   `json:"wait"`
	Duration          time.Duration   `json:"duration"`
	Repair            bool            `json:"repair"`
	Code              string          `json:"code,omitempty"`
	Type              string          `json:"type,omitempty"`
	ProviderRequestID string          `json:"providerRequestId,omitempty"`
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
	RawProviderEnvelope  json.RawMessage
	ParseFailureResponse json.RawMessage
	PreparedOperation    PreparedOperation
	Cache                CacheFacts
}

type CacheFacts struct {
	Mode          cachekey.Mode `json:"mode"`
	Status        string        `json:"status"`
	OperationHash string        `json:"operationHash,omitempty"`
	Version       string        `json:"version,omitempty"`
	Served        bool          `json:"served"`
	Written       bool          `json:"written"`
}
