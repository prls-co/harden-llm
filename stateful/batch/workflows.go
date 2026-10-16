package batch

import (
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"time"
)

type PendingJob struct {
	Job     JobRef
	Ready   []int
	Active  int
	Expired bool
}
type DispatcherState struct {
	Controller Controller
	Jobs       []PendingJob
	Turn       int
	Processed  int
}

func (s *DispatcherState) PopReady() (Ref, bool) {
	for n := 0; n < len(s.Jobs); n++ {
		i := (s.Turn + n) % len(s.Jobs)
		j := &s.Jobs[i]
		if len(j.Ready) == 0 || j.Expired {
			continue
		}
		index := j.Ready[0]
		j.Ready = j.Ready[1:]
		j.Active++
		s.Turn = (i + 1) % len(s.Jobs)
		return Ref{Scope: j.Job.Scope, JobID: j.Job.JobID, Index: index}, true
	}
	return Ref{}, false
}

type completion struct {
	Ref          Ref
	Probe        bool
	DispatchedAt time.Time
	ReservedAt   time.Time
	Outcome      Outcome
	Counts       Counts
}
type CooldownUpdate struct {
	Jobs   []JobRef
	Reason string
	Next   *time.Time
}

func durableOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{StartToCloseTimeout: 15 * time.Second, RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, MaximumInterval: 30 * time.Second}}
}

// BatchWorkflow owns registration and a durable overall deadline, not inference retries.
func BatchWorkflow(ctx workflow.Context, job JobRef) error {
	ctx = workflow.WithActivityOptions(ctx, durableOptions())
	var a *Activities
	if err := workflow.ExecuteActivity(ctx, a.Enqueue, job).Get(ctx, nil); err != nil {
		return err
	}
	for {
		var status Status
		if err := workflow.ExecuteActivity(ctx, a.Status, job).Get(ctx, &status); err != nil {
			return err
		}
		if status.Counts.Done() {
			return nil
		}
		remaining := job.Deadline.Sub(workflow.Now(ctx))
		if remaining <= 0 {
			return workflow.ExecuteActivity(ctx, a.Expire, job).Get(ctx, nil)
		}
		if err := workflow.Sleep(ctx, min(remaining, 30*time.Second)); err != nil {
			return err
		}
	}
}

// DispatcherWorkflow is the sole admission authority for its configured CPA pool.
// All queues, timers, and policy state participate in Temporal replay.
func DispatcherWorkflow(ctx workflow.Context, s DispatcherState) error {
	if err := s.Controller.Policy.Validate(); err != nil {
		return err
	}
	var a *Activities
	wake := workflow.GetSignalChannel(ctx, enqueueSignal)
	resume := workflow.GetSignalChannel(ctx, "resume")
	configure := workflow.GetSignalChannel(ctx, "policy")
	finished := workflow.NewBufferedChannel(ctx, 1000)
	active := 0
	lastWork := workflow.Now(ctx)
	nextHeartbeat := lastWork
	coalesceHeartbeats := workflow.GetVersion(ctx, "coalesce-status-heartbeats", workflow.DefaultVersion, 1) != workflow.DefaultVersion
	if err := workflow.SetQueryHandler(ctx, stateQuery, func() (DispatcherState, error) { return s, nil }); err != nil {
		return err
	}
	refs := func() []JobRef {
		out := make([]JobRef, 0, len(s.Jobs))
		for _, j := range s.Jobs {
			out = append(out, j.Job)
		}
		return out
	}
	publish := func() error {
		var next *time.Time
		reason := ""
		if s.Controller.Paused {
			reason = "account_restricted"
		} else if s.Controller.Cooling {
			reason = "provider_throttled"
			v := s.Controller.NextEligible
			next = &v
		} else if v := s.Controller.WindowWait(workflow.Now(ctx)); !v.IsZero() {
			reason = "admission_window"
			next = &v
		}
		metaCtx := workflow.WithActivityOptions(ctx, durableOptions())
		return workflow.ExecuteActivity(metaCtx, a.Cooldown, CooldownUpdate{Jobs: refs(), Reason: reason, Next: next}).Get(metaCtx, nil)
	}
	enqueue := func(job JobRef) {
		for _, j := range s.Jobs {
			if j.Job.JobID == job.JobID && j.Job.Scope == job.Scope {
				return
			}
		}
		if job.Count < 1 || job.Count > MaximumItems {
			return
		}
		ready := make([]int, job.Count)
		for i := range ready {
			ready[i] = i
		}
		s.Jobs = append(s.Jobs, PendingJob{Job: job, Ready: ready})
		lastWork = workflow.Now(ctx)
	}
	finish := func(done completion) {
		active--
		s.Processed++
		lastWork = workflow.Now(ctx)
		for i := range s.Jobs {
			j := &s.Jobs[i]
			if j.Job.JobID == done.Ref.JobID && j.Job.Scope == done.Ref.Scope {
				j.Active--
				if done.Outcome.Kind == "rejected" && !j.Expired {
					j.Ready = append(j.Ready, done.Ref.Index)
				}
				if done.Counts.Done() {
					j.Ready = nil
					j.Expired = true
				}
				break
			}
		}
		wasCooling, wasPaused, wasEligible := s.Controller.Cooling, s.Controller.Paused, s.Controller.NextEligible
		s.Controller.Complete(workflow.Now(ctx), done.DispatchedAt, done.ReservedAt, done.Probe, done.Outcome.Kind, time.Duration(done.Outcome.RetryAfterMS)*time.Millisecond)
		if !coalesceHeartbeats || wasCooling != s.Controller.Cooling || wasPaused != s.Controller.Paused || !wasEligible.Equal(s.Controller.NextEligible) {
			nextHeartbeat = workflow.Now(ctx)
		}
	}
	drainSignals := func() {
		var incoming JobRef
		for wake.ReceiveAsync(&incoming) {
			enqueue(incoming)
		}
		var reset bool
		for resume.ReceiveAsync(&reset) {
			if reset {
				s.Controller.Paused = false
				s.Controller.Cooling = true
				s.Controller.Probing = false
				s.Controller.NextEligible = workflow.Now(ctx)
				nextHeartbeat = workflow.Now(ctx)
			}
		}
		var policy Policy
		for configure.ReceiveAsync(&policy) {
			if err := s.Controller.Configure(policy); err != nil {
				workflow.GetLogger(ctx).Warn("invalid batch admission policy ignored")
			} else {
				nextHeartbeat = workflow.Now(ctx)
			}
		}
		var done completion
		for finished.ReceiveAsync(&done) {
			finish(done)
		}
	}
	for {
		drainSignals()
		now := workflow.Now(ctx)
		for i := range s.Jobs {
			j := &s.Jobs[i]
			if !j.Expired && !now.Before(j.Job.Deadline) {
				metaCtx := workflow.WithActivityOptions(ctx, durableOptions())
				if err := workflow.ExecuteActivity(metaCtx, a.Expire, j.Job).Get(metaCtx, nil); err != nil {
					return err
				}
				j.Expired = true
				j.Ready = nil
			}
		}
		kept := s.Jobs[:0]
		for _, j := range s.Jobs {
			if !j.Expired || j.Active > 0 {
				kept = append(kept, j)
			}
		}
		s.Jobs = kept
		if s.Turn >= len(s.Jobs) {
			s.Turn = 0
		}
		drain := workflow.GetInfo(ctx).GetContinueAsNewSuggested() || workflow.GetInfo(ctx).GetCurrentHistoryLength() > 10000
		if drain && active == 0 {
			// Activities above may have yielded while new signals arrived. Carry
			// all accepted jobs/policy changes across the run boundary.
			drainSignals()
			s.Processed = 0
			return workflow.NewContinueAsNewError(ctx, DispatcherWorkflowName, s)
		}
		if len(s.Jobs) > 0 && !now.Before(nextHeartbeat) {
			if err := publish(); err != nil {
				return err
			}
			nextHeartbeat = workflow.Now(ctx).Add(5 * time.Second)
		}
		for !drain && s.Controller.CanStart(workflow.Now(ctx), active) {
			r, ready := s.PopReady()
			if !ready {
				break
			}
			active++
			probe := s.Controller.Cooling
			dispatched := workflow.Now(ctx)
			reserved := dispatched
			s.Controller.Start(workflow.Now(ctx))
			if !s.Controller.WindowWait(workflow.Now(ctx)).IsZero() {
				nextHeartbeat = workflow.Now(ctx)
			}
			workflow.Go(ctx, func(child workflow.Context) {
				callCtx := workflow.WithActivityOptions(child, workflow.ActivityOptions{StartToCloseTimeout: 195 * time.Second, HeartbeatTimeout: 30 * time.Second, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1}})
				var outcome Outcome
				if err := workflow.ExecuteActivity(callCtx, a.Generate, r).Get(callCtx, &outcome); err != nil {
					outcome = Outcome{Kind: "failed", Error: &ItemError{Code: "activity_failed", Message: "The provider activity failed; inspect correlated diagnostics."}}
				}
				if outcome.StartedAt != nil {
					dispatched = *outcome.StartedAt
				}
				metaCtx := workflow.WithActivityOptions(child, durableOptions())
				var c Counts
				if err := workflow.ExecuteActivity(metaCtx, a.Record, r, outcome).Get(metaCtx, &c); err != nil {
					workflow.GetLogger(child).Error("batch outcome persistence failed", "jobId", r.JobID, "item", r.Index)
					return
				}
				finished.Send(child, completion{Ref: r, Probe: probe, DispatchedAt: dispatched, ReservedAt: reserved, Outcome: outcome, Counts: c})
			})
		}
		now = workflow.Now(ctx)
		if len(s.Jobs) == 0 && active == 0 && now.Sub(lastWork) >= 30*time.Minute {
			drainSignals()
			if len(s.Jobs) == 0 {
				return nil
			}
		}
		delay := 5 * time.Second
		if !drain && !s.Controller.Paused && !s.Controller.Probing && active < s.Controller.Policy.Concurrency {
			ready := false
			for _, j := range s.Jobs {
				ready = ready || len(j.Ready) > 0
			}
			if ready {
				next := s.Controller.NextAdmission(now)
				delay = min(delay, max(time.Millisecond, next.Sub(now)))
			}
		}
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(wake, func(ch workflow.ReceiveChannel, _ bool) { var j JobRef; ch.Receive(ctx, &j); enqueue(j) })
		selector.AddReceive(resume, func(ch workflow.ReceiveChannel, _ bool) {
			var v bool
			ch.Receive(ctx, &v)
			if v {
				s.Controller.Paused = false
				s.Controller.Cooling = true
				s.Controller.Probing = false
				s.Controller.NextEligible = workflow.Now(ctx)
				nextHeartbeat = workflow.Now(ctx)
			}
		})
		selector.AddReceive(configure, func(ch workflow.ReceiveChannel, _ bool) {
			var p Policy
			ch.Receive(ctx, &p)
			if err := s.Controller.Configure(p); err != nil {
				workflow.GetLogger(ctx).Warn("invalid batch admission policy ignored")
			} else {
				nextHeartbeat = workflow.Now(ctx)
			}
		})
		selector.AddReceive(finished, func(ch workflow.ReceiveChannel, _ bool) { var d completion; ch.Receive(ctx, &d); finish(d) })
		timerCtx, cancel := workflow.WithCancel(ctx)
		selector.AddFuture(workflow.NewTimer(timerCtx, delay), func(workflow.Future) {})
		selector.Select(ctx)
		cancel()
	}
}
