package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-494

import (
	"context"
	"errors"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
)

type trackingObserverSpy struct {
	starts, ends map[string]int
	returned     context.Context
	err          error
}

func (s *trackingObserverSpy) begin(name string) (context.Context, func(error)) {
	s.starts[name]++
	return s.returned, func(err error) {
		s.ends[name]++
		if !errors.Is(err, s.err) {
			panic("observer callback error changed")
		}
	}
}
func (s *trackingObserverSpy) StartCall(context.Context, hardenllm.CallObservation) (context.Context, func(hardenllm.CallSummary, error)) {
	ctx, end := s.begin("call")
	return ctx, func(_ hardenllm.CallSummary, err error) { end(err) }
}
func (s *trackingObserverSpy) StartRuntime(context.Context, hardenllm.CallObservation) (context.Context, func(error)) {
	return s.begin("runtime")
}
func (s *trackingObserverSpy) StartProvider(context.Context, hardenllm.ObserverTarget, string) (context.Context, func(bool, error)) {
	ctx, end := s.begin("provider")
	return ctx, func(_ bool, err error) { end(err) }
}
func (s *trackingObserverSpy) StartAttempt(context.Context, hardenllm.ObserverTarget, string, int) (context.Context, func(error)) {
	return s.begin("attempt")
}
func (s *trackingObserverSpy) StartRetry(context.Context, hardenllm.ObserverTarget, string, hardenllm.RetryClassification, time.Duration) (context.Context, func(error)) {
	return s.begin("retry")
}
func (s *trackingObserverSpy) StartValidation(context.Context, coreruntime.Connection, bool) (context.Context, func(error)) {
	return s.begin("validation")
}
func (s *trackingObserverSpy) StartCache(context.Context, string) (context.Context, func(string, error)) {
	ctx, end := s.begin("cache")
	return ctx, func(_ string, err error) { end(err) }
}

func TestRequestTrackingObserverDelegation(t *testing.T) {
	t.Parallel()
	for _, tracked := range []bool{false, true} {
		t.Run(map[bool]string{false: "untracked", true: "tracked"}[tracked], func(t *testing.T) {
			now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
			record, _ := NewTrackingRecord("a60f21da-604f-4fe3-a039-23382ec6b313", "/v1/responses", now, now.Add(time.Minute))
			session := NewTrackingSession(record, func() time.Time { return now })
			ctx := context.Background()
			if tracked {
				ctx = WithTrackingSession(ctx, session)
			}
			spy := &trackingObserverSpy{starts: map[string]int{}, ends: map[string]int{}, returned: context.WithValue(ctx, trackingObserverTestKey{}, "original context"), err: errors.New("synthetic callback")}
			observer := trackingObserver{next: spy}
			check := func(got context.Context) {
				if got != spy.returned {
					t.Fatal("wrapped observer context replaced")
				}
			}
			c, callEnd := observer.StartCall(ctx, hardenllm.CallObservation{})
			check(c)
			callEnd(hardenllm.CallSummary{}, spy.err)
			c, end := observer.StartRuntime(ctx, hardenllm.CallObservation{})
			check(c)
			end(spy.err)
			c, providerEnd := observer.StartProvider(ctx, hardenllm.ObserverTarget{}, "text")
			check(c)
			providerEnd(true, spy.err)
			c, end = observer.StartAttempt(ctx, hardenllm.ObserverTarget{}, "text", 1)
			check(c)
			end(spy.err)
			c, end = observer.StartValidation(ctx, coreruntime.Connection{}, false)
			check(c)
			end(spy.err)
			c, cacheEnd := observer.StartCache(ctx, "read")
			check(c)
			cacheEnd("miss", spy.err)
			c, end = observer.StartRetry(ctx, hardenllm.ObserverTarget{}, "text", hardenllm.RetryClassification{Category: "rate_limit"}, 5*time.Second)
			check(c)
			if tracked {
				waiting := session.Snapshot(false)
				if waiting.Status != "waiting" || waiting.Reason == nil || *waiting.Reason != "provider_throttled" || waiting.NextAttemptAt == nil || !waiting.NextAttemptAt.Equal(now.Add(5*time.Second)) {
					t.Fatal("actual retry not observed", waiting)
				}
			}
			end(spy.err)
			for _, name := range []string{"call", "runtime", "provider", "attempt", "validation", "cache", "retry"} {
				if spy.starts[name] != 1 || spy.ends[name] != 1 {
					t.Fatal("observer forwarding count", name, spy.starts[name], spy.ends[name])
				}
			}
			snapshot := session.Snapshot(false)
			if tracked {
				if snapshot.Status != "running" || snapshot.Reason != nil || snapshot.NextAttemptAt != nil {
					t.Fatal("wait exit not observed")
				}
			} else if snapshot.Revision != record.Revision {
				t.Fatal("untracked observer did tracking work")
			}
		})
	}
}

type trackingObserverTestKey struct{}
