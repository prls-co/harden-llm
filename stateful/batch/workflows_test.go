package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-476
import (
	"context"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
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
func TestBatchWorkflowUsesDurableJobDeadline(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivityWithOptions(a.Enqueue, activity.RegisterOptions{Name: "batch.Enqueue"})
	env.RegisterActivity(&a)
	env.OnActivity(a.Enqueue, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.Status, mock.Anything, mock.Anything).Return(Status{Counts: Counts{Total: 1, Queued: 1}}, nil)
	env.OnActivity(a.Expire, mock.Anything, mock.Anything).Return(nil).Once()
	env.ExecuteWorkflow(BatchWorkflow, JobRef{JobID: "job", Deadline: env.Now().Add(time.Minute)})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
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
