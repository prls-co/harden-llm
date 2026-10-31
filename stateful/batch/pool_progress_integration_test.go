//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-508

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestPoolPublicationIsBoundedScopedAndDoesNotInventProgress(t *testing.T) {
	t.Parallel()
	s, unattached := ledgerFixture(t)
	ctx := context.Background()
	first := attachQueueJob(t, s, strings.Repeat("b", 64), 2)
	second := attachQueueJob(t, s, strings.Repeat("c", 64), 2)
	next := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	p := PoolPublication{PoolID: "fixture-pool", Generation: 2, Reason: "provider_throttled", Next: &next, Limit: 1}
	page, err := s.PublishPool(ctx, p)
	if err != nil || page.Updated != 1 || !page.More {
		t.Fatal("unbounded/missing publication", page, err)
	}
	p.After = page.After
	page, err = s.PublishPool(ctx, p)
	if err != nil || page.Updated != 1 || page.More {
		t.Fatal("publication cursor did not finish", page, err)
	}
	for _, job := range []JobRef{first, second} {
		v, err := s.Status(ctx, job.Scope, job.JobID)
		if err != nil || v.WaitingReason != "provider_throttled" || v.LastWorkerHeartbeat == nil || v.LastProgressAt != nil || v.NextAttemptAt == nil || !v.NextAttemptAt.Equal(next) || v.Counts.Queued != 2 {
			t.Fatal("publication changed execution/progress or waiting floor", err, v)
		}
	}
	before, _ := s.Status(ctx, first.Scope, first.JobID)
	p.After = 0
	page, err = s.PublishPool(ctx, p)
	if err != nil || page.Updated != 0 {
		t.Fatal("identical tick was not coalesced", page, err)
	}
	p.Reason = "account_restricted"
	p.Next = nil
	page, err = s.PublishPool(ctx, p)
	if err != nil || page.Updated != 1 {
		t.Fatal("real gate transition hidden by coalescing", page, err)
	}
	after, _ := s.Status(ctx, first.Scope, first.JobID)
	if after.WaitingReason != "account_restricted" || after.LastProgressAt != nil || !after.LastWorkerHeartbeat.Equal(*before.LastWorkerHeartbeat) {
		t.Fatal("gate update fabricated progress/heartbeat", after)
	}
	untouched, _ := s.Status(ctx, unattached.Scope, unattached.JobID)
	if untouched.LastWorkerHeartbeat != nil {
		t.Fatal("publication touched unattached manifest")
	}
	p.Generation = 1
	if _, err := s.PublishPool(ctx, p); !errors.Is(err, ErrGenerationFenced) {
		t.Fatal("obsolete publisher generation changed status", err)
	}
	p.Generation = 2
	p.Limit = 129
	if _, err := s.PublishPool(ctx, p); err == nil {
		t.Fatal("unbounded publisher accepted")
	}
}
func TestPoolHeartbeatCoalescesPerJobAndFencesInvocation(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	ctx := context.Background()
	r := permitFixture(ref, 1)
	nonce := uuid.NewString()
	if _, err := s.ReserveAttempt(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimAttempt(ctx, r.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.MarkDispatch(ctx, r.PermitID, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatAttempt(ctx, attempt, true); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Status(ctx, ref.Scope, ref.JobID)
	if before.LastWorkerHeartbeat == nil || before.LastProgressAt == nil {
		t.Fatal("actual bytes did not record liveness/progress")
	}
	if err := s.HeartbeatAttempt(ctx, attempt, true); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Status(ctx, ref.Scope, ref.JobID)
	if !after.LastWorkerHeartbeat.Equal(*before.LastWorkerHeartbeat) || !after.LastProgressAt.Equal(*before.LastProgressAt) {
		t.Fatal("same five-second window issued another job update")
	}
	past := time.Now().UTC().Add(-6 * time.Second).Truncate(time.Microsecond)
	if _, err := s.Pool.Exec(ctx, "UPDATE batch_jobs SET last_heartbeat=$3,last_progress=$3 WHERE api_key_hash=$1 AND id=$2", ref.Scope, ref.JobID, past); err != nil {
		t.Fatal(err)
	}
	foreign := attempt
	foreign.ClaimID = uuid.NewString()
	_ = s.HeartbeatAttempt(ctx, foreign, true)
	unchanged, _ := s.Status(ctx, ref.Scope, ref.JobID)
	if !unchanged.LastWorkerHeartbeat.Equal(past) {
		t.Fatal("foreign invocation advanced observation")
	}
	if err := s.HeartbeatAttempt(ctx, attempt, false); err != nil {
		t.Fatal(err)
	}
	after, _ = s.Status(ctx, ref.Scope, ref.JobID)
	if !after.LastProgressAt.Equal(past) || !after.LastWorkerHeartbeat.After(past) {
		t.Fatal("heartbeat invented byte progress", after)
	}
}
