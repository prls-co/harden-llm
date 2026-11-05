package batch

import (
	"context"
	"sync"
	"time"
)

const poolWakeCollection = 50 * time.Millisecond
const poolWakeMaximum = time.Second

type poolWakeBatch struct {
	notice SettlementNotice
	done   chan struct{}
	err    error
}

// One Activities instance has one trusted pool. There is at most one collecting
// batch and one in-flight RPC; no daemon, per-permit map or retained success cache.
// A caller arriving after sealing must join a new batch, even while the old RPC
// is in flight. Every hint asks the actor to read all outstanding SQL entries.
type poolWakeHints struct {
	mu      sync.Mutex
	pool    string
	pending *poolWakeBatch
	rpc     chan struct{}
}

func (a *Activities) coalescePoolRead(ctx context.Context, pool string, notice SettlementNotice) error {
	if a.Temporal == nil || pool == "" || (a.PoolID != "" && pool != a.PoolID) {
		return ErrGenerationFenced
	}
	return a.wakeHints.send(ctx, pool, notice, func(bounded context.Context, n SettlementNotice) error {
		return a.Temporal.SignalWorkflow(bounded, "hllm-cpa-pool-v1/"+pool, "", settlementSignal, n)
	})
}

func (s *poolWakeHints) send(ctx context.Context, pool string, notice SettlementNotice, signal func(context.Context, SettlementNotice) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.pool != "" && s.pool != pool {
		s.mu.Unlock()
		return ErrGenerationFenced
	}
	s.pool = pool
	if s.rpc == nil {
		s.rpc = make(chan struct{}, 1)
	}
	batch := s.pending
	leader := batch == nil
	if leader {
		batch = &poolWakeBatch{done: make(chan struct{})}
		s.pending = batch
	}
	batch.notice = notice
	s.mu.Unlock()
	if leader {
		// The existing caller owns this bounded operation. Its cancellation must
		// not discard followers' already-committed hints. Its earlier deadline
		// still bounds collection, RPC admission and the actual signal together.
		deadline := time.Now().Add(poolWakeMaximum)
		if earlier, ok := ctx.Deadline(); ok && earlier.Before(deadline) {
			deadline = earlier
		}
		owned, stop := context.WithDeadline(context.WithoutCancel(ctx), deadline)
		s.flush(owned, batch, signal)
		stop()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-batch.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return batch.err
	}
}

func (s *poolWakeHints) flush(ctx context.Context, batch *poolWakeBatch, signal func(context.Context, SettlementNotice) error) {
	finish := func(err error) {
		s.mu.Lock()
		if s.pending == batch {
			s.pending = nil
		}
		batch.err = err
		close(batch.done)
		s.mu.Unlock()
	}
	select {
	case s.rpc <- struct{}{}:
		defer func() { <-s.rpc }()
	case <-ctx.Done():
		finish(ctx.Err())
		return
	}
	timer := time.NewTimer(poolWakeCollection)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		finish(ctx.Err())
		return
	case <-timer.C:
	}
	s.mu.Lock()
	// Seal before starting the RPC. Later arrivals need a trailing wake because
	// the actor may have read SQL before their claim/settlement was committed.
	s.pending = nil
	notice := batch.notice
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		finish(err)
		return
	}
	finish(signal(ctx, notice))
}
