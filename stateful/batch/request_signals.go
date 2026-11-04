package batch

import (
	"context"
	"go.temporal.io/sdk/client"
)

// RequestPoolSignals wakes the existing authority. It cannot start or replace a
// pool and carries only bounded references, never inference input/output.
type RequestPoolSignals interface {
	Wake(context.Context, string) error
	Changed(context.Context, string, SettlementNotice) error
}

type TemporalRequestSignals struct{ Client client.Client }

func (s TemporalRequestSignals) Wake(ctx context.Context, pool string) error {
	if s.Client == nil || !validPool(pool) {
		return ErrUnavailable
	}
	return s.Client.SignalWorkflow(ctx, "hllm-cpa-pool-v1/"+pool, "", enqueueSignal, JobRef{})
}
func (s TemporalRequestSignals) Changed(ctx context.Context, pool string, notice SettlementNotice) error {
	if s.Client == nil || !validPool(pool) || !validPermitID(notice.PermitID) || notice.Revision < 0 {
		return ErrUnavailable
	}
	return s.Client.SignalWorkflow(ctx, "hllm-cpa-pool-v1/"+pool, "", settlementSignal, notice)
}
