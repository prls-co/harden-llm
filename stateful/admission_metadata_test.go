package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-521

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful/batch"
)

type admissionLaneProbe struct {
	batch.RequestQueueStore
	entered      chan string
	release      chan struct{}
	active, peak atomic.Int32
}

func (s *admissionLaneProbe) block(ctx context.Context, lane string) error {
	n := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		old := s.peak.Load()
		if n <= old || s.peak.CompareAndSwap(old, n) {
			break
		}
	}
	s.entered <- lane
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *admissionLaneProbe) ReadRequest(ctx context.Context, _, _, _ string, _ bool) (batch.RequestWaiter, error) {
	return batch.RequestWaiter{}, s.block(ctx, "poll")
}
func (s *admissionLaneProbe) ClaimAttempt(ctx context.Context, _, _ string) (batch.Attempt, error) {
	return batch.Attempt{}, s.block(ctx, "claim")
}

func TestCPAAdmissionMetadataReservesCapacityForExistingPermits(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	probe := &admissionLaneProbe{entered: make(chan string, 8), release: make(chan struct{})}
	service, err := NewService(Config{Admission: &CPAAdmission{Store: probe, Signals: admissionSignals{}, PoolID: "pool", Endpoint: "https://fixture.invalid/v1"}, Client: ClientOptions{Connections: []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: "https://fixture.invalid/v1", APIKey: "synthetic-unused-key"}}, DefaultConnection: "cpa"}})
	if err != nil {
		t.Fatal(err)
	}
	store := service.config.Admission.Store
	var group sync.WaitGroup
	defer func() { cancel(); close(probe.release); group.Wait() }()
	launch := func(claim bool) {
		group.Add(1)
		go func() {
			defer group.Done()
			if claim {
				_, _ = store.ClaimAttempt(ctx, "permit", "owner")
			} else {
				_, _ = store.ReadRequest(ctx, "scope", "request", "owner", true)
			}
		}()
	}
	wait := func(want string) {
		t.Helper()
		select {
		case got := <-probe.entered:
			if got != want {
				t.Fatal(got, want)
			}
		case <-ctx.Done():
			t.Fatal("metadata lane starved", want)
		}
	}
	for range 2 {
		launch(false)
		wait("poll")
	}
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err = store.ReadRequest(short, "scope", "request", "owner", true)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("polling exceeded its lane", err)
	}
	// Existing grants must progress even while all polling capacity is held.
	for range 2 {
		launch(true)
		wait("claim")
	}
	short, stop = context.WithTimeout(ctx, 20*time.Millisecond)
	_, err = store.ClaimAttempt(short, "permit", "owner")
	stop()
	if !errors.Is(err, context.DeadlineExceeded) || probe.peak.Load() != 4 {
		t.Fatal("claim lane exceeded combined bound", err, probe.peak.Load())
	}
}
