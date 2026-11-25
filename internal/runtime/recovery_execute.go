package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
)

const (
	stageOriginalGenerate       = "original.generate"
	stageOriginalRepairInitial  = "original.repair.initial"
	stageOriginalRepairEscalate = "original.repair.escalation"
	stageRerunGenerate          = "rerun.generate"
	stageRerunRepairInitial     = "rerun.repair.initial"
	stageRerunRepairEscalate    = "rerun.repair.escalation"
)

type plannedWork struct {
	call              Call
	connection        Connection
	credential        Credential
	generationBinding ResolvedConnection
	target            ExecutionTarget
	prepared          PreparedOperation
	generationCall    Call
	branch            string
	stage             string
	lastAttempt       int
	history           []RepairHistoryEntry
	generationTarget  ExecutionTarget
	generationHash    string
}

func executeRecoveryPlan(
	ctx context.Context, executor Executor, connection Connection, credential Credential,
	call Call, config retry.Config,
	cache Cache, cacheMode cachekey.Mode, cacheVersion, callID, traceID string,
) (record CallRecord, err error) {
	baseCall := call
	selected := targetFromConnection(connection, call.ModelID)
	ctx, finishRuntime := startRuntime(ctx, call.Observer, selected, call.CallType)
	defer func() { finishRuntime(err) }()
	if call.WebSearch && call.SearchMemo == nil {
		// One memo belongs to the whole logical call. Repair calls disable search,
		// while a fresh rerun may reuse the generation's memoized fallback.
		baseCall.SearchMemo = &sync.Map{}
	}
	record = newCallRecord(callID, traceID, selected, call.Origin, cacheMode, cacheVersion, min(config.Policy.MaxAttempts, 10))
	if err := executionContextError(ctx, config.Now()); err != nil {
		record.StopReason = stopReasonFor(err)
		return record, err
	}
	if cacheMode != cachekey.ModeOff && cache == nil {
		return record, errors.New("runtime cache is required when cache mode is active")
	}
	startedAt := config.Now()
	record.startTiming(ctx, startedAt)
	var work *plannedWork
	progress := newProgressEmitter(ctx, baseCall, config, &record, startedAt)
	emit := func(eventType string, attempt int, terminal bool) {
		progress.emit(ProgressSnapshot{
			Type: eventType, Stage: work.stage, Branch: work.branch, ConnectionID: work.connection.ID,
			ReasoningEffort: work.call.ReasoningEffort, Attempt: attempt, Terminal: terminal,
		})
	}
	defer func() {
		record.finishDiagnostics(config, startedAt, err)
		if work != nil {
			record.Diagnostics.Stage = work.stage
			emit("run.terminal", work.lastAttempt, true)
		}
	}()

	work, err = preparePlannedWork(ctx, executor, connection, credential, baseCall, "original", stageOriginalGenerate, nil, nil)
	if err != nil {
		return record, err
	}
	work.generationTarget = work.target
	record.GenerationTarget = work.generationTarget
	if cacheMode != cachekey.ModeOff {
		if err := record.prepareBranchCache(work); err != nil {
			return record, err
		}
	}
	// A cache hit has the same start event and correlation IDs as a provider run.
	emit("run.started", 0, false)
	if cacheMode != cachekey.ModeOff {
		if served, err := record.lookupPlannedCache(ctx, cache, work, baseCall); served || err != nil {
			return record, err
		}
	}
	// A generation cache hit is authoritative and must remain replayable even
	// when a formerly used repair connection was removed. On a miss/refresh, admit
	// every enabled leaf before the first provider dispatch.
	if err := validateRecoveryTargets(config.Policy); err != nil {
		record.StopReason = "configuration_error"
		return record, err
	}

	providerAccumulator := accounting.NewProviderAccumulator()
	attempt := 0
	for {
		emit("run.progress", attempt, false)
		if err := executionContextError(ctx, config.Now()); err != nil {
			record.StopReason = stopReasonFor(err)
			return record, err
		}
		if attempt >= config.Policy.MaxAttempts {
			record.StopReason = "attempts_exhausted"
			return record, exhaustedRecoveryError(record.StopReason)
		}
		attempt++
		started := config.Now().UTC()
		prepared := work.prepared
		prepared.StreamProgress = progress.streamCallback(func() {
			emit("run.progress", attempt, false)
		})
		result, failure, observation := dispatchAttempt(ctx, executor, prepared, work.call, work.target, attempt)
		record.AssistantText = result.AssistantText
		progress.activeStream = nil
		finished := config.Now().UTC()
		if result.Search != nil {
			record.Search = result.Search
		}
		failure = record.accountAttempt(&providerAccumulator, work.connection, &result, failure)
		previousOutput := encodeRepairOutput(result.Output)
		if failure == nil {
			failure, _ = admitProviderResult(observation.context, work.connection, work.call, result)
			if failure != nil && previousOutput != "" {
				captureParseFailureResponse(&record, previousOutput)
			}
		}
		if failure != nil {
			captureProviderParseFailure(&record, failure, &previousOutput)
		}
		observation.finishProvider(result.ProviderDispatched, failure)
		classification := retry.Classify(failure, config.Policy)
		semanticParse := failure != nil && classification.Category == retry.CategoryParse
		nextSemantic := semanticParse && nextPlannedStage(config.Policy, *work) != ""
		transportRetry := !semanticParse && classification.Retryable
		if nextSemantic || transportRetry {
			classification.Retryable = true
		}
		inputAttempts := make([]int, 0, len(work.history))
		for _, entry := range work.history {
			inputAttempts = append(inputAttempts, entry.Attempt)
		}
		attemptRecord := AttemptRecord{
			Number: attempt, Target: work.target,
			Repair: work.call.Repair != nil, Stage: work.stage, Branch: work.branch,
			TriggerAttempt: triggerAttempt(work.history), InputAttempts: inputAttempts, TransportRetryOf: work.lastAttempt,
			ReasoningEffort: work.call.ReasoningEffort,
		}
		attemptRecord.setOutcome(result, classification, started, finished, max(0, config.Now().Sub(started)))
		record.Attempts = append(record.Attempts, attemptRecord)
		work.lastAttempt = attempt
		emit("run.progress", attempt, false)
		observation.finishAttempt(failure)
		if failure == nil {
			result = record.acceptProviderResult(result, attempt, work.target)
			completedBy := "generation"
			if work.call.Repair != nil {
				completedBy = "repair"
			}
			record.writeCachedResult(ctx, cache, call.Observer, result, work.generationHash, work.generationTarget, completedBy)
			return record, nil
		}
		if executionErr := executionContextError(ctx, config.Now()); executionErr != nil {
			record.StopReason = stopReasonFor(executionErr)
			return record, executionContextFailure(failure, executionErr)
		}
		if semanticParse {
			// A semantic transition is another provider operation and must never
			// consume an attempt after the one global budget is exhausted. Keep
			// the real validation error as the cause; StopReason carries the
			// machine-readable budget decision.
			if attempt >= config.Policy.MaxAttempts {
				record.StopReason = "attempts_exhausted"
				return record, failure
			}
			entry := RepairHistoryEntry{Stage: work.stage, Attempt: attempt, Output: previousOutput, ValidationError: boundedError(failure)}
			history := append(append([]RepairHistoryEntry(nil), work.history...), entry)
			next, nextErr := prepareNextPlannedWork(ctx, executor, connection, credential, baseCall, *work, history, config.Policy, attempt)
			if nextErr != nil {
				record.StopReason = "configuration_error"
				return record, nextErr
			}
			if next == nil {
				record.StopReason = "recovery_exhausted"
				return record, failure
			}
			work = next
			if work.branch != record.Branch {
				work.generationTarget = work.target
				record.Branch = work.branch
				record.GenerationTarget = work.generationTarget
				if cacheMode != cachekey.ModeOff {
					if err := record.prepareBranchCache(work); err != nil {
						return record, err
					}
					if served, err := record.lookupPlannedCache(ctx, cache, work, baseCall); served || err != nil {
						return record, err
					}
				}
			}
			continue
		}
		if !transportRetry {
			record.StopReason = stopReasonFor(failure)
			return record, failure
		}
		if attempt >= config.Policy.MaxAttempts {
			record.StopReason = "attempts_exhausted"
			return record, failure
		}
		if waitErr := record.waitBeforeRetry(ctx, work.call, classification, config); waitErr != nil {
			record.StopReason = stopReasonFor(waitErr)
			return record, waitErr
		}
	}
}

func prepareNextPlannedWork(ctx context.Context, executor Executor, selectedConnection Connection, credential Credential, baseCall Call, current plannedWork, history []RepairHistoryEntry, policy retry.Policy, attempt int) (*plannedWork, error) {
	next := nextPlannedStage(policy, current)
	if next == "" {
		return nil, nil
	}
	if next == stageRerunGenerate {
		target := policy.Rerun.Target
		call := baseCall
		call.Repair = nil
		call.WebSearch = baseCall.WebSearch
		return preparePlannedWork(ctx, executor, selectedConnection, credential, call, "rerun", next, &target, nil)
	}
	var target retry.RecoveryTarget
	if current.branch == "original" {
		if next == stageOriginalRepairInitial {
			target = policy.JSONRepair.Initial
		} else {
			target = *policy.JSONRepair.Escalation
		}
	} else {
		if next == stageRerunRepairInitial {
			target = policy.Rerun.JSONRepair.Initial
		} else {
			target = *policy.Rerun.JSONRepair.Escalation
		}
	}
	call := baseCall
	call.WebSearch = false
	if target.Source == "generation" {
		call = current.generationCall
		call.WebSearch = false
		selectedConnection = current.generationBinding.Connection
		credential = current.generationBinding.Credential
	}
	if baseCall.WebSearch {
		call.WebSearchTool = nil
		if len(call.Tools) == 0 && call.ToolChoice.Mode == "required" {
			call.ToolChoice = ToolChoice{}
		}
	}
	call.Repair = &RepairRequest{Attempt: attempt + 1, MaxAttempts: policy.MaxAttempts, Stage: next, Branch: current.branch, TargetSchema: append(json.RawMessage(nil), baseCall.Schema...), History: append([]RepairHistoryEntry(nil), history...)}
	nextWork, err := preparePlannedWork(ctx, executor, selectedConnection, credential, call, current.branch, next, &target, call.Repair)
	if err != nil {
		return nil, err
	}
	nextWork.generationHash = current.generationHash
	nextWork.generationTarget = current.generationTarget
	nextWork.generationCall = current.generationCall
	nextWork.generationBinding = current.generationBinding
	return nextWork, nil
}

func preparePlannedWork(ctx context.Context, executor Executor, connection Connection, credential Credential, baseCall Call, branch, stage string, target *retry.RecoveryTarget, repair *RepairRequest) (*plannedWork, error) {
	call := baseCall
	call.Repair = repair
	if target != nil {
		if target.Source == "model" {
			if target.ConnectionID != "" {
				binding, ok := baseCall.Connections[target.ConnectionID]
				if !ok {
					return nil, errors.New("runtime recovery upstream is not configured")
				}
				connection = binding.Connection
				credential = binding.Credential
			}
			call.ProviderOptions = cloneMap(target.ProviderOptions)
			if call.OptionProtocol == "" || target.ReasoningEffort != "" {
				call.ReasoningEffort = target.ReasoningEffort
			}
			if target.ModelID != "" {
				call.ModelID = target.ModelID
			}
		}
	}
	prepared, err := executor.Prepare(ctx, connection, credential, call)
	if err != nil {
		return nil, err
	}
	prepared = withCacheDomain(prepared, connection)
	var history []RepairHistoryEntry
	if repair != nil {
		history = append([]RepairHistoryEntry(nil), repair.History...)
	}
	generationCall := call
	generationCall.Repair = nil
	return &plannedWork{call: call, connection: connection, credential: credential, generationBinding: ResolvedConnection{Connection: connection, Credential: credential}, generationCall: generationCall, target: targetFromPrepared(connection, prepared), prepared: prepared, branch: branch, stage: stage, history: history}, nil
}

// validateRecoveryTargets admits every model override before the first provider
// dispatch. Every recovery target uses the one configured connection and its
// credential; only the requested model and native options may change.
func validateRecoveryTargets(policy retry.Policy) error {
	for path, target := range policy.Targets {
		if err := target.Validate("recoveryPolicy."+path, path != "rerun.target"); err != nil {
			return err
		}
	}
	return nil
}

func nextPlannedStage(policy retry.Policy, current plannedWork) string {
	if current.branch == "original" {
		switch current.stage {
		case stageOriginalGenerate:
			if policy.JSONRepair != nil {
				return stageOriginalRepairInitial
			}
			if policy.Rerun != nil {
				return stageRerunGenerate
			}
		case stageOriginalRepairInitial:
			if policy.JSONRepair != nil && policy.JSONRepair.Escalation != nil {
				return stageOriginalRepairEscalate
			}
			if policy.Rerun != nil {
				return stageRerunGenerate
			}
		case stageOriginalRepairEscalate:
			if policy.Rerun != nil {
				return stageRerunGenerate
			}
		}
		return ""
	}
	if policy.Rerun == nil || policy.Rerun.JSONRepair == nil {
		return ""
	}
	switch current.stage {
	case stageRerunGenerate:
		return stageRerunRepairInitial
	case stageRerunRepairInitial:
		if policy.Rerun.JSONRepair.Escalation != nil {
			return stageRerunRepairEscalate
		}
	}
	return ""
}

func triggerAttempt(history []RepairHistoryEntry) int {
	if len(history) == 0 {
		return 0
	}
	return history[0].Attempt
}

func encodeRepairOutput(output any) string {
	if output == nil {
		return ""
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func boundedError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.ToValidUTF8(err.Error(), "")
	if len(value) > 8<<10 {
		value = value[:8<<10]
	}
	return value
}

func exhaustedRecoveryError(reason string) error {
	return &retry.ProviderError{Err: errors.New("recovery attempt budget exhausted: " + reason), Code: "RECOVERY_ATTEMPTS_EXHAUSTED", Category: retry.CategoryOther}
}

func stopReasonFor(err error) string {
	switch {
	case err == nil:
		return "succeeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		var providerError *retry.ProviderError
		if errors.As(err, &providerError) {
			switch providerError.Code {
			case "REPAIR_INPUT_LIMIT":
				return "repair_input_limit_exceeded"
			case "RESPONSE_TOO_LARGE":
				return "response_limit_exceeded"
			}
		}
		return "terminal_error"
	}
}

func (record *CallRecord) prepareBranchCache(work *plannedWork) error {
	hash, err := cachekey.Hash(work.prepared.Operation, record.Cache.Version)
	if err != nil {
		return err
	}
	work.generationHash = hash
	record.Cache.OperationHash = hash
	if work.branch == "original" {
		record.Cache.OriginalOperationHash = hash
	} else {
		record.Cache.RerunOperationHash = hash
	}
	return nil
}

func (record *CallRecord) lookupPlannedCache(ctx context.Context, cache Cache, work *plannedWork, call Call) (bool, error) {
	if record.Cache.Mode == cachekey.ModeCache {
		cached, found, err := cache.Get(ctx, work.generationHash, record.Cache.Version)
		if err != nil {
			return false, err
		}
		if found {
			if err := admitPlannedCachedResult(ctx, work.connection, work.generationTarget, cached, call); err != nil {
				return false, NewCacheIntegrityError()
			}
			record.serveCached(cached)
			record.rememberBranchCache(work.branch, work.generationTarget)
			record.StopReason = "succeeded"
			return true, nil
		}
		record.Cache.Status = "miss"
	} else {
		record.Cache.Status = "refresh"
	}
	record.rememberBranchCache(work.branch, work.generationTarget)
	return false, nil
}

func admitPlannedCachedResult(ctx context.Context, connection Connection, generationTarget ExecutionTarget, cached CachedResult, call Call) error {
	if cached.GenerationTarget.ConnectionID == "" {
		// Historical v2 projections did not carry generation identity; retain
		// their strict producer equality check until the projection is rewritten.
		if err := validateCacheProducer(cached.Producer, generationTarget); err != nil {
			return err
		}
	} else {
		switch cached.CompletedBy {
		case "generation":
			if err := validateCacheProducer(cached.GenerationTarget, generationTarget); err != nil {
				return err
			}
			if err := validateCacheProducer(cached.Producer, cached.GenerationTarget); err != nil {
				return err
			}
		case "repair":
			if err := validateTargetSnapshot(cached.GenerationTarget); err != nil {
				return err
			}
			if err := validateTargetSnapshot(cached.Producer); err != nil {
				return err
			}
		default:
			return NewCacheIntegrityError()
		}
	}
	return admitResult(cached.ProviderResult, call.CallType, func(value any) error {
		return validateStructuredValue(ctx, call.Observer, connection, false, call.ValidateStructured, value)
	})
}

func validateTargetSnapshot(target ExecutionTarget) error {
	if strings.TrimSpace(target.ConnectionID) == "" || strings.TrimSpace(target.Provider) == "" || strings.TrimSpace(target.Protocol) == "" || strings.TrimSpace(target.Endpoint) == "" || strings.TrimSpace(target.ModelID) == "" {
		return NewCacheIntegrityError()
	}
	return nil
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
