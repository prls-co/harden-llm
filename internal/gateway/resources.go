package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/postgres"
)

const (
	clientStateSchemaVersion = 1
	defaultHistoryLimit      = 20
	maximumHistoryLimit      = 100
	defaultArtifactTTL       = time.Minute
	maximumArtifactTTL       = 5 * time.Minute
)

var (
	ErrInvalidRequest = errors.New("gateway: invalid request")
	ErrInvalidCursor  = errors.New("gateway: invalid cursor")
)

type ClientState struct {
	SchemaVersion      int               `json:"schemaVersion"`
	SelectedProfileID  string            `json:"selectedProfileId,omitempty"`
	ModelID            string            `json:"modelId,omitempty"`
	SystemPrompt       string            `json:"systemPrompt,omitempty"`
	UserPrompt         string            `json:"userPrompt,omitempty"`
	SchemaShorthand    string            `json:"schemaShorthand,omitempty"`
	CallType           string            `json:"callType"`
	Schema             json.RawMessage   `json:"schema,omitempty"`
	ReasoningEffort    string            `json:"reasoningEffort,omitempty"`
	ReasoningByProfile map[string]string `json:"reasoningByProfile,omitempty"`
	StructuredRepair   bool              `json:"structuredRepair"`
	CacheMode          string            `json:"cacheMode"`
	ProviderOptions    map[string]any    `json:"providerOptions,omitempty"`
	MaxAttempts        int               `json:"maxAttempts,omitempty"`
	InitialBackoffMS   int               `json:"initialBackoffMs,omitempty"`
	MaximumBackoffMS   int               `json:"maximumBackoffMs,omitempty"`
	RetryNetwork       *bool             `json:"retryNetwork,omitempty"`
	RetryRateLimit     *bool             `json:"retryRateLimit,omitempty"`
	RetryServerError   *bool             `json:"retryServerError,omitempty"`
	RetryEmpty         *bool             `json:"retryEmpty,omitempty"`
	RetryParse         *bool             `json:"retryParse,omitempty"`
	RepairEscalation   map[string]any    `json:"repairEscalation,omitempty"`
	UI                 map[string]any    `json:"ui,omitempty"`
}

type HistoryItem struct {
	RunID       string          `json:"runId"`
	ProfileID   string          `json:"profileId"`
	TraceID     string          `json:"traceId"`
	Status      string          `json:"status"`
	Request     json.RawMessage `json:"request"`
	Result      json.RawMessage `json:"result"`
	StartedAt   time.Time       `json:"startedAt"`
	CompletedAt time.Time       `json:"completedAt"`
}

type HistoryPage struct {
	Items      []HistoryItem `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

type StatsView struct {
	SchemaVersion       int                 `json:"schemaVersion"`
	TotalCount          int64               `json:"totalCount"`
	SuccessCount        int64               `json:"successCount"`
	FailureCount        int64               `json:"failureCount"`
	TimeoutCount        int64               `json:"timeoutCount"`
	ResultAccounting    AccountingStatsView `json:"resultAccounting"`
	ProviderAccounting  AccountingStatsView `json:"providerAccounting"`
	Cached              CachedStatsView     `json:"cached"`
	TotalCallDurationMS int64               `json:"totalCallDurationMs"`
	MaxCallDurationMS   int64               `json:"maxCallDurationMs"`
	OverBudgetCount     int64               `json:"overBudgetCount"`
	MaxOverBudgetMS     int64               `json:"maxOverBudgetMs"`
}

type AccountingStatsView struct {
	Usage UsageStatsView `json:"usage"`
	Cost  CostStatsView  `json:"cost"`
}

type UsageStatsView struct {
	PromptTokens        int64             `json:"promptTokens"`
	CacheReadTokens     int64             `json:"cacheReadTokens"`
	CacheCreationTokens int64             `json:"cacheCreationTokens"`
	OutputTokens        int64             `json:"outputTokens"`
	ReasoningTokens     int64             `json:"reasoningTokens"`
	TotalTokens         int64             `json:"totalTokens"`
	Coverage            UsageCoverageView `json:"coverage"`
}

type UsageCoverageView struct {
	Complete     int64 `json:"complete"`
	Partial      int64 `json:"partial"`
	Unavailable  int64 `json:"unavailable"`
	Inconsistent int64 `json:"inconsistent"`
}

type CostStatsView struct {
	KnownSubtotalUSD float64          `json:"knownSubtotalUsd"`
	Coverage         CostCoverageView `json:"coverage"`
}

type CostCoverageView struct {
	Exact       int64 `json:"exact"`
	Partial     int64 `json:"partial"`
	Unknown     int64 `json:"unknown"`
	Unavailable int64 `json:"unavailable"`
}

type CachedStatsView struct {
	Count int64         `json:"count"`
	Cost  CostStatsView `json:"cost"`
}

type TraceArtifact struct {
	ArtifactID  string    `json:"artifactId"`
	Kind        string    `json:"kind"`
	State       string    `json:"state"`
	SHA256      string    `json:"sha256"`
	SizeBytes   int64     `json:"sizeBytes"`
	ContentType string    `json:"contentType"`
	CreatedAt   time.Time `json:"createdAt"`
}

type TraceObservation struct {
	Sequence  int             `json:"sequence"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"createdAt"`
}

type TraceResource struct {
	Available bool            `json:"available"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Message   string          `json:"message,omitempty"`
}

type TraceResources struct {
	Request  TraceResource `json:"request"`
	Response TraceResource `json:"response"`
}

type TraceView struct {
	TraceID      string             `json:"traceId"`
	Record       json.RawMessage    `json:"record"`
	Observations []TraceObservation `json:"observations"`
	Artifacts    []TraceArtifact    `json:"artifacts"`
	Resources    TraceResources     `json:"resources"`
}

type ArtifactLifecycle interface {
	PresignGet(context.Context, string, string, time.Duration) (string, error)
	DeleteExecution(context.Context, string, string, string) error
	ClearOwner(context.Context, string) (int64, error)
}

type ResourceServiceConfig struct {
	Store          *postgres.Store
	Profiles       *ProfileService
	ModelRefresher ModelRefresher
	Artifacts      ArtifactLifecycle
	ArtifactTTL    time.Duration
	Clock          func() time.Time
	NewID          func() (string, error)
	Telemetry      *Telemetry
}

type ResourceService struct {
	store          *postgres.Store
	profiles       *ProfileService
	modelRefresher ModelRefresher
	artifacts      ArtifactLifecycle
	artifactTTL    time.Duration
	clock          func() time.Time
	newID          func() (string, error)
	telemetry      *Telemetry
}

func NewResourceService(config ResourceServiceConfig) (*ResourceService, error) {
	if config.Store == nil || config.Profiles == nil {
		return nil, errors.New("gateway: resource store and profile service are required")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.NewID == nil {
		config.NewID = newGatewayID
	}
	if config.ArtifactTTL == 0 {
		config.ArtifactTTL = defaultArtifactTTL
	}
	if config.ArtifactTTL < time.Second || config.ArtifactTTL > maximumArtifactTTL {
		return nil, errors.New("gateway: artifact URL TTL is outside the supported range")
	}
	if config.Telemetry == nil {
		config.Telemetry = newNoopTelemetry()
	}
	return &ResourceService{
		store: config.Store, profiles: config.Profiles, modelRefresher: config.ModelRefresher,
		artifacts: config.Artifacts, artifactTTL: config.ArtifactTTL,
		clock: config.Clock, newID: config.NewID, telemetry: config.Telemetry,
	}, nil
}

func (service *ResourceService) State(ctx context.Context, ownerID string) (ClientState, error) {
	record, err := service.store.ClientState(ctx, ownerID)
	if errors.Is(err, postgres.ErrNotFound) {
		return defaultClientState(), nil
	}
	if err != nil {
		return ClientState{}, err
	}
	var state ClientState
	if err := decodeStrictJSON(record.Document, &state); err != nil || validateClientState(state) != nil {
		return ClientState{}, errors.New("gateway: stored client state is invalid")
	}
	return state, nil
}

func (service *ResourceService) SaveState(ctx context.Context, ownerID string, state ClientState) (ClientState, error) {
	if err := validateClientState(state); err != nil {
		return ClientState{}, err
	}
	document, err := json.Marshal(state)
	if err != nil {
		return ClientState{}, fmt.Errorf("gateway: encode client state: %w", err)
	}
	if err := service.store.SaveClientState(ctx, postgres.ClientState{OwnerID: ownerID, Document: document, UpdatedAt: service.clock().UTC()}); err != nil {
		return ClientState{}, err
	}
	return state, nil
}

func (service *ResourceService) Profiles(ctx context.Context, ownerID string) ([]ProfileState, error) {
	return service.profiles.Profiles(ctx, ownerID)
}

func (service *ResourceService) SaveProfile(ctx context.Context, request SaveProfileRequest) (ProfileState, error) {
	operationContext, endOperation := service.telemetry.StartOperation(ctx, OperationProfileSave)
	state, err := service.profiles.Save(operationContext, request)
	endOperation(err)
	return state, err
}

func (service *ResourceService) DeleteProfile(ctx context.Context, ownerID, profileID string) error {
	return service.profiles.Delete(ctx, ownerID, profileID)
}

func (service *ResourceService) RefreshModels(ctx context.Context, ownerID, profileID string) (ProfileState, error) {
	if service.modelRefresher == nil {
		return ProfileState{}, errors.New("gateway: model refresh is not configured")
	}
	operationContext, endOperation := service.telemetry.StartOperation(ctx, OperationModelRefresh)
	state, err := service.profiles.RefreshModels(operationContext, ownerID, profileID, service.modelRefresher)
	endOperation(err)
	return state, err
}

func (service *ResourceService) ExportBundle(ctx context.Context, ownerID string) (ProfileBundle, error) {
	bundleID, err := service.newID()
	if err != nil {
		return ProfileBundle{}, errors.New("gateway: generate bundle ID")
	}
	return service.profiles.ExportBundle(ctx, ownerID, bundleID)
}

func (service *ResourceService) ReplaceBundle(ctx context.Context, ownerID string, bundle ProfileBundle) ([]ProfileState, error) {
	return service.profiles.ReplaceBundle(ctx, ownerID, bundle)
}

func (service *ResourceService) History(ctx context.Context, ownerID, encodedCursor string, limit int) (HistoryPage, error) {
	if limit == 0 {
		limit = defaultHistoryLimit
	}
	if limit < 1 || limit > maximumHistoryLimit {
		return HistoryPage{}, fmt.Errorf("%w: history limit", ErrInvalidRequest)
	}
	var cursor *postgres.RunCursor
	if encodedCursor != "" {
		decoded, err := decodeHistoryCursor(encodedCursor)
		if err != nil {
			return HistoryPage{}, err
		}
		cursor = &decoded
	}
	records, err := service.store.Runs(ctx, ownerID, limit+1, cursor)
	if err != nil {
		return HistoryPage{}, err
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	items := make([]HistoryItem, 0, len(records))
	for _, record := range records {
		items = append(items, HistoryItem{
			RunID: record.ID, ProfileID: record.ProfileID, TraceID: record.TraceID, Status: record.Status,
			Request: append(json.RawMessage(nil), record.Request...), Result: append(json.RawMessage(nil), record.Result...),
			StartedAt: record.StartedAt, CompletedAt: record.CompletedAt,
		})
	}
	page := HistoryPage{Items: items}
	if hasMore && len(records) > 0 {
		page.NextCursor, err = encodeHistoryCursor(postgres.RunCursor{StartedAt: records[len(records)-1].StartedAt, ID: records[len(records)-1].ID})
		if err != nil {
			return HistoryPage{}, err
		}
	}
	return page, nil
}

func (service *ResourceService) Stats(ctx context.Context, ownerID string) (StatsView, error) {
	stats, err := service.store.RunStats(ctx, ownerID)
	if err != nil {
		return StatsView{}, err
	}
	return StatsView{
		SchemaVersion: 2, TotalCount: stats.TotalCount, SuccessCount: stats.SuccessCount,
		FailureCount: stats.FailureCount, TimeoutCount: stats.TimeoutCount,
		ResultAccounting: AccountingStatsView{
			Usage: UsageStatsView{
				PromptTokens: stats.ResultPromptTokens, CacheReadTokens: stats.ResultCacheReadTokens,
				CacheCreationTokens: stats.ResultCacheCreationTokens, OutputTokens: stats.ResultOutputTokens,
				ReasoningTokens: stats.ResultReasoningTokens, TotalTokens: stats.ResultTotalTokens,
				Coverage: UsageCoverageView{
					Complete: stats.ResultCompleteUsageCount, Partial: stats.ResultPartialUsageCount,
					Unavailable: stats.ResultUnavailableUsageCount, Inconsistent: stats.ResultInconsistentUsageCount,
				},
			},
			Cost: CostStatsView{
				KnownSubtotalUSD: stats.ResultKnownCostSubtotalUSD,
				Coverage: CostCoverageView{
					Exact: stats.ResultExactCostCount, Partial: stats.ResultPartialCostCount,
					Unknown: stats.ResultUnknownCostCount, Unavailable: stats.ResultUnavailableCostCount,
				},
			},
		},
		ProviderAccounting: AccountingStatsView{
			Usage: UsageStatsView{
				PromptTokens: stats.ProviderPromptTokens, CacheReadTokens: stats.ProviderCacheReadTokens,
				CacheCreationTokens: stats.ProviderCacheCreationTokens, OutputTokens: stats.ProviderOutputTokens,
				ReasoningTokens: stats.ProviderReasoningTokens, TotalTokens: stats.ProviderTotalTokens,
				Coverage: UsageCoverageView{
					Complete: stats.ProviderCompleteUsageCount, Partial: stats.ProviderPartialUsageCount,
					Unavailable: stats.ProviderUnavailableUsageCount, Inconsistent: stats.ProviderInconsistentUsageCount,
				},
			},
			Cost: CostStatsView{
				KnownSubtotalUSD: stats.ProviderKnownCostSubtotalUSD,
				Coverage: CostCoverageView{
					Exact: stats.ProviderExactCostCount, Partial: stats.ProviderPartialCostCount,
					Unknown: stats.ProviderUnknownCostCount, Unavailable: stats.ProviderUnavailableCostCount,
				},
			},
		},
		Cached: CachedStatsView{
			Count: stats.CachedCount,
			Cost: CostStatsView{
				KnownSubtotalUSD: stats.CachedKnownCostSubtotalUSD,
				Coverage: CostCoverageView{
					Exact: stats.CachedExactCostCount, Partial: stats.CachedPartialCostCount,
					Unknown: stats.CachedUnknownCostCount, Unavailable: stats.CachedUnavailableCostCount,
				},
			},
		},
		TotalCallDurationMS: stats.TotalCallDurationMS, MaxCallDurationMS: stats.MaxCallDurationMS,
		OverBudgetCount: stats.OverBudgetCount, MaxOverBudgetMS: stats.MaxOverBudgetMS,
	}, nil
}

func (service *ResourceService) DeleteHistory(ctx context.Context, ownerID, runID string) error {
	run, err := service.store.Run(ctx, ownerID, runID)
	if err != nil {
		return err
	}
	if service.artifacts == nil {
		return errors.New("gateway: artifact coordinator is not configured")
	}
	return service.artifacts.DeleteExecution(ctx, ownerID, runID, run.TraceID)
}

func (service *ResourceService) ClearHistory(ctx context.Context, ownerID string) (int64, error) {
	if service.artifacts == nil {
		return 0, errors.New("gateway: artifact coordinator is not configured")
	}
	return service.artifacts.ClearOwner(ctx, ownerID)
}

func (service *ResourceService) Trace(ctx context.Context, ownerID, traceID string) (TraceView, error) {
	record, observations, err := service.store.Trace(ctx, ownerID, traceID)
	if err != nil {
		return TraceView{}, err
	}
	artifacts, err := service.store.Artifacts(ctx, ownerID, traceID)
	if err != nil {
		return TraceView{}, err
	}
	publicArtifacts := make([]TraceArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		publicArtifacts = append(publicArtifacts, TraceArtifact{
			ArtifactID: artifact.ID, Kind: artifact.Kind, State: artifact.State, SHA256: artifact.SHA256,
			SizeBytes: artifact.SizeBytes, ContentType: artifact.ContentType, CreatedAt: artifact.CreatedAt,
		})
	}
	publicObservations := make([]TraceObservation, 0, len(observations))
	for _, observation := range observations {
		publicObservations = append(publicObservations, TraceObservation{
			Sequence: observation.Sequence, Type: observation.Type,
			Data: append(json.RawMessage(nil), observation.Data...), CreatedAt: observation.CreatedAt,
		})
	}
	resources := unavailableTraceResources()
	recordPayload := append(json.RawMessage(nil), record.Record...)
	run, runErr := service.store.RunByTrace(ctx, ownerID, traceID)
	switch {
	case runErr == nil:
		recordPayload = append(json.RawMessage(nil), run.Result...)
		resources = TraceResources{
			Request:  availableTraceResource(run.Request),
			Response: availableTraceResource(run.Result),
		}
	case errors.Is(runErr, postgres.ErrNotFound):
		// Retained v1 orphan traces remain readable until the explicit audited
		// reconciliation. New writes are always run-bound.
	default:
		return TraceView{}, runErr
	}
	return TraceView{
		TraceID: traceID, Record: recordPayload,
		Observations: publicObservations, Artifacts: publicArtifacts, Resources: resources,
	}, nil
}

func availableTraceResource(payload json.RawMessage) TraceResource {
	return TraceResource{Available: true, Payload: append(json.RawMessage(nil), payload...)}
}

func unavailableTraceResources() TraceResources {
	return TraceResources{
		Request: TraceResource{
			Message: "Request payload is not available for this trace.",
		},
		Response: TraceResource{
			Message: "Response payload is not available for this trace.",
		},
	}
}

func (service *ResourceService) PresignArtifact(ctx context.Context, ownerID, traceID, artifactID string) (string, error) {
	if service.artifacts == nil {
		return "", errors.New("gateway: artifact coordinator is not configured")
	}
	artifact, err := service.store.Artifact(ctx, ownerID, traceID, artifactID)
	if err != nil {
		return "", err
	}
	return service.artifacts.PresignGet(ctx, ownerID, artifact.ObjectKey, service.artifactTTL)
}

func defaultClientState() ClientState {
	return ClientState{SchemaVersion: clientStateSchemaVersion, CallType: string(hardenllm.CallTypeText), CacheMode: string(hardenllm.CacheModeOff)}
}

func validateClientState(state ClientState) error {
	if state.SchemaVersion != clientStateSchemaVersion || !utf8.ValidString(state.SystemPrompt) || !utf8.ValidString(state.UserPrompt) ||
		!utf8.ValidString(state.SchemaShorthand) || len(state.SelectedProfileID) > 1500 || len(state.ModelID) > 512 ||
		len(state.SystemPrompt) > 32<<10 || len(state.UserPrompt) > 64<<10 || len(state.SchemaShorthand) > 64<<10 {
		return fmt.Errorf("%w: client state fields", ErrInvalidRequest)
	}
	if state.CallType != string(hardenllm.CallTypeText) && state.CallType != string(hardenllm.CallTypeStructured) {
		return fmt.Errorf("%w: call type", ErrInvalidRequest)
	}
	if state.ReasoningEffort != "" && state.ReasoningEffort != string(hardenllm.ReasoningEffortLowest) && state.ReasoningEffort != string(hardenllm.ReasoningEffortMiddle) && state.ReasoningEffort != string(hardenllm.ReasoningEffortHighest) {
		return fmt.Errorf("%w: reasoning effort", ErrInvalidRequest)
	}
	for profileID, effort := range state.ReasoningByProfile {
		if len(profileID) > 1500 || (effort != string(hardenllm.ReasoningEffortLowest) && effort != string(hardenllm.ReasoningEffortMiddle) && effort != string(hardenllm.ReasoningEffortHighest)) {
			return fmt.Errorf("%w: reasoning profile map", ErrInvalidRequest)
		}
	}
	if state.CacheMode != string(hardenllm.CacheModeOff) && state.CacheMode != string(hardenllm.CacheModeCache) && state.CacheMode != string(hardenllm.CacheModeRefresh) {
		return fmt.Errorf("%w: cache mode", ErrInvalidRequest)
	}
	if len(state.Schema) > 64<<10 {
		return fmt.Errorf("%w: schema", ErrInvalidRequest)
	}
	if len(state.Schema) > 0 && string(state.Schema) != "null" {
		var schema map[string]any
		if json.Unmarshal(state.Schema, &schema) != nil || schema == nil {
			return fmt.Errorf("%w: schema", ErrInvalidRequest)
		}
	}
	if encoded, err := json.Marshal(state.ProviderOptions); err != nil || len(encoded) > 32<<10 || containsSecretKey(state.ProviderOptions) {
		return fmt.Errorf("%w: provider options", ErrInvalidRequest)
	}
	if len(state.RepairEscalation) > 0 {
		if encoded, err := json.Marshal(state.RepairEscalation); err != nil || len(encoded) > 16<<10 || containsSecretKey(state.RepairEscalation) {
			return fmt.Errorf("%w: repair escalation", ErrInvalidRequest)
		}
	}
	if len(state.UI) > 0 {
		if encoded, err := json.Marshal(state.UI); err != nil || len(encoded) > 16<<10 || containsSecretKey(state.UI) {
			return fmt.Errorf("%w: ui state", ErrInvalidRequest)
		}
	}
	if state.MaxAttempts < 0 || state.MaxAttempts > maximumRunAttempts || state.InitialBackoffMS < 0 || state.InitialBackoffMS > 60000 || state.MaximumBackoffMS < 0 || state.MaximumBackoffMS > 600000 {
		return fmt.Errorf("%w: retry controls", ErrInvalidRequest)
	}
	return nil
}

func containsSecretKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if isSecretKey(key) {
				return true
			}
			if containsSecretKey(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsSecretKey(nested) {
				return true
			}
		}
	}
	return false
}

func isSecretKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", ""), "_", ""))

	// Provider request controls such as max_tokens and max_output_tokens are
	// ordinary utility-llm options, not credentials. Match credential-shaped
	// names explicitly so those options remain usable without allowing common
	// credential fields through the persisted state or run boundary.
	for _, prefix := range []string{"authorization", "apikey", "credential", "password", "secret"} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}

	switch normalized {
	case "token", "accesstoken", "authtoken", "bearertoken", "clienttoken", "idtoken", "refreshtoken", "sessiontoken":
		return true
	default:
		return false
	}
}

func encodeHistoryCursor(cursor postgres.RunCursor) (string, error) {
	encoded, err := json.Marshal(struct {
		StartedAt time.Time `json:"startedAt"`
		ID        string    `json:"id"`
	}{StartedAt: cursor.StartedAt.UTC(), ID: cursor.ID})
	if err != nil {
		return "", fmt.Errorf("gateway: encode history cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeHistoryCursor(value string) (postgres.RunCursor, error) {
	if len(value) > 512 {
		return postgres.RunCursor{}, ErrInvalidCursor
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return postgres.RunCursor{}, ErrInvalidCursor
	}
	var cursor struct {
		StartedAt time.Time `json:"startedAt"`
		ID        string    `json:"id"`
	}
	if err := decodeStrictJSON(decoded, &cursor); err != nil || cursor.StartedAt.IsZero() || cursor.ID == "" {
		return postgres.RunCursor{}, ErrInvalidCursor
	}
	return postgres.RunCursor{StartedAt: cursor.StartedAt.UTC(), ID: cursor.ID}, nil
}

func decodeStrictJSON(document []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("gateway: unexpected trailing JSON")
	}
	return nil
}
