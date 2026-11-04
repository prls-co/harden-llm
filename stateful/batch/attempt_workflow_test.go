package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-500

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestIndependentAttemptRetriesOnlySettlementMetadata(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.OnGetVersion("combined-settlement-notice", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	var a Activities
	env.RegisterActivity(&a)
	now := env.Now()
	input := AttemptInput{Reservation: Reservation{PermitID: "permit", PoolID: "pool", Deadline: now.Add(time.Minute), BudgetMS: 300000}, ProviderTaskQueue: "provider"}
	observation := AttemptObservation{Outcome: Outcome{Kind: "completed", ArtifactID: "attempt.json"}, NativeEntered: true, LocalStopped: true, ObservedAt: now}
	env.OnActivity(a.InvokeAttempt, mock.Anything, "permit").Return(InvocationResult{Disposition: "observed", ClaimID: "owner", Observation: &observation}, nil).Once()
	attempts := 0
	env.OnActivity(a.SettleKnownAttempt, mock.Anything, mock.Anything).Return(func(ctx context.Context, k KnownSettlement) (Attempt, error) {
		attempts++
		if k.ClaimID != "owner" || k.Observation.Outcome.ArtifactID != "attempt.json" {
			t.Error("settlement changed retained evidence")
		}
		if attempts == 1 {
			return Attempt{}, errors.New("synthetic lost metadata acknowledgement")
		}
		return Attempt{Reservation: input.Reservation, State: "settled", Observation: &observation, SettlementRevision: 1}, nil
	})
	env.OnActivity(a.NotifySettlement, mock.Anything, mock.Anything).Return(nil).Once()
	env.ExecuteWorkflow(AttemptWorkflow, input)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatal("metadata was not retried independently", attempts)
	}
	env.AssertExpectations(t)
}
func TestIndependentAttemptLostProviderReturnDoesNotReplay(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.OnGetVersion("combined-settlement-notice", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	var a Activities
	env.RegisterActivity(&a)
	input := AttemptInput{Reservation: Reservation{PermitID: "permit", PoolID: "pool", Deadline: env.Now().Add(time.Minute), BudgetMS: 300000}, ProviderTaskQueue: "provider"}
	invocations := 0
	env.OnActivity(a.InvokeAttempt, mock.Anything, "permit").Return(func(context.Context, string) (InvocationResult, error) {
		invocations++
		return InvocationResult{}, errors.New("synthetic provider activity acknowledgement lost")
	}).Once()
	reads := 0
	env.OnActivity(a.ReconcileAttempt, mock.Anything, AttemptReference{PoolID: "pool", PermitID: "permit"}).Return(func(context.Context, AttemptReference) (Attempt, error) {
		reads++
		if reads == 1 {
			return Attempt{Reservation: input.Reservation, State: "dispatching"}, nil
		}
		return Attempt{Reservation: input.Reservation, State: "settled", SettlementRevision: 1, Observation: &AttemptObservation{Outcome: Outcome{Kind: "completed", ArtifactID: "existing.json"}, LocalStopped: true}}, nil
	})
	env.OnActivity(a.NotifySettlement, mock.Anything, mock.Anything).Return(nil).Once()
	env.ExecuteWorkflow(AttemptWorkflow, input)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if invocations != 1 || reads != 2 {
		t.Fatal("lost provider result replayed or bypassed readback", invocations, reads)
	}
	env.AssertExpectations(t)
}
func TestIndependentAttemptClaimLoserWaitsForOriginalOwner(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.OnGetVersion("combined-settlement-notice", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	var a Activities
	env.RegisterActivity(&a)
	input := AttemptInput{Reservation: Reservation{PermitID: "permit", PoolID: "pool", Deadline: env.Now().Add(time.Minute), BudgetMS: 300000}, ProviderTaskQueue: "provider"}
	env.OnActivity(a.InvokeAttempt, mock.Anything, "permit").Return(InvocationResult{Disposition: "claim_owned_elsewhere"}, nil).Once()
	env.OnActivity(a.ReconcileAttempt, mock.Anything, AttemptReference{PoolID: "pool", PermitID: "permit"}).Return(Attempt{Reservation: input.Reservation, State: "settled", SettlementRevision: 1, Observation: &AttemptObservation{Outcome: Outcome{Kind: "unconfirmed"}}}, nil).Once()
	env.OnActivity(a.NotifySettlement, mock.Anything, mock.Anything).Return(nil).Once()
	env.ExecuteWorkflow(AttemptWorkflow, input)
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	env.AssertExpectations(t)
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-500
func TestIndependentAttemptSettlesWithOneMetadataActivity(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var a Activities
	env.RegisterActivity(&a)
	now := env.Now()
	input := AttemptInput{Reservation: Reservation{PermitID: "permit", PoolID: "pool", Deadline: now.Add(time.Minute), BudgetMS: 300000}, ProviderTaskQueue: "provider"}
	observation := AttemptObservation{Outcome: Outcome{Kind: "completed", ArtifactID: "attempt.json"}, NativeEntered: true, LocalStopped: true, ObservedAt: now}
	env.OnActivity(a.InvokeAttempt, mock.Anything, "permit").Return(InvocationResult{Disposition: "observed", ClaimID: "owner", Observation: &observation}, nil).Once()
	metadataCalls, notificationActivities := 0, 0
	env.OnActivity(a.FinalizeAttempt, mock.Anything, mock.Anything).Return(func(context.Context, KnownSettlement) (Attempt, error) {
		metadataCalls++
		return Attempt{Reservation: input.Reservation, State: "settled", SettlementRevision: 1, Observation: &observation}, nil
	}).Once()
	env.OnActivity(a.NotifySettlement, mock.Anything, mock.Anything).Return(func(context.Context, Attempt) error {
		notificationActivities++
		return nil
	}).Maybe()
	env.ExecuteWorkflow(AttemptWorkflow, input)
	if err := env.GetWorkflowError(); err != nil || metadataCalls != 1 || notificationActivities != 0 {
		t.Fatal("one confirmed result scheduled a second metadata activity just to notify", err, metadataCalls, notificationActivities)
	}
	env.AssertExpectations(t)
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-500
func TestIndependentCombinedFinalizationNeverReplaysProvider(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"known-metadata-retry", "lost-provider-return", "claim-owned-elsewhere"} {
		t.Run(mode, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.OnGetVersion("combined-settlement-notice", workflow.DefaultVersion, 1).Return(workflow.Version(1))
			var a Activities
			env.RegisterActivity(&a)
			now := env.Now()
			input := AttemptInput{Reservation: Reservation{PermitID: "permit", PoolID: "pool", Deadline: now.Add(time.Minute), BudgetMS: 300000}, ProviderTaskQueue: "provider"}
			observation := AttemptObservation{Outcome: Outcome{Kind: "completed", ArtifactID: "retained.json"}, NativeEntered: true, LocalStopped: true, ObservedAt: now}
			invocations, finalizations := 0, 0
			env.OnActivity(a.InvokeAttempt, mock.Anything, "permit").Return(func(context.Context, string) (InvocationResult, error) {
				invocations++
				switch mode {
				case "known-metadata-retry":
					return InvocationResult{Disposition: "observed", ClaimID: "owner", Observation: &observation}, nil
				case "lost-provider-return":
					return InvocationResult{}, errors.New("synthetic lost provider acknowledgement")
				default:
					return InvocationResult{Disposition: "claim_owned_elsewhere"}, nil
				}
			}).Once()
			env.OnActivity(a.FinalizeAttempt, mock.Anything, mock.Anything).Return(func(_ context.Context, known KnownSettlement) (Attempt, error) {
				finalizations++
				if known.Reference != (AttemptReference{PoolID: "pool", PermitID: "permit"}) {
					t.Error("metadata retry replaced the execution identity", known.Reference)
				}
				if mode == "known-metadata-retry" {
					if known.ClaimID != "owner" || known.Observation.Outcome.ArtifactID != "retained.json" || !known.Observation.ObservedAt.Equal(now) || !known.Observation.LocalStopped {
						t.Error("metadata retry changed retained evidence", known)
					}
					if finalizations == 1 {
						return Attempt{}, errors.New("synthetic SQL acknowledgement loss")
					}
				} else if known.ClaimID != "" {
					t.Error("missing or foreign invocation return manufactured ownership", known.ClaimID)
				}
				if mode == "lost-provider-return" && finalizations == 1 {
					return Attempt{Reservation: input.Reservation, State: "dispatching"}, nil
				}
				return Attempt{Reservation: input.Reservation, State: "settled", Observation: &observation, SettlementRevision: 1}, nil
			})
			env.OnActivity(a.NotifySettlement, mock.Anything, mock.Anything).Return(func(context.Context, Attempt) error {
				t.Error("new workflow scheduled a separate notification activity")
				return nil
			}).Maybe()
			env.ExecuteWorkflow(AttemptWorkflow, input)
			want := 2
			if mode == "claim-owned-elsewhere" {
				want = 1
			}
			if err := env.GetWorkflowError(); err != nil || invocations != 1 || finalizations != want {
				t.Fatal("combined metadata recovery lost evidence or replayed inference", err, invocations, finalizations)
			}
			env.AssertExpectations(t)
		})
	}
}
