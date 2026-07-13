package hardenllm

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
	contractschema "github.com/prls-co/harden-llm/internal/schema"
)

var errRuntimeUnavailable = errors.New("hardenllm: runtime is not initialized")

// Client owns immutable dependencies for provider-neutral calls.
type Client struct {
	options       Options
	executor      coreruntime.Executor
	newID         func() string
	observeRecord func(coreruntime.CallRecord)
}

// New constructs a client without changing global logging or telemetry state.
func New(options Options) (*Client, error) {
	if options.Logger == nil {
		options.Logger = slog.New(slog.DiscardHandler)
	}
	return &Client{options: options, newID: newRuntimeID}, nil
}

// Call executes one provider-neutral LLM request.
func (client *Client) Call(ctx context.Context, request Request) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("hardenllm: context is required")
	}
	if client == nil || client.executor == nil {
		return Result{}, errRuntimeUnavailable
	}
	profiles, err := runtimeProfiles(request.Profiles)
	if err != nil {
		return Result{}, err
	}
	if request.ProfileID == "" {
		return Result{}, errors.New("hardenllm: profile ID is required")
	}
	if _, ok := profiles[request.ProfileID]; !ok {
		return Result{}, fmt.Errorf("hardenllm: profile %q was not found", request.ProfileID)
	}
	if client.options.Credentials == nil {
		return Result{}, errors.New("hardenllm: credential resolver is required")
	}

	cacheMode, err := cachekey.ResolveMode(string(request.CacheMode))
	if err != nil {
		return Result{}, fmt.Errorf("hardenllm: %w", err)
	}
	var runtimeCache coreruntime.Cache
	if client.options.Cache != nil {
		runtimeCache = &cacheAdapter{store: client.options.Cache}
	}
	call := coreruntime.Call{
		SystemPrompt: request.SystemPrompt, UserPrompt: request.UserPrompt, CallType: string(request.CallType),
		Schema: append([]byte(nil), request.Schema...), ReasoningEffort: string(request.ReasoningEffort),
		ProviderOptions: cloneAnyMap(request.ProviderOptions), Context: runtimeContext(request.Context),
		StructuredRepair: runtimeRepairPolicy(request.RetryPolicy.StructuredRepair),
	}
	if request.CallType == CallTypeStructured {
		if len(request.Schema) == 0 {
			return Result{}, errors.New("hardenllm: structured calls require a schema")
		}
		normalizedSchema, normalizeErr := contractschema.Normalize(request.Schema)
		if normalizeErr != nil {
			return Result{}, normalizeErr
		}
		call.Schema = normalizedSchema
		call.ValidateStructured = func(value any) error {
			return contractschema.ValidateValue(normalizedSchema, value)
		}
	}
	callID, traceID := client.newID(), client.newID()
	record, err := coreruntime.Execute(
		ctx,
		client.executor,
		func(ctx context.Context, profile coreruntime.Profile) (coreruntime.Credential, error) {
			credential, resolveErr := client.options.Credentials.ResolveCredential(ctx, CredentialRequest{
				Scope: profile.CredentialScope, BaseURL: profile.BaseURL, APIInferenceType: profile.APIInferenceType,
			})
			return coreruntime.Credential{APIKey: credential.APIKey, Headers: cloneStringMap(credential.Headers)}, resolveErr
		},
		request.ProfileID,
		profiles,
		call,
		retryConfig(request.RetryPolicy),
		runtimeCache,
		cacheMode,
		request.CacheVersion,
		callID,
		traceID,
	)
	if err != nil {
		return Result{}, err
	}
	if client.observeRecord != nil {
		client.observeRecord(record)
	}
	return resultFromRecord(record), nil
}

func runtimeRepairPolicy(policy StructuredRepairPolicy) coreruntime.StructuredRepair {
	result := coreruntime.StructuredRepair{Enabled: policy.Enabled}
	if policy.Escalation != nil {
		result.Escalation = &coreruntime.RepairEscalation{
			Attempt: policy.Escalation.Attempt, ModelID: policy.Escalation.ModelID,
			ReasoningEffort: string(policy.Escalation.ReasoningEffort),
		}
	}
	return result
}

func retryConfig(policy RetryPolicy) retry.Config {
	resolved := retry.DefaultPolicy()
	maxAttempts := policy.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = retry.DefaultMaxAttempts
	}
	applyBool := func(value *bool, target *bool) {
		if value != nil {
			*target = *value
		}
	}
	applyBool(policy.RetryNetwork, &resolved.Network)
	applyBool(policy.RetryRateLimit, &resolved.RateLimit)
	applyBool(policy.RetryServerError, &resolved.ServerError)
	applyBool(policy.RetryEmpty, &resolved.EmptyResponse)
	applyBool(policy.RetryParse, &resolved.ParseError)
	return retry.Config{
		MaxAttempts: maxAttempts,
		BaseDelay:   policy.InitialBackoff,
		MaxDelay:    policy.MaximumBackoff,
		Policy:      resolved,
	}
}

func runtimeProfiles(catalog ProfileCatalog) (map[string]coreruntime.Profile, error) {
	if len(catalog) == 0 {
		return nil, errors.New("hardenllm: profile catalog must contain at least one profile")
	}
	profiles := make(map[string]coreruntime.Profile, len(catalog))
	for key, profile := range catalog {
		if key == "" || profile.LLMProfile != key {
			return nil, fmt.Errorf("hardenllm: profile key %q must match llmProfile %q", key, profile.LLMProfile)
		}
		profiles[key] = coreruntime.Profile{
			ID: profile.LLMProfile, Provider: profile.Provider, APIInferenceType: profile.APIInferenceType,
			CredentialScope: profile.EndpointCredentialScope, BaseURL: profile.BaseURL, ModelID: profile.ModelID,
			DefaultOptions: cloneAnyMap(profile.DefaultOptions), ReasoningEffortMap: cloneNestedAnyMap(profile.ReasoningEffortMap),
			Backups: append([]string(nil), profile.BackupProfiles...), SupportsStructuredOutput: profile.SupportsContractedStructuredOutput,
		}
	}
	return profiles, nil
}

func runtimeContext(value ObservabilityContext) coreruntime.ObservabilityContext {
	return coreruntime.ObservabilityContext{
		TaskID: value.TaskID, TaskSlug: value.TaskSlug, ItemID: value.ItemID, RunID: value.RunID,
		OrganizationID: value.OrganizationID, QuerySetID: value.QuerySetID, Environment: value.Environment,
		Release: value.Release, PromptLabels: append([]string(nil), value.PromptLabels...),
		Tags: cloneStringMap(value.Tags), Metadata: cloneStringMap(value.Metadata),
	}
}

func resultFromRecord(record coreruntime.CallRecord) Result {
	attempts := make([]Attempt, 0, len(record.Attempts))
	for _, item := range record.Attempts {
		attempts = append(attempts, Attempt{
			Number: item.Number, ProfileID: item.ProfileID, Category: string(item.Category), HTTPStatus: item.Status,
			Retryable: item.Retryable, Wait: item.Delay, Repair: item.Repair, BackupIndex: item.BackupIndex, ProviderUsed: true,
		})
	}
	return Result{
		Output: record.Output, CallID: record.CallID, TraceID: record.TraceID,
		Usage: Usage{
			InputTokens: record.Usage.InputTokens, CacheReadTokens: record.Usage.CacheReadTokens,
			CacheCreationTokens: record.Usage.CacheCreationTokens, OutputTokens: record.Usage.OutputTokens,
			ReasoningTokens: record.Usage.ReasoningTokens, TotalTokens: record.Usage.TotalTokens,
		},
		Cost:     Cost{TotalUSD: record.Cost.TotalUSD, Known: record.Cost.Known, Source: record.Cost.Source},
		Attempts: attempts,
		Cache: CacheResult{
			Mode: CacheMode(record.Cache.Mode), Status: record.Cache.Status, OperationHash: record.Cache.OperationHash,
			Version: record.Cache.Version, Served: record.Cache.Served, Written: record.Cache.Written,
		},
	}
}

func newRuntimeID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(fmt.Sprintf("hardenllm: secure ID generation failed: %v", err))
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneNestedAnyMap(input map[string]map[string]any) map[string]map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]map[string]any, len(input))
	for key, value := range input {
		result[key] = cloneAnyMap(value)
	}
	return result
}
