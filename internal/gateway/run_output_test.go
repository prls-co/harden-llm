package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-025

import (
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

func TestRunOutputTimingAndRepairProjection(t *testing.T) {
	started := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	completed := started.Add(1250 * time.Millisecond)
	attempts := []hardenllm.Attempt{
		{Wait: 500 * time.Millisecond},
		{Wait: 250 * time.Millisecond, Repair: true},
	}

	if got := elapsedMilliseconds(started, completed); got != 1250 {
		t.Fatalf("elapsed milliseconds = %d, want 1250", got)
	}
	if got := elapsedMilliseconds(completed, started); got != 0 {
		t.Fatalf("backwards elapsed milliseconds = %d, want 0", got)
	}
	if got := attemptWaitMilliseconds(attempts); got != 750 {
		t.Fatalf("attempt wait milliseconds = %d, want 750", got)
	}
	if !attemptsUsedRepair(attempts) {
		t.Fatal("repair projection = false, want true")
	}
	if attemptsUsedRepair([]hardenllm.Attempt{{Repair: false}}) {
		t.Fatal("repair projection = true, want false")
	}
}
