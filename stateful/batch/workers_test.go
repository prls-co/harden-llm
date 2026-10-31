package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-506

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type ownedWorker struct {
	mu sync.Mutex
	worker.Worker
	started, stopped      int
	startError            error
	onStart               func()
	activities, workflows []string
}

func (w *ownedWorker) Start() error {
	w.started++
	if w.onStart != nil {
		w.onStart()
	}
	return w.startError
}
func (w *ownedWorker) Stop() { w.mu.Lock(); defer w.mu.Unlock(); w.stopped++ }
func (w *ownedWorker) RegisterWorkflowWithOptions(_ interface{}, options workflow.RegisterOptions) {
	w.workflows = append(w.workflows, options.Name)
}
func (w *ownedWorker) RegisterActivityWithOptions(_ interface{}, options activity.RegisterOptions) {
	w.activities = append(w.activities, options.Name)
}
func TestWorkersSeparateProviderQueueAndPreserveLegacyRegistrations(t *testing.T) {
	t.Parallel()
	queues := []string{}
	options := []worker.Options{}
	created := []*ownedWorker{}
	factory := func(_ client.Client, queue string, opt worker.Options) worker.Worker {
		w := &ownedWorker{}
		queues = append(queues, queue)
		options = append(options, opt)
		created = append(created, w)
		return w
	}
	fakeClient := starterTemporal{}
	a := &Activities{Store: &PostgresStore{}, Artifacts: &invocationArtifacts{}, PoolID: "pool", Policy: DefaultPolicy(), Execute: func(context.Context, string, hardenllm.Request) (hardenllm.Result, error) {
		return hardenllm.Result{}, nil
	}, Encode: func(Generation, hardenllm.Result, time.Time) (json.RawMessage, error) { return nil, nil }}
	combined, err := newWorkers(fakeClient, "control", a, factory)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(queues, []string{"control", "control/provider"}) || !options[1].DisableWorkflowWorker || options[1].MaxConcurrentActivityExecutionSize != 128 {
		t.Fatal("provider queue/concurrency not independent", queues, options[1])
	}
	if !reflect.DeepEqual(created[1].activities, []string{"InvokeAttempt", "ProviderReady"}) || len(created[0].workflows) != 4 {
		t.Fatal("inference escaped provider queue or compatibility workflow missing", created)
	}
	for _, name := range created[0].activities {
		if name == "InvokeAttempt" || name == "ProviderReady" {
			t.Fatal("V2 provider activity registered on metadata queue")
		}
	}
	if err := combined.Start(); err != nil {
		t.Fatal(err)
	}
	combined.Stop()
	combined.Stop()
	if created[0].started != 1 || created[1].started != 1 || created[0].stopped != 1 || created[1].stopped != 1 {
		t.Fatal("worker lifecycle leaked or stopped twice")
	}
}
func TestWorkersRollBackProviderIfControlStartFails(t *testing.T) {
	t.Parallel()
	provider, control := &ownedWorker{}, &ownedWorker{startError: errors.New("synthetic control start failure")}
	combined := &pairedWorker{Worker: control, provider: provider, stopped: make(chan struct{})}
	if err := combined.Start(); err == nil || provider.started != 1 || provider.stopped != 1 || control.stopped != 0 {
		t.Fatal("failed startup leaked provider worker", err)
	}
}

func TestWorkersShutdownCancelsTransportBeforeRecordingLocalStop(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	stopWorker := make(chan struct{})
	env.SetWorkerStopChannel(stopWorker)
	a, s, _ := invocationFixture()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var returned atomic.Bool
	a.Execute = func(ctx context.Context, _ string, _ hardenllm.Request) (hardenllm.Result, error) {
		close(entered)
		select {
		case <-ctx.Done():
		case <-release:
		}
		returned.Store(true)
		return hardenllm.Result{}, context.Canceled
	}
	env.RegisterActivity(a)
	done := make(chan InvocationResult, 1)
	go func() {
		value, err := env.ExecuteActivity(a.InvokeAttempt, s.attempt.PermitID)
		if err != nil {
			t.Error(err)
			done <- InvocationResult{}
			return
		}
		var out InvocationResult
		if err := value.Get(&out); err != nil {
			t.Error(err)
		}
		done <- out
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("provider activity did not enter transport")
	}
	close(stopWorker)
	var out InvocationResult
	select {
	case out = <-done:
	case <-time.After(time.Second):
		t.Error("worker shutdown did not cancel provider context")
		releaseOnce.Do(func() { close(release) })
		select {
		case out = <-done:
		case <-time.After(time.Second):
			t.Fatal("owned activity did not terminate after fixture release")
		}
	}
	if !returned.Load() || out.Observation == nil || !out.Observation.LocalStopped || s.attempt.StoppedAt == nil || out.Observation.Outcome.Kind != "unconfirmed" {
		t.Fatal("shutdown fabricated stop before provider return", out)
	}
}

func TestWorkersFatalErrorStopsBothAndRunReturnsFailure(t *testing.T) {
	t.Parallel()
	var callbacks []func(error)
	provider, control := &ownedWorker{}, &ownedWorker{}
	creations := 0
	factory := func(_ client.Client, _ string, opt worker.Options) worker.Worker {
		callbacks = append(callbacks, opt.OnFatalError)
		creations++
		if creations == 1 {
			return control
		}
		return provider
	}
	a, _, _ := invocationFixture()
	a.PoolID = "pool"
	a.Policy = DefaultPolicy()
	a.Execute = func(context.Context, string, hardenllm.Request) (hardenllm.Result, error) {
		return hardenllm.Result{}, nil
	}
	combined, err := newWorkers(starterTemporal{}, "control", a, factory)
	if err != nil {
		t.Fatal(err)
	}
	if len(callbacks) != 2 || callbacks[0] == nil || callbacks[1] == nil {
		t.Fatal("fatal worker errors are not owned by the combined lifecycle")
	}
	started := make(chan struct{})
	// The callback runs only after both Start calls complete; no polling/sleep.
	control.onStart = func() { close(started) }
	done := make(chan error, 1)
	go func() { done <- combined.Run(nil) }()
	<-started
	fatal := errors.New("synthetic fatal provider poller failure")
	callbacks[1](fatal)
	select {
	case err := <-done:
		if !errors.Is(err, fatal) {
			t.Fatal("Run lost fatal worker failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("fatal worker failure did not terminate Run")
	}
	if provider.stopped != 1 || control.stopped != 1 {
		t.Fatal("fatal shutdown leaked owned worker", provider.stopped, control.stopped)
	}
}

func TestWorkersFatalDuringFailedStartupStopsProviderOnce(t *testing.T) {
	t.Parallel()
	var callback func(error)
	provider, control := &ownedWorker{}, &ownedWorker{startError: errors.New("synthetic startup failure")}
	calls := 0
	factory := func(_ client.Client, _ string, opt worker.Options) worker.Worker {
		calls++
		if calls == 1 {
			callback = opt.OnFatalError
			return control
		}
		return provider
	}
	a, _, _ := invocationFixture()
	a.PoolID = "pool"
	a.Policy = DefaultPolicy()
	a.Execute = func(context.Context, string, hardenllm.Request) (hardenllm.Result, error) {
		return hardenllm.Result{}, nil
	}
	combined, err := newWorkers(starterTemporal{}, "control", a, factory)
	if err != nil {
		t.Fatal(err)
	}
	control.onStart = func() { callback(errors.New("synthetic fatal during startup")) }
	if err := combined.Start(); err == nil {
		t.Fatal("startup failure lost")
	}
	select {
	case <-combined.(*pairedWorker).stopped:
	case <-time.After(time.Second):
		t.Fatal("fatal failed startup did not terminate lifecycle")
	}
	if provider.stopped != 1 || control.stopped != 0 {
		t.Fatal("fatal failed startup stopped a worker twice or stopped an unstarted worker", provider.stopped, control.stopped)
	}
}
