package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
)

func Execute(
	ctx context.Context, executor Executor, connection Connection, credential Credential,
	call Call, config retry.Config,
	cache Cache, cacheMode cachekey.Mode, cacheVersion, callID, traceID string,
) (record CallRecord, err error) {
	if ctx == nil {
		return record, errors.New("runtime context is required")
	}
	if executor == nil {
		return record, errors.New("runtime executor is required")
	}
	if err := config.Policy.Validate(); err != nil {
		return record, err
	}
	if connection.ID == "" {
		return record, errors.New("runtime connection ID is required")
	}
	if call.CallType == "structured" && call.ValidateStructured == nil {
		return record, errors.New("structured call validator is required")
	}
	if config.Random == nil {
		config.Random = rand.Float64
	}
	if config.Wait == nil {
		config.Wait = retry.Wait
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if cacheVersion == "" {
		cacheVersion = cachekey.DefaultVersion
	}
	if call.CallType == "structured" && config.Policy.UsesExplicitPlan() {
		return executeRecoveryPlan(ctx, executor, connection, credential, call, config, cache, cacheMode, cacheVersion, callID, traceID)
	}
	if call.WebSearch {
		call.SearchMemo = &sync.Map{}
	}
	selected := targetFromConnection(connection, call.ModelID)
	ctx, finishRuntime := startRuntime(ctx, call.Observer, selected, call.CallType)
	defer func() { finishRuntime(err) }()
	record = newCallRecord(callID, traceID, selected, call.Origin, cacheMode, cacheVersion, 0)
	startedAt := config.Now()
	record.startTiming(ctx, startedAt)
	progress := newProgressEmitter(ctx, call, config, &record, startedAt)
	emitProgress := func(eventType string, attempt int, terminal bool) {
		progress.emit(ProgressSnapshot{
			Type: eventType, Stage: stageOriginalGenerate, Branch: "original", ConnectionID: connection.ID,
			ReasoningEffort: call.ReasoningEffort, Attempt: attempt, Terminal: terminal,
		})
	}
	defer func() {
		record.finishDiagnostics(config, startedAt, err)
		emitProgress("run.terminal", len(record.Attempts), true)
	}()
	emitProgress("run.started", 0, false)
	if err := executionContextError(ctx, config.Now()); err != nil {
		return record, err
	}
	prepared, err := executor.Prepare(ctx, connection, credential, call)
	if err != nil {
		return record, err
	}
	prepared = withCacheDomain(prepared, connection)
	record.PreparedOperation = prepared
	target := targetFromPrepared(connection, prepared)
	if cacheMode != cachekey.ModeOff {
		if cache == nil {
			return record, errors.New("runtime cache is required when cache mode is active")
		}
		record.Cache.OperationHash, err = cachekey.Hash(prepared.Operation, cacheVersion)
		if err != nil {
			return record, err
		}
		if cacheMode == cachekey.ModeCache {
			cacheContext, endCache := startCache(ctx, call.Observer, "lookup")
			cached, found, cacheErr := cache.Get(cacheContext, record.Cache.OperationHash, cacheVersion)
			if cacheErr != nil {
				endCache("unknown", cacheErr)
				return record, cacheErr
			}
			if found {
				if admissionErr := admitCachedResult(cacheContext, connection, target, cached, call); admissionErr != nil {
					endCache("unknown", admissionErr)
					return record, NewCacheIntegrityError()
				}
				endCache("hit", nil)
				record.serveCached(cached)
				record.rememberBranchCache("original", target)
				return record, nil
			}
			endCache("miss", nil)
			record.Cache.Status = "miss"
		} else {
			record.Cache.Status = "refresh"
		}
		record.rememberBranchCache("original", target)
	}

	providerAccumulator := accounting.NewProviderAccumulator()
	for number := 1; ; number++ {
		if err := executionContextError(ctx, config.Now()); err != nil {
			return record, err
		}
		started := config.Now()
		preparedAttempt := prepared
		preparedAttempt.StreamProgress = progress.streamCallback(func() {
			emitProgress("run.progress", number, false)
		})
		result, failure, observation := dispatchAttempt(ctx, executor, preparedAttempt, call, target, number)
		record.AssistantText = result.AssistantText
		progress.activeStream = nil
		finished := config.Now().UTC()
		failure = record.accountAttempt(&providerAccumulator, connection, &result, failure)
		previousOutput := ""
		if failure == nil {
			var schemaRejected bool
			failure, schemaRejected = admitProviderResult(observation.context, connection, call, result)
			if schemaRejected {
				encoded, marshalErr := json.Marshal(result.Output)
				if marshalErr != nil {
					failure = marshalErr
				} else {
					previousOutput = string(encoded)
					captureParseFailureResponse(&record, previousOutput)
				}
			}
		}
		observation.finishProvider(result.ProviderDispatched, failure)
		if failure != nil {
			captureProviderParseFailure(&record, failure, &previousOutput)
		}
		if contextErr := executionContextError(ctx, config.Now()); contextErr != nil {
			failure = executionContextFailure(failure, contextErr)
		}
		classification := retry.Classify(failure, config.Policy)
		attemptRecord := AttemptRecord{
			Number: number, Target: target, Repair: call.Repair != nil, Stage: stageOriginalGenerate,
			Branch: "original", ReasoningEffort: call.ReasoningEffort,
		}
		attemptRecord.setOutcome(result, classification, started, finished, max(0, config.Now().Sub(started)))
		record.Attempts = append(record.Attempts, attemptRecord)
		record.Diagnostics.Stage = attemptRecord.Stage
		emitProgress("run.progress", number, false)
		observation.finishAttempt(failure)
		if failure == nil {
			result = record.acceptProviderResult(result, number, target)
			record.writeCachedResult(ctx, cache, call.Observer, result, record.Cache.OperationHash, target, "generation")
			return record, nil
		}
		if number == config.Policy.MaxAttempts || !classification.Retryable {
			return record, failure
		}
		if err := record.waitBeforeRetry(ctx, call, classification, config); err != nil {
			return record, err
		}
		if err := executionContextError(ctx, config.Now()); err != nil {
			return record, err
		}
	}
}

func newCallRecord(callID, traceID string, target ExecutionTarget, origin Origin, cacheMode cachekey.Mode, cacheVersion string, attemptCapacity int) CallRecord {
	return CallRecord{
		CallID: callID, TraceID: traceID, SelectedTarget: target, GenerationTarget: target, Branch: "original",
		Origin:       origin,
		ResultSource: ResultSource{Kind: ResultSourceNone},
		Accounting:   Accounting{Result: accounting.EmptyLedger(), Provider: accounting.EmptyLedger()},
		Attempts:     make([]AttemptRecord, 0, attemptCapacity),
		Cache:        CacheFacts{Mode: cacheMode, Status: "skipped", Version: cacheVersion},
	}
}

func (record *CallRecord) startTiming(ctx context.Context, startedAt time.Time) {
	record.ResponseCreatedAt = startedAt.Unix()
	if deadline, ok := ctx.Deadline(); ok {
		deadlineUTC := deadline.UTC()
		record.Diagnostics.DeadlineAt = &deadlineUTC
		effective := max(deadline.Sub(startedAt).Milliseconds(), 0)
		record.Diagnostics.EffectiveTimeout = durationPointer(time.Duration(effective) * time.Millisecond)
	}
}

func (record *CallRecord) finishDiagnostics(config retry.Config, startedAt time.Time, err error) {
	if record.StopReason == "" {
		record.StopReason = stopReasonFor(err)
	}
	used := len(record.Attempts)
	record.Diagnostics.AttemptsUsed = used
	record.Diagnostics.AttemptsRemaining = max(config.Policy.MaxAttempts-used, 0)
	record.Diagnostics.Elapsed = max(config.Now().Sub(startedAt), 0)
	record.Diagnostics.Branch = record.Branch
	record.Diagnostics.StopReason = record.StopReason
}

func (record *CallRecord) setResultPayload(result ProviderResult) {
	record.Output, record.Search = result.Output, result.Search
	record.NativeResponse = result.NativeResponse
	record.AssistantText = result.AssistantText
	record.Accounting.Result = result.Accounting
}

func (record *CallRecord) acceptProviderResult(result ProviderResult, number int, producer ExecutionTarget) ProviderResult {
	if result.Search == nil {
		result.Search = record.Search
	}
	record.setResultPayload(result)
	record.ResultSource = ResultSource{Kind: ResultSourceProvider, AttemptNumber: number, Producer: &producer}
	return result
}

// Preserve effect-owned uncertainty while the logical context still determines
// cancellation/deadline classification. A local stop is not a remote outcome.
func executionContextFailure(failure, contextErr error) error {
	var uncertain interface{ InferenceUnconfirmed() bool }
	var provider *retry.ProviderError
	if (errors.As(failure, &uncertain) && uncertain.InferenceUnconfirmed()) || (errors.As(failure, &provider) && provider.Code == "execution_unconfirmed") {
		if errors.Is(failure, contextErr) {
			return failure
		}
		return errors.Join(contextErr, failure)
	}
	return contextErr
}

func executionContextError(ctx context.Context, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !now.Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}
func normalizedLedger(ledger Ledger) Ledger {
	if ledger.Usage.Status == "" {
		ledger.Usage = accounting.UnavailableUsage()
	}
	if ledger.Cost.Status == "" {
		ledger.Cost = accounting.UnavailableCost()
	}
	return ledger
}

func admitResult(result ProviderResult, callType string, validateStructured func(any) error) error {
	if accountingErr := validateLedger(result.Accounting); accountingErr != nil {
		return accountingErr
	}
	if !acceptedOutput(result.Output) {
		return &retry.ProviderError{Err: errors.New("provider returned no accepted output"), Code: "OUTPUT_REQUIRED", Category: retry.CategoryOther}
	}
	if searchErr := ValidateSearchResult(result.Search); searchErr != nil {
		return &retry.ProviderError{Err: errors.New("provider search metadata is invalid"), Code: "SEARCH_INVALID", Category: retry.CategoryOther}
	}
	if callType == "structured" {
		if validateStructured == nil {
			return errors.New("runtime structured output validator is required")
		}
		return validateStructured(result.Output)
	}
	return nil
}

func admitCachedResult(ctx context.Context, connection Connection, target ExecutionTarget, cached CachedResult, call Call) error {
	if err := validateCacheProducer(cached.Producer, target); err != nil {
		return err
	}
	validateStructured := func(value any) error {
		return validateStructuredValue(ctx, call.Observer, connection, false, call.ValidateStructured, value)
	}
	return admitResult(cached.ProviderResult, call.CallType, validateStructured)
}

func validateStructuredValue(ctx context.Context, observer Observer, connection Connection, repair bool, validator func(any) error, value any) error {
	if validator == nil {
		return errors.New("runtime structured output validator is required")
	}
	if observer == nil {
		return validator(value)
	}
	validationContext, finish := observer.StartValidation(ctx, connection, repair)
	_ = validationContext
	err := validator(value)
	finish(err)
	return err
}

func validateCacheProducer(producer, expected ExecutionTarget) error {
	if strings.TrimSpace(producer.ConnectionID) == "" || producer.Provider != expected.Provider || producer.Protocol != expected.Protocol || producer.Endpoint != expected.Endpoint || producer.ModelID != expected.ModelID {
		return NewCacheIntegrityError()
	}
	return nil
}

func validateLedger(ledger Ledger) error {
	if err := ledger.Usage.Validate(); err != nil {
		return accountingProviderError()
	}
	if err := ledger.Cost.Validate(); err != nil {
		return accountingProviderError()
	}
	return nil
}

func acceptedOutput(output any) bool {
	if output == nil {
		return false
	}
	if text, ok := output.(string); ok {
		return strings.TrimSpace(text) != ""
	}
	return true
}

func accountingProviderError() error {
	return &retry.ProviderError{Err: errors.New("provider accounting is invalid"), Code: "ACCOUNTING_INVALID", Category: retry.CategoryOther}
}

func combineAccountingFailure(existing error) error {
	accountingFailure := accountingProviderError()
	if existing == nil {
		return accountingFailure
	}
	if errors.Is(existing, context.Canceled) || errors.Is(existing, context.DeadlineExceeded) {
		return existing
	}
	var providerError *retry.ProviderError
	if !errors.As(existing, &providerError) {
		return accountingFailure
	}
	switch providerError.Code {
	case "ENDPOINT_POLICY", "TLS_OR_TRANSPORT_CONFIGURATION", "RESPONSE_TOO_LARGE":
		return existing
	}
	var accountingError *retry.ProviderError
	if !errors.As(accountingFailure, &accountingError) {
		return accountingFailure
	}
	merged := *accountingError
	merged.Status = providerError.Status
	merged.RetryAfter = providerError.RetryAfter
	merged.ProviderRequestID = providerError.ProviderRequestID
	merged.Type = providerError.Type
	return &merged
}

func targetFromConnection(connection Connection, modelID string) ExecutionTarget {
	return ExecutionTarget{
		ConnectionID: connection.ID, Provider: connection.Provider, Protocol: connection.APIInferenceType,
		Endpoint: connection.BaseURL, ModelID: modelID,
	}
}

func targetFromPrepared(connection Connection, prepared PreparedOperation) ExecutionTarget {
	target := targetFromConnection(connection, prepared.Operation.Model)
	target.Protocol = prepared.Operation.Protocol
	target.Endpoint = prepared.Operation.Endpoint.Identity
	target.ModelID = prepared.Operation.Model
	if prepared.Operation.ResponseProjection.Provider != "" {
		target.Provider = prepared.Operation.ResponseProjection.Provider
	}
	return target
}

func captureProviderParseFailure(record *CallRecord, err error, previousOutput *string) {
	var providerError *retry.ProviderError
	if !errors.As(err, &providerError) || providerError.Category != retry.CategoryParse || providerError.RawResponse == "" {
		return
	}
	*previousOutput = providerError.RawResponse
	captureParseFailureResponse(record, providerError.RawResponse)
}

func captureParseFailureResponse(record *CallRecord, raw string) {
	if record == nil || len(record.ParseFailureResponse) > 0 || raw == "" {
		return
	}
	encoded, err := json.Marshal(map[string]any{
		"schemaVersion": "harden-llm.parse-failure.v1",
		"rawResponse":   raw,
	})
	if err == nil {
		record.ParseFailureResponse = encoded
	}
}

func resolveAttemptCost(ledger Ledger, connection Connection) (accounting.Cost, error) {
	if connection.Pricing == nil || ledger.Cost.Source == "reported" {
		return ledger.Cost, nil
	}
	return accounting.ResolveCost(ledger.Usage, *connection.Pricing, nil)
}
