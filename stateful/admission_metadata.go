package stateful

import (
	"context"
	"github.com/prls-co/harden-llm/stateful/batch"
	"time"
)

// Bound database work before its short transaction deadline begins. Callers may
// wait under their original request context; no slot spans native execution.
// This is per API instance and is independent of the global provider permit cap.
// Two slots serve enqueue/poll work; two separate slots serve existing permits
// and cancellation. A burst of ungranted waiters cannot starve a five-second
// claim/dispatch lease or known-outcome settlement.
type admissionMetadataStore struct {
	batch.RequestQueueStore
	slots  chan struct{}
	urgent chan struct{}
}

func (s *admissionMetadataStore) run(ctx context.Context, fn func() error) error {
	select {
	case s.slots <- struct{}{}:
	default:
		if session := trackingSession(ctx); session != nil {
			session.AdmissionWait("persistence_wait", nil)
		}
		select {
		case s.slots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer func() { <-s.slots }()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fn()
}
func (s *admissionMetadataStore) PoolGeneration(ctx context.Context, pool string) (out batch.PoolGeneration, err error) {
	err = s.run(ctx, func() error { var e error; out, e = s.RequestQueueStore.PoolGeneration(ctx, pool); return e })
	return
}
func (s *admissionMetadataStore) EnqueueRequest(ctx context.Context, w batch.RequestWaiter) (out batch.RequestWaiter, err error) {
	err = s.run(ctx, func() error { var e error; out, e = s.RequestQueueStore.EnqueueRequest(ctx, w); return e })
	return
}
func (s *admissionMetadataStore) ReadRequest(ctx context.Context, scope, id, owner string, refresh bool) (out batch.RequestWaiter, err error) {
	err = s.run(ctx, func() error {
		var e error
		out, e = s.RequestQueueStore.ReadRequest(ctx, scope, id, owner, refresh)
		return e
	})
	return
}
func (s *admissionMetadataStore) CancelRequest(ctx context.Context, scope, id, owner string) error {
	return s.runUrgent(ctx, func() error { return s.RequestQueueStore.CancelRequest(ctx, scope, id, owner) })
}
func (s *admissionMetadataStore) runUrgent(ctx context.Context, fn func() error) error {
	if s.urgent == nil {
		return s.run(ctx, fn)
	}
	lane := &admissionMetadataStore{slots: s.urgent}
	return lane.run(ctx, fn)
}
func (s *admissionMetadataStore) ClaimAttempt(ctx context.Context, id, owner string) (out batch.Attempt, err error) {
	err = s.runUrgent(ctx, func() error { var e error; out, e = s.RequestQueueStore.ClaimAttempt(ctx, id, owner); return e })
	return
}
func (s *admissionMetadataStore) MarkDispatch(ctx context.Context, id, owner string) (out batch.Attempt, err error) {
	err = s.runUrgent(ctx, func() error { var e error; out, e = s.RequestQueueStore.MarkDispatch(ctx, id, owner); return e })
	return
}
func (s *admissionMetadataStore) MarkLocalStop(ctx context.Context, id, owner string, at time.Time) (out batch.Attempt, err error) {
	err = s.runUrgent(ctx, func() error { var e error; out, e = s.RequestQueueStore.MarkLocalStop(ctx, id, owner, at); return e })
	return
}
func (s *admissionMetadataStore) StageAttempt(ctx context.Context, id, owner string, o batch.AttemptObservation) (out batch.Attempt, err error) {
	err = s.runUrgent(ctx, func() error { var e error; out, e = s.RequestQueueStore.StageAttempt(ctx, id, owner, o); return e })
	return
}
func (s *admissionMetadataStore) SettleAttempt(ctx context.Context, id, owner string, o batch.AttemptObservation) (out batch.Attempt, err error) {
	err = s.runUrgent(ctx, func() error { var e error; out, e = s.RequestQueueStore.SettleAttempt(ctx, id, owner, o); return e })
	return
}
