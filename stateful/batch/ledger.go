package batch

import (
	"context"
	"errors"
	"time"
)

var ErrGenerationFenced = errors.New("batch: pool generation does not own admission")
var ErrClaimOwned = errors.New("batch: invocation claim already owned")
var ErrClaimUnconfirmed = errors.New("batch: invocation claim unconfirmed")
var ErrSettlementConflict = errors.New("batch: settlement does not match retained outcome")

type PoolGeneration struct {
	PoolID     string
	Generation int64
	Mode       string
	Revision   int64
}

// Reservation contains capacity and ownership metadata only, never inference input.
type Reservation struct {
	PermitID       string
	PoolID         string
	Generation     int64
	Sequence       int64
	Ref            Ref
	RequestID      string
	Kind           string
	WorkflowID     string
	GrantedAt      time.Time
	StartBy        time.Time
	Deadline       time.Time
	BudgetMS       int64
	WindowID       int64
	Epoch          int64
	PolicyRevision int64
	Probe          bool
}
type AttemptObservation struct {
	Outcome       Outcome   `json:"outcome"`
	NativeEntered bool      `json:"native_entered"`
	LocalStopped  bool      `json:"local_stopped"`
	ObservedAt    time.Time `json:"observed_at"`
}
type Attempt struct {
	Reservation
	Ordinal            int64
	State              string
	ClaimID            string
	ClaimedAt          *time.Time
	DispatchedAt       *time.Time
	GenerationDeadline *time.Time
	StoppedAt          *time.Time
	SettlementRevision int64
	Observation        *AttemptObservation
	Counts             Counts
}

func (a Attempt) executionDeadline() time.Time {
	if a.GenerationDeadline != nil {
		return *a.GenerationDeadline
	}
	return a.Deadline
}

// ExecutionStore is the additive claim ledger. Only a workflow-owned reservation
// can enter it; this interface does not independently mint capacity.
type ExecutionStore interface {
	Store
	EnsurePool(context.Context, string, int64) error
	PoolGeneration(context.Context, string) (PoolGeneration, error)
	QuiescePool(context.Context, string, int64) error
	AdvancePool(context.Context, string, int64, int64) error
	ReserveAttempt(context.Context, Reservation) (Attempt, error)
	ClaimAttempt(context.Context, string, string) (Attempt, error)
	MarkDispatch(context.Context, string, string) (Attempt, error)
	StageAttempt(context.Context, string, string, AttemptObservation) (Attempt, error)
	SettleAttempt(context.Context, string, string, AttemptObservation) (Attempt, error)
	ExpireReservation(context.Context, string) (Attempt, error)
	MarkLocalStop(context.Context, string, string, time.Time) (Attempt, error)
	ReadAttempts(context.Context, string, []string) ([]Attempt, error)
}
