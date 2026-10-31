package stateful

import (
	"sync"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

// TrackingSession projects bounded facts under a mutex; callbacks never do I/O.
// The handler, not a runtime terminal event, finalizes the outcome.
type TrackingSession struct {
	mu              sync.Mutex
	record          TrackingRecord
	now             func() time.Time
	epoch           uint64
	sequence        uint64
	received        int64
	events          int64
	providerEntered bool
}

func NewTrackingSession(r TrackingRecord, now func() time.Time) *TrackingSession {
	return &TrackingSession{record: r, now: now}
}
func (s *TrackingSession) changed() {
	now := trackingTime(s.now(), s.record.UpdatedAt)
	s.record.UpdatedAt = now
	s.record.LastProgressAt = &now
	s.record.Revision++
}
func (s *TrackingSession) Phase(stage string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.FinishedAt != nil {
		return
	}
	switch stage {
	case "preparing", "provider_attempt", "validation", "response_projection":
	default:
		return
	}
	if stage == "provider_attempt" {
		s.providerEntered = true
	}
	s.epoch++
	if s.record.Stage != nil && *s.record.Stage == stage && s.record.Status == "running" {
		return
	}
	s.record.Status = "running"
	s.record.Stage = &stage
	s.record.Reason = nil
	s.record.NextAttemptAt = nil
	s.changed()
}
func (s *TrackingSession) Attempt(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.FinishedAt == nil && n > s.record.AttemptsUsed {
		s.record.AttemptsUsed = n
		s.changed()
	}
}
func (s *TrackingSession) Wait(reason string, delay time.Duration) func() {
	s.mu.Lock()
	if s.record.FinishedAt != nil {
		s.mu.Unlock()
		return func() {}
	}
	s.epoch++
	epoch := s.epoch
	stage := "retry_wait"
	next := trackingTime(s.now(), s.record.UpdatedAt).Add(max(delay, 0))
	s.record.Status = "waiting"
	s.record.Stage = &stage
	s.record.Reason = &reason
	s.record.NextAttemptAt = &next
	s.changed()
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.record.FinishedAt != nil || s.epoch != epoch {
			return
		}
		stage := "preparing"
		s.record.Status = "running"
		s.record.Stage = &stage
		s.record.Reason = nil
		s.record.NextAttemptAt = nil
		s.epoch++
		s.changed()
	}
}
func (s *TrackingSession) Progress(p hardenllm.ProgressEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.FinishedAt != nil || p.Sequence <= s.sequence {
		return
	}
	s.sequence = p.Sequence
	// Progress and observer callbacks are separate sources: an old queued progress
	// snapshot must not undo an authoritative phase/wait transition.
	changed := false
	if p.AttemptsUsed > s.record.AttemptsUsed {
		s.record.AttemptsUsed = p.AttemptsUsed
		changed = true
	}
	if p.ReceivedBytes > s.received {
		s.received = p.ReceivedBytes
		changed = true
	}
	if p.EventCount > s.events {
		s.events = p.EventCount
		changed = true
	}
	if changed {
		s.changed()
	}
}
func (s *TrackingSession) ProviderEntered() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.providerEntered
}
func (s *TrackingSession) Finish(status, code string, confirmed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.FinishedAt != nil {
		return
	}
	switch status {
	case "completed", "failed", "cancelled", "unconfirmed":
	default:
		return
	}
	s.record.Status = status
	s.record.Reason = nil
	s.record.NextAttemptAt = nil
	s.record.OutcomeConfirmed = &confirmed
	if code != "" {
		s.record.ErrorCode = &code
	}
	s.changed()
	now := s.record.UpdatedAt
	s.record.FinishedAt = &now
	s.record.LastWorkerHeartbeatAt = now
	s.epoch++
}
func (s *TrackingSession) Snapshot(heartbeat bool) TrackingRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	if heartbeat && s.record.FinishedAt == nil {
		s.record.LastWorkerHeartbeatAt = trackingTime(s.now(), s.record.UpdatedAt)
		s.record.UpdatedAt = s.record.LastWorkerHeartbeatAt
		s.record.Revision++
	}
	return s.record
}
