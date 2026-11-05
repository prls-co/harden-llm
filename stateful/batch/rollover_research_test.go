package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-476
// This characterizes the current rollover boundary, not uninterrupted admission.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestDispatcherRolloverRetainsQueuedWorkAndWaitsForSlowActivity(t *testing.T) {
	for _, reason := range []string{"history_length", "server_suggestion"} {
		t.Run(reason, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			var a Activities
			env.RegisterActivity(&a)
			start := env.Now()
			p := DefaultPolicy()
			p.Concurrency = 2
			state := DispatcherState{Controller: NewController(p), Jobs: []PendingJob{{Job: JobRef{Scope: "fixture", JobID: "job", Count: 3, Deadline: start.Add(time.Hour)}, Ready: []int{0, 1, 2}}}}
			for i, delay := range []time.Duration{120 * time.Second, time.Second} {
				env.OnActivity(a.Generate, mock.Anything, Ref{Scope: "fixture", JobID: "job", Index: i}).Return(Outcome{Kind: "completed", ArtifactID: "synthetic"}, nil).After(delay).Once()
			}
			var recorded int
			env.OnActivity(a.Record, mock.Anything, mock.Anything, mock.Anything).Return(func(context.Context, Ref, Outcome) (Counts, error) {
				recorded++
				return Counts{Total: 3, Succeeded: recorded}, nil
			})
			env.OnActivity(a.Cooldown, mock.Anything, mock.Anything).Return(nil)
			env.RegisterDelayedCallback(func() {
				if reason == "history_length" {
					env.SetCurrentHistoryLength(10001)
				} else {
					env.SetContinueAsNewSuggested(true)
				}
			}, 500*time.Millisecond)
			env.ExecuteWorkflow(DispatcherWorkflow, state)
			var next *workflow.ContinueAsNewError
			if !errors.As(env.GetWorkflowError(), &next) {
				t.Fatal("expected durable continuation", env.GetWorkflowError())
			}
			var carried DispatcherState
			if err := converter.GetDefaultDataConverter().FromPayloads(next.Input, &carried); err != nil {
				t.Fatal(err)
			}
			if env.Now().Sub(start) != 120*time.Second || recorded != 2 || len(carried.Jobs) != 1 || carried.Jobs[0].Active != 0 || len(carried.Jobs[0].Ready) != 1 || carried.Jobs[0].Ready[0] != 2 {
				t.Fatalf("lost work or unexpected drain timing: elapsed=%s state=%+v", env.Now().Sub(start), carried)
			}
			env.AssertExpectations(t)
			continued := suite.NewTestWorkflowEnvironment()
			continued.SetStartTime(env.Now())
			continued.RegisterActivity(&a)
			continued.OnActivity(a.Generate, mock.Anything, Ref{Scope: "fixture", JobID: "job", Index: 2}).Return(Outcome{Kind: "completed", ArtifactID: "synthetic"}, nil).Once()
			continued.OnActivity(a.Record, mock.Anything, mock.Anything, mock.Anything).Return(Counts{Total: 3, Succeeded: 3}, nil).Once()
			continued.OnActivity(a.Cooldown, mock.Anything, mock.Anything).Return(nil)
			continued.ExecuteWorkflow(DispatcherWorkflow, carried)
			if err := continued.GetWorkflowError(); err != nil {
				t.Fatal("queued item did not finish after rollover", err)
			}
			continued.AssertExpectations(t)
			t.Logf("slow_activity_ms=120000 spare_slot_blocked_ms=119000 queued_items_carried=1 duplicate_generations=0 trigger=%s", reason)
		})
	}
}
