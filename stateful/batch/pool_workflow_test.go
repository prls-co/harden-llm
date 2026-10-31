package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-505

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func continuedPoolState(t *testing.T, env *testsuite.TestWorkflowEnvironment) PoolState {
	t.Helper()
	var continued *workflow.ContinueAsNewError
	if !errors.As(env.GetWorkflowError(), &continued) {
		t.Fatal("pool did not continue as new", env.GetWorkflowError())
	}
	var state PoolState
	if err := converter.GetDefaultDataConverter().FromPayloads(continued.Input, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-525
func TestPoolOneWakeReadsAllSettlementsWithoutTrustingNotice(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	state := NewPoolState("pool", 2, DefaultPolicy())
	started := env.Now()
	var reservations []Reservation
	var acknowledgements []AttemptStartResult
	for i := range 2 {
		r, err := state.Grant(started, poolReady(i, started))
		if err != nil {
			t.Fatal(err)
		}
		reservations = append(reservations, r)
		acknowledgements = append(acknowledgements, AttemptStartResult{PermitID: r.PermitID, Started: true})
	}
	state.AcknowledgeStarts(acknowledgements)
	var settled atomic.Bool
	var reads atomic.Int32
	env.OnActivity(a.InitializePool, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.PublishPool, mock.Anything, mock.Anything).Return(PublicationPage{}, nil).Maybe()
	env.OnActivity(a.SelectPoolReady, mock.Anything, mock.Anything).Return(ReadyPage{}, nil).Maybe()
	env.OnActivity(a.ReadPool, mock.Anything, mock.Anything).Return(func(_ context.Context, read PoolRead) ([]Attempt, error) {
		reads.Add(1)
		ids := map[string]bool{}
		for _, id := range read.IDs {
			ids[id] = true
		}
		if len(ids) != 2 || !ids[reservations[0].PermitID] || !ids[reservations[1].PermitID] {
			t.Error("wake did not request every outstanding ledger row", read.IDs)
		}
		var out []Attempt
		for _, r := range reservations {
			if settled.Load() {
				out = append(out, poolSettlement(r, started.Add(2*time.Second), "completed", true))
			} else {
				out = append(out, Attempt{Reservation: r, State: "dispatching", ClaimedAt: &started, DispatchedAt: &started})
			}
		}
		return out, nil
	})
	env.RegisterDelayedCallback(func() {
		// Even an exaggerated revision cannot release permits without SQL proof.
		env.SignalWorkflow(settlementSignal, SettlementNotice{PermitID: reservations[0].PermitID, Revision: 999})
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		value, err := env.QueryWorkflow(stateQuery)
		var observed PoolState
		if err != nil || value.Get(&observed) != nil || len(observed.Outstanding) != 2 || observed.Sequence != 2 {
			t.Error("hint alone changed capacity or identities", err, observed)
		}
	}, 1500*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		settled.Store(true)
		env.SignalWorkflow(settlementSignal, SettlementNotice{PermitID: reservations[0].PermitID, Revision: 1})
	}, 2*time.Second)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("continue", true) }, 3*time.Second)
	env.ExecuteWorkflow(PoolWorkflow, state)
	continued := continuedPoolState(t, env)
	if reads.Load() != 3 || len(continued.Outstanding) != 0 || len(continued.StartIntents) != 0 || continued.Sequence != 2 || env.Now().Sub(started) >= 5*time.Second {
		t.Fatal("one hint lost a settlement or waited for periodic fallback", reads.Load(), continued)
	}
	env.AssertExpectations(t)
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-521
func TestPoolPendingClaimVersionPreservesRecordedLimits(t *testing.T) {
	t.Parallel()
	for _, version := range []workflow.Version{workflow.DefaultVersion, 1, 2} {
		for _, publicationVersion := range []workflow.Version{workflow.DefaultVersion, 1} {
			t.Run(fmt.Sprintf("claims-%d/publication-%d", version, publicationVersion), func(t *testing.T) {
				var suite testsuite.WorkflowTestSuite
				env := suite.NewTestWorkflowEnvironment()
				var a Activities
				env.RegisterActivity(&a)
				env.OnGetVersion("bounded-pending-claims", workflow.DefaultVersion, 2).Return(version)
				env.OnGetVersion("coalesced-pool-publication", workflow.DefaultVersion, 1).Return(publicationVersion)
				env.OnActivity(a.InitializePool, mock.Anything, PoolIdentity{PoolID: "pool", Generation: 2}).Return(nil)
				env.OnActivity(a.PublishPool, mock.Anything, mock.Anything).Return(PublicationPage{}, nil).Maybe()
				env.OnActivity(a.SelectPoolReady, mock.Anything, mock.Anything).Return(ReadyPage{}, nil).Maybe()
				env.RegisterDelayedCallback(func() { env.SignalWorkflow("continue", true) }, time.Second)
				state := NewPoolState("pool", 2, DefaultPolicy())
				state.PendingClaimLimit = 0 // Input shape retained before this feature.
				state.CoalesceTransientPublication = false
				env.ExecuteWorkflow(PoolWorkflow, state)
				continued := continuedPoolState(t, env)
				want := 0
				if version == 1 {
					want = 16
				} else if version == 2 {
					want = 32
				}
				if continued.CoalesceTransientPublication != (publicationVersion == 1) {
					t.Fatal("recorded publication behavior changed", publicationVersion, continued.CoalesceTransientPublication)
				}
				if continued.PendingClaimLimit != want {
					t.Fatal("recorded handoff policy changed", version, continued.PendingClaimLimit, want)
				}
			})
		}
	}
}
func TestPoolWorkflowContinuesWithActiveAttemptAndStartsIndependentWork(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	env.OnActivity(a.PublishPool, mock.Anything, mock.Anything).Return(PublicationPage{}, nil).Maybe()
	state := NewPoolState("pool", 2, DefaultPolicy())
	started := env.Now()
	first := poolReady(0, started)
	second := poolReady(1, started)
	env.OnActivity(a.InitializePool, mock.Anything, PoolIdentity{PoolID: "pool", Generation: 2}).Return(nil)
	selected := false
	env.OnActivity(a.SelectPoolReady, mock.Anything, mock.Anything).Return(func(ctx context.Context, q QueueSelection) (ReadyPage, error) {
		if !selected {
			selected = true
			return ReadyPage{Items: []ReadyItem{first, second}, AfterScope: first.Ref.Scope}, nil
		}
		return ReadyPage{AfterScope: q.AfterScope}, nil
	})
	var mu sync.Mutex
	attempts := map[string]Attempt{}
	starts := 0
	env.OnActivity(a.StartAttempts, mock.Anything, mock.Anything).Return(func(ctx context.Context, rs []Reservation) ([]AttemptStartResult, error) {
		mu.Lock()
		defer mu.Unlock()
		out := make([]AttemptStartResult, len(rs))
		for i, r := range rs {
			starts++
			now := env.Now()
			attempts[r.PermitID] = Attempt{Reservation: r, State: "dispatching", ClaimedAt: &now, DispatchedAt: &now}
			out[i] = AttemptStartResult{PermitID: r.PermitID, Started: true}
		}
		return out, nil
	})
	env.OnActivity(a.ReadPool, mock.Anything, mock.Anything).Return(func(context.Context, PoolRead) ([]Attempt, error) {
		mu.Lock()
		defer mu.Unlock()
		out := []Attempt{}
		for _, v := range attempts {
			out = append(out, v)
		}
		return out, nil
	})
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("continue", true) }, 10*time.Second)
	env.ExecuteWorkflow(PoolWorkflow, state)
	continued := continuedPoolState(t, env)
	if starts != 2 || len(continued.Outstanding) != 2 || len(continued.StartIntents) != 0 || !continued.WindowAnchored || continued.Sequence != 2 {
		t.Fatal("continuation waited for/lost active independent work", starts, continued)
	}
	if env.Now().Sub(started) > 15*time.Second {
		t.Fatal("pool continuation drained provider work")
	}
	env.AssertExpectations(t)
}
func TestPoolWorkflowRetainsUnknownStartsAndRestrictionsAcrossContinuation(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	env.OnActivity(a.PublishPool, mock.Anything, mock.Anything).Return(PublicationPage{}, nil).Maybe()
	p := DefaultPolicy()
	p.Concurrency = 1
	state := NewPoolState("pool", 2, p)
	now := env.Now()
	r, err := state.Grant(now, poolReady(0, now))
	if err != nil {
		t.Fatal(err)
	}
	state.Controller.Paused = true
	env.OnActivity(a.InitializePool, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.ReadPool, mock.Anything, mock.Anything).Return([]Attempt{}, nil)
	env.OnActivity(a.StartAttempts, mock.Anything, mock.Anything).Return([]AttemptStartResult{{PermitID: r.PermitID, Started: false, ErrorCode: "start_unconfirmed"}}, nil)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("continue", true) }, time.Second)
	env.ExecuteWorkflow(PoolWorkflow, state)
	continued := continuedPoolState(t, env)
	if !continued.Controller.Paused || len(continued.Outstanding) != 1 || len(continued.StartIntents) != 1 || continued.StartIntents[0].PermitID != r.PermitID || continued.Sequence != 1 {
		t.Fatal("uncertain start or account restriction was reset", continued)
	}
	env.AssertExpectations(t)
}

func TestPoolWorkflowCanContinueDuringMetadataInitializationOutage(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetTestTimeout(time.Second)
	var a Activities
	env.RegisterActivity(&a)
	env.OnActivity(a.PublishPool, mock.Anything, mock.Anything).Return(PublicationPage{}, nil).Maybe()
	state := NewPoolState("pool", 2, DefaultPolicy())
	state.Controller.Paused = true
	env.OnActivity(a.InitializePool, mock.Anything, mock.Anything).Return(ErrUnavailable)
	started := env.Now()
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("continue", true) }, time.Second)
	env.RegisterDelayedCallback(func() {
		t.Error("initialization outage ignored continuation beyond bounded metadata allowance")
		env.CancelWorkflow()
	}, 20*time.Second)
	env.ExecuteWorkflow(PoolWorkflow, state)
	continued := continuedPoolState(t, env)
	if !continued.Controller.Paused || !continued.PersistenceWait || env.Now().Sub(started) > 20*time.Second {
		t.Fatal("metadata outage prevented bounded continuation or discarded restriction", continued, env.Now().Sub(started))
	}
}

func TestPoolWorkflowWakesAtRecoveryFloorWithoutFiveSecondRounding(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	env.OnActivity(a.PublishPool, mock.Anything, mock.Anything).Return(PublicationPage{}, nil).Maybe()
	state := NewPoolState("pool", 2, DefaultPolicy())
	started := env.Now()
	state.Controller.Cooling = true
	state.Controller.NextEligible = started.Add(2 * time.Second)
	env.OnActivity(a.InitializePool, mock.Anything, mock.Anything).Return(nil)
	calls := 0
	env.OnActivity(a.SelectPoolReady, mock.Anything, mock.Anything).Return(func(context.Context, QueueSelection) (ReadyPage, error) {
		calls++
		if elapsed := env.Now().Sub(started); elapsed != 2*time.Second {
			t.Error("eligible work delayed by generic polling interval", elapsed)
		}
		return ReadyPage{}, nil
	}).Once()
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("continue", true) }, 3*time.Second)
	env.ExecuteWorkflow(PoolWorkflow, state)
	_ = continuedPoolState(t, env)
	if calls != 1 {
		t.Fatal("recovery timer did not inspect eligible work", calls)
	}
	env.AssertExpectations(t)
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-509
func TestPoolWorkflowPublishesWaitingAndHeartbeatsWithoutProviderWork(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	state := NewPoolState("pool", 2, DefaultPolicy())
	state.Controller.Paused = true
	env.OnActivity(a.InitializePool, mock.Anything, mock.Anything).Return(nil)
	calls := 0
	env.OnActivity(a.PublishPool, mock.Anything, mock.Anything).Return(func(ctx context.Context, p PoolPublication) (PublicationPage, error) {
		calls++
		if p.Reason != "account_restricted" || p.Next != nil || p.Limit != 128 {
			t.Error("publication invented wait/progress or unbounded page", p)
		}
		if calls == 1 {
			return PublicationPage{After: 17, More: true, Updated: 128}, nil
		}
		if calls == 2 && p.After != 17 {
			t.Error("publication lost page cursor", p)
		}
		return PublicationPage{After: p.After, Updated: 1}, nil
	})
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("continue", true) }, 6*time.Second)
	env.ExecuteWorkflow(PoolWorkflow, state)
	continued := continuedPoolState(t, env)
	if calls != 3 || !continued.Controller.Paused {
		t.Fatal("waiting/heartbeat publication missing or coupled to provider work", calls, continued)
	}
	env.AssertExpectations(t)
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-505
func TestPoolPolicyResultStaysPausedAcrossIdleAndContinuation(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	first := suite.NewTestWorkflowEnvironment()
	var a Activities
	first.RegisterActivity(&a)
	state := NewPoolState("pool", 2, DefaultPolicy())
	started := first.Now()
	r, err := state.Grant(started, poolReady(0, started))
	if err != nil {
		t.Fatal(err)
	}
	state.AcknowledgeStarts([]AttemptStartResult{{PermitID: r.PermitID, Started: true}})
	first.OnActivity(a.InitializePool, mock.Anything, mock.Anything).Return(nil)
	first.OnActivity(a.PublishPool, mock.Anything, mock.Anything).Return(PublicationPage{}, nil).Maybe()
	first.OnActivity(a.SelectPoolReady, mock.Anything, mock.Anything).Return(ReadyPage{}, nil).Maybe()
	first.OnActivity(a.ReadPool, mock.Anything, mock.Anything).Return([]Attempt{poolSettlement(r, started, "policy", true)}, nil).Once()
	first.RegisterDelayedCallback(func() {
		value, err := first.QueryWorkflow(stateQuery)
		var current PoolState
		if err != nil || value.Get(&current) != nil || !current.Controller.Paused || len(current.Outstanding) != 0 {
			t.Error("policy result did not pause the now-idle account", err, current)
		}
	}, time.Second)
	first.RegisterDelayedCallback(func() { first.SignalWorkflow("continue", true) }, 6*time.Second)
	first.ExecuteWorkflow(PoolWorkflow, state)
	continued := continuedPoolState(t, first)
	if !continued.Controller.Paused || continued.Sequence != 1 || len(continued.Outstanding) != 0 || continued.CanGrant(first.Now()) {
		t.Fatal("idle continuation discarded the actual account policy result", continued)
	}
	first.AssertExpectations(t)

	second := suite.NewTestWorkflowEnvironment()
	second.RegisterActivity(&a)
	second.OnActivity(a.InitializePool, mock.Anything, mock.Anything).Return(nil)
	second.OnActivity(a.PublishPool, mock.Anything, mock.Anything).Return(func(_ context.Context, p PoolPublication) (PublicationPage, error) {
		if p.Reason != "account_restricted" || p.Next != nil {
			t.Error("resumed idle account lost its restriction projection", p)
		}
		return PublicationPage{}, nil
	}).Maybe()
	// Selecting/starting work has no mock: any admission while paused fails.
	second.RegisterDelayedCallback(func() { second.SignalWorkflow("continue", true) }, 6*time.Second)
	second.ExecuteWorkflow(PoolWorkflow, continued)
	retained := continuedPoolState(t, second)
	if !retained.Controller.Paused || retained.Sequence != 1 || retained.PolicyRevision != state.PolicyRevision || len(retained.Outstanding) != 0 {
		t.Fatal("account restriction was reset on the next workflow run", retained)
	}
	second.AssertExpectations(t)
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-505
func TestPoolPolicyChangesPreserveActivePermitsAndRestrictions(t *testing.T) {
	t.Parallel()
	for _, restricted := range []bool{false, true} {
		t.Run(fmt.Sprintf("restricted-%t", restricted), func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			var a Activities
			env.RegisterActivity(&a)
			policy := DefaultPolicy()
			policy.Concurrency, policy.Burst = 2, 2
			state := NewPoolState("pool", 2, policy)
			started := env.Now()
			var original []Reservation
			var acknowledgements []AttemptStartResult
			for i := range 2 {
				r, err := state.Grant(started, poolReady(i, started))
				if err != nil {
					t.Fatal(err)
				}
				original = append(original, r)
				acknowledgements = append(acknowledgements, AttemptStartResult{PermitID: r.PermitID, Started: true})
			}
			state.AcknowledgeStarts(acknowledgements)
			if restricted {
				state.Controller.Paused, state.Controller.Cooling = true, true
				state.Controller.NextEligible = started.Add(time.Minute)
			}
			env.OnActivity(a.InitializePool, mock.Anything, mock.Anything).Return(nil)
			env.OnActivity(a.PublishPool, mock.Anything, mock.Anything).Return(PublicationPage{}, nil).Maybe()
			env.OnActivity(a.ReadPool, mock.Anything, mock.Anything).Return([]Attempt{
				{Reservation: original[0], State: "dispatching", ClaimedAt: &started, DispatchedAt: &started},
				{Reservation: original[1], State: "dispatching", ClaimedAt: &started, DispatchedAt: &started},
			}, nil).Once()
			if !restricted {
				env.OnActivity(a.SelectPoolReady, mock.Anything, mock.Anything).Return(func(context.Context, QueueSelection) (ReadyPage, error) {
					if env.Now().Sub(started) < 2*time.Second {
						t.Error("smaller cap admitted new work before active count could fit")
					}
					candidate := poolReady(2, started)
					candidate.BudgetMS = 450000
					return ReadyPage{Items: []ReadyItem{candidate}}, nil
				}).Once()
				env.OnActivity(a.StartAttempts, mock.Anything, mock.Anything).Return(func(_ context.Context, rs []Reservation) ([]AttemptStartResult, error) {
					if len(rs) != 1 || rs[0].PolicyRevision != 3 || rs[0].BudgetMS != 450000 || !rs[0].Deadline.Equal(started.Add(time.Hour)) {
						t.Error("new cap lost admission policy revision or accepted budget", rs)
					}
					return []AttemptStartResult{{PermitID: rs[0].PermitID, Started: true}}, nil
				}).Once()
			}
			check := func(capacity, count int, revision int64) {
				value, err := env.QueryWorkflow(stateQuery)
				var current PoolState
				if err != nil || value.Get(&current) != nil {
					t.Fatal("policy state unavailable", err)
				}
				if current.Controller.Policy.Concurrency != capacity || current.PolicyRevision != revision || len(current.Outstanding) != count || current.Controller.WindowStarts != count || current.Controller.BurstLeft != 0 {
					t.Error("policy change reset capacity, reservations or start budget", current)
				}
				for _, want := range original {
					found := false
					for _, permit := range current.Outstanding {
						if permit.ID == want.PermitID {
							found = true
							// The actor retains IDs; immutable accepted budgets live in SQL
							// and are qualified independently by TEST-497.
							if !permit.Claimed || permit.Applied || permit.AwaitingStop {
								t.Error("policy update changed active claim/settlement ownership", permit)
							}
						}
					}
					if !found {
						t.Error("active permit was revoked by cap shrink", want.PermitID)
					}
				}
				if restricted && (!current.Controller.Paused || !current.Controller.Cooling || !current.Controller.NextEligible.Equal(started.Add(time.Minute))) {
					t.Error("policy update erased an account restriction or recovery floor", current.Controller)
				}
			}
			env.RegisterDelayedCallback(func() {
				smaller := policy
				smaller.Concurrency = 1
				env.SignalWorkflow("policy", smaller)
			}, time.Second)
			env.RegisterDelayedCallback(func() { check(1, 2, 2) }, 1500*time.Millisecond)
			env.RegisterDelayedCallback(func() {
				larger := policy
				larger.Concurrency = 3
				env.SignalWorkflow("policy", larger)
			}, 2*time.Second)
			want := 3
			if restricted {
				want = 2
			}
			env.RegisterDelayedCallback(func() { check(3, want, 3) }, 2500*time.Millisecond)
			env.RegisterDelayedCallback(func() { env.SignalWorkflow("continue", true) }, 3*time.Second)
			env.ExecuteWorkflow(PoolWorkflow, state)
			continued := continuedPoolState(t, env)
			if len(continued.Outstanding) != want || continued.Controller.Policy.Concurrency != 3 || continued.Controller.Paused != restricted {
				t.Fatal("continuation lost accepted active work or changed policy", continued)
			}
			env.AssertExpectations(t)
		})
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-529
func TestPoolRecoveryJitterVersionKeepsLegacyDeadlineAndRecordedChoice(t *testing.T) {
	t.Parallel()
	for _, version := range []workflow.Version{workflow.DefaultVersion, 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			at := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
			env.SetStartTime(at)
			env.OnGetVersion("recovery-jitter-padding", workflow.DefaultVersion, 1).Return(version)
			var a Activities
			env.RegisterActivity(&a)
			policy := DefaultPolicy()
			policy.RecoveryJitterMS, policy.Concurrency = 1000, 1
			state := NewPoolState("pool", 2, policy)
			state.RecoveryJitterVersion = 0 // Retained input predates this feature.
			r, err := state.Grant(at, poolReady(0, at))
			if err != nil {
				t.Fatal(err)
			}
			state.AcknowledgeStarts([]AttemptStartResult{{PermitID: r.PermitID, Started: true}})
			env.OnActivity(a.InitializePool, mock.Anything, mock.Anything).Return(nil)
			env.OnActivity(a.PublishPool, mock.Anything, mock.Anything).Return(PublicationPage{}, nil).Maybe()
			env.OnActivity(a.ReadPool, mock.Anything, mock.Anything).Return([]Attempt{jitterRefusal(r, at, 2000)}, nil).Once()
			env.RegisterDelayedCallback(func() { env.SignalWorkflow("continue", true) }, time.Second)
			env.ExecuteWorkflow(PoolWorkflow, state)
			carried := continuedPoolState(t, env)
			want := at.Add(2 * time.Second)
			if version == 1 {
				want = want.Add(745 * time.Millisecond)
			}
			if !carried.Controller.NextEligible.Equal(want) || (carried.RecoveryPadding != nil) != (version == 1) || carried.RecoveryJitterVersion != max(0, int(version)) {
				t.Fatal("history gate changed the recovery deadline or dropped its chosen padding", carried.Controller.NextEligible, carried.RecoveryPadding, version)
			}
			env.AssertExpectations(t)
		})
	}
}
