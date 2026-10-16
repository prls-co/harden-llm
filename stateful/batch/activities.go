package batch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type TemporalStarter struct {
	Client    client.Client
	TaskQueue string
}

func (t TemporalStarter) Start(ctx context.Context, j JobRef) error {
	_, err := t.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: j.WorkflowID, TaskQueue: t.TaskQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, BatchWorkflowName, j)
	var duplicate *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &duplicate) {
		return nil
	}
	return err
}

type Activities struct {
	Store     Store
	Artifacts Artifacts
	Execute   Executor
	Encode    Encoder
	Temporal  client.Client
	TaskQueue string
	PoolID    string
	Policy    Policy
	Logger    *slog.Logger
}

func (a *Activities) Enqueue(ctx context.Context, j JobRef) error {
	_, err := a.Temporal.SignalWithStartWorkflow(ctx, "hllm-cpa-pool-v1/"+a.PoolID, enqueueSignal, j, client.StartWorkflowOptions{TaskQueue: a.TaskQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE}, DispatcherWorkflowName, DispatcherState{Controller: NewController(a.Policy)})
	return err
}
func (a *Activities) Status(ctx context.Context, j JobRef) (Status, error) {
	return a.Store.Status(ctx, j.Scope, j.JobID)
}
func (a *Activities) Expire(ctx context.Context, j JobRef) error { return a.Store.Expire(ctx, j) }
func (a *Activities) Record(ctx context.Context, r Ref, o Outcome) (Counts, error) {
	return a.Store.Record(ctx, r, o)
}
func (a *Activities) Cooldown(ctx context.Context, u CooldownUpdate) error {
	return a.Store.Cooldown(ctx, u.Jobs, u.Reason, u.Next)
}

var safeErrorCode = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (a *Activities) Generate(ctx context.Context, r Ref) (out Outcome, returnErr error) {
	started := time.Now()
	out.Kind = "failed"
	defer func() {
		if a.Logger != nil {
			code := ""
			status := 0
			if out.Error != nil {
				code = out.Error.Code
				status = out.Error.HTTPStatus
			}
			level := slog.LevelInfo
			if out.Kind == "policy" {
				level = slog.LevelError
			}
			a.Logger.Log(ctx, level, "batch provider attempt completed", "job_id", r.JobID, "item", r.Index, "attempt", out.Attempt, "kind", out.Kind, "code", code, "http_status", status, "cpa_request_id", out.ProviderRequestID, "retry_after_ms", out.RetryAfterMS, "started_at", out.StartedAt, "finished_at", out.FinishedAt, "retry_wait_ms", out.RetryWaitMS, "queue_ms", out.QueueMS, "generation_ms", out.DurationMS, "headers_ms", out.HeadersMS, "first_output_ms", out.FirstOutputMS, "input_tokens", out.InputTokens, "output_tokens", out.OutputTokens, "reasoning_tokens", out.ReasoningTokens, "cpa_internal_attempts", out.CPAInternalAttempts)
		}
	}()
	item, err := a.Store.Begin(ctx, r)
	if err != nil {
		return Outcome{Kind: "failed", Error: &ItemError{Code: "item_unavailable", Message: "Could not start the stored item."}}, nil
	}
	if item.State == "succeeded" || item.State == "failed" {
		return item.Outcome, nil
	}
	out.Attempt = item.Attempt
	out.QueueMS = max(0, started.Sub(item.ReadyAt).Milliseconds())
	if item.WasWaiting {
		out.RetryWaitMS = out.QueueMS
		out.QueueMS = 0
	}
	if !started.Before(item.Deadline) {
		out.Error = &ItemError{Code: "job_deadline_exceeded", Message: "The job deadline expired."}
		return out, nil
	}
	providerTimeout := 180 * time.Second
	if item.Item.Generation.TimeoutMS > 0 {
		providerTimeout = min(providerTimeout, time.Duration(item.Item.Generation.TimeoutMS)*time.Millisecond)
	}
	callCtx, cancel := context.WithTimeout(ctx, min(providerTimeout, time.Until(item.Deadline)))
	defer cancel()
	progress := make(chan hardenllm.ProgressEvent, 8)
	request := item.Item.Generation.Request()
	request.Origin = hardenllm.Origin{Client: "harden-llm-batch", JobID: r.JobID}
	request.Progress = progress
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		var bytes int64
		changed := false
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case p := <-progress:
				if p.ReceivedBytes > bytes {
					bytes = p.ReceivedBytes
					changed = true
				}
			case <-ticker.C:
				activity.RecordHeartbeat(ctx, map[string]any{"jobId": r.JobID, "item": r.Index, "receivedBytes": bytes})
				_ = a.Store.Heartbeat(ctx, r, changed)
				changed = false
			}
		}
	}()
	providerStarted := time.Now()
	at := providerStarted.UTC()
	out.StartedAt = &at
	if a.Logger != nil {
		a.Logger.InfoContext(ctx, "batch provider attempt started", "job_id", r.JobID, "item", r.Index, "attempt", out.Attempt, "started_at", at)
	}
	result, err := a.Execute(callCtx, r.Scope, request)
	finished := time.Now().UTC()
	out.FinishedAt = &finished
	out.DurationMS = time.Since(providerStarted).Milliseconds()
	for _, attempt := range result.Attempts {
		if attempt.Stream != nil {
			out.FirstOutputMS = attempt.Stream.FirstOutputMs
			out.HeadersMS = attempt.Stream.HeadersMs
			out.ProviderRequestID = attempt.Stream.CorrelationID
		}
	}
	u := result.Accounting.Provider.Usage
	if u.Status == "complete" {
		out.InputTokens = &u.PromptTokens
		out.OutputTokens = &u.CompletionTokens
		out.ReasoningTokens = &u.ReasoningTokens
		out.CachedInputTokens = &u.CacheReadTokens
	}
	if err != nil {
		code := "upstream_error"
		status := 0
		var p *hardenllm.ProviderError
		if errors.As(err, &p) {
			status = p.Status
			if safeErrorCode.MatchString(p.Code) {
				code = p.Code
			}
			if p.RetryAfter > 0 {
				out.RetryAfterMS = p.RetryAfter.Milliseconds()
				if p.RetryAfter%time.Millisecond != 0 {
					out.RetryAfterMS++
				}
			}
			switch {
			case status == 401 || status == 403 || code == "account_deactivated" || code == "policy_violation" || code == "insufficient_quota" || code == "billing_hard_limit_reached" || code == "billing_not_active":
				out.Kind = "policy"
			case status == 429 || code == "rate_limit_exceeded" || code == "model_cooldown" || code == "auth_unavailable":
				out.Kind = "rejected"
			}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code = "provider_timeout"
		}
		out.Error = &ItemError{Code: code, HTTPStatus: status, Message: "The provider request did not produce a validated result."}
		return out, nil
	}
	raw, err := a.Encode(item.Item.Generation, result, time.Now().UTC())
	if err != nil || !json.Valid(raw) {
		out.Error = &ItemError{Code: "result_encoding_failed", Message: "The result could not be encoded."}
		return out, nil
	}
	id := fmt.Sprintf("batch-%04d-%04d.json", r.Index, item.Attempt)
	storeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer stop()
	if err = a.Artifacts.PutBatchResult(storeCtx, r.Scope, r.JobID, id, raw); err != nil {
		out.Error = &ItemError{Code: "result_storage_failed", Message: "The generated result could not be stored."}
		return out, nil
	}
	out.Kind = "completed"
	out.ArtifactID = id
	return out, nil
}
func NewWorker(c client.Client, queue string, a *Activities) (worker.Worker, error) {
	if c == nil || queue == "" || a == nil || a.Store == nil || a.Artifacts == nil || a.Execute == nil || a.Encode == nil || a.PoolID == "" {
		return nil, ErrUnavailable
	}
	if err := a.Policy.Validate(); err != nil {
		return nil, err
	}
	w := worker.New(c, queue, worker.Options{MaxConcurrentActivityExecutionSize: a.Policy.Concurrency + 16, WorkerStopTimeout: 15 * time.Second, DisableRegistrationAliasing: true})
	w.RegisterWorkflowWithOptions(BatchWorkflow, workflow.RegisterOptions{Name: BatchWorkflowName})
	w.RegisterWorkflowWithOptions(DispatcherWorkflow, workflow.RegisterOptions{Name: DispatcherWorkflowName})
	w.RegisterActivity(a)
	return w, nil
}
