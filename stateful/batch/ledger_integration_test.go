//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-498

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prls-co/harden-llm/internal/integrationtest"
)

func ledgerFixture(t *testing.T) (*PostgresStore, Ref) {
	t.Helper()
	_, dsn := integrationtest.PostgresLease(t)
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if err := ApplySchema(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	s := &PostgresStore{Pool: p}
	job, err := s.Create(context.Background(), strings.Repeat("a", 64), uuid.NewString(), []Item{{CustomID: "one", Generation: Generation{ModelID: "gpt-6-luna", TimeoutMS: 300000}}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsurePool(context.Background(), "fixture-pool", 2); err != nil {
		t.Fatal("initialize claim authority", err)
	}
	return s, Ref{Scope: job.Scope, JobID: job.JobID, Index: 0}
}
func permitFixture(ref Ref, sequence int64) Reservation {
	now := time.Now().UTC()
	id := uuid.NewString()
	return Reservation{PermitID: id, PoolID: "fixture-pool", Generation: 2, Sequence: sequence, Ref: ref, Kind: "job", WorkflowID: "hllm-attempt/" + id, GrantedAt: now, StartBy: now.Add(5 * time.Second), Deadline: now.Add(time.Minute), BudgetMS: 300000, PolicyRevision: 1}
}
func TestAttemptLedgerClaimAndSettlement(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	ctx := context.Background()
	p := permitFixture(ref, 1)
	reserved, err := s.ReserveAttempt(ctx, p)
	if err != nil || reserved.State != "reserved" {
		t.Fatal("reservation failed", err)
	}
	again, err := s.ReserveAttempt(ctx, p)
	if err != nil || again.Ordinal != reserved.Ordinal {
		t.Fatal("repeat reservation replaced attempt", err)
	}
	var wins, owned atomic.Int32
	var winner string
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			nonce := uuid.NewString()
			_, err := s.ClaimAttempt(ctx, p.PermitID, nonce)
			if err == nil {
				wins.Add(1)
				mu.Lock()
				winner = nonce
				mu.Unlock()
			} else if errors.Is(err, ErrClaimOwned) {
				owned.Add(1)
			} else {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || owned.Load() != 1 {
		t.Fatal("concurrent activity claims admitted duplicate inference", wins.Load(), owned.Load())
	}
	if _, err := s.MarkDispatch(ctx, p.PermitID, winner); err != nil {
		t.Fatal(err)
	}
	finish := time.Now().UTC()
	observation := AttemptObservation{Outcome: Outcome{Kind: "completed", ArtifactID: "result.json", FinishedAt: &finish}, NativeEntered: true, LocalStopped: true, ObservedAt: finish}
	if _, err := s.StageAttempt(ctx, p.PermitID, winner, observation); err != nil {
		t.Fatal(err)
	}
	settled, err := s.SettleAttempt(ctx, p.PermitID, winner, observation)
	if err != nil || settled.State != "settled" || settled.StoppedAt == nil || settled.Counts.Succeeded != 1 {
		t.Fatal("settlement not authoritative", err, settled)
	}
	duplicate, err := s.SettleAttempt(ctx, p.PermitID, winner, observation)
	if err != nil || duplicate.SettlementRevision != settled.SettlementRevision {
		t.Fatal("duplicate settlement released/changed attempt", err)
	}
	changed := observation
	changed.Outcome.Kind = "failed"
	if _, err := s.SettleAttempt(ctx, p.PermitID, winner, changed); !errors.Is(err, ErrSettlementConflict) {
		t.Fatal("different terminal outcome overwrote retained result", err)
	}
	if _, err := s.SettleAttempt(ctx, p.PermitID, uuid.NewString(), observation); !errors.Is(err, ErrClaimOwned) {
		t.Fatal("foreign invocation settled attempt", err)
	}
	stored, err := s.Load(ctx, ref)
	if err != nil || stored.State != "succeeded" || stored.Attempt != 1 {
		t.Fatal("claim/settlement counts changed", err, stored.State, stored.Attempt)
	}
}
func TestAttemptReservationExpiryCannotRevokeClaim(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	ctx := context.Background()
	p := permitFixture(ref, 1)
	if _, err := s.ReserveAttempt(ctx, p); err != nil {
		t.Fatal(err)
	}
	nonce := uuid.NewString()
	if _, err := s.ClaimAttempt(ctx, p.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, "UPDATE batch_attempts SET start_by=now()-interval '1 second' WHERE permit_id=$1", p.PermitID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExpireReservation(ctx, p.PermitID); !errors.Is(err, ErrClaimOwned) {
		t.Fatal("lease expiry revoked an invocation claim", err)
	}
	if _, err := s.ClaimAttempt(ctx, p.PermitID, uuid.NewString()); !errors.Is(err, ErrClaimOwned) {
		t.Fatal("expired claim authorized replacement inference", err)
	}
	if err := s.QuiescePool(ctx, p.PoolID, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePool(ctx, p.PoolID, 2, 3); !errors.Is(err, ErrGenerationFenced) {
		t.Fatal("handoff ignored unresolved execution", err)
	}
}

func TestAttemptUnknownClaimCommit(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"found", "absent", "readback_failed", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			s, ref := ledgerFixture(t)
			ctx := context.Background()
			p := permitFixture(ref, 1)
			if _, err := s.ReserveAttempt(ctx, p); err != nil {
				t.Fatal(err)
			}
			nonce := uuid.NewString()
			reads := 0
			opts := claimWriteOptions{onReadback: func() { reads++ }}
			opts.commit = func(ctx context.Context, tx pgx.Tx) error {
				if kind == "found" || kind == "readback_failed" {
					if err := tx.Commit(ctx); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := tx.Rollback(ctx); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "foreign" {
					if _, err := s.ClaimAttempt(ctx, p.PermitID, uuid.NewString()); err != nil {
						t.Fatal(err)
					}
				}
				return errors.New("synthetic lost claim acknowledgement")
			}
			if kind == "readback_failed" {
				opts.readbackError = errors.New("synthetic failed readback")
			}
			_, err := s.claimAttempt(ctx, p.PermitID, nonce, opts)
			if kind == "found" {
				if err != nil {
					t.Fatal("positive same-owner readback rejected", err)
				}
			} else if !errors.Is(err, ErrClaimUnconfirmed) {
				t.Fatal("uncertain claim authorized inference", kind, err)
			}
			if reads != 1 {
				t.Fatal("claim retried insert/ownership instead of one readback", reads)
			}
		})
	}
}
func TestAttemptNeverSentExpiryAndLateClaim(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	ctx := context.Background()
	p := permitFixture(ref, 1)
	p.GrantedAt = time.Now().Add(-2 * time.Second)
	p.StartBy = time.Now().Add(-time.Second)
	if _, err := s.ReserveAttempt(ctx, p); err != nil {
		t.Fatal(err)
	}
	expired, err := s.ExpireReservation(ctx, p.PermitID)
	if err != nil || expired.StoppedAt == nil || expired.Observation == nil || expired.Observation.Outcome.Kind != "never_dispatched" {
		t.Fatal("never-sent expiry not authoritative", err)
	}
	if _, err := s.ClaimAttempt(ctx, p.PermitID, uuid.NewString()); !errors.Is(err, ErrClaimOwned) {
		t.Fatal("late worker claimed expired permit", err)
	}
	next := permitFixture(ref, 2)
	res, err := s.ReserveAttempt(ctx, next)
	if err != nil || res.Ordinal != 2 {
		t.Fatal("never-sent expiry did not safely return item", err, res.Ordinal)
	}
	item, err := s.Load(ctx, ref)
	if err != nil || item.Attempt != 0 {
		t.Fatal("never-sent reservation consumed provider retry", err, item.Attempt)
	}
}
func TestAttemptLocalStopFenceAndGeneration(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	ctx := context.Background()
	p := permitFixture(ref, 1)
	nonce := uuid.NewString()
	if _, err := s.ReserveAttempt(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimAttempt(ctx, p.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDispatch(ctx, p.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	o := AttemptObservation{Outcome: Outcome{Kind: "unconfirmed"}, NativeEntered: true, ObservedAt: time.Now().UTC()}
	settled, err := s.SettleAttempt(ctx, p.PermitID, nonce, o)
	if err != nil || settled.StoppedAt != nil || settled.Counts.Failed != 1 {
		t.Fatal("unknown result lost local-stop uncertainty", err)
	}
	if err := s.QuiescePool(ctx, p.PoolID, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePool(ctx, p.PoolID, 2, 3); !errors.Is(err, ErrGenerationFenced) {
		t.Fatal("handoff released unresolved native work", err)
	}
	stopped, err := s.MarkLocalStop(ctx, p.PermitID, nonce, time.Now())
	if err != nil || stopped.SettlementRevision != settled.SettlementRevision+1 {
		t.Fatal("local-stop acknowledgement not revision fenced", err)
	}
	if err := s.AdvancePool(ctx, p.PoolID, 2, 3); err != nil {
		t.Fatal("drained handoff blocked", err)
	}
	next := permitFixture(ref, 2)
	if _, err := s.ReserveAttempt(ctx, next); !errors.Is(err, ErrGenerationFenced) {
		t.Fatal("obsolete generation minted capacity", err)
	}
	old, err := s.Load(ctx, ref)
	if err != nil || old.Outcome.Kind != "unconfirmed" {
		t.Fatal("local stop rewrote the inference outcome", err)
	}
}
func TestAttemptLedgerRejectsIncompatibleIndex(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	if _, err := s.Pool.Exec(ctx, "DROP INDEX batch_attempts_unsettled_item; CREATE INDEX batch_attempts_unsettled_item ON batch_attempts(permit_id)"); err != nil {
		t.Fatal(err)
	}
	if err := ApplySchema(ctx, s.Pool); err == nil {
		t.Fatal("incompatible named execution index accepted")
	}
}

func TestAttemptConcurrentReservationAcknowledgesSamePermit(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	ctx := context.Background()
	p := permitFixture(ref, 1)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := s.reserveAttempt(ctx, p, reservationWriteOptions{afterMissing: func() { arrived <- struct{}{}; <-release }})
			done <- err
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("concurrent reservation did not reach the owned SQL barrier")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal("repeat metadata starter did not acknowledge the same permit", err)
		}
	}
	var rows int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM batch_attempts WHERE permit_id=$1", p.PermitID).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("same starter created duplicate attempts", rows, err)
	}
}

func TestAttemptUnknownSettlementCommitIsMetadataOnly(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	ctx := context.Background()
	p := permitFixture(ref, 1)
	nonce := uuid.NewString()
	if _, err := s.ReserveAttempt(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimAttempt(ctx, p.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDispatch(ctx, p.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	finish := time.Now().UTC()
	o := AttemptObservation{Outcome: Outcome{Kind: "completed", ArtifactID: "one.json", FinishedAt: &finish}, NativeEntered: true, LocalStopped: true, ObservedAt: finish}
	commits := 0
	settled, err := s.settleAttempt(ctx, p.PermitID, nonce, o, func(ctx context.Context, tx pgx.Tx) error {
		commits++
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("synthetic lost settlement acknowledgement")
	})
	if err != nil || settled.State != "settled" || commits != 1 || settled.Counts.Succeeded != 1 {
		t.Fatal("unknown settlement replaced or lost completion", err, commits)
	}
	if _, err := s.ClaimAttempt(ctx, p.PermitID, uuid.NewString()); !errors.Is(err, ErrClaimOwned) {
		t.Fatal("settlement uncertainty authorized new inference", err)
	}
}
func TestAttemptOrdinaryStoresMetadataOnly(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	p := permitFixture(Ref{Scope: strings.Repeat("b", 64)}, 1)
	p.Kind = "ordinary"
	p.RequestID = uuid.NewString()
	nonce := uuid.NewString()
	if _, err := s.ReserveAttempt(ctx, p); err != nil {
		t.Fatal("ordinary metadata depended on a manifest", err)
	}
	if _, err := s.ClaimAttempt(ctx, p.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDispatch(ctx, p.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	o := AttemptObservation{Outcome: Outcome{Kind: "failed", Error: &ItemError{Code: "synthetic_error", Message: "The ordinary execution failed."}}, NativeEntered: true, LocalStopped: true, ObservedAt: time.Now().UTC()}
	if _, err := s.SettleAttempt(ctx, p.PermitID, nonce, o); err != nil {
		t.Fatal(err)
	}
	var jobs, items int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM batch_jobs WHERE api_key_hash=$1", p.Ref.Scope).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM batch_items WHERE api_key_hash=$1", p.Ref.Scope).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 || items != 0 {
		t.Fatal("ordinary admission created inference manifests", jobs, items)
	}
}

func TestAttemptReservationCannotChangeAcceptedBudget(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	p := permitFixture(ref, 1)
	p.BudgetMS = 900000
	if _, err := s.ReserveAttempt(context.Background(), p); !errors.Is(err, ErrConflict) {
		t.Fatal("reservation rewrote the accepted generation budget", err)
	}
}

func TestAttemptLedgerAdoptsLegacySchemaWithoutChangingAcceptedData(t *testing.T) {
	t.Parallel()
	_, dsn := integrationtest.PostgresLease(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE unrelated_sentinel(value text); INSERT INTO unrelated_sentinel VALUES('retained')"); err != nil {
		t.Fatal(err)
	}
	s := &PostgresStore{Pool: pool}
	job, err := s.Create(ctx, strings.Repeat("a", 64), uuid.NewString(), []Item{{CustomID: "legacy", Generation: Generation{ModelID: "gpt-6-luna"}}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err := pool.QueryRow(ctx, "SELECT request::text FROM batch_items WHERE job_id=$1", job.JobID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ApplySchema(ctx, pool); err != nil {
			t.Fatal("additive/repeated adoption", err)
		}
	}
	var after, sentinel string
	var ordinal int64
	var current *string
	if err := pool.QueryRow(ctx, "SELECT request::text,reservation_ordinal,current_permit::text FROM batch_items WHERE job_id=$1", job.JobID).Scan(&after, &ordinal, &current); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT value FROM unrelated_sentinel").Scan(&sentinel); err != nil {
		t.Fatal(err)
	}
	if before != after || ordinal != 0 || current != nil || sentinel != "retained" {
		t.Fatal("schema adoption changed accepted or unrelated data")
	}
	if err := s.EnsurePool(ctx, "fixture-pool", 2); err != nil {
		t.Fatal(err)
	}
	p := permitFixture(Ref{Scope: job.Scope, JobID: job.JobID, Index: 0}, 1)
	p.BudgetMS = LegacyGenerationBudget.Milliseconds()
	if _, err := s.ReserveAttempt(ctx, p); err != nil {
		t.Fatal("legacy zero snapshot cannot enter ledger", err)
	}
}
