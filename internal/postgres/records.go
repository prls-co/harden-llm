package postgres

import (
	"encoding/json"
	"time"
)

type User struct {
	ID           string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Session struct {
	ID          string
	OwnerID     string
	TokenDigest []byte
	ExpiresAt   time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
}

type ProfileRecord struct {
	OwnerID      string
	ID           string
	CredentialID string
	Document     json.RawMessage
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type CredentialRecord struct {
	OwnerID    string
	ID         string
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
	Origin     string
	Metadata   json.RawMessage
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type ClientState struct {
	OwnerID   string
	Document  json.RawMessage
	UpdatedAt time.Time
}

type RunRecord struct {
	OwnerID     string
	ID          string
	ProfileID   string
	TraceID     string
	Status      string
	Request     json.RawMessage
	Result      json.RawMessage
	Execution   *ExecutionFields
	StartedAt   time.Time
	CompletedAt time.Time
}

type TraceRecord struct {
	OwnerID   string
	TraceID   string
	RunID     string
	Record    json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ExecutionFields struct {
	SchemaVersion       int16
	SelectedProvider    string
	SelectedProtocol    string
	SelectedEndpoint    string
	SelectedModelID     string
	ResultSource        string
	ProducerProfileID   string
	ProducerProvider    string
	ProducerProtocol    string
	ProducerEndpoint    string
	ProducerModelID     string
	ProviderInvoked     bool
	ResultUsage         UsageFields
	ProviderUsage       UsageFields
	ResultCost          CostFields
	ProviderCost        CostFields
	CacheServed         bool
	TotalCallDurationMS int64
	OverBudgetMS        int64
}

type UsageFields struct {
	Status              string
	InputTokens         int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	OutputTokens        int64
	ReasoningTokens     int64
}

type CostFields struct {
	Status              string
	KnownSubtotalUSD    float64
	KnownObservations   int64
	UnknownObservations int64
}

type ObservationRecord struct {
	OwnerID   string
	TraceID   string
	Sequence  int
	Type      string
	Data      json.RawMessage
	CreatedAt time.Time
}

type ArtifactRecord struct {
	OwnerID     string
	RunID       string
	TraceID     string
	ID          string
	Kind        string
	ObjectKey   string
	ContentType string
	SHA256      string
	SizeBytes   int64
	State       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ArtifactOperation struct {
	ID            string
	BatchID       string
	Action        string
	State         string
	OwnerID       string
	RunID         string
	TraceID       string
	ArtifactID    string
	Kind          string
	ObjectKey     string
	ContentType   string
	SHA256        string
	SizeBytes     int64
	AttemptCount  int
	NextAttemptAt time.Time
	ErrorCategory string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type ArtifactOperationBacklog struct {
	Pending   int64
	OldestAge time.Duration
}

type ArtifactDeleteBatch struct {
	ID                    string
	OwnerID               string
	Scope                 string
	RunID                 string
	TraceID               string
	State                 string
	ExpectedArtifactCount int
	DeletedRunCount       int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	Operations            []ArtifactOperation
}

type CacheRecord struct {
	OwnerID       string
	Version       string
	OperationHash string
	Operation     json.RawMessage
	Result        json.RawMessage
	Usage         json.RawMessage
	Cost          json.RawMessage
	Envelope      json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type RunStats struct {
	TotalCount   int64
	SuccessCount int64
	FailureCount int64
	TimeoutCount int64

	ResultPromptTokens          int64
	ResultCacheReadTokens       int64
	ResultCacheCreationTokens   int64
	ResultOutputTokens          int64
	ResultReasoningTokens       int64
	ResultTotalTokens           int64
	ProviderPromptTokens        int64
	ProviderCacheReadTokens     int64
	ProviderCacheCreationTokens int64
	ProviderOutputTokens        int64
	ProviderReasoningTokens     int64
	ProviderTotalTokens         int64

	ResultCompleteUsageCount       int64
	ResultPartialUsageCount        int64
	ResultUnavailableUsageCount    int64
	ResultInconsistentUsageCount   int64
	ProviderCompleteUsageCount     int64
	ProviderPartialUsageCount      int64
	ProviderUnavailableUsageCount  int64
	ProviderInconsistentUsageCount int64

	ResultKnownCostSubtotalUSD   float64
	ProviderKnownCostSubtotalUSD float64
	CachedKnownCostSubtotalUSD   float64
	ResultExactCostCount         int64
	ResultPartialCostCount       int64
	ResultUnknownCostCount       int64
	ResultUnavailableCostCount   int64
	ProviderExactCostCount       int64
	ProviderPartialCostCount     int64
	ProviderUnknownCostCount     int64
	ProviderUnavailableCostCount int64
	CachedExactCostCount         int64
	CachedPartialCostCount       int64
	CachedUnknownCostCount       int64
	CachedUnavailableCostCount   int64
	CachedCount                  int64

	TotalCallDurationMS int64
	MaxCallDurationMS   int64
	OverBudgetCount     int64
	MaxOverBudgetMS     int64
}
