package hardenllm

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
	contractschema "github.com/prls-co/harden-llm/internal/schema"
	"github.com/prls-co/harden-llm/internal/traces"
)

var errRuntimeUnavailable = errors.New("hardenllm: runtime is not initialized")

// Client owns immutable dependencies for provider-neutral calls.
type Client struct {
	options           Options
	connections       ConnectionCatalog
	defaultConnection string
	executor          coreruntime.Executor
	modelDiscovery    ModelDiscoverer
	observer          Observer
	newID             func() (string, error)
	observeRecord     func(coreruntime.CallRecord)
}

// New constructs a client without changing global logging or telemetry state.
func New(options Options) (*Client, error) {
	if options.Executor == nil {
		return nil, errors.New("hardenllm: an injected executor is required")
	}
	connections, defaultConnection, err := normalizeConnections(options.Connections, options.DefaultConnection)
	if err != nil {
		return nil, err
	}
	return &Client{options: options, connections: connections, defaultConnection: defaultConnection,
		executor: options.Executor, modelDiscovery: options.DiscoverModels, observer: options.Observer, newID: newRuntimeID}, nil
}

// Models lists native model identifiers from one configured upstream. It does
// not affect inference readiness or cache identity.
func (client *Client) Models(ctx context.Context, connectionID string) ([]Model, error) {
	if ctx == nil {
		return nil, errors.New("hardenllm: context is required")
	}
	if client == nil {
		return nil, errRuntimeUnavailable
	}
	if client.modelDiscovery == nil {
		return nil, &UnsupportedOperationError{Operation: "models"}
	}
	connection, err := client.connection(connectionID)
	if err != nil {
		return nil, err
	}
	return client.modelDiscovery(ctx, connection)
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
	connection = connection.Clone()
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
	request.RecoveryPolicy = cloneRecoveryPolicy(request.RecoveryPolicy)
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
	started := time.Now()
	var record coreruntime.CallRecord
	if client.observer != nil {
		var finish func(coreruntime.CallSummary, error)
		ctx, finish = client.observer.StartCall(ctx, coreruntime.CallObservation{ConnectionID: connection.ID, Provider: connection.Provider, ModelID: request.ModelID, CallType: string(request.CallType)})
		defer func() { finish(coreruntime.Summarize(record, started, time.Now(), err), err) }()
	}
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
	bindings := make(map[string]coreruntime.ResolvedConnection, len(client.connections))
	for id, value := range client.connections {
		bindings[id] = coreruntime.ResolvedConnection{Connection: runtimeConnection(value), Credential: coreruntime.Credential{APIKey: value.APIKey, Headers: cloneStringMap(value.Headers)}}
	}
	if err := validateRecoveryConnections(request.RecoveryPolicy, bindings); err != nil {
		return Result{}, err
	}
	call := coreruntime.Call{
		Connections: bindings,
		ModelID:     request.ModelID, Messages: cloneMessages(request.Messages), CallType: string(request.CallType),
		Schema: append([]byte(nil), request.Schema...), Tools: cloneTools(request.Tools),
		ToolChoice: request.ToolChoice, ReasoningEffort: string(request.ReasoningEffort),
		WebSearch: request.WebSearch,
		Origin: coreruntime.Origin{
			Client: request.Origin.Client, Component: request.Origin.Component, OperationID: request.Origin.OperationID,
			ParentRunID: request.Origin.ParentRunID, JobID: request.Origin.JobID, TestRunID: request.Origin.TestRunID,
			TestID: request.Origin.TestID, SourceRevision: request.Origin.SourceRevision,
		},
		ProviderOptions: cloneAnyMap(request.ProviderOptions), Context: runtimeContext(request.Context),
		Observer: client.observer,
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
					Result: ProjectLedger(snapshot.Accounting.Result), Provider: ProjectLedger(snapshot.Accounting.Provider),
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
	if request.CaptureDiagnostics != nil {
		projected := traces.Project(record, call.Context, started, time.Now(), err)
		secrets := []string{}
		for _, binding := range bindings {
			secrets = append(secrets, binding.Credential.APIKey)
			for _, value := range binding.Credential.Headers {
				secrets = append(secrets, value)
			}
		}
		artifacts, captureErr := traces.ArtifactProjections(projected, record.ParseFailureResponse, secrets...)
		if captureErr == nil && len(artifacts) > 0 {
			// The trace value and bytes use the same redacted projection.
			var detached Trace
			captureErr = json.Unmarshal(artifacts[0].Content, &detached)
			projected = detached
		}
		if captureErr == nil {
			request.CaptureDiagnostics(DiagnosticCapture{Trace: projected, Artifacts: artifacts})
		} else {
			request.CaptureDiagnostics(DiagnosticCapture{ErrorCode: "diagnostic_projection_failed"})
		}
	}
	result = resultFromRecord(record)
	if err != nil {
		return result, err
	}
	return result, nil
}

func cloneRecoveryPolicy(policy RecoveryPolicy) RecoveryPolicy {
	policy.RetryOn = slices.Clone(policy.RetryOn)
	policy.JSONRepair = cloneRepairPlan(policy.JSONRepair)
	if policy.Rerun != nil {
		rerun := *policy.Rerun
		rerun.Target.ProviderOptions = cloneAnyMap(rerun.Target.ProviderOptions)
		rerun.JSONRepair = cloneRepairPlan(rerun.JSONRepair)
		policy.Rerun = &rerun
	}
	return policy
}

func cloneRepairPlan(plan *JSONRepairPlan) *JSONRepairPlan {
	if plan == nil {
		return nil
	}
	copy := *plan
	copy.Initial.ProviderOptions = cloneAnyMap(plan.Initial.ProviderOptions)
	if plan.Escalation != nil {
		escalation := *plan.Escalation
		escalation.ProviderOptions = cloneAnyMap(escalation.ProviderOptions)
		copy.Escalation = &escalation
	}
	return &copy
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
		BaseURL: connection.BaseURL, CacheDomain: connection.CacheDomain, SupportsWebSearch: connection.SupportsWebSearch, Pricing: clonePricing(connection.Pricing),
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
			Result:   ProjectLedger(record.Accounting.Result),
			Provider: ProjectLedger(record.Accounting.Provider),
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
			CorrelationID: item.Stream.CorrelationID, HeadersMs: clonePointer(item.Stream.HeadersMs), FirstEventMs: clonePointer(item.Stream.FirstEventMs), FirstOutputMs: clonePointer(item.Stream.FirstOutputMs), LastEventAt: clonePointer(item.Stream.LastEventAt), LastOutputAt: clonePointer(item.Stream.LastOutputAt)}
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
		TransportRetryOf: item.TransportRetryOf, StartedAt: clonePointer(item.StartedAt), FinishedAt: clonePointer(item.FinishedAt),
		ReasoningEffort: item.ReasoningEffort, DispatchObserved: item.DispatchObserved, Stream: stream,
		WaitDiagnostics: waitDiagnostics,
	}
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	detached := *value
	return &detached
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

// ProjectLedger renders the core ledger with its derived wire token totals.
func ProjectLedger(ledger ProviderLedger) AccountingLedger {
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
		result[key] = cloneJSONValue(value)
	}
	return result
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMap(typed)
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = cloneJSONValue(item)
		}
		return result
	case json.RawMessage:
		return append(json.RawMessage(nil), typed...)
	case []byte:
		return append([]byte(nil), typed...)
	default:
		return value
	}
}

func cloneMessages(input []Message) []Message {
	result := append([]Message(nil), input...)
	for i := range result {
		result[i].Content = append(json.RawMessage(nil), input[i].Content...)
		result[i].ToolCalls = append([]ToolCall(nil), input[i].ToolCalls...)
	}
	return result
}

func cloneTools(input []FunctionTool) []FunctionTool {
	result := append([]FunctionTool(nil), input...)
	for i := range result {
		result[i].Parameters = append(json.RawMessage(nil), input[i].Parameters...)
		if input[i].Strict != nil {
			v := *input[i].Strict
			result[i].Strict = &v
		}
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

func validateRecoveryConnections(policy RecoveryPolicy, bindings map[string]coreruntime.ResolvedConnection) error {
	check := func(target RecoveryTarget) error {
		if target.ConnectionID != "" {
			if _, ok := bindings[target.ConnectionID]; !ok {
				return fmt.Errorf("hardenllm: recovery upstream %q is not configured", target.ConnectionID)
			}
		}
		return nil
	}
	if policy.JSONRepair != nil {
		if err := check(policy.JSONRepair.Initial); err != nil {
			return err
		}
		if policy.JSONRepair.Escalation != nil {
			if err := check(*policy.JSONRepair.Escalation); err != nil {
				return err
			}
		}
	}
	if policy.Rerun != nil {
		if err := check(policy.Rerun.Target); err != nil {
			return err
		}
		if policy.Rerun.JSONRepair != nil {
			if err := check(policy.Rerun.JSONRepair.Initial); err != nil {
				return err
			}
			if policy.Rerun.JSONRepair.Escalation != nil {
				if err := check(*policy.Rerun.JSONRepair.Escalation); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
