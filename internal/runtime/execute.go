package runtime

import (
	"context"
	"encoding/json"
	"errors"

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
) (CallRecord, error) {
	if executor == nil {
		return CallRecord{}, errors.New("runtime executor is required")
	}
	if credentials == nil {
		return CallRecord{}, errors.New("credential lookup is required")
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
	record := CallRecord{
		CallID: callID, TraceID: traceID,
		Cache: CacheFacts{Mode: cacheMode, Status: "skipped", Version: cacheVersion},
	}
	var lastErr error
	for backupIndex, profileID := range plan {
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
				cached, found, cacheErr := cache.Get(ctx, operationHash, cacheVersion)
				if cacheErr != nil {
					return record, cacheErr
				}
				if found {
					record.Output = cached.Output
					record.Usage = cached.Usage
					record.Cost = cached.Cost
					record.RawProviderEnvelope = append(json.RawMessage(nil), cached.RawProviderEnvelope...)
					record.Cache.Status = "hit"
					record.Cache.Served = true
					return record, nil
				}
				record.Cache.Status = "miss"
			} else {
				record.Cache.Status = "refresh"
			}
		}
		var providerResult ProviderResult
		var aggregateUsage Usage
		var aggregateCost Cost
		costObserved := false
		activePrepared := prepared
		var lastFailure error
		var lastClassification retry.Classification
		previousOutput := ""
		repairAttempts := make(map[int]bool)
		attempts, runErr := retry.Do(ctx, retryConfig, func(attemptContext context.Context, _ int) error {
			attemptNumber := len(repairAttempts) + 1
			repairActive := false
			if call.StructuredRepair.Enabled && RepairEligible(attemptNumber-1, retryConfig.MaxAttempts, lastClassification, len(call.Schema) > 0) {
				repairCall := call
				repairCall.Repair = buildRepairRequest(attemptNumber, retryConfig.MaxAttempts, previousOutput, call)
				var prepareErr error
				activePrepared, prepareErr = executor.Prepare(attemptContext, profile, credential, repairCall)
				if prepareErr != nil {
					lastFailure = prepareErr
					lastClassification = retry.Classify(prepareErr, retryConfig.Policy)
					return prepareErr
				}
				repairActive = true
			}
			repairAttempts[attemptNumber] = repairActive
			result, executeErr := executor.Execute(attemptContext, activePrepared)
			if executeErr != nil {
				lastFailure = executeErr
				lastClassification = retry.Classify(executeErr, retryConfig.Policy)
				return executeErr
			}
			aggregateUsage = addUsage(aggregateUsage, result.Usage)
			aggregateCost, costObserved = addCost(aggregateCost, costObserved, result.Cost)
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
							return call.ValidateStructured(value)
						})
						if repairErr != nil {
							executeErr = &retry.ProviderError{Err: repairErr, Parse: true}
						} else if err := json.Unmarshal(data, &result.Output); err != nil {
							executeErr = &retry.ProviderError{Err: err, Parse: true}
						}
					}
				} else if validationErr := call.ValidateStructured(result.Output); validationErr != nil {
					executeErr = &retry.ProviderError{Err: validationErr, Parse: true}
				}
				if executeErr != nil {
					encoded, _ := json.Marshal(result.Output)
					previousOutput = string(encoded)
					lastFailure = executeErr
					lastClassification = retry.Classify(executeErr, retryConfig.Policy)
					return executeErr
				}
			}
			providerResult = result
			providerResult.Usage = aggregateUsage
			if costObserved {
				providerResult.Cost = aggregateCost
			}
			lastFailure = nil
			lastClassification = retry.Classification{Category: retry.CategorySuccess}
			return nil
		})
		for index := range attempts {
			attempts[index].ProfileID = profileID
			attempts[index].BackupIndex = backupIndex
			attempts[index].Repair = repairAttempts[attempts[index].Number]
		}
		record.Attempts = append(record.Attempts, attempts...)
		if runErr == nil {
			record.Output = providerResult.Output
			record.Usage = providerResult.Usage
			record.Cost = providerResult.Cost
			record.RawProviderEnvelope = append(record.RawProviderEnvelope[:0], providerResult.RawProviderEnvelope...)
			if cacheMode != cachekey.ModeOff {
				if cacheErr := cache.Set(ctx, record.Cache.OperationHash, cacheVersion, prepared.Operation, providerResult); cacheErr != nil {
					return record, cacheErr
				}
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
	return record, lastErr
}

func buildRepairRequest(attempt, maxAttempts int, previousOutput string, call Call) *RepairRequest {
	request := &RepairRequest{
		Attempt: attempt, MaxAttempts: maxAttempts, PreviousOutput: previousOutput,
		TargetSchema: append(json.RawMessage(nil), call.Schema...),
	}
	if escalation := call.StructuredRepair.Escalation; escalation != nil && attempt >= escalation.Attempt {
		request.Escalated = true
		request.ModelID = escalation.ModelID
		request.ReasoningEffort = escalation.ReasoningEffort
	}
	return request
}

func addUsage(left, right Usage) Usage {
	return Usage{
		InputTokens:         left.InputTokens + right.InputTokens,
		CacheReadTokens:     left.CacheReadTokens + right.CacheReadTokens,
		CacheCreationTokens: left.CacheCreationTokens + right.CacheCreationTokens,
		OutputTokens:        left.OutputTokens + right.OutputTokens,
		ReasoningTokens:     left.ReasoningTokens + right.ReasoningTokens,
		TotalTokens:         left.TotalTokens + right.TotalTokens,
	}
}

func addCost(current Cost, observed bool, next Cost) (Cost, bool) {
	if !observed {
		return next, true
	}
	if !current.Known || !next.Known {
		return Cost{Known: false, Source: "unknown"}, true
	}
	source := current.Source
	if source != next.Source {
		source = "mixed"
	}
	return Cost{TotalUSD: current.TotalUSD + next.TotalUSD, Known: true, Source: source}, true
}
