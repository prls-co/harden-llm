package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-025

import (
	"bytes"
	"encoding/json"
	"strings"
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

func TestRunOutputUsesEmptyAttemptsArrayWhenRuntimeHasNoAttempts(t *testing.T) {
	encoded, err := json.Marshal(RunOutput{Attempts: cloneAttempts(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"attempts":[]`)) {
		t.Fatalf("run output attempts = %s, want an empty array", encoded)
	}
}

func TestNormalizeRunResultDocumentRepairsRetainedNullAttempts(t *testing.T) {
	normalized := normalizeRunResultDocument(json.RawMessage(`{"schemaVersion":2,"attempts":null,"status":"succeeded"}`))
	if !bytes.Contains(normalized, []byte(`"attempts":[]`)) {
		t.Fatalf("normalized result = %s, want an empty attempts array", normalized)
	}

	unchanged := normalizeRunResultDocument(json.RawMessage(`{"schemaVersion":2,"attempts":[{"number":1}]}`))
	if string(unchanged) != `{"schemaVersion":2,"attempts":[{"number":1}]}` {
		t.Fatalf("non-null attempts changed: %s", unchanged)
	}
}

func TestRunArtifactsUseTypedIdentityWithoutParsingObjectKeys(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	references := []hardenllm.ArtifactRef{{
		ArtifactID: "artifact-explicit", Kind: "trace", Key: "opaque/and-not-derived.json",
		SHA256: strings.Repeat("a", 64), SizeBytes: 17, ContentType: "application/json",
	}}
	public, records := runArtifacts("owner-a", "run-a", "trace-a", references, now)
	if len(public) != 1 || len(records) != 1 || public[0].ArtifactID != "artifact-explicit" ||
		records[0].RunID != "run-a" || records[0].TraceID != "trace-a" || records[0].State != "available" {
		t.Fatalf("typed artifacts = %#v %#v", public, records)
	}
}
