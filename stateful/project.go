package stateful

import (
	"context"
	"errors"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/accounting"
	"slices"
	"time"
)

type ExecutionTarget struct {
	ProfileID string `json:"profileId"`
	Provider  string `json:"provider"`
	Protocol  string `json:"protocol"`
	Endpoint  string `json:"endpoint"`
	ModelID   string `json:"modelId"`
}
type ResultSource struct {
	Kind          hardenllm.ResultSourceKind `json:"kind"`
	AttemptNumber int                        `json:"attemptNumber,omitempty"`
	Producer      *ExecutionTarget           `json:"producer,omitempty"`
}
type BranchCache struct {
	Branch           string                `json:"branch"`
	GenerationTarget ExecutionTarget       `json:"generationTarget"`
	Cache            hardenllm.CacheResult `json:"cache"`
}
type RunDiagnostics struct {
	hardenllm.Diagnostics
	BranchCaches []BranchCache `json:"branchCaches,omitempty"`
}

type Attempt struct {
	Number            int                          `json:"number"`
	ProfileID         string                       `json:"profileId"`
	Target            ExecutionTarget              `json:"target"`
	Category          string                       `json:"category,omitempty"`
	HTTPStatus        int                          `json:"httpStatus,omitempty"`
	Code              string                       `json:"code,omitempty"`
	Type              string                       `json:"type,omitempty"`
	ProviderRequestID string                       `json:"providerRequestId,omitempty"`
	Retryable         bool                         `json:"retryable"`
	Wait              time.Duration                `json:"wait"`
	Duration          time.Duration                `json:"duration"`
	Repair            bool                         `json:"repair"`
	ProviderUsed      bool                         `json:"providerUsed"`
	Stage             string                       `json:"stage,omitempty"`
	Branch            string                       `json:"branch,omitempty"`
	TriggerAttempt    int                          `json:"triggerAttemptNumber,omitempty"`
	InputAttempts     []int                        `json:"inputAttemptNumbers,omitempty"`
	TransportRetryOf  int                          `json:"transportRetryOfAttempt,omitempty"`
	StartedAt         *time.Time                   `json:"startedAt,omitempty"`
	FinishedAt        *time.Time                   `json:"finishedAt,omitempty"`
	ReasoningEffort   string                       `json:"reasoningEffort,omitempty"`
	DispatchObserved  bool                         `json:"dispatchObserved"`
	Stream            *hardenllm.StreamDiagnostics `json:"stream,omitempty"`
	WaitDiagnostics   *hardenllm.WaitDiagnostics   `json:"waitDiagnostics,omitempty"`
}

func projectTarget(target hardenllm.ExecutionTarget, labels map[string]string) ExecutionTarget {
	return ExecutionTarget{ProfileID: labels[target.ConnectionID], Provider: target.Provider, Protocol: target.Protocol, Endpoint: target.Endpoint, ModelID: target.ModelID}
}
func projectRun(runID string, input RunInput, resolved resolvedRun, result hardenllm.Result, err error, started, finished time.Time) RecordedRunOutput {
	status := "succeeded"
	if err != nil {
		status = "failed"
		if errors.Is(err, context.DeadlineExceeded) {
			status = "timeout"
		}
	}
	out := RecordedRunOutput{SchemaVersion: 4, RunID: runID, Status: status, Output: result.Output, CallID: result.CallID, TraceID: result.TraceID, Origin: input.Origin, Search: result.Search, Accounting: result.Accounting, Cache: result.Cache, Attempts: []Attempt{}, Artifacts: []ArtifactMetadata{}, ResultSource: ResultSource{Kind: result.ResultSource.Kind, AttemptNumber: result.ResultSource.AttemptNumber}, TotalCallDurationMs: max(finished.Sub(started).Milliseconds(), 0), TotalActualWaitMs: result.Diagnostics.TotalActualWaitMs}
	if out.Cache.Mode == "" {
		out.Cache.Mode = input.CacheMode
		if out.Cache.Mode == "" {
			out.Cache.Mode = hardenllm.CacheModeOff
		}
		out.Cache.Status = "bypassed"
	}
	if out.ResultSource.Kind == "" {
		out.ResultSource.Kind = hardenllm.ResultSourceNone
		empty := hardenllm.ProjectLedger(accounting.EmptyLedger())
		out.Accounting = hardenllm.Accounting{Result: empty, Provider: empty}
	}
	if result.SelectedTarget.ConnectionID != "" {
		target := projectTarget(result.SelectedTarget, resolved.labels)
		out.SelectedTarget = &target
	} else if c, ok := resolved.connections["generation"]; ok {
		out.SelectedTarget = &ExecutionTarget{ProfileID: input.ProfileID, Provider: c.Provider, Protocol: c.Protocol, Endpoint: c.BaseURL, ModelID: resolved.request.ModelID}
	}
	if result.ResultSource.Producer != nil {
		target := projectTarget(*result.ResultSource.Producer, resolved.labels)
		out.ResultSource.Producer = &target
	}
	for _, a := range result.Attempts {
		out.Attempts = append(out.Attempts, Attempt{Number: a.Number, ProfileID: resolved.labels[a.ConnectionID], Target: projectTarget(a.Target, resolved.labels), Category: a.Category, HTTPStatus: a.HTTPStatus, Code: a.Code, Type: a.Type, ProviderRequestID: a.ProviderRequestID, Retryable: a.Retryable, Wait: a.Wait, Duration: a.Duration, Repair: a.Repair, ProviderUsed: a.ProviderUsed, Stage: a.Stage, Branch: a.Branch, TriggerAttempt: a.TriggerAttempt, InputAttempts: slices.Clone(a.InputAttempts), TransportRetryOf: a.TransportRetryOf, StartedAt: a.StartedAt, FinishedAt: a.FinishedAt, ReasoningEffort: resolved.reasoning[a.ConnectionID], DispatchObserved: a.DispatchObserved, Stream: a.Stream, WaitDiagnostics: a.WaitDiagnostics})
		out.TotalWaitMs += a.Wait.Milliseconds()
		out.UsedRepair = out.UsedRepair || a.Repair
		out.ProviderInvoked = out.ProviderInvoked || a.ProviderUsed
	}
	if input.TimeoutMS > 0 {
		out.OverBudgetMs = max(out.TotalCallDurationMs-int64(input.TimeoutMS), 0)
	}
	if result.Diagnostics.StopReason != "" || result.Diagnostics.AttemptsUsed > 0 || result.Diagnostics.ReceivedBytes > 0 {
		out.Diagnostics = &RunDiagnostics{Diagnostics: result.Diagnostics}
		for _, branch := range result.Diagnostics.BranchCaches {
			out.Diagnostics.BranchCaches = append(out.Diagnostics.BranchCaches, BranchCache{Branch: branch.Branch, GenerationTarget: projectTarget(branch.GenerationTarget, resolved.labels), Cache: branch.Cache})
		}
		target := projectTarget(result.GenerationTarget, resolved.labels)
		out.GenerationTarget = &target
		out.StopReason = result.Diagnostics.StopReason
	}
	return out
}
func historyRunSummary(input RunInput, out RunOutput, started, finished time.Time) HistorySummary {
	model := input.ModelID
	if out.SelectedTarget != nil {
		model = out.SelectedTarget.ModelID
	}
	return HistorySummary{RunID: out.RunID, ProfileID: input.ProfileID, ModelID: model, Status: out.Status, StartedAt: started, FinishedAt: finished, CacheServed: out.Cache.Served, ResultSource: string(out.ResultSource.Kind), ProviderTokens: out.Accounting.Provider.Usage.TotalTokens, ResultTokens: out.Accounting.Result.Usage.TotalTokens, KnownCostUSD: out.Accounting.Provider.Cost.KnownSubtotalUSD, KnownCostObservations: out.Accounting.Provider.Cost.KnownObservations, UnknownCostObservations: out.Accounting.Provider.Cost.UnknownObservations, DurationMS: out.TotalCallDurationMs}
}
