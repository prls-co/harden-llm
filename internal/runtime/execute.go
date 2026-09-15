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
		ResultSource: ResultSource{Kind: ResultSourceNone},
		Accounting:   Accounting{Result: accounting.EmptyLedger(), Provider: accounting.EmptyLedger()},
		Attempts:     make([]AttemptRecord, 0, config.Policy.MaxAttempts),
		Cache:        CacheFacts{Mode: cacheMode, Status: "skipped", Version: cacheVersion},
	}
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
				cached.ProviderResult.Accounting = normalizedLedger(cached.ProviderResult.Accounting)
				if accountingErr := validateLedger(cached.ProviderResult.Accounting); accountingErr != nil {
					endCache("unknown", accountingErr)
					return record, accountingErr
				}
				endCache("hit", nil)
				record.Output, record.Search = cached.ProviderResult.Output, cached.ProviderResult.Search
				record.Accounting.Result = cached.ProviderResult.Accounting
				producer := cached.Producer
				record.ResultSource = ResultSource{Kind: ResultSourceCache, Producer: &producer}
				record.RawProviderEnvelope = append(json.RawMessage(nil), cached.ProviderResult.RawProviderEnvelope...)
				record.Cache.Status, record.Cache.Served = "hit", true
				return record, nil
			}
			endCache("miss", nil)
			record.Cache.Status = "miss"
		} else {
			record.Cache.Status = "refresh"
		}
	}

	// The prepared request and its operation context advance together. A
	// transport retry leaves this work intact; only invalid output creates repair.
	work := struct {
		call     Call
		prepared PreparedOperation
	}{call, prepared}
	for number := 1; ; number++ {
		if err := executionContextError(ctx, config.Now()); err != nil {
			return record, err
		}
		started := config.Now()
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
		result, failure := executor.Execute(providerContext, work.prepared)
		providerUsed := result.ProviderDispatched
		result.Accounting = normalizedLedger(result.Accounting)
		if accountingErr := validateLedger(result.Accounting); accountingErr != nil {
			if failure == nil {
				failure = accountingErr
			}
		} else if hasProviderAccounting(result.Accounting) {
			ledger, accountingErr := accounting.AddLedger(record.Accounting.Provider, result.Accounting)
			if accountingErr != nil {
				if failure == nil {
					failure = accountingProviderError()
				}
			} else {
				record.Accounting.Provider = ledger
			}
		}
		if len(result.RawProviderEnvelope) > 0 {
			record.RawProviderEnvelope = append(json.RawMessage(nil), result.RawProviderEnvelope...)
		}
		if failure == nil && !acceptedOutput(result.Output) {
			failure = &retry.ProviderError{Err: errors.New("provider returned no accepted output"), Code: "OUTPUT_REQUIRED", Category: retry.CategoryOther}
		}
		endProvider(result.ProviderDispatched, failure)
		previousOutput := ""
		if failure != nil {
			captureProviderParseFailure(&record, failure, &previousOutput)
		}
		if failure == nil && call.CallType == "structured" {
			var validationErr error
			if call.Telemetry == nil {
				validationErr = call.ValidateStructured(result.Output)
			} else {
				validationErr = call.Telemetry.ValidateSchema(attemptContext, profile, work.call.Repair != nil, func(context.Context) error { return call.ValidateStructured(result.Output) })
			}
			if validationErr != nil {
				failure = &retry.ProviderError{Err: validationErr, Category: retry.CategoryParse}
				encoded, marshalErr := json.Marshal(result.Output)
				if marshalErr != nil {
					failure = marshalErr
				} else {
					previousOutput = string(encoded)
					captureParseFailureResponse(&record, previousOutput)
				}
			}
		}
		if contextErr := executionContextError(ctx, config.Now()); contextErr != nil {
			failure = contextErr
		}
		classification := retry.Classify(failure, config.Policy)
		repairNext := classification.Category == retry.CategoryParse && call.CallType == "structured" && config.Policy.RepairInvalidOutput
		classification.Retryable = classification.Retryable || repairNext
		record.Attempts = append(record.Attempts, AttemptRecord{
			Number: number, ProfileID: profile.ID, Target: target, ProviderUsed: providerUsed,
			Category: classification.Category, Status: classification.Status, Retryable: classification.Retryable,
			Duration: max(0, config.Now().Sub(started)), Repair: work.call.Repair != nil,
			Code: classification.Code, Type: classification.Type, ProviderRequestID: classification.ProviderRequestID,
		})
		endAttempt(failure)
		if failure == nil {
			record.Output, record.Search = result.Output, result.Search
			record.Accounting.Result = result.Accounting
			producer := target
			record.ResultSource = ResultSource{Kind: ResultSourceProvider, AttemptNumber: number, Producer: &producer}
			record.RawProviderEnvelope = append(json.RawMessage(nil), result.RawProviderEnvelope...)
			if cacheMode != cachekey.ModeOff {
				cacheContext := ctx
				endCache := func(string, error) {}
				if call.Telemetry != nil {
					cacheContext, endCache = call.Telemetry.StartCache(ctx, "write")
				}
				cacheErr := cache.Set(cacheContext, record.Cache.OperationHash, cacheVersion, prepared.Operation, CachedResult{ProviderResult: result, Producer: target})
				if cacheErr != nil {
					endCache("unknown", cacheErr)
					return record, cacheErr
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
		if call.Telemetry == nil {
			err = config.Wait(ctx, delay)
		} else {
			err = call.Telemetry.WaitForRetry(ctx, target, call.CallType, classification, delay, config.Wait)
		}
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
