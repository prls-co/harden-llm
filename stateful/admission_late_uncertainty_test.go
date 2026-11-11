package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-540.

import (
	"context"
	"errors"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

func TestCPAAdmissionLateCompletionPreservesUncertainty(t *testing.T) {
	t.Parallel()
	store := newAdmissionMemoryStore()
	native := &admissionScriptNative{execute: func(ctx context.Context, _ hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
		// The effect returns a nominal success only after the accepted caller
		// deadline. Keep that real deadline; never fabricate a valid on-time result.
		<-ctx.Done()
		return hardenllm.ProviderResult{Output: "late", AssistantText: "late", ProviderDispatched: true}, nil
	}}
	client := admissionTestClient(t, store, native)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result, err := client.Call(ctx, admissionTestRequest())
	var uncertain interface{ InferenceUnconfirmed() bool }
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &uncertain) || !uncertain.InferenceUnconfirmed() || native.calls.Load() != 1 || len(store.observations) != 1 || store.observations[0].Outcome.Kind != "unconfirmed" || !store.observations[0].LocalStopped || result.Output != nil {
		t.Fatalf("late success lost uncertainty or crossed the accepted deadline: calls=%d observations=%d error=%v", native.calls.Load(), len(store.observations), err)
	}
}
