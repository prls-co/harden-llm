package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/retry"
)

type attemptObservation struct {
	context        context.Context
	finishAttempt  func(error)
	finishProvider func(bool, error)
}

// The caller finishes the provider and attempt observations at their own
// admission and classification boundaries.
func dispatchAttempt(ctx context.Context, executor Executor, prepared PreparedOperation, call Call, target ExecutionTarget, number int) (ProviderResult, error, attemptObservation) {
	observation := attemptObservation{context: ctx, finishAttempt: func(error) {}, finishProvider: func(bool, error) {}}
	if call.Observer != nil {
		observation.context, observation.finishAttempt = call.Observer.StartAttempt(ctx, target, call.CallType, number)
		ctx, observation.finishProvider = call.Observer.StartProvider(observation.context, target, call.CallType)
	}
	result, err := executor.Execute(ctx, prepared)
	return result, err, observation
}

func (record *CallRecord) accountAttempt(accumulator *accounting.ProviderAccumulator, connection Connection, result *ProviderResult, failure error) error {
	record.Diagnostics.ReceivedBytes += result.Stream.ReceivedBytes
	record.Diagnostics.EventCount += result.Stream.EventCount
	record.Diagnostics.OutputBytes += result.Stream.OutputBytes
	record.Diagnostics.OutputCodePoints += result.Stream.OutputCodePoints
	result.Accounting = normalizedLedger(result.Accounting)
	if result.ProviderDispatched {
		cost, err := resolveAttemptCost(result.Accounting, connection)
		if err != nil {
			failure = combineAccountingFailure(failure)
		} else {
			result.Accounting.Cost = cost
		}
	}
	if err := accumulator.Observe(result.ProviderDispatched, result.Accounting); err != nil {
		failure = combineAccountingFailure(failure)
	}
	record.Accounting.Provider = accumulator.Ledger()
	return failure
}

func admitProviderResult(ctx context.Context, connection Connection, call Call, result ProviderResult) (failure error, schemaRejected bool) {
	err := admitResult(result, call.CallType, func(value any) error {
		return validateStructuredValue(ctx, call.Observer, connection, call.Repair != nil, call.ValidateStructured, value)
	})
	var providerError *retry.ProviderError
	if err != nil && call.CallType == "structured" && !errors.As(err, &providerError) {
		return &retry.ProviderError{Err: err, Code: "STRUCTURED_SCHEMA", Category: retry.CategoryParse}, true
	}
	return err, false
}

func (attempt *AttemptRecord) setOutcome(result ProviderResult, classification retry.Classification, started, finished time.Time, elapsed time.Duration) {
	startedUTC := started.UTC()
	attempt.ConnectionID = attempt.Target.ConnectionID
	attempt.ProviderUsed = result.ProviderDispatched
	attempt.DispatchObserved = result.ProviderDispatched
	attempt.Category = classification.Category
	attempt.Status = classification.Status
	attempt.Retryable = classification.Retryable
	attempt.Code = classification.Code
	attempt.Type = classification.Type
	attempt.ProviderRequestID = classification.ProviderRequestID
	attempt.StartedAt = &startedUTC
	attempt.FinishedAt = &finished
	attempt.Duration = elapsed
	attempt.Stream = result.Stream.attemptSnapshot()
}

func (stream StreamDiagnostics) attemptSnapshot() *StreamDiagnostics {
	if stream.ReceivedBytes > 0 || stream.EventCount > 0 || stream.OutputBytes > 0 || stream.OutputCodePoints > 0 || stream.TerminalState != "" {
		return &stream
	}
	return nil
}

func (record *CallRecord) waitBeforeRetry(ctx context.Context, call Call, classification retry.Classification, config retry.Config) error {
	attempt := &record.Attempts[len(record.Attempts)-1]
	delay := retry.Delay(attempt.Number, classification.RetryAfter, config.Policy.Backoff, config.Random())
	if deadline, ok := ctx.Deadline(); ok && delay >= deadline.Sub(config.Now()) {
		return context.DeadlineExceeded
	}
	attempt.Delay = delay
	started := config.Now()
	err := waitForRetry(ctx, call.Observer, attempt.Target, call.CallType, classification, delay, config.Wait)
	actual := max(config.Now().Sub(started), 0)
	record.Diagnostics.TotalActualWait += actual
	attempt.WaitDiagnostics = &WaitDiagnostics{
		Reason: string(classification.Category), Planned: delay,
		Actual: actual, RetryAfter: classification.RetryAfter,
	}
	return err
}
