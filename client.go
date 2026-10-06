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
	"unicode/utf8"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/providers"
	"github.com/prls-co/harden-llm/internal/retry"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
	contractschema "github.com/prls-co/harden-llm/internal/schema"
)

var errRuntimeUnavailable = errors.New("hardenllm: runtime is not initialized")

// Client owns immutable dependencies for provider-neutral calls.
type Client struct {
	options           Options
	connections       ConnectionCatalog
	defaultConnection string
	executor          coreruntime.Executor
	modelDiscovery    *providers.ModelDiscovery
	telemetry         *coreruntime.Telemetry
	newID             func() (string, error)
	observeRecord     func(coreruntime.CallRecord)
}

// New constructs a client without changing global logging or telemetry state.
func New(options Options) (*Client, error) {
	if options.Logger == nil {
		options.Logger = slog.New(slog.DiscardHandler)
	}
	connections, defaultConnection, err := normalizeConnections(options.Connections, options.DefaultConnection)
	if err != nil {
		return nil, err
	}
	endpointPolicy := providerEndpointPolicy(options.EndpointPolicy)
	executor, err := providers.NewRouter(providers.Config{
		EndpointPolicy: endpointPolicy,
		Logger:         options.Logger,
		JinaAPIKey:     options.WebSearch.JinaAPIKey, JinaTimeout: options.WebSearch.JinaTimeout,
		JinaMaxResponseBytes: options.WebSearch.JinaMaxResponseBytes,
	})
	if err != nil {
		return nil, err
	}
	modelDiscovery, err := providers.NewModelDiscovery(endpointPolicy)
	if err != nil {
		return nil, fmt.Errorf("hardenllm: initialize model discovery: %w", err)
	}
	telemetry, err := coreruntime.NewTelemetry(options.TracerProvider, options.MeterProvider)
	if err != nil {
		return nil, fmt.Errorf("hardenllm: initialize telemetry: %w", err)
	}
	return &Client{
		options: options, connections: connections, defaultConnection: defaultConnection,
		executor: executor, modelDiscovery: modelDiscovery, telemetry: telemetry, newID: newRuntimeID,
	}, nil
}

func providerEndpointPolicy(policy EndpointPolicy) providers.EndpointPolicy {
	return providers.EndpointPolicy{
		AllowedHosts: policy.AllowedHosts, PrivateAllowedHosts: policy.PrivateAllowedHosts,
		PrivateAllowlist: policy.PrivateAllowlist, Resolver: policy.Resolver,
		DialContext: policy.DialContext, TLSConfig: policy.TLSConfig,
		ConnectTimeout: policy.ConnectTimeout, TLSHandshakeTimeout: policy.TLSHandshakeTimeout,
		ResponseHeaderTimeout: policy.ResponseHeaderTimeout,
	}
}

// Models lists native model identifiers from one configured upstream. It does
// not affect inference readiness or cache identity.
func (client *Client) Models(ctx context.Context, connectionID string) ([]Model, error) {
	if ctx == nil {
		return nil, errors.New("hardenllm: context is required")
	}
	if client == nil || client.modelDiscovery == nil {
		return nil, errRuntimeUnavailable
	}
	connection, err := client.connection(connectionID)
	if err != nil {
		return nil, err
	}
	discovered, err := client.modelDiscovery.Discover(ctx, providers.ModelDiscoveryRequest{
		BaseURL: connection.BaseURL, APIInferenceType: connection.Protocol,
		APIKey: connection.APIKey, Headers: cloneStringMap(connection.Headers),
	})
	if err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(discovered))
	for _, model := range discovered {
		models = append(models, Model{ID: model.ID, Label: model.Label, OwnedBy: connection.Provider})
	}
	return models, nil
}

func (client *Client) connection(connectionID string) (Connection, error) {
	connectionID = strings.TrimSpace(connectionID)
	if connectionID == "" {
		connectionID = client.defaultConnection
	}
	connection, ok := client.connections[connectionID]
	if !ok {
		return Connection{}, fmt.Errorf("hardenllm: connection %q was not configured", connectionID)
	}
	return connection, nil
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
	if err := validateOrigin(request.Origin); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(request.ModelID) == "" || !utf8.ValidString(request.ModelID) || len(request.ModelID) > 512 {
		return Result{}, errors.New("hardenllm: model ID is required and must not exceed 512 UTF-8 bytes")
	}
	connection, err := client.connection(request.ConnectionID)
	if err != nil {
		return Result{}, err
	}
	if request.CallType != CallTypeText && request.CallType != CallTypeStructured {
		return Result{}, fmt.Errorf("hardenllm: call type must be %q or %q", CallTypeText, CallTypeStructured)
	}
	if err := validateTools(request.Tools, request.ToolChoice); err != nil {
		return Result{}, err
	}
	if request.CallType == CallTypeStructured && len(request.Tools) > 0 {
		return Result{}, errors.New("hardenllm: structured output and function tools cannot be combined")
	}
	if request.WebSearch && len(request.Tools) > 0 {
		return Result{}, errors.New("hardenllm: web search and function tools cannot be combined")
	}
	ctx, endCall := client.telemetry.StartCall(ctx, coreruntime.CallObservation{
		ConnectionID: connection.ID, Provider: connection.Provider, ModelID: request.ModelID, CallType: string(request.CallType),
	})
	var record coreruntime.CallRecord
	defer func() { endCall(record, err) }()
	cacheMode, err := cachekey.ResolveMode(string(request.CacheMode))
	if err != nil {
		return Result{}, fmt.Errorf("hardenllm: %w", err)
	}
	var runtimeCache coreruntime.Cache
	if client.options.Cache != nil {
		runtimeCache = &cacheAdapter{store: client.options.Cache}
	}
	if err := request.RecoveryPolicy.ValidateProviderOptions(request.ProviderOptions); err != nil {
		return Result{}, err
	}
	if err := request.RecoveryPolicy.Validate(); err != nil {
		return Result{}, err
	}
	call := coreruntime.Call{
		ModelID: request.ModelID, Messages: append([]coreruntime.Message(nil), request.Messages...), CallType: string(request.CallType),
		Schema: append([]byte(nil), request.Schema...), Tools: append([]coreruntime.FunctionTool(nil), request.Tools...),
		ToolChoice: request.ToolChoice, ReasoningEffort: string(request.ReasoningEffort),
		WebSearch: request.WebSearch,
		Origin: coreruntime.Origin{
			Client: request.Origin.Client, Component: request.Origin.Component, OperationID: request.Origin.OperationID,
			ParentRunID: request.Origin.ParentRunID, JobID: request.Origin.JobID, TestRunID: request.Origin.TestRunID,
			TestID: request.Origin.TestID, SourceRevision: request.Origin.SourceRevision,
		},
		ProviderOptions: cloneAnyMap(request.ProviderOptions), Context: runtimeContext(request.Context),
		Telemetry: client.telemetry,
	}
	if request.Progress != nil {
		call.Progress = func(snapshot coreruntime.ProgressSnapshot) {
			progressAttempts := make([]Attempt, 0, len(snapshot.Attempts))
			for _, item := range snapshot.Attempts {
				progressAttempts = append(progressAttempts, publicAttempt(item))
			}
			var progressAccounting *Accounting
			if snapshot.Accounting != nil {
				value := Accounting{
					Result: publicAccountingLedger(snapshot.Accounting.Result), Provider: publicAccountingLedger(snapshot.Accounting.Provider),
				}
				progressAccounting = &value
			}
			event := ProgressEvent{
				SchemaVersion: 1, Sequence: snapshot.Sequence, RunID: snapshot.RunID,
				CallID: snapshot.CallID, TraceID: snapshot.TraceID, Type: snapshot.Type,
				Stage: snapshot.Stage, Branch: snapshot.Branch, ConnectionID: snapshot.ConnectionID,
				ReasoningEffort: snapshot.ReasoningEffort, Attempt: snapshot.Attempt,
				AttemptsUsed: snapshot.AttemptsUsed, AttemptsRemaining: snapshot.AttemptsRemaining,
				ElapsedMs: snapshot.ElapsedMs, DeadlineRemainingMs: snapshot.DeadlineRemainingMs,
				ReceivedBytes: snapshot.ReceivedBytes, EventCount: snapshot.EventCount,
				OutputBytes: snapshot.OutputBytes, OutputCodePoints: snapshot.OutputCodePoints,
				LastActivity: snapshot.LastActivity.UTC().Format(time.RFC3339Nano),
				StopReason:   snapshot.StopReason, Terminal: snapshot.Terminal, MaxAttempts: snapshot.MaxAttempts,
				EffectiveTimeoutMs: durationMillisecondsPointer(snapshot.EffectiveTimeout), Origin: publicOrigin(snapshot.Origin),
				Attempts: progressAttempts, Accounting: progressAccounting,
			}
			select {
			case request.Progress <- event:
			default:
			}
		}
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
	runtimeConnection := runtimeConnection(connection)
	runtimeCredential := coreruntime.Credential{APIKey: connection.APIKey, Headers: cloneStringMap(connection.Headers)}
	record, err = coreruntime.Execute(
		ctx,
		client.executor,
		runtimeConnection,
		runtimeCredential,
		call,
		retry.Config{Policy: request.RecoveryPolicy},
		runtimeCache,
		cacheMode,
		request.CacheVersion,
		callID,
		traceID,
	)
	if client.observeRecord != nil {
		client.observeRecord(record)
	}
	result = resultFromRecord(record)
	if err != nil {
		return result, err
	}
	return result, nil
}

func validateOrigin(origin Origin) error {
	encoded, err := json.Marshal(origin)
	if err != nil || len(encoded) > 2<<10 {
		return errors.New("hardenllm: origin exceeds the 2 KiB limit")
	}
	for name, value := range map[string]string{
		"client": origin.Client, "component": origin.Component, "operationId": origin.OperationID,
		"parentRunId": origin.ParentRunID, "jobId": origin.JobID, "testRunId": origin.TestRunID,
		"testId": origin.TestID, "sourceRevision": origin.SourceRevision,
	} {
		if !utf8.ValidString(value) || len(value) > 256 {
			return fmt.Errorf("hardenllm: origin.%s exceeds the 256-byte limit", name)
		}
	}
	return nil
}

func runtimeConnection(connection Connection) coreruntime.Connection {
	return coreruntime.Connection{
		ID: connection.ID, Provider: connection.Provider, APIInferenceType: connection.Protocol,
		BaseURL: connection.BaseURL, CacheDomain: connection.CacheDomain, SupportsWebSearch: connection.SupportsWebSearch,
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
		attempts = append(attempts, publicAttempt(item))
	}
	return Result{
		Search: record.Search,
		Output: record.Output, CallID: record.CallID, TraceID: record.TraceID,
		Origin: Origin{Client: record.Origin.Client, Component: record.Origin.Component, OperationID: record.Origin.OperationID,
			ParentRunID: record.Origin.ParentRunID, JobID: record.Origin.JobID, TestRunID: record.Origin.TestRunID,
			TestID: record.Origin.TestID, SourceRevision: record.Origin.SourceRevision},
		SelectedTarget:   publicExecutionTarget(record.SelectedTarget),
		GenerationTarget: publicExecutionTarget(record.GenerationTarget),
		ResultSource: ResultSource{
			Kind: ResultSourceKind(record.ResultSource.Kind), AttemptNumber: record.ResultSource.AttemptNumber,
			Producer: publicExecutionTargetPointer(record.ResultSource.Producer),
		},
		Accounting: Accounting{
			Result:   publicAccountingLedger(record.Accounting.Result),
			Provider: publicAccountingLedger(record.Accounting.Provider),
		},
		Attempts: attempts,
		Cache: CacheResult{
			Mode: CacheMode(record.Cache.Mode), Status: record.Cache.Status, OperationHash: record.Cache.OperationHash,
			OriginalOperationHash: record.Cache.OriginalOperationHash, RerunOperationHash: record.Cache.RerunOperationHash,
			Version: record.Cache.Version, Served: record.Cache.Served, Written: record.Cache.Written,
		},
		Diagnostics: Diagnostics{
			Stage: record.Diagnostics.Stage, Branch: record.Diagnostics.Branch, StopReason: record.Diagnostics.StopReason,
			AttemptsUsed: record.Diagnostics.AttemptsUsed, AttemptsRemaining: record.Diagnostics.AttemptsRemaining,
			ElapsedMs: record.Diagnostics.Elapsed.Milliseconds(), TotalActualWaitMs: record.Diagnostics.TotalActualWait.Milliseconds(),
			ReceivedBytes: record.Diagnostics.ReceivedBytes, EventCount: record.Diagnostics.EventCount,
			OutputBytes: record.Diagnostics.OutputBytes, OutputCodePoints: record.Diagnostics.OutputCodePoints,
			EffectiveTimeoutMs: durationMillisecondsPointer(record.Diagnostics.EffectiveTimeout), DeadlineAt: record.Diagnostics.DeadlineAt,
			BranchCaches: publicBranchCaches(record.Diagnostics.BranchCaches),
		},
	}
}

func publicAttempt(item coreruntime.AttemptRecord) Attempt {
	var stream *StreamDiagnostics
	if item.Stream != nil {
		value := StreamDiagnostics{ReceivedBytes: item.Stream.ReceivedBytes, EventCount: item.Stream.EventCount, OutputBytes: item.Stream.OutputBytes, OutputCodePoints: item.Stream.OutputCodePoints, TerminalState: item.Stream.TerminalState,
			FirstEventMs: item.Stream.FirstEventMs, FirstOutputMs: item.Stream.FirstOutputMs, LastEventAt: item.Stream.LastEventAt, LastOutputAt: item.Stream.LastOutputAt}
		stream = &value
	}
	var waitDiagnostics *WaitDiagnostics
	if item.WaitDiagnostics != nil {
		value := WaitDiagnostics{Reason: item.WaitDiagnostics.Reason, PlannedMs: item.WaitDiagnostics.Planned.Milliseconds(), ActualMs: item.WaitDiagnostics.Actual.Milliseconds(), RetryAfterMs: item.WaitDiagnostics.RetryAfter.Milliseconds()}
		waitDiagnostics = &value
	}
	return Attempt{
		Number: item.Number, ConnectionID: item.ConnectionID, Target: publicExecutionTarget(item.Target),
		Category: string(item.Category), HTTPStatus: item.Status, Code: item.Code, Type: item.Type,
		ProviderRequestID: item.ProviderRequestID, Retryable: item.Retryable, Wait: item.Delay,
		Duration: item.Duration, Repair: item.Repair, ProviderUsed: item.ProviderUsed, Stage: item.Stage,
		Branch: item.Branch, TriggerAttempt: item.TriggerAttempt, InputAttempts: append([]int(nil), item.InputAttempts...),
		TransportRetryOf: item.TransportRetryOf, StartedAt: item.StartedAt, FinishedAt: item.FinishedAt,
		ReasoningEffort: item.ReasoningEffort, DispatchObserved: item.DispatchObserved, Stream: stream,
		WaitDiagnostics: waitDiagnostics,
	}
}

func publicOrigin(origin coreruntime.Origin) *Origin {
	if origin == (coreruntime.Origin{}) {
		return nil
	}
	value := Origin{Client: origin.Client, Component: origin.Component, OperationID: origin.OperationID,
		ParentRunID: origin.ParentRunID, JobID: origin.JobID, TestRunID: origin.TestRunID,
		TestID: origin.TestID, SourceRevision: origin.SourceRevision}
	return &value
}

func durationMillisecondsPointer(value *time.Duration) *int64 {
	if value == nil {
		return nil
	}
	milliseconds := value.Milliseconds()
	return &milliseconds
}

func publicBranchCaches(values []coreruntime.BranchCache) []BranchCache {
	if values == nil {
		return nil
	}
	result := make([]BranchCache, 0, len(values))
	for _, value := range values {
		result = append(result, BranchCache{Branch: value.Branch, GenerationTarget: publicExecutionTarget(value.GenerationTarget), Cache: CacheResult{
			Mode: CacheMode(value.Cache.Mode), Status: value.Cache.Status, OperationHash: value.Cache.OperationHash,
			OriginalOperationHash: value.Cache.OriginalOperationHash, RerunOperationHash: value.Cache.RerunOperationHash,
			Version: value.Cache.Version, Served: value.Cache.Served, Written: value.Cache.Written,
		}})
	}
	return result
}

func publicExecutionTargetPointer(target *coreruntime.ExecutionTarget) *ExecutionTarget {
	if target == nil {
		return nil
	}
	result := publicExecutionTarget(*target)
	return &result
}

func publicExecutionTarget(target coreruntime.ExecutionTarget) ExecutionTarget {
	return ExecutionTarget{
		ConnectionID: target.ConnectionID, Provider: target.Provider, Protocol: target.Protocol,
		Endpoint: target.Endpoint, ModelID: target.ModelID,
	}
}

func publicAccountingLedger(ledger coreruntime.Ledger) AccountingLedger {
	usage := ledger.Usage
	cost := ledger.Cost
	return AccountingLedger{
		Usage: Usage{
			InputTokens: usage.InputTokens, CacheReadTokens: usage.CacheReadTokens,
			CacheCreationTokens: usage.CacheCreationTokens, OutputTokens: usage.OutputTokens,
			ReasoningTokens: usage.ReasoningTokens, PromptTokens: usage.PromptTokens(),
			CompletionTokens: usage.CompletionTokens(), TotalTokens: usage.TotalTokens(), Status: string(usage.Status),
		},
		Cost: Cost{
			KnownSubtotalUSD: cost.KnownSubtotalUSD, Status: string(cost.Status), Source: cost.Source,
			KnownObservations: cost.KnownObservations, UnknownObservations: cost.UnknownObservations,
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
