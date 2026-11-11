//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-541.

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestReferenceUncertaintyHistory(t *testing.T) {
	t.Parallel()
	pool, _ := referencePool(t, true)
	service, store, calls, _ := referenceUncertaintyService(t, pool, "partial")
	input := uncertaintyRunInput()
	out, state, err := service.Run(t.Context(), keyHashA, input)
	if err == nil || ProjectRunError(err).Code != "execution_unconfirmed" || out.History.Status != "saved" || calls.Load() != 1 || len(store.observations) != 1 {
		t.Fatal("unknown provider outcome prevented independent recording or repeated inference", err, out.History, calls.Load())
	}
	record, err := service.GetHistory(t.Context(), keyHashA, state.LastRunID)
	if err != nil || record.Error == nil || record.Error.Code != "execution_unconfirmed" || record.Status != "failed" || countRows(t, pool, "history", keyHashA) != 1 {
		t.Fatal("stored failed local run lost its unknown provider outcome", err, record.Error)
	}
	var result RecordedRunOutput
	if json.Unmarshal(record.Result, &result) != nil || result.Output != nil || !result.ProviderInvoked || len(result.Attempts) != 1 || result.Accounting.Provider.Usage.Status == "complete" {
		t.Fatal("history invented output or known usage", result)
	}
	if _, err := service.GetHistory(t.Context(), keyHashB, out.RunID); err == nil {
		t.Fatal("uncertain history crossed key scope")
	}
	stats, err := service.Statistics(context.Background(), keyHashA, HistoryQuery{Before: time.Now().Add(time.Second)})
	if err != nil || stats.TotalCount != 1 || stats.Failed != 1 || stats.Succeeded != 0 || calls.Load() != 1 {
		t.Fatal("execution counts changed or history reads replayed inference", stats, err)
	}
}
