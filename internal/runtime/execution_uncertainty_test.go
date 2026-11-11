package runtime

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-540.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
)

type uncertainEffectError struct{ error }

func (uncertainEffectError) InferenceUnconfirmed() bool { return true }

func TestExecutionContextPreservesProviderUncertainty(t *testing.T) {
	t.Parallel()
	for _, contextErr := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, failure := range []error{uncertainEffectError{errors.New("private transport failure")}, uncertainEffectError{contextErr}, &retry.ProviderError{Code: "execution_unconfirmed", Category: retry.CategoryOther}} {
			combined := executionContextFailure(failure, contextErr)
			if !errors.Is(combined, contextErr) || !errors.Is(combined, failure) {
				t.Fatal("context replaced effect-owned uncertainty", combined)
			}
		}
		if executionContextFailure(errors.New("known failure"), contextErr) != contextErr {
			t.Fatal("logical deadline lost precedence for known failures")
		}
	}
}

type contextUncertaintyExecutor struct {
	progressContractExecutor
	before func()
}

func (e *contextUncertaintyExecutor) Execute(ctx context.Context, operation PreparedOperation) (ProviderResult, error) {
	e.before()
	return e.progressContractExecutor.Execute(ctx, operation)
}

// Drive the real core loops with their existing injected clock. Expiry occurs
// after confirmed effect entry, independent of compiler/host scheduling. The
// actual TLS interruption and local stop remain covered at T1.
func TestExecutionLoopsPreserveProviderUncertainty(t *testing.T) {
	t.Parallel()
	for _, structured := range []bool{false, true} {
		for _, contextErr := range []error{context.Canceled, context.DeadlineExceeded} {
			now := time.Now()
			deadline := now.Add(time.Hour)
			ctx, cancel := context.WithDeadline(t.Context(), deadline)
			policy := retry.Policy{MaxAttempts: 3, RetryOn: []retry.Category{retry.CategoryNetwork}, Backoff: retry.Backoff{}}
			call := Call{CallType: "text", ModelID: "fixture-model"}
			if structured {
				call, policy = decisionCall(), progressExplicitPolicy()
			}
			executor := &contextUncertaintyExecutor{progressContractExecutor: progressContractExecutor{outcomes: []progressContractOutcome{{result: ProviderResult{ProviderDispatched: true}, err: uncertainEffectError{contextErr}}}}, before: func() {
				if contextErr == context.Canceled {
					cancel()
				} else {
					now = deadline.Add(time.Nanosecond)
				}
			}}
			record, err := Execute(ctx, executor, progressConnection("original"), progressCredential, call, retry.Config{Policy: policy, Now: func() time.Time { return now }}, nil, cachekey.ModeOff, "v1", "call", "trace")
			cancel()
			var uncertain interface{ InferenceUnconfirmed() bool }
			if !errors.Is(err, contextErr) || !errors.As(err, &uncertain) || !uncertain.InferenceUnconfirmed() || executor.executes != 1 || len(record.Attempts) != 1 || record.Output != nil || record.Cache.Written {
				t.Fatalf("core lost uncertainty or replayed the effect: structured=%v context=%v calls=%d attempts=%d error=%v", structured, contextErr, executor.executes, len(record.Attempts), err)
			}
		}
	}
}
