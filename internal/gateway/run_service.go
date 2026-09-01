package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/postgres"
)

const (
	defaultCacheVersion = "operation-v2"
	maximumRunAttempts  = 10
	persistenceTimeout  = 5 * time.Second
)

type RunInput struct {
	ProfileID        string              `json:"profileId"`
	ModelID          string              `json:"modelId,omitempty"`
	SystemPrompt     string              `json:"systemPrompt,omitempty"`
	UserPrompt       string              `json:"userPrompt"`
	CallType         hardenllm.CallType  `json:"callType"`
	Schema           json.RawMessage     `json:"schema,omitempty"`
	ReasoningEffort  string              `json:"reasoningEffort,omitempty"`
	ProviderOptions  map[string]any      `json:"providerOptions,omitempty"`
	CacheMode        hardenllm.CacheMode `json:"cacheMode,omitempty"`
	CacheVersion     string              `json:"cacheVersion,omitempty"`
	MaxAttempts      int                 `json:"maxAttempts,omitempty"`
	StructuredRepair bool                `json:"structuredRepair,omitempty"`
	TimeoutMS        int                 `json:"timeoutMs,omitempty"`
	InitialBackoffMS int                 `json:"initialBackoffMs,omitempty"`
	MaximumBackoffMS int                 `json:"maximumBackoffMs,omitempty"`
	RetryNetwork     *bool               `json:"retryNetwork,omitempty"`
	RetryRateLimit   *bool               `json:"retryRateLimit,omitempty"`
	RetryServerError *bool               `json:"retryServerError,omitempty"`
	RetryEmpty       *bool               `json:"retryEmpty,omitempty"`
	RetryParse       *bool               `json:"retryParse,omitempty"`
	RepairEscalation *RepairEscalation   `json:"repairEscalation,omitempty"`
}

type RepairEscalation struct {
	Attempt         int    `json:"attempt"`
	ProfileID       string `json:"profileId,omitempty"`
	ModelID         string `json:"modelId"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

type RunArtifact struct {
	ArtifactID  string `json:"artifactId"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"sizeBytes"`
	ContentType string `json:"contentType"`
}

type RunOutput struct {
	SchemaVersion       int                       `json:"schemaVersion"`
	RunID               string                    `json:"runId"`
	Status              string                    `json:"status"`
	Output              any                       `json:"output"`
	CallID              string                    `json:"callId"`
	TraceID             string                    `json:"traceId"`
	SelectedTarget      hardenllm.ExecutionTarget `json:"selectedTarget"`
	ResultSource        hardenllm.ResultSource    `json:"resultSource"`
	Accounting          hardenllm.Accounting      `json:"accounting"`
	Attempts            []hardenllm.Attempt       `json:"attempts"`
	Cache               hardenllm.CacheResult     `json:"cache"`
	Artifacts           []RunArtifact             `json:"artifacts"`
	ProviderInvoked     bool                      `json:"providerInvoked"`
	TotalCallDurationMs int64                     `json:"totalCallDurationMs"`
	TotalWaitMs         int64                     `json:"totalWaitMs"`
	OverBudgetMs        int64                     `json:"overBudgetMs"`
	UsedRepair          bool                      `json:"usedRepair"`
}

type RunState struct {
	LastRunID   string `json:"lastRunId"`
	LastTraceID string `json:"lastTraceId"`
}

type RuntimeCaller interface {
	Call(context.Context, hardenllm.Request) (hardenllm.Result, error)
}

type RuntimeClientConfig struct {
	OwnerID     string
	Credentials hardenllm.CredentialResolver
	Cache       hardenllm.CacheStore
	Artifacts   hardenllm.ArtifactStore
}

type RuntimeCallerFactory func(RuntimeClientConfig) (RuntimeCaller, error)
type RuntimeArtifactScope func(ownerID string) (hardenllm.ArtifactStore, error)

type RunServiceConfig struct {
	Store         *postgres.Store
	Profiles      *ProfileService
	CallerFactory RuntimeCallerFactory
	ArtifactScope RuntimeArtifactScope
	Clock         func() time.Time
	NewID         func() (string, error)
	Telemetry     *Telemetry
	Logger        *slog.Logger
}

type RunService struct {
	store         *postgres.Store
	profiles      *ProfileService
	callerFactory RuntimeCallerFactory
	artifactScope RuntimeArtifactScope
	clock         func() time.Time
	newID         func() (string, error)
	telemetry     *Telemetry
	logger        *slog.Logger
}

func NewRunService(config RunServiceConfig) (*RunService, error) {
	if config.Store == nil || config.Profiles == nil || config.CallerFactory == nil {
		return nil, errors.New("gateway: run store, profiles, and caller factory are required")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.NewID == nil {
		config.NewID = newGatewayID
	}
	if config.Telemetry == nil {
		config.Telemetry = newNoopTelemetry()
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	return &RunService{
		store: config.Store, profiles: config.Profiles, callerFactory: config.CallerFactory,
		artifactScope: config.ArtifactScope, clock: config.Clock, newID: config.NewID,
		telemetry: config.Telemetry, logger: config.Logger,
	}, nil
}

func (service *RunService) Run(ctx context.Context, ownerID string, input RunInput) (output RunOutput, state RunState, err error) {
	if err := validateRunInput(input); err != nil {
		return RunOutput{}, RunState{}, err
	}
	if input.CacheVersion == "" {
		input.CacheVersion = defaultCacheVersion
	}
	if input.CacheMode == "" {
		input.CacheMode = hardenllm.CacheModeOff
	}
	runID, err := service.newID()
	if err != nil {
		return RunOutput{}, RunState{}, errors.New("gateway: generate run ID")
	}
	catalog, credentials, err := service.profiles.RuntimeProfiles(ctx, ownerID)
	if err != nil {
		return RunOutput{}, RunState{}, err
	}
	if _, ok := catalog[input.ProfileID]; !ok {
		return RunOutput{}, RunState{}, postgres.ErrNotFound
	}
	profile := catalog[input.ProfileID]
	if input.ModelID != "" {
		profile.ModelID = strings.TrimSpace(input.ModelID)
		catalog[input.ProfileID] = profile
	}
	runStartedAt := service.clock()
	ctx, endRun := service.telemetry.StartOperation(ctx, OperationRun)
	defer func() {
		outcome, category := gatewayOutcome(err)
		service.logger.InfoContext(ctx, "run completed",
			"run_id", runID, "call_id", output.CallID, "trace_id", state.LastTraceID,
			"profile", input.ProfileID, "model", profile.ModelID, "provider", profile.Provider,
			"outcome", outcome, "category", category, "duration_ms", time.Since(runStartedAt).Milliseconds(),
		)
		endRun(err)
	}()
	var artifactStore hardenllm.ArtifactStore
	if service.artifactScope != nil {
		artifactStore, err = service.artifactScope(ownerID)
		if err != nil {
			artifactStore = nil
		}
	}
	cache := &ownerCacheStore{store: service.store, ownerID: ownerID, version: input.CacheVersion, clock: service.clock}
	caller, err := service.callerFactory(RuntimeClientConfig{OwnerID: ownerID, Credentials: credentials, Cache: cache, Artifacts: artifactStore})
	if err != nil || caller == nil {
		return RunOutput{}, RunState{}, errors.New("gateway: initialize runtime caller")
	}
	startedAt := service.clock().UTC()
	callContext := ctx
	var cancelCall context.CancelFunc
	if input.TimeoutMS > 0 {
		callContext, cancelCall = context.WithTimeout(ctx, time.Duration(input.TimeoutMS)*time.Millisecond)
		defer cancelCall()
	}
	result, callErr := caller.Call(callContext, hardenllm.Request{
		ProfileID: input.ProfileID, Profiles: catalog, SystemPrompt: input.SystemPrompt, UserPrompt: input.UserPrompt,
		CallType: input.CallType, Schema: append(json.RawMessage(nil), input.Schema...),
		ReasoningEffort: hardenllm.ReasoningEffort(input.ReasoningEffort), ProviderOptions: cloneAnyMap(input.ProviderOptions),
		Context:   hardenllm.ObservabilityContext{TaskID: runID, RunID: runID, OrganizationID: ownerID},
		CacheMode: input.CacheMode, CacheVersion: input.CacheVersion,
		RetryPolicy: hardenllm.RetryPolicy{
			MaxAttempts: input.MaxAttempts, InitialBackoff: time.Duration(input.InitialBackoffMS) * time.Millisecond,
			MaximumBackoff: time.Duration(input.MaximumBackoffMS) * time.Millisecond,
			RetryNetwork:   input.RetryNetwork, RetryRateLimit: input.RetryRateLimit, RetryServerError: input.RetryServerError,
			RetryEmpty: input.RetryEmpty, RetryParse: input.RetryParse,
			StructuredRepair: hardenllm.StructuredRepairPolicy{Enabled: input.StructuredRepair, Escalation: repairEscalation(input.RepairEscalation)},
		},
	})
	completedAt := service.clock().UTC()
	traceID := result.TraceID
	if traceID == "" {
		traceID, _ = service.newID()
	}
	status := "succeeded"
	if callErr != nil {
		status = "failed"
		if errors.Is(callErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = "timeout"
		}
	}
	artifacts, artifactRecords := runArtifacts(ownerID, runID, traceID, result.Artifacts, completedAt)
	totalCallDurationMs := elapsedMilliseconds(startedAt, completedAt)
	totalWaitMs := attemptWaitMilliseconds(result.Attempts)
	overBudgetMs := int64(0)
	if input.TimeoutMS > 0 {
		overBudgetMs = max(totalCallDurationMs-int64(input.TimeoutMS), 0)
	}
	usedRepair := attemptsUsedRepair(result.Attempts)
	output = RunOutput{
		SchemaVersion: 2, RunID: runID, Status: status,
		Output: result.Output, CallID: result.CallID, TraceID: traceID,
		SelectedTarget: result.SelectedTarget, ResultSource: result.ResultSource, Accounting: result.Accounting,
		Attempts: append([]hardenllm.Attempt(nil), result.Attempts...),
		Cache:    result.Cache, Artifacts: artifacts, TotalCallDurationMs: totalCallDurationMs,
		ProviderInvoked: attemptsInvokedProvider(result.Attempts),
		TotalWaitMs:     totalWaitMs, OverBudgetMs: overBudgetMs, UsedRepair: usedRepair,
	}
	requestDocument, _ := json.Marshal(input)
	resultDocument, _ := json.Marshal(output)
	traceDocument, _ := json.Marshal(map[string]any{
		"schemaVersion": 2, "runId": runID, "traceId": traceID,
	})
	observations := runObservations(ownerID, traceID, result.Attempts, completedAt)
	persistContext, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), persistenceTimeout)
	defer persistCancel()
	persistContext, endPersistence := service.telemetry.StartPersistence(persistContext, "postgres", OperationTracePersistence)
	persistErr := service.store.SaveExecution(persistContext, postgres.RunRecord{
		OwnerID: ownerID, ID: runID, ProfileID: input.ProfileID, TraceID: traceID, Status: status,
		Request: requestDocument, Result: resultDocument, Execution: executionFields(result, output),
		StartedAt: startedAt, CompletedAt: completedAt,
	}, postgres.TraceRecord{OwnerID: ownerID, TraceID: traceID, RunID: runID, Record: traceDocument, CreatedAt: startedAt, UpdatedAt: completedAt}, observations, artifactRecords)
	endPersistence(persistErr)
	state = RunState{LastRunID: runID, LastTraceID: traceID}
	if callErr != nil {
		return output, state, callErr
	}
	if persistErr != nil {
		return RunOutput{}, state, errors.New("gateway: persist completed run")
	}
	return output, state, nil
}

func executionFields(result hardenllm.Result, output RunOutput) *postgres.ExecutionFields {
	producer := hardenllm.ExecutionTarget{}
	if result.ResultSource.Producer != nil {
		producer = *result.ResultSource.Producer
	}
	return &postgres.ExecutionFields{
		SchemaVersion:    2,
		SelectedProvider: result.SelectedTarget.Provider, SelectedProtocol: result.SelectedTarget.Protocol,
		SelectedEndpoint: result.SelectedTarget.Endpoint, SelectedModelID: result.SelectedTarget.ModelID,
		ResultSource:      string(result.ResultSource.Kind),
		ProducerProfileID: producer.ProfileID, ProducerProvider: producer.Provider,
		ProducerProtocol: producer.Protocol, ProducerEndpoint: producer.Endpoint,
		ProducerModelID: producer.ModelID,
		ProviderInvoked: output.ProviderInvoked,
		ResultUsage: postgres.UsageFields{
			Status:      result.Accounting.Result.Usage.Status,
			InputTokens: result.Accounting.Result.Usage.InputTokens, CacheReadTokens: result.Accounting.Result.Usage.CacheReadTokens,
			CacheCreationTokens: result.Accounting.Result.Usage.CacheCreationTokens,
			OutputTokens:        result.Accounting.Result.Usage.OutputTokens, ReasoningTokens: result.Accounting.Result.Usage.ReasoningTokens,
		},
		ProviderUsage: postgres.UsageFields{
			Status:      result.Accounting.Provider.Usage.Status,
			InputTokens: result.Accounting.Provider.Usage.InputTokens, CacheReadTokens: result.Accounting.Provider.Usage.CacheReadTokens,
			CacheCreationTokens: result.Accounting.Provider.Usage.CacheCreationTokens,
			OutputTokens:        result.Accounting.Provider.Usage.OutputTokens, ReasoningTokens: result.Accounting.Provider.Usage.ReasoningTokens,
		},
		ResultCost: postgres.CostFields{
			Status: result.Accounting.Result.Cost.Status, KnownSubtotalUSD: result.Accounting.Result.Cost.KnownSubtotalUSD,
			KnownObservations:   result.Accounting.Result.Cost.KnownObservations,
			UnknownObservations: result.Accounting.Result.Cost.UnknownObservations,
		},
		ProviderCost: postgres.CostFields{
			Status: result.Accounting.Provider.Cost.Status, KnownSubtotalUSD: result.Accounting.Provider.Cost.KnownSubtotalUSD,
			KnownObservations:   result.Accounting.Provider.Cost.KnownObservations,
			UnknownObservations: result.Accounting.Provider.Cost.UnknownObservations,
		},
		CacheServed: result.Cache.Served, TotalCallDurationMS: output.TotalCallDurationMs, OverBudgetMS: output.OverBudgetMs,
	}
}

func elapsedMilliseconds(started, completed time.Time) int64 {
	if completed.Before(started) {
		return 0
	}
	return completed.Sub(started).Milliseconds()
}

func attemptWaitMilliseconds(attempts []hardenllm.Attempt) int64 {
	var total int64
	for _, attempt := range attempts {
		total += attempt.Wait.Milliseconds()
	}
	return total
}

func attemptsUsedRepair(attempts []hardenllm.Attempt) bool {
	for _, attempt := range attempts {
		if attempt.Repair {
			return true
		}
	}
	return false
}

func attemptsInvokedProvider(attempts []hardenllm.Attempt) bool {
	for _, attempt := range attempts {
		if attempt.ProviderUsed {
			return true
		}
	}
	return false
}

func validateRunInput(input RunInput) error {
	if strings.TrimSpace(input.ProfileID) == "" || len(input.ProfileID) > 1500 || !utf8.ValidString(input.ModelID) || len(input.ModelID) > 512 || !utf8.ValidString(input.SystemPrompt) || !utf8.ValidString(input.UserPrompt) ||
		len(input.SystemPrompt) > 32<<10 || len(input.UserPrompt) == 0 || len(input.UserPrompt) > 64<<10 {
		return fmt.Errorf("%w: run fields", ErrInvalidRequest)
	}
	if input.CallType != hardenllm.CallTypeText && input.CallType != hardenllm.CallTypeStructured {
		return fmt.Errorf("%w: call type", ErrInvalidRequest)
	}
	if input.CallType == hardenllm.CallTypeStructured {
		var schema map[string]any
		if len(input.Schema) == 0 || len(input.Schema) > 64<<10 || json.Unmarshal(input.Schema, &schema) != nil || schema == nil {
			return fmt.Errorf("%w: structured schema", ErrInvalidRequest)
		}
	} else if len(input.Schema) != 0 && string(input.Schema) != "null" {
		return fmt.Errorf("%w: text schema", ErrInvalidRequest)
	}
	if input.ReasoningEffort != "" && input.ReasoningEffort != string(hardenllm.ReasoningEffortLowest) && input.ReasoningEffort != string(hardenllm.ReasoningEffortMiddle) && input.ReasoningEffort != string(hardenllm.ReasoningEffortHighest) {
		return fmt.Errorf("%w: reasoning effort", ErrInvalidRequest)
	}
	if input.CacheMode != "" && input.CacheMode != hardenllm.CacheModeOff && input.CacheMode != hardenllm.CacheModeCache && input.CacheMode != hardenllm.CacheModeRefresh {
		return fmt.Errorf("%w: cache mode", ErrInvalidRequest)
	}
	if len(input.CacheVersion) > 64 || (input.MaxAttempts < 0 || input.MaxAttempts > maximumRunAttempts) || input.TimeoutMS < 0 || input.TimeoutMS > 60000 || input.InitialBackoffMS < 0 || input.InitialBackoffMS > 60000 || input.MaximumBackoffMS < 0 || input.MaximumBackoffMS > 600000 {
		return fmt.Errorf("%w: run controls", ErrInvalidRequest)
	}
	if input.RepairEscalation != nil {
		escalation := input.RepairEscalation
		if escalation.Attempt < 2 || escalation.Attempt > maximumRunAttempts ||
			(strings.TrimSpace(escalation.ProfileID) != "" && (!utf8.ValidString(escalation.ProfileID) || len(escalation.ProfileID) > 1500)) ||
			!utf8.ValidString(escalation.ModelID) || strings.TrimSpace(escalation.ModelID) == "" || len(escalation.ModelID) > 512 ||
			(escalation.ReasoningEffort != "" && escalation.ReasoningEffort != string(hardenllm.ReasoningEffortLowest) && escalation.ReasoningEffort != string(hardenllm.ReasoningEffortMiddle) && escalation.ReasoningEffort != string(hardenllm.ReasoningEffortHighest)) {
			return fmt.Errorf("%w: repair escalation", ErrInvalidRequest)
		}
	}
	if encoded, err := json.Marshal(input.ProviderOptions); err != nil || len(encoded) > 32<<10 || containsSecretKey(input.ProviderOptions) {
		return fmt.Errorf("%w: provider options", ErrInvalidRequest)
	}
	return nil
}

func repairEscalation(input *RepairEscalation) *hardenllm.RepairEscalation {
	if input == nil {
		return nil
	}
	return &hardenllm.RepairEscalation{
		Attempt: input.Attempt, ProfileID: strings.TrimSpace(input.ProfileID), ModelID: strings.TrimSpace(input.ModelID), ReasoningEffort: hardenllm.ReasoningEffort(strings.TrimSpace(input.ReasoningEffort)),
	}
}

type ownerCacheStore struct {
	store   *postgres.Store
	ownerID string
	version string
	clock   func() time.Time
}

func (cache *ownerCacheStore) Get(ctx context.Context, operationHash string) (hardenllm.CacheRecord, bool, error) {
	record, err := cache.store.Cache(ctx, cache.ownerID, cache.version, operationHash)
	if errors.Is(err, postgres.ErrNotFound) {
		return hardenllm.CacheRecord{}, false, nil
	}
	if err != nil {
		return hardenllm.CacheRecord{}, false, err
	}
	return hardenllm.CacheRecord{
		SchemaVersion: 2, CacheVersion: record.Version, OperationHash: record.OperationHash,
		Operation: record.Operation, RawProviderEnvelope: record.Envelope, ProviderResult: record.Result, CreatedAt: record.CreatedAt,
	}, true, nil
}

func (cache *ownerCacheStore) Set(ctx context.Context, operationHash string, record hardenllm.CacheRecord) error {
	var projection struct {
		Accounting struct {
			Usage json.RawMessage `json:"usage"`
			Cost  json.RawMessage `json:"cost"`
		} `json:"accounting"`
	}
	if err := json.Unmarshal(record.ProviderResult, &projection); err != nil {
		return errors.New("gateway: cached provider result is invalid")
	}
	if len(projection.Accounting.Usage) == 0 {
		projection.Accounting.Usage = json.RawMessage(`{}`)
	}
	if len(projection.Accounting.Cost) == 0 {
		projection.Accounting.Cost = json.RawMessage(`{}`)
	}
	now := cache.clock().UTC()
	return cache.store.PutCache(ctx, postgres.CacheRecord{
		OwnerID: cache.ownerID, Version: record.CacheVersion, OperationHash: operationHash,
		Operation: record.Operation, Result: record.ProviderResult,
		Usage: projection.Accounting.Usage, Cost: projection.Accounting.Cost,
		Envelope: record.RawProviderEnvelope, CreatedAt: record.CreatedAt, UpdatedAt: now,
	})
}

func (cache *ownerCacheStore) Delete(ctx context.Context, operationHash string) error {
	return cache.store.DeleteCache(ctx, cache.ownerID, operationHash)
}

func runArtifacts(ownerID, runID, traceID string, references []hardenllm.ArtifactRef, now time.Time) ([]RunArtifact, []postgres.ArtifactRecord) {
	public := make([]RunArtifact, 0, len(references))
	records := make([]postgres.ArtifactRecord, 0, len(references))
	for _, reference := range references {
		if reference.ArtifactID == "" || len(reference.ArtifactID) > 128 ||
			(reference.Kind != "trace" && reference.Kind != "parse-failure-response" && reference.Kind != "diagnostic-event") ||
			reference.Key == "" || reference.ContentType != "application/json" || reference.SizeBytes < 1 {
			continue
		}
		if len(reference.SHA256) != 64 || strings.ToLower(reference.SHA256) != reference.SHA256 {
			continue
		}
		public = append(public, RunArtifact{ArtifactID: reference.ArtifactID, Kind: reference.Kind, State: "available", SHA256: reference.SHA256, SizeBytes: reference.SizeBytes, ContentType: reference.ContentType})
		records = append(records, postgres.ArtifactRecord{
			OwnerID: ownerID, RunID: runID, TraceID: traceID, ID: reference.ArtifactID, Kind: reference.Kind, ObjectKey: reference.Key,
			ContentType: reference.ContentType, SHA256: reference.SHA256, SizeBytes: reference.SizeBytes,
			State: "available", CreatedAt: now, UpdatedAt: now,
		})
	}
	return public, records
}

func runObservations(ownerID, traceID string, attempts []hardenllm.Attempt, now time.Time) []postgres.ObservationRecord {
	result := make([]postgres.ObservationRecord, 0, len(attempts))
	for index, attempt := range attempts {
		data, _ := json.Marshal(attempt)
		result = append(result, postgres.ObservationRecord{OwnerID: ownerID, TraceID: traceID, Sequence: index, Type: "provider.attempt", Data: data, CreatedAt: now})
	}
	return result
}
