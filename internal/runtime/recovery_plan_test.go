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
	profile        string
	stage          string
	history        []RepairHistoryEntry
	reasoning      string
	webSearch      bool
	providerOption map[string]any
	defaultOptions map[string]any
	userPrompt     string
}

func (executor *planExecutor) Prepare(_ context.Context, profile Profile, _ Credential, call Call) (PreparedOperation, error) {
	stage := stageOriginalGenerate
	var history []RepairHistoryEntry
	if call.Repair != nil {
		stage = call.Repair.Stage
		history = append(history, call.Repair.History...)
	}
	executor.prepared = append(executor.prepared, planPrepared{
		profile: profile.ID, stage: stage, history: history, reasoning: call.ReasoningEffort,
		webSearch: call.WebSearch, providerOption: cloneMap(call.ProviderOptions),
		defaultOptions: cloneMap(profile.DefaultOptions), userPrompt: call.UserPrompt,
	})
	return PreparedOperation{Operation: cachekey.Operation{
		SchemaVersion: cachekey.OperationSchemaVersion, Protocol: "fixture",
		Endpoint: cachekey.Endpoint{Identity: "https://fixture.example", Method: "POST", Path: "/run"},
		Model:    profile.ModelID, Payload: map[string]any{"stage": stage, "profile": profile.ID},
		SemanticHeaders: map[string]any{}, ResponseProjection: cachekey.ResponseProjection{Provider: profile.Provider, Kind: "structured-output", Version: "v1"},
	}, Opaque: planPrepared{profile: profile.ID, stage: stage, history: history}}, nil
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
	usage, err := accounting.CompleteUsage(1, 0, 0, 1, 0)
	if err != nil {
		panic(err)
	}
	return Ledger{Usage: usage, Cost: accounting.UnavailableCost()}
}

func planProfiles() map[string]Profile {
	profiles := make(map[string]Profile)
	for _, id := range []string{"original", "repair-l", "repair-h", "rerun", "rerun-l", "rerun-h"} {
		profiles[id] = Profile{ID: id, Provider: "fixture", APIInferenceType: "chat-completions", BaseURL: "https://fixture.example", ModelID: id, SupportsStructuredOutput: true, ReasoningEffortMap: map[string]map[string]any{"lowest": {}, "highest": {}}, DefaultOptions: map[string]any{"profileDefault": id}}
	}
	return profiles
}

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
			Initial:    retry.RecoveryTarget{Source: "profile", ProfileID: "repair-l", ReasoningEffort: "lowest"},
			Escalation: &retry.RecoveryTarget{Source: "profile", ProfileID: "repair-h", ReasoningEffort: "highest"},
		},
		Rerun: &retry.RerunPlan{
			Target: retry.RecoveryTarget{Source: "profile", ProfileID: "rerun", ReasoningEffort: "lowest"},
			JSONRepair: &retry.RepairPlan{
				Initial:    retry.RecoveryTarget{Source: "profile", ProfileID: "rerun-l", ReasoningEffort: "lowest"},
				Escalation: &retry.RecoveryTarget{Source: "profile", ProfileID: "rerun-h", ReasoningEffort: "highest"},
			},
		},
	}
	call := Call{CallType: "structured", UserPrompt: "return JSON", Schema: json.RawMessage(`{"type":"object"}`), ValidateStructured: func(value any) error {
		object, ok := value.(map[string]any)
		if !ok || object["ok"] != true {
			return errors.New("ok must be true")
		}
		return nil
	}, ReasoningEffort: "highest", WebSearch: true, ProviderOptions: map[string]any{"callerOption": "original"}}
	policy.JSONRepair.Initial.ProviderOptions = map[string]any{"callerOption": "repair-l"}
	policy.JSONRepair.Escalation.ProviderOptions = map[string]any{"callerOption": "repair-h"}
	policy.Rerun.Target.ProviderOptions = map[string]any{"callerOption": "rerun"}
	policy.Rerun.JSONRepair.Initial.ProviderOptions = map[string]any{"callerOption": "rerun-l"}
	policy.Rerun.JSONRepair.Escalation.ProviderOptions = map[string]any{"callerOption": "rerun-h"}
	record, err := Execute(context.Background(), executor, func(context.Context, Profile) (Credential, error) { return Credential{APIKey: "fixture"}, nil }, "original", planProfiles(), call, retry.Config{Policy: policy}, nil, cachekey.ModeOff, "v1", "call", "trace")
	if err != nil {
		t.Fatal(err)
	}
	if record.Output.(map[string]any)["ok"] != true || record.ResultSource.Producer == nil || record.ResultSource.Producer.ProfileID != "rerun-h" {
		t.Fatalf("result=%#v source=%#v", record.Output, record.ResultSource)
	}
	wantStages := []string{stageOriginalGenerate, stageOriginalRepairInitial, stageOriginalRepairEscalate, stageRerunGenerate, stageRerunRepairInitial, stageRerunRepairEscalate}
	gotStages := make([]string, 0, len(record.Attempts))
	for _, attempt := range record.Attempts {
		gotStages = append(gotStages, attempt.Stage)
	}
	if !reflect.DeepEqual(gotStages, wantStages) {
		t.Fatalf("stages=%v want=%v", gotStages, wantStages)
	}
	if len(executor.prepared) != len(wantStages) || len(executor.prepared[2].history) != 2 || len(executor.prepared[5].history) != 2 {
		t.Fatalf("prepared=%#v", executor.prepared)
	}
	if executor.prepared[3].history != nil {
		t.Fatalf("rerun generation leaked history: %#v", executor.prepared[3].history)
	}
	wantProfiles := []string{"original", "repair-l", "repair-h", "rerun", "rerun-l", "rerun-h"}
	wantReasoning := []string{"highest", "lowest", "highest", "lowest", "lowest", "highest"}
	wantSearch := []bool{true, false, false, true, false, false}
	wantOptions := []string{"original", "repair-l", "repair-h", "rerun", "rerun-l", "rerun-h"}
	for index, prepared := range executor.prepared {
		if prepared.profile != wantProfiles[index] || prepared.reasoning != wantReasoning[index] || prepared.webSearch != wantSearch[index] || prepared.providerOption["callerOption"] != wantOptions[index] {
			t.Fatalf("target settings at %d = %#v", index, prepared)
		}
		if prepared.defaultOptions["profileDefault"] != wantProfiles[index] {
			t.Fatalf("profile defaults leaked at %d = %#v", index, prepared.defaultOptions)
		}
	}
	if len(executor.prepared[2].history) != 2 || executor.prepared[2].history[0].Stage != stageOriginalGenerate || executor.prepared[2].history[1].Stage != stageOriginalRepairInitial {
		t.Fatalf("escalated repair history did not retain flat complete history: %#v", executor.prepared[2].history)
	}
	if executor.prepared[3].userPrompt != call.UserPrompt {
		t.Fatalf("rerun generation inherited repair context: %q", executor.prepared[3].userPrompt)
	}
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
			Initial:    retry.RecoveryTarget{Source: "profile", ProfileID: "repair-l"},
			Escalation: nil,
		},
		Rerun: nil,
	}
	call := Call{
		CallType: "structured", UserPrompt: "return JSON", Schema: json.RawMessage(`{"type":"object"}`),
		ValidateStructured: func(value any) error {
			if object, ok := value.(map[string]any); ok && object["ok"] == true {
				return nil
			}
			return errors.New("ok must be true")
		},
	}
	record, err := Execute(context.Background(), executor, func(context.Context, Profile) (Credential, error) {
		return Credential{}, nil
	}, "original", planProfiles(), call, retry.Config{Policy: policy}, nil, cachekey.ModeOff, "v1", "call", "trace")
	if err == nil || len(record.Attempts) != 1 || len(executor.prepared) != 1 {
		t.Fatalf("budget allowed another stage: err=%v attempts=%d prepared=%d", err, len(record.Attempts), len(executor.prepared))
	}
	if record.StopReason != "attempts_exhausted" || record.Attempts[0].Stage != stageOriginalGenerate {
		t.Fatalf("budget diagnostics = %#v attempts=%#v", record.Diagnostics, record.Attempts)
	}
}
