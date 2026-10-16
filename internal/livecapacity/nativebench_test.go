package livecapacity

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-471

import (
	"context"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativePoolContinuesAfterUnknownAndNeverReplaysIt(t *testing.T) {
	var calls atomic.Int32
	b := NativeBench{Directory: t.TempDir(), Events: io.Discard, Started: time.Now(), Call: func(ctx context.Context, prefix string, id, attempt int) NativeMeasurement {
		calls.Add(1)
		kind := "completed"
		if id == 0 {
			kind = "unconfirmed"
		}
		return NativeMeasurement{ID: id, Attempt: attempt, Kind: kind, Started: time.Now(), Finished: time.Now()}
	}}
	p, err := b.Point(context.Background(), "fixture", 2, 7, 0, true)
	if err != nil || p.Completed != 6 || p.Unknown != 1 || calls.Load() != 7 || p.Dispatched != 7 {
		t.Fatalf("pool halted or replayed unknown: %+v %v calls=%d", p, err, calls.Load())
	}
}
func TestNativePoolOverlapsAndRetriesOnlyKnownRejection(t *testing.T) {
	var active, peak atomic.Int32
	ready := make(chan struct{})
	b := NativeBench{Directory: t.TempDir(), Events: io.Discard, Started: time.Now(), FallbackPause: time.Millisecond, Call: func(ctx context.Context, prefix string, id, attempt int) NativeMeasurement {
		n := active.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if n == 4 {
			close(ready)
		}
		if attempt == 1 {
			select {
			case <-ready:
			case <-ctx.Done():
			}
		}
		active.Add(-1)
		kind := "completed"
		if id == 0 && attempt == 1 {
			kind = "rejected"
		}
		return NativeMeasurement{ID: id, Attempt: attempt, Kind: kind, Started: time.Now(), Finished: time.Now()}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p, err := b.Point(ctx, "fixture", 4, 4, 0, true)
	if err != nil || p.Completed != 4 || p.Dispatched != 5 || p.Rejections != 1 || peak.Load() != 4 {
		t.Fatalf("retry/overlap incorrect: %+v %v peak=%d", p, err, peak.Load())
	}
}

func TestNativeSearchDoublesThenBisectsKnownThrottleBoundary(t *testing.T) {
	b := NativeBench{Directory: t.TempDir(), Events: io.Discard, Started: time.Now(), Call: func(ctx context.Context, prefix string, id, attempt int) NativeMeasurement {
		kind := "completed"
		if id >= 6 {
			kind = "rejected"
		}
		return NativeMeasurement{ID: id, Attempt: attempt, Kind: kind, Started: time.Now(), Finished: time.Now()}
	}}
	lo, hi, err := b.Search(context.Background(), 16, 1, 0)
	if err != nil || lo != 6 || hi != 7 {
		t.Fatalf("bad bracket: %d %d %v", lo, hi, err)
	}
	expected := []int{1, 2, 4, 8, 6, 7}
	if len(b.Points) != len(expected) {
		t.Fatal(b.Points)
	}
	for i, c := range expected {
		if b.Points[i].Concurrency != c {
			t.Fatal("wrong search sequence", b.Points)
		}
	}
}

func TestNativeWindowBudgetDoesNotRefillOnFastCompletion(t *testing.T) {
	var starts []time.Time
	var mu sync.Mutex
	b := NativeBench{Directory: t.TempDir(), Events: io.Discard, Started: time.Now(), WindowBurst: 2, WindowDuration: 50 * time.Millisecond, Call: func(ctx context.Context, prefix string, id, attempt int) NativeMeasurement {
		now := time.Now()
		mu.Lock()
		starts = append(starts, now)
		mu.Unlock()
		return NativeMeasurement{ID: id, Attempt: attempt, Kind: "completed", Started: now, Finished: now}
	}}
	p, err := b.Point(context.Background(), "window", 4, 6, 0, true)
	if err != nil || p.Completed != 6 {
		t.Fatalf("window queue failed: %+v %v", p, err)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	if starts[2].Sub(starts[0]) < 45*time.Millisecond || starts[4].Sub(starts[2]) < 45*time.Millisecond {
		t.Fatal("fast completions refilled quota", starts)
	}
}
func TestNativeRecoveryProbeDoesNotWaitForAnIndependentSlowCall(t *testing.T) {
	release := make(chan struct{})
	b := NativeBench{Directory: t.TempDir(), Events: io.Discard, Started: time.Now(), FallbackPause: time.Millisecond, Call: func(ctx context.Context, prefix string, id, attempt int) NativeMeasurement {
		kind := "completed"
		if id == 0 {
			select {
			case <-release:
			case <-ctx.Done():
				kind = "unconfirmed"
			}
		}
		if id == 1 && attempt == 1 {
			kind = "rejected"
		}
		if id == 1 && attempt == 2 {
			close(release)
		}
		return NativeMeasurement{ID: id, Attempt: attempt, Kind: kind, Started: time.Now(), Finished: time.Now()}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p, err := b.Point(ctx, "probe", 2, 3, 0, true)
	if err != nil || p.Completed != 3 || ctx.Err() != nil {
		t.Fatalf("recovery blocked on unrelated call: %+v %v", p, err)
	}
}

func TestNativeWindowRefundsOnlyKnownRejections(t *testing.T) {
	for _, kind := range []string{"rejected", "unconfirmed"} {
		t.Run(kind, func(t *testing.T) {
			var count atomic.Int32
			b := NativeBench{Directory: t.TempDir(), Events: io.Discard, Started: time.Now(), WindowBurst: 2, WindowDuration: time.Hour, FallbackPause: time.Millisecond, Call: func(ctx context.Context, prefix string, id, attempt int) NativeMeasurement {
				count.Add(1)
				result := "completed"
				if id == 0 && attempt == 1 {
					result = kind
				}
				return NativeMeasurement{ID: id, Attempt: attempt, Kind: result, Started: time.Now(), Finished: time.Now()}
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			n := 2
			if kind == "unconfirmed" {
				n = 3
			}
			p, err := b.Point(ctx, "refund", 2, n, 0, true)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "rejected" && (p.Completed != 2 || count.Load() != 3 || ctx.Err() != nil) {
				t.Fatalf("known refusal lost quota credit: %+v calls=%d", p, count.Load())
			}
			if kind == "unconfirmed" && (p.Completed != 1 || p.Unknown != 1 || count.Load() != 2) {
				t.Fatalf("unknown quota credit incorrectly refunded: %+v calls=%d", p, count.Load())
			}
		})
	}
}
