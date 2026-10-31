package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-529

import (
	"encoding/json"
	"testing"
	"time"
)

func jitterFixture(t *testing.T, maximum int) (PoolState, []Reservation, time.Time) {
	t.Helper()
	at := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	policy := DefaultPolicy()
	policy.RecoveryJitterMS = maximum
	state := NewPoolState("pool", 2, policy)
	var reservations []Reservation
	for i := range 3 {
		r, err := state.Grant(at, poolReady(i, at))
		if err != nil {
			t.Fatal(err)
		}
		reservations = append(reservations, r)
	}
	return state, reservations, at
}

func jitterRefusal(r Reservation, at time.Time, wait int64) Attempt {
	a := poolSettlement(r, at, "rejected", true)
	a.Observation.Outcome.RetryAfterMS = wait
	return a
}

func TestRecoveryJitterPadsFloorOnceAndSurvivesContinuation(t *testing.T) {
	t.Parallel()
	state, rs, at := jitterFixture(t, 1000)
	state.Observe(at, jitterRefusal(rs[0], at, 2000))
	floor := at.Add(2 * time.Second)
	// Immutable version-one SHA-256 vector: pool / episode=2 / empty probe /
	// policy revision=1 / UTC floor=2026-10-31T00:00:02Z -> 745 ms.
	want := floor.Add(745 * time.Millisecond)
	if state.RecoveryPadding == nil || !state.RecoveryPadding.Floor.Equal(floor) || !state.Controller.NextEligible.Equal(want) || state.CanGrant(floor) || !state.CanGrant(want) {
		t.Fatal("recovery did not pad the trusted floor with the fixed version-one choice", state.RecoveryPadding, state.Controller.NextEligible)
	}
	state.Observe(at.Add(100*time.Millisecond), jitterRefusal(rs[1], at.Add(100*time.Millisecond), 2000))
	if !state.Controller.NextEligible.Equal(want) {
		t.Fatal("another rejection cumulatively padded an already scheduled deadline", state.Controller.NextEligible, want)
	}
	raw, err := state.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var carried PoolState
	if err := json.Unmarshal(raw, &carried); err != nil {
		t.Fatal(err)
	}
	if !carried.Controller.NextEligible.Equal(want) || carried.RecoveryPadding == nil || !carried.RecoveryPadding.Until.Equal(want) {
		t.Fatal("continuation redrew or lost selected padding", carried)
	}
	// Changing the setting must not shorten/redraw the selected deadline.
	policy := carried.Controller.Policy
	policy.RecoveryJitterMS = 0
	if err := carried.Controller.Configure(policy); err != nil || !carried.Controller.NextEligible.Equal(want) {
		t.Fatal("policy change reset an accepted recovery deadline", err)
	}
	if _, err := carried.Snapshot(); err != nil {
		t.Fatal("policy change invalidated the retained recovery choice", err)
	}
	carried.Observe(at.Add(time.Second), jitterRefusal(rs[2], at.Add(time.Second), 3000))
	if !carried.Controller.NextEligible.Equal(at.Add(4*time.Second)) || carried.RecoveryPadding != nil {
		t.Fatal("disabled padding modified a new trusted deadline", carried.Controller.NextEligible, carried.RecoveryPadding)
	}
}

func TestRecoveryJitterDefaultAndLegacyKeepExactUnpaddedFloor(t *testing.T) {
	t.Parallel()
	for _, legacy := range []bool{false, true} {
		maximum := 0
		if legacy {
			maximum = 1000
		}
		state, rs, at := jitterFixture(t, maximum)
		if legacy {
			state.RecoveryJitterVersion = 0
		}
		state.Observe(at, jitterRefusal(rs[0], at, 2000))
		if state.RecoveryPadding != nil || !state.Controller.NextEligible.Equal(at.Add(2*time.Second)) {
			t.Fatal("default/retained histories gained admission delay", legacy, state.Controller.NextEligible)
		}
	}
}

func TestRecoveryJitterBoundsNeverShortenLongHintsOrDelayNormalDispatch(t *testing.T) {
	t.Parallel()
	state, rs, at := jitterFixture(t, 1000)
	// Grants are still immediate; optional jitter only affects recovery.
	if !rs[0].GrantedAt.Equal(at) || !rs[1].GrantedAt.Equal(at) || !rs[2].GrantedAt.Equal(at) {
		t.Fatal("normal native dispatch acquired a jitter delay")
	}
	state.Observe(at, jitterRefusal(rs[0], at, 120000))
	if state.RecoveryPadding == nil || state.Controller.NextEligible.Before(at.Add(120*time.Second)) || state.Controller.NextEligible.After(at.Add(121*time.Second)) {
		t.Fatal("jitter shortened/capped a long trusted server wait", state.Controller.NextEligible)
	}
	probeAt := state.Controller.NextEligible
	probe, err := state.Grant(probeAt, poolReady(3, probeAt))
	if err != nil || !probe.Probe || state.CanGrant(probeAt) {
		t.Fatal("padding changed single-probe admission", err, probe)
	}
	state.Observe(probeAt, poolSettlement(probe, probeAt, "completed", true))
	if state.RecoveryPadding != nil || state.Controller.Cooling || !state.Controller.NextEligible.IsZero() {
		t.Fatal("successful probe retained recovery padding", state.RecoveryPadding, state.Controller)
	}
}

func TestRecoveryJitterPolicyRejectsOutOfRangePadding(t *testing.T) {
	t.Parallel()
	for _, n := range []int{-1, 0, 1000, 1001} {
		p := DefaultPolicy()
		p.RecoveryJitterMS = n
		if err := p.Validate(); (err != nil) != (n < 0 || n > 1000) {
			t.Error("recovery padding policy bound incorrect", n, err)
		}
	}
}

func TestRecoveryJitterPreservesFallbackFloorAndRejectsInvalidSnapshots(t *testing.T) {
	t.Parallel()
	state, rs, at := jitterFixture(t, 1000)
	state.Observe(at, Attempt{Reservation: rs[0], State: "dispatching", ClaimedAt: &at})
	state.Observe(at.Add(time.Second), jitterRefusal(rs[0], at.Add(time.Second), 0))
	if state.RecoveryPadding == nil || !state.RecoveryPadding.Floor.Equal(at.Add(61*time.Second)) || state.Controller.NextEligible.Before(at.Add(61*time.Second)) || state.Controller.NextEligible.After(at.Add(62*time.Second)) {
		t.Fatal("fallback estimate/backoff floor was reset or shortened", state.RecoveryPadding)
	}
	for name, corrupt := range map[string]func(*PoolState){
		"unknown-version":    func(s *PoolState) { s.RecoveryJitterVersion = 2 },
		"negative-policy":    func(s *PoolState) { s.Controller.Policy.RecoveryJitterMS = -1 },
		"oversized-policy":   func(s *PoolState) { s.Controller.Policy.RecoveryJitterMS = 1001 },
		"missing-floor":      func(s *PoolState) { s.RecoveryPadding.Floor = time.Time{} },
		"before-floor":       func(s *PoolState) { s.RecoveryPadding.Until = s.RecoveryPadding.Floor.Add(-time.Millisecond) },
		"oversized-padding":  func(s *PoolState) { s.RecoveryPadding.Until = s.RecoveryPadding.Floor.Add(1001 * time.Millisecond) },
		"not-cooling":        func(s *PoolState) { s.Controller.Cooling = false },
		"different-deadline": func(s *PoolState) { s.Controller.NextEligible = s.Controller.NextEligible.Add(time.Millisecond) },
	} {
		t.Run(name, func(t *testing.T) {
			s, items, now := jitterFixture(t, 1000)
			s.Observe(now, jitterRefusal(items[0], now, 2000))
			corrupt(&s)
			if _, err := s.Snapshot(); err == nil {
				t.Fatal("corrupt recovery choice could continue", name)
			}
		})
	}
}
