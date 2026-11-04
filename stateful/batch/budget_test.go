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

func TestTerminalThrottleCoolsWithoutRefundOrReplay(t *testing.T) {
	t.Parallel()
	at := time.Unix(1000, 0)
	controller := NewController(DefaultPolicy())
	controller.Start(at)
	before := controller.WindowStarts
	controller.CompleteOutcome(at, at, at, false, Outcome{Kind: "unconfirmed", Admission: "throttled"})
	if !controller.Cooling || controller.WindowStarts != before || controller.Rate != 50 {
		t.Fatal("terminal throttle reopened/refunded work", controller)
	}
	controller.CompleteOutcome(at, at, at, false, Outcome{Kind: "unconfirmed", Admission: "throttled"})
	if controller.Rate != 50 {
		t.Fatal("same cooling episode repeatedly halved pace")
	}
}

func TestReservationPreservesAcceptedSnapshot(t *testing.T) {
	t.Parallel()
	deadline := time.Unix(1000, 0)
	for _, tc := range []struct {
		name       string
		generation Generation
		budget     int64
		deadline   time.Time
		want       bool
	}{
		{"explicit", Generation{TimeoutMS: 300000}, 300000, deadline, true},
		{"shorter_job", Generation{TimeoutMS: 300000}, 300000, deadline.Add(-time.Second), true},
		{"legacy_zero", Generation{}, 180000, deadline, true},
		{"rewritten_budget", Generation{TimeoutMS: 300000}, 900000, deadline, false},
		{"rewritten_legacy", Generation{}, 300000, deadline, false},
		{"extended_job", Generation{TimeoutMS: 300000}, 300000, deadline.Add(time.Microsecond), false},
		{"missing_deadline", Generation{TimeoutMS: 300000}, 300000, time.Time{}, false},
		{"invalid_snapshot", Generation{TimeoutMS: -1}, 300000, deadline, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reservationMatchesSnapshot(Reservation{BudgetMS: tc.budget, Deadline: tc.deadline}, tc.generation, deadline); got != tc.want {
				t.Fatalf("snapshot check = %v, want %v", got, tc.want)
			}
		})
	}
}
