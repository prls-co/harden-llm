package runtime

import (
	"context"
	"encoding/json"

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
	Backups                  []string
	SupportsStructuredOutput bool
}

type Credential struct {
	APIKey  string
	Headers map[string]string
}

type Call struct {
	SystemPrompt       string
	UserPrompt         string
	CallType           string
	Schema             json.RawMessage
	ReasoningEffort    string
	ProviderOptions    map[string]any
	Context            ObservabilityContext
	StructuredRepair   StructuredRepair
	ValidateStructured func(any) error
	Repair             *RepairRequest
}

type StructuredRepair struct {
	Enabled    bool
	Escalation *RepairEscalation
}

type RepairEscalation struct {
	Attempt         int
	ModelID         string
	ReasoningEffort string
}

type RepairRequest struct {
	Attempt         int
	MaxAttempts     int
	PreviousOutput  string
	TargetSchema    json.RawMessage
	Escalated       bool
	ModelID         string
	ReasoningEffort string
}

type PreparedOperation struct {
	Operation cachekey.Operation
	Opaque    any
}

type Usage struct {
	InputTokens         int64 `json:"inputTokens"`
	CacheReadTokens     int64 `json:"cacheReadTokens"`
	CacheCreationTokens int64 `json:"cacheCreationTokens"`
	OutputTokens        int64 `json:"outputTokens"`
	ReasoningTokens     int64 `json:"reasoningTokens"`
	TotalTokens         int64 `json:"totalTokens"`
}

type Cost struct {
	TotalUSD float64 `json:"totalUsd"`
	Known    bool    `json:"known"`
	Source   string  `json:"source"`
}

type ProviderResult struct {
	Output              any             `json:"output"`
	Usage               Usage           `json:"usage"`
	Cost                Cost            `json:"cost"`
	RawProviderEnvelope json.RawMessage `json:"rawProviderEnvelope"`
}

type Executor interface {
	Prepare(ctx context.Context, profile Profile, credential Credential, call Call) (PreparedOperation, error)
	Execute(ctx context.Context, operation PreparedOperation) (ProviderResult, error)
}

type CredentialLookup func(context.Context, Profile) (Credential, error)

type CallRecord struct {
	CallID              string
	TraceID             string
	Output              any
	Usage               Usage
	Cost                Cost
	Attempts            []retry.Attempt
	RawProviderEnvelope json.RawMessage
	PreparedOperation   PreparedOperation
	Cache               CacheFacts
}

type CacheFacts struct {
	Mode          cachekey.Mode
	Status        string
	OperationHash string
	Version       string
	Served        bool
	Written       bool
}
