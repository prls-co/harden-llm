package livecapacity

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-468

import (
	"testing"
	"time"
)

func TestAdmissionAndSharedCooldown(t *testing.T) {
	now := time.Unix(100, 0)
	s := NewScheduler(4, now, RouteConfig{Concurrency: 2, Interval: time.Second}, 5)
	a, ok := s.Admit(now, false)
	if !ok || a.Route != "cpa" {
		t.Fatal(a, ok)
	}
	if _, ok = s.Admit(now, false); ok {
		t.Fatal("pacing bypassed")
	}
	b, ok := s.Admit(now.Add(time.Second), false)
	if !ok {
		t.Fatal("second slot")
	}
	if _, ok = s.Admit(now.Add(2*time.Second), false); ok {
		t.Fatal("concurrency bypassed")
	}
	s.Finish(a.ID, Outcome{Kind: "rejected", RetryAfter: 45 * time.Second}, now.Add(2*time.Second))
	if _, ok = s.Admit(now.Add(3*time.Second), false); ok {
		t.Fatal("shared cooldown bypassed")
	}
	// An undispatched item may overflow during a long cooldown, but the rejected
	// item stays pinned to CPA and cannot be replayed on the paid route.
	c, ok := s.Admit(now.Add(3*time.Second), true)
	if !ok || c.Route != "openai" || c.ID == a.ID {
		t.Fatal(c, ok)
	}
	s.Finish(b.ID, Outcome{Kind: "completed", UsageKnown: true}, now.Add(4*time.Second))
	if s.CPA.Concurrency != 1 || s.CPA.PauseUntil.Before(now.Add(47*time.Second)) {
		t.Fatal(s.CPA)
	}
}

func TestBudgetAndUnknownOutcome(t *testing.T) {
	now := time.Unix(100, 0)
	s := NewScheduler(3, now, RouteConfig{Concurrency: 1, Interval: time.Second}, AttemptReservation)
	s.CPA.PauseUntil = now.Add(time.Hour)
	a, ok := s.Admit(now, true)
	if !ok || a.Route != "openai" {
		t.Fatal(a, ok)
	}
	s.Finish(a.ID, Outcome{Kind: "unconfirmed"}, now.Add(time.Second))
	if _, ok = s.Admit(now.Add(2*time.Second), true); ok {
		t.Fatal("unknown reservation released")
	}
	if s.Items[a.ID].Status != "unconfirmed" {
		t.Fatal(s.Items[a.ID])
	}
	s2 := NewScheduler(2, now, RouteConfig{Concurrency: 1, Interval: time.Second}, AttemptReservation)
	s2.CPA.PauseUntil = now.Add(time.Hour)
	a, _ = s2.Admit(now, true)
	s2.Finish(a.ID, Outcome{Kind: "completed", UsageKnown: true, Cost: AttemptReservation / 2}, now)
	if _, ok = s2.Admit(now.Add(time.Second), true); ok {
		t.Fatal("settled spend ignored")
	}
}

func TestHeartbeatDeadlineStallAndCancel(t *testing.T) {
	now := time.Unix(100, 0)
	s := NewScheduler(2, now, RouteConfig{Concurrency: 1, Interval: time.Second}, 5)
	a, _ := s.Admit(now, false)
	p := s.Snapshot(now.Add(5 * time.Second))
	if p.ActiveCPA != 1 || p.Completed != 0 || p.Percent != 0 {
		t.Fatal(p)
	}
	if s.Stalled(now.Add(62*time.Second)) != "overdue_request" {
		t.Fatal("missing stall")
	}
	s.Stop("deadline")
	if _, ok := s.Admit(now.Add(time.Hour), true); ok {
		t.Fatal("dispatch after deadline")
	}
	if s.Items[a.ID].Status != "active" || s.Items[1].Status != "queued" {
		t.Fatal("stop lost state")
	}
	s.Finish(a.ID, Outcome{Kind: "unconfirmed"}, now.Add(time.Hour))
	if s.Snapshot(now.Add(time.Hour)).Unconfirmed != 1 {
		t.Fatal("cancel unknown lost")
	}
}

func TestBaselineFailureAndRetryLimit(t *testing.T) {
	now := time.Unix(100, 0)
	s := NewScheduler(1, now, RouteConfig{Concurrency: 1, Interval: time.Second}, 5)
	a, _ := s.Admit(now, false)
	s.Finish(a.ID, Outcome{Kind: "rejected"}, now)
	if !s.CPA.Disabled {
		t.Fatal("failed single-call baseline must pause route")
	}
	s.CPA.Disabled = false
	for n := 0; n < 2; n++ {
		s.CPA.Disabled = false
		a, ok := s.Admit(now.Add(time.Hour*time.Duration(n+1)), false)
		if !ok {
			t.Fatal("missing retry")
		}
		s.CPA.Disabled = false
		s.Finish(a.ID, Outcome{Kind: "rejected"}, now)
	}
	if s.Items[0].Status != "failed" {
		t.Fatal(s.Items[0])
	}
}
