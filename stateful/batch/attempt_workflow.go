package batch

import (
	"context"
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const AttemptWorkflowName = "HLLMCPAAttemptV2"
const settlementSignal = "attempt-settled"

type AttemptInput struct {
	Reservation       Reservation
	ProviderTaskQueue string
}
type AttemptReference struct{ PoolID, PermitID string }
type KnownSettlement struct {
	Reference   AttemptReference
	ClaimID     string
	Observation AttemptObservation
}
type SettlementNotice struct {
	PermitID string
	Revision int64
}

func (a *Activities) SettleKnownAttempt(ctx context.Context, known KnownSettlement) (Attempt, error) {
	ledger, ok := a.Store.(ExecutionStore)
	if !ok {
		return Attempt{}, ErrUnavailable
	}
	retained, err := a.lookupAttempt(ctx, known.Reference)
	if err != nil {
		return Attempt{}, err
	}
	if retained.State == "settled" {
		return retained, nil
	}
	return ledger.SettleAttempt(ctx, known.Reference.PermitID, known.ClaimID, known.Observation)
}
func (a *Activities) ReconcileAttempt(ctx context.Context, ref AttemptReference) (Attempt, error) {
	ledger, ok := a.Store.(ExecutionStore)
	if !ok {
		return Attempt{}, ErrUnavailable
	}
	retained, err := a.lookupAttempt(ctx, ref)
	if err != nil {
		return Attempt{}, err
	}
	if retained.State == "settled" {
		return retained, nil
	}
	if retained.State == "reserved" {
		if !time.Now().Before(retained.StartBy) {
			return ledger.ExpireReservation(ctx, retained.PermitID)
		}
		return retained, nil
	}
	if retained.Observation != nil {
		return ledger.SettleAttempt(ctx, retained.PermitID, retained.ClaimID, *retained.Observation)
	}
	if retained.Kind == "job" && retained.DispatchedAt != nil && a.Artifacts != nil {
		recovered, readErr := readAttemptArtifact(ctx, a.Artifacts, retained)
		if readErr == nil {
			return ledger.SettleAttempt(ctx, retained.PermitID, retained.ClaimID, recovered.Observation)
		}
	}
	if time.Now().Before(retained.executionDeadline().Add(attemptFinalizationGrace)) {
		return retained, nil
	}
	observation := AttemptObservation{Outcome: Outcome{Kind: "unconfirmed", Error: &ItemError{Code: "execution_unconfirmed", Message: "No authoritative invocation outcome was retained before finalization."}}, NativeEntered: retained.DispatchedAt != nil, LocalStopped: retained.StoppedAt != nil, ObservedAt: time.Now().UTC()}
	return ledger.SettleAttempt(ctx, retained.PermitID, retained.ClaimID, observation)
}
func (a *Activities) NotifySettlement(ctx context.Context, attempt Attempt) error {
	if a.Temporal == nil || attempt.State != "settled" || attempt.SettlementRevision < 1 {
		return ErrUnavailable
	}
	return a.Temporal.SignalWorkflow(ctx, "hllm-cpa-pool-v1/"+attempt.PoolID, "", settlementSignal, SettlementNotice{PermitID: attempt.PermitID, Revision: attempt.SettlementRevision})
}
func AttemptWorkflow(ctx workflow.Context, input AttemptInput) error {
	if input.ProviderTaskQueue == "" || input.Reservation.BudgetMS < 1 || input.Reservation.Deadline.IsZero() {
		return errors.New("batch: invalid independent attempt input")
	}
	var a *Activities
	remaining := input.Reservation.Deadline.Sub(workflow.Now(ctx))
	providerOptions := workflow.ActivityOptions{TaskQueue: input.ProviderTaskQueue, StartToCloseTimeout: max(time.Second, min(time.Duration(input.Reservation.BudgetMS)*time.Millisecond, remaining)+attemptFinalizationGrace), HeartbeatTimeout: 30 * time.Second, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1}}
	providerCtx := workflow.WithActivityOptions(ctx, providerOptions)
	var invoked InvocationResult
	// This is the only provider activity scheduling site. A failure or lost
	// acknowledgement below only enters metadata reconciliation.
	invocationErr := workflow.ExecuteActivity(providerCtx, a.InvokeAttempt, input.Reservation.PermitID).Get(providerCtx, &invoked)
	reference := AttemptReference{PoolID: input.Reservation.PoolID, PermitID: input.Reservation.PermitID}
	metaCtx := workflow.WithActivityOptions(ctx, attemptMetadataOptions())
	for {
		var retained Attempt
		var err error
		if invocationErr == nil && invoked.Observation != nil && invoked.ClaimID != "" {
			known := KnownSettlement{Reference: reference, ClaimID: invoked.ClaimID, Observation: *invoked.Observation}
			err = workflow.ExecuteActivity(metaCtx, a.SettleKnownAttempt, known).Get(metaCtx, &retained)
		} else {
			err = workflow.ExecuteActivity(metaCtx, a.ReconcileAttempt, reference).Get(metaCtx, &retained)
		}
		if err == nil && retained.State == "settled" {
			// A lost notice is covered by the pool's bounded ledger readback. It does
			// not warrant replaying execution or changing the retained settlement.
			_ = workflow.ExecuteActivity(metaCtx, a.NotifySettlement, retained).Get(metaCtx, nil)
			return nil
		}
		if err != nil && invoked.Observation != nil {
			// Prefer an already-authoritative outcome if a concurrent finalization
			// won. Keep our known observation for later metadata retry otherwise.
			var current Attempt
			readErr := workflow.ExecuteActivity(metaCtx, a.ReconcileAttempt, reference).Get(metaCtx, &current)
			if readErr == nil && current.State == "settled" {
				_ = workflow.ExecuteActivity(metaCtx, a.NotifySettlement, current).Get(metaCtx, nil)
				return nil
			}
		}
		if err := workflow.Sleep(ctx, 5*time.Second); err != nil {
			return err
		}
	}
}
func (a *Activities) lookupAttempt(ctx context.Context, ref AttemptReference) (Attempt, error) {
	ledger, ok := a.Store.(ExecutionStore)
	if !ok {
		return Attempt{}, ErrUnavailable
	}
	attempts, err := ledger.ReadAttempts(ctx, ref.PoolID, []string{ref.PermitID})
	if err != nil {
		return Attempt{}, err
	}
	if len(attempts) != 1 {
		return Attempt{}, ErrNotFound
	}
	return attempts[0], nil
}
func attemptMetadataOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{ScheduleToCloseTimeout: 15 * time.Second, StartToCloseTimeout: 5 * time.Second, RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, MaximumInterval: 5 * time.Second}}
}
