//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-544.

import (
	"bytes"
	"log/slog"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
)

func TestCPAAdmissionCacheHitDoesNotEmitDispatchDiagnostics(t *testing.T) {
	t.Parallel()
	pool, _ := referencePool(t, true)
	service, store, calls, _ := referenceUncertaintyService(t, pool, "completed")
	var logs bytes.Buffer
	service.config.Admission.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	input := uncertaintyRunInput()
	input.TimeoutMS = 5000
	input.CacheMode = hardenllm.CacheModeCache
	first, _, err := service.Run(t.Context(), keyHashA, input)
	if err != nil || !first.Cache.Written || calls.Load() != 1 || len(store.observations) != 1 {
		t.Fatal("cache seed did not cross the native dispatch boundary", err)
	}
	before := append([]byte(nil), logs.Bytes()...)
	second, _, err := service.Run(t.Context(), keyHashA, input)
	if err != nil || !second.Cache.Served || second.ProviderInvoked || calls.Load() != 1 || len(store.observations) != 1 || len(second.Attempts) != 0 {
		t.Fatal("cache hit fabricated a new provider attempt", err)
	}
	if !bytes.Equal(before, logs.Bytes()) || len(diagnosticLogRecords(t, logs.Bytes())) != 1 {
		t.Fatal("cache hit emitted inherited provider diagnostics")
	}
}
