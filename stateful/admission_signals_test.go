package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-521

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/stateful/batch"
)

type admissionSignalStub struct {
	wake    func(context.Context, string) error
	changed func(context.Context, string, batch.SettlementNotice) error
}

func (s admissionSignalStub) Wake(ctx context.Context, pool string) error { return s.wake(ctx, pool) }
func (s admissionSignalStub) Changed(ctx context.Context, pool string, notice batch.SettlementNotice) error {
	return s.changed(ctx, pool, notice)
}

func TestAdmissionWakeCoalescesHintsAndPreservesSettlementNotices(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls, changed atomic.Int32
	s := &admissionPoolSignals{next: admissionSignalStub{
		wake: func(ctx context.Context, pool string) error {
			if pool != "pool" {
				t.Error("changed trusted pool")
			}
			if calls.Add(1) == 1 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		},
		changed: func(_ context.Context, pool string, n batch.SettlementNotice) error {
			if pool != "pool" || n.PermitID != "permit" {
				t.Error("changed settlement identity")
			}
			changed.Add(1)
			return nil
		},
	}}
	done := make(chan error, 1)
	go func() { done <- s.Wake(ctx, "pool") }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A queued follower must observe its own cancellation without issuing a
	// second RPC or canceling the live leader.
	stopped, stop := context.WithCancel(ctx)
	stop()
	if err := s.Wake(stopped, "pool"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := s.Wake(ctx, "other-pool"); !errors.Is(err, batch.ErrGenerationFenced) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("overlapping hints made duplicate RPCs", calls.Load())
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Hold the production mutex around the test timestamp only; no sleeps or
	// provider replays are involved in checking the successful-ack window.
	for range 32 {
		s.mu.Lock()
		s.lastAck = time.Now()
		s.mu.Unlock()
		if err := s.Wake(ctx, "pool"); err != nil {
			t.Fatal(err)
		}
		if err := s.Changed(ctx, "pool", batch.SettlementNotice{PermitID: "permit"}); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 || changed.Load() != 32 {
		t.Fatal("coalescing dropped settlements or duplicated wake", calls.Load(), changed.Load())
	}
	s.mu.Lock()
	s.lastAck = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if err := s.Wake(ctx, "pool"); err != nil || calls.Load() != 2 {
		t.Fatal("expired hint was cached", err, calls.Load())
	}
}

func TestAdmissionWakeDoesNotCacheFailure(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	want := errors.New("wake unconfirmed")
	s := &admissionPoolSignals{next: admissionSignalStub{wake: func(context.Context, string) error {
		if calls.Add(1) == 1 {
			return want
		}
		return nil
	}}}
	if err := s.Wake(context.Background(), "pool"); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if err := s.Wake(context.Background(), "pool"); err != nil || calls.Load() != 2 {
		t.Fatal("failed ack suppressed next metadata hint", err, calls.Load())
	}
}
