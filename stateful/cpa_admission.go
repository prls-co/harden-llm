package stateful

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/stateful/batch"
)

// CPAAdmission composes ordinary dispatches with the configured CPA pool.
// Endpoint identifies a trusted CPA origin, never an incoming scope selector.
type CPAAdmission struct {
	Store            batch.RequestQueueStore
	Signals          batch.RequestPoolSignals
	PoolID, Endpoint string
	Logger           *slog.Logger
}

type admissionExecutionKey struct{}
type admissionExecution struct {
	id      string
	ordinal atomic.Int32
}
type admissionObserver struct{ hardenllm.Observer }

func (o admissionObserver) StartCall(ctx context.Context, v hardenllm.CallObservation) (context.Context, func(hardenllm.CallSummary, error)) {
	ctx, finish := o.Observer.StartCall(ctx, v)
	var cancel context.CancelFunc = func() {}
	if _, ok := ctx.Deadline(); !ok {
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
	}
	ctx = context.WithValue(ctx, admissionExecutionKey{}, &admissionExecution{id: uuid.NewString()})
	return ctx, func(s hardenllm.CallSummary, err error) { defer cancel(); finish(s, err) }
}

func admissionOrigin(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return ""
		}
		port = strconv.Itoa(n)
	}
	if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host
}

func (a *CPAAdmission) validate() error {
	if a.Store == nil || a.Signals == nil || len(a.PoolID) < 1 || len(a.PoolID) > 128 || strings.TrimSpace(a.PoolID) != a.PoolID || strings.ContainsAny(a.PoolID, "\x00\r\n\t") || admissionOrigin(a.Endpoint) == "" {
		return errors.New("stateful: CPA admission requires a store, Temporal client, pool identity and configured endpoint")
	}
	return nil
}

func (s *Service) scopedEffects(scope string) hardenllm.Options {
	o := s.effects
	if s.config.Admission != nil {
		o.Executor = admissionExecutor{next: o.Executor, admission: s.config.Admission, scope: scope}
		o.Observer = admissionObserver{Observer: o.Observer}
	}
	return o
}

type admissionExecutor struct {
	next      hardenllm.Executor
	admission *CPAAdmission
	scope     string
}
type admissionPrepared struct{ operation hardenllm.PreparedOperation }

func (e admissionExecutor) Prepare(ctx context.Context, c hardenllm.ProviderConnection, k hardenllm.ProviderCredential, call hardenllm.ProviderCall) (hardenllm.PreparedOperation, error) {
	p, err := e.next.Prepare(ctx, c, k, call)
	if err == nil && admissionOrigin(c.BaseURL) == admissionOrigin(e.admission.Endpoint) {
		p.Opaque = admissionPrepared{operation: p}
	}
	return p, err
}

func admissionFailure() error {
	return &hardenllm.ProviderError{Status: 503, Category: retry.CategoryOther, Code: "admission_unavailable", Err: errors.New("CPA admission could not be confirmed")}
}

func (a *CPAAdmission) failure(ctx context.Context, stage string, causes ...error) error {
	if a.Logger != nil {
		class := "unavailable"
		if len(causes) > 0 {
			switch {
			case errors.Is(causes[0], batch.ErrClaimOwned):
				class = "claim_not_available"
			case errors.Is(causes[0], batch.ErrClaimUnconfirmed):
				class = "claim_unconfirmed"
			case errors.Is(causes[0], batch.ErrGenerationFenced):
				class = "generation_fenced"
			case errors.Is(causes[0], context.DeadlineExceeded):
				class = "deadline"
			case errors.Is(causes[0], context.Canceled):
				class = "canceled"
			}
		}
		a.Logger.ErrorContext(ctx, "ordinary CPA admission not confirmed", "pool_id", a.PoolID, "metadata_stage", stage, "metadata_error_class", class, "native_entered", false)
	}
	return admissionFailure()
}

func (a *CPAAdmission) notify(ctx context.Context, permit string, revision int64) {
	bounded, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	// Revision zero requests positive claim readback. The pool never trusts a
	// notice to release capacity or anchor a window without reading its ledger.
	_ = a.Signals.Changed(bounded, a.PoolID, batch.SettlementNotice{PermitID: permit, Revision: revision})
}

func (a *CPAAdmission) begin(ctx context.Context, scope string, execution *admissionExecution) (attempt batch.Attempt, err error) {
	if ctx.Err() != nil {
		return attempt, ctx.Err()
	}
	deadline, ok := ctx.Deadline()
	if !ok || checkScope(scope) != nil || execution == nil {
		return attempt, admissionFailure()
	}
	marker, readErr := a.Store.PoolGeneration(ctx, a.PoolID)
	if readErr != nil || marker.Generation < 2 || marker.Mode != "active" {
		return attempt, a.failure(ctx, "pool_generation")
	}
	w := batch.RequestWaiter{RequestID: uuid.NewString(), PoolID: a.PoolID, Generation: marker.Generation, Scope: scope, OwnerID: uuid.NewString(), ExecutionID: execution.id, Ordinal: int(execution.ordinal.Add(1)), Deadline: deadline.UTC().Truncate(time.Microsecond), BudgetMS: max(1, time.Until(deadline).Milliseconds())}
	permit := ""
	succeeded := false
	defer func() {
		if succeeded {
			return
		}
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		_ = a.Store.CancelRequest(cleanup, scope, w.RequestID, w.OwnerID)
		if permit != "" {
			// This invocation never entered Execute even if a metadata acknowledgement
			// was lost. Its fresh owner nonce fences all cleanup from another invocation.
			o := batch.AttemptObservation{Outcome: batch.Outcome{Kind: "never_dispatched"}, LocalStopped: true, ObservedAt: time.Now().UTC()}
			_, _ = a.Store.MarkLocalStop(cleanup, permit, w.OwnerID, o.ObservedAt)
			_, _ = a.Store.StageAttempt(cleanup, permit, w.OwnerID, o)
			settled, e := a.Store.SettleAttempt(cleanup, permit, w.OwnerID, o)
			if e == nil {
				a.notify(cleanup, permit, settled.SettlementRevision)
			}
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	if s := trackingSession(ctx); s != nil {
		s.AdmissionWait("dispatch_pending", nil)
	}
	if _, queueErr := a.Store.EnqueueRequest(ctx, w); queueErr != nil {
		return attempt, a.failure(ctx, "enqueue")
	}
	wake, stop := context.WithTimeout(ctx, 3*time.Second)
	signalErr := a.Signals.Wake(wake, a.PoolID)
	stop()
	if signalErr != nil {
		return attempt, a.failure(ctx, "wake")
	}
	waitStarted := time.Now()
	unavailableReads := 0
	for {
		if ctx.Err() != nil {
			return attempt, ctx.Err()
		}
		current, e := a.Store.ReadRequest(ctx, scope, w.RequestID, w.OwnerID, true)
		if errors.Is(e, batch.ErrUnavailable) && unavailableReads < 2 {
			// A read/heartbeat acknowledgement can be recovered with the same
			// owner and waiter. Never enqueue again or retry a claim/dispatch.
			unavailableReads++
			if a.Logger != nil {
				a.Logger.WarnContext(ctx, "ordinary CPA admission recovering waiter read", "pool_id", a.PoolID, "metadata_stage", "waiter_read", "recovery_read", unavailableReads, "native_entered", false)
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return attempt, ctx.Err()
			case <-timer.C:
				continue
			}
		}
		if e != nil || current.ClosedAt != nil || !time.Now().Before(current.LiveUntil) {
			stage := "waiter_read"
			if e == nil {
				stage = "waiter_expired_or_closed"
			}
			return attempt, a.failure(ctx, stage, e)
		}
		unavailableReads = 0
		if s := trackingSession(ctx); s != nil {
			reason := current.WaitingReason
			if reason == "" {
				reason = "dispatch_pending"
			}
			s.AdmissionWait(reason, current.NextAttemptAt)
		}
		if current.PermitID != "" {
			permit = current.PermitID
			break
		}
		delay := 100 * time.Millisecond
		if time.Since(waitStarted) > 2*time.Second {
			delay = 500 * time.Millisecond
		}
		if current.NextAttemptAt != nil && time.Until(*current.NextAttemptAt) > delay {
			delay = min(2*time.Second, time.Until(*current.NextAttemptAt))
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return attempt, ctx.Err()
		case <-timer.C:
		}
	}
	attempt, err = a.Store.ClaimAttempt(ctx, permit, w.OwnerID)
	if err != nil {
		return batch.Attempt{}, a.failure(ctx, "claim", err)
	}
	if attempt.Kind != "ordinary" || attempt.RequestID != w.RequestID || attempt.PoolID != w.PoolID || attempt.Generation != w.Generation || attempt.Ref.Scope != scope || attempt.ClaimID != w.OwnerID || attempt.BudgetMS != w.BudgetMS || !attempt.Deadline.Equal(w.Deadline) {
		return batch.Attempt{}, a.failure(ctx, "claim_identity")
	}
	if ctx.Err() != nil {
		return attempt, ctx.Err()
	}
	attempt, err = a.Store.MarkDispatch(ctx, permit, w.OwnerID)
	if err != nil {
		return batch.Attempt{}, a.failure(ctx, "dispatch_intent", err)
	}
	if ctx.Err() != nil || !time.Now().Before(attempt.StartBy) || !time.Now().Before(attempt.Deadline) {
		if ctx.Err() != nil {
			return attempt, ctx.Err()
		}
		return attempt, context.DeadlineExceeded
	}
	succeeded = true
	return attempt, nil
}

func (e admissionExecutor) Execute(ctx context.Context, p hardenllm.PreparedOperation) (result hardenllm.ProviderResult, err error) {
	wrapped, managed := p.Opaque.(admissionPrepared)
	if !managed {
		return e.next.Execute(ctx, p)
	}
	execution, _ := ctx.Value(admissionExecutionKey{}).(*admissionExecution)
	waitStarted := time.Now()
	a, err := e.admission.begin(ctx, e.scope, execution)
	if err != nil {
		return result, err
	}
	queuedMS := time.Since(waitStarted).Milliseconds()
	nativeEntered, stopped := false, false
	started := time.Now()
	var finished time.Time
	var duration time.Duration
	observation := batch.AttemptObservation{Outcome: batch.Outcome{Kind: "never_dispatched"}, LocalStopped: true}
	defer func() {
		panicValue := recover()
		now := time.Now().UTC()
		if panicValue != nil {
			err = &hardenllm.ProviderError{Category: retry.CategoryOther, Code: "execution_unconfirmed", Err: errors.New("CPA invocation stopped reporting its outcome")}
			observation.Outcome = batch.Outcome{Kind: "unconfirmed", Error: &batch.ItemError{Code: "invocation_panicked", Message: "The invocation outcome is unconfirmed."}}
		}
		observation.NativeEntered, observation.LocalStopped, observation.ObservedAt = nativeEntered, !nativeEntered || stopped, now
		out := &observation.Outcome
		out.QueueMS = queuedMS
		out.Attempt = int(a.Ordinal)
		out.GenerationBudgetMS = a.BudgetMS
		out.GenerationDeadline = a.GenerationDeadline
		if nativeEntered {
			if !stopped {
				duration = time.Since(started)
			}
			at := started.UTC()
			out.StartedAt = &at
			out.DurationMS = duration.Milliseconds()
		}
		if stopped {
			out.FinishedAt = &finished
		}
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		if observation.LocalStopped {
			_, _ = e.admission.Store.MarkLocalStop(cleanup, a.PermitID, a.ClaimID, now)
		}
		_, _ = e.admission.Store.StageAttempt(cleanup, a.PermitID, a.ClaimID, observation)
		settled, settleErr := e.admission.Store.SettleAttempt(cleanup, a.PermitID, a.ClaimID, observation)
		if settleErr == nil {
			e.admission.notify(cleanup, a.PermitID, settled.SettlementRevision)
		}
		if e.admission.Logger != nil {
			e.admission.Logger.InfoContext(ctx, "ordinary CPA dispatch observed", "execution_id", execution.id, "request_id", a.RequestID, "permit_id", a.PermitID, "dispatch_ordinal", a.Ordinal, "kind", out.Kind, "admission", out.Admission, "native_entered", nativeEntered, "local_stopped", observation.LocalStopped, "settlement_confirmed", settleErr == nil, "queue_ms", out.QueueMS, "generation_ms", out.DurationMS, "started_at", out.StartedAt, "finished_at", out.FinishedAt, "retry_after_ms", out.RetryAfterMS, "headers_ms", out.HeadersMS, "first_output_ms", out.FirstOutputMS, "input_tokens", out.InputTokens, "output_tokens", out.OutputTokens, "reasoning_tokens", out.ReasoningTokens)
		}
	}()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	original := wrapped.operation
	original.Operation, original.StreamProgress = p.Operation, p.StreamProgress
	// The ledger claim and dispatch intent already authorize this invocation.
	// A best-effort pool read hint must not consume its five-second start lease.
	// One bounded, request-owned sender runs alongside native I/O and is joined
	// before finalization; it can neither grant capacity nor replay inference.
	hintCtx, stopHint := context.WithCancel(ctx)
	hintDone := make(chan struct{})
	go func() {
		defer close(hintDone)
		e.admission.notify(hintCtx, a.PermitID, 0)
	}()
	defer func() { stopHint(); <-hintDone }()
	nativeEntered = true
	result, err = e.next.Execute(ctx, original)
	duration = time.Since(started)
	finished = time.Now().UTC()
	stopped = true
	observation.Outcome, err = batch.ObserveProviderDispatch(result, err)
	if err == nil && a.GenerationDeadline != nil && finished.After(*a.GenerationDeadline) {
		observation.Outcome.Kind = "unconfirmed"
		err = context.DeadlineExceeded
	}
	return result, err
}
