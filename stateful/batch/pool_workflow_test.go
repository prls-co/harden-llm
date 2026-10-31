package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-505

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
