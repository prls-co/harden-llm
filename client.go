package hardenllm

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/cachekey"
	contractprofiles "github.com/prls-co/harden-llm/internal/profiles"
	"github.com/prls-co/harden-llm/internal/providers"
	"github.com/prls-co/harden-llm/internal/retry"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
	contractschema "github.com/prls-co/harden-llm/internal/schema"
)

var errRuntimeUnavailable = errors.New("hardenllm: runtime is not initialized")

// Client owns immutable dependencies for provider-neutral calls.
type Client struct {
	options       Options
	executor      coreruntime.Executor
	telemetry     *coreruntime.Telemetry
	newID         func() (string, error)
	observeRecord func(coreruntime.CallRecord)
}

// New constructs a client without changing global logging or telemetry state.
func New(options Options) (*Client, error) {
	if options.Logger == nil {
		options.Logger = slog.New(slog.DiscardHandler)
	}
	executor, err := providers.NewRouter(providers.Config{
		EndpointPolicy: providers.EndpointPolicy{
			AllowedHosts: options.EndpointPolicy.AllowedHosts, PrivateAllowedHosts: options.EndpointPolicy.PrivateAllowedHosts,
			PrivateAllowlist: options.EndpointPolicy.PrivateAllowlist, Resolver: options.EndpointPolicy.Resolver,
			DialContext: options.EndpointPolicy.DialContext, TLSConfig: options.EndpointPolicy.TLSConfig,
			ConnectTimeout: options.EndpointPolicy.ConnectTimeout, TLSHandshakeTimeout: options.EndpointPolicy.TLSHandshakeTimeout,
			ResponseHeaderTimeout: options.EndpointPolicy.ResponseHeaderTimeout,
		},
		Logger: options.Logger,
	})
	if err != nil {
		return nil, err
	}
	telemetry, err := coreruntime.NewTelemetry(options.TracerProvider, options.MeterProvider)
	if err != nil {
		return nil, fmt.Errorf("hardenllm: initialize telemetry: %w", err)
	}
	return &Client{options: options, executor: executor, telemetry: telemetry, newID: newRuntimeID}, nil
}

// Call executes one provider-neutral LLM request. When provider execution
// fails, the returned Result retains generated IDs and available diagnostics;
// callers must still treat a non-nil error as a failed call.
func (client *Client) Call(ctx context.Context, request Request) (result Result, err error) {
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
	if request.CallType != CallTypeText && request.CallType != CallTypeStructured {
		return Result{}, fmt.Errorf("hardenllm: call type must be %q or %q", CallTypeText, CallTypeStructured)
	}
	profile := profiles[request.ProfileID]
	ctx, endCall := client.telemetry.StartCall(ctx, coreruntime.CallObservation{
		ProfileID: profile.ID, Provider: profile.Provider, ModelID: profile.ModelID, CallType: string(request.CallType),
	})
	var record coreruntime.CallRecord
	defer func() { endCall(record, err) }()
	startedAt := time.Now().UTC()

	cacheMode, err := cachekey.ResolveMode(string(request.CacheMode))
	if err != nil {
		return Result{}, fmt.Errorf("hardenllm: %w", err)
	}
	var runtimeCache coreruntime.Cache
	if client.options.Cache != nil {
		runtimeCache = &cacheAdapter{store: client.options.Cache}
	}
	resolvedRetry := retryConfig(request.RetryPolicy, request.CallType == CallTypeStructured)
	repairPolicy, err := runtimeRepairPolicy(
		request.RetryPolicy.StructuredRepair, resolvedRetry.MaxAttempts, request.CallType == CallTypeStructured,
	)
	if err != nil {
		return Result{}, err
	}
	call := coreruntime.Call{
		SystemPrompt: request.SystemPrompt, UserPrompt: request.UserPrompt, CallType: string(request.CallType),
		Schema: append([]byte(nil), request.Schema...), ReasoningEffort: string(request.ReasoningEffort),
		ProviderOptions: cloneAnyMap(request.ProviderOptions), Context: runtimeContext(request.Context),
		StructuredRepair: repairPolicy,
		Telemetry:        client.telemetry,
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
	callID, err := client.newID()
	if err != nil {
		return Result{}, fmt.Errorf("hardenllm: generate call ID: %w", err)
	}
	traceID, err := client.newID()
	if err != nil {
		return Result{}, fmt.Errorf("hardenllm: generate trace ID: %w", err)
	}
	callSecrets := make([]string, 0, 6)
	if request.SystemPrompt != "" {
		callSecrets = append(callSecrets, request.SystemPrompt)
	}
	if request.UserPrompt != "" {
		callSecrets = append(callSecrets, request.UserPrompt)
	}
	record, err = coreruntime.Execute(
		ctx,
		client.executor,
		func(ctx context.Context, profile coreruntime.Profile) (coreruntime.Credential, error) {
			credential, resolveErr := client.options.Credentials.ResolveCredential(ctx, CredentialRequest{
				Scope: profile.CredentialScope, OwnerID: request.Context.OrganizationID,
				BaseURL: profile.BaseURL, APIInferenceType: profile.APIInferenceType,
			})
			if credential.APIKey != "" {
				callSecrets = append(callSecrets, credential.APIKey)
			}
			for _, value := range credential.Headers {
				if value != "" {
					callSecrets = append(callSecrets, value)
				}
			}
			return coreruntime.Credential{APIKey: credential.APIKey, Headers: cloneStringMap(credential.Headers)}, resolveErr
		},
		request.ProfileID,
		profiles,
		call,
		resolvedRetry,
		runtimeCache,
		cacheMode,
		request.CacheVersion,
		callID,
		traceID,
	)
	completedAt := time.Now().UTC()
	artifactRefs := client.persistCallArtifacts(ctx, record, call.Context, startedAt, completedAt, err, callSecrets)
	if client.observeRecord != nil {
		client.observeRecord(record)
	}
	result = resultFromRecord(record)
	result.Artifacts = artifactRefs
	if err != nil {
		return result, err
	}
	return result, nil
}

func runtimeRepairPolicy(policy StructuredRepairPolicy, maxAttempts int, structured bool) (coreruntime.StructuredRepair, error) {
	result := coreruntime.StructuredRepair{Enabled: policy.Enabled}
	if !policy.Enabled {
		if policy.Escalation != nil {
			return coreruntime.StructuredRepair{}, errors.New("hardenllm: structured repair escalation requires repair to be enabled")
		}
		return result, nil
	}
	if !structured {
		return coreruntime.StructuredRepair{}, errors.New("hardenllm: structured repair is supported only for structured calls")
	}
	if policy.Escalation == nil {
		return result, nil
	}
	escalation := policy.Escalation
	modelID := strings.TrimSpace(escalation.ModelID)
	effort := ReasoningEffort(strings.TrimSpace(string(escalation.ReasoningEffort)))
	if escalation.Attempt < 2 || escalation.Attempt > maxAttempts {
		return coreruntime.StructuredRepair{}, errors.New("hardenllm: structured repair escalation attempt must be from 2 through maxAttempts")
	}
	if modelID == "" {
		return coreruntime.StructuredRepair{}, errors.New("hardenllm: structured repair escalation model ID is required")
	}
	if effort != "" && effort != ReasoningEffortLowest && effort != ReasoningEffortMiddle && effort != ReasoningEffortHighest {
		return coreruntime.StructuredRepair{}, errors.New("hardenllm: structured repair escalation reasoning effort must be lowest, middle, or highest")
	}
	result.Escalation = &coreruntime.RepairEscalation{
		Attempt: escalation.Attempt, ModelID: modelID, ReasoningEffort: string(effort),
	}
	return result, nil
}

func retryConfig(policy RetryPolicy, structured bool) retry.Config {
	resolved := retry.DefaultPolicy()
	resolved.ParseError = structured
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
	validated := make(contractprofiles.Catalog, len(catalog))
	for key, profile := range catalog {
		supportsTemperature := profile.SupportsTemperature
		tokensParam := profile.TokensParam
		responsesTokensParam := profile.ResponsesTokensParam
		var pricing *contractprofiles.Pricing
		if profile.Pricing != nil {
			pricing = &contractprofiles.Pricing{
				Input: profile.Pricing.Input, CacheRead: profile.Pricing.CacheRead,
				CacheCreation: profile.Pricing.CacheCreation, Output: profile.Pricing.Output,
				Reasoning: profile.Pricing.Reasoning,
			}
		}
		validated[key] = contractprofiles.Profile{
			SchemaVersion: profile.SchemaVersion, LLMProfile: profile.LLMProfile, Provider: profile.Provider,
			APIInferenceType: profile.APIInferenceType, EndpointCredentialScope: profile.EndpointCredentialScope,
			BaseURL: profile.BaseURL, ModelID: profile.ModelID, Pricing: pricing,
			SupportsTemperature:                &supportsTemperature,
			SupportsContractedStructuredOutput: profile.SupportsContractedStructuredOutput,
			TokensParam:                        &tokensParam, ResponsesTokensParam: &responsesTokensParam,
			DefaultOptions:     cloneAnyMap(profile.DefaultOptions),
			ReasoningEffortMap: cloneNestedAnyMap(profile.ReasoningEffortMap),
			BackupProfiles:     append([]string(nil), profile.BackupProfiles...),
		}
	}
	encodedCatalog, err := json.Marshal(validated)
	if err != nil {
		return nil, fmt.Errorf("hardenllm: encode profile catalog: %w", err)
	}
	validated, err = contractprofiles.ParseCatalog(encodedCatalog)
	if err != nil {
		return nil, fmt.Errorf("hardenllm: %w", err)
	}
	profiles := make(map[string]coreruntime.Profile, len(catalog))
	for key, profile := range validated {
		profiles[key] = coreruntime.Profile{
			ID: profile.LLMProfile, Provider: profile.Provider, APIInferenceType: profile.APIInferenceType,
			CredentialScope: profile.EndpointCredentialScope, BaseURL: profile.BaseURL, ModelID: profile.ModelID,
			DefaultOptions: cloneAnyMap(profile.DefaultOptions), ReasoningEffortMap: cloneNestedAnyMap(profile.ReasoningEffortMap),
			Backups: append([]string(nil), profile.BackupProfiles...), SupportsStructuredOutput: profile.SupportsContractedStructuredOutput,
			SupportsTemperature: *profile.SupportsTemperature, TokensParam: *profile.TokensParam,
			ResponsesTokensParam: *profile.ResponsesTokensParam, Pricing: runtimeContractPricing(profile.Pricing),
		}
	}
	return profiles, nil
}

func runtimeContractPricing(pricing *contractprofiles.Pricing) coreruntime.Pricing {
	if pricing == nil {
		return coreruntime.Pricing{}
	}
	return coreruntime.Pricing{
		Input: pricing.Input, CacheRead: pricing.CacheRead, CacheCreation: pricing.CacheCreation,
		Output: pricing.Output, Reasoning: pricing.Reasoning,
	}
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
			Code: item.Code, Type: item.Type, ProviderRequestID: item.ProviderRequestID,
			Retryable: item.Retryable, Wait: item.Delay, Duration: item.Duration,
			Repair: item.Repair, BackupIndex: item.BackupIndex, ProviderUsed: true,
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

func newRuntimeID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("secure random source: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
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
