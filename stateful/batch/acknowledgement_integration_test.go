//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-528

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	hardenllm "github.com/prls-co/harden-llm"
)

type acknowledgementStore struct {
	*PostgresStore
	claim    claimWriteOptions
	dispatch dispatchWriteOptions
}

func (s acknowledgementStore) ClaimAttempt(ctx context.Context, id, nonce string) (Attempt, error) {
	return s.claimAttempt(ctx, id, nonce, s.claim)
}
func (s acknowledgementStore) MarkDispatch(ctx context.Context, id, nonce string) (Attempt, error) {
	return s.markDispatch(ctx, id, nonce, s.dispatch)
}

func TestInvocationAcknowledgementReadbackKeepsOneNativeAndAttempt(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"claim", "dispatch"} {
		t.Run(phase, func(t *testing.T) {
			s, ref := ledgerFixture(t)
			ctx := context.Background()
			r := permitFixture(ref, 1)
			if _, err := s.ReserveAttempt(ctx, r); err != nil {
				t.Fatal(err)
			}
			writes, reads := 0, 0
			commit := func(ctx context.Context, tx pgx.Tx) error {
				writes++
				if err := tx.Commit(ctx); err != nil {
					return err
				}
				return errors.New("synthetic acknowledgement loss after real COMMIT")
			}
			firstReadFails := func(ctx context.Context, tx pgx.Tx) error {
				reads++
				if reads == 1 {
					// Abort a real readback transaction; the next attempt must be
					// read-only and positively confirm the original committed owner.
					_, err := tx.Exec(ctx, "SELECT 1/0")
					return err
				}
				return nil
			}
			wrapped := acknowledgementStore{PostgresStore: s}
			if phase == "claim" {
				wrapped.claim = claimWriteOptions{commit: commit, beforeReadback: firstReadFails}
			} else {
				wrapped.dispatch = dispatchWriteOptions{commit: commit, beforeReadback: firstReadFails}
			}
			var nativeCalls atomic.Int32
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nativeCalls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = io.WriteString(w, `{"answer":"one-result"}`)
			}))
			defer native.Close()
			artifacts := &invocationArtifacts{}
			a := &Activities{Store: wrapped, Artifacts: artifacts, Encode: func(Generation, hardenllm.Result, time.Time) (json.RawMessage, error) {
				return json.RawMessage(`{"answer":"one-result"}`), nil
			}, Execute: func(ctx context.Context, _ string, _ hardenllm.Request) (hardenllm.Result, error) {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, native.URL, nil)
				if err != nil {
					return hardenllm.Result{}, err
				}
				response, err := native.Client().Do(req)
				if err != nil {
					return hardenllm.Result{}, err
				}
				defer response.Body.Close()
				_, err = io.Copy(io.Discard, response.Body)
				return hardenllm.Result{}, err
			}}
			out, err := a.InvokeAttempt(ctx, r.PermitID)
			if err != nil || out.Observation == nil || out.Observation.Outcome.Kind != "completed" || !out.Observation.LocalStopped || nativeCalls.Load() != 1 || writes != 1 || reads != 2 || artifacts.puts != 1 {
				t.Fatal("transient metadata read lost first execution or repeated its write", err, out, nativeCalls.Load(), writes, reads, artifacts.puts)
			}
			retained, err := a.FinalizeAttempt(ctx, KnownSettlement{Reference: AttemptReference{PoolID: r.PoolID, PermitID: r.PermitID}, ClaimID: out.ClaimID, Observation: *out.Observation})
			if err != nil || retained.State != "settled" || retained.Ordinal != 1 || retained.Counts.Succeeded != 1 {
				t.Fatal("confirmed owner did not settle the original attempt", err, retained)
			}
			redelivery, err := a.InvokeAttempt(ctx, r.PermitID)
			if err != nil || redelivery.Disposition != "claim_owned_elsewhere" || nativeCalls.Load() != 1 || writes != 1 {
				t.Fatal("readback recovery authorized activity redelivery", err, redelivery, nativeCalls.Load(), writes)
			}
			item, err := s.Load(ctx, ref)
			var count int
			countErr := s.Pool.QueryRow(ctx, `SELECT count(*) FROM batch_attempts WHERE job_id=$1`, ref.JobID).Scan(&count)
			if err != nil || countErr != nil || count != 1 || item.State != "succeeded" || item.Attempt != 1 {
				t.Fatal("acknowledgement recovery consumed a replacement reservation", err, countErr, count, item.State, item.Attempt)
			}
		})
	}
}

func TestDispatchRecordedBeforeThisWriteNeverUsesReadbackPermission(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	ctx := context.Background()
	r := permitFixture(ref, 1)
	if _, err := s.ReserveAttempt(ctx, r); err != nil {
		t.Fatal(err)
	}
	nonce := uuid.NewString()
	if _, err := s.ClaimAttempt(ctx, r.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDispatch(ctx, r.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	writes, reads := 0, 0
	_, err := s.markDispatch(ctx, r.PermitID, nonce, dispatchWriteOptions{commit: func(ctx context.Context, tx pgx.Tx) error {
		writes++
		return tx.Commit(ctx)
	}, beforeReadback: func(context.Context, pgx.Tx) error { reads++; return nil }})
	if !errors.Is(err, ErrClaimOwned) || writes != 0 || reads != 0 {
		t.Fatal("existing dispatch intent was reused as new inference permission", err, writes, reads)
	}
}

func TestAcknowledgementReadbackRejectsInvalidProofWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"claim", "dispatch"} {
		for _, mode := range []string{"rolled-back", "foreign", "terminal", "stopped", "expired", "fenced", "changed-write-stamp", "changed-budget", "persistent-read-error", "caller-canceled"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				s, ref := ledgerFixture(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				r := permitFixture(ref, 1)
				if _, err := s.ReserveAttempt(ctx, r); err != nil {
					t.Fatal(err)
				}
				nonce := uuid.NewString()
				if phase == "dispatch" {
					if _, err := s.ClaimAttempt(ctx, r.PermitID, nonce); err != nil {
						t.Fatal(err)
					}
				}
				writes, reads := 0, 0
				var before []Attempt
				commit := func(bounded context.Context, tx pgx.Tx) error {
					writes++
					if mode == "rolled-back" {
						if err := tx.Rollback(bounded); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := tx.Commit(bounded); err != nil {
							t.Fatal(err)
						}
						switch mode {
						case "foreign":
							_, err := s.Pool.Exec(ctx, `UPDATE batch_attempts SET claim_id=$2 WHERE permit_id=$1`, r.PermitID, uuid.NewString())
							if err != nil {
								t.Fatal(err)
							}
						case "terminal":
							o := AttemptObservation{Outcome: Outcome{Kind: "unconfirmed"}, NativeEntered: phase == "dispatch", ObservedAt: time.Now().UTC()}
							if _, err := s.SettleAttempt(ctx, r.PermitID, nonce, o); err != nil {
								t.Fatal(err)
							}
						case "stopped":
							if _, err := s.MarkLocalStop(ctx, r.PermitID, nonce, time.Now()); err != nil {
								t.Fatal(err)
							}
						case "expired":
							if _, err := s.Pool.Exec(ctx, `UPDATE batch_attempts SET start_by=now()-interval '1 second' WHERE permit_id=$1`, r.PermitID); err != nil {
								t.Fatal(err)
							}
						case "fenced":
							if err := s.QuiescePool(ctx, r.PoolID, r.Generation); err != nil {
								t.Fatal(err)
							}
						case "changed-write-stamp":
							query := `UPDATE batch_attempts SET claimed_at=claimed_at-interval '1 microsecond' WHERE permit_id=$1`
							if phase == "dispatch" {
								query = `UPDATE batch_attempts SET dispatch_at=dispatch_at-interval '1 microsecond' WHERE permit_id=$1`
							}
							if _, err := s.Pool.Exec(ctx, query, r.PermitID); err != nil {
								t.Fatal(err)
							}
						case "changed-budget":
							if _, err := s.Pool.Exec(ctx, `UPDATE batch_attempts SET budget_ms=budget_ms-1 WHERE permit_id=$1`, r.PermitID); err != nil {
								t.Fatal(err)
							}
						}
					}
					var err error
					before, err = s.ReadAttempts(context.Background(), r.PoolID, []string{r.PermitID})
					if err != nil {
						t.Fatal(err)
					}
					if mode == "caller-canceled" {
						cancel()
					}
					return errors.New("synthetic lost acknowledgement")
				}
				readback := func(ctx context.Context, tx pgx.Tx) error {
					reads++
					if mode == "persistent-read-error" {
						_, err := tx.Exec(ctx, "SELECT 1/0")
						return err
					}
					return nil
				}
				var err error
				if phase == "claim" {
					_, err = s.claimAttempt(ctx, r.PermitID, nonce, claimWriteOptions{commit: commit, beforeReadback: readback})
				} else {
					_, err = s.markDispatch(ctx, r.PermitID, nonce, dispatchWriteOptions{commit: commit, beforeReadback: readback})
				}
				wantReads := 1
				if mode == "persistent-read-error" {
					wantReads = 2
				} else if mode == "caller-canceled" {
					wantReads = 0
				}
				if err == nil || writes != 1 || reads != wantReads {
					t.Fatal("invalid proof authorized execution or repeated metadata write", err, writes, reads, wantReads)
				}
				after, err := s.ReadAttempts(context.Background(), r.PoolID, []string{r.PermitID})
				oldJSON, _ := json.Marshal(before)
				newJSON, _ := json.Marshal(after)
				if err != nil || string(oldJSON) != string(newJSON) {
					t.Fatal("rejected acknowledgement proof mutated retained state", err)
				}
			})
		}
	}
}

func TestClaimReadbackCannotAdoptPreviouslyRecordedDispatch(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	ctx := context.Background()
	r := permitFixture(ref, 1)
	if _, err := s.ReserveAttempt(ctx, r); err != nil {
		t.Fatal(err)
	}
	nonce := uuid.NewString()
	reads := 0
	_, err := s.claimAttempt(ctx, r.PermitID, nonce, claimWriteOptions{commit: func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		if _, err := s.MarkDispatch(ctx, r.PermitID, nonce); err != nil {
			t.Fatal(err)
		}
		return errors.New("synthetic lost claim acknowledgement with recorded intent")
	}, beforeReadback: func(context.Context, pgx.Tx) error { reads++; return nil }})
	if !errors.Is(err, ErrClaimUnconfirmed) || reads != 1 {
		t.Fatal("claim readback adopted permission with an already-recorded dispatch", err, reads)
	}
}

func TestAcknowledgementReadbackCannotResurrectCanceledOrdinaryOwner(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"claim", "dispatch"} {
		t.Run(phase, func(t *testing.T) {
			s, _ := ledgerFixture(t)
			ctx := context.Background()
			waiter, r := requestFixture(t, s, 1)
			if _, err := s.ReserveAttempt(ctx, r); err != nil {
				t.Fatal(err)
			}
			if phase == "dispatch" {
				if _, err := s.ClaimAttempt(ctx, r.PermitID, waiter.OwnerID); err != nil {
					t.Fatal(err)
				}
			}
			writes, reads := 0, 0
			commit := func(ctx context.Context, tx pgx.Tx) error {
				writes++
				if err := tx.Commit(ctx); err != nil {
					return err
				}
				if err := s.CancelRequest(ctx, waiter.Scope, waiter.RequestID, waiter.OwnerID); err != nil {
					t.Fatal(err)
				}
				return errors.New("synthetic acknowledgement loss after owner cancellation")
			}
			readback := func(context.Context, pgx.Tx) error { reads++; return nil }
			var err error
			if phase == "claim" {
				_, err = s.claimAttempt(ctx, r.PermitID, waiter.OwnerID, claimWriteOptions{commit: commit, beforeReadback: readback})
			} else {
				_, err = s.markDispatch(ctx, r.PermitID, waiter.OwnerID, dispatchWriteOptions{commit: commit, beforeReadback: readback})
			}
			if err == nil || reads != 1 || writes != 1 {
				t.Fatal("readback revived a canceled ordinary owner or repeated its write", err, reads, writes)
			}
		})
	}
}
