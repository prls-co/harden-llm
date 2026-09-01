package command

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-061

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/postgres"
)

func TestLegacyHistoryClassifierAndDigest(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	candidate := postgres.RunlessTraceCandidate{
		Trace: postgres.TraceRecord{
			OwnerID: "owner-a", TraceID: "trace-a",
			Record:    json.RawMessage(`{"schemaVersion":1,"runId":"run-a","traceId":"trace-a","callId":"call-a","status":"succeeded","attempts":[],"cache":{},"usage":{},"cost":{}}`),
			CreatedAt: now, UpdatedAt: now,
		},
		Observations: []postgres.ObservationRecord{{
			OwnerID: "owner-a", TraceID: "trace-a", Sequence: 0, Type: "result",
			Data: json.RawMessage(`{"outcome":"success"}`), CreatedAt: now,
		}},
		Artifacts: []postgres.ArtifactRecord{{
			OwnerID: "owner-a", TraceID: "trace-a", ID: "artifact-a", Kind: "trace",
			ObjectKey:   "llm-traces/owner-a/run-a/trace-a/artifact-a.json",
			ContentType: "application/json", SHA256: string(make([]byte, 64)), SizeBytes: 1,
			State: "available", CreatedAt: now, UpdatedAt: now,
		}},
		Fingerprint: "fingerprint-a",
	}
	if runID, ok := classifyLegacyDeletedExecution(candidate); !ok || runID != "run-a" {
		t.Fatalf("classification = %q %t", runID, ok)
	}
	malformed := candidate
	malformed.Trace.Record = json.RawMessage(`{"schemaVersion":1,"runId":"run-a","traceId":"other"}`)
	if _, ok := classifyLegacyDeletedExecution(malformed); ok {
		t.Fatal("self-inconsistent retained trace was classified")
	}
	left := classifiedRunlessTrace{candidate: candidate, runID: "run-a", class: "legacy_deleted_execution"}
	rightCandidate := candidate
	rightCandidate.Trace.TraceID = "trace-b"
	rightCandidate.Fingerprint = "fingerprint-b"
	right := classifiedRunlessTrace{candidate: rightCandidate, runID: "run-b", class: "legacy_deleted_execution"}
	first := historyPlanDigest("all-owners", "", []classifiedRunlessTrace{left, right}, false)
	second := historyPlanDigest("all-owners", "", []classifiedRunlessTrace{right, left}, false)
	if first != second || len(first) != 64 {
		t.Fatalf("plan digests = %q %q", first, second)
	}
	if first == historyPlanDigest("all-owners", "", []classifiedRunlessTrace{left, right}, true) {
		t.Fatal("truncation did not change plan digest")
	}
}
