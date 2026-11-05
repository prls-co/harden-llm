package batch

import (
	"context"
	"errors"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/retry"
)

type unconfirmedDispatchError struct{ error }

func (e unconfirmedDispatchError) Unwrap() error              { return e.error }
func (e unconfirmedDispatchError) InferenceUnconfirmed() bool { return true }

// ObserveProviderDispatch records one concrete ordinary dispatch, not its final
// hardened result. No request/output text is retained. Ambiguous transport or
// partial refusal cannot authorize an automatic core retry.
func ObserveProviderDispatch(r hardenllm.ProviderResult, err error) (Outcome, error) {
	o := Outcome{Kind: "completed", HeadersMS: r.Stream.HeadersMs, FirstOutputMS: r.Stream.FirstOutputMs, ProviderRequestID: r.Stream.CorrelationID}
	u := r.Accounting.Usage
	if u.Status == "complete" {
		input, output := u.PromptTokens(), u.CompletionTokens()
		o.InputTokens = &input
		o.OutputTokens = &output
		o.ReasoningTokens = &u.ReasoningTokens
		o.CachedInputTokens = &u.CacheReadTokens
	}
	if err == nil {
		return o, nil
	}
	o.Kind, o.Error, o.RetryAfterMS = classifyGenerationError(err, hardenllm.Result{AssistantText: r.AssistantText, Diagnostics: hardenllm.Diagnostics{OutputBytes: r.Stream.OutputBytes, OutputCodePoints: r.Stream.OutputCodePoints}})
	base, _, _ := classifyAttemptError(err)
	if base == "rejected" {
		o.Admission = "throttled"
	} else if base == "policy" {
		o.Admission = "restricted"
	}
	var p *hardenllm.ProviderError
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &p) && p.Category == retry.CategoryNetwork) {
		o.Kind = "unconfirmed"
	}
	if o.Kind == "unconfirmed" {
		if errors.As(err, &p) {
			copy := *p
			copy.Category = retry.CategoryOther
			err = &copy
		}
		err = unconfirmedDispatchError{error: err}
	}
	return o, err
}
