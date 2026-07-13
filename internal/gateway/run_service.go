package gateway

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/traces"
)

const (
	defaultCacheVersion = "v1"
	maximumRunAttempts  = 10
	persistenceTimeout  = 5 * time.Second
)

type RunInput struct {
	ProfileID        string              `json:"profileId"`
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
}

type RunArtifact struct {
	ArtifactID  string `json:"artifactId"`
	Kind        string `json:"kind"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"sizeBytes"`
	ContentType string `json:"contentType"`
}

type RunOutput struct {
	RunID     string                `json:"runId"`
	Output    any                   `json:"output"`
	CallID    string                `json:"callId"`
	TraceID   string                `json:"traceId"`
	Usage     hardenllm.Usage       `json:"usage"`
	Cost      hardenllm.Cost        `json:"cost"`
	Attempts  []hardenllm.Attempt   `json:"attempts"`
	Cache     hardenllm.CacheResult `json:"cache"`
	Artifacts []RunArtifact         `json:"artifacts"`
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
}

type RunService struct {
	store         *postgres.Store
	profiles      *ProfileService
	callerFactory RuntimeCallerFactory
	artifactScope RuntimeArtifactScope
	clock         func() time.Time
	newID         func() (string, error)
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
	return &RunService{
		store: config.Store, profiles: config.Profiles, callerFactory: config.CallerFactory,
		artifactScope: config.ArtifactScope, clock: config.Clock, newID: config.NewID,
	}, nil
}

func (service *RunService) Run(ctx context.Context, ownerID string, input RunInput) (RunOutput, RunState, error) {
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
	result, callErr := caller.Call(ctx, hardenllm.Request{
		ProfileID: input.ProfileID, Profiles: catalog, SystemPrompt: input.SystemPrompt, UserPrompt: input.UserPrompt,
		CallType: input.CallType, Schema: append(json.RawMessage(nil), input.Schema...),
		ReasoningEffort: hardenllm.ReasoningEffort(input.ReasoningEffort), ProviderOptions: cloneAnyMap(input.ProviderOptions),
		Context:   hardenllm.ObservabilityContext{TaskID: runID, RunID: runID, OrganizationID: ownerID},
		CacheMode: input.CacheMode, CacheVersion: input.CacheVersion,
		RetryPolicy: hardenllm.RetryPolicy{MaxAttempts: input.MaxAttempts, StructuredRepair: hardenllm.StructuredRepairPolicy{Enabled: input.StructuredRepair}},
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
	output := RunOutput{
		RunID: runID, Output: result.Output, CallID: result.CallID, TraceID: traceID,
		Usage: result.Usage, Cost: result.Cost, Attempts: append([]hardenllm.Attempt(nil), result.Attempts...),
		Cache: result.Cache, Artifacts: artifacts,
	}
	requestDocument, _ := json.Marshal(input)
	persistedOutput := output
	persistedOutput.Artifacts = nil
	resultDocument, _ := json.Marshal(persistedOutput)
	traceDocument, _ := json.Marshal(map[string]any{
		"schemaVersion": 1, "runId": runID, "callId": result.CallID, "traceId": traceID,
		"status": status, "profileId": input.ProfileID, "output": result.Output,
		"usage": result.Usage, "cost": result.Cost, "attempts": result.Attempts, "cache": result.Cache,
	})
	observations := runObservations(ownerID, traceID, result.Attempts, completedAt)
	persistContext, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), persistenceTimeout)
	defer persistCancel()
	persistErr := service.store.SaveExecution(persistContext, postgres.RunRecord{
		OwnerID: ownerID, ID: runID, ProfileID: input.ProfileID, TraceID: traceID, Status: status,
		Request: requestDocument, Result: resultDocument, StartedAt: startedAt, CompletedAt: completedAt,
	}, postgres.TraceRecord{OwnerID: ownerID, TraceID: traceID, Record: traceDocument, CreatedAt: startedAt, UpdatedAt: completedAt}, observations)
	if persistErr == nil {
		for _, artifact := range artifactRecords {
			if saveErr := service.store.SaveArtifact(persistContext, artifact); saveErr != nil {
				output.Artifacts = removeRunArtifact(output.Artifacts, artifact.ID)
			}
		}
		updatedResult, marshalErr := json.Marshal(output)
		if marshalErr != nil {
			persistErr = errors.New("gateway: encode persisted run result")
		} else if updateErr := service.store.UpdateRunResult(persistContext, ownerID, runID, updatedResult); updateErr != nil {
			persistErr = updateErr
		}
	}
	state := RunState{LastRunID: runID, LastTraceID: traceID}
	if callErr != nil {
		return RunOutput{}, state, callErr
	}
	if persistErr != nil {
		return RunOutput{}, state, errors.New("gateway: persist completed run")
	}
	return output, state, nil
}

func validateRunInput(input RunInput) error {
	if strings.TrimSpace(input.ProfileID) == "" || len(input.ProfileID) > 1500 || !utf8.ValidString(input.SystemPrompt) || !utf8.ValidString(input.UserPrompt) ||
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
	if len(input.CacheVersion) > 64 || (input.MaxAttempts < 0 || input.MaxAttempts > maximumRunAttempts) || input.TimeoutMS < 0 || input.TimeoutMS > 60000 {
		return fmt.Errorf("%w: run controls", ErrInvalidRequest)
	}
	if encoded, err := json.Marshal(input.ProviderOptions); err != nil || len(encoded) > 32<<10 || containsSecretKey(input.ProviderOptions) {
		return fmt.Errorf("%w: provider options", ErrInvalidRequest)
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
		SchemaVersion: 1, CacheVersion: record.Version, OperationHash: record.OperationHash,
		Operation: record.Operation, RawProviderEnvelope: record.Envelope, ProviderResult: record.Result, CreatedAt: record.CreatedAt,
	}, true, nil
}

func (cache *ownerCacheStore) Set(ctx context.Context, operationHash string, record hardenllm.CacheRecord) error {
	var projection struct {
		Usage json.RawMessage `json:"usage"`
		Cost  json.RawMessage `json:"cost"`
	}
	if err := json.Unmarshal(record.ProviderResult, &projection); err != nil {
		return errors.New("gateway: cached provider result is invalid")
	}
	if len(projection.Usage) == 0 {
		projection.Usage = json.RawMessage(`{}`)
	}
	if len(projection.Cost) == 0 {
		projection.Cost = json.RawMessage(`{}`)
	}
	now := cache.clock().UTC()
	return cache.store.PutCache(ctx, postgres.CacheRecord{
		OwnerID: cache.ownerID, Version: record.CacheVersion, OperationHash: operationHash,
		Operation: record.Operation, Result: record.ProviderResult, Usage: projection.Usage, Cost: projection.Cost,
		Envelope: record.RawProviderEnvelope, CreatedAt: record.CreatedAt, UpdatedAt: now,
	})
}

func (cache *ownerCacheStore) Delete(ctx context.Context, operationHash string) error {
	return cache.store.DeleteCache(ctx, cache.ownerID, operationHash)
}

func runArtifacts(ownerID, runID, traceID string, references []hardenllm.ArtifactRef, now time.Time) ([]RunArtifact, []postgres.ArtifactRecord) {
	public := make([]RunArtifact, 0, len(references))
	records := make([]postgres.ArtifactRecord, 0, len(references))
	prefix := path.Join(
		"llm-traces", traces.SafeObjectKeyComponent(ownerID), traces.SafeObjectKeyComponent(runID), traces.SafeObjectKeyComponent(traceID),
	) + "/"
	for _, reference := range references {
		if !strings.HasPrefix(reference.Key, prefix) || path.Dir(reference.Key) != strings.TrimSuffix(prefix, "/") ||
			reference.ContentType != "application/json" || reference.SizeBytes < 1 || !strings.HasSuffix(reference.Key, ".json") {
			continue
		}
		digest, err := hex.DecodeString(reference.SHA256)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != reference.SHA256 {
			continue
		}
		filename := path.Base(reference.Key)
		artifactID := strings.TrimSuffix(filename, ".json")
		kind := ""
		switch {
		case strings.HasSuffix(artifactID, "-trace"):
			kind = traces.ArtifactKindTrace
		case strings.Contains(artifactID, "-raw"):
			kind = traces.ArtifactKindParseFailureResponse
		case strings.Contains(artifactID, "-diagnostic"):
			kind = "diagnostic-event"
		}
		if kind == "" || len(artifactID) > 128 {
			continue
		}
		public = append(public, RunArtifact{ArtifactID: artifactID, Kind: kind, SHA256: reference.SHA256, SizeBytes: reference.SizeBytes, ContentType: reference.ContentType})
		records = append(records, postgres.ArtifactRecord{
			OwnerID: ownerID, TraceID: traceID, ID: artifactID, Kind: kind, ObjectKey: reference.Key,
			ContentType: reference.ContentType, SHA256: reference.SHA256, SizeBytes: reference.SizeBytes,
			Available: true, CreatedAt: now, UpdatedAt: now,
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

func removeRunArtifact(artifacts []RunArtifact, artifactID string) []RunArtifact {
	for index := range artifacts {
		if artifacts[index].ArtifactID == artifactID {
			return append(artifacts[:index:index], artifacts[index+1:]...)
		}
	}
	return artifacts
}
