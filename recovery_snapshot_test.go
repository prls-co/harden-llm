package hardenllm_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-437

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"sync"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
)

type recoverySnapshotExecutor struct {
	mu         sync.Mutex
	prepared   []string
	dispatched []string
	finalModel string
	onPrepare  func()
}

func (e *recoverySnapshotExecutor) Prepare(ctx context.Context, c hardenllm.ProviderConnection, k hardenllm.ProviderCredential, call hardenllm.ProviderCall) (hardenllm.PreparedOperation, error) {
	if e.onPrepare != nil {
		e.onPrepare()
	}
	e.mu.Lock()
	e.prepared = append(e.prepared, c.ID+":"+call.ModelID+":"+k.APIKey)
	e.mu.Unlock()
	op, err := (&contractExecutor{}).Prepare(ctx, c, k, call)
	op.Opaque = c.ID + ":" + call.ModelID + ":" + k.APIKey
	return op, err
}
func (e *recoverySnapshotExecutor) Execute(_ context.Context, op hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
	e.mu.Lock()
	e.dispatched = append(e.dispatched, op.Opaque.(string))
	e.mu.Unlock()
	r := hardenllm.ProviderResult{Output: map[string]any{"ok": false}, ProviderDispatched: true}
	if op.Operation.Model != e.finalModel {
		r.Output = map[string]any{"ok": "invalid"}
	} else {
		r.Output = map[string]any{"ok": true}
	}
	r.Accounting.Usage.Status = "complete"
	r.Accounting.Usage.InputTokens = 2
	r.Accounting.Usage.OutputTokens = 1
	r.Accounting.Cost.Status = "unknown"
	r.Accounting.Cost.UnknownObservations = 1
	return r, nil
}
func snapshotOptions(e hardenllm.Executor) hardenllm.Options {
	a, b := 1.0, 2.0
	return hardenllm.Options{Connections: hardenllm.ConnectionCatalog{
		"a": {ID: "a", Provider: "openai", Protocol: "responses", BaseURL: "https://provider.example/v1", APIKey: "key-a", Pricing: &hardenllm.Pricing{Input: &a, Output: &a}},
		"b": {ID: "b", Provider: "openai", Protocol: "responses", BaseURL: "https://provider.example/v1", APIKey: "key-b", Pricing: &hardenllm.Pricing{Input: &b, Output: &b}},
	}, DefaultConnection: "a", Executor: e}
}
func snapshotRequest() hardenllm.Request {
	r := coreContractRequest()
	r.ModelID = "generate"
	r.CallType = hardenllm.CallTypeStructured
	r.Schema = json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`)
	r.RecoveryPolicy = hardenllm.RecoveryPolicy{MaxAttempts: 2, RetryOn: []hardenllm.RecoveryCategory{}, JSONRepair: &hardenllm.JSONRepairPlan{Initial: hardenllm.RecoveryTarget{Source: "model", ConnectionID: "b", ModelID: "repair"}}, Backoff: hardenllm.RecoveryBackoff{}}
	return r
}
func TestRecoverySnapshot(t *testing.T) {
	var fixture struct {
		Repair struct {
			Dispatches      []string
			ProviderCostUSD float64
			ResultCostUSD   float64
		}
		FullRecovery struct {
			Dispatches      []string
			ProviderCostUSD float64
		}
	}
	encoded, err := os.ReadFile("test/fixtures/reference/recovery-cross-profile.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatal(err)
	}
	t.Run("recovery policy is detached before preparing the generation", func(t *testing.T) {
		r := snapshotRequest()
		e := &recoverySnapshotExecutor{finalModel: "repair"}
		e.onPrepare = func() { r.RecoveryPolicy.JSONRepair.Initial.ConnectionID = "a" }
		client, err := hardenllm.New(snapshotOptions(e))
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.Call(context.Background(), r)
		if err != nil || !reflect.DeepEqual(e.dispatched, fixture.Repair.Dispatches) || result.Accounting.Provider.Cost.KnownSubtotalUSD != fixture.Repair.ProviderCostUSD {
			t.Fatalf("caller changed in-flight policy: dispatches=%v cost=%v err=%v", e.dispatched, result.Accounting.Provider.Cost.KnownSubtotalUSD, err)
		}
	})
	t.Run("repair uses exact connection key and attempt rates", func(t *testing.T) {
		e := &recoverySnapshotExecutor{finalModel: "repair"}
		opts := snapshotOptions(e)
		client, err := hardenllm.New(opts)
		if err != nil {
			t.Fatal(err)
		}
		*opts.Connections["a"].Pricing.Input = 99
		result, err := client.Call(context.Background(), snapshotRequest())
		if err != nil {
			t.Fatal(err)
		}
		want := fixture.Repair.Dispatches
		if !reflect.DeepEqual(e.dispatched, want) {
			t.Errorf("dispatches=%v want=%v", e.dispatched, want)
		}
		if math.Abs(result.Accounting.Provider.Cost.KnownSubtotalUSD-fixture.Repair.ProviderCostUSD) > 1e-9 || result.Accounting.Provider.Cost.KnownObservations != 2 || result.Accounting.Provider.Cost.UnknownObservations != 0 {
			t.Errorf("mixed attempt cost=%+v", result.Accounting.Provider.Cost)
		}
		if result.Accounting.Result.Cost.KnownSubtotalUSD != fixture.Repair.ResultCostUSD || result.ResultSource.Producer == nil || result.ResultSource.Producer.ConnectionID != "b" {
			t.Errorf("result producer/cost=%+v %+v", result.ResultSource, result.Accounting.Result.Cost)
		}
	})
	t.Run("unknown target fails before preparing or dispatching generation", func(t *testing.T) {
		e := &recoverySnapshotExecutor{finalModel: "repair"}
		client, _ := hardenllm.New(snapshotOptions(e))
		r := snapshotRequest()
		r.RecoveryPolicy.JSONRepair.Initial.ConnectionID = "missing"
		_, err := client.Call(context.Background(), r)
		if err == nil || len(e.prepared) != 0 || len(e.dispatched) != 0 {
			t.Fatalf("unknown target: err=%v prepared=%v dispatches=%v", err, e.prepared, e.dispatched)
		}
	})
	t.Run("all five recovery leaves keep their bindings", func(t *testing.T) {
		e := &recoverySnapshotExecutor{finalModel: "final"}
		client, _ := hardenllm.New(snapshotOptions(e))
		r := snapshotRequest()
		r.RecoveryPolicy.MaxAttempts = 6
		r.RecoveryPolicy.JSONRepair.Escalation = &hardenllm.RecoveryTarget{Source: "model", ConnectionID: "b", ModelID: "escalate"}
		r.RecoveryPolicy.Rerun = &hardenllm.RerunPlan{Target: hardenllm.RecoveryTarget{Source: "model", ConnectionID: "b", ModelID: "rerun"}, JSONRepair: &hardenllm.JSONRepairPlan{Initial: hardenllm.RecoveryTarget{Source: "model", ConnectionID: "a", ModelID: "rerun-repair"}, Escalation: &hardenllm.RecoveryTarget{Source: "model", ConnectionID: "b", ModelID: "final"}}}
		result, err := client.Call(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		want := fixture.FullRecovery.Dispatches
		if !reflect.DeepEqual(e.dispatched, want) || len(result.Attempts) != 6 || result.Accounting.Provider.Cost.KnownSubtotalUSD != fixture.FullRecovery.ProviderCostUSD {
			t.Fatalf("recovery bindings/cost: %v %+v %v", e.dispatched, result.Accounting.Provider.Cost, err)
		}
	})
	t.Run("reported unknown and zero prices remain distinct", func(t *testing.T) {
		for _, kind := range []string{"reported", "unknown", "zero"} {
			t.Run(kind, func(t *testing.T) {
				e := &contractExecutor{execute: func(context.Context, hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
					r, _ := (&contractExecutor{}).Execute(context.Background(), hardenllm.PreparedOperation{})
					if kind == "reported" {
						r.Accounting.Cost.Status = "exact"
						r.Accounting.Cost.Source = "reported"
						r.Accounting.Cost.KnownSubtotalUSD = 0.75
						r.Accounting.Cost.KnownObservations = 1
						r.Accounting.Cost.UnknownObservations = 0
					}
					return r, nil
				}}
				opts := snapshotOptions(e)
				if kind == "unknown" {
					c := opts.Connections["a"]
					c.Pricing = nil
					opts.Connections["a"] = c
				} else if kind == "zero" {
					z := 0.0
					c := opts.Connections["a"]
					c.Pricing = &hardenllm.Pricing{Input: &z, Output: &z}
					opts.Connections["a"] = c
				}
				client, _ := hardenllm.New(opts)
				result, err := client.Call(context.Background(), coreContractRequest())
				if err != nil {
					t.Fatal(err)
				}
				cost := result.Accounting.Provider.Cost
				if kind == "reported" && (cost.KnownSubtotalUSD != 0.75 || cost.Source != "reported") {
					t.Errorf("reported cost lost: %+v", cost)
				}
				if kind == "unknown" && cost.Status != "unknown" {
					t.Errorf("missing price became known: %+v", cost)
				}
				if kind == "zero" && (cost.Status != "exact" || cost.KnownSubtotalUSD != 0 || cost.KnownObservations != 1) {
					t.Errorf("explicit zero not known: %+v", cost)
				}
			})
		}
	})
	t.Run("concurrent requests do not change configured bindings", func(t *testing.T) {
		e := &recoverySnapshotExecutor{finalModel: "repair"}
		client, _ := hardenllm.New(snapshotOptions(e))
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				r, err := client.Call(context.Background(), snapshotRequest())
				if err != nil || r.Accounting.Provider.Cost.KnownSubtotalUSD != 9 {
					t.Errorf("concurrent snapshot: %v %+v", err, r.Accounting.Provider.Cost)
				}
			})
		}
		wg.Wait()
		if len(e.dispatched) != 20 {
			t.Errorf("concurrent dispatches=%d", len(e.dispatched))
		}
	})
}
