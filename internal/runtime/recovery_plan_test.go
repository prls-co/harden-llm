package runtime

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-239 TEST-240 TEST-241 TEST-246

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
)

type planExecutor struct {
	prepared []planPrepared
	sequence map[string]any
}

type planPrepared struct {
	connection      string
	model           string
	stage           string
	history         []RepairHistoryEntry
	reasoning       string
	webSearch       bool
	webSearchTool   json.RawMessage
	toolChoice      ToolChoice
	providerOption  map[string]any
	ordinaryOptions map[string]any
	messages        []Message
}

func (executor *planExecutor) Prepare(_ context.Context, connection Connection, _ Credential, call Call) (PreparedOperation, error) {
	stage := stageOriginalGenerate
	var history []RepairHistoryEntry
	if call.Repair != nil {
		stage = call.Repair.Stage
		history = append(history, call.Repair.History...)
	}
	executor.prepared = append(executor.prepared, planPrepared{
		connection: connection.ID, model: call.ModelID, stage: stage, history: history, reasoning: call.ReasoningEffort,
		webSearch: call.WebSearch, webSearchTool: append(json.RawMessage(nil), call.WebSearchTool...), toolChoice: call.ToolChoice, providerOption: cloneMap(call.ProviderOptions), ordinaryOptions: cloneMap(call.OrdinaryOptions),
		messages: append([]Message(nil), call.Messages...),
	})
	return PreparedOperation{Operation: cachekey.Operation{
		SchemaVersion: cachekey.OperationSchemaVersion, Protocol: "fixture",
		Endpoint: cachekey.Endpoint{Identity: "https://fixture.example", Method: "POST", Path: "/run"},
		Model:    call.ModelID, Payload: map[string]any{"stage": stage, "connection": connection.ID},
		SemanticHeaders: map[string]any{}, ResponseProjection: cachekey.ResponseProjection{Provider: connection.Provider, Kind: "structured-output", Version: "v1"},
	}, Opaque: planPrepared{connection: connection.ID, stage: stage, history: history}}, nil
}

func (executor *planExecutor) Execute(_ context.Context, operation PreparedOperation) (ProviderResult, error) {
	prepared := operation.Opaque.(planPrepared)
	value, ok := executor.sequence[prepared.stage]
	if !ok {
		return ProviderResult{}, errors.New("missing scripted stage " + prepared.stage)
	}
	return ProviderResult{Output: value, ProviderDispatched: true, Accounting: planLedger()}, nil
}

func planLedger() Ledger {
	return Ledger{Usage: completeUsageWithoutTest(1, 0, 0, 1, 0), Cost: accounting.UnavailableCost()}
}

func planConnection() Connection {
	return Connection{ID: "primary", Provider: "fixture", APIInferenceType: "chat-completions", BaseURL: "https://fixture.example"}
}

func recoveryStageOrder() []string {
	return []string{
		"original.generate", "original.repair.initial", "original.repair.escalation",
		"rerun.generate", "rerun.repair.initial", "rerun.repair.escalation",
	}
}

func attemptStages(attempts []AttemptRecord) []string {
	stages := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		stages = append(stages, attempt.Stage)
	}
	return stages
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-535
func TestExplicitRecoveryPlanRunsBothBranchesWithFlatHistory(t *testing.T) {
	executor := &planExecutor{sequence: map[string]any{
		stageOriginalGenerate:       map[string]any{"ok": "bad"},
		stageOriginalRepairInitial:  map[string]any{"ok": "bad"},
		stageOriginalRepairEscalate: map[string]any{"ok": "bad"},
		stageRerunGenerate:          map[string]any{"ok": "bad"},
		stageRerunRepairInitial:     map[string]any{"ok": "bad"},
		stageRerunRepairEscalate:    map[string]any{"ok": true},
	}}
	policy := retry.Policy{
		MaxAttempts: 6, RetryOn: []retry.Category{}, Backoff: retry.Backoff{},
		JSONRepair: &retry.RepairPlan{
			Initial:    retry.RecoveryTarget{Source: "model", ModelID: "repair-low", ReasoningEffort: "low"},
			Escalation: &retry.RecoveryTarget{Source: "model", ModelID: "repair-high", ReasoningEffort: "high"},
		},
		Rerun: &retry.RerunPlan{
			Target: retry.RecoveryTarget{Source: "model", ModelID: "rerun", ReasoningEffort: "low"},
			JSONRepair: &retry.RepairPlan{
				Initial:    retry.RecoveryTarget{Source: "model", ModelID: "rerun-low", ReasoningEffort: "low"},
				Escalation: &retry.RecoveryTarget{Source: "model", ModelID: "rerun-high", ReasoningEffort: "high"},
			},
		},
	}
	call := Call{CallType: "structured", ModelID: "original-model", Messages: []Message{{Role: "user", Content: json.RawMessage(`"return JSON"`)}}, Schema: json.RawMessage(`{"type":"object"}`), ValidateStructured: func(value any) error {
		object, ok := value.(map[string]any)
		if !ok || object["ok"] != true {
			return errors.New("ok must be true")
		}
		return nil
	}, ReasoningEffort: "high", WebSearch: true, ProviderOptions: map[string]any{"callerOption": "original"}}
	call.WebSearchTool = json.RawMessage(`{"type":"web_search","search_context_size":"low"}`)
	call.ToolChoice = ToolChoice{Mode: "required"}
	policy.JSONRepair.Initial.ProviderOptions = map[string]any{"callerOption": "repair-l"}
	policy.JSONRepair.Escalation.ProviderOptions = map[string]any{"callerOption": "repair-h"}
	policy.Rerun.Target.ProviderOptions = map[string]any{"callerOption": "rerun"}
	policy.Rerun.JSONRepair.Initial.ProviderOptions = map[string]any{"callerOption": "rerun-l"}
	policy.Rerun.JSONRepair.Escalation.ProviderOptions = map[string]any{"callerOption": "rerun-h"}
	record, err := Execute(context.Background(), executor, planConnection(), Credential{APIKey: "fixture"}, call, retry.Config{Policy: policy}, nil, cachekey.ModeOff, "v1", "call", "trace")
	if err != nil {
		t.Fatal(err)
	}
	if record.Output.(map[string]any)["ok"] != true || record.ResultSource.Producer == nil || record.ResultSource.Producer.ConnectionID != "primary" || record.ResultSource.Producer.ModelID != "rerun-high" {
		t.Fatalf("result=%#v source=%#v", record.Output, record.ResultSource)
	}
	wantStages := recoveryStageOrder()
	gotStages := attemptStages(record.Attempts)
	if !reflect.DeepEqual(gotStages, wantStages) {
		t.Fatalf("stages=%v want=%v", gotStages, wantStages)
	}
	if len(executor.prepared) != len(wantStages) || len(executor.prepared[2].history) != 2 || len(executor.prepared[5].history) != 2 {
		t.Fatalf("prepared=%#v", executor.prepared)
	}
	if executor.prepared[3].history != nil {
		t.Fatalf("rerun generation leaked history: %#v", executor.prepared[3].history)
	}
	wantModels := []string{"original-model", "repair-low", "repair-high", "rerun", "rerun-low", "rerun-high"}
	wantReasoning := []string{"high", "low", "high", "low", "low", "high"}
	wantSearch := []bool{true, false, false, true, false, false}
	wantOptions := []string{"original", "repair-l", "repair-h", "rerun", "rerun-l", "rerun-h"}
	for index, prepared := range executor.prepared {
		if wantSearch[index] {
			if !reflect.DeepEqual(prepared.webSearchTool, call.WebSearchTool) || prepared.toolChoice != call.ToolChoice {
				t.Fatalf("generation search settings lost: %#v", prepared)
			}
		} else if len(prepared.webSearchTool) != 0 || prepared.toolChoice.Mode != "" {
			t.Fatalf("repair retained search settings: %#v", prepared)
		}
		if prepared.connection != "primary" || prepared.model != wantModels[index] || prepared.reasoning != wantReasoning[index] || prepared.webSearch != wantSearch[index] || prepared.providerOption["callerOption"] != wantOptions[index] {
			t.Fatalf("target settings at %d = %#v", index, prepared)
		}
	}
	if len(executor.prepared[2].history) != 2 || executor.prepared[2].history[0].Stage != stageOriginalGenerate || executor.prepared[2].history[1].Stage != stageOriginalRepairInitial {
		t.Fatalf("escalated repair history did not retain flat complete history: %#v", executor.prepared[2].history)
	}
	if !reflect.DeepEqual(executor.prepared[3].messages, call.Messages) {
		t.Fatalf("rerun generation inherited repair context: %#v", executor.prepared[3].messages)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-217 TEST-244 TEST-246
func TestRecoveryBoundaryAccountingCacheReplaysRerunRepair(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	call := decisionCall()
	rerunConnection := planConnection()
	rerunConnection.ID = "secondary"
	call.Connections = map[string]ResolvedConnection{rerunConnection.ID: {Connection: rerunConnection}}
	policy := decisionPlan()
	policy.JSONRepair = nil
	policy.MaxAttempts = 3
	policy.Rerun.Target.ConnectionID = rerunConnection.ID
	policy.Rerun.Target.ReasoningEffort = "high"
	config := retry.Config{Policy: policy}
	cache := &progressContractCache{}
	producer := &orderedPlanExecutor{succeedAt: 3}
	fresh, err := Execute(ctx, producer, planConnection(), Credential{}, call, config, cache, cachekey.ModeCache, "v1", "fresh", "trace")
	if err != nil || !fresh.Cache.Written || len(producer.dispatches) != 3 {
		t.Fatalf("rerun repair did not write: cache=%+v dispatches=%d err=%v", fresh.Cache, len(producer.dispatches), err)
	}
	if fresh.Cache.OriginalOperationHash == "" || fresh.Cache.OriginalOperationHash == cache.hash ||
		fresh.Cache.RerunOperationHash != cache.hash || cache.record.CompletedBy != "repair" ||
		cache.record.GenerationTarget.ModelID != "rerun" || cache.record.Producer.ModelID != "rerun-initial" {
		t.Fatalf("rerun cache identity: cache=%+v stored=%+v", fresh.Cache, cache.record)
	}

	cache.found = true
	replayExecutor := &orderedPlanExecutor{}
	var last ProgressSnapshot
	call.Progress = func(snapshot ProgressSnapshot) { last = snapshot }
	replay, err := Execute(ctx, replayExecutor, planConnection(), Credential{}, call, config, cache, cachekey.ModeCache, "v1", "replay", "trace")
	if err != nil || !replay.Cache.Served || replay.Cache.Written || len(replayExecutor.dispatches) != 1 ||
		!reflect.DeepEqual(attemptStages(replay.Attempts), []string{"original.generate"}) {
		t.Fatalf("rerun cache hit: cache=%+v stages=%v dispatches=%d err=%v", replay.Cache, attemptStages(replay.Attempts), len(replayExecutor.dispatches), err)
	}
	branches := replay.Diagnostics.BranchCaches
	if replay.Branch != "rerun" || replay.ProjectionCallID != "fresh" || replay.GenerationTarget != cache.record.GenerationTarget ||
		replay.ResultSource.Kind != ResultSourceCache || !reflect.DeepEqual(replay.ResultSource.Producer, fresh.ResultSource.Producer) ||
		replay.Accounting.Result != fresh.Accounting.Result || !reflect.DeepEqual(replay.Output, fresh.Output) ||
		len(branches) != 2 || branches[0].Branch != "original" || branches[0].Cache.Status != "miss" ||
		branches[1].Branch != "rerun" || branches[1].Cache.Status != "hit" {
		t.Fatalf("rerun replay lost result or branch identity: %+v", replay)
	}
	if fresh.Accounting.Provider.Usage.InputTokens != 3 || replay.Accounting.Provider.Usage.InputTokens != 1 {
		t.Fatalf("provider input tokens: fresh=%d replay=%d", fresh.Accounting.Provider.Usage.InputTokens, replay.Accounting.Provider.Usage.InputTokens)
	}
	assertRerunTerminal := func(record CallRecord, reason string) {
		t.Helper()
		if record.Diagnostics.Branch != "rerun" || record.Diagnostics.Stage != "rerun.generate" ||
			last.Type != "run.terminal" || !last.Terminal || last.Branch != "rerun" || last.Stage != "rerun.generate" ||
			last.ConnectionID != rerunConnection.ID || last.ReasoningEffort != "high" ||
			last.Attempt != 0 || last.AttemptsUsed != 1 || last.StopReason != reason {
			t.Errorf("terminal diagnostics=%s/%s progress=%+v", record.Diagnostics.Branch, record.Diagnostics.Stage, last)
		}
	}
	assertRerunTerminal(replay, "succeeded")

	cache.record.CompletedBy = "invalid"
	last = ProgressSnapshot{}
	replayExecutor = &orderedPlanExecutor{}
	rejected, err := Execute(ctx, replayExecutor, planConnection(), Credential{}, call, config, cache, cachekey.ModeCache, "v1", "rejected", "trace")
	if !errors.Is(err, ErrCacheIntegrity) || rejected.Cache.Served || rejected.Cache.Written || len(replayExecutor.dispatches) != 1 {
		t.Fatalf("invalid cache replay: cache=%+v dispatches=%d err=%v", rejected.Cache, len(replayExecutor.dispatches), err)
	}
	assertRerunTerminal(rejected, "terminal_error")
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-242 TEST-243
func TestExplicitRecoveryPlanDoesNotPreparePastGlobalAttemptBudget(t *testing.T) {
	executor := &planExecutor{sequence: map[string]any{
		stageOriginalGenerate:      map[string]any{"ok": false},
		stageOriginalRepairInitial: map[string]any{"ok": true},
	}}
	policy := retry.Policy{
		MaxAttempts: 1, RetryOn: []retry.Category{}, Backoff: retry.Backoff{},
		JSONRepair: &retry.RepairPlan{
			Initial:    retry.RecoveryTarget{Source: "model", ModelID: "repair-low"},
			Escalation: nil,
		},
		Rerun: nil,
	}
	call := Call{
		CallType: "structured", ModelID: "original-model", Messages: []Message{{Role: "user", Content: json.RawMessage(`"return JSON"`)}}, Schema: json.RawMessage(`{"type":"object"}`),
		ValidateStructured: func(value any) error {
			if object, ok := value.(map[string]any); ok && object["ok"] == true {
				return nil
			}
			return errors.New("ok must be true")
		},
	}
	record, err := Execute(context.Background(), executor, planConnection(), Credential{}, call, retry.Config{Policy: policy}, nil, cachekey.ModeOff, "v1", "call", "trace")
	if err == nil || len(record.Attempts) != 1 || len(executor.prepared) != 1 {
		t.Fatalf("budget allowed another stage: err=%v attempts=%d prepared=%d", err, len(record.Attempts), len(executor.prepared))
	}
	if record.StopReason != "attempts_exhausted" || record.Attempts[0].Stage != stageOriginalGenerate {
		t.Fatalf("budget diagnostics = %#v attempts=%#v", record.Diagnostics, record.Attempts)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-008
func TestOrdinaryOptionsSurviveModelRecovery(t *testing.T) {
	caller := map[string]any{"max_output_tokens": 128, "temperature": 0}
	call := Call{ModelID: "first", CallType: "structured", OptionProtocol: "responses", OrdinaryOptions: caller, Messages: []Message{{Role: "user", Content: json.RawMessage(`"hello"`)}}, ValidateStructured: func(v any) error {
		if v == "ok" {
			return nil
		}
		return errors.New("invalid")
	}}
	policy := retry.Policy{MaxAttempts: 3, RetryOn: []retry.Category{}, JSONRepair: &retry.RepairPlan{Initial: retry.RecoveryTarget{Source: "model", ModelID: "second", ProviderOptions: map[string]any{"max_output_tokens": 512}}, Escalation: &retry.RecoveryTarget{Source: "model", ModelID: "third"}}}
	executor := &planExecutor{sequence: map[string]any{stageOriginalGenerate: "bad", stageOriginalRepairInitial: "bad", stageOriginalRepairEscalate: "ok"}}
	record, err := Execute(context.Background(), executor, planConnection(), Credential{APIKey: "fixture"}, call, retry.Config{Policy: policy}, nil, cachekey.ModeOff, "v1", "id", "trace")
	if err != nil || len(record.Attempts) != 3 || len(executor.prepared) != 3 {
		t.Fatalf("attempts %v error %v", record.Attempts, err)
	}
	for _, stage := range executor.prepared {
		if !reflect.DeepEqual(stage.ordinaryOptions, caller) {
			t.Errorf("%s lost original caller options: %v", stage.model, stage.ordinaryOptions)
		}
	}
	if caller["max_output_tokens"] != 128 || record.Accounting.Provider.Usage.InputTokens != 3 {
		t.Fatalf("original or accounting changed: %v %v", caller, record.Accounting)
	}
}
