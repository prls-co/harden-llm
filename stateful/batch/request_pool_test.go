package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-516

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
)

func TestPoolRequestGrantSharesBudgetWithoutBatchIdentity(t *testing.T) {
	now := time.Now().UTC()
	p := DefaultPolicy()
	p.Concurrency = 1
	s := NewPoolState("request-pool", 2, p)
	i := ReadyItem{Kind: "ordinary", RequestID: uuid.NewString(), Ref: Ref{Scope: strings.Repeat("a", 64)}, BudgetMS: 60000, Deadline: now.Add(time.Minute), ReadyAt: now}
	r, err := s.Grant(now, i)
	if err != nil || r.Kind != "ordinary" || r.RequestID != i.RequestID || r.Ref.JobID != "" || r.BudgetMS != 60000 {
		t.Fatal("ordinary grant required batch identity or lost metadata", r, err)
	}
	if s.CanGrant(now) || s.Sequence != 1 || len(s.Outstanding) != 1 || s.Controller.WindowStarts != 1 {
		t.Fatal("ordinary dispatch bypassed shared capacity or budget", s)
	}
	o := AttemptObservation{Outcome: Outcome{Kind: "never_dispatched"}, LocalStopped: true, ObservedAt: now}
	a := Attempt{Reservation: r, State: "settled", SettlementRevision: 1, Observation: &o, StoppedAt: &now}
	if !s.Observe(now, a) || len(s.Outstanding) != 0 || s.Controller.WindowStarts != 0 || s.DispatchNeedsReady {
		t.Fatal("abandoned ordinary waiter leaked capacity or pretended provider worker failed", s)
	}
}

func TestRequestStarterPublishesPermitWithoutDurableInference(t *testing.T) {
	t.Parallel()
	id := uuid.NewString()
	r := Reservation{PermitID: id, PoolID: "pool", Kind: "ordinary", RequestID: uuid.NewString(), WorkflowID: "hllm-cpa-request-v2/" + id}
	var reservations atomic.Int32
	a := &Activities{PoolID: "pool", TaskQueue: "control", Store: starterStore{reserve: func(_ context.Context, got Reservation) (Attempt, error) {
		if got != r {
			t.Error("request grant identity changed")
		}
		reservations.Add(1)
		return Attempt{Reservation: got, State: "reserved"}, nil
	}}, Temporal: starterTemporal{start: func(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error) {
		t.Error("ordinary request spawned durable provider workflow")
		return nil, ErrUnavailable
	}}, Execute: func(context.Context, string, hardenllm.Request) (hardenllm.Result, error) {
		t.Error("request starter ran inference")
		return hardenllm.Result{}, ErrUnavailable
	}}
	result, err := a.StartAttempts(context.Background(), []Reservation{r})
	if err != nil || reservations.Load() != 1 || len(result) != 1 || !result[0].Started || result[0].PermitID != id {
		t.Fatal("request grant was not published once", result, err)
	}
}

func TestPoolRequestIdentitiesRemainDistinctWithinOneScope(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	s := NewPoolState("pool", 2, DefaultPolicy())
	i := ReadyItem{Kind: "ordinary", RequestID: uuid.NewString(), Ref: Ref{Scope: strings.Repeat("a", 64)}, BudgetMS: 60000, Deadline: now.Add(time.Minute)}
	first, err := s.Grant(now, i)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grant(now, i); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate live waiter got another grant", err)
	}
	i.RequestID = uuid.NewString()
	second, err := s.Grant(now, i)
	if err != nil || first.PermitID == second.PermitID || second.Sequence != first.Sequence+1 {
		t.Fatal("independent calls in same scope collided", first, second, err)
	}
	if len(s.ReadyExclusions()) != 0 || len(s.ReadyRequestExclusions()) != 2 {
		t.Fatal("request intents treated as batch identities", s)
	}
}

func TestPoolWorkflowRetiresAppliedRequestSettlementWithoutReleasingUnknown(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	env.OnActivity(a.PublishPool, mock.Anything, mock.Anything).Return(PublicationPage{}, nil).Maybe()
	s := NewPoolState("pool", 2, DefaultPolicy())
	var attempts []Attempt
	for i := range 2 {
		now := env.Now()
		r, err := s.Grant(now, ReadyItem{Kind: "ordinary", RequestID: uuid.NewString(), Ref: Ref{Scope: strings.Repeat("a", 64)}, BudgetMS: 60000, Deadline: now.Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		s.AcknowledgeStarts([]AttemptStartResult{{PermitID: r.PermitID, Started: true}})
		o := AttemptObservation{Outcome: Outcome{Kind: "completed", FinishedAt: &now}, NativeEntered: true, LocalStopped: true, ObservedAt: now}
		attempt := Attempt{Reservation: r, State: "settled", ClaimID: uuid.NewString(), ClaimedAt: &now, DispatchedAt: &now, StoppedAt: &now, SettlementRevision: 1, Observation: &o}
		if i == 1 {
			o.Outcome.Kind = "unconfirmed"
			o.LocalStopped = false
			attempt.StoppedAt = nil
		}
		attempts = append(attempts, attempt)
	}
	env.OnActivity(a.InitializePool, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.ReadPool, mock.Anything, mock.Anything).Return(attempts, nil)
	var ackCalls atomic.Int32
	env.OnActivity(a.RetireRequests, mock.Anything, mock.Anything).Return(func(_ context.Context, r PoolRead) error {
		if len(r.IDs) != 1 || r.IDs[0] != attempts[0].PermitID {
			t.Error("unconfirmed active request was retired", r)
		}
		if ackCalls.Add(1) == 1 {
			return ErrUnavailable
		}
		return nil
	})
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("continue", true) }, 12*time.Second)
	env.ExecuteWorkflow(PoolWorkflow, s)
	continued := continuedPoolState(t, env)
	if ackCalls.Load() != 2 || len(continued.RequestRetirements) != 0 || len(continued.Outstanding) != 1 || continued.Outstanding[0].ID != attempts[1].PermitID || !continued.Outstanding[0].AwaitingStop {
		t.Fatal("continuation lost retirement/unknown local execution", ackCalls.Load(), continued)
	}
	env.AssertExpectations(t)
}

func TestPoolRequestRetirementBackpressureReportsPersistenceWait(t *testing.T) {
	t.Parallel()
	s := NewPoolState("pool", 2, DefaultPolicy())
	for range 1000 {
		s.RequestRetirements = append(s.RequestRetirements, uuid.NewString())
	}
	if s.CanGrant(time.Now()) {
		t.Fatal("unbounded unapplied retirement metadata")
	}
	if reason, next := s.Waiting(time.Now()); reason != "persistence_wait" || next != nil {
		t.Fatal("retirement backpressure omitted actual wait", reason, next)
	}
}
