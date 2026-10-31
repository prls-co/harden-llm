package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-499

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	hardenllm "github.com/prls-co/harden-llm"
)

type invocationStore struct {
	ExecutionStore
	mu                              sync.Mutex
	attempt                         Attempt
	item                            StoredItem
	claimErr, dispatchErr, stageErr error
	stageCalls                      int
}

func (s *invocationStore) ClaimAttempt(_ context.Context, id, nonce string) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimErr != nil {
		return Attempt{}, s.claimErr
	}
	if s.attempt.ClaimID != "" || s.attempt.State != "reserved" {
		return Attempt{}, ErrClaimOwned
	}
	s.attempt.ClaimID = nonce
	s.attempt.State = "dispatching"
	return s.attempt, nil
}
func (s *invocationStore) MarkDispatch(_ context.Context, id, nonce string) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dispatchErr != nil {
		return Attempt{}, s.dispatchErr
	}
	if s.attempt.ClaimID != nonce || s.attempt.DispatchedAt != nil {
		return Attempt{}, ErrClaimOwned
	}
	now := time.Now().UTC()
	s.attempt.DispatchedAt = &now
	return s.attempt, nil
}
func (s *invocationStore) Load(context.Context, Ref) (StoredItem, error) { return s.item, nil }
func (s *invocationStore) StageAttempt(ctx context.Context, id, nonce string, o AttemptObservation) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stageCalls++
	if err := ctx.Err(); err != nil {
		return Attempt{}, err
	}
	if s.stageErr != nil {
		return Attempt{}, s.stageErr
	}
	if nonce != s.attempt.ClaimID {
		return Attempt{}, ErrClaimOwned
	}
	s.attempt.Observation = &o
	return s.attempt, nil
}
func (s *invocationStore) MarkLocalStop(ctx context.Context, id, nonce string, at time.Time) (Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Attempt{}, err
	}
	if nonce != s.attempt.ClaimID {
		return Attempt{}, ErrClaimOwned
	}
	s.attempt.StoppedAt = &at
	return s.attempt, nil
}

type invocationArtifacts struct {
	mu               sync.Mutex
	values           map[string][]byte
	puts, reads      int
	lostAck, corrupt bool
}

func (a *invocationArtifacts) PutBatchResult(_ context.Context, scope, job, id string, raw []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.puts++
	if a.values == nil {
		a.values = map[string][]byte{}
	}
	a.values[scope+"/"+job+"/"+id] = append([]byte(nil), raw...)
	if a.lostAck {
		return errors.New("synthetic lost object acknowledgement")
	}
	return nil
}
func (a *invocationArtifacts) OpenBatchResult(_ context.Context, scope, job, id string) (io.ReadCloser, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reads++
	raw, ok := a.values[scope+"/"+job+"/"+id]
	if !ok {
		return nil, ErrNotFound
	}
	if a.corrupt {
		raw = []byte(`{"not":"the committed artifact"}`)
	}
	return io.NopCloser(bytes.NewReader(raw)), nil
}
func invocationFixture() (*Activities, *invocationStore, *invocationArtifacts) {
	now := time.Now().UTC()
	id := uuid.NewString()
	s := &invocationStore{attempt: Attempt{Reservation: Reservation{PermitID: id, PoolID: "pool", Generation: 2, Sequence: 1, Kind: "job", Ref: Ref{Scope: strings.Repeat("a", 64), JobID: uuid.NewString()}, GrantedAt: now, StartBy: now.Add(5 * time.Second), Deadline: now.Add(time.Minute), BudgetMS: 300000, PolicyRevision: 1}, Ordinal: 1, State: "reserved"}, item: StoredItem{Item: Item{CustomID: "one", Generation: Generation{ModelID: "gpt-6-luna", TimeoutMS: 300000}}, State: "active", Deadline: now.Add(time.Hour), ReadyAt: now}}
	artifacts := &invocationArtifacts{}
	a := &Activities{Store: s, Artifacts: artifacts, Encode: func(Generation, hardenllm.Result, time.Time) (json.RawMessage, error) {
		return json.RawMessage(`{"answer":"private-output-sentinel"}`), nil
	}}
	return a, s, artifacts
}
func TestInvocationClaimsOnceAndKeepsResponseOutOfReturn(t *testing.T) {
	t.Parallel()
	a, s, artifacts := invocationFixture()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	a.Execute = func(ctx context.Context, _ string, r hardenllm.Request) (hardenllm.Result, error) {
		calls.Add(1)
		if r.CacheMode != hardenllm.CacheModeOff || r.RecoveryPolicy.MaxAttempts != 1 || r.RecoveryPolicy.JSONRepair != nil || r.RecoveryPolicy.Rerun != nil {
			t.Error("durable attempt enabled replay or cache")
		}
		deadline, ok := ctx.Deadline()
		if !ok || deadline.After(s.attempt.Deadline) {
			t.Error("provider context exceeded reservation")
		}
		close(entered)
		<-release
		return hardenllm.Result{}, nil
	}
	result := make(chan InvocationResult, 1)
	go func() {
		out, err := a.InvokeAttempt(context.Background(), s.attempt.PermitID)
		if err != nil {
			t.Error(err)
		}
		result <- out
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("reserved invocation did not enter provider")
	}
	loser, err := a.InvokeAttempt(context.Background(), s.attempt.PermitID)
	if err != nil || loser.Disposition != "claim_owned_elsewhere" || loser.Observation != nil {
		t.Error("losing invocation manufactured terminal evidence", loser, err)
	}
	close(release)
	out := <-result
	if out.Observation == nil || out.Observation.Outcome.Kind != "completed" || !out.Observation.LocalStopped || calls.Load() != 1 || artifacts.puts != 1 {
		t.Fatal("claimed invocation missing durable result", out, calls.Load(), artifacts.puts)
	}
	raw, _ := json.Marshal(out)
	if bytes.Contains(raw, []byte("private-output-sentinel")) {
		t.Fatal("provider output entered Temporal activity return")
	}
	if s.attempt.StoppedAt == nil || s.attempt.Observation == nil {
		t.Fatal("owner did not stage stop/outcome before returning")
	}
}
func TestInvocationUnknownClaimCannotExecute(t *testing.T) {
	t.Parallel()
	a, s, _ := invocationFixture()
	s.claimErr = ErrClaimUnconfirmed
	calls := 0
	a.Execute = func(context.Context, string, hardenllm.Request) (hardenllm.Result, error) {
		calls++
		return hardenllm.Result{}, nil
	}
	out, err := a.InvokeAttempt(context.Background(), s.attempt.PermitID)
	if err != nil || out.Disposition != "claim_unconfirmed" || out.Observation != nil || calls != 0 || s.stageCalls != 0 {
		t.Fatal("uncertain claim authorized execution or invented an outcome", out, err, calls)
	}
}
func TestInvocationRecoversLostArtifactAckWithoutInferenceRetry(t *testing.T) {
	t.Parallel()
	for _, corrupt := range []bool{false, true} {
		a, s, artifacts := invocationFixture()
		artifacts.lostAck = true
		artifacts.corrupt = corrupt
		calls := 0
		a.Execute = func(context.Context, string, hardenllm.Request) (hardenllm.Result, error) {
			calls++
			return hardenllm.Result{}, nil
		}
		out, err := a.InvokeAttempt(context.Background(), s.attempt.PermitID)
		want := "completed"
		if corrupt {
			want = "unconfirmed"
		}
		if err != nil || out.Observation == nil || out.Observation.Outcome.Kind != want || calls != 1 || artifacts.puts != 1 || artifacts.reads != 1 {
			t.Fatal("artifact acknowledgement recovery replayed or invented evidence", out, err, calls, artifacts.puts, artifacts.reads)
		}
	}
}
func TestInvocationReturnsSameObservationWhenStageFails(t *testing.T) {
	t.Parallel()
	a, s, _ := invocationFixture()
	s.stageErr = ErrUnavailable
	calls := 0
	a.Execute = func(context.Context, string, hardenllm.Request) (hardenllm.Result, error) {
		calls++
		return hardenllm.Result{}, nil
	}
	out, err := a.InvokeAttempt(context.Background(), s.attempt.PermitID)
	if err != nil || out.Observation == nil || out.Observation.Outcome.Kind != "completed" || calls != 1 || s.stageCalls != 1 {
		t.Fatal("metadata outage lost bounded return or retried inference", out, err, calls, s.stageCalls)
	}
}

func TestInvocationNeverSentAndPanicKeepDifferentLocalStopEvidence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		setup         func(*invocationStore)
		panicProvider bool
		want          string
		wantCalls     int
		wantStopped   bool
	}{
		{name: "expired_reservation", setup: func(s *invocationStore) { s.attempt.StartBy = time.Now().Add(-time.Second) }, want: "never_dispatched", wantStopped: true},
		{name: "dispatch_not_confirmed", setup: func(s *invocationStore) { s.dispatchErr = ErrUnavailable }, want: "never_dispatched", wantStopped: true},
		{name: "provider_panicked", setup: func(*invocationStore) {}, panicProvider: true, want: "unconfirmed", wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, s, _ := invocationFixture()
			tc.setup(s)
			calls := 0
			a.Execute = func(context.Context, string, hardenllm.Request) (hardenllm.Result, error) {
				calls++
				if tc.panicProvider {
					panic("private-panic-sentinel")
				}
				return hardenllm.Result{}, nil
			}
			out, err := a.InvokeAttempt(context.Background(), s.attempt.PermitID)
			if err != nil || out.Observation == nil || out.Observation.Outcome.Kind != tc.want || out.Observation.LocalStopped != tc.wantStopped || calls != tc.wantCalls || (s.attempt.StoppedAt != nil) != tc.wantStopped {
				t.Fatal("invocation invented stop or execution evidence", out, err, calls)
			}
			encoded, _ := json.Marshal(out)
			if bytes.Contains(encoded, []byte("private-panic-sentinel")) {
				t.Fatal("panic content entered durable metadata")
			}
		})
	}
}
func TestInvocationArtifactRejectsDifferentIdentityAndLateOutcome(t *testing.T) {
	t.Parallel()
	a, s, artifacts := invocationFixture()
	a.Execute = func(context.Context, string, hardenllm.Request) (hardenllm.Result, error) {
		return hardenllm.Result{}, nil
	}
	out, err := a.InvokeAttempt(context.Background(), s.attempt.PermitID)
	if err != nil || out.Observation == nil {
		t.Fatal(err)
	}
	retained := s.attempt
	original := artifacts.values[retained.Ref.Scope+"/"+retained.Ref.JobID+"/"+out.Observation.Outcome.ArtifactID]
	for _, tc := range []struct {
		name   string
		mutate func(*attemptArtifact)
	}{
		{"different_claim", func(a *attemptArtifact) { a.ClaimID = uuid.NewString() }},
		{"different_permit", func(a *attemptArtifact) { a.PermitID = uuid.NewString() }},
		{"different_pool", func(a *attemptArtifact) { a.PoolID = "other" }},
		{"different_scope", func(a *attemptArtifact) { a.Ref.Scope = strings.Repeat("b", 64) }},
		{"different_item", func(a *attemptArtifact) { a.Ref.Index++ }},
		{"late_result", func(a *attemptArtifact) {
			late := retained.Deadline.Add(time.Microsecond)
			a.Observation.Outcome.FinishedAt = &late
		}},
		{"missing_stop", func(a *attemptArtifact) { a.Observation.LocalStopped = false }},
		{"unknown_format", func(a *attemptArtifact) { a.Version = 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var altered attemptArtifact
			if err := json.Unmarshal(original, &altered); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&altered)
			raw, _ := json.Marshal(altered)
			if _, err := decodeAttemptArtifact(raw, retained); !errors.Is(err, ErrSettlementConflict) {
				t.Fatal("accepted unrelated or late result artifact", err)
			}
		})
	}
	if _, err := decodeAttemptArtifact(append(append([]byte(nil), original...), []byte(` {}`)...), retained); err == nil {
		t.Fatal("accepted trailing artifact document")
	}
}

func TestInvocationBoundsDiagnosticMetadata(t *testing.T) {
	t.Parallel()
	for _, id := range []string{strings.Repeat("x", 1<<20), "unsafe\ncorrelation"} {
		var out Outcome
		copyAttemptDiagnostics(&out, hardenllm.Result{Attempts: []hardenllm.Attempt{{Stream: &hardenllm.StreamDiagnostics{CorrelationID: id}}}})
		raw, _ := json.Marshal(out)
		if out.ProviderRequestID != "" || len(raw) > 1000 {
			t.Fatal("untrusted correlation entered bounded durable observation")
		}
	}
	var out Outcome
	copyAttemptDiagnostics(&out, hardenllm.Result{Attempts: []hardenllm.Attempt{{Stream: &hardenllm.StreamDiagnostics{CorrelationID: "bounded-request-123"}}}})
	if out.ProviderRequestID != "bounded-request-123" {
		t.Fatal("safe bounded correlation was lost")
	}
}
