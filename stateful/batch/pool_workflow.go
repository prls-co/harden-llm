package batch

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type PoolIdentity struct {
	PoolID     string
	Generation int64
}
type PoolRead struct {
	PoolID     string
	Generation int64
	IDs        []string
}

func (a *Activities) InitializePool(ctx context.Context, identity PoolIdentity) error {
	ledger, ok := a.Store.(ExecutionStore)
	if !ok || identity.PoolID != a.PoolID || identity.Generation < 2 {
		return ErrGenerationFenced
	}
	current, err := ledger.PoolGeneration(ctx, identity.PoolID)
	if errors.Is(err, ErrNotFound) {
		return ledger.EnsurePool(ctx, identity.PoolID, identity.Generation)
	}
	if err != nil {
		return err
	}
	if current.Generation != identity.Generation {
		return ErrGenerationFenced
	}
	return nil
}
func (a *Activities) SelectPoolReady(ctx context.Context, q QueueSelection) (ReadyPage, error) {
	store, ok := a.Store.(QueueStore)
	if !ok || q.PoolID != a.PoolID {
		return ReadyPage{}, ErrUnavailable
	}
	return store.SelectReady(ctx, q)
}
func (a *Activities) ReadPool(ctx context.Context, read PoolRead) ([]Attempt, error) {
	ledger, ok := a.Store.(ExecutionStore)
	if !ok || read.PoolID != a.PoolID || len(read.IDs) > 128 {
		return nil, ErrUnavailable
	}
	bounded, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	generation, err := ledger.PoolGeneration(bounded, read.PoolID)
	if err != nil {
		return nil, err
	}
	if generation.Generation != read.Generation {
		return nil, ErrGenerationFenced
	}
	attempts, err := ledger.ReadAttempts(bounded, read.PoolID, read.IDs)
	if err != nil {
		return nil, err
	}
	for i, current := range attempts {
		if current.Generation != read.Generation {
			return nil, ErrGenerationFenced
		}
		if current.State == "reserved" && !time.Now().Before(current.StartBy) {
			updated, e := ledger.ExpireReservation(bounded, current.PermitID)
			if e == nil {
				attempts[i] = updated
			} else if !errors.Is(e, ErrClaimOwned) {
				return nil, e
			}
		} else if current.State == "dispatching" && (current.Observation != nil || !time.Now().Before(current.executionDeadline().Add(attemptFinalizationGrace))) {
			updated, e := a.ReconcileAttempt(bounded, AttemptReference{PoolID: read.PoolID, PermitID: current.PermitID})
			if e != nil {
				return nil, e
			}
			attempts[i] = updated
		}
	}
	raw, err := json.Marshal(attempts)
	if err != nil || len(raw) > 1<<20 {
		return nil, ErrUnavailable
	}
	return attempts, nil
}
func (a *Activities) ProviderReady(ctx context.Context) error { return ctx.Err() }

func (a *Activities) RetireRequests(ctx context.Context, read PoolRead) error {
	store, ok := a.Store.(RequestQueueStore)
	if !ok || read.PoolID != a.PoolID {
		return ErrUnavailable
	}
	return store.AcknowledgeRequests(ctx, read)
}

type poolStarterFuture struct {
	future       workflow.Future
	reservations []Reservation
}

func poolMetadataOptions(timeout time.Duration) workflow.ActivityOptions {
	return workflow.ActivityOptions{ScheduleToCloseTimeout: timeout + time.Second, StartToCloseTimeout: timeout, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1}}
}

// PoolWorkflow owns grants and durable pacing, not provider activities. Only
// independent attempt workflows schedule inference, so CAN never drains calls.
func PoolWorkflow(ctx workflow.Context, state PoolState) error {
	if err := state.Controller.Policy.Validate(); err != nil {
		return err
	}
	if _, err := state.Snapshot(); err != nil {
		return err
	}
	if err := workflow.SetQueryHandler(ctx, stateQuery, func() (PoolState, error) { return state, nil }); err != nil {
		return err
	}
	var a *Activities
	publishStatus := workflow.GetVersion(ctx, "pool-status-publication", workflow.DefaultVersion, 1) != workflow.DefaultVersion
	if workflow.GetVersion(ctx, "coalesced-dispatch-backpressure", workflow.DefaultVersion, 1) != workflow.DefaultVersion {
		state.CoalesceDispatchFailures = true
	}
	identity := PoolIdentity{PoolID: state.PoolID, Generation: state.Generation}
	initCtx := workflow.WithActivityOptions(ctx, attemptMetadataOptions())
	metaCtx := workflow.WithActivityOptions(ctx, poolMetadataOptions(5*time.Second))
	startCtx := workflow.WithActivityOptions(ctx, poolMetadataOptions(15*time.Second))
	wakes := workflow.GetSignalChannel(ctx, enqueueSignal)
	notices := workflow.GetSignalChannel(ctx, settlementSignal)
	resumes := workflow.GetSignalChannel(ctx, "resume")
	policies := workflow.GetSignalChannel(ctx, "policy")
	continuations := workflow.GetSignalChannel(ctx, "continue")
	quiesces := workflow.GetSignalChannel(ctx, "quiesce")
	oldUpgrades := workflow.GetSignalChannel(ctx, legacyUpgradeSignal)
	oldQuiesces := workflow.GetSignalChannel(ctx, legacyQuiesceSignal)
	var starters []poolStarterFuture
	var reads []workflow.Future
	var selection, readiness, publication, retirement workflow.Future
	var retirementIDs []string
	var nextRetirement time.Time
	var publicationRequest PoolPublication
	var ready []ReadyItem
	now := workflow.Now(ctx)
	nextRead, nextQueue, nextStart := now, now, now
	forceContinue, urgentRead, readFailed := false, false, false
	drain := func() {
		var obsolete bool
		for oldUpgrades.ReceiveAsync(&obsolete) {
		}
		for oldQuiesces.ReceiveAsync(&obsolete) {
		}
		var job JobRef
		for wakes.ReceiveAsync(&job) {
			nextQueue = workflow.Now(ctx)
		}
		var notice SettlementNotice
		for notices.ReceiveAsync(&notice) {
			urgentRead = true
		}
		var resume bool
		for resumes.ReceiveAsync(&resume) {
			if resume {
				state.Controller.Paused = false
				if !state.Controller.Probing {
					state.Controller.Cooling = true
					state.Controller.NextEligible = maxTime(state.Controller.NextEligible, workflow.Now(ctx))
					state.Episode++
					state.ProbeID = ""
				}
			}
		}
		var policy Policy
		for policies.ReceiveAsync(&policy) {
			if err := state.Controller.Configure(policy); err == nil {
				state.PolicyRevision++
			} else {
				workflow.GetLogger(ctx).Warn("invalid pool policy ignored")
			}
		}
		var continued bool
		for continuations.ReceiveAsync(&continued) {
			forceContinue = forceContinue || continued
		}
		var quiesce bool
		for quiesces.ReceiveAsync(&quiesce) {
			if quiesce {
				state.Quiescing = true
			}
		}
	}
	for {
		drain()
		if forceContinue || workflow.GetInfo(ctx).GetContinueAsNewSuggested() || workflow.GetInfo(ctx).GetCurrentHistoryLength() > 8000 {
			if _, err := state.Snapshot(); err != nil {
				return err
			}
			return workflow.NewContinueAsNewError(ctx, PoolWorkflowName, state)
		}
		if err := workflow.ExecuteActivity(initCtx, a.InitializePool, identity).Get(initCtx, nil); err == nil {
			state.PersistenceWait = false
			break
		}
		state.PersistenceWait = true
		if err := workflow.Sleep(ctx, 2*time.Second); err != nil {
			return err
		}
	}
	for {
		drain()
		now = workflow.Now(ctx)
		forceContinue = forceContinue || workflow.GetInfo(ctx).GetContinueAsNewSuggested() || workflow.GetInfo(ctx).GetCurrentHistoryLength() > 8000
		keptReads := reads[:0]
		for _, future := range reads {
			if !future.IsReady() {
				keptReads = append(keptReads, future)
				continue
			}
			var attempts []Attempt
			if err := future.Get(ctx, &attempts); err != nil {
				state.PersistenceWait = true
				readFailed = true
			} else {
				for _, attempt := range attempts {
					if state.Observe(now, attempt) {
						nextQueue = now
					}
				}
			}
		}
		hadReads := len(reads) > 0
		reads = keptReads
		if hadReads && len(reads) == 0 {
			if !readFailed {
				state.PersistenceWait = false
			}
			readFailed = false
		}
		keptStarters := starters[:0]
		for _, pending := range starters {
			if !pending.future.IsReady() {
				keptStarters = append(keptStarters, pending)
				continue
			}
			var result []AttemptStartResult
			if err := pending.future.Get(ctx, &result); err != nil {
				nextStart = now.Add(2 * time.Second)
			} else {
				state.AcknowledgeStarts(result)
				for _, r := range result {
					if !r.Started {
						nextStart = now.Add(2 * time.Second)
					}
				}
			}
		}
		starters = keptStarters
		if selection != nil && selection.IsReady() {
			var page ReadyPage
			if err := selection.Get(ctx, &page); err != nil {
				state.PersistenceWait = true
				nextQueue = now.Add(2 * time.Second)
			} else if len(page.Items) > 128 {
				state.PersistenceWait = true
			} else {
				ready = page.Items
				state.AfterScope = page.AfterScope
				if len(ready) == 0 {
					nextQueue = now.Add(5 * time.Second)
				}
			}
			selection = nil
		}
		if readiness != nil && readiness.IsReady() {
			if err := readiness.Get(ctx, nil); err == nil {
				state.DispatchNeedsReady = false
				state.DispatchBlockedUntil = time.Time{}
			} else {
				state.DispatchBlockedUntil = now.Add(state.DispatchBackoff)
				state.DispatchBackoff = min(30*time.Second, state.DispatchBackoff*2)
			}
			readiness = nil
		}
		if publication != nil && publication.IsReady() {
			var page PublicationPage
			if err := publication.Get(ctx, &page); err != nil {
				state.PublicationAt = now
				state.PublicationAfter = 0
				state.PublicationReason = publicationRequest.Reason
				state.PublicationNext = publicationRequest.Next
				workflow.GetLogger(ctx).Warn("pool status publication unavailable")
			} else {
				reason, next := state.Waiting(now)
				state.PublicationReason = publicationRequest.Reason
				state.PublicationNext = publicationRequest.Next
				if reason != publicationRequest.Reason || !sameOptionalTime(next, publicationRequest.Next) {
					state.PublicationAfter = 0
					state.PublicationAt = time.Time{}
				} else if page.More {
					state.PublicationAfter = page.After
					state.PublicationAt = time.Time{}
				} else {
					state.PublicationAfter = 0
					state.PublicationAt = now
				}
			}
			publication = nil
		}
		if retirement != nil && retirement.IsReady() {
			if err := retirement.Get(ctx, nil); err == nil {
				// New settlements may have arrived while this bounded batch ran.
				state.RequestRetirements = state.RequestRetirements[len(retirementIDs):]
			} else {
				nextRetirement = now.Add(2 * time.Second)
			}
			retirement = nil
		}
		if forceContinue && len(starters) == 0 && len(reads) == 0 && selection == nil && readiness == nil && publication == nil && retirement == nil {
			// No yielding operation after this drain. Lost notices/wakes are backed by
			// ledger/manifests; restrictions and all grant/start identities are carried.
			drain()
			if _, err := state.Snapshot(); err != nil {
				return err
			}
			return workflow.NewContinueAsNewError(ctx, PoolWorkflowName, state)
		}
		if !forceContinue {
			if retirement == nil && len(state.RequestRetirements) > 0 && !now.Before(nextRetirement) {
				retirementIDs = append([]string(nil), state.RequestRetirements[:min(128, len(state.RequestRetirements))]...)
				retirement = workflow.ExecuteActivity(metaCtx, a.RetireRequests, PoolRead{PoolID: state.PoolID, Generation: state.Generation, IDs: retirementIDs})
			}
			if publishStatus && publication == nil {
				reason, next := state.Waiting(now)
				changed := reason != state.PublicationReason || !sameOptionalTime(next, state.PublicationNext)
				if changed || state.PublicationAt.IsZero() || !now.Before(state.PublicationAt.Add(5*time.Second)) {
					if changed {
						state.PublicationAfter = 0
					}
					publicationRequest = PoolPublication{PoolID: state.PoolID, Generation: state.Generation, Reason: reason, Next: next, After: state.PublicationAfter, Limit: 128}
					publication = workflow.ExecuteActivity(metaCtx, a.PublishPool, publicationRequest)
					if changed {
						workflow.GetLogger(ctx).Info("pool waiting state changed", "pool_id", state.PoolID, "reason", reason, "next_eligible_at", next, "outstanding", len(state.Outstanding), "pending_starts", len(state.StartIntents), "window_id", state.WindowID, "window_starts", state.Controller.WindowStarts, "window_anchored", state.WindowAnchored, "episode", state.Episode, "policy_revision", state.PolicyRevision, "launch_rate", state.Controller.Rate)
					}
				}
			}
			if len(reads) == 0 && len(state.Outstanding) > 0 && (urgentRead || !now.Before(nextRead)) {
				ids := make([]string, len(state.Outstanding))
				for i, p := range state.Outstanding {
					ids[i] = p.ID
				}
				for first := 0; first < len(ids); first += 128 {
					last := min(first+128, len(ids))
					request := PoolRead{PoolID: state.PoolID, Generation: state.Generation, IDs: ids[first:last]}
					reads = append(reads, workflow.ExecuteActivity(metaCtx, a.ReadPool, request))
				}
				nextRead = now.Add(5 * time.Second)
				urgentRead = false
				readFailed = false
			}
			// An empty pool must also positively recover after a metadata read failure.
			if len(reads) == 0 && len(state.Outstanding) == 0 && state.PersistenceWait && !now.Before(nextRead) {
				reads = append(reads, workflow.ExecuteActivity(metaCtx, a.ReadPool, PoolRead{PoolID: state.PoolID, Generation: state.Generation}))
				nextRead = now.Add(5 * time.Second)
			}
			if state.DispatchNeedsReady && readiness == nil && !now.Before(state.DispatchBlockedUntil) && !state.Controller.Paused {
				readyCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{TaskQueue: workflow.GetInfo(ctx).TaskQueueName + "/provider", ScheduleToCloseTimeout: 10 * time.Second, StartToCloseTimeout: 5 * time.Second, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1}})
				readiness = workflow.ExecuteActivity(readyCtx, a.ProviderReady)
			}
			for len(ready) > 0 && state.CanGrant(now) {
				candidate := ready[0]
				ready = ready[1:]
				if _, err := state.Grant(now, candidate); err != nil {
					nextQueue = now
				}
			}
			if selection == nil && len(ready) == 0 && state.CanGrant(now) && !now.Before(nextQueue) {
				selection = workflow.ExecuteActivity(metaCtx, a.SelectPoolReady, QueueSelection{PoolID: state.PoolID, Generation: state.Generation, AfterScope: state.AfterScope, Limit: 64, Exclude: state.ReadyExclusions(), ExcludeRequests: state.ReadyRequestExclusions()})
			}
			if !now.Before(nextStart) {
				running := map[string]bool{}
				for _, pending := range starters {
					for _, r := range pending.reservations {
						running[r.PermitID] = true
					}
				}
				batch := []Reservation{}
				for _, r := range state.StartIntents {
					if running[r.PermitID] {
						continue
					}
					batch = append(batch, r)
					if len(batch) == 16 {
						if len(starters) == 4 {
							break
						}
						copyBatch := append([]Reservation(nil), batch...)
						starters = append(starters, poolStarterFuture{workflow.ExecuteActivity(startCtx, a.StartAttempts, copyBatch), copyBatch})
						batch = nil
					}
				}
				if len(batch) > 0 && len(starters) < 4 {
					copyBatch := append([]Reservation(nil), batch...)
					starters = append(starters, poolStarterFuture{workflow.ExecuteActivity(startCtx, a.StartAttempts, copyBatch), copyBatch})
				}
			}
		}
		delay := 5 * time.Second
		if !forceContinue && !state.Quiescing && !state.Controller.Paused && !state.DispatchNeedsReady && !state.PersistenceWait && !state.unresolved() && len(state.Outstanding) < state.Controller.Policy.Concurrency && len(state.StartIntents) < maximumStartIntents && !state.Controller.Probing {
			eligible := state.Controller.NextAdmission(now)
			if !eligible.IsZero() && eligible.After(now) {
				delay = min(delay, eligible.Sub(now))
			}
		}
		if !forceContinue && len(state.StartIntents) > 0 && len(starters) < 4 && nextStart.After(now) {
			delay = min(delay, nextStart.Sub(now))
		}
		selector := workflow.NewSelector(ctx)
		for _, channel := range []workflow.ReceiveChannel{wakes, notices, resumes, policies, continuations, quiesces, oldUpgrades, oldQuiesces} {
			selector.AddReceive(channel, func(workflow.ReceiveChannel, bool) {})
		}
		for _, pending := range starters {
			selector.AddFuture(pending.future, func(workflow.Future) {})
		}
		for _, future := range reads {
			selector.AddFuture(future, func(workflow.Future) {})
		}
		if selection != nil {
			selector.AddFuture(selection, func(workflow.Future) {})
		}
		if readiness != nil {
			selector.AddFuture(readiness, func(workflow.Future) {})
		}
		if publication != nil {
			selector.AddFuture(publication, func(workflow.Future) {})
		}
		if retirement != nil {
			selector.AddFuture(retirement, func(workflow.Future) {})
		}
		timerCtx, cancel := workflow.WithCancel(ctx)
		selector.AddFuture(workflow.NewTimer(timerCtx, delay), func(workflow.Future) {})
		selector.Select(ctx)
		cancel()
	}
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func sameOptionalTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}
