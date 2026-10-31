package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-525

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func successfulWakeAttempt(id string) Attempt {
	now := time.Now()
	return Attempt{Reservation: Reservation{PoolID: "pool", PermitID: id, Kind: "job"}, State: "settled", SettlementRevision: 1, StoppedAt: &now, Observation: &AttemptObservation{Outcome: Outcome{Kind: "completed"}, LocalStopped: true}}
}

func TestDurablePoolWakeCoalescesConcurrentSettlements(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		a := &Activities{PoolID: "pool", Temporal: starterTemporal{signal: func(ctx context.Context, id, run, name string, arg interface{}) error {
			calls.Add(1)
			notice, ok := arg.(SettlementNotice)
			deadline, bounded := ctx.Deadline()
			if id != "hllm-cpa-pool-v1/pool" || run != "" || name != settlementSignal || !ok || notice.Revision != 1 || !bounded || time.Until(deadline) > time.Second {
				t.Error("coalesced hint changed identity or lost bounded RPC", id, name, arg)
			}
			return nil
		}}}
		var joined sync.WaitGroup
		for range 32 {
			joined.Go(func() {
				if err := a.NotifySettlement(context.Background(), successfulWakeAttempt("permit")); err != nil {
					t.Error(err)
				}
			})
		}
		joined.Wait()
		if calls.Load() != 1 {
			t.Fatal("successful settlements amplified one pool read into per-item signals", calls.Load())
		}
	})
}

func TestDurablePoolWakeTrailsInflightAndKeepsCanceledFollowers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var calls, active, peak atomic.Int32
		a := &Activities{PoolID: "pool", Temporal: starterTemporal{signal: func(ctx context.Context, _, _, _ string, _ interface{}) error {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			if calls.Add(1) == 1 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}}}
		var joined sync.WaitGroup
		joined.Go(func() {
			if err := a.NotifySettlement(context.Background(), successfulWakeAttempt("first")); err != nil {
				t.Error(err)
			}
		})
		<-entered // The first batch is sealed and its RPC is still in flight.
		for range 16 {
			joined.Go(func() {
				if err := a.NotifySettlement(context.Background(), successfulWakeAttempt("trailing")); err != nil {
					t.Error(err)
				}
			})
		}
		synctest.Wait() // Establish a trailing owner before adding a canceled follower.
		canceled, cancel := context.WithCancel(context.Background())
		canceledDone := make(chan error, 1)
		go func() { canceledDone <- a.NotifySettlement(canceled, successfulWakeAttempt("canceled-follower")) }()
		synctest.Wait()
		cancel()
		if err := <-canceledDone; !errors.Is(err, context.Canceled) {
			t.Error("follower cancellation was lost", err)
		}
		if calls.Load() != 1 {
			t.Error("trailing batch overlapped an in-flight RPC", calls.Load())
		}
		close(release)
		joined.Wait()
		if calls.Load() != 2 || peak.Load() != 1 {
			t.Fatal("sealed-batch arrivals lost their trailing wake or unbounded RPC overlap", calls.Load(), peak.Load())
		}
	})
}

func TestDurablePoolWakeCanceledOwnerStillDeliversFollowers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		a := &Activities{PoolID: "pool", Temporal: starterTemporal{signal: func(context.Context, string, string, string, interface{}) error {
			calls.Add(1)
			return nil
		}}}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		owner := make(chan error, 1)
		go func() { owner <- a.NotifySettlement(ctx, successfulWakeAttempt("owner")) }()
		synctest.Wait()
		follower := make(chan error, 1)
		go func() { follower <- a.NotifySettlement(context.Background(), successfulWakeAttempt("follower")) }()
		synctest.Wait()
		cancel()
		if err := <-follower; err != nil {
			t.Error("owner cancellation discarded another committed hint", err)
		}
		if err := <-owner; !errors.Is(err, context.Canceled) || calls.Load() != 1 {
			t.Fatal("owner cancellation was hidden or shared hint was lost", err, calls.Load())
		}
	})
}

func TestDurablePoolWakeClaimsJoinSettlementsWithoutOrphanSender(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		a := &Activities{PoolID: "pool", Temporal: starterTemporal{signal: func(context.Context, string, string, string, interface{}) error {
			calls.Add(1)
			return nil
		}}}
		stop := a.startClaimReadHint(context.Background(), successfulWakeAttempt("claim"))
		synctest.Wait()
		done := make(chan error, 1)
		go func() { done <- a.NotifySettlement(context.Background(), successfulWakeAttempt("settled")) }()
		synctest.Wait()
		stop() // A short invocation still joins its bounded sender before returning.
		if err := <-done; err != nil || calls.Load() != 1 {
			t.Fatal("claim/settlement wake was dropped or duplicated", err, calls.Load())
		}
	})
}

func TestDurablePoolWakeBoundsRPCAndRejectsForeignPool(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		a := &Activities{PoolID: "pool", Temporal: starterTemporal{signal: func(ctx context.Context, _, _, _ string, _ interface{}) error {
			calls.Add(1)
			<-ctx.Done()
			return ctx.Err()
		}}}
		started := time.Now()
		if err := a.NotifySettlement(context.Background(), successfulWakeAttempt("blocked")); !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != time.Second {
			t.Error("signal collection/admission/RPC lifetime is unbounded", err, time.Since(started))
		}
		foreign := successfulWakeAttempt("foreign")
		foreign.PoolID = "other"
		if err := a.NotifySettlement(context.Background(), foreign); !errors.Is(err, ErrGenerationFenced) || calls.Load() != 1 {
			t.Fatal("coalescing crossed the configured pool boundary", err, calls.Load())
		}
	})
}

func TestDurablePoolWakeFailureAndEarlierDeadlineAreNotCached(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		lost := errors.New("synthetic signal acknowledgement loss")
		a := &Activities{PoolID: "pool", Temporal: starterTemporal{signal: func(context.Context, string, string, string, interface{}) error {
			if calls.Add(1) == 1 {
				return lost
			}
			return nil
		}}}
		short, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer stop()
		if err := a.NotifySettlement(short, successfulWakeAttempt("short")); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 0 {
			t.Error("earlier caller deadline did not bound collection/RPC", err, calls.Load())
		}
		if err := a.NotifySettlement(context.Background(), successfulWakeAttempt("lost")); !errors.Is(err, lost) {
			t.Error("failed acknowledgement was hidden", err)
		}
		if err := a.NotifySettlement(context.Background(), successfulWakeAttempt("next")); err != nil || calls.Load() != 2 {
			t.Fatal("failure was cached or a subsequent batch could not run", err, calls.Load())
		}
	})
}

func TestDurablePoolWakeRestrictionsBypassSuccessfulBatch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		a := &Activities{PoolID: "pool", Temporal: starterTemporal{signal: func(ctx context.Context, _, _, _ string, arg interface{}) error {
			calls.Add(1)
			if arg.(SettlementNotice).PermitID == "normal" {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}}}
		done := make(chan error, 1)
		go func() { done <- a.NotifySettlement(context.Background(), successfulWakeAttempt("normal")) }()
		<-entered
		for _, kind := range []string{"policy", "rejected", "unconfirmed"} {
			urgent := successfulWakeAttempt(kind)
			urgent.Observation.Outcome.Kind = kind
			if err := a.NotifySettlement(context.Background(), urgent); err != nil {
				t.Error("critical observation waited behind successful wake", kind, err)
			}
		}
		if calls.Load() != 4 {
			t.Error("critical hint was coalesced or suppressed", calls.Load())
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}
