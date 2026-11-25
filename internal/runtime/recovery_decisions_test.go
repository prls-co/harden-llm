package runtime

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-462

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
)

type orderedPlanExecutor struct {
	planExecutor
	succeedAt  int
	dispatches []PreparedOperation
}

func (e *orderedPlanExecutor) Execute(_ context.Context, op PreparedOperation) (ProviderResult, error) {
	e.dispatches = append(e.dispatches, op)
	output := map[string]any{"ok": false}
	if len(e.dispatches) == e.succeedAt {
		output["ok"] = true
	}
	return ProviderResult{Output: output, ProviderDispatched: true, Accounting: planLedger()}, nil
}
func decisionPlan() retry.Policy {
	leaf := func(model string) retry.RecoveryTarget { return retry.RecoveryTarget{Source: "model", ModelID: model} }
	a, b, c, d, e := leaf("initial"), leaf("escalation"), leaf("rerun"), leaf("rerun-initial"), leaf("rerun-escalation")
	return retry.Policy{MaxAttempts: 6, RetryOn: []retry.Category{}, JSONRepair: &retry.RepairPlan{Initial: a, Escalation: &b}, Rerun: &retry.RerunPlan{Target: c, JSONRepair: &retry.RepairPlan{Initial: d, Escalation: &e}}}
}
func decisionCall() Call {
	return Call{CallType: "structured", ModelID: "generation", Messages: []Message{{Role: "user", Content: json.RawMessage(`"return JSON"`)}}, Schema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`), ValidateStructured: func(value any) error {
		if object, ok := value.(map[string]any); !ok || object["ok"] != true {
			return errors.New("ok must be true")
		}
		return nil
	}}
}

func TestRecoveryDecisionWitnesses(t *testing.T) {
	t.Parallel()
	stages := recoveryStageOrder()
	for success := 1; success <= len(stages); success++ {
		t.Run(fmt.Sprintf("success at %s", stages[success-1]), func(t *testing.T) {
			executor := &orderedPlanExecutor{succeedAt: success}
			waits := 0
			result, err := Execute(context.Background(), executor, planConnection(), Credential{APIKey: "synthetic"}, decisionCall(), retry.Config{Policy: decisionPlan(), Wait: func(context.Context, time.Duration) error { waits++; return nil }}, nil, cachekey.ModeOff, "v1", "call", "trace")
			got := attemptStages(result.Attempts)
			if err != nil || !reflect.DeepEqual(got, stages[:success]) || len(executor.prepared) != success || len(executor.dispatches) != success || waits != 0 || result.StopReason != "succeeded" || result.ResultSource.AttemptNumber != success {
				t.Fatalf("unexpected early exit: stages=%v prepares=%d calls=%d waits=%d result=%#v err=%v", got, len(executor.prepared), len(executor.dispatches), waits, result, err)
			}
		})
	}
	for _, tc := range []struct {
		name                                                    string
		repair, escalation, rerun, rerunRepair, rerunEscalation bool
		want                                                    []string
	}{
		{"all disabled", false, false, false, false, false, stages[:1]},
		{"repair only", true, false, false, false, false, stages[:2]},
		{"escalation only branch", true, true, false, false, false, stages[:3]},
		{"direct fresh rerun", false, false, true, false, false, []string{stages[0], stages[3]}},
		{"rerun repair", false, false, true, true, false, []string{stages[0], stages[3], stages[4]}},
		{"rerun escalation", false, false, true, true, true, []string{stages[0], stages[3], stages[4], stages[5]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := decisionPlan()
			if !tc.repair {
				policy.JSONRepair = nil
			} else if !tc.escalation {
				policy.JSONRepair.Escalation = nil
			}
			if !tc.rerun {
				policy.Rerun = nil
			} else if !tc.rerunRepair {
				policy.Rerun.JSONRepair = nil
			} else if !tc.rerunEscalation {
				policy.Rerun.JSONRepair.Escalation = nil
			}
			executor := &orderedPlanExecutor{}
			waits := 0
			result, err := Execute(context.Background(), executor, planConnection(), Credential{}, decisionCall(), retry.Config{Policy: policy, Wait: func(context.Context, time.Duration) error { waits++; return nil }}, nil, cachekey.ModeOff, "v1", "call", "trace")
			got := attemptStages(result.Attempts)
			if err == nil || !reflect.DeepEqual(got, tc.want) || len(executor.prepared) != len(tc.want) || len(executor.dispatches) != len(tc.want) || waits != 0 || result.Output != nil {
				t.Fatalf("disabled branch differs: stages=%v want=%v err=%v", got, tc.want, err)
			}
		})
	}
}

func TestRecoveryDeadlineIndependence(t *testing.T) {
	t.Parallel()
	for _, callType := range []string{"text", "structured"} {
		for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
			t.Run(callType+"/"+delta.String(), func(t *testing.T) {
				const planned, actual = 500 * time.Millisecond, 100 * time.Millisecond
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				deadline, _ := ctx.Deadline()
				now := deadline.Add(-planned + delta)
				executor := &recoveryExecutor{failures: []error{&retry.ProviderError{Status: 503, RetryAfter: planned}}}
				var waits []time.Duration
				policy := retry.DefaultPolicy()
				policy.MaxAttempts = 2
				policy.JSONRepair = nil
				call := Call{CallType: callType, ModelID: "generation", ValidateStructured: func(any) error { return nil }}
				config := retry.Config{
					Policy: policy, Now: func() time.Time { return now }, Random: func() float64 { return 0 },
					Wait: func(_ context.Context, delay time.Duration) error {
						waits = append(waits, delay)
						now = now.Add(actual)
						return nil
					},
				}
				result, err := Execute(ctx, executor, planConnection(), Credential{}, call, config, nil, cachekey.ModeOff, "v1", "call", "trace")
				if delta < 0 {
					if err != nil || !reflect.DeepEqual(waits, []time.Duration{planned}) || len(executor.dispatched) != 2 || executor.prepares != 1 || len(result.Attempts) != 2 {
						t.Fatalf("fitting retry changed: waits=%v prepares=%d calls=%d attempts=%d err=%v", waits, executor.prepares, len(executor.dispatched), len(result.Attempts), err)
					}
					want := WaitDiagnostics{Reason: string(retry.CategoryServer), Planned: planned, Actual: actual, RetryAfter: planned}
					first := result.Attempts[0]
					if first.Delay != planned || first.WaitDiagnostics == nil || *first.WaitDiagnostics != want || result.Diagnostics.TotalActualWait != actual {
						t.Fatalf("wait diagnostics=%#v delay=%v total=%v", first.WaitDiagnostics, first.Delay, result.Diagnostics.TotalActualWait)
					}
				} else {
					if !errors.Is(err, context.DeadlineExceeded) || len(waits) != 0 || len(executor.dispatched) != 1 || executor.prepares != 1 || len(result.Attempts) != 1 || result.StopReason != "deadline_exceeded" {
						t.Fatalf("unfitting wait admitted: result=%#v waits=%v err=%v", result, waits, err)
					}
					first := result.Attempts[0]
					if first.Delay != 0 || first.WaitDiagnostics != nil || result.Diagnostics.TotalActualWait != 0 {
						t.Fatalf("unattempted wait diagnostics=%#v delay=%v total=%v", first.WaitDiagnostics, first.Delay, result.Diagnostics.TotalActualWait)
					}
				}
			})
		}
	}
}
