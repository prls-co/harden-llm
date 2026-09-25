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
	persistenceTimeout  = 5 * time.Second
	maximumDurableRunMS = 2 * 60 * 60 * 1000
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
	RecoveryPolicy  hardenllm.RecoveryPolicy       `json:"recoveryPolicy"`
	Origin          hardenllm.Origin               `json:"origin,omitempty"`
	Progress        chan<- hardenllm.ProgressEvent `json:"-"`
	// Ready is an internal admission signal used by the request-bound SSE
	// writer. It is never serialized and is sent exactly once before provider
	// execution/cache lookup begins.
	Ready chan<- struct{} `json:"-"`
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
	Search              *hardenllm.SearchResult    `json:"search,omitempty"`
	SchemaVersion       int                        `json:"schemaVersion"`
	RunID               string                     `json:"runId"`
	Status              string                     `json:"status"`
	Output              any                        `json:"output"`
	CallID              string                     `json:"callId"`
	TraceID             string                     `json:"traceId"`
	Origin              hardenllm.Origin           `json:"origin,omitempty"`
	SelectedTarget      hardenllm.ExecutionTarget  `json:"selectedTarget"`
	ResultSource        hardenllm.ResultSource     `json:"resultSource"`
	Accounting          hardenllm.Accounting       `json:"accounting"`
	Attempts            []hardenllm.Attempt        `json:"attempts"`
	Cache               hardenllm.CacheResult      `json:"cache"`
	Artifacts           []RunArtifact              `json:"artifacts"`
	ProviderInvoked     bool                       `json:"providerInvoked"`
	TotalCallDurationMs int64                      `json:"totalCallDurationMs"`
	TotalWaitMs         int64                      `json:"totalWaitMs"`
	TotalActualWaitMs   int64                      `json:"totalActualWaitMs"`
	OverBudgetMs        int64                      `json:"overBudgetMs"`
	UsedRepair          bool                       `json:"usedRepair"`
	Diagnostics         *hardenllm.Diagnostics     `json:"diagnostics,omitempty"`
	GenerationTarget    *hardenllm.ExecutionTarget `json:"generationTarget,omitempty"`
	StopReason          string                     `json:"stopReason,omitempty"`
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
	return service.run(ctx, ownerID, input, "")
}

// RunWithID executes one provider operation under a caller-owned durable run
// identity. Durable Harden workflows use this entry point so an activity retry
// reconciles the same persisted execution instead of allocating another run.
func (service *RunService) RunWithID(ctx context.Context, ownerID string, input RunInput, runID string) (output RunOutput, state RunState, err error) {
	return service.run(ctx, ownerID, input, runID)
}

func (service *RunService) run(ctx context.Context, ownerID string, input RunInput, requestedRunID string) (output RunOutput, state RunState, err error) {
	if err := validateRunInput(input); err != nil {
		return RunOutput{}, RunState{}, err
	}
	if input.CacheVersion == "" {
		input.CacheVersion = defaultCacheVersion
	}
	if input.CacheMode == "" {
		input.CacheMode = hardenllm.CacheModeOff
	}
	runID := strings.TrimSpace(requestedRunID)
	if runID == "" {
		runID, err = service.newID()
		if err != nil {
			return RunOutput{}, RunState{}, errors.New("gateway: generate run ID")
		}
	}
	if strings.TrimSpace(runID) != runID || len(runID) > 128 || strings.IndexFunc(runID, func(value rune) bool {
		return !(value >= 'a' && value <= 'z') && !(value >= 'A' && value <= 'Z') && !(value >= '0' && value <= '9') && value != '_' && value != '-' && value != '.'
	}) >= 0 {
		return RunOutput{}, RunState{}, errors.New("gateway: requested run ID is invalid")
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
	// Resolve every enabled structured-recovery profile against the one
	// owner-scoped catalog snapshot before constructing the runtime caller or
	// admitting an SSE response. This keeps ordinary configuration errors on
	// the JSON error transport and guarantees that a missing leaf cannot cause
	// the selected generation call to run first.
	if err := validateRecoveryCatalog(input, catalog); err != nil {
		return RunOutput{}, RunState{}, err
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
	if input.Ready != nil {
		select {
		case input.Ready <- struct{}{}:
		default:
		}
	}
	startedAt := service.clock().UTC()
	callContext := ctx
	var cancelCall context.CancelFunc
	if input.TimeoutMS > 0 {
		callContext, cancelCall = context.WithTimeout(ctx, time.Duration(input.TimeoutMS)*time.Millisecond)
		defer cancelCall()
	}
	originMetadata := map[string]string{}
	for key, value := range map[string]string{
		"origin.client": input.Origin.Client, "origin.component": input.Origin.Component,
		"origin.operationId": input.Origin.OperationID, "origin.parentRunId": input.Origin.ParentRunID,
		"origin.jobId": input.Origin.JobID, "origin.testRunId": input.Origin.TestRunID,
		"origin.testId": input.Origin.TestID, "origin.sourceRevision": input.Origin.SourceRevision,
	} {
		if value != "" {
			originMetadata[key] = value
		}
	}
	result, callErr := caller.Call(callContext, hardenllm.Request{
		ProfileID: input.ProfileID, Profiles: catalog, SystemPrompt: input.SystemPrompt, UserPrompt: input.UserPrompt,
		CallType: input.CallType, Schema: append(json.RawMessage(nil), input.Schema...),
		ReasoningEffort: hardenllm.ReasoningEffort(input.ReasoningEffort), ProviderOptions: cloneAnyMap(input.ProviderOptions),
		WebSearch: input.WebSearch,
		Context:   hardenllm.ObservabilityContext{TaskID: runID, RunID: runID, OrganizationID: ownerID, Metadata: originMetadata},
		Origin:    input.Origin,
		CacheMode: input.CacheMode, CacheVersion: input.CacheVersion,
		RecoveryPolicy: input.RecoveryPolicy,
		Progress:       input.Progress,
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
		SchemaVersion: 4, RunID: runID, Status: status,
		Output: result.Output, CallID: result.CallID, TraceID: traceID,
		Origin:         input.Origin,
		SelectedTarget: result.SelectedTarget, ResultSource: result.ResultSource, Accounting: result.Accounting,
		Attempts: cloneAttempts(result.Attempts),
		Cache:    result.Cache, Artifacts: artifacts, TotalCallDurationMs: totalCallDurationMs,
		Search: result.Search, ProviderInvoked: attemptsInvokedProvider(result.Attempts),
		TotalWaitMs: totalWaitMs, TotalActualWaitMs: result.Diagnostics.TotalActualWaitMs, OverBudgetMs: overBudgetMs, UsedRepair: usedRepair,
	}
	if result.Diagnostics.StopReason != "" || result.Diagnostics.AttemptsUsed > 0 || result.Diagnostics.ReceivedBytes > 0 {
		output.Diagnostics = &result.Diagnostics
		generationTarget := result.GenerationTarget
		output.GenerationTarget = &generationTarget
		output.StopReason = result.Diagnostics.StopReason
	}
	requestDocument, _ := json.Marshal(input)
	resultDocument, _ := json.Marshal(output)
	traceDocument, _ := json.Marshal(map[string]any{
		"schemaVersion": 4, "runId": runID, "traceId": traceID,
		"origin": input.Origin, "attempts": result.Attempts, "resultSource": result.ResultSource,
		"generationTarget": result.GenerationTarget, "diagnostics": result.Diagnostics,
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
		// Keep the canonical runtime result available to a request-bound SSE
		// terminal event and to callers diagnosing a persistence failure. JSON
		// callers still receive the same transport error, but the persisted
		// execution identity and diagnostics are never discarded in memory.
		return output, state, errors.New("gateway: persist completed run")
	}
	return output, state, nil
}

func validateRecoveryCatalog(input RunInput, catalog hardenllm.ProfileCatalog) error {
	if input.CallType != hardenllm.CallTypeStructured || !input.RecoveryPolicy.UsesExplicitPlan() {
		return nil
	}
	check := func(role string, target hardenllm.RecoveryTarget, allowGeneration bool) error {
		if err := target.Validate("recoveryPolicy."+role, allowGeneration); err != nil {
			return err
		}
		if target.Source == "generation" {
			return nil
		}
		profile, ok := catalog[target.ProfileID]
		if !ok || profile.LLMProfile != target.ProfileID {
			return &hardenllm.RecoveryPolicyError{Field: "recoveryPolicy." + role + ".profileId", Message: fmt.Sprintf("profile %q was not found", target.ProfileID)}
		}
		return nil
	}
	policy := input.RecoveryPolicy
	if policy.JSONRepair != nil {
		if err := check("jsonRepair.initial", policy.JSONRepair.Initial, true); err != nil {
			return err
		}
		if policy.JSONRepair.Escalation != nil {
			if err := check("jsonRepair.escalation", *policy.JSONRepair.Escalation, true); err != nil {
				return err
			}
		}
	}
	if policy.Rerun != nil {
		if err := check("rerun.target", policy.Rerun.Target, false); err != nil {
			return err
		}
		if policy.Rerun.JSONRepair != nil {
			if err := check("rerun.jsonRepair.initial", policy.Rerun.JSONRepair.Initial, true); err != nil {
				return err
			}
			if policy.Rerun.JSONRepair.Escalation != nil {
				if err := check("rerun.jsonRepair.escalation", *policy.Rerun.JSONRepair.Escalation, true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func nonblockingProgress(channel chan<- hardenllm.ProgressEvent, event hardenllm.ProgressEvent) {
	select {
	case channel <- event:
	default:
	}
}

func executionFields(result hardenllm.Result, output RunOutput) *postgres.ExecutionFields {
	producer := hardenllm.ExecutionTarget{}
	if result.ResultSource.Producer != nil {
		producer = *result.ResultSource.Producer
	}
	return &postgres.ExecutionFields{
		SchemaVersion:    4,
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

func cloneAttempts(attempts []hardenllm.Attempt) []hardenllm.Attempt {
	return append([]hardenllm.Attempt{}, attempts...)
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
	if len(input.CacheVersion) > 64 || input.TimeoutMS < 0 || input.TimeoutMS > maximumDurableRunMS {
		return fmt.Errorf("%w: run controls", ErrInvalidRequest)
	}
	if err := validateOrigin(input.Origin); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	if err := input.RecoveryPolicy.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	if err := input.RecoveryPolicy.ValidateProviderOptions(input.ProviderOptions); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	if encoded, err := json.Marshal(input.ProviderOptions); err != nil || len(encoded) > 32<<10 || containsSecretKey(input.ProviderOptions) {
		return fmt.Errorf("%w: provider options", ErrInvalidRequest)
	}
	return nil
}

func validateOrigin(origin hardenllm.Origin) error {
	encoded, err := json.Marshal(origin)
	if err != nil || len(encoded) > 2<<10 {
		return errors.New("origin exceeds the 2 KiB limit")
	}
	for name, value := range map[string]string{
		"client": origin.Client, "component": origin.Component, "operationId": origin.OperationID,
		"parentRunId": origin.ParentRunID, "jobId": origin.JobID, "testRunId": origin.TestRunID,
		"testId": origin.TestID, "sourceRevision": origin.SourceRevision,
	} {
		if !utf8.ValidString(value) || len(value) > 256 {
			return fmt.Errorf("origin.%s exceeds the 256-byte limit", name)
		}
	}
	return nil
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
		SchemaVersion: 3, CacheVersion: record.Version, OperationHash: record.OperationHash,
		ProviderResult: record.Result, CreatedAt: record.CreatedAt,
	}, true, nil
}

func (cache *ownerCacheStore) Set(ctx context.Context, operationHash string, record hardenllm.CacheRecord) error {
	now := cache.clock().UTC()
	return cache.store.PutCache(ctx, postgres.CacheRecord{
		OwnerID: cache.ownerID, Version: record.CacheVersion, OperationHash: operationHash,
		Result: record.ProviderResult, CreatedAt: record.CreatedAt, UpdatedAt: now,
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
