package stateful

import (
	"errors"
	"github.com/google/uuid"
	"time"
)

// RequestStatus is bounded observation metadata, never retained model output.
type RequestStatus struct {
	ID                    string     `json:"id"`
	Endpoint              string     `json:"endpoint"`
	Status                string     `json:"status"`
	Stage                 *string    `json:"stage"`
	Reason                *string    `json:"reason"`
	CreatedAt             time.Time  `json:"created_at"`
	DeadlineAt            time.Time  `json:"deadline_at"`
	RetentionExpiresAt    time.Time  `json:"retention_expires_at"`
	ObservedAt            time.Time  `json:"observed_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	LastWorkerHeartbeatAt time.Time  `json:"last_worker_heartbeat_at"`
	LastProgressAt        *time.Time `json:"last_progress_at"`
	FinishedAt            *time.Time `json:"finished_at"`
	NextAttemptAt         *time.Time `json:"next_attempt_at"`
	ElapsedMS             int64      `json:"elapsed_ms"`
	RetryAfterMS          *int64     `json:"retry_after_ms"`
	AttemptsUsed          int        `json:"attempts_used"`
	ObservationStale      bool       `json:"observation_stale"`
	OutcomeConfirmed      *bool      `json:"outcome_confirmed"`
	ErrorCode             *string    `json:"error_code"`
}

// TrackingRecord carries the invocation fence separately from its public view.
type TrackingRecord struct {
	RequestStatus
	OwnerID  string `json:"-"`
	Revision int64  `json:"-"`
}

var ErrTrackingConflict = errors.New("request tracking ID already retained")
var ErrTrackingNotFound = errors.New("request tracking ID not found")
var ErrTrackingUnavailable = errors.New("request tracking storage unavailable")

func ValidTrackingID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && len(value) == 36 && id.String() == value
}

func (r TrackingRecord) Observe(now time.Time) RequestStatus {
	v := r.RequestStatus
	v.ObservedAt = now.UTC()
	end := now
	if v.FinishedAt != nil {
		end = *v.FinishedAt
	}
	v.ElapsedMS = max(end.Sub(v.CreatedAt).Milliseconds(), 0)
	if v.NextAttemptAt != nil {
		remaining := max(v.NextAttemptAt.Sub(now).Milliseconds(), 0)
		v.RetryAfterMS = &remaining
	}
	v.ObservationStale = v.FinishedAt == nil && now.Sub(v.LastWorkerHeartbeatAt) > 15*time.Second
	if v.FinishedAt == nil && now.After(v.DeadlineAt.Add(30*time.Second)) {
		v.Status = "unconfirmed"
		reason, confirmed := "execution_unobserved", false
		v.Reason, v.OutcomeConfirmed, v.NextAttemptAt, v.RetryAfterMS = &reason, &confirmed, nil, nil
	}
	return v
}

func NewTrackingRecord(id, endpoint string, now, deadline time.Time) (TrackingRecord, error) {
	if !ValidTrackingID(id) || (endpoint != "/v1/responses" && endpoint != "/v1/chat/completions") || !deadline.After(now) {
		return TrackingRecord{}, ErrTrackingUnavailable
	}
	owner, err := uuid.NewRandom()
	if err != nil {
		return TrackingRecord{}, ErrTrackingUnavailable
	}
	now, deadline = now.UTC(), deadline.UTC()
	stage := "preparing"
	return TrackingRecord{RequestStatus: RequestStatus{ID: id, Endpoint: endpoint, Status: "running", Stage: &stage, CreatedAt: now, DeadlineAt: deadline, RetentionExpiresAt: deadline.Add(24 * time.Hour), UpdatedAt: now, LastWorkerHeartbeatAt: now}, OwnerID: owner.String(), Revision: 1}, nil
}
