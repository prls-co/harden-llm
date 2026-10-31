package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-491

import (
	"encoding/json"
	hardenllm "github.com/prls-co/harden-llm"
	apicontract "github.com/prls-co/harden-llm/api"
	"strings"
	"testing"
	"time"
)

func TestRequestTrackingID(t *testing.T) {
	t.Parallel()
	valid := "a60f21da-604f-4fe3-a039-23382ec6b313"
	for _, value := range []string{valid, "00000001-0000-1000-8000-000000000000"} {
		if !ValidTrackingID(value) {
			t.Errorf("rejected canonical nonnil UUID %q", value)
		}
	}
	for _, value := range []string{"", "bad", strings.ToUpper(valid), strings.ReplaceAll(valid, "-", ""), "urn:uuid:" + valid, "00000000-0000-0000-0000-000000000000", valid + "," + valid, " " + valid} {
		if ValidTrackingID(value) {
			t.Errorf("accepted invalid UUID %q", value)
		}
	}
}

func TestRequestTrackingObservation(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	wait := start.Add(55 * time.Second)
	stage, reason := "retry_wait", "provider_throttled"
	r := TrackingRecord{RequestStatus: RequestStatus{ID: "a60f21da-604f-4fe3-a039-23382ec6b313", Endpoint: "/v1/responses", Status: "waiting", Stage: &stage, Reason: &reason, CreatedAt: start, DeadlineAt: start.Add(time.Minute), RetentionExpiresAt: start.Add(24*time.Hour + time.Minute), UpdatedAt: start.Add(40 * time.Second), LastWorkerHeartbeatAt: start.Add(40 * time.Second), NextAttemptAt: &wait}, OwnerID: "private-nonce", Revision: 3}
	view := r.Observe(start.Add(42 * time.Second))
	if view.ElapsedMS != 42000 || view.RetryAfterMS == nil || *view.RetryAfterMS != 13000 || view.ObservationStale || !view.ObservedAt.Equal(start.Add(42*time.Second)) {
		t.Fatalf("incorrect waiting projection: %+v", view)
	}
	if r.Observe(start.Add(56*time.Second)).ObservationStale != true {
		t.Fatal("lost heartbeat did not become stale")
	}
	unknown := r.Observe(start.Add(91 * time.Second))
	if unknown.Status != "unconfirmed" || unknown.OutcomeConfirmed == nil || *unknown.OutcomeConfirmed || unknown.FinishedAt != nil || unknown.NextAttemptAt != nil || unknown.Reason == nil || *unknown.Reason != "execution_unobserved" {
		t.Fatalf("incorrect derived uncertainty: %+v", unknown)
	}
	if r.Status != "waiting" || r.FinishedAt != nil {
		t.Fatal("GET projection mutated stored state")
	}
	finish := start.Add(50 * time.Second)
	confirmed := true
	r.Status, r.FinishedAt, r.OutcomeConfirmed, r.NextAttemptAt = "completed", &finish, &confirmed, nil
	if done := r.Observe(start.Add(200 * time.Second)); done.ElapsedMS != 50000 || done.ObservationStale || done.Status != "completed" {
		t.Fatalf("terminal elapsed/liveness changed: %+v", done)
	}
	if skew := r.Observe(start.Add(-time.Second)); skew.ElapsedMS < 0 {
		t.Fatal("clock skew produced negative elapsed")
	}
	encoded, err := json.Marshal(r)
	if err != nil || strings.Contains(string(encoded), "private-nonce") || strings.Contains(string(encoded), "revision") {
		t.Fatal("public projection leaked invocation metadata", err)
	}
}

func TestRequestTrackingSession(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	record, err := NewTrackingRecord("a60f21da-604f-4fe3-a039-23382ec6b313", "/v1/responses", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	session := NewTrackingSession(record, func() time.Time { return now })
	session.Phase("provider_attempt")
	session.Attempt(1)
	first := session.Snapshot(false)
	now = now.Add(time.Second)
	heartbeat := session.Snapshot(true)
	if !heartbeat.LastProgressAt.Equal(*first.LastProgressAt) || !heartbeat.LastWorkerHeartbeatAt.Equal(now) {
		t.Fatal("heartbeat fabricated execution progress")
	}
	oldEnd := session.Wait("provider_throttled", 10*time.Second)
	session.Phase("validation")
	newer := session.Snapshot(false)
	oldEnd()
	if got := session.Snapshot(false); got.Stage == nil || *got.Stage != "validation" || got.Revision != newer.Revision {
		t.Fatal("old wait exit cleared a newer phase")
	}
	session.Finish("completed", "", true)
	terminal := session.Snapshot(false)
	now = now.Add(time.Second)
	session.Phase("provider_attempt")
	session.Attempt(5)
	session.Wait("retry_backoff", time.Second)()
	session.Snapshot(true)
	if got := session.Snapshot(false); got.Revision != terminal.Revision || got.Status != "completed" || !got.LastWorkerHeartbeatAt.Equal(terminal.LastWorkerHeartbeatAt) {
		t.Fatal("terminal session was revived")
	}
}

func TestRequestTrackingWireSchema(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	r, _ := NewTrackingRecord("a60f21da-604f-4fe3-a039-23382ec6b313", "/v1/responses", start, start.Add(time.Minute))
	for _, status := range []string{"running", "waiting", "completed", "failed", "cancelled", "unconfirmed"} {
		view := r.Observe(start.Add(time.Second))
		view.Status = status
		bytes, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		if err := apicontract.Validate("RequestStatus", bytes); err != nil {
			t.Fatal("status does not satisfy canonical schema", status, err)
		}
	}
	for _, field := range []string{"status", "stage", "reason", "error_code"} {
		bytes, _ := json.Marshal(r.Observe(start))
		var object map[string]any
		json.Unmarshal(bytes, &object)
		object[field] = "unbounded-provider-value"
		bytes, _ = json.Marshal(object)
		if err := apicontract.Validate("RequestStatus", bytes); err == nil {
			t.Fatal("schema accepted unknown enum", field)
		}
	}
}

func TestRequestTrackingProgressSources(t *testing.T) {
	t.Parallel()
	now := time.Now()
	record, _ := NewTrackingRecord("a60f21da-604f-4fe3-a039-23382ec6b313", "/v1/responses", now, now.Add(time.Minute))
	session := NewTrackingSession(record, func() time.Time { return now })
	session.Phase("provider_attempt")
	session.Progress(hardenllm.ProgressEvent{Sequence: 1, AttemptsUsed: 1, ReceivedBytes: 10, EventCount: 1})
	first := session.Snapshot(false)
	now = now.Add(time.Second)
	session.Progress(hardenllm.ProgressEvent{Sequence: 2, AttemptsUsed: 1, ReceivedBytes: 10, EventCount: 1})
	if next := session.Snapshot(false); next.Revision != first.Revision || !next.LastProgressAt.Equal(*first.LastProgressAt) {
		t.Fatal("identical progress invented activity")
	}
	session.Wait("provider_throttled", time.Second)
	session.Progress(hardenllm.ProgressEvent{Sequence: 3, Stage: "validation", AttemptsUsed: 1, ReceivedBytes: 11, EventCount: 2})
	waiting := session.Snapshot(false)
	if waiting.Status != "waiting" || waiting.Reason == nil || *waiting.Reason != "provider_throttled" {
		t.Fatal("queued progress undid authoritative retry phase")
	}
	session.Progress(hardenllm.ProgressEvent{Sequence: 1, AttemptsUsed: 99, ReceivedBytes: 1000})
	if next := session.Snapshot(false); next.Revision != waiting.Revision || next.AttemptsUsed != 1 {
		t.Fatal("old progress source rewound projection")
	}
	session.Finish("completed", "", true)
	terminal := session.Snapshot(false)
	session.Progress(hardenllm.ProgressEvent{Sequence: 4, AttemptsUsed: 99, ReceivedBytes: 1000})
	if next := session.Snapshot(false); next.Revision != terminal.Revision {
		t.Fatal("progress revived final state")
	}
}
