package runtime

import (
	"context"
	"time"

	"github.com/prls-co/harden-llm/internal/retry"
)

// Observer receives bounded execution facts only. Validation, waiting and
// provider work remain owned by the runtime.
type Observer interface {
	StartCall(context.Context, CallObservation) (context.Context, func(CallSummary, error))
	StartRuntime(context.Context, CallObservation) (context.Context, func(error))
	StartProvider(context.Context, ExecutionTarget, string) (context.Context, func(bool, error))
	StartAttempt(context.Context, ExecutionTarget, string, int) (context.Context, func(error))
	StartRetry(context.Context, ExecutionTarget, string, retry.Classification, time.Duration) (context.Context, func(error))
	StartValidation(context.Context, Connection, bool) (context.Context, func(error))
	StartCache(context.Context, string) (context.Context, func(string, error))
}

// CallSummary excludes payloads, prepared operations and credentials.
type CallSummary struct {
	CallID           string
	TraceID          string
	SelectedTarget   ExecutionTarget
	GenerationTarget ExecutionTarget
	Status           string
	Duration         time.Duration
	WaitDuration     time.Duration
	AttemptCount     int
	Cache            CacheFacts
	Accounting       Accounting
}

// Summarize projects one terminal record into payload-free observer facts.
func Summarize(record CallRecord, started, completed time.Time, err error) CallSummary {
	status := "succeeded"
	if err != nil {
		status = "failed"
	}
	return CallSummary{CallID: record.CallID, TraceID: record.TraceID, SelectedTarget: record.SelectedTarget, GenerationTarget: record.GenerationTarget, Status: status, Duration: max(completed.Sub(started), 0), WaitDuration: record.Diagnostics.TotalActualWait, AttemptCount: len(record.Attempts), Cache: record.Cache, Accounting: record.Accounting}
}

func waitForRetry(ctx context.Context, observer Observer, target ExecutionTarget, callType string, classification retry.Classification, delay time.Duration, wait func(context.Context, time.Duration) error) (err error) {
	if observer != nil {
		var finish func(error)
		ctx, finish = observer.StartRetry(ctx, target, callType, classification, delay)
		defer func() { finish(err) }()
	}
	return wait(ctx, delay)
}

type CallObservation struct {
	ConnectionID string
	Provider     string
	ModelID      string
	CallType     string
}
