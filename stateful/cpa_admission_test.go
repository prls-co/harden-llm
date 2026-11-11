package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-517

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/prls-co/harden-llm/internal/retry"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful/batch"
)

type admissionSignals struct{ wake func() error }

func (s admissionSignals) Wake(context.Context, string) error {
	if s.wake != nil {
		return s.wake()
	}
	return nil
}
func (admissionSignals) Changed(context.Context, string, batch.SettlementNotice) error { return nil }

type admissionUnavailableStore struct {
	batch.RequestQueueStore
	queued atomic.Int32
}

func (s *admissionUnavailableStore) PoolGeneration(context.Context, string) (batch.PoolGeneration, error) {
	return batch.PoolGeneration{PoolID: "pool", Generation: 2, Mode: "active"}, nil
}
func (s *admissionUnavailableStore) EnqueueRequest(context.Context, batch.RequestWaiter) (batch.RequestWaiter, error) {
	s.queued.Add(1)
	return batch.RequestWaiter{}, batch.ErrUnavailable
}
func (s *admissionUnavailableStore) CancelRequest(context.Context, string, string, string) error {
	return nil
}

type admissionNative struct{ calls atomic.Int32 }

func (s *admissionNative) Prepare(context.Context, hardenllm.ProviderConnection, hardenllm.ProviderCredential, hardenllm.ProviderCall) (hardenllm.PreparedOperation, error) {
	return hardenllm.PreparedOperation{}, nil
}
func (s *admissionNative) Execute(context.Context, hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
	s.calls.Add(1)
	return hardenllm.ProviderResult{Output: "OK", AssistantText: "OK"}, nil
}

func TestCPAAdmissionUnavailableCannotBypassIntoNative(t *testing.T) {
	t.Parallel()
	store := &admissionUnavailableStore{}
	s, err := NewService(Config{Admission: &CPAAdmission{Store: store, Signals: admissionSignals{}, PoolID: "pool", Endpoint: "https://fixture.invalid/v1"}, Client: ClientOptions{Connections: []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: "https://fixture.invalid/v1", APIKey: "fixture-private-key"}}, DefaultConnection: "cpa"}})
	if err != nil {
		t.Fatal(err)
	}
	native := &admissionNative{}
	s.effects.Executor = native
	c, err := s.ClientForKey(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = c.Call(ctx, hardenllm.Request{ModelID: "chosen-model", CallType: hardenllm.CallTypeText, Messages: []hardenllm.Message{{Role: "user", Content: json.RawMessage(`"private prompt"`)}}, CacheMode: hardenllm.CacheModeOff, RecoveryPolicy: hardenllm.RecoveryPolicy{MaxAttempts: 1, RetryOn: []hardenllm.RecoveryCategory{}, Backoff: hardenllm.RecoveryBackoff{}}})
	if err == nil || store.queued.Load() != 1 || native.calls.Load() != 0 {
		t.Fatal("ordinary CPA execution bypassed admission", store.queued.Load(), native.calls.Load(), err)
	}
}

type admissionMemoryStore struct {
	onEnqueue func(context.Context)
	batch.RequestQueueStore
	mu             sync.Mutex
	waiters        map[string]batch.RequestWaiter
	attempts       map[string]batch.Attempt
	queued         []batch.RequestWaiter
	observations   []batch.AttemptObservation
	neverGrant     bool
	fail           string
	onClaim        func()
	claims         int
	readFailures   int
	reads          int
	readFailureErr error
}

func newAdmissionMemoryStore() *admissionMemoryStore {
	return &admissionMemoryStore{waiters: map[string]batch.RequestWaiter{}, attempts: map[string]batch.Attempt{}}
}
func (s *admissionMemoryStore) PoolGeneration(context.Context, string) (batch.PoolGeneration, error) {
	return batch.PoolGeneration{PoolID: "pool", Generation: 2, Mode: "active"}, nil
}
func (s *admissionMemoryStore) EnqueueRequest(ctx context.Context, w batch.RequestWaiter) (batch.RequestWaiter, error) {
	if s.onEnqueue != nil {
		s.onEnqueue(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w.LiveUntil = time.Now().Add(10 * time.Second)
	w.WaitingReason = "pool_capacity"
	if !s.neverGrant {
		w.PermitID = uuid.NewString()
		s.attempts[w.PermitID] = batch.Attempt{Reservation: batch.Reservation{PermitID: w.PermitID, PoolID: w.PoolID, Generation: w.Generation, Kind: "ordinary", RequestID: w.RequestID, Ref: batch.Ref{Scope: w.Scope}, StartBy: time.Now().Add(5 * time.Second), Deadline: w.Deadline, BudgetMS: w.BudgetMS}, Ordinal: int64(w.Ordinal), State: "reserved"}
	}
	s.waiters[w.RequestID] = w
	s.queued = append(s.queued, w)
	return w, nil
}
func (s *admissionMemoryStore) ReadRequest(_ context.Context, scope, id, owner string, _ bool) (batch.RequestWaiter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.readFailures > 0 {
		s.readFailures--
		if s.readFailureErr != nil {
			return batch.RequestWaiter{}, s.readFailureErr
		}
		return batch.RequestWaiter{}, batch.ErrUnavailable
	}
	w := s.waiters[id]
	if w.Scope != scope || w.OwnerID != owner {
		return batch.RequestWaiter{}, batch.ErrNotFound
	}
	return w, nil
}

func TestCPAAdmissionReadRecoveryPreservesCancellationAndOwnershipFailure(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			store := newAdmissionMemoryStore()
			store.readFailures = 99
			if missing {
				store.readFailureErr = batch.ErrNotFound
			}
			native := &admissionNative{}
			client := admissionTestClient(t, store, native)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			_, err := client.Call(ctx, admissionTestRequest())
			if err == nil || native.calls.Load() != 0 || store.claims != 0 || store.reads > 1 || len(store.queued) > 1 {
				t.Fatal("metadata recovery ignored cancellation/ownership failure or repeated admission", err)
			}
		})
	}
}

// A temporary read failure must recover the same waiter, not admit another
// dispatch. Persistent failure remains bounded and never enters the provider.
func TestCPAAdmissionRecoversSameWaiterAfterUnavailableRead(t *testing.T) {
	for _, failures := range []int{1, 2, 99} {
		t.Run(fmt.Sprint(failures), func(t *testing.T) {
			store := newAdmissionMemoryStore()
			store.readFailures = failures
			native := &admissionNative{}
			client := admissionTestClient(t, store, native)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := client.Call(ctx, admissionTestRequest())
			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.queued) != 1 || store.reads != min(failures+1, 3) {
				t.Fatal("metadata recovery changed admission identity or exceeded its read bound", len(store.queued), store.reads)
			}
			if failures < 3 {
				if err != nil || native.calls.Load() != 1 || store.claims != 1 || len(store.observations) != 1 {
					t.Fatal("temporary metadata error aborted or repeated inference", err, native.calls.Load(), store.claims)
				}
			} else if err == nil || native.calls.Load() != 0 || store.claims != 0 || store.waiters[store.queued[0].RequestID].ClosedAt == nil {
				t.Fatal("persistent metadata error bypassed admission or retained a live waiter", err)
			}
		})
	}
}
func (s *admissionMemoryStore) CancelRequest(_ context.Context, scope, id, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.waiters[id]
	if w.Scope != scope || w.OwnerID != owner {
		return batch.ErrNotFound
	}
	now := time.Now()
	w.ClosedAt = &now
	s.waiters[id] = w
	return nil
}
func (s *admissionMemoryStore) ClaimAttempt(_ context.Context, id, owner string) (batch.Attempt, error) {
	s.mu.Lock()
	a := s.attempts[id]
	s.claims++
	a.ClaimID = owner
	a.State = "dispatching"
	now := time.Now()
	a.ClaimedAt = &now
	a.GenerationDeadline = &a.Deadline
	s.attempts[id] = a
	hook := s.onClaim
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	if s.fail == "claim" {
		return batch.Attempt{}, batch.ErrClaimUnconfirmed
	}
	return a, nil
}
func (s *admissionMemoryStore) MarkDispatch(ctx context.Context, id, owner string) (batch.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail == "dispatch" {
		return batch.Attempt{}, batch.ErrUnavailable
	}
	a := s.attempts[id]
	if ctx.Err() != nil {
		return batch.Attempt{}, ctx.Err()
	}
	if a.ClaimID != owner {
		return batch.Attempt{}, batch.ErrClaimOwned
	}
	now := time.Now()
	a.DispatchedAt = &now
	s.attempts[id] = a
	return a, nil
}
func (s *admissionMemoryStore) MarkLocalStop(_ context.Context, id, owner string, at time.Time) (batch.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.attempts[id]
	if a.ClaimID != owner {
		return batch.Attempt{}, batch.ErrClaimOwned
	}
	a.StoppedAt = &at
	s.attempts[id] = a
	return a, nil
}
func (s *admissionMemoryStore) StageAttempt(_ context.Context, id, owner string, o batch.AttemptObservation) (batch.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail == "settlement" {
		return batch.Attempt{}, batch.ErrUnavailable
	}
	a := s.attempts[id]
	if a.ClaimID != owner {
		return batch.Attempt{}, batch.ErrClaimOwned
	}
	a.Observation = &o
	s.attempts[id] = a
	return a, nil
}
func (s *admissionMemoryStore) SettleAttempt(_ context.Context, id, owner string, o batch.AttemptObservation) (batch.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail == "settlement" {
		return batch.Attempt{}, batch.ErrUnavailable
	}
	a := s.attempts[id]
	if a.ClaimID != owner {
		return batch.Attempt{}, batch.ErrClaimOwned
	}
	a.Observation = &o
	a.State = "settled"
	a.SettlementRevision = 1
	s.attempts[id] = a
	s.observations = append(s.observations, o)
	return a, nil
}

type admissionScriptNative struct {
	prepare func(hardenllm.ProviderConnection, hardenllm.ProviderCredential, hardenllm.ProviderCall)
	execute func(context.Context, hardenllm.PreparedOperation) (hardenllm.ProviderResult, error)
	calls   atomic.Int32
}

func (s *admissionScriptNative) Prepare(_ context.Context, c hardenllm.ProviderConnection, k hardenllm.ProviderCredential, call hardenllm.ProviderCall) (hardenllm.PreparedOperation, error) {
	if s.prepare != nil {
		s.prepare(c, k, call)
	}
	return hardenllm.PreparedOperation{Opaque: call}, nil
}
func (s *admissionScriptNative) Execute(ctx context.Context, p hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
	s.calls.Add(1)
	return s.execute(ctx, p)
}
func admissionTestClient(t *testing.T, store *admissionMemoryStore, native hardenllm.Executor, override ...batch.RequestPoolSignals) *hardenllm.Client {
	t.Helper()
	signals := admissionSignals{}
	if store.fail == "signal" {
		signals.wake = func() error { return batch.ErrUnavailable }
	}
	var port batch.RequestPoolSignals = signals
	if len(override) != 0 {
		port = override[0]
	}
	s, err := NewService(Config{Admission: &CPAAdmission{Store: store, Signals: port, PoolID: "pool", Endpoint: "https://fixture.invalid/v1"}, Client: ClientOptions{Connections: []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: "https://fixture.invalid/v1", APIKey: "fixture-private-key"}}, DefaultConnection: "cpa"}})
	if err != nil {
		t.Fatal(err)
	}
	s.effects.Executor = trackingExecutor{next: native}
	c, err := s.ClientForKey(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-521
func TestCPAAdmissionClaimHintCannotBlockConfirmedNativeDispatch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{})
	native := &admissionScriptNative{execute: func(context.Context, hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
		close(entered)
		return hardenllm.ProviderResult{Output: "OK", AssistantText: "OK"}, nil
	}}
	signals := admissionSignalStub{wake: func(context.Context, string) error { return nil }, changed: func(ctx context.Context, _ string, n batch.SettlementNotice) error {
		if n.Revision != 0 {
			return nil
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	c := admissionTestClient(t, newAdmissionMemoryStore(), native, signals)
	done := make(chan error, 1)
	go func() { _, err := c.Call(ctx, admissionTestRequest()); done <- err }()
	select {
	case <-entered:
	case <-time.After(250 * time.Millisecond):
		t.Error("best-effort claim hint delayed confirmed native dispatch")
	}
	cancel()
	<-done // The request joins its bounded hint; no background metadata owner leaks.
}
func admissionTestRequest() hardenllm.Request {
	return hardenllm.Request{ModelID: "selected-model", CallType: hardenllm.CallTypeText, Messages: []hardenllm.Message{{Role: "user", Content: json.RawMessage(`"private prompt"`)}}, ProviderOptions: map[string]any{"temperature": 0.2}, CacheMode: hardenllm.CacheModeOff, RecoveryPolicy: hardenllm.RecoveryPolicy{MaxAttempts: 1, RetryOn: []hardenllm.RecoveryCategory{}, Backoff: hardenllm.RecoveryBackoff{}}}
}

func TestCPAAdmissionRetryAndRepairAcquireDistinctDispatchPermits(t *testing.T) {
	t.Parallel()
	for _, repair := range []bool{false, true} {
		t.Run(fmt.Sprint(repair), func(t *testing.T) {
			store := newAdmissionMemoryStore()
			native := &admissionScriptNative{}
			native.prepare = func(c hardenllm.ProviderConnection, k hardenllm.ProviderCredential, call hardenllm.ProviderCall) {
				if call.ModelID != "selected-model" || k.APIKey != "fixture-private-key" || c.BaseURL != "https://fixture.invalid/v1" {
					t.Error("caller selection changed", call.ModelID, c.BaseURL)
				}
			}
			native.execute = func(_ context.Context, p hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
				if _, ok := p.Opaque.(hardenllm.ProviderCall); !ok {
					t.Error("opaque native request not preserved")
				}
				if native.calls.Load() == 1 {
					if repair {
						return hardenllm.ProviderResult{Output: "bad-json", AssistantText: "bad-json", ProviderDispatched: true}, nil
					}
					return hardenllm.ProviderResult{ProviderDispatched: true}, &hardenllm.ProviderError{Status: 429, Category: retry.CategoryRateLimit, Code: "rate_limit_exceeded"}
				}
				if repair {
					return hardenllm.ProviderResult{Output: map[string]any{"ok": true}, AssistantText: `{"ok":true}`, ProviderDispatched: true}, nil
				}
				return hardenllm.ProviderResult{Output: "OK", AssistantText: "OK", ProviderDispatched: true}, nil
			}
			c := admissionTestClient(t, store, native)
			req := admissionTestRequest()
			req.RecoveryPolicy.MaxAttempts = 2
			if repair {
				req.CallType = hardenllm.CallTypeStructured
				req.Schema = json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)
				req.RecoveryPolicy.JSONRepair = &hardenllm.JSONRepairPlan{Initial: hardenllm.RecoveryTarget{Source: "generation"}}
			} else {
				req.RecoveryPolicy.RetryOn = []hardenllm.RecoveryCategory{retry.CategoryRateLimit}
			}
			_, err := c.Call(context.Background(), req)
			if err != nil || native.calls.Load() != 2 || len(store.queued) != 2 || len(store.observations) != 2 {
				t.Fatal("core dispatch did not acquire/settle its own permit", native.calls.Load(), len(store.queued), err)
			}
			first, second := store.queued[0], store.queued[1]
			if first.ExecutionID != second.ExecutionID || first.Ordinal != 1 || second.Ordinal != 2 || first.RequestID == second.RequestID || first.OwnerID == second.OwnerID || first.PermitID == second.PermitID || second.Scope != strings.Repeat("a", 64) || second.Deadline.After(first.Deadline) {
				t.Fatal("dispatch identity/scope/deadline was reset", first, second)
			}
			if !repair && (store.observations[0].Outcome.Kind != "rejected" || store.observations[0].Outcome.Admission != "throttled") {
				t.Fatal("explicit refusal lost shared-pool feedback", store.observations[0])
			}
		})
	}
}

func TestCPAAdmissionCanceledQueueHasNoProviderEntry(t *testing.T) {
	t.Parallel()
	store := newAdmissionMemoryStore()
	store.neverGrant = true
	native := &admissionNative{}
	now := time.Now()
	record, _ := NewTrackingRecord(uuid.NewString(), "/v1/responses", now, now.Add(time.Second))
	session := NewTrackingSession(record, time.Now)
	ctx, cancel := context.WithCancel(WithTrackingSession(context.Background(), session))
	defer cancel()
	// Wake follows positive enqueue acknowledgement. Cancel there so compiler
	// scheduling cannot turn the queued-cancellation case into pre-entry expiry.
	c := admissionTestClient(t, store, native, admissionSignals{wake: func() error {
		cancel()
		return nil
	}})
	_, err := c.Call(ctx, admissionTestRequest())
	if !errors.Is(err, context.Canceled) || native.calls.Load() != 0 || session.ProviderEntered() || len(store.queued) != 1 {
		t.Fatal("abandoned waiter entered native execution", err, native.calls.Load(), session.ProviderEntered())
	}
	row := store.waiters[store.queued[0].RequestID]
	if row.ClosedAt == nil || session.Snapshot(false).Stage == nil || *session.Snapshot(false).Stage != "admission_wait" {
		t.Fatal("queued caller was not canceled/observed", row, session.Snapshot(false))
	}
}

func TestCPAAdmissionExpiredCallerHasNoProviderEntry(t *testing.T) {
	t.Parallel()
	store := newAdmissionMemoryStore()
	store.neverGrant = true
	native := &admissionNative{}
	c := admissionTestClient(t, store, native)
	now := time.Now()
	record, recordErr := NewTrackingRecord(uuid.NewString(), "/v1/responses", now.Add(-time.Second), now.Add(-time.Nanosecond))
	if recordErr != nil {
		t.Fatal(recordErr)
	}
	session := NewTrackingSession(record, time.Now)
	ctx, cancel := context.WithDeadline(WithTrackingSession(context.Background(), session), now.Add(-time.Nanosecond))
	defer cancel()
	_, err := c.Call(ctx, admissionTestRequest())
	if !errors.Is(err, context.DeadlineExceeded) || native.calls.Load() != 0 || session.ProviderEntered() || len(store.queued) != 0 {
		t.Fatal("expired caller entered the queue or provider", err, native.calls.Load(), len(store.queued))
	}
}

func TestCPAAdmissionUncertainTransportCannotAutomaticallyRetry(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"dispatched-network", "unobserved-network", "partial-refusal"} {
		t.Run(variant, func(t *testing.T) {
			store := newAdmissionMemoryStore()
			native := &admissionScriptNative{execute: func(context.Context, hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
				r := hardenllm.ProviderResult{ProviderDispatched: variant != "unobserved-network"}
				p := &hardenllm.ProviderError{Category: retry.CategoryNetwork, Code: "NETWORK_ERROR"}
				if variant == "partial-refusal" {
					r.AssistantText = "private partial output"
					r.Stream.OutputBytes = 22
					p.Status = 429
					p.Category = retry.CategoryRateLimit
					p.RetryAfter = time.Second
				}
				return r, p
			}}
			c := admissionTestClient(t, store, native)
			req := admissionTestRequest()
			req.RecoveryPolicy.MaxAttempts = 3
			req.RecoveryPolicy.RetryOn = []hardenllm.RecoveryCategory{retry.CategoryNetwork, retry.CategoryRateLimit}
			_, err := c.Call(context.Background(), req)
			if err == nil || native.calls.Load() != 1 || len(store.observations) != 1 || store.observations[0].Outcome.Kind != "unconfirmed" || !store.observations[0].LocalStopped {
				t.Fatal("uncertain inference replayed or lost positive local stop", err, native.calls.Load(), store.observations)
			}
			raw, _ := json.Marshal(store.observations)
			if strings.Contains(string(raw), "private partial output") || strings.Contains(string(raw), "private prompt") {
				t.Fatal("ordinary metadata retained inference payload")
			}
		})
	}
}

func TestCPAAdmissionMetadataFaultsNeverRepeatNativeExecution(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"signal", "claim", "dispatch", "settlement", "cancel-after-claim", "panic"} {
		t.Run(fault, func(t *testing.T) {
			store := newAdmissionMemoryStore()
			store.fail = fault
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if fault == "cancel-after-claim" {
				store.onClaim = cancel
			}
			native := &admissionScriptNative{execute: func(context.Context, hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
				if fault == "panic" {
					panic("private panic payload")
				}
				return hardenllm.ProviderResult{Output: "OK", AssistantText: "OK", ProviderDispatched: true}, nil
			}}
			c := admissionTestClient(t, store, native)
			_, err := c.Call(ctx, admissionTestRequest())
			want := int32(0)
			if fault == "settlement" || fault == "panic" {
				want = 1
			}
			if native.calls.Load() != want || (fault == "settlement" && err != nil) || (fault != "settlement" && err == nil) {
				t.Fatal("metadata fault replayed/replaced inference", fault, native.calls.Load(), err)
			}
			if fault == "panic" && (len(store.observations) != 1 || store.observations[0].LocalStopped || store.observations[0].Outcome.Kind != "unconfirmed") {
				t.Fatal("panic invented transport stop", store.observations)
			}
		})
	}
}

func TestCPAAdmissionTrackingSeparatesWaitingAndNativeEntry(t *testing.T) {
	t.Parallel()
	now := time.Now()
	r, _ := NewTrackingRecord(uuid.NewString(), "/v1/responses", now, now.Add(time.Minute))
	session := NewTrackingSession(r, func() time.Time { return now })
	session.AdmissionWait("pool_capacity", nil)
	before := session.Snapshot(false)
	now = now.Add(time.Second)
	session.AdmissionWait("pool_capacity", nil)
	if after := session.Snapshot(false); after.Revision != before.Revision || session.ProviderEntered() || after.NextAttemptAt != nil || after.Observe(now).RetryAfterMS != nil {
		t.Fatal("polling invented progress, native entry or ETA", after)
	}
	native := &admissionNative{}
	ctx := WithTrackingSession(context.Background(), session)
	if _, err := (trackingExecutor{next: native}).Execute(ctx, hardenllm.PreparedOperation{}); err != nil {
		t.Fatal(err)
	}
	if after := session.Snapshot(false); !session.ProviderEntered() || after.Stage == nil || *after.Stage != "provider_attempt" || after.Reason != nil || native.calls.Load() != 1 {
		t.Fatal("actual dispatch not observed", after)
	}
}

func TestCPAAdmissionUnmanagedGenerationAndModelDiscoveryBypass(t *testing.T) {
	t.Parallel()
	store := &admissionUnavailableStore{}
	s, err := NewService(Config{Admission: &CPAAdmission{Store: store, Signals: admissionSignals{}, PoolID: "pool", Endpoint: "https://fixture.invalid/v1"}, Client: ClientOptions{Connections: []hardenllm.Connection{{ID: "other", Provider: "openai", Protocol: "responses", BaseURL: "https://other.invalid/v1", APIKey: "other-fixture-key"}}, DefaultConnection: "other"}})
	if err != nil {
		t.Fatal(err)
	}
	native := &admissionNative{}
	s.effects.Executor = trackingExecutor{next: native}
	s.effects.DiscoverModels = func(context.Context, hardenllm.Connection) ([]hardenllm.Model, error) {
		return []hardenllm.Model{{ID: "other-model"}}, nil
	}
	c, err := s.ClientForKey(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(context.Background(), admissionTestRequest()); err != nil || native.calls.Load() != 1 || store.queued.Load() != 0 {
		t.Fatal("unmanaged provider consumed CPA admission", err, native.calls.Load(), store.queued.Load())
	}
	if models, err := c.Models(context.Background(), ""); err != nil || len(models) != 1 || store.queued.Load() != 0 {
		t.Fatal("model discovery consumed admission", models, err)
	}
}

func TestCPAAdmissionOriginUsesConfiguredDomainAcrossAliases(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"https://fixture.invalid/v1", "https://FIXTURE.invalid:443/other", "https://fixture.invalid.:0443/v1"} {
		if got := admissionOrigin(input); got != "https://fixture.invalid" {
			t.Fatal("equivalent CPA domain bypassed admission", input, got)
		}
	}
	for _, input := range []string{"https://fixture.invalid:8443/v1", "http://fixture.invalid/v1"} {
		if admissionOrigin(input) == "https://fixture.invalid" {
			t.Fatal("different origin was conflated", input)
		}
	}
	for _, input := range []string{"https://key@fixture.invalid/v1", "https://fixture.invalid:99999/v1", "https://fixture.invalid/v1?key=private"} {
		if admissionOrigin(input) != "" {
			t.Fatal("invalid configured origin accepted")
		}
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-521
func TestCPAAdmissionBoundsMetadataWithoutReducingNativeCapacity(t *testing.T) {
	t.Parallel()
	store := newAdmissionMemoryStore()
	var active, peak atomic.Int32
	entered := make(chan struct{}, 12)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	store.onEnqueue = func(ctx context.Context) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	nativeEntered := make(chan struct{}, 12)
	nativeRelease := make(chan struct{})
	var releaseNative sync.Once
	defer releaseNative.Do(func() { close(nativeRelease) })
	native := &admissionScriptNative{execute: func(ctx context.Context, _ hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
		nativeEntered <- struct{}{}
		select {
		case <-nativeRelease:
		case <-ctx.Done():
			return hardenllm.ProviderResult{}, ctx.Err()
		}
		return hardenllm.ProviderResult{Output: "OK", AssistantText: "OK"}, nil
	}}
	c := admissionTestClient(t, store, native)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var group sync.WaitGroup
	defer func() {
		cancel()
		once.Do(func() { close(release) })
		releaseNative.Do(func() { close(nativeRelease) })
		group.Wait()
	}()
	launch := make(chan struct{})
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-launch
			_, err := c.Call(ctx, admissionTestRequest())
			if err != nil {
				t.Error(err)
			}
		}()
	}
	close(launch)
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("metadata did not fill its bounded slots")
		}
	}
	select {
	case <-entered:
		t.Error("metadata oversubscription reached the backing store")
	case <-time.After(50 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	for range 12 {
		select {
		case <-nativeEntered:
		case <-ctx.Done():
			t.Fatal("metadata bound leaked into native execution", ctx.Err())
		}
	}
	releaseNative.Do(func() { close(nativeRelease) })
	group.Wait()
	if peak.Load() > 2 || native.calls.Load() != 12 {
		t.Fatal("metadata limit failed or reduced inference demand", peak.Load(), native.calls.Load())
	}
}

func TestCPAAdmissionMetadataQueueReportsWaitAndHonorsDeadline(t *testing.T) {
	t.Parallel()
	s := &admissionMetadataStore{slots: make(chan struct{}, 1)}
	s.slots <- struct{}{}
	now := time.Now()
	record, _ := NewTrackingRecord(uuid.NewString(), "/v1/responses", now, now.Add(time.Second))
	session := NewTrackingSession(record, time.Now)
	ctx, cancel := context.WithTimeout(WithTrackingSession(context.Background(), session), 20*time.Millisecond)
	defer cancel()
	called := false
	err := s.run(ctx, func() error { called = true; return nil })
	state := session.Snapshot(false)
	if !errors.Is(err, context.DeadlineExceeded) || called || state.Status != "waiting" || state.Reason == nil || *state.Reason != "persistence_wait" || state.NextAttemptAt != nil || session.ProviderEntered() {
		t.Fatal("metadata queue hid waiting or extended the caller", state, called, err)
	}
}
