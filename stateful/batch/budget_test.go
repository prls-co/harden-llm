package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-496

import (
	"context"
	hardenllm "github.com/prls-co/harden-llm"
	"testing"
	"time"
)

func TestGenerationBudgetPreservesLegacyAndLongSnapshots(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ms   int64
		want time.Duration
	}{{0, 180 * time.Second}, {180000, 180 * time.Second}, {300000, 300 * time.Second}, {900000, 900 * time.Second}, {1000, time.Second}} {
		if got := generationBudget(Generation{TimeoutMS: tc.ms}); got != tc.want {
			t.Errorf("snapshot timeout %d resolved to %s, want %s", tc.ms, got, tc.want)
		}
	}
}
func TestGenerationRefusalAfterOutputCannotRequeue(t *testing.T) {
	t.Parallel()
	refusal := &hardenllm.ProviderError{Status: 429, Code: "rate_limit_exceeded", RetryAfter: time.Minute}
	for _, result := range []hardenllm.Result{
		{AssistantText: "partial"},
		{Diagnostics: hardenllm.Diagnostics{OutputBytes: 1}},
		{Attempts: []hardenllm.Attempt{{Stream: &hardenllm.StreamDiagnostics{OutputBytes: 1}}}},
	} {
		kind, _, wait := classifyGenerationError(refusal, result)
		if kind != "unconfirmed" || wait != 60000 {
			t.Fatal("partial inference was authorized for replay or lost future admission hint", kind, wait)
		}
	}
	if kind, _, _ := classifyGenerationError(refusal, hardenllm.Result{}); kind != "rejected" {
		t.Fatal("explicit pre-output refusal no longer recoverable")
	}
	if kind, _, _ := classifyGenerationError(&hardenllm.ProviderError{Status: 503, Code: "auth_unavailable"}, hardenllm.Result{}); kind != "policy" {
		t.Fatal("missing credentials treated as temporary capacity", kind)
	}
}

func TestGenerationContextSeparatesJobAndCallDeadlines(t *testing.T) {
	t.Parallel()
	before := time.Now()
	ctx, cancel := generationContext(context.Background(), Generation{TimeoutMS: 300000}, before.Add(time.Hour))
	defer cancel()
	after := time.Now()
	deadline, _ := ctx.Deadline()
	if deadline.Before(before.Add(300*time.Second)) || deadline.After(after.Add(300*time.Second)) {
		t.Fatal("new generation budget did not reach provider context")
	}
	jobDeadline := time.Now().Add(90 * time.Second)
	ctx, stop := generationContext(context.Background(), Generation{TimeoutMS: 300000}, jobDeadline)
	defer stop()
	actual, _ := ctx.Deadline()
	if !actual.Equal(jobDeadline) {
		t.Fatal("call extended the job deadline")
	}
	parent, cancelParent := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
	defer cancelParent()
	ctx, done := generationContext(parent, Generation{TimeoutMS: 900000}, time.Now().Add(time.Hour))
	defer done()
	want, _ := parent.Deadline()
	actual, _ = ctx.Deadline()
	if !actual.Equal(want) {
		t.Fatal("generation replaced an earlier parent deadline")
	}
}
