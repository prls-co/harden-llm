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
	ctx context.Context, executor Executor, credentials CredentialLookup,
	selected string, profiles map[string]Profile, call Call, config retry.Config,
	cache Cache, cacheMode cachekey.Mode, cacheVersion, callID, traceID string,
) (record CallRecord, err error) {
	if ctx == nil {
		return record, errors.New("runtime context is required")
	}
	if executor == nil {
		return record, errors.New("runtime executor is required")
	}
	if credentials == nil {
		return record, errors.New("credential lookup is required")
	}
	if err := config.Policy.Validate(); err != nil {
		return record, err
	}
	profile, ok := profiles[selected]
	if !ok || selected == "" || profile.ID != selected {
		return record, errors.New("runtime selected profile was not found")
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
	if call.CallType == "structured" && hasExplicitRecoveryPlan(config.Policy) {
		return executeRecoveryPlan(ctx, executor, credentials, selected, profiles, call, config, cache, cacheMode, cacheVersion, callID, traceID)
	}
	if call.WebSearch {
		call.SearchMemo = &sync.Map{}
	}
	if call.Telemetry != nil {
		var finish func(error)
		ctx, finish = call.Telemetry.StartRuntime(ctx, CallObservation{ProfileID: profile.ID, Provider: profile.Provider, ModelID: profile.ModelID, CallType: call.CallType})
		defer func() { finish(err) }()
	}
	if cacheVersion == "" {
		cacheVersion = cachekey.DefaultVersion
	}
	record = CallRecord{
		CallID: callID, TraceID: traceID, SelectedTarget: targetFromProfile(profile),
		GenerationTarget: targetFromProfile(profile), Branch: "original",
		Origin:       call.Origin,
		ResultSource: ResultSource{Kind: ResultSourceNone},
		Accounting:   Accounting{Result: accounting.EmptyLedger(), Provider: accounting.EmptyLedger()},
		// Keep the slice capacity independent of request data. Policy.Validate
		// bounds MaxAttempts for execution, but a user-controlled value should
		// never determine an allocation size.
		Attempts: make([]AttemptRecord, 0),
		Cache:    CacheFacts{Mode: cacheMode, Status: "skipped", Version: cacheVersion},
	}
	startedAt := config.Now()
	if deadline, ok := ctx.Deadline(); ok {
		deadlineUTC := deadline.UTC()
		record.Diagnostics.DeadlineAt = &deadlineUTC
		effective := max(deadline.Sub(startedAt).Milliseconds(), 0)
		record.Diagnostics.EffectiveTimeout = durationPointer(time.Duration(effective) * time.Millisecond)
	}
	var progressSequence uint64
	var activeStream *StreamDiagnostics
	emitProgress := func(eventType string, attempt int, terminal bool) {
		if call.Progress == nil {
			return
		}
		progressSequence++
		var accountingSnapshot *Accounting
		if len(record.Attempts) > 0 {
			value := record.Accounting
			accountingSnapshot = &value
		}
		var effectiveTimeout *time.Duration
		if record.Diagnostics.EffectiveTimeout != nil {
			value := *record.Diagnostics.EffectiveTimeout
			effectiveTimeout = &value
		}
		receivedBytes := record.Diagnostics.ReceivedBytes
		eventCount := record.Diagnostics.EventCount
		outputBytes := record.Diagnostics.OutputBytes
		outputCodePoints := record.Diagnostics.OutputCodePoints
		if activeStream != nil {
			receivedBytes += activeStream.ReceivedBytes
			eventCount += activeStream.EventCount
			outputBytes += activeStream.OutputBytes
			outputCodePoints += activeStream.OutputCodePoints
		}
		event := ProgressSnapshot{Sequence: progressSequence, RunID: call.Context.RunID, CallID: callID, TraceID: traceID,
			Type: eventType, Stage: "original.generate", Branch: "original", ProfileID: profile.ID,
			ReasoningEffort: call.ReasoningEffort, Attempt: attempt, AttemptsUsed: len(record.Attempts),
			AttemptsRemaining: max(config.Policy.MaxAttempts-len(record.Attempts), 0),
			ElapsedMs:         max(config.Now().Sub(startedAt).Milliseconds(), 0), StopReason: record.StopReason, Terminal: terminal,
			ReceivedBytes: receivedBytes, EventCount: eventCount,
			OutputBytes: outputBytes, OutputCodePoints: outputCodePoints,
			LastActivity: config.Now(), MaxAttempts: config.Policy.MaxAttempts, EffectiveTimeout: effectiveTimeout,
			Origin: record.Origin, Attempts: append([]AttemptRecord(nil), record.Attempts...), Accounting: accountingSnapshot}
		if deadline, ok := ctx.Deadline(); ok {
			remaining := max(deadline.Sub(config.Now()).Milliseconds(), 0)
			event.DeadlineRemainingMs = &remaining
		}
		call.Progress(event)
	}
	defer func() {
		if record.StopReason == "" {
			record.StopReason = stopReasonFor(err)
		}
		record.Diagnostics.AttemptsUsed = len(record.Attempts)
		record.Diagnostics.AttemptsRemaining = max(config.Policy.MaxAttempts-len(record.Attempts), 0)
		record.Diagnostics.Elapsed = max(config.Now().Sub(startedAt), 0)
		record.Diagnostics.Stage = recordLastStage(record.Attempts)
		record.Diagnostics.Branch = "original"
		record.Diagnostics.StopReason = record.StopReason
		emitProgress("run.terminal", len(record.Attempts), true)
	}()
	emitProgress("run.started", 0, false)
	if err := executionContextError(ctx, config.Now()); err != nil {
		return record, err
	}
	credential, err := credentials(ctx, profile)
	if err != nil {
		return record, err
	}
	prepared, err := executor.Prepare(ctx, profile, credential, call)
	if err != nil {
		return record, err
	}
	record.PreparedOperation = prepared
	target := targetFromPrepared(profile, prepared)
	if cacheMode != cachekey.ModeOff {
		if cache == nil {
			return record, errors.New("runtime cache is required when cache mode is active")
		}
		record.Cache.OperationHash, err = cachekey.Hash(prepared.Operation, cacheVersion)
		if err != nil {
			return record, err
		}
		if cacheMode == cachekey.ModeCache {
			cacheContext := ctx
			endCache := func(string, error) {}
			if call.Telemetry != nil {
				cacheContext, endCache = call.Telemetry.StartCache(ctx, "lookup")
			}
			cached, found, cacheErr := cache.Get(cacheContext, record.Cache.OperationHash, cacheVersion)
			if cacheErr != nil {
				endCache("unknown", cacheErr)
				return record, cacheErr
			}
			if found {
				if admissionErr := admitCachedResult(cacheContext, profile, target, cached, call); admissionErr != nil {
					endCache("unknown", admissionErr)
					return record, NewCacheIntegrityError()
				}
				endCache("hit", nil)
				record.Output, record.Search = cached.ProviderResult.Output, cached.ProviderResult.Search
				record.Accounting.Result = cached.ProviderResult.Accounting
				producer := cached.Producer
				record.ResultSource = ResultSource{Kind: ResultSourceCache, Producer: &producer}
				record.Cache.Status, record.Cache.Served = "hit", true
				record.Diagnostics.BranchCaches = append(record.Diagnostics.BranchCaches, BranchCache{Branch: "original", GenerationTarget: target, Cache: record.Cache})
				return record, nil
			}
			endCache("miss", nil)
			record.Cache.Status = "miss"
		} else {
			record.Cache.Status = "refresh"
		}
		record.Diagnostics.BranchCaches = append(record.Diagnostics.BranchCaches, BranchCache{Branch: "original", GenerationTarget: target, Cache: record.Cache})
	}

	// The prepared request and its operation context advance together. A
	// transport retry leaves this work intact; only invalid output creates repair.
	work := struct {
		call     Call
		prepared PreparedOperation
	}{call, prepared}
	providerAccumulator := accounting.NewProviderAccumulator()
	for number := 1; ; number++ {
		if err := executionContextError(ctx, config.Now()); err != nil {
			return record, err
		}
		started := config.Now()
		startedUTC := started.UTC()
		attemptContext := ctx
		endAttempt := func(error) {}
		if call.Telemetry != nil {
			attemptContext, endAttempt = call.Telemetry.StartAttempt(ctx, target, call.CallType, number)
		}
		providerContext := attemptContext
		endProvider := func(bool, error) {}
		if call.Telemetry != nil {
			providerContext, endProvider = call.Telemetry.StartProvider(attemptContext, target, call.CallType)
		}
		activeStream = &StreamDiagnostics{}
		preparedAttempt := work.prepared
		preparedAttempt.StreamProgress = func(snapshot StreamDiagnostics) {
			value := snapshot
			activeStream = &value
			emitProgress("run.progress", number, false)
		}
		result, failure := executor.Execute(providerContext, preparedAttempt)
		activeStream = nil
		finishedUTC := config.Now().UTC()
		providerUsed := result.ProviderDispatched
		record.Diagnostics.ReceivedBytes += result.Stream.ReceivedBytes
		record.Diagnostics.EventCount += result.Stream.EventCount
		record.Diagnostics.OutputBytes += result.Stream.OutputBytes
		record.Diagnostics.OutputCodePoints += result.Stream.OutputCodePoints
		result.Accounting = normalizedLedger(result.Accounting)
		if accountingErr := providerAccumulator.Observe(providerUsed, result.Accounting); accountingErr != nil {
			failure = combineAccountingFailure(failure)
		}
		record.Accounting.Provider = providerAccumulator.Ledger()
		previousOutput := ""
		if failure == nil {
			validateStructured := func(value any) error {
				return validateStructuredValue(attemptContext, call.Telemetry, profile, work.call.Repair != nil, call.ValidateStructured, value)
			}
			admissionErr := admitResult(result, work.call.CallType, validateStructured)
			if admissionErr != nil {
				var providerError *retry.ProviderError
				if work.call.CallType == "structured" && !errors.As(admissionErr, &providerError) {
					failure = &retry.ProviderError{Err: admissionErr, Category: retry.CategoryParse}
					encoded, marshalErr := json.Marshal(result.Output)
					if marshalErr != nil {
						failure = marshalErr
					} else {
						previousOutput = string(encoded)
						captureParseFailureResponse(&record, previousOutput)
					}
				} else {
					failure = admissionErr
				}
			}
		}
		endProvider(result.ProviderDispatched, failure)
		if failure != nil {
			captureProviderParseFailure(&record, failure, &previousOutput)
		}
		if contextErr := executionContextError(ctx, config.Now()); contextErr != nil {
			failure = contextErr
		}
		classification := retry.Classify(failure, config.Policy)
		repairNext := classification.Category == retry.CategoryParse && call.CallType == "structured" && config.Policy.RepairInvalidOutput
		classification.Retryable = classification.Retryable || repairNext
		attemptRecord := AttemptRecord{
			Number: number, ProfileID: profile.ID, Target: target, ProviderUsed: providerUsed,
			Category: classification.Category, Status: classification.Status, Retryable: classification.Retryable,
			Duration: max(0, config.Now().Sub(started)), Repair: work.call.Repair != nil, Stage: "original.generate", Branch: "original",
			Code: classification.Code, Type: classification.Type, ProviderRequestID: classification.ProviderRequestID,
			StartedAt: &startedUTC, FinishedAt: &finishedUTC, ReasoningEffort: work.call.ReasoningEffort,
			DispatchObserved: providerUsed,
		}
		if result.Stream.ReceivedBytes > 0 || result.Stream.EventCount > 0 || result.Stream.OutputBytes > 0 || result.Stream.OutputCodePoints > 0 || result.Stream.TerminalState != "" {
			stream := result.Stream
			attemptRecord.Stream = &stream
		}
		record.Attempts = append(record.Attempts, attemptRecord)
		emitProgress("run.progress", number, false)
		endAttempt(failure)
		if failure == nil {
			record.Output = result.Output
			if result.Search != nil {
				record.Search = result.Search
			}
			record.Accounting.Result = result.Accounting
			producer := target
			record.ResultSource = ResultSource{Kind: ResultSourceProvider, AttemptNumber: number, Producer: &producer}
			if cacheMode != cachekey.ModeOff {
				cacheContext := ctx
				endCache := func(string, error) {}
				if call.Telemetry != nil {
					cacheContext, endCache = call.Telemetry.StartCache(ctx, "write")
				}
				cacheResult := result
				if cacheResult.Search == nil {
					cacheResult.Search = record.Search
				}
				cacheErr := cache.Set(cacheContext, record.Cache.OperationHash, cacheVersion, CachedResult{ProviderResult: cacheResult, Producer: target, GenerationTarget: target, CompletedBy: "generation"})
				if cacheErr != nil {
					record.Cache.Status = "write_failed"
					endCache(record.Cache.Status, cacheErr)
					return record, nil
				}
				endCache(record.Cache.Status, nil)
				record.Cache.Written = true
			}
			return record, nil
		}
		if number == config.Policy.MaxAttempts || !classification.Retryable {
			return record, failure
		}
		delay := retry.Delay(number, classification.RetryAfter, config.Policy.Backoff, config.Random())
		if deadline, ok := ctx.Deadline(); ok && delay >= deadline.Sub(config.Now()) {
			return record, context.DeadlineExceeded
		}
		record.Attempts[len(record.Attempts)-1].Delay = delay
		waitStarted := config.Now()
		if call.Telemetry == nil {
			err = config.Wait(ctx, delay)
		} else {
			err = call.Telemetry.WaitForRetry(ctx, target, call.CallType, classification, delay, config.Wait)
		}
		actualWait := max(0, config.Now().Sub(waitStarted))
		record.Diagnostics.TotalActualWait += actualWait
		record.Attempts[len(record.Attempts)-1].WaitDiagnostics = &WaitDiagnostics{Reason: string(classification.Category), Planned: delay, Actual: actualWait, RetryAfter: classification.RetryAfter}
		if err != nil {
			return record, err
		}
		if err := executionContextError(ctx, config.Now()); err != nil {
			return record, err
		}
		if repairNext {
			repairCall := call
			repairCall.Repair = buildRepairRequest(number+1, config.Policy.MaxAttempts, previousOutput, failure, call)
			repairPrepared, prepareErr := executor.Prepare(ctx, profile, credential, repairCall)
			if prepareErr != nil {
				return record, prepareErr
			}
			work.call, work.prepared = repairCall, repairPrepared
		}
	}
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

func admitCachedResult(ctx context.Context, profile Profile, target ExecutionTarget, cached CachedResult, call Call) error {
	if err := validateCacheProducer(cached.Producer, target); err != nil {
		return err
	}
	validateStructured := func(value any) error {
		return validateStructuredValue(ctx, call.Telemetry, profile, false, call.ValidateStructured, value)
	}
	return admitResult(cached.ProviderResult, call.CallType, validateStructured)
}

func validateStructuredValue(ctx context.Context, telemetry *Telemetry, profile Profile, repair bool, validator func(any) error, value any) error {
	if validator == nil {
		return errors.New("runtime structured output validator is required")
	}
	if telemetry == nil {
		return validator(value)
	}
	return telemetry.ValidateSchema(ctx, profile, repair, func(context.Context) error { return validator(value) })
}

func validateCacheProducer(producer, expected ExecutionTarget) error {
	if strings.TrimSpace(producer.ProfileID) == "" || producer.Provider != expected.Provider || producer.Protocol != expected.Protocol || producer.Endpoint != expected.Endpoint || producer.ModelID != expected.ModelID {
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

func targetFromProfile(profile Profile) ExecutionTarget {
	return ExecutionTarget{
		ProfileID: profile.ID, Provider: profile.Provider, Protocol: profile.APIInferenceType,
		Endpoint: profile.BaseURL, ModelID: profile.ModelID,
	}
}

func targetFromPrepared(profile Profile, prepared PreparedOperation) ExecutionTarget {
	target := targetFromProfile(profile)
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
