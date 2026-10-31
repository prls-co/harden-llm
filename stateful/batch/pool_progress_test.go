package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-509

import (
	"testing"
	"time"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-521
func TestPoolCoalescesTransientStatusButPublishesRestrictionsImmediately(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	s := NewPoolState("pool", 2, DefaultPolicy())
	s.CoalesceTransientPublication = true
	s.PublicationAt = now
	s.PublicationReason = "dispatch_pending"
	for _, reason := range []string{"", "pool_capacity", "dispatch_pending"} {
		if s.publicationDue(now.Add(time.Millisecond), reason, nil) {
			t.Fatal("transient status flicker bypassed heartbeat coalescing", reason)
		}
		if !s.publicationDue(now.Add(5*time.Second), reason, nil) {
			t.Fatal("coalescing suppressed heartbeat", reason)
		}
	}
	for _, reason := range []string{"provider_throttled", "admission_window", "account_restricted", "unresolved_execution", "worker_unavailable", "persistence_wait", "workflow_rollover"} {
		if !s.publicationDue(now.Add(time.Millisecond), reason, nil) {
			t.Fatal("critical restriction publication delayed", reason)
		}
		s.PublicationReason = reason
		if !s.publicationDue(now.Add(time.Millisecond), "dispatch_pending", nil) {
			t.Fatal("restriction recovery publication delayed", reason)
		}
		s.PublicationReason = "dispatch_pending"
	}
	next := now.Add(time.Hour)
	if !s.publicationDue(now.Add(time.Millisecond), "dispatch_pending", &next) {
		t.Fatal("trusted eligibility change delayed")
	}
	s.CoalesceTransientPublication = false
	if !s.publicationDue(now.Add(time.Millisecond), "", nil) {
		t.Fatal("legacy publication decisions changed")
	}
}

func TestPoolWaitingProjectionUsesActualGateAndNoInventedETA(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	next := now.Add(7 * time.Second)
	for _, test := range []struct {
		name, want string
		configure  func(*PoolState)
		next       *time.Time
	}{
		{"idle", "", func(*PoolState) {}, nil},
		{"restricted", "account_restricted", func(s *PoolState) { s.Controller.Paused = true }, nil},
		{"throttled", "provider_throttled", func(s *PoolState) { s.Controller.Cooling = true; s.Controller.NextEligible = next }, &next},
		{"worker", "worker_unavailable", func(s *PoolState) { s.DispatchNeedsReady = true; s.DispatchBlockedUntil = next }, &next},
		{"metadata", "persistence_wait", func(s *PoolState) { s.PersistenceWait = true }, nil},
		{"rollover", "workflow_rollover", func(s *PoolState) { s.Quiescing = true }, nil},
		{"unknown", "unresolved_execution", func(s *PoolState) { s.Outstanding = []PoolPermit{{ID: "one", AwaitingStop: true}} }, nil},
		{"dispatch", "dispatch_pending", func(s *PoolState) { s.StartIntents = []Reservation{{PermitID: "one"}} }, nil},
		{"capacity", "pool_capacity", func(s *PoolState) { s.Controller.Policy.Concurrency = 1; s.Outstanding = []PoolPermit{{ID: "one"}} }, nil},
		{"unanchored_window", "admission_window", func(s *PoolState) { s.Controller.WindowStarts = 200 }, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := NewPoolState("pool", 2, DefaultPolicy())
			test.configure(&s)
			reason, at := s.Waiting(now)
			if reason != test.want || (at == nil) != (test.next == nil) || at != nil && !at.Equal(*test.next) {
				t.Fatal("false waiting cause or eligibility", reason, at)
			}
		})
	}
}
