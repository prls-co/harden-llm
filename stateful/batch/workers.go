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
	provider worker.Worker
	stopOnce sync.Once
	stopped  chan struct{}
}

func (w *pairedWorker) Start() error {
	if err := w.provider.Start(); err != nil {
		return err
	}
	if err := w.Worker.Start(); err != nil {
		w.provider.Stop()
		return err
	}
	return nil
}
func (w *pairedWorker) Stop() {
	w.stopOnce.Do(func() { w.provider.Stop(); w.Worker.Stop(); close(w.stopped) })
}
func (w *pairedWorker) Run(interrupt <-chan interface{}) error {
	if err := w.Start(); err != nil {
		return err
	}
	select {
	case <-interrupt:
	case <-w.stopped:
	}
	w.Stop()
	return nil
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
	control := create(c, queue, worker.Options{MaxConcurrentActivityExecutionSize: a.Policy.Concurrency + 16, WorkerStopTimeout: 45 * time.Second, DisableRegistrationAliasing: true})
	provider := create(c, queue+"/provider", worker.Options{MaxConcurrentActivityExecutionSize: a.Policy.Concurrency, WorkerStopTimeout: 45 * time.Second, DisableRegistrationAliasing: true, DisableWorkflowWorker: true})
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
		{"Enqueue", a.Enqueue}, {"Status", a.Status}, {"Expire", a.Expire}, {"Record", a.Record}, {"Cooldown", a.Cooldown}, {"Generate", a.Generate},
		{"InitializePool", a.InitializePool}, {"SelectPoolReady", a.SelectPoolReady}, {"ReadPool", a.ReadPool}, {"StartAttempts", a.StartAttempts},
		{"SettleKnownAttempt", a.SettleKnownAttempt}, {"ReconcileAttempt", a.ReconcileAttempt}, {"NotifySettlement", a.NotifySettlement},
	} {
		control.RegisterActivityWithOptions(entry.fn, activity.RegisterOptions{Name: entry.name})
	}
	provider.RegisterActivityWithOptions(a.InvokeAttempt, activity.RegisterOptions{Name: "InvokeAttempt"})
	provider.RegisterActivityWithOptions(a.ProviderReady, activity.RegisterOptions{Name: "ProviderReady"})
	return &pairedWorker{Worker: control, provider: provider, stopped: make(chan struct{})}, nil
}
