package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-501

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	hardenllm "github.com/prls-co/harden-llm"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

type starterStore struct {
	ExecutionStore
	reserve func(context.Context, Reservation) (Attempt, error)
}

func (s starterStore) ReserveAttempt(ctx context.Context, r Reservation) (Attempt, error) {
	return s.reserve(ctx, r)
}

type starterTemporal struct {
	client.Client
	start  func(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error)
	signal func(context.Context, string, string, string, interface{}) error
}

func (c starterTemporal) ExecuteWorkflow(ctx context.Context, opts client.StartWorkflowOptions, w interface{}, args ...interface{}) (client.WorkflowRun, error) {
	return c.start(ctx, opts, w, args...)
}
func (c starterTemporal) SignalWorkflow(ctx context.Context, id, run, signal string, arg interface{}) error {
	return c.signal(ctx, id, run, signal, arg)
}
func TestAttemptStarterParallelBoundedAndSameIdentity(t *testing.T) {
	t.Parallel()
	var reserveCalls, starts atomic.Int32
	reservations := make([]Reservation, 16)
	for i := range reservations {
		id := uuid.NewString()
		reservations[i] = Reservation{PermitID: id, PoolID: "pool", WorkflowID: "attempt/" + id, Sequence: int64(i + 1)}
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	providerQueue := "control/provider"
	a := &Activities{TaskQueue: "control", PoolID: "pool", Store: starterStore{reserve: func(ctx context.Context, r Reservation) (Attempt, error) {
		reserveCalls.Add(1)
		return Attempt{Reservation: r}, nil
	}}}
	a.Execute = func(context.Context, string, hardenllm.Request) (hardenllm.Result, error) {
		t.Error("starter executed inference")
		return hardenllm.Result{}, nil
	}
	a.Temporal = starterTemporal{start: func(ctx context.Context, opts client.StartWorkflowOptions, w interface{}, args ...interface{}) (client.WorkflowRun, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 15*time.Second {
			t.Error("start RPC does not share bounded activity budget")
		}
		input := args[0].(AttemptInput)
		if input.ProviderTaskQueue != providerQueue || opts.TaskQueue != "control" || opts.WorkflowIDReusePolicy != enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE || opts.ID != input.Reservation.WorkflowID || w != AttemptWorkflowName {
			t.Error("attempt start changed identity or queue", opts, input)
		}
		if starts.Add(1) == 16 {
			close(entered)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if input.Reservation.Sequence == 1 {
			return nil, &serviceerror.WorkflowExecutionAlreadyStarted{}
		}
		if input.Reservation.Sequence == 2 {
			return nil, errors.New("synthetic lost start acknowledgement")
		}
		return nil, nil
	}}
	result := make(chan []AttemptStartResult, 1)
	go func() {
		out, err := a.StartAttempts(context.Background(), reservations)
		if err != nil {
			t.Error(err)
		}
		result <- out
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		once.Do(func() { close(release) })
		t.Fatal("starter serialized metadata RPCs or did not start all 16")
	}
	once.Do(func() { close(release) })
	out := <-result
	if len(out) != 16 || reserveCalls.Load() != 16 || starts.Load() != 16 {
		t.Fatal("starter lost intents", len(out), reserveCalls.Load(), starts.Load())
	}
	for i, r := range out {
		if r.PermitID != reservations[i].PermitID || r.Started != (i != 1) {
			t.Fatal("starter replaced identity or acknowledged unknown start", i, r)
		}
	}
}
func TestAttemptStarterRejectsOversizeAndDoesNotStartUnreserved(t *testing.T) {
	t.Parallel()
	a := &Activities{TaskQueue: "control", PoolID: "pool", Store: starterStore{reserve: func(context.Context, Reservation) (Attempt, error) { return Attempt{}, ErrUnavailable }}, Temporal: starterTemporal{start: func(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error) {
		t.Error("unconfirmed reservation started a workflow")
		return nil, nil
	}}}
	if _, err := a.StartAttempts(context.Background(), make([]Reservation, 17)); err == nil {
		t.Fatal("unbounded starter batch accepted")
	}
	id := uuid.NewString()
	out, err := a.StartAttempts(context.Background(), []Reservation{{PermitID: id, PoolID: "pool"}})
	if err != nil || len(out) != 1 || out[0].PermitID != id || out[0].Started {
		t.Fatal("unreserved intent acknowledged", out, err)
	}
}
func TestAttemptSettlementNoticeUsesStablePoolAndRevision(t *testing.T) {
	t.Parallel()
	calls := 0
	a := &Activities{Temporal: starterTemporal{signal: func(_ context.Context, id, run, name string, arg interface{}) error {
		calls++
		n := arg.(SettlementNotice)
		if id != "hllm-cpa-pool-v1/pool" || run != "" || name != settlementSignal || n.PermitID != "permit" || n.Revision != 2 {
			t.Error("notification bound to old run or carried untrusted result", id, run, name, n)
		}
		return nil
	}}}
	if err := a.NotifySettlement(context.Background(), Attempt{Reservation: Reservation{PoolID: "pool", PermitID: "permit"}, State: "settled", SettlementRevision: 2}); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
}
