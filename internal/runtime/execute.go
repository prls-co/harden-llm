package runtime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
)

func Execute(
	ctx context.Context,
	executor Executor,
	credentials CredentialLookup,
	primary string,
	profiles map[string]Profile,
	call Call,
	retryConfig retry.Config,
	cache Cache,
	cacheMode cachekey.Mode,
	cacheVersion string,
	callID string,
	traceID string,
) (record CallRecord, err error) {
	if executor == nil {
		return CallRecord{}, errors.New("runtime executor is required")
	}
	if credentials == nil {
		return CallRecord{}, errors.New("credential lookup is required")
	}
	if call.Telemetry != nil {
		observation := CallObservation{CallType: call.CallType}
		if profile, ok := profiles[primary]; ok {
			observation.ProfileID = profile.ID
			observation.Provider = profile.Provider
			observation.ModelID = profile.ModelID
		}
		var endRuntime func(error)
		ctx, endRuntime = call.Telemetry.StartRuntime(ctx, observation)
		defer func() { endRuntime(err) }()
	}
	nodes := make(map[string]ProfileNode, len(profiles))
	for id, profile := range profiles {
		nodes[id] = ProfileNode{ID: profile.ID, Backups: append([]string(nil), profile.Backups...)}
	}
	plan, err := BuildBackupPlan(primary, nodes)
	if err != nil {
		return CallRecord{}, err
	}

	if cacheVersion == "" {
		cacheVersion = cachekey.DefaultVersion
	}
	selectedProfile := profiles[primary]
	record = CallRecord{
		CallID: callID, TraceID: traceID,
		SelectedTarget: targetFromProfile(selectedProfile),
		ResultSource:   ResultSource{Kind: ResultSourceNone},
		Accounting: Accounting{
			Result: accounting.EmptyLedger(), Provider: accounting.EmptyLedger(),
		},
		Cache: CacheFacts{Mode: cacheMode, Status: "skipped", Version: cacheVersion},
	}
	maxAttempts := retryConfig.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = retry.DefaultMaxAttempts
	}
	var lastErr error
	providerAccounting := accounting.EmptyLedger()
	for backupIndex, profileID := range plan {
		attemptOffset := len(record.Attempts)
		if attemptOffset >= maxAttempts {
			break
		}
		if err := ctx.Err(); err != nil {
			return record, err
		}
		profile := profiles[profileID]
		credential, err := credentials(ctx, profile)
		if err != nil {
			return record, err
		}
		prepared, err := executor.Prepare(ctx, profile, credential, call)
		if err != nil {
			return record, err
		}
		record.PreparedOperation = prepared
		if cacheMode != cachekey.ModeOff {
			if cache == nil {
				return record, errors.New("runtime cache is required when cache mode is active")
			}
			operationHash, hashErr := cachekey.Hash(prepared.Operation, cacheVersion)
			if hashErr != nil {
				return record, hashErr
			}
			record.Cache.OperationHash = operationHash
			if cacheMode == cachekey.ModeCache {
				cacheContext := ctx
				endCache := func(string, error) {}
				if call.Telemetry != nil {
					cacheContext, endCache = call.Telemetry.StartCache(ctx, "lookup")
				}
				cached, found, cacheErr := cache.Get(cacheContext, operationHash, cacheVersion)
				if cacheErr != nil {
					endCache("unknown", cacheErr)
					return record, cacheErr
				}
				if found {
					endCache("hit", nil)
					record.Output = cached.ProviderResult.Output
					record.Accounting.Result = normalizedLedger(cached.ProviderResult.Accounting)
					record.Accounting.Provider = providerAccounting
					producer := cached.Producer
					record.ResultSource = ResultSource{Kind: ResultSourceCache, Producer: &producer}
					record.RawProviderEnvelope = append(json.RawMessage(nil), cached.ProviderResult.RawProviderEnvelope...)
					record.Cache.Status = "hit"
					record.Cache.Served = true
					return record, nil
				}
				endCache("miss", nil)
				record.Cache.Status = "miss"
			} else {
				record.Cache.Status = "refresh"
			}
		}
		var providerResult ProviderResult
		activePrepared := prepared
		var lastFailure error
		var lastClassification retry.Classification
		previousOutput := ""
		repairAttempts := make(map[int]bool)
		attemptProfiles := make(map[int]string)
		attemptTargets := make(map[int]ExecutionTarget)
		providerUsed := make(map[int]bool)
		activeRetryConfig := retryConfig
		activeRetryConfig.MaxAttempts = maxAttempts - attemptOffset
		if call.Telemetry != nil {
			activeRetryConfig.Hooks = call.Telemetry.RetryHooks(call.CallType, retryConfig.Policy, attemptOffset, func(number int) ExecutionTarget {
				return attemptTargets[number]
			})
		}
		attempts, runErr := retry.Do(ctx, activeRetryConfig, func(attemptContext context.Context, localAttemptNumber int) error {
			globalAttemptNumber := attemptOffset + localAttemptNumber
			repairActive := false
			activeProfile := profile
			attemptProfiles[localAttemptNumber] = profile.ID
			attemptTargets[localAttemptNumber] = targetFromProfile(profile)
			if call.StructuredRepair.Enabled && RepairEligible(globalAttemptNumber-1, maxAttempts, lastClassification, len(call.Schema) > 0) {
				repairCall := call
				repairCall.Repair = buildRepairRequest(globalAttemptNumber, maxAttempts, previousOutput, call)
				repairProfile := profile
				repairCredential := credential
				if repairCall.Repair.ProfileID != "" {
					candidate, ok := profiles[repairCall.Repair.ProfileID]
					if !ok {
						lastFailure = errors.New("runtime: structured repair profile was not found")
						lastClassification = retry.Classify(lastFailure, retryConfig.Policy)
						return lastFailure
					}
					repairProfile = candidate
					var credentialErr error
					repairCredential, credentialErr = credentials(attemptContext, repairProfile)
					if credentialErr != nil {
						lastFailure = credentialErr
						lastClassification = retry.Classify(lastFailure, retryConfig.Policy)
						return lastFailure
					}
				}
				activeProfile = repairProfile
				attemptProfiles[localAttemptNumber] = repairProfile.ID
				attemptTargets[localAttemptNumber] = targetFromProfile(repairProfile)
				var prepareErr error
				activePrepared, prepareErr = executor.Prepare(attemptContext, repairProfile, repairCredential, repairCall)
				if prepareErr != nil {
					lastFailure = prepareErr
					lastClassification = retry.Classify(prepareErr, retryConfig.Policy)
					return prepareErr
				}
				repairActive = true
			}
			repairAttempts[localAttemptNumber] = repairActive
			activeTarget := targetFromPrepared(activeProfile, activePrepared)
			attemptTargets[localAttemptNumber] = activeTarget
			providerUsed[localAttemptNumber] = true
			providerContext := attemptContext
			endProvider := func(error) {}
			if call.Telemetry != nil {
				providerContext, endProvider = call.Telemetry.StartProvider(attemptContext, activeTarget, call.CallType)
			}
			result, executeErr := executor.Execute(providerContext, activePrepared)
			endProvider(executeErr)
			attemptAccounting := normalizedLedger(result.Accounting)
			if hasProviderAccounting(attemptAccounting) {
				var accountingErr error
				providerAccounting, accountingErr = accounting.AddLedger(providerAccounting, attemptAccounting)
				if accountingErr != nil {
					lastFailure = accountingErr
					lastClassification = retry.Classify(accountingErr, retryConfig.Policy)
					return accountingErr
				}
			}
			if executeErr != nil {
				captureProviderParseFailure(&record, executeErr, &previousOutput)
				lastFailure = executeErr
				lastClassification = retry.Classify(executeErr, retryConfig.Policy)
				return executeErr
			}
			if call.CallType == "structured" {
				if call.ValidateStructured == nil {
					return errors.New("structured call validator is required")
				}
				if repairActive {
					encoded, marshalErr := json.Marshal(result.Output)
					if marshalErr != nil {
						executeErr = &retry.ProviderError{Err: marshalErr, Parse: true}
					} else {
						data, _, repairErr := ExtractRepairData(encoded, func(raw json.RawMessage) error {
							var value any
							if err := json.Unmarshal(raw, &value); err != nil {
								return err
							}
							if call.Telemetry == nil {
								return call.ValidateStructured(value)
							}
							return call.Telemetry.ValidateSchema(attemptContext, activeProfile, true, func(context.Context) error {
								return call.ValidateStructured(value)
							})
						})
						if repairErr != nil {
							executeErr = &retry.ProviderError{Err: repairErr, Parse: true}
						} else if err := json.Unmarshal(data, &result.Output); err != nil {
							executeErr = &retry.ProviderError{Err: err, Parse: true}
						}
					}
				} else {
					var validationErr error
					if call.Telemetry == nil {
						validationErr = call.ValidateStructured(result.Output)
					} else {
						validationErr = call.Telemetry.ValidateSchema(attemptContext, activeProfile, false, func(context.Context) error {
							return call.ValidateStructured(result.Output)
						})
					}
					if validationErr != nil {
						executeErr = &retry.ProviderError{Err: validationErr, Parse: true}
					}
				}
				if executeErr != nil {
					encoded, _ := json.Marshal(result.Output)
					previousOutput = string(encoded)
					captureParseFailureResponse(&record, previousOutput)
					lastFailure = executeErr
					lastClassification = retry.Classify(executeErr, retryConfig.Policy)
					return executeErr
				}
			}
			providerResult = result
			providerResult.Accounting = attemptAccounting
			lastFailure = nil
			lastClassification = retry.Classification{Category: retry.CategorySuccess}
			return nil
		})
		for _, attempt := range attempts {
			attemptProfileID := profileID
			if repairProfileID := attemptProfiles[attempt.Number]; repairProfileID != "" {
				attemptProfileID = repairProfileID
			}
			record.Attempts = append(record.Attempts, AttemptRecord{
				Number: attemptOffset + attempt.Number, RetryLocalNumber: attempt.Number,
				ProfileID: attemptProfileID, BackupIndex: backupIndex,
				Target: attemptTargets[attempt.Number], ProviderUsed: providerUsed[attempt.Number],
				Category: attempt.Category, Status: attempt.Status, Retryable: attempt.Retryable,
				Delay: attempt.Delay, Duration: attempt.Duration, Repair: repairAttempts[attempt.Number],
				Code: attempt.Code, Type: attempt.Type, ProviderRequestID: attempt.ProviderRequestID,
			})
		}
		record.Accounting.Provider = providerAccounting
		if runErr == nil {
			record.Output = providerResult.Output
			record.Accounting.Result = providerResult.Accounting
			successfulAttempt := record.Attempts[len(record.Attempts)-1]
			producer := successfulAttempt.Target
			record.ResultSource = ResultSource{
				Kind: ResultSourceProvider, AttemptNumber: successfulAttempt.Number, Producer: &producer,
			}
			record.RawProviderEnvelope = append(record.RawProviderEnvelope[:0], providerResult.RawProviderEnvelope...)
			if cacheMode != cachekey.ModeOff {
				cacheContext := ctx
				endCache := func(string, error) {}
				if call.Telemetry != nil {
					cacheContext, endCache = call.Telemetry.StartCache(ctx, "write")
				}
				cachedResult := CachedResult{ProviderResult: providerResult, Producer: successfulAttempt.Target}
				if cacheErr := cache.Set(cacheContext, record.Cache.OperationHash, cacheVersion, prepared.Operation, cachedResult); cacheErr != nil {
					endCache("unknown", cacheErr)
					return record, cacheErr
				}
				endCache(record.Cache.Status, nil)
				record.Cache.Written = true
			}
			return record, nil
		}
		lastErr = runErr
		_ = lastFailure
		if !BackupEligible(retry.Classify(runErr, retryConfig.Policy)) || backupIndex == len(plan)-1 {
			return record, runErr
		}
	}
	if lastErr == nil && len(record.Attempts) >= maxAttempts {
		lastErr = errors.New("runtime: call-global attempt budget exhausted")
	}
	return record, lastErr
}

func hasProviderAccounting(ledger Ledger) bool {
	return ledger.Usage.Status != accounting.UsageUnavailable || ledger.Cost.Status != accounting.CostUnavailable
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
	if !errors.As(err, &providerError) || !providerError.Parse || providerError.RawResponse == "" {
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

func buildRepairRequest(attempt, maxAttempts int, previousOutput string, call Call) *RepairRequest {
	request := &RepairRequest{
		Attempt: attempt, MaxAttempts: maxAttempts, PreviousOutput: previousOutput,
		TargetSchema: append(json.RawMessage(nil), call.Schema...),
	}
	if escalation := call.StructuredRepair.Escalation; escalation != nil && attempt >= escalation.Attempt {
		request.Escalated = true
		request.ProfileID = escalation.ProfileID
		request.ModelID = escalation.ModelID
		request.ReasoningEffort = escalation.ReasoningEffort
	}
	return request
}
