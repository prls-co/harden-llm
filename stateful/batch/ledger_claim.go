package batch

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type claimWriteOptions struct {
	commit        func(context.Context, pgx.Tx) error
	readbackError error
	onReadback    func()
}

func (s *PostgresStore) ClaimAttempt(ctx context.Context, id, nonce string) (Attempt, error) {
	return s.claimAttempt(ctx, id, nonce, claimWriteOptions{})
}
func (s *PostgresStore) claimAttempt(ctx context.Context, id, nonce string, opts claimWriteOptions) (Attempt, error) {
	var result Attempt
	if !validPermitID(id) || !validPermitID(nonce) {
		return result, ErrClaimUnconfirmed
	}
	changed := false
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		a, err := readAttempt(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if a.State != "reserved" || a.ClaimID != "" {
			return ErrClaimOwned
		}
		if err := guardPool(ctx, tx, a.PoolID, a.Generation); err != nil {
			return err
		}
		if err := guardRequestClaim(ctx, tx, a, nonce); err != nil {
			return err
		}
		now := time.Now().UTC()
		if !now.Before(a.StartBy) || !now.Before(a.Deadline) {
			return ErrClaimOwned
		}
		deadline := minTime(a.Deadline, now.Add(time.Duration(a.BudgetMS)*time.Millisecond))
		if _, err := tx.Exec(ctx, `UPDATE batch_attempts SET state='dispatching',claim_id=$2,claimed_at=$3,generation_deadline=$4 WHERE permit_id=$1 AND state='reserved'`, id, nonce, now, deadline); err != nil {
			return err
		}
		changed = true
		result, err = readAttempt(ctx, tx, id, false)
		return err
	}, opts.commit)
	if err == nil {
		return result, nil
	}
	if changed {
		if opts.onReadback != nil {
			opts.onReadback()
		}
		if opts.readbackError != nil {
			return Attempt{}, ErrClaimUnconfirmed
		}
		readErr := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			var e error
			result, e = readAttempt(ctx, tx, id, false)
			return e
		}, nil)
		if readErr == nil && result.ClaimID == nonce && result.State == "dispatching" {
			return result, nil
		}
		return Attempt{}, ErrClaimUnconfirmed
	}
	return Attempt{}, ledgerError(err)
}
func (s *PostgresStore) MarkDispatch(ctx context.Context, id, nonce string) (Attempt, error) {
	var result Attempt
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		a, err := readAttempt(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if a.ClaimID != nonce || a.State != "dispatching" {
			return ErrClaimOwned
		}
		if a.DispatchedAt != nil {
			return ErrClaimOwned
		}
		if err := guardPool(ctx, tx, a.PoolID, a.Generation); err != nil {
			return err
		}
		if err := guardRequestClaim(ctx, tx, a, nonce); err != nil {
			return err
		}
		now := time.Now().UTC()
		if !now.Before(a.StartBy) || !now.Before(a.executionDeadline()) {
			return ErrClaimOwned
		}
		if a.Kind == "job" {
			tag, err := tx.Exec(ctx, `UPDATE batch_items SET attempts=attempts+1 WHERE api_key_hash=$1 AND job_id=$2 AND item_index=$3 AND current_permit=$4 AND state='active' AND attempts<16`, a.Ref.Scope, a.Ref.JobID, a.Ref.Index, id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrClaimOwned
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE batch_attempts SET dispatch_at=$2 WHERE permit_id=$1`, id, now); err != nil {
			return err
		}
		result, err = readAttempt(ctx, tx, id, false)
		return err
	}, nil)
	// An uncertain dispatch-intent acknowledgement must not authorize Execute.
	if err != nil {
		return Attempt{}, ledgerError(err)
	}
	return result, nil
}
func validObservation(o AttemptObservation) bool {
	switch o.Outcome.Kind {
	case "completed", "failed", "rejected", "policy", "unconfirmed", "never_dispatched":
	default:
		return false
	}
	if o.ObservedAt.IsZero() {
		return false
	}
	if o.Outcome.Kind == "completed" && !o.NativeEntered {
		return false
	}
	if o.Outcome.Kind == "never_dispatched" && (o.NativeEntered || !o.LocalStopped) {
		return false
	}
	raw, err := json.Marshal(o)
	return err == nil && len(raw) <= 60000
}
func sameTerminalObservation(a, b AttemptObservation) bool {
	// Positive local-stop evidence may advance after the immutable result.
	a.LocalStopped, b.LocalStopped = false, false
	return sameObservation(a, b)
}
func (s *PostgresStore) StageAttempt(ctx context.Context, id, nonce string, o AttemptObservation) (Attempt, error) {
	var result Attempt
	if !validObservation(o) {
		return result, ErrSettlementConflict
	}
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		a, err := readAttempt(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if a.ClaimID != nonce || a.State == "reserved" {
			return ErrClaimOwned
		}
		if !observationMatchesOwner(a, o) {
			return ErrSettlementConflict
		}
		if a.Observation != nil {
			if !sameTerminalObservation(*a.Observation, o) {
				return ErrSettlementConflict
			}
			result = a
			return nil
		}
		raw, _ := json.Marshal(o)
		if _, err := tx.Exec(ctx, `UPDATE batch_attempts SET observation=$3,local_stopped_at=CASE WHEN $4 THEN $5 ELSE local_stopped_at END WHERE permit_id=$1 AND claim_id=$2`, id, nonce, raw, o.LocalStopped, o.ObservedAt); err != nil {
			return err
		}
		result, err = readAttempt(ctx, tx, id, false)
		return err
	}, nil)
	return result, ledgerError(err)
}
func (s *PostgresStore) SettleAttempt(ctx context.Context, id, nonce string, o AttemptObservation) (Attempt, error) {
	return s.settleAttempt(ctx, id, nonce, o, nil)
}
func (s *PostgresStore) settleAttempt(ctx context.Context, id, nonce string, o AttemptObservation, commit func(context.Context, pgx.Tx) error) (Attempt, error) {
	var result Attempt
	if !validObservation(o) {
		return result, ErrSettlementConflict
	}
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		a, err := readAttempt(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if a.ClaimID != nonce || a.State == "reserved" {
			return ErrClaimOwned
		}
		if !observationMatchesOwner(a, o) {
			return ErrSettlementConflict
		}
		if a.State == "settled" {
			if a.Observation == nil || !sameTerminalObservation(*a.Observation, o) {
				return ErrSettlementConflict
			}
			result = a
			return fillAttemptCounts(ctx, tx, &result)
		}
		if a.Observation != nil && !sameTerminalObservation(*a.Observation, o) {
			return ErrSettlementConflict
		}
		if o.NativeEntered && a.DispatchedAt == nil {
			return ErrSettlementConflict
		}
		if o.Outcome.Kind == "completed" && (o.Outcome.FinishedAt == nil || o.Outcome.FinishedAt.After(a.executionDeadline())) {
			return ErrSettlementConflict
		}
		if err := applyAttemptItem(ctx, tx, &a, o); err != nil {
			return err
		}
		raw, _ := json.Marshal(o)
		if _, err := tx.Exec(ctx, `UPDATE batch_attempts SET state='settled',observation=$3,settlement_revision=1,local_stopped_at=CASE WHEN $4 THEN $5 ELSE local_stopped_at END WHERE permit_id=$1 AND claim_id=$2`, id, nonce, raw, o.LocalStopped, o.ObservedAt); err != nil {
			return err
		}
		result, err = readAttempt(ctx, tx, id, false)
		if err != nil {
			return err
		}
		return fillAttemptCounts(ctx, tx, &result)
	}, commit)
	if err == nil {
		return result, nil
	}
	// Settlement retries/readback are metadata only, never a new invocation.
	if !errors.Is(err, ErrClaimOwned) && !errors.Is(err, ErrSettlementConflict) {
		readErr := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			var e error
			result, e = readAttempt(ctx, tx, id, false)
			if e != nil {
				return e
			}
			return fillAttemptCounts(ctx, tx, &result)
		}, nil)
		if readErr == nil && result.State == "settled" && result.ClaimID == nonce && result.Observation != nil && sameTerminalObservation(*result.Observation, o) {
			return result, nil
		}
	}
	return Attempt{}, ledgerError(err)
}
func fillAttemptCounts(ctx context.Context, tx pgx.Tx, a *Attempt) error {
	if a.Kind != "job" {
		return nil
	}
	var err error
	a.Counts, err = counts(ctx, tx, a.Ref.Scope, a.Ref.JobID)
	return err
}
func applyAttemptItem(ctx context.Context, tx pgx.Tx, a *Attempt, o AttemptObservation) error {
	if a.Kind == "ordinary" {
		_, err := tx.Exec(ctx, `UPDATE batch_request_waiters SET closed_at=COALESCE(closed_at,now()) WHERE request_id=$1 AND current_permit=$2`, a.RequestID, a.PermitID)
		return err
	}
	var current *string
	var state string
	var attempts int
	var deadline time.Time
	if err := tx.QueryRow(ctx, `SELECT i.current_permit::text,i.state,i.attempts,j.deadline FROM batch_items i JOIN batch_jobs j ON j.api_key_hash=i.api_key_hash AND j.id=i.job_id WHERE i.api_key_hash=$1 AND i.job_id=$2 AND i.item_index=$3 FOR UPDATE OF i`, a.Ref.Scope, a.Ref.JobID, a.Ref.Index).Scan(&current, &state, &attempts, &deadline); err != nil {
		return err
	}
	if current == nil || *current != a.PermitID || state == "succeeded" || state == "failed" {
		return ErrSettlementConflict
	}
	itemOutcome := o.Outcome
	state = "failed"
	if itemOutcome.Kind == "completed" {
		state = "succeeded"
	}
	if itemOutcome.Kind == "rejected" && attempts < 16 && time.Now().Before(deadline) {
		state = "waiting"
	}
	if itemOutcome.Kind == "rejected" && attempts >= 16 {
		itemOutcome.Kind = "failed"
		itemOutcome.Error = &ItemError{Code: "refusal_budget_exhausted", Message: "The item reached its refusal budget."}
	}
	if itemOutcome.Kind == "never_dispatched" {
		if time.Now().Before(deadline) {
			state = "queued"
		} else {
			itemOutcome.Kind = "failed"
			itemOutcome.Error = &ItemError{Code: "job_deadline_exceeded", Message: "The job deadline expired before dispatch."}
		}
		if a.DispatchedAt != nil {
			attempts = max(0, attempts-1)
		}
	}
	raw, _ := json.Marshal(itemOutcome)
	if _, err := tx.Exec(ctx, `UPDATE batch_items SET state=$5,outcome=$6,current_permit=NULL,attempts=$7,queued_at=CASE WHEN $5 IN ('queued','waiting') THEN now() ELSE queued_at END WHERE api_key_hash=$1 AND job_id=$2 AND item_index=$3 AND current_permit=$4`, a.Ref.Scope, a.Ref.JobID, a.Ref.Index, a.PermitID, state, raw, attempts); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE batch_jobs SET last_progress=now(),last_heartbeat=now() WHERE api_key_hash=$1 AND id=$2`, a.Ref.Scope, a.Ref.JobID)
	return err
}

func observationMatchesOwner(a Attempt, o AttemptObservation) bool {
	if a.Kind == "ordinary" {
		return o.Outcome.ArtifactID == ""
	}
	return o.Outcome.Kind != "completed" || o.Outcome.ArtifactID != ""
}
func (s *PostgresStore) ExpireReservation(ctx context.Context, id string) (Attempt, error) {
	var result Attempt
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		a, err := readAttempt(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if a.State == "settled" {
			result = a
			return fillAttemptCounts(ctx, tx, &result)
		}
		if a.State != "reserved" || a.ClaimID != "" {
			return ErrClaimOwned
		}
		if time.Now().Before(a.StartBy) {
			return ErrClaimOwned
		}
		now := time.Now().UTC()
		o := AttemptObservation{Outcome: Outcome{Kind: "never_dispatched"}, LocalStopped: true, ObservedAt: now}
		if err := applyAttemptItem(ctx, tx, &a, o); err != nil {
			return err
		}
		raw, _ := json.Marshal(o)
		if _, err := tx.Exec(ctx, `UPDATE batch_attempts SET state='settled',observation=$2,settlement_revision=1,local_stopped_at=$3 WHERE permit_id=$1 AND state='reserved'`, id, raw, now); err != nil {
			return err
		}
		result, err = readAttempt(ctx, tx, id, false)
		if err != nil {
			return err
		}
		return fillAttemptCounts(ctx, tx, &result)
	}, nil)
	return result, ledgerError(err)
}
func (s *PostgresStore) MarkLocalStop(ctx context.Context, id, nonce string, at time.Time) (Attempt, error) {
	var result Attempt
	err := s.ledgerTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		a, err := readAttempt(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if a.ClaimID != nonce || a.ClaimID == "" {
			return ErrClaimOwned
		}
		if a.StoppedAt == nil {
			if _, err := tx.Exec(ctx, `UPDATE batch_attempts SET local_stopped_at=$3,settlement_revision=CASE WHEN state='settled' THEN settlement_revision+1 ELSE settlement_revision END WHERE permit_id=$1 AND claim_id=$2`, id, nonce, at.UTC()); err != nil {
				return err
			}
		}
		result, err = readAttempt(ctx, tx, id, false)
		return err
	}, nil)
	return result, ledgerError(err)
}
