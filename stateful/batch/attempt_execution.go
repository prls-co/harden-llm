package batch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	hardenllm "github.com/prls-co/harden-llm"
	"go.temporal.io/sdk/activity"
)

const attemptFinalizationGrace = 30 * time.Second
const maximumResultArtifact = 16 << 20

// InvocationResult is bounded execution evidence; response bodies stay in Garage.
type InvocationResult struct {
	Disposition string              `json:"disposition"`
	ClaimID     string              `json:"claim_id,omitempty"`
	Observation *AttemptObservation `json:"observation,omitempty"`
}

type attemptArtifact struct {
	Version     int                `json:"attempt_artifact_version"`
	PermitID    string             `json:"permit_id"`
	PoolID      string             `json:"pool_id"`
	ClaimID     string             `json:"claim_id"`
	Ref         Ref                `json:"ref"`
	Observation AttemptObservation `json:"observation"`
	Response    json.RawMessage    `json:"response"`
}

func attemptArtifactID(id string) string { return "attempt-" + id + ".json" }

func (a *Activities) InvokeAttempt(ctx context.Context, id string) (returned InvocationResult, returnErr error) {
	ledger, ok := a.Store.(ExecutionStore)
	if !ok || a.Execute == nil || a.Encode == nil || a.Artifacts == nil {
		return returned, ErrUnavailable
	}
	// Each invocation creates a fresh nonce. Even a redelivery of this activity
	// cannot reuse the original invocation's permission to execute the provider.
	nonce := uuid.NewString()
	claimed, err := ledger.ClaimAttempt(ctx, id, nonce)
	if err != nil {
		switch {
		case errors.Is(err, ErrClaimOwned):
			return InvocationResult{Disposition: "claim_owned_elsewhere"}, nil
		case errors.Is(err, ErrClaimUnconfirmed):
			if recovered, ok := a.confirmNeverEnteredClaim(ctx, ledger, id, nonce); ok {
				return recovered, nil
			}
			return InvocationResult{Disposition: "claim_unconfirmed"}, nil
		default:
			return returned, err
		}
	}
	returned = InvocationResult{Disposition: "observed", ClaimID: nonce}
	observation := AttemptObservation{Outcome: Outcome{Kind: "unconfirmed", Error: &ItemError{Code: "execution_unconfirmed", Message: "The invocation outcome is unconfirmed."}}, ObservedAt: time.Now().UTC()}
	nativeEntered := false
	var finalizationDeadline time.Time
	// Cleanup is independent of Temporal accepting the activity's normal return.
	// A panic does not prove transport closure; keep its permit unresolved.
	defer func() {
		if recover() != nil {
			observation.Outcome.Kind = "unconfirmed"
			if !nativeEntered {
				observation.Outcome.Kind = "never_dispatched"
				observation.LocalStopped = true
			}
			observation.Outcome.ArtifactID = ""
			observation.Outcome.Error = &ItemError{Code: "invocation_panicked", Message: "The invocation stopped reporting a confirmed result."}
			returnErr = nil
		}
		observation.NativeEntered = nativeEntered
		observation.ObservedAt = time.Now().UTC()
		if finalizationDeadline.IsZero() {
			finalizationDeadline = minTime(claimed.executionDeadline().Add(attemptFinalizationGrace), time.Now().Add(attemptFinalizationGrace))
		}
		cleanup, stop := context.WithDeadline(context.WithoutCancel(ctx), finalizationDeadline)
		defer stop()
		if observation.LocalStopped {
			_, _ = ledger.MarkLocalStop(cleanup, id, nonce, observation.ObservedAt)
		}
		_, _ = ledger.StageAttempt(cleanup, id, nonce, observation)
		returned.Observation = &observation
		if a.Logger != nil {
			out := observation.Outcome
			code := ""
			if out.Error != nil {
				code = out.Error.Code
			}
			a.Logger.InfoContext(ctx, "independent provider invocation observed", "permit_id", id, "job_id", claimed.Ref.JobID, "item", claimed.Ref.Index, "kind", out.Kind, "admission", out.Admission, "error_code", code, "native_entered", nativeEntered, "local_stopped", observation.LocalStopped, "generation_budget_ms", out.GenerationBudgetMS, "generation_deadline_at", out.GenerationDeadline, "started_at", out.StartedAt, "finished_at", out.FinishedAt, "generation_ms", out.DurationMS, "retry_wait_ms", out.RetryWaitMS, "queue_ms", out.QueueMS, "headers_ms", out.HeadersMS, "first_output_ms", out.FirstOutputMS, "retry_after_ms", out.RetryAfterMS, "input_tokens", out.InputTokens, "output_tokens", out.OutputTokens, "reasoning_tokens", out.ReasoningTokens)
		}
	}()
	neverSent := func(code string) (InvocationResult, error) {
		observation.Outcome = Outcome{Kind: "never_dispatched", Error: &ItemError{Code: code, Message: "The invocation did not enter the provider path."}}
		observation.LocalStopped = true
		return returned, nil
	}
	if claimed.Kind != "job" {
		return neverSent("invalid_attempt_kind")
	}
	stopClaimHint := a.startClaimReadHint(ctx, claimed)
	defer stopClaimHint()
	item, err := a.Store.Load(ctx, claimed.Ref)
	if err != nil {
		return neverSent("item_unavailable")
	}
	if !reservationMatchesSnapshot(claimed.Reservation, item.Item.Generation, item.Deadline) {
		return neverSent("snapshot_conflict")
	}
	if ctx.Err() != nil || !time.Now().Before(claimed.StartBy) || !time.Now().Before(claimed.Deadline) {
		return neverSent("dispatch_deadline_exceeded")
	}
	dispatched, err := ledger.MarkDispatch(ctx, id, nonce)
	if err != nil {
		return neverSent("dispatch_not_confirmed")
	}
	claimed = dispatched
	// Check again after the final metadata round trip. A dispatch-intent row is
	// not evidence that Execute ran; this owner can positively report never-sent.
	if ctx.Err() != nil || !time.Now().Before(claimed.StartBy) || !time.Now().Before(claimed.Deadline) {
		return neverSent("dispatch_deadline_exceeded")
	}
	callCtx, cancel := context.WithDeadline(ctx, claimed.executionDeadline())
	defer cancel()
	stopShutdownWatch := watchProviderShutdown(ctx, cancel)
	defer stopShutdownWatch()
	progress := make(chan hardenllm.ProgressEvent, 8)
	request := item.Item.Generation.Request()
	request.Origin = hardenllm.Origin{Client: "harden-llm-batch", JobID: claimed.Ref.JobID}
	request.Progress = progress
	stopProgress := a.attemptHeartbeat(ctx, claimed, progress)
	defer stopProgress()
	providerStarted := time.Now()
	started := providerStarted.UTC()
	deadline, _ := callCtx.Deadline()
	observation.Outcome = Outcome{Kind: "unconfirmed", Attempt: item.Attempt + 1, StartedAt: &started, QueueMS: max(0, started.Sub(item.ReadyAt).Milliseconds()), GenerationBudgetMS: claimed.BudgetMS, GenerationDeadline: &deadline}
	if item.Outcome.Kind == "rejected" {
		observation.Outcome.RetryWaitMS = observation.Outcome.QueueMS
		observation.Outcome.QueueMS = 0
	}
	if a.Logger != nil {
		a.Logger.InfoContext(ctx, "independent provider invocation started", "permit_id", id, "job_id", claimed.Ref.JobID, "item", claimed.Ref.Index, "generation_budget_ms", claimed.BudgetMS, "generation_deadline_at", deadline, "started_at", started)
	}
	nativeEntered = true
	result, executionErr := a.Execute(callCtx, claimed.Ref.Scope, request)
	finished := time.Now().UTC()
	finalizationDeadline = minTime(claimed.executionDeadline().Add(attemptFinalizationGrace), finished.Add(attemptFinalizationGrace))
	observation.LocalStopped = true // Execute has returned, not a lease/heartbeat inference.
	observation.Outcome.FinishedAt = &finished
	observation.Outcome.DurationMS = time.Since(providerStarted).Milliseconds()
	copyAttemptDiagnostics(&observation.Outcome, result)
	if executionErr != nil {
		observation.Outcome.Kind, observation.Outcome.Error, observation.Outcome.RetryAfterMS = classifyGenerationError(executionErr, result)
		base, _, _ := classifyAttemptError(executionErr)
		if base == "rejected" {
			observation.Outcome.Admission = "throttled"
		}
		if base == "policy" {
			observation.Outcome.Admission = "restricted"
		}
		var providerErr *hardenllm.ProviderError
		if !errors.As(executionErr, &providerErr) || providerErr.Status == 0 || errors.Is(executionErr, context.Canceled) || errors.Is(executionErr, context.DeadlineExceeded) {
			observation.Outcome.Kind = "unconfirmed"
		}
		return returned, nil
	}
	if finished.After(deadline) {
		observation.Outcome.Kind = "unconfirmed"
		observation.Outcome.Error = &ItemError{Code: "generation_deadline_exceeded", Message: "The invocation returned after its generation deadline."}
		return returned, nil
	}
	raw, err := a.Encode(item.Item.Generation, result, finished)
	if err != nil || !json.Valid(raw) || len(raw) > maximumResultArtifact {
		observation.Outcome.Kind = "failed"
		observation.Outcome.Error = &ItemError{Code: "result_encoding_failed", Message: "The result could not be encoded."}
		return returned, nil
	}
	observation.Outcome.Kind = "completed"
	observation.Outcome.ArtifactID = attemptArtifactID(id)
	observation.NativeEntered = true
	observation.ObservedAt = finished
	envelope := attemptArtifact{Version: 1, PermitID: id, PoolID: claimed.PoolID, ClaimID: nonce, Ref: claimed.Ref, Observation: observation, Response: raw}
	encoded, err := json.Marshal(envelope)
	if err != nil || len(encoded) > maximumResultArtifact {
		observation.Outcome.Kind = "failed"
		observation.Outcome.ArtifactID = ""
		observation.Outcome.Error = &ItemError{Code: "result_encoding_failed", Message: "The result exceeded its artifact limit."}
		return returned, nil
	}
	artifactCtx, stop := context.WithDeadline(context.WithoutCancel(ctx), finalizationDeadline)
	defer stop()
	if err = a.Artifacts.PutBatchResult(artifactCtx, claimed.Ref.Scope, claimed.Ref.JobID, observation.Outcome.ArtifactID, encoded); err != nil {
		recovered, recoverErr := readAttemptArtifact(artifactCtx, a.Artifacts, claimed)
		if recoverErr != nil || !sameTerminalObservation(recovered.Observation, observation) {
			observation.Outcome.Kind = "unconfirmed"
			observation.Outcome.ArtifactID = ""
			observation.Outcome.Error = &ItemError{Code: "result_storage_unconfirmed", Message: "The generated result's persistence is unconfirmed."}
		}
	}
	return returned, nil
}

// Only this invocation's fresh nonce can establish that its uncertain claim
// committed. No provider execution has occurred on this return path. A positive
// same-owner stop CAS can therefore stage never-dispatched evidence; missing or
// foreign ownership remains unconfirmed and cannot authorize another inference.
func (a *Activities) confirmNeverEnteredClaim(ctx context.Context, ledger ExecutionStore, id, nonce string) (InvocationResult, bool) {
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer stop()
	now := time.Now().UTC()
	retained, err := ledger.MarkLocalStop(cleanup, id, nonce, now)
	if err != nil || retained.ClaimID != nonce || retained.State == "reserved" || retained.State == "settled" || retained.DispatchedAt != nil {
		return InvocationResult{}, false
	}
	o := AttemptObservation{Outcome: Outcome{Kind: "never_dispatched", Error: &ItemError{Code: "claim_acknowledgement_lost", Message: "The invocation did not enter the provider path."}}, LocalStopped: true, ObservedAt: now}
	_, stageErr := ledger.StageAttempt(cleanup, id, nonce, o)
	if a.Logger != nil {
		a.Logger.InfoContext(cleanup, "claim acknowledgement recovered without provider entry", "permit_id", id, "pool_id", retained.PoolID, "native_entered", false, "local_stopped", true, "stage_confirmed", stageErr == nil)
	}
	return InvocationResult{Disposition: "observed", ClaimID: nonce, Observation: &o}, true
}

// A claim hint prompts authoritative ledger readback while a long native call
// is active. It cannot grant/release capacity and must not delay native entry.
// The activity owns and joins one bounded sender, including on panic/cancel.
func (a *Activities) startClaimReadHint(ctx context.Context, claimed Attempt) func() {
	if a.Temporal == nil {
		return func() {}
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)

		_ = a.Temporal.SignalWorkflow(bounded, "hllm-cpa-pool-v1/"+claimed.PoolID, "", settlementSignal, SettlementNotice{PermitID: claimed.PermitID, Revision: 0})
	}()
	return func() { cancel(); <-done }
}

func watchProviderShutdown(ctx context.Context, cancel context.CancelFunc) func() {
	if !activity.IsActivity(ctx) {
		return func() {}
	}
	stop, joined := make(chan struct{}), make(chan struct{})
	shutdown := activity.GetWorkerStopChannel(ctx)
	go func() {
		defer close(joined)
		select {
		case <-shutdown:
			cancel()
		case <-stop:
		}
	}()
	return func() { close(stop); <-joined }
}
func copyAttemptDiagnostics(out *Outcome, result hardenllm.Result) {
	for _, attempt := range result.Attempts {
		if attempt.Stream != nil {
			out.HeadersMS = attempt.Stream.HeadersMs
			out.FirstOutputMS = attempt.Stream.FirstOutputMs
			id := attempt.Stream.CorrelationID
			if len(id) <= 256 && !strings.ContainsFunc(id, func(r rune) bool { return r < 32 || r == 127 }) {
				out.ProviderRequestID = id
			} else {
				out.ProviderRequestID = ""
			}
		}
	}
	usage := result.Accounting.Provider.Usage
	if usage.Status == "complete" {
		out.InputTokens = &usage.PromptTokens
		out.OutputTokens = &usage.CompletionTokens
		out.ReasoningTokens = &usage.ReasoningTokens
		out.CachedInputTokens = &usage.CacheReadTokens
	}
}
func (a *Activities) attemptHeartbeat(ctx context.Context, attempt Attempt, progress <-chan hardenllm.ProgressEvent) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		var received int64
		changed := false
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case p := <-progress:
				if p.ReceivedBytes > received {
					received = p.ReceivedBytes
					changed = true
				}
			case <-ticker.C:
				if activity.IsActivity(ctx) {
					activity.RecordHeartbeat(ctx, map[string]any{"permitId": attempt.PermitID, "receivedBytes": received})
				}
				if store, ok := a.Store.(PoolProgressStore); ok {
					_ = store.HeartbeatAttempt(ctx, attempt, changed)
				} else {
					_ = a.Store.Heartbeat(ctx, attempt.Ref, changed)
				}
				changed = false
			}
		}
	}()
	return func() { close(stop); <-done }
}
func decodeAttemptArtifact(raw []byte, attempt Attempt) (attemptArtifact, error) {
	var artifact attemptArtifact
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > maximumResultArtifact || decoder.Decode(&artifact) != nil {
		return artifact, ErrSettlementConflict
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return artifact, ErrSettlementConflict
	}
	o := artifact.Observation
	if artifact.Version != 1 || artifact.PoolID != attempt.PoolID || artifact.PermitID != attempt.PermitID || artifact.ClaimID != attempt.ClaimID || artifact.Ref != attempt.Ref || !validPermitID(artifact.ClaimID) || o.Outcome.Kind != "completed" || o.Outcome.ArtifactID != attemptArtifactID(attempt.PermitID) || o.Outcome.FinishedAt == nil || o.Outcome.FinishedAt.After(attempt.executionDeadline()) || !o.LocalStopped || !o.NativeEntered || !json.Valid(artifact.Response) || !validObservation(o) {
		return artifact, ErrSettlementConflict
	}
	if o.Outcome.StartedAt == nil || o.Outcome.FinishedAt.Before(*o.Outcome.StartedAt) || o.ObservedAt.Before(*o.Outcome.FinishedAt) || (attempt.ClaimedAt != nil && o.Outcome.StartedAt.Before(*attempt.ClaimedAt)) {
		return artifact, ErrSettlementConflict
	}
	return artifact, nil
}

func (s *Service) attemptResponse(ctx context.Context, raw []byte, ref Ref, outcome Outcome) (json.RawMessage, error) {
	ledger, ok := s.Store.(ExecutionStore)
	if !ok {
		return nil, ErrUnavailable
	}
	var header struct {
		PoolID   string `json:"pool_id"`
		PermitID string `json:"permit_id"`
	}
	if json.Unmarshal(raw, &header) != nil || !validPool(header.PoolID) || !validPermitID(header.PermitID) || attemptArtifactID(header.PermitID) != outcome.ArtifactID {
		return nil, ErrSettlementConflict
	}
	attempts, err := ledger.ReadAttempts(ctx, header.PoolID, []string{header.PermitID})
	if err != nil {
		return nil, err
	}
	if len(attempts) != 1 || attempts[0].Ref != ref || attempts[0].Kind != "job" || attempts[0].State != "settled" || attempts[0].Observation == nil {
		return nil, ErrSettlementConflict
	}
	artifact, err := decodeAttemptArtifact(raw, attempts[0])
	if err != nil {
		return nil, err
	}
	// The artifact is evidence for this authoritative settlement, never a source
	// that can replace a different finalized outcome.
	if !sameObservation(AttemptObservation{Outcome: artifact.Observation.Outcome}, AttemptObservation{Outcome: outcome}) {
		return nil, ErrSettlementConflict
	}
	return artifact.Response, nil
}
func readAttemptArtifact(ctx context.Context, artifacts Artifacts, attempt Attempt) (attemptArtifact, error) {
	var empty attemptArtifact
	reader, err := artifacts.OpenBatchResult(ctx, attempt.Ref.Scope, attempt.Ref.JobID, attemptArtifactID(attempt.PermitID))
	if err != nil {
		return empty, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(reader, maximumResultArtifact+1))
	closeErr := reader.Close()
	if readErr != nil {
		return empty, readErr
	}
	if closeErr != nil {
		return empty, closeErr
	}
	return decodeAttemptArtifact(raw, attempt)
}
