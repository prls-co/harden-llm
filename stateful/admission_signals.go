package stateful

import (
	"context"
	"github.com/prls-co/harden-llm/stateful/batch"
	"sync"
	"time"
)

type admissionWake struct {
	done chan struct{}
	err  error
}

// Wake is only a hint to scan committed live waiters. Coalescing it never grants
// inference permission: every caller must still positively claim its own permit.
// The pool's bounded manifest scan covers a row committed after a recent wake.
type admissionPoolSignals struct {
	next    batch.RequestPoolSignals
	mu      sync.Mutex
	pool    string
	lastAck time.Time
	flight  *admissionWake
}

func (s *admissionPoolSignals) Wake(ctx context.Context, pool string) error {
	s.mu.Lock()
	if s.pool == pool && !s.lastAck.IsZero() && time.Since(s.lastAck) < 50*time.Millisecond {
		s.mu.Unlock()
		return ctx.Err()
	}
	if s.flight != nil && s.pool == pool {
		flight := s.flight
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-flight.done:
			return flight.err
		}
	}
	// A configured admission instance has exactly one trusted pool identity.
	if s.flight != nil {
		s.mu.Unlock()
		return batch.ErrGenerationFenced
	}
	flight := &admissionWake{done: make(chan struct{})}
	s.flight = flight
	s.pool = pool
	s.mu.Unlock()
	err := s.next.Wake(ctx, pool)
	s.mu.Lock()
	flight.err = err
	if err == nil {
		s.lastAck = time.Now()
	}
	s.flight = nil
	close(flight.done)
	s.mu.Unlock()
	return err
}
func (s *admissionPoolSignals) Changed(ctx context.Context, pool string, notice batch.SettlementNotice) error {
	return s.next.Changed(ctx, pool, notice)
}
