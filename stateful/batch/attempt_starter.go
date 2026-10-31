package batch

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

type AttemptStartResult struct {
	PermitID  string
	Started   bool
	ErrorCode string
}

// StartAttempts groups metadata RPCs, never inference. Every retry retains the
// same permit/workflow IDs, including uncertain ExecuteWorkflow acknowledgements.
func (a *Activities) StartAttempts(ctx context.Context, reservations []Reservation) ([]AttemptStartResult, error) {
	ledger, ok := a.Store.(ExecutionStore)
	if !ok || a.Temporal == nil || a.TaskQueue == "" || len(reservations) < 1 || len(reservations) > 16 {
		return nil, ErrUnavailable
	}
	bounded, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	out := make([]AttemptStartResult, len(reservations))
	var wait sync.WaitGroup
	for i, reservation := range reservations {
		out[i].PermitID = reservation.PermitID
		wait.Add(1)
		go func(i int, r Reservation) {
			defer wait.Done()
			if r.PoolID != a.PoolID {
				out[i].ErrorCode = "pool_mismatch"
				return
			}
			if _, err := ledger.ReserveAttempt(bounded, r); err != nil {
				out[i].ErrorCode = "reservation_unconfirmed"
				return
			}
			_, err := a.Temporal.ExecuteWorkflow(bounded, client.StartWorkflowOptions{ID: r.WorkflowID, TaskQueue: a.TaskQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, AttemptWorkflowName, AttemptInput{Reservation: r, ProviderTaskQueue: a.TaskQueue + "/provider"})
			var duplicate *serviceerror.WorkflowExecutionAlreadyStarted
			if err == nil || errors.As(err, &duplicate) {
				out[i].Started = true
				return
			}
			out[i].ErrorCode = "start_unconfirmed"
		}(i, reservation)
	}
	wait.Wait()
	return out, nil
}
