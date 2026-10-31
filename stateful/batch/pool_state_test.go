package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-504

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func poolReady(index int, now time.Time) ReadyItem {
	return ReadyItem{Ref: Ref{Scope: strings.Repeat("a", 64), JobID: "fbb5a4c6-7a03-483b-9ef4-d18d489e5102", Index: index}, BudgetMS: 300000, Deadline: now.Add(time.Hour), ReadyAt: now}
}
func poolSettlement(r Reservation, now time.Time, kind string, stopped bool) Attempt {
	observed := now.Add(-time.Millisecond)
	a := Attempt{Reservation: r, State: "settled", ClaimID: uuid.NewString(), ClaimedAt: &observed, DispatchedAt: &observed, SettlementRevision: 1, Observation: &AttemptObservation{Outcome: Outcome{Kind: kind}, NativeEntered: true, LocalStopped: stopped, ObservedAt: now}}
	if stopped {
		a.StoppedAt = &now
	}
	return a
}
func TestPoolStateWindowAnchorsOnClaimAndNeverRefillsUnconfirmedStarts(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	p := DefaultPolicy()
	p.Concurrency = 10
	p.WindowRequests = 2
	s := NewPoolState("pool", 2, p)
	first, err := s.Grant(now, poolReady(0, now))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Grant(now, poolReady(1, now))
	if err != nil {
		t.Fatal(err)
	}
	if s.CanGrant(now.Add(10 * time.Minute)) {
		t.Fatal("unacknowledged starts minted another quota window")
	}
	claim := Attempt{Reservation: first, State: "dispatching", ClaimedAt: &now}
	receipt := now.Add(10 * time.Second)
	s.Observe(receipt, claim)
	if !s.WindowAnchored || !s.Controller.WindowStart.Equal(receipt) || s.Controller.WindowStarts != 2 {
		t.Fatal("first claim did not anchor reserved budget", s)
	}
	s.Observe(receipt.Add(10*time.Second), Attempt{Reservation: second, State: "dispatching", ClaimedAt: &now})
	if !s.Controller.WindowStart.Equal(receipt) {
		t.Fatal("later claims postponed refill anchor")
	}
	if s.CanGrant(receipt.Add(60*time.Second)) || !s.CanGrant(receipt.Add(61*time.Second)) {
		t.Fatal("window refill did not use confirmed anchor")
	}
}
func TestPoolStateSettlementReleasesOnceAfterPositiveStopAndKeepsUnknown(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	s := NewPoolState("pool", 2, DefaultPolicy())
	r, err := s.Grant(now, poolReady(0, now))
	if err != nil {
		t.Fatal(err)
	}
	a := poolSettlement(r, now, "unconfirmed", false)
	if !s.Observe(now, a) || len(s.Outstanding) != 1 || !s.Outstanding[0].AwaitingStop || s.CanGrant(now) {
		t.Fatal("unknown transport lifetime released capacity", s)
	}
	successes := s.Controller.Successes
	if s.Observe(now, a) || s.Controller.Successes != successes {
		t.Fatal("duplicate settlement changed admission")
	}
	stopped := now.Add(time.Second)
	a.StoppedAt = &stopped
	a.SettlementRevision++
	if !s.Observe(stopped, a) || len(s.Outstanding) != 0 {
		t.Fatal("later positive stop did not release permit", s)
	}
	if s.Observe(stopped, a) {
		t.Fatal("duplicate completion released a second permit")
	}
}
func TestPoolStateOnlyCurrentProbeCanReopenAndTerminalThrottleDoesNotRefund(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	s := NewPoolState("pool", 2, DefaultPolicy())
	old, _ := s.Grant(now, poolReady(0, now))
	refused, _ := s.Grant(now, poolReady(1, now))
	a := poolSettlement(refused, now, "unconfirmed", true)
	a.Observation.Outcome.Admission = "throttled"
	a.Observation.Outcome.RetryAfterMS = 2000
	s.Observe(now, a)
	if !s.Controller.Cooling || s.Controller.Rate != 50 || s.Controller.WindowStarts != 2 {
		t.Fatal("terminal throttle refunded or failed to cool", s.Controller)
	}
	s.Observe(now, poolSettlement(old, now, "completed", true))
	if !s.Controller.Cooling {
		t.Fatal("pre-throttle completion reopened gate")
	}
	probe, err := s.Grant(now.Add(2*time.Second), poolReady(2, now))
	if err != nil || !probe.Probe {
		t.Fatal("recovery probe not granted", err)
	}
	if s.CanGrant(now.Add(3 * time.Second)) {
		t.Fatal("more than one recovery probe admitted")
	}
	raw, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var continued PoolState
	if err := json.Unmarshal(raw, &continued); err != nil {
		t.Fatal(err)
	}
	if continued.ProbeID != probe.PermitID || !continued.Controller.Probing || len(continued.Outstanding) != 1 {
		t.Fatal("continuation discarded current probe", continued)
	}
	continued.Observe(now.Add(3*time.Second), poolSettlement(probe, now.Add(3*time.Second), "completed", true))
	if continued.Controller.Cooling || continued.Controller.BurstLeft != 0 || continued.Controller.Rate != 50 {
		t.Fatal("current probe failed to reopen at adapted pace", continued.Controller)
	}
	if !continued.Controller.Epoch.Equal(now.Add(3 * time.Second)) {
		t.Fatal("successful probe retained the prior episode's dispatch origin", continued.Controller.Epoch)
	}
}
func TestPoolStateNeverSentBackpressureAndSnapshotBound(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	p := DefaultPolicy()
	p.Concurrency = 1000
	p.Burst = 1000
	p.WindowRequests = 0
	s := NewPoolState("pool", 2, p)
	for i := range 1000 {
		if len(s.StartIntents) == maximumStartIntents {
			acks := make([]AttemptStartResult, len(s.StartIntents))
			for j, r := range s.StartIntents {
				acks[j] = AttemptStartResult{PermitID: r.PermitID, Started: true}
			}
			s.AcknowledgeStarts(acks)
		}
		if _, err := s.Grant(now, poolReady(i, now)); err != nil {
			t.Fatal(i, err)
		}
	}
	if s.CanGrant(now) {
		t.Fatal("outstanding capacity cap bypassed")
	}
	raw, err := s.Snapshot()
	if err != nil || len(raw) >= maximumPoolSnapshot {
		t.Fatal("continuation snapshot unbounded", len(raw), err)
	}
	if len(s.ReadyExclusions()) != len(s.StartIntents) {
		t.Fatal("unpersisted intents were not excluded from selection")
	}
	var restored PoolState
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Outstanding) != 1000 || restored.Sequence != 1000 {
		t.Fatal("snapshot lost permits or sequence")
	}
	small := NewPoolState("pool", 2, DefaultPolicy())
	r, _ := small.Grant(now, poolReady(0, now))
	a := poolSettlement(r, now, "never_dispatched", true)
	a.Observation.NativeEntered = false
	a.DispatchedAt = nil
	small.Observe(now, a)
	if small.Controller.WindowStarts != 0 || small.Controller.Rate != 100 || small.Controller.Cooling || !small.DispatchNeedsReady || small.CanGrant(now.Add(time.Minute)) {
		t.Fatal("worker unavailability treated as provider throttle or reopened without readiness", small)
	}
}

func TestPoolStateOldEpisodeRefusalRefundsOnlyItsCurrentWindow(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	s := NewPoolState("pool", 2, DefaultPolicy())
	throttled, _ := s.Grant(now, poolReady(0, now))
	old, _ := s.Grant(now, poolReady(1, now))
	first := poolSettlement(throttled, now, "unconfirmed", true)
	first.Observation.Outcome.Admission = "throttled"
	first.Observation.Outcome.RetryAfterMS = 2000
	s.Observe(now, first)
	probe, err := s.Grant(now.Add(2*time.Second), poolReady(2, now))
	if err != nil {
		t.Fatal(err)
	}
	s.Observe(now.Add(3*time.Second), poolSettlement(probe, now.Add(3*time.Second), "completed", true))
	before := s.Controller.WindowStarts
	s.Observe(now.Add(4*time.Second), poolSettlement(old, now.Add(4*time.Second), "rejected", true))
	if s.Controller.WindowStarts != before-1 || s.Controller.Cooling {
		t.Fatal("old episode refusal lost valid window credit or reset recovery", before, s.Controller)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-521
func TestPoolCoalescesOneUnavailableDispatchEpisode(t *testing.T) {
	t.Parallel()
	for _, coalesce := range []bool{false, true} {
		t.Run(fmt.Sprint(coalesce), func(t *testing.T) {
			now := time.Unix(1000, 0)
			s := NewPoolState("pool", 2, DefaultPolicy())
			s.CoalesceDispatchFailures = coalesce
			var reservations []Reservation
			for i := range 8 {
				r, err := s.Grant(now, poolReady(i, now))
				if err != nil {
					t.Fatal(err)
				}
				reservations = append(reservations, r)
			}
			for _, r := range reservations {
				a := poolSettlement(r, now, "never_dispatched", true)
				a.Observation.NativeEntered = false
				a.ClaimedAt = nil
				a.ClaimID = ""
				a.DispatchedAt = nil
				s.Observe(now, a)
			}
			floor, backoff := 30*time.Second, 30*time.Second
			if coalesce {
				floor, backoff = 2*time.Second, 4*time.Second
			}
			if !s.DispatchNeedsReady || !s.DispatchBlockedUntil.Equal(now.Add(floor)) || s.DispatchBackoff != backoff {
				t.Fatal("dispatch episode/version behavior changed", coalesce, s.DispatchBlockedUntil.Sub(now), s.DispatchBackoff)
			}
		})
	}
}
