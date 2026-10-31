//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-515

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRequestReservationRequiresLiveOwnerMetadata(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	r := permitFixture(Ref{Scope: ref.Scope}, 1)
	r.Kind, r.RequestID = "ordinary", uuid.NewString()
	if _, err := s.ReserveAttempt(context.Background(), r); err == nil {
		t.Fatal("ordinary permit admitted without a request-bound waiter")
	}
}

func requestFixture(t *testing.T, s *PostgresStore, sequence int64) (RequestWaiter, Reservation) {
	t.Helper()
	r := permitFixture(Ref{Scope: strings.Repeat("b", 64)}, sequence)
	r.Kind, r.RequestID = "ordinary", uuid.NewString()
	w, err := s.EnqueueRequest(context.Background(), RequestWaiter{RequestID: r.RequestID, PoolID: r.PoolID, Generation: r.Generation, Scope: r.Ref.Scope, OwnerID: uuid.NewString(), ExecutionID: uuid.NewString(), Ordinal: 1, Deadline: r.Deadline, BudgetMS: r.BudgetMS})
	if err != nil {
		t.Fatal(err)
	}
	return w, r
}

func TestRequestWaiterScopeIdentityAndCancellation(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	w, r := requestFixture(t, s, 1)
	again, err := s.EnqueueRequest(ctx, w)
	if err != nil || again.Sequence != w.Sequence || !again.LiveUntil.Equal(w.LiveUntil) {
		t.Fatal("metadata retry changed owner/sequence/liveness", again, err)
	}
	changed := w
	changed.Ordinal++
	if _, err := s.EnqueueRequest(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("request identity rewritten", err)
	}
	for _, bad := range []struct{ scope, owner string }{{strings.Repeat("c", 64), w.OwnerID}, {w.Scope, uuid.NewString()}} {
		if _, err := s.ReadRequest(ctx, bad.scope, w.RequestID, bad.owner, true); !errors.Is(err, ErrNotFound) {
			t.Fatal("waiter owner leaked", err)
		}
		if err := s.CancelRequest(ctx, bad.scope, w.RequestID, bad.owner); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign caller canceled waiter", err)
		}
	}
	if err := s.CancelRequest(ctx, w.Scope, w.RequestID, w.OwnerID); err != nil {
		t.Fatal(err)
	}
	reserved, err := s.ReserveAttempt(ctx, r)
	if err != nil || reserved.State != "settled" || reserved.Observation == nil || reserved.Observation.Outcome.Kind != "never_dispatched" || reserved.StoppedAt == nil {
		t.Fatal("canceled selection lost safe settlement", reserved, err)
	}
	if _, err := s.ClaimAttempt(ctx, r.PermitID, w.OwnerID); !errors.Is(err, ErrClaimOwned) {
		t.Fatal("abandoned request claimed inference", err)
	}
}

func TestRequestClaimRequiresLiveOwnerAndCancelBlocksDispatch(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	w, r := requestFixture(t, s, 1)
	if _, err := s.ReserveAttempt(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimAttempt(ctx, r.PermitID, uuid.NewString()); !errors.Is(err, ErrClaimOwned) {
		t.Fatal("foreign invocation claimed ordinary permit", err)
	}
	if _, err := s.ClaimAttempt(ctx, r.PermitID, w.OwnerID); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelRequest(ctx, w.Scope, w.RequestID, w.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDispatch(ctx, r.PermitID, w.OwnerID); !errors.Is(err, ErrClaimOwned) {
		t.Fatal("canceled claim dispatched", err)
	}
	read, err := s.ReadAttempts(ctx, r.PoolID, []string{r.PermitID})
	if err != nil || len(read) != 1 || read[0].State != "dispatching" || read[0].StoppedAt != nil {
		t.Fatal("cancellation pretended local transport stopped", read, err)
	}
	now := time.Now().UTC()
	o := AttemptObservation{Outcome: Outcome{Kind: "never_dispatched"}, LocalStopped: true, ObservedAt: now}
	if _, err := s.SettleAttempt(ctx, r.PermitID, w.OwnerID, o); err != nil {
		t.Fatal("live owner cannot attest never entered", err)
	}
}

func TestRequestExpiredLivenessCannotResurrectOrDispatch(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	w, r := requestFixture(t, s, 1)
	if _, err := s.Pool.Exec(ctx, `UPDATE batch_request_waiters SET live_until=now()-interval '1 second' WHERE request_id=$1`, w.RequestID); err != nil {
		t.Fatal(err)
	}
	read, err := s.ReadRequest(ctx, w.Scope, w.RequestID, w.OwnerID, true)
	if err != nil || !read.LiveUntil.Before(time.Now()) {
		t.Fatal("expired live waiter resurrected", read, err)
	}
	reserved, err := s.ReserveAttempt(ctx, r)
	if err != nil || reserved.State != "settled" || reserved.Observation == nil || reserved.Observation.Outcome.Kind != "never_dispatched" {
		t.Fatal("expired selection did not settle without inference", reserved, err)
	}
	if _, err := s.ClaimAttempt(ctx, r.PermitID, w.OwnerID); !errors.Is(err, ErrClaimOwned) {
		t.Fatal("expired caller dispatched", err)
	}
}

func TestRequestCompletionNeedsNoArtifactAndIsImmutable(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	w, r := requestFixture(t, s, 1)
	if _, err := s.ReserveAttempt(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimAttempt(ctx, r.PermitID, w.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDispatch(ctx, r.PermitID, w.OwnerID); err != nil {
		t.Fatal(err)
	}
	finish := time.Now().UTC()
	o := AttemptObservation{Outcome: Outcome{Kind: "completed", FinishedAt: &finish}, NativeEntered: true, LocalStopped: true, ObservedAt: finish}
	bad := o
	bad.Outcome.ArtifactID = "forbidden-result.json"
	if _, err := s.SettleAttempt(ctx, r.PermitID, w.OwnerID, bad); !errors.Is(err, ErrSettlementConflict) {
		t.Fatal("direct-call artifact accepted", err)
	}
	retained, err := s.SettleAttempt(ctx, r.PermitID, w.OwnerID, o)
	if err != nil || retained.State != "settled" || retained.StoppedAt == nil {
		t.Fatal("metadata-only ordinary success not retained", retained, err)
	}
	if _, err := s.SettleAttempt(ctx, r.PermitID, w.OwnerID, o); err != nil {
		t.Fatal("metadata retry changed completion", err)
	}
	bad = o
	bad.Outcome.Kind = "failed"
	if _, err := s.SettleAttempt(ctx, r.PermitID, w.OwnerID, bad); !errors.Is(err, ErrSettlementConflict) {
		t.Fatal("late ordinary outcome overwritten", err)
	}
	var jobs, items int
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM batch_jobs WHERE api_key_hash=$1),(SELECT count(*) FROM batch_items WHERE api_key_hash=$1)`, w.Scope).Scan(&jobs, &items); err != nil || jobs != 0 || items != 0 {
		t.Fatal("ordinary completion fabricated batch history", jobs, items, err)
	}
}

func TestRequestCancelRacesClaimWithoutRevokingOwnership(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	for sequence := int64(1); sequence <= 12; sequence++ {
		w, r := requestFixture(t, s, sequence)
		if _, err := s.ReserveAttempt(ctx, r); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var claimed atomic.Bool
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			_, err := s.ClaimAttempt(ctx, r.PermitID, w.OwnerID)
			if err == nil {
				claimed.Store(true)
			} else if !errors.Is(err, ErrClaimOwned) {
				t.Error(err)
			}
		}()
		go func() {
			defer wait.Done()
			<-start
			if err := s.CancelRequest(ctx, w.Scope, w.RequestID, w.OwnerID); err != nil {
				t.Error(err)
			}
		}()
		close(start)
		wait.Wait()
		if _, err := s.MarkDispatch(ctx, r.PermitID, w.OwnerID); !errors.Is(err, ErrClaimOwned) {
			t.Fatal("cancel/claim race allowed abandoned dispatch", err)
		}
		read, err := s.ReadAttempts(ctx, r.PoolID, []string{r.PermitID})
		if err != nil || len(read) != 1 || read[0].StoppedAt != nil || (claimed.Load() && read[0].ClaimID != w.OwnerID) {
			t.Fatal("cancel inferred stop or changed live claim", read, err)
		}
	}
}

func expireWaiterForRetention(t *testing.T, s *PostgresStore, w RequestWaiter) {
	t.Helper()
	_, err := s.Pool.Exec(context.Background(), `UPDATE batch_request_waiters SET created_at=now()-interval '27 hours',deadline=now()-interval '26 hours',live_until=now()-interval '26 hours',retention_until=now()-interval '2 hours' WHERE request_id=$1`, w.RequestID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRequestRetentionRequiresPositivePoolSettlementAndLocalStop(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	w, r := requestFixture(t, s, 1)
	if _, err := s.ReserveAttempt(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimAttempt(ctx, r.PermitID, w.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDispatch(ctx, r.PermitID, w.OwnerID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	o := AttemptObservation{Outcome: Outcome{Kind: "unconfirmed"}, NativeEntered: true, ObservedAt: now}
	if _, err := s.SettleAttempt(ctx, r.PermitID, w.OwnerID, o); err != nil {
		t.Fatal(err)
	}
	expireWaiterForRetention(t, s, w)
	read := PoolRead{PoolID: r.PoolID, Generation: r.Generation, IDs: []string{r.PermitID}}
	if err := s.AcknowledgeRequests(ctx, read); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneRequests(ctx, r.PoolID, 128); err != nil || n != 0 {
		t.Fatal("unknown local stop deleted", n, err)
	}
	if _, err := s.MarkLocalStop(ctx, r.PermitID, w.OwnerID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneRequests(ctx, r.PoolID, 128); err != nil || n != 0 {
		t.Fatal("unapplied pool settlement deleted", n, err)
	}
	if err := s.AcknowledgeRequests(ctx, read); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneRequests(ctx, r.PoolID, 128); err != nil || n != 1 {
		t.Fatal("retired metadata not pruned", n, err)
	}
	retained, err := s.ReadAttempts(ctx, r.PoolID, read.IDs)
	if err != nil || len(retained) != 0 {
		t.Fatal("retired ledger remains", retained, err)
	}
	if _, err := s.ReadRequest(ctx, w.Scope, w.RequestID, w.OwnerID, false); !errors.Is(err, ErrNotFound) {
		t.Fatal("retired waiter remains", err)
	}
}

func TestRequestQueueSharesScopeAndOwnerRotationWithJobs(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	jobA := attachQueueJob(t, s, a, 4)
	w, r := requestFixture(t, s, 1) // B, before B's job.
	jobB := attachQueueJob(t, s, b, 4)
	page, err := s.SelectReady(ctx, QueueSelection{PoolID: r.PoolID, Generation: 2, Limit: 6})
	if err != nil || len(page.Items) != 6 {
		t.Fatal("mixed ready page", page, err)
	}
	for i, item := range page.Items {
		wantScope := a
		if i%2 == 1 {
			wantScope = b
		}
		if item.Ref.Scope != wantScope {
			t.Fatal("ordinary calls bought extra scope share", i, item)
		}
		if i == 1 {
			if item.Kind != "ordinary" || item.RequestID != w.RequestID || item.Ref.JobID != "" || item.BudgetMS != w.BudgetMS {
				t.Fatal("ordinary waiter projected as inference manifest", item)
			}
		} else {
			want := jobA.JobID
			if i%2 == 1 {
				want = jobB.JobID
			}
			if item.Kind != "" || item.Ref.JobID != want {
				t.Fatal("job rotation changed", i, item)
			}
		}
	}
	page, err = s.SelectReady(ctx, QueueSelection{PoolID: r.PoolID, Generation: 2, Limit: 8, ExcludeRequests: []string{w.RequestID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.RequestID != "" {
			t.Fatal("unpersisted request intent selected again", item)
		}
	}
	if _, err := s.ReserveAttempt(ctx, r); err != nil {
		t.Fatal(err)
	}
	page, err = s.SelectReady(ctx, QueueSelection{PoolID: r.PoolID, Generation: 2, Limit: 8})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.RequestID != "" {
			t.Fatal("reserved request selected again", item)
		}
	}
}

func TestRequestQueueNeverSelectsClosedOrExpiredLiveOwner(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	for seq := int64(1); seq <= 3; seq++ {
		w, _ := requestFixture(t, s, seq)
		switch seq {
		case 1:
			if err := s.CancelRequest(ctx, w.Scope, w.RequestID, w.OwnerID); err != nil {
				t.Fatal(err)
			}
		case 2:
			if _, err := s.Pool.Exec(ctx, `UPDATE batch_request_waiters SET live_until=now()-interval '1 second' WHERE request_id=$1`, w.RequestID); err != nil {
				t.Fatal(err)
			}
		case 3:
			expireWaiterForRetention(t, s, w)
		}
	}
	page, err := s.SelectReady(ctx, QueueSelection{PoolID: "fixture-pool", Generation: 2, Limit: 64})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("abandoned ordinary owner remained eligible", page, err)
	}
}

func TestRequestWaitingPublicationSharesBoundedPoolPages(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	w, _ := requestFixture(t, s, 1)
	job := attachQueueJob(t, s, w.Scope, 1)
	next := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	p := PoolPublication{PoolID: w.PoolID, Generation: 2, Limit: 1, Reason: "provider_throttled", Next: &next}
	page, err := s.PublishPool(ctx, p)
	if err != nil || page.Updated != 1 || !page.More || page.After != w.Sequence {
		t.Fatal("mixed publication was unbounded or skipped request", page, err)
	}
	read, err := s.ReadRequest(ctx, w.Scope, w.RequestID, w.OwnerID, false)
	if err != nil || read.WaitingReason != p.Reason || read.PoolHeartbeatAt == nil || read.NextAttemptAt == nil || !read.NextAttemptAt.Equal(next) {
		t.Fatal("ordinary caller cannot observe cooldown floor", read, err)
	}
	page, err = s.PublishPool(ctx, p)
	if err != nil || page.Updated != 0 {
		t.Fatal("identical waiter publication not coalesced", page, err)
	}
	p.After = w.Sequence
	page, err = s.PublishPool(ctx, p)
	if err != nil || page.More || page.Updated != 1 {
		t.Fatal("second bounded publication skipped job", page, err)
	}
	status, err := s.Status(ctx, job.Scope, job.JobID)
	if err != nil || status.WaitingReason != p.Reason {
		t.Fatal("mixed publication changed job waiting contract", status, err)
	}
}

func TestRequestRetentionPruningIsPagedAndSchemaRejectsWeakerIndex(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	for seq := int64(1); seq <= 4; seq++ {
		w, _ := requestFixture(t, s, seq)
		expireWaiterForRetention(t, s, w)
	}
	if n, err := s.PruneRequests(ctx, "fixture-pool", 2); err != nil || n != 2 {
		t.Fatal("pruning ignored page bound", n, err)
	}
	if n, err := s.PruneRequests(ctx, "fixture-pool", 2); err != nil || n != 2 {
		t.Fatal("second pruning page lost rows", n, err)
	}
	if err := ApplySchema(ctx, s.Pool); err != nil {
		t.Fatal("additive schema not repeatable", err)
	}
	if _, err := s.Pool.Exec(ctx, `DROP INDEX batch_request_waiters_ready`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `CREATE INDEX batch_request_waiters_ready ON batch_request_waiters(pool_id)`); err != nil {
		t.Fatal(err)
	}
	if err := ApplySchema(ctx, s.Pool); err == nil {
		t.Fatal("weakened request ready index accepted")
	}
}

func TestRequestDispatchOrdinalSurvivesIndependentReservation(t *testing.T) {
	t.Parallel()
	s, _ := ledgerFixture(t)
	w, _ := requestFixture(t, s, 1)
	w.RequestID = uuid.NewString()
	w.OwnerID = uuid.NewString()
	w.Ordinal = 2
	w, err := s.EnqueueRequest(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	r := permitFixture(Ref{Scope: w.Scope}, 2)
	r.Kind, r.RequestID, r.Deadline, r.BudgetMS = "ordinary", w.RequestID, w.Deadline, w.BudgetMS
	a, err := s.ReserveAttempt(context.Background(), r)
	if err != nil || a.Ordinal != 2 {
		t.Fatal("repair/retry dispatch lost ordinal", a.Ordinal, err)
	}
}
