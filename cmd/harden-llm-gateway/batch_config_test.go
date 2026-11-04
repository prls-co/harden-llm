package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-479
import (
	hardenllm "github.com/prls-co/harden-llm"
	"strings"
	"testing"
	"time"
)

func TestBatchSettingsRequireExplicitSelfHostedDependencies(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	conns := []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses"}}
	s, err := loadBatchSettings(get, conns)
	if err != nil || s.enabled {
		t.Fatal("batch must be opt-in", err)
	}
	env["HARDEN_LLM_BATCH_ENABLED"] = "true"
	if _, err = loadBatchSettings(get, conns); err == nil {
		t.Fatal("missing Temporal accepted")
	}
	env["HARDEN_LLM_TEMPORAL_ADDRESS"] = "temporal:7233"
	env["HARDEN_LLM_TEMPORAL_NAMESPACE"] = "harden-llm-dev"
	env["HARDEN_LLM_BATCH_POOL_ID"] = "shared-cpa"
	env["HARDEN_LLM_BATCH_UPSTREAM"] = "cpa"
	s, err = loadBatchSettings(get, conns)
	if err != nil || s.policy.Concurrency != 128 || s.policy.StartRate != 100 || s.taskQueue != "harden-llm-batch-v1" {
		t.Fatalf("wrong defaults: %+v %v", s, err)
	}
	env["HARDEN_LLM_BATCH_CONCURRENCY"] = "256"
	s, err = loadBatchSettings(get, conns)
	if err != nil || s.policy.Concurrency != 256 {
		t.Fatal(err)
	}
	env["HARDEN_LLM_BATCH_CONCURRENCY"] = "1001"
	if _, err = loadBatchSettings(get, conns); err == nil {
		t.Fatal("unbounded capacity accepted")
	}
	env["HARDEN_LLM_BATCH_CONCURRENCY"] = "128"
	env["HARDEN_LLM_BATCH_WINDOW_REQUESTS"] = "200"
	env["HARDEN_LLM_BATCH_WINDOW_MS"] = "61000"
	s, err = loadBatchSettings(get, conns)
	if err != nil || s.policy.WindowRequests != 200 || s.policy.WindowDuration != 61*time.Second {
		t.Fatal("admission window not configured independently", err)
	}
	env["HARDEN_LLM_BATCH_WINDOW_REQUESTS"] = "1001"
	if _, err = loadBatchSettings(get, conns); err == nil {
		t.Fatal("unbounded admission window accepted")
	}
	env["HARDEN_LLM_BATCH_WINDOW_REQUESTS"] = "0"
	s, err = loadBatchSettings(get, conns)
	if err != nil || s.policy.WindowRequests != 0 {
		t.Fatal("explicit probing without an admission budget rejected", err)
	}
	env["HARDEN_LLM_TEMPORAL_ADDRESS"] = "https://secret:credential@host"
	if _, err = loadBatchSettings(get, conns); err == nil || strings.Contains(err.Error(), "credential@") {
		t.Fatal("unsafe Temporal address diagnostics", err)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-529
func TestBatchRecoveryJitterDefaultsToZeroAndHasExplicitBounds(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "0", "1000", "-1", "1001", "not-a-number"} {
		t.Run(raw, func(t *testing.T) {
			env := map[string]string{"HARDEN_LLM_BATCH_ENABLED": "true", "HARDEN_LLM_TEMPORAL_ADDRESS": "temporal:7233", "HARDEN_LLM_TEMPORAL_NAMESPACE": "test", "HARDEN_LLM_BATCH_POOL_ID": "test-pool", "HARDEN_LLM_BATCH_UPSTREAM": "cpa", "HARDEN_LLM_BATCH_RECOVERY_JITTER_MS": raw}
			s, err := loadBatchSettings(func(k string) string { return env[k] }, []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses"}})
			valid := raw == "" || raw == "0" || raw == "1000"
			if (err == nil) != valid || (valid && s.policy.RecoveryJitterMS != map[string]int{"": 0, "0": 0, "1000": 1000}[raw]) {
				t.Error("recovery padding setting ignored default/bounds", raw, s.policy.RecoveryJitterMS, err)
			}
		})
	}
}
