package stateful

import (
	"context"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
)

type trackingSessionKey struct{}

func WithTrackingSession(ctx context.Context, s *TrackingSession) context.Context {
	return context.WithValue(ctx, trackingSessionKey{}, s)
}
func trackingSession(ctx context.Context) *TrackingSession {
	s, _ := ctx.Value(trackingSessionKey{}).(*TrackingSession)
	return s
}

type trackingObserver struct{ next hardenllm.Observer }

func (o trackingObserver) StartCall(ctx context.Context, v hardenllm.CallObservation) (context.Context, func(hardenllm.CallSummary, error)) {
	return o.next.StartCall(ctx, v)
}
func (o trackingObserver) StartRuntime(ctx context.Context, v hardenllm.CallObservation) (context.Context, func(error)) {
	return o.next.StartRuntime(ctx, v)
}
func (o trackingObserver) StartProvider(ctx context.Context, t hardenllm.ObserverTarget, c string) (context.Context, func(bool, error)) {
	if s := trackingSession(ctx); s != nil {
		s.Phase("provider_attempt")
	}
	return o.next.StartProvider(ctx, t, c)
}
func (o trackingObserver) StartAttempt(ctx context.Context, t hardenllm.ObserverTarget, c string, n int) (context.Context, func(error)) {
	if s := trackingSession(ctx); s != nil {
		s.Phase("provider_attempt")
		s.Attempt(n)
	}
	return o.next.StartAttempt(ctx, t, c, n)
}
func (o trackingObserver) StartRetry(ctx context.Context, t hardenllm.ObserverTarget, c string, v hardenllm.RetryClassification, d time.Duration) (context.Context, func(error)) {
	next, finish := o.next.StartRetry(ctx, t, c, v, d)
	if s := trackingSession(ctx); s != nil {
		reason := "retry_backoff"
		if v.Category == "rate_limit" {
			reason = "provider_throttled"
		}
		end := s.Wait(reason, d)
		return next, func(err error) { end(); finish(err) }
	}
	return next, finish
}
func (o trackingObserver) StartValidation(ctx context.Context, c coreruntime.Connection, repair bool) (context.Context, func(error)) {
	if s := trackingSession(ctx); s != nil {
		s.Phase("validation")
	}
	return o.next.StartValidation(ctx, c, repair)
}
func (o trackingObserver) StartCache(ctx context.Context, op string) (context.Context, func(string, error)) {
	return o.next.StartCache(ctx, op)
}
