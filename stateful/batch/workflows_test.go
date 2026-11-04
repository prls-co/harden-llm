package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-476
import (
	"context"
	"errors"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatcherQueuesAndRetriesOnlyExplicitThrottle(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	var generated, recorded atomic.Int32
	env.OnActivity(a.Generate, mock.Anything, mock.Anything).Return(func(_ context.Context, r Ref) (Outcome, error) {
		if generated.Add(1) == 1 {
			return Outcome{Kind: "rejected", RetryAfterMS: 7000}, nil
		}
		return Outcome{Kind: "completed", ArtifactID: "result.json"}, nil
	})
	env.OnActivity(a.Record, mock.Anything, mock.Anything, mock.Anything).Return(func(_ context.Context, _ Ref, o Outcome) (Counts, error) {
		if o.Kind == "completed" {
			recorded.Add(1)
		}
		return Counts{Total: 2, Succeeded: int(recorded.Load())}, nil
	})
	env.OnActivity(a.Cooldown, mock.Anything, mock.Anything).Return(nil)
	state := DispatcherState{Controller: NewController(DefaultPolicy()), Jobs: []PendingJob{{Job: JobRef{Scope: "fixture", JobID: "job", Count: 2, Deadline: env.Now().Add(time.Hour)}, Ready: []int{0, 1}}}}
	env.ExecuteWorkflow(DispatcherWorkflow, state)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if generated.Load() != 3 || recorded.Load() != 2 {
		t.Fatalf("generated=%d recorded=%d", generated.Load(), recorded.Load())
	}
	env.AssertExpectations(t)
}

func TestDispatcherDefersIndependentWorkAfterFailedAttemptHint(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	started := env.Now()
	first := Ref{Scope: "fixture", JobID: "job", Index: 0}
	second := Ref{Scope: "fixture", JobID: "job", Index: 1}
	env.OnActivity(a.Generate, mock.Anything, first).Return(Outcome{Kind: "failed", RetryAfterMS: 7000}, nil).Once()
	env.OnActivity(a.Generate, mock.Anything, second).Return(func(context.Context, Ref) (Outcome, error) {
		if env.Now().Before(started.Add(7 * time.Second)) {
			t.Error("independent work ignored the failed attempt's retry hint")
		}
		return Outcome{Kind: "completed", ArtifactID: "result.json"}, nil
	}).Once()
	var failures, successes int
	env.OnActivity(a.Record, mock.Anything, mock.Anything, mock.Anything).Return(func(_ context.Context, _ Ref, o Outcome) (Counts, error) {
		if o.Kind == "failed" {
			failures++
		} else if o.Kind == "completed" {
			successes++
		}
		return Counts{Total: 2, Failed: failures, Succeeded: successes}, nil
	})
	env.OnActivity(a.Cooldown, mock.Anything, mock.Anything).Return(nil)
	p := DefaultPolicy()
	p.Concurrency = 1
	state := DispatcherState{Controller: NewController(p), Jobs: []PendingJob{{Job: JobRef{Scope: "fixture", JobID: "job", Count: 2, Deadline: started.Add(time.Hour)}, Ready: []int{0, 1}}}}
	env.ExecuteWorkflow(DispatcherWorkflow, state)
	if err := env.GetWorkflowError(); err != nil || failures != 1 || successes != 1 {
		t.Fatalf("failed item was replayed or independent work lost: failures=%d successes=%d error=%v", failures, successes, err)
	}
	env.AssertExpectations(t)
}
func TestBatchWorkflowUsesDurableJobDeadline(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivityWithOptions(a.Enqueue, activity.RegisterOptions{Name: "batch.Enqueue"})
	env.RegisterActivity(&a)
	env.OnActivity(a.Enqueue, mock.Anything, mock.Anything).Return(nil)
	expired := false
	env.OnActivity(a.Status, mock.Anything, mock.Anything).Return(func(context.Context, JobRef) (Status, error) {
		if expired {
			return Status{Counts: Counts{Total: 1, Failed: 1}}, nil
		}
		return Status{Counts: Counts{Total: 1, Queued: 1}}, nil
	})
	env.OnActivity(a.Expire, mock.Anything, mock.Anything).Return(func(context.Context, JobRef) error { expired = true; return nil }).Once()
	env.ExecuteWorkflow(BatchWorkflow, JobRef{JobID: "job", Deadline: env.Now().Add(time.Minute)})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if !expired {
		t.Fatal("durable deadline did not expire queued work")
	}
	env.AssertExpectations(t)
}
func TestDispatcherFairnessAndWorkConservingQueue(t *testing.T) {
	s := DispatcherState{Jobs: []PendingJob{{Job: JobRef{JobID: "a"}, Ready: []int{0, 1, 2}}, {Job: JobRef{JobID: "b"}, Ready: []int{0, 1}}}}
	for _, want := range []string{"a", "b", "a", "b", "a"} {
		r, ok := s.PopReady()
		if !ok || r.JobID != want {
			t.Fatalf("expected %s, got %+v", want, r)
		}
	}
	if _, ok := s.PopReady(); ok {
		t.Fatal("empty queue admitted work")
	}
}

func TestDispatcherPublishesHeartbeatWithoutWritingForEverySuccess(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	var recorded, publications atomic.Int32
	for i := range 32 {
		env.OnActivity(a.Generate, mock.Anything, Ref{Scope: "fixture", JobID: "job", Index: i}).Return(Outcome{Kind: "completed", ArtifactID: "result.json"}, nil).After(time.Duration(i+1) * 100 * time.Millisecond)
	}
	env.OnActivity(a.Record, mock.Anything, mock.Anything, mock.Anything).Return(func(context.Context, Ref, Outcome) (Counts, error) {
		return Counts{Total: 32, Succeeded: int(recorded.Add(1))}, nil
	})
	env.OnActivity(a.Cooldown, mock.Anything, mock.Anything).Return(func(context.Context, CooldownUpdate) error {
		publications.Add(1)
		return nil
	})
	ready := make([]int, 32)
	for i := range ready {
		ready[i] = i
	}
	env.ExecuteWorkflow(DispatcherWorkflow, DispatcherState{Controller: NewController(DefaultPolicy()), Jobs: []PendingJob{{Job: JobRef{Scope: "fixture", JobID: "job", Count: 32, Deadline: env.Now().Add(time.Hour)}, Ready: ready}}})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if recorded.Load() != 32 || publications.Load() > 4 {
		t.Fatalf("success path should coalesce metadata writes: results=%d publications=%d", recorded.Load(), publications.Load())
	}
}

func TestDispatcherRetainsAccountPauseAcrossIdleAndNewJobs(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	var calls atomic.Int32
	env.OnActivity(a.Generate, mock.Anything, mock.Anything).Return(func(context.Context, Ref) (Outcome, error) {
		calls.Add(1)
		return Outcome{Kind: "completed", ArtifactID: "result.json"}, nil
	})
	env.OnActivity(a.Record, mock.Anything, mock.Anything, mock.Anything).Return(Counts{Total: 1, Succeeded: 1}, nil)
	env.OnActivity(a.Cooldown, mock.Anything, mock.Anything).Return(nil)
	initial := NewController(DefaultPolicy())
	initial.Paused = true
	job := JobRef{Scope: "fixture", JobID: "late", Count: 1, Deadline: env.Now().Add(2 * time.Hour)}
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(enqueueSignal, job) }, 35*time.Minute)
	env.RegisterDelayedCallback(func() {
		if calls.Load() != 0 {
			t.Error("an idle account restriction was bypassed")
		}
		env.SignalWorkflow("resume", true)
	}, 36*time.Minute)
	env.ExecuteWorkflow(DispatcherWorkflow, DispatcherState{Controller: initial})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("account pause was discarded when the dispatcher became idle")
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-496
func TestDispatcherAllowsLongGenerationOnceWithinBudget(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	start := env.Now()
	generated := 0
	var settled time.Time
	env.OnActivity(a.Generate, mock.Anything, mock.Anything).Return(func(context.Context, Ref) (Outcome, error) {
		generated++
		return Outcome{Kind: "completed", ArtifactID: "one.json"}, nil
	}).After(190 * time.Second).Once()
	env.OnActivity(a.Record, mock.Anything, mock.Anything, mock.Anything).Return(func(context.Context, Ref, Outcome) (Counts, error) {
		settled = env.Now()
		return Counts{Total: 1, Succeeded: 1}, nil
	}).Once()
	env.OnActivity(a.Cooldown, mock.Anything, mock.Anything).Return(nil)
	state := DispatcherState{Controller: NewController(DefaultPolicy()), Jobs: []PendingJob{{Job: JobRef{Scope: "fixture", JobID: "long", Count: 1, Deadline: start.Add(time.Hour)}, Ready: []int{0}}}}
	env.ExecuteWorkflow(DispatcherWorkflow, state)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if generated != 1 || settled.Before(start.Add(190*time.Second)) || settled.After(start.Add(300*time.Second)) {
		t.Fatal("long generation failed or was replayed", generated, settled.Sub(start))
	}
	env.AssertExpectations(t)
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-511
func TestDispatcherDrainsOwnedExecutionBeforeV2Handoff(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	stable := "hllm-cpa-pool-v1/pool"
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: stable})
	p := DefaultPolicy()
	p.Concurrency = 1
	state := DispatcherState{Controller: NewController(p), Jobs: []PendingJob{{Job: JobRef{Scope: "fixture", JobID: "job", Count: 2, Deadline: env.Now().Add(time.Hour)}, Ready: []int{0, 1}}}}
	env.OnActivity(a.Generate, mock.Anything, Ref{Scope: "fixture", JobID: "job", Index: 0}).Return(Outcome{Kind: "completed", ArtifactID: "one.json"}, nil).After(10 * time.Second).Once()
	env.OnActivity(a.Record, mock.Anything, mock.Anything, mock.Anything).Return(Counts{Total: 2, Succeeded: 1, Queued: 1}, nil).Once()
	env.OnActivity(a.Cooldown, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.PrepareLegacyJobs, mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity(a.AdvanceLegacyPool, mock.Anything, mock.Anything).Return(PoolFence{PoolID: "pool", Generation: 2, Sequence: 7}, nil).Once()
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(legacyUpgradeSignal, true) }, time.Second)
	env.RegisterDelayedCallback(func() { t.Error("handoff did not finish within bounded drain"); env.CancelWorkflow() }, 20*time.Second)
	env.ExecuteWorkflow(DispatcherWorkflow, state)
	var continued *workflow.ContinueAsNewError
	if !errors.As(env.GetWorkflowError(), &continued) || continued.WorkflowType.Name != PoolWorkflowName {
		t.Fatal("dispatcher did not hand off stable authority", env.GetWorkflowError())
	}
	var next PoolState
	if err := converter.GetDefaultDataConverter().FromPayloads(continued.Input, &next); err != nil {
		t.Fatal(err)
	}
	if next.Sequence != 7 || next.Generation != 2 || next.PoolID != "pool" || next.Controller.WindowStarts != 1 || len(next.Outstanding) != 0 {
		t.Fatal("handoff reset quota/sequence or retained owned execution", next)
	}
	if env.Now().Sub(state.Jobs[0].Job.Deadline.Add(-time.Hour)) < 10*time.Second {
		t.Fatal("handoff did not wait for owned provider return")
	}
	env.AssertExpectations(t)
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-514
func TestBatchWorkflowWaitsForClaimedFinalizationWithoutReplayingInference(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	start := env.Now()
	env.OnActivity(a.Enqueue, mock.Anything, mock.Anything).Return(nil)
	expired := false
	env.OnActivity(a.Status, mock.Anything, mock.Anything).Return(func(context.Context, JobRef) (Status, error) {
		if expired && env.Now().After(start.Add(15*time.Second)) {
			return Status{Counts: Counts{Total: 1, Failed: 1}}, nil
		}
		return Status{Counts: Counts{Total: 1, Active: 1}}, nil
	})
	env.OnActivity(a.Expire, mock.Anything, mock.Anything).Return(func(context.Context, JobRef) error { expired = true; return nil }).Once()
	env.ExecuteWorkflow(BatchWorkflow, JobRef{Deadline: start.Add(10 * time.Second)})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if !expired || env.Now().Sub(start) < 15*time.Second {
		t.Fatal("job workflow ended before owned finalization")
	}
	env.AssertExpectations(t)
}
