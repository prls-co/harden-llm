package stateful

import (
	"context"
	hardenllm "github.com/prls-co/harden-llm"
)

type trackingExecutor struct{ next hardenllm.Executor }

func (e trackingExecutor) Prepare(ctx context.Context, c hardenllm.ProviderConnection, k hardenllm.ProviderCredential, call hardenllm.ProviderCall) (hardenllm.PreparedOperation, error) {
	return e.next.Prepare(ctx, c, k, call)
}
func (e trackingExecutor) Execute(ctx context.Context, p hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
	if ctx.Err() != nil {
		return hardenllm.ProviderResult{}, ctx.Err()
	}
	if s := trackingSession(ctx); s != nil {
		s.Phase("provider_attempt")
	}
	return e.next.Execute(ctx, p)
}
