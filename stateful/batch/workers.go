package batch

import (
	"sync"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type pairedWorker struct {
	worker.Worker
	provider        worker.Worker
	stopOnce        sync.Once
	failureOnce     sync.Once
	stopped         chan struct{}
	failure         chan error
	lifecycle       sync.Mutex
	providerStarted bool
	controlStarted  bool
}

func (w *pairedWorker) Start() error {
	w.lifecycle.Lock()
	defer w.lifecycle.Unlock()
	select {
	case <-w.stopped:
		return ErrUnavailable
	default:
	}
	if w.providerStarted || w.controlStarted {
		return ErrUnavailable
	}
	if err := w.provider.Start(); err != nil {
		return err
	}
	w.providerStarted = true
	if err := w.Worker.Start(); err != nil {
		w.provider.Stop()
		w.providerStarted = false
		return err
	}
	w.controlStarted = true
	return nil
}
func (w *pairedWorker) Stop() {
	w.stopOnce.Do(func() {
		w.lifecycle.Lock()
		defer w.lifecycle.Unlock()
		if w.providerStarted {
			w.provider.Stop()
			w.providerStarted = false
		}
		if w.controlStarted {
			w.Worker.Stop()
			w.controlStarted = false
		}
		close(w.stopped)
	})
}
func (w *pairedWorker) Run(interrupt <-chan interface{}) error {
	if err := w.Start(); err != nil {
		return err
	}
	select {
	case <-interrupt:
	case err := <-w.failure:
		w.Stop()
		return err
	case <-w.stopped:
	}
	w.Stop()
	select {
	case err := <-w.failure:
		return err
	default:
	}
	return nil
}

// Errors lets an owner using Start (rather than Run) terminate/restart the
// process on a fatal poller error. The channel retains only the first failure.
func (w *pairedWorker) Errors() <-chan error { return w.failure }
func (w *pairedWorker) fail(err error) {
	if err == nil {
		return
	}
	w.failureOnce.Do(func() {
		w.failure <- err
		// SDK callbacks must not block the worker thread they are shutting down.
		go w.Stop()
	})
}

type workerFactory func(client.Client, string, worker.Options) worker.Worker

func newWorkers(c client.Client, queue string, a *Activities, create workerFactory) (worker.Worker, error) {
	if c == nil || queue == "" || a == nil || a.Store == nil || a.Artifacts == nil || a.Execute == nil || a.Encode == nil || a.PoolID == "" {
		return nil, ErrUnavailable
	}
	if err := a.Policy.Validate(); err != nil {
		return nil, err
	}
	if a.TaskQueue == "" {
		a.TaskQueue = queue
	}
	if a.TaskQueue != queue {
		return nil, ErrUnavailable
	}
	if a.Temporal == nil {
		a.Temporal = c
	}
	combined := &pairedWorker{stopped: make(chan struct{}), failure: make(chan error, 1)}
	// Poll capacity feeds metadata/workflow execution; it does not grant native
	// capacity. Bound both queues' pollers independently of their execution slots.
	pollers := min(8, max(2, (a.Policy.Concurrency+15)/16))
	workflowPollers := min(32, max(2, (a.Policy.Concurrency+3)/4))
	control := create(c, queue, worker.Options{MaxConcurrentWorkflowTaskPollers: workflowPollers, MaxConcurrentActivityTaskPollers: workflowPollers, MaxConcurrentActivityExecutionSize: a.Policy.Concurrency + 16, WorkerStopTimeout: 45 * time.Second, DisableRegistrationAliasing: true, OnFatalError: combined.fail})
	provider := create(c, queue+"/provider", worker.Options{MaxConcurrentActivityTaskPollers: pollers, MaxConcurrentActivityExecutionSize: a.Policy.Concurrency, WorkerStopTimeout: 45 * time.Second, DisableRegistrationAliasing: true, DisableWorkflowWorker: true, OnFatalError: combined.fail})
	for _, entry := range []struct {
		name string
		fn   interface{}
	}{
		{BatchWorkflowName, BatchWorkflow}, {DispatcherWorkflowName, DispatcherWorkflow}, {PoolWorkflowName, PoolWorkflow}, {AttemptWorkflowName, AttemptWorkflow},
	} {
		control.RegisterWorkflowWithOptions(entry.fn, workflow.RegisterOptions{Name: entry.name})
	}
	// Legacy Generate remains on the control queue until V1 is explicitly drained.
	// V2 inference is registered only on the separate provider activity queue.
	for _, entry := range []struct {
		name string
		fn   interface{}
	}{
		{"PrepareLegacyJobs", a.PrepareLegacyJobs}, {"AdvanceLegacyPool", a.AdvanceLegacyPool}, {"Enqueue", a.Enqueue}, {"Status", a.Status}, {"Expire", a.Expire}, {"Record", a.Record}, {"Cooldown", a.Cooldown}, {"Generate", a.Generate},
		{"PublishPool", a.PublishPool}, {"InitializePool", a.InitializePool}, {"SelectPoolReady", a.SelectPoolReady}, {"ReadPool", a.ReadPool}, {"StartAttempts", a.StartAttempts},
		{"RetireRequests", a.RetireRequests},
		{"SettleKnownAttempt", a.SettleKnownAttempt}, {"ReconcileAttempt", a.ReconcileAttempt}, {"NotifySettlement", a.NotifySettlement},
		{"FinalizeAttempt", a.FinalizeAttempt},
	} {
		control.RegisterActivityWithOptions(entry.fn, activity.RegisterOptions{Name: entry.name})
	}
	provider.RegisterActivityWithOptions(a.InvokeAttempt, activity.RegisterOptions{Name: "InvokeAttempt"})
	provider.RegisterActivityWithOptions(a.ProviderReady, activity.RegisterOptions{Name: "ProviderReady"})
	combined.Worker, combined.provider = control, provider
	return combined, nil
}
