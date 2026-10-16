package livecapacity

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-471

import (
	"context"
	"io"
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
