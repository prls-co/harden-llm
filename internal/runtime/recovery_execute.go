package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
	call                Call
	profile             Profile
	target              ExecutionTarget
	prepared            PreparedOperation
	generationCall      Call
	branch              string
	stage               string
	history             []RepairHistoryEntry
	generationTarget    ExecutionTarget
	generationProfileID string
	generationHash      string
}

// hasExplicitRecoveryPlan intentionally excludes the legacy boolean. The old
// execution path remains available for stored v2 callers and fixtures.
func hasExplicitRecoveryPlan(policy retry.Policy) bool {
	return policy.UsesExplicitPlan()
}

func executeRecoveryPlan(
	ctx context.Context, executor Executor, credentials CredentialLookup,
	selected string, profiles map[string]Profile, call Call, config retry.Config,
	cache Cache, cacheMode cachekey.Mode, cacheVersion, callID, traceID string,
) (record CallRecord, err error) {
	if call.CallType != "structured" {
		return record, errors.New("runtime explicit recovery plans require structured calls")
	}
	if config.Random == nil {
		config.Random = func() float64 { return 0 }
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
	baseProfile, ok := profiles[selected]
	if !ok || selected == "" || baseProfile.ID != selected {
		return record, errors.New("runtime selected profile was not found")
	}
	baseCall := call
	if call.Telemetry != nil {
		var finish func(error)
		ctx, finish = call.Telemetry.StartRuntime(ctx, CallObservation{ProfileID: baseProfile.ID, Provider: baseProfile.Provider, ModelID: baseProfile.ModelID, CallType: call.CallType})
		defer func() { finish(err) }()
	}
	if call.WebSearch && call.SearchMemo == nil {
		// One memo belongs to the whole logical call. Repair calls disable search,
		// while a fresh rerun may reuse the generation's memoized fallback.
		baseCall.SearchMemo = &sync.Map{}
	}
	record = CallRecord{
		CallID: callID, TraceID: traceID, SelectedTarget: targetFromProfile(baseProfile),
		GenerationTarget: targetFromProfile(baseProfile), Branch: "original",
		Origin:       call.Origin,
		ResultSource: ResultSource{Kind: ResultSourceNone},
		Accounting:   Accounting{Result: accounting.EmptyLedger(), Provider: accounting.EmptyLedger()},
		Attempts:     make([]AttemptRecord, 0, min(config.Policy.MaxAttempts, 10)),
		Cache:        CacheFacts{Mode: cacheMode, Status: "skipped", Version: cacheVersion},
	}
	if err := executionContextError(ctx, config.Now()); err != nil {
		record.StopReason = stopReasonFor(err)
		return record, err
	}
	if cacheMode != cachekey.ModeOff && cache == nil {
		return record, errors.New("runtime cache is required when cache mode is active")
	}
	startedAt := config.Now()
	if deadline, ok := ctx.Deadline(); ok {
		deadlineUTC := deadline.UTC()
		record.Diagnostics.DeadlineAt = &deadlineUTC
		effective := max(deadline.Sub(startedAt).Milliseconds(), 0)
		record.Diagnostics.EffectiveTimeout = durationPointer(time.Duration(effective) * time.Millisecond)
	}
	var work *plannedWork
	var progressSequence uint64
	var activeStream *StreamDiagnostics
	emit := func(work *plannedWork, eventType string, attempt int, terminal bool) {
		if baseCall.Progress == nil || work == nil {
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
		event := ProgressSnapshot{Sequence: progressSequence, RunID: baseCall.Context.RunID, CallID: callID, TraceID: traceID,
			Type: eventType, Stage: work.stage, Branch: work.branch, ProfileID: work.profile.ID,
			ReasoningEffort: work.call.ReasoningEffort, Attempt: attempt, AttemptsUsed: len(record.Attempts),
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
		baseCall.Progress(event)
	}
	defer func() {
		if record.StopReason == "" {
			record.StopReason = stopReasonFor(err)
		}
		record.Diagnostics.AttemptsUsed = len(record.Attempts)
		record.Diagnostics.AttemptsRemaining = max(config.Policy.MaxAttempts-len(record.Attempts), 0)
		record.Diagnostics.Elapsed = max(config.Now().Sub(startedAt), 0)
		record.Diagnostics.Stage = recordLastStage(record.Attempts)
		record.Diagnostics.Branch = record.Branch
		record.Diagnostics.StopReason = record.StopReason
		emit(work, "run.terminal", len(record.Attempts), true)
	}()

	work, err = preparePlannedGeneration(ctx, executor, credentials, selected, baseProfile, profiles, baseCall, "original", stageOriginalGenerate, nil, config.Policy.MaxAttempts)
	if err != nil {
		return record, err
	}
	work.generationTarget = targetFromPrepared(work.profile, work.prepared)
	record.GenerationTarget = work.generationTarget
	if cacheMode != cachekey.ModeOff {
		work.generationHash, err = cachekey.Hash(work.prepared.Operation, cacheVersion)
		if err != nil {
			return record, err
		}
		record.Cache.OperationHash = work.generationHash
		record.Cache.OriginalOperationHash = work.generationHash
		if cacheMode == cachekey.ModeCache {
			// The initial progress snapshot is emitted after the generation
			// operation has been prepared but before this cache lookup. A cache
			// hit therefore has the same correlation IDs as a provider run.
			emit(work, "run.started", 0, false)
			cached, found, cacheErr := cache.Get(ctx, work.generationHash, cacheVersion)
			if cacheErr != nil {
				return record, cacheErr
			}
			if found {
				if err := admitPlannedCachedResult(ctx, work.profile, work.generationTarget, cached, baseCall); err != nil {
					return record, NewCacheIntegrityError()
				}
				record.Output, record.Search = cached.ProviderResult.Output, cached.ProviderResult.Search
				record.Accounting.Result = cached.ProviderResult.Accounting
				producer := cached.Producer
				record.ResultSource = ResultSource{Kind: ResultSourceCache, Producer: &producer}
				record.Cache.Status, record.Cache.Served = "hit", true
				record.Diagnostics.BranchCaches = append(record.Diagnostics.BranchCaches, BranchCache{Branch: "original", GenerationTarget: work.generationTarget, Cache: record.Cache})
				record.Diagnostics.Stage = work.stage
				record.Diagnostics.Branch = work.branch
				record.StopReason = "succeeded"
				return record, nil
			}
			record.Cache.Status = "miss"
		} else {
			emit(work, "run.started", 0, false)
			record.Cache.Status = "refresh"
		}
		if record.Cache.Status != "hit" {
			record.Diagnostics.BranchCaches = append(record.Diagnostics.BranchCaches, BranchCache{Branch: "original", GenerationTarget: work.generationTarget, Cache: record.Cache})
		}
	} else {
		emit(work, "run.started", 0, false)
	}
	// A generation cache hit is authoritative and must remain replayable even
	// when a formerly used repair profile was removed. On a miss/refresh, admit
	// every enabled leaf before the first provider dispatch.
	if err := validateRecoveryProfiles(profiles, baseProfile.ID, config.Policy); err != nil {
		record.StopReason = "configuration_error"
		return record, err
	}
	if err := validateRecoveryCredentials(ctx, credentials, profiles, baseProfile.ID, config.Policy); err != nil {
		record.StopReason = "configuration_error"
		return record, err
	}

	providerAccumulator := accounting.NewProviderAccumulator()
	attempt := 0
	lastWorkStage := ""
	lastWorkAttempt := 0
	for {
		emit(work, "run.progress", attempt, false)
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
		attemptContext := ctx
		endAttempt := func(error) {}
		if call.Telemetry != nil {
			attemptContext, endAttempt = call.Telemetry.StartAttempt(ctx, targetFromPrepared(work.profile, work.prepared), work.call.CallType, attempt)
		}
		providerContext := attemptContext
		endProvider := func(bool, error) {}
		if call.Telemetry != nil {
			providerContext, endProvider = call.Telemetry.StartProvider(attemptContext, targetFromPrepared(work.profile, work.prepared), work.call.CallType)
		}
		activeStream = &StreamDiagnostics{}
		prepared := work.prepared
		prepared.StreamProgress = func(snapshot StreamDiagnostics) {
			value := snapshot
			activeStream = &value
			emit(work, "run.progress", attempt, false)
		}
		result, failure := executor.Execute(providerContext, prepared)
		activeStream = nil
		finished := config.Now().UTC()
		providerUsed := result.ProviderDispatched
		if result.Search != nil {
			record.Search = result.Search
		}
		record.Diagnostics.ReceivedBytes += result.Stream.ReceivedBytes
		record.Diagnostics.EventCount += result.Stream.EventCount
		record.Diagnostics.OutputBytes += result.Stream.OutputBytes
		record.Diagnostics.OutputCodePoints += result.Stream.OutputCodePoints
		result.Accounting = normalizedLedger(result.Accounting)
		if accountingErr := providerAccumulator.Observe(providerUsed, result.Accounting); accountingErr != nil {
			failure = combineAccountingFailure(failure)
		}
		record.Accounting.Provider = providerAccumulator.Ledger()
		previousOutput := encodeRepairOutput(result.Output)
		if failure == nil {
			admissionErr := admitResult(result, work.call.CallType, func(value any) error {
				return validateStructuredValue(attemptContext, work.call.Telemetry, work.profile, work.call.Repair != nil, work.call.ValidateStructured, value)
			})
			if admissionErr != nil {
				failure = admissionErr
				// A completed structured response that fails the application
				// validator is the only semantic condition allowed to advance the
				// recovery plan.
				var providerError *retry.ProviderError
				if work.call.CallType == "structured" && !errors.As(admissionErr, &providerError) {
					failure = &retry.ProviderError{Err: admissionErr, Category: retry.CategoryParse}
				}
				if previousOutput != "" {
					captureParseFailureResponse(&record, previousOutput)
				}
			}
		}
		if failure != nil {
			captureProviderParseFailure(&record, failure, &previousOutput)
		}
		endProvider(result.ProviderDispatched, failure)
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
			Number: attempt, ProfileID: work.profile.ID, Target: targetFromPrepared(work.profile, work.prepared),
			ProviderUsed: providerUsed, Category: classification.Category, Status: classification.Status,
			Retryable: classification.Retryable, Duration: max(0, config.Now().Sub(started)),
			Repair: work.call.Repair != nil, Stage: work.stage, Branch: work.branch,
			TriggerAttempt: triggerAttempt(work.history), InputAttempts: inputAttempts,
			Code: classification.Code, Type: classification.Type, ProviderRequestID: classification.ProviderRequestID,
			StartedAt: &started, FinishedAt: &finished, ReasoningEffort: work.call.ReasoningEffort,
			DispatchObserved: providerUsed,
		}
		if result.Stream.ReceivedBytes > 0 || result.Stream.EventCount > 0 || result.Stream.OutputBytes > 0 || result.Stream.OutputCodePoints > 0 || result.Stream.TerminalState != "" {
			stream := result.Stream
			attemptRecord.Stream = &stream
		}
		if lastWorkStage == work.stage {
			attemptRecord.TransportRetryOf = lastWorkAttempt
		}
		record.Attempts = append(record.Attempts, attemptRecord)
		lastWorkStage, lastWorkAttempt = work.stage, attempt
		emit(work, "run.progress", attempt, false)
		endAttempt(failure)
		if failure == nil {
			record.Output = result.Output
			if result.Search != nil {
				record.Search = result.Search
			}
			record.Accounting.Result = result.Accounting
			producer := targetFromPrepared(work.profile, work.prepared)
			record.ResultSource = ResultSource{Kind: ResultSourceProvider, AttemptNumber: attempt, Producer: &producer}
			if cacheMode != cachekey.ModeOff {
				cacheResult := result
				if cacheResult.Search == nil {
					cacheResult.Search = record.Search
				}
				cacheErr := cache.Set(ctx, work.generationHash, cacheVersion, CachedResult{
					ProviderResult: cacheResult, Producer: producer,
					GenerationTarget: work.generationTarget, CompletedBy: map[bool]string{true: "repair", false: "generation"}[work.call.Repair != nil],
				})
				if cacheErr != nil {
					record.Cache.Status = "write_failed"
					return record, nil
				}
				record.Cache.Written = true
			}
			record.StopReason = "succeeded"
			return record, nil
		}
		if executionErr := executionContextError(ctx, config.Now()); executionErr != nil {
			record.StopReason = stopReasonFor(executionErr)
			return record, executionErr
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
			next, nextErr := prepareNextPlannedWork(ctx, executor, credentials, profiles, baseCall, *work, history, config.Policy, attempt)
			if nextErr != nil {
				record.StopReason = "configuration_error"
				return record, nextErr
			}
			if next == nil {
				record.StopReason = "recovery_exhausted"
				return record, failure
			}
			if next.branch != work.branch {
				next.generationTarget = targetFromPrepared(next.profile, next.prepared)
				record.Branch = next.branch
				record.GenerationTarget = next.generationTarget
				if cacheMode != cachekey.ModeOff {
					next.generationHash, err = cachekey.Hash(next.prepared.Operation, cacheVersion)
					if err != nil {
						return record, err
					}
					record.Cache.OperationHash = next.generationHash
					record.Cache.RerunOperationHash = next.generationHash
					if cacheMode == cachekey.ModeCache {
						cached, found, cacheErr := cache.Get(ctx, next.generationHash, cacheVersion)
						if cacheErr != nil {
							return record, cacheErr
						}
						if found {
							if cacheErr := admitPlannedCachedResult(ctx, next.profile, next.generationTarget, cached, baseCall); cacheErr != nil {
								return record, NewCacheIntegrityError()
							}
							record.Output, record.Search = cached.ProviderResult.Output, cached.ProviderResult.Search
							record.Accounting.Result = cached.ProviderResult.Accounting
							producer := cached.Producer
							record.ResultSource = ResultSource{Kind: ResultSourceCache, Producer: &producer}
							record.Cache.Status, record.Cache.Served = "hit", true
							record.Diagnostics.BranchCaches = append(record.Diagnostics.BranchCaches, BranchCache{Branch: "rerun", GenerationTarget: next.generationTarget, Cache: record.Cache})
							record.Diagnostics.Stage = next.stage
							record.Diagnostics.Branch = next.branch
							record.StopReason = "succeeded"
							return record, nil
						}
					}
					if record.Cache.Status != "hit" {
						record.Diagnostics.BranchCaches = append(record.Diagnostics.BranchCaches, BranchCache{Branch: "rerun", GenerationTarget: next.generationTarget, Cache: record.Cache})
					}
				}
			}
			work = next
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
		delay := retry.Delay(attempt, classification.RetryAfter, config.Policy.Backoff, config.Random())
		if deadline, ok := ctx.Deadline(); ok && delay >= deadline.Sub(config.Now()) {
			record.StopReason = "deadline_exceeded"
			return record, context.DeadlineExceeded
		}
		record.Attempts[len(record.Attempts)-1].Delay = delay
		waitStarted := config.Now()
		waitErr := error(nil)
		if call.Telemetry != nil {
			waitErr = call.Telemetry.WaitForRetry(ctx, targetFromPrepared(work.profile, work.prepared), work.call.CallType, classification, delay, config.Wait)
		} else {
			waitErr = config.Wait(ctx, delay)
		}
		actualWait := max(0, config.Now().Sub(waitStarted))
		record.Diagnostics.TotalActualWait += actualWait
		record.Attempts[len(record.Attempts)-1].WaitDiagnostics = &WaitDiagnostics{Reason: string(classification.Category), Planned: delay, Actual: actualWait, RetryAfter: classification.RetryAfter}
		if waitErr != nil {
			record.StopReason = stopReasonFor(waitErr)
			return record, waitErr
		}
	}
}

func preparePlannedGeneration(ctx context.Context, executor Executor, credentials CredentialLookup, selected string, selectedProfile Profile, profiles map[string]Profile, baseCall Call, branch, stage string, target *retry.RecoveryTarget, maxAttempts int) (*plannedWork, error) {
	return preparePlannedWork(ctx, executor, credentials, profiles, baseCall, selectedProfile, selected, branch, stage, target, nil, maxAttempts)
}

func prepareNextPlannedWork(ctx context.Context, executor Executor, credentials CredentialLookup, profiles map[string]Profile, baseCall Call, current plannedWork, history []RepairHistoryEntry, policy retry.Policy, attempt int) (*plannedWork, error) {
	next := nextPlannedStage(policy, current)
	if next == "" {
		return nil, nil
	}
	if next == stageRerunGenerate {
		target := policy.Rerun.Target
		profile, profileID, err := resolveRecoveryProfile(profiles, current.generationProfileID, target)
		if err != nil {
			return nil, err
		}
		call := baseCall
		call.Repair = nil
		call.WebSearch = baseCall.WebSearch
		return preparePlannedWork(ctx, executor, credentials, profiles, call, profile, profileID, "rerun", next, &target, nil, policy.MaxAttempts)
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
	profile, profileID, err := resolveRecoveryProfile(profiles, current.generationProfileID, target)
	if err != nil {
		return nil, err
	}
	call := baseCall
	call.WebSearch = false
	if target.Source == "generation" {
		call = current.generationCall
		call.WebSearch = false
	}
	call.Repair = &RepairRequest{Attempt: attempt + 1, MaxAttempts: policy.MaxAttempts, Stage: next, Branch: current.branch, TargetSchema: append(json.RawMessage(nil), baseCall.Schema...), History: append([]RepairHistoryEntry(nil), history...)}
	nextWork, err := preparePlannedWork(ctx, executor, credentials, profiles, call, profile, profileID, current.branch, next, &target, call.Repair, policy.MaxAttempts)
	if err != nil {
		return nil, err
	}
	nextWork.generationHash = current.generationHash
	nextWork.generationTarget = current.generationTarget
	nextWork.generationProfileID = current.generationProfileID
	nextWork.generationCall = current.generationCall
	return nextWork, nil
}

func preparePlannedWork(ctx context.Context, executor Executor, credentials CredentialLookup, profiles map[string]Profile, baseCall Call, profile Profile, profileID, branch, stage string, target *retry.RecoveryTarget, repair *RepairRequest, maxAttempts int) (*plannedWork, error) {
	call := baseCall
	call.Repair = repair
	if target != nil {
		if target.Source == "profile" {
			// A profile target is a leaf operation. Do not carry provider
			// options from the failed generation into a different target.
			call.ProviderOptions = nil
			// An omitted target reasoning setting means that target profile's
			// native default, not the failed model's portable override, applies.
			call.ReasoningEffort = target.ReasoningEffort
			if target.ProviderOptions != nil {
				call.ProviderOptions = cloneMap(target.ProviderOptions)
			}
			if target.ModelID != "" {
				profile.ModelID = target.ModelID
			}
		}
	}
	credential, err := credentials(ctx, profile)
	if err != nil {
		return nil, err
	}
	prepared, err := executor.Prepare(ctx, profile, credential, call)
	if err != nil {
		return nil, err
	}
	var history []RepairHistoryEntry
	if repair != nil {
		history = append([]RepairHistoryEntry(nil), repair.History...)
	}
	generationCall := call
	generationCall.Repair = nil
	return &plannedWork{call: call, profile: profile, generationCall: generationCall, target: targetFromPrepared(profile, prepared), prepared: prepared, branch: branch, stage: stage, history: history, generationProfileID: profileID}, nil
}

func resolveRecoveryProfile(profiles map[string]Profile, generationID string, target retry.RecoveryTarget) (Profile, string, error) {
	id := target.ProfileID
	if target.Source == "generation" {
		id = generationID
	}
	profile, ok := profiles[id]
	if !ok || id == "" || profile.ID != id {
		return Profile{}, "", fmt.Errorf("runtime recovery target profile %q was not found", id)
	}
	return profile, id, nil
}

// validateRecoveryProfiles performs all leaf-target admission before the first
// provider dispatch. A recovery target never executes the saved policy on the
// referenced profile; it only reads that profile's provider capabilities.
func validateRecoveryProfiles(profiles map[string]Profile, _ string, policy retry.Policy) error {
	check := func(role string, target retry.RecoveryTarget, allowGeneration bool) error {
		if err := target.Validate("recoveryPolicy."+role, allowGeneration); err != nil {
			return err
		}
		if target.Source == "generation" {
			return nil
		}
		profile, ok := profiles[target.ProfileID]
		if !ok || target.ProfileID == "" || profile.ID != target.ProfileID {
			return &retry.ValidationError{Field: "recoveryPolicy." + role + ".profileId", Message: fmt.Sprintf("profile %q was not found", target.ProfileID)}
		}
		return nil
	}
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

// validateRecoveryCredentials admits every profile-backed leaf before the
// first provider dispatch. The generation profile has already been resolved
// while preparing the initial operation; it is skipped here to avoid doing
// duplicate secret lookups. A generation-cache hit intentionally bypasses
// this check because it does not dispatch any recovery target.
func validateRecoveryCredentials(ctx context.Context, credentials CredentialLookup, profiles map[string]Profile, generationID string, policy retry.Policy) error {
	if credentials == nil {
		return errors.New("runtime recovery credentials are unavailable")
	}
	seen := map[string]struct{}{generationID: {}}
	check := func(target retry.RecoveryTarget) error {
		if target.Source == "generation" || target.ProfileID == "" {
			return nil
		}
		if _, ok := seen[target.ProfileID]; ok {
			return nil
		}
		profile, ok := profiles[target.ProfileID]
		if !ok {
			return &retry.ValidationError{Field: "recoveryPolicy", Message: fmt.Sprintf("profile %q was not found", target.ProfileID)}
		}
		if _, err := credentials(ctx, profile); err != nil {
			return fmt.Errorf("recovery target profile %q credentials: %w", target.ProfileID, err)
		}
		seen[target.ProfileID] = struct{}{}
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

func recordLastStage(attempts []AttemptRecord) string {
	if len(attempts) == 0 {
		return ""
	}
	return attempts[len(attempts)-1].Stage
}

func admitPlannedCachedResult(ctx context.Context, profile Profile, generationTarget ExecutionTarget, cached CachedResult, call Call) error {
	if cached.GenerationTarget.ProfileID == "" {
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
		return validateStructuredValue(ctx, call.Telemetry, profile, false, call.ValidateStructured, value)
	})
}

func validateTargetSnapshot(target ExecutionTarget) error {
	if strings.TrimSpace(target.ProfileID) == "" || strings.TrimSpace(target.Provider) == "" || strings.TrimSpace(target.Protocol) == "" || strings.TrimSpace(target.Endpoint) == "" || strings.TrimSpace(target.ModelID) == "" {
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
