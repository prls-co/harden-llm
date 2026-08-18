package runtime

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-008 TEST-009

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
)

func TestStructuredRepair(t *testing.T) {
	if RepairEligible(1, 1, retry.Classification{Category: retry.CategoryParse, Retryable: true}, true) {
		t.Fatal("repair escaped the existing one-attempt budget")
	}
	if !RepairEligible(1, 2, retry.Classification{Category: retry.CategoryParse, Retryable: true}, true) {
		t.Fatal("parse failure with schema and remaining budget should be repair eligible")
	}
	if RepairEligible(1, 2, retry.Classification{Category: retry.CategoryServer, Retryable: true}, true) {
		t.Fatal("server failure should not trigger structured repair")
	}

	envelope := json.RawMessage(`{"repair":{"explanation":"fixed type","changes":["made answer a string"]},"data":{"answer":"ok"}}`)
	data, metadata, err := ExtractRepairData(envelope, func(value json.RawMessage) error {
		if string(value) != `{"answer":"ok"}` {
			return errors.New("unexpected data")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"answer":"ok"}` || metadata.Explanation != "fixed type" || !reflect.DeepEqual(metadata.Changes, []string{"made answer a string"}) {
		t.Fatalf("unexpected repair result: data=%s metadata=%#v", data, metadata)
	}
	for _, invalid := range []string{
		`{"data":{"answer":"ok"}}`,
		`{"repair":{"explanation":"x","changes":[]},"data":{"answer":"ok"},"extra":true}`,
		`{"repair":{"explanation":"x","changes":[1]},"data":{"answer":"ok"}}`,
	} {
		if _, _, err := ExtractRepairData(json.RawMessage(invalid), func(json.RawMessage) error { return nil }); err == nil {
			t.Fatalf("invalid repair envelope accepted: %s", invalid)
		}
	}

	t.Run("execute loop consumes shared attempt budget", func(t *testing.T) {
		executor := &repairSequenceExecutor{}
		contract := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)
		record, err := Execute(
			context.Background(), executor,
			func(context.Context, Profile) (Credential, error) { return Credential{}, nil },
			"primary", map[string]Profile{"primary": {ID: "primary"}},
			Call{
				CallType: "structured", Schema: contract,
				StructuredRepair: StructuredRepair{Enabled: true},
				ValidateStructured: func(value any) error {
					object, ok := value.(map[string]any)
					if !ok || object["answer"] != "ok" {
						return errors.New("answer must be ok string")
					}
					return nil
				},
			},
			retry.Config{
				MaxAttempts: 2,
				Policy:      retry.Policy{Network: true, RateLimit: true, ServerError: true, EmptyResponse: true, ParseError: true},
				Wait:        func(context.Context, time.Duration) error { return nil },
			},
			nil, cachekey.ModeOff, "operation-v2", "call", "trace",
		)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(record.Output, map[string]any{"answer": "ok"}) {
			t.Fatalf("repair output = %#v", record.Output)
		}
		if executor.prepares != 2 || executor.executes != 2 || len(record.Attempts) != 2 || !record.Attempts[1].Repair {
			t.Fatalf("repair counts/attempts = %d/%d/%#v", executor.prepares, executor.executes, record.Attempts)
		}
		if record.Usage.InputTokens != 18 || record.Usage.OutputTokens != 5 || record.Usage.TotalTokens != 23 {
			t.Fatalf("repair usage was not accumulated: %#v", record.Usage)
		}
	})

	t.Run("repair escalation switches profile and credential", func(t *testing.T) {
		executor := &repairSequenceExecutor{}
		_, err := Execute(
			context.Background(), executor,
			func(_ context.Context, profile Profile) (Credential, error) {
				return Credential{APIKey: profile.ID + "-credential"}, nil
			},
			"primary", map[string]Profile{
				"primary": {ID: "primary", ModelID: "primary-model"},
				"backup":  {ID: "backup", ModelID: "backup-model"},
			},
			Call{
				CallType: "structured", Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`),
				StructuredRepair: StructuredRepair{Enabled: true, Escalation: &RepairEscalation{Attempt: 2, ProfileID: "backup", ModelID: "repair-model"}},
				ValidateStructured: func(value any) error {
					object, ok := value.(map[string]any)
					if !ok || object["answer"] != "ok" {
						return errors.New("answer must be ok string")
					}
					return nil
				},
			},
			retry.Config{
				MaxAttempts: 2,
				Policy:      retry.Policy{ParseError: true},
				Wait:        func(context.Context, time.Duration) error { return nil },
			},
			nil, cachekey.ModeOff, "operation-v2", "call", "trace",
		)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(executor.profiles, []string{"primary", "backup"}) {
			t.Fatalf("prepared profiles = %#v", executor.profiles)
		}
		if !reflect.DeepEqual(executor.credentials, []string{"primary-credential", "backup-credential"}) {
			t.Fatalf("prepared credentials = %#v", executor.credentials)
		}
	})

	t.Run("terminal parse failure preserves billable accounting", func(t *testing.T) {
		executor := partialFailureExecutor{}
		record, err := Execute(
			context.Background(), executor,
			func(context.Context, Profile) (Credential, error) { return Credential{}, nil },
			"primary", map[string]Profile{"primary": {ID: "primary"}},
			Call{CallType: "structured", Schema: json.RawMessage(`{"type":"object"}`)},
			retry.Config{MaxAttempts: 1, Policy: retry.Policy{ParseError: true}},
			nil, cachekey.ModeOff, "operation-v2", "call", "trace",
		)
		if err == nil {
			t.Fatal("terminal provider parse failure was accepted")
		}
		if record.Usage != (Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}) {
			t.Fatalf("partial usage was lost: %#v", record.Usage)
		}
		if record.Cost != (Cost{TotalUSD: 0.25, Known: true, Source: "reported"}) {
			t.Fatalf("partial cost was lost: %#v", record.Cost)
		}
		if !strings.Contains(string(record.ParseFailureResponse), `"rawResponse":"not-json"`) {
			t.Fatalf("parse failure evidence was lost: %s", record.ParseFailureResponse)
		}
	})
}

type repairSequenceExecutor struct {
	prepares    int
	executes    int
	profiles    []string
	credentials []string
}

func (executor *repairSequenceExecutor) Prepare(_ context.Context, profile Profile, credential Credential, call Call) (PreparedOperation, error) {
	executor.prepares++
	executor.profiles = append(executor.profiles, profile.ID)
	executor.credentials = append(executor.credentials, credential.APIKey)
	return PreparedOperation{
		Operation: cachekey.Operation{
			SchemaVersion: cachekey.OperationSchemaVersion,
			Protocol:      "fixture", Endpoint: cachekey.Endpoint{Identity: "https://example.com:443", Method: "POST", Path: "/run"},
			Model: "fixture", Payload: map[string]any{"repair": call.Repair != nil},
			SemanticHeaders: map[string]any{}, ResponseProjection: cachekey.ResponseProjection{Provider: "fixture", Kind: "fixture", Version: "v1"},
		},
		Opaque: call.Repair != nil,
	}, nil
}

func (executor *repairSequenceExecutor) Execute(_ context.Context, operation PreparedOperation) (ProviderResult, error) {
	executor.executes++
	if operation.Opaque == true {
		return ProviderResult{
			Output: map[string]any{
				"repair": map[string]any{"explanation": "fixed type", "changes": []any{"answer is string"}},
				"data":   map[string]any{"answer": "ok"},
			},
			Usage: Usage{InputTokens: 10, OutputTokens: 3, TotalTokens: 13},
		}, nil
	}
	return ProviderResult{
		Output: map[string]any{"answer": float64(42)},
		Usage:  Usage{InputTokens: 8, OutputTokens: 2, TotalTokens: 10},
	}, nil
}

type partialFailureExecutor struct{}

func (partialFailureExecutor) Prepare(context.Context, Profile, Credential, Call) (PreparedOperation, error) {
	return PreparedOperation{Operation: cachekey.Operation{
		SchemaVersion:      cachekey.OperationSchemaVersion,
		Protocol:           "fixture",
		Endpoint:           cachekey.Endpoint{Identity: "https://example.com:443", Method: "POST", Path: "/run"},
		Model:              "fixture",
		Payload:            map[string]any{},
		SemanticHeaders:    map[string]any{},
		ResponseProjection: cachekey.ResponseProjection{Provider: "fixture", Kind: "fixture", Version: "v1"},
	}}, nil
}

func (partialFailureExecutor) Execute(context.Context, PreparedOperation) (ProviderResult, error) {
	return ProviderResult{
		Usage: Usage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10},
		Cost:  Cost{TotalUSD: 0.25, Known: true, Source: "reported"},
	}, &retry.ProviderError{Err: errors.New("invalid structured output"), Parse: true, RawResponse: "not-json"}
}

func TestBackupProfiles(t *testing.T) {
	base := map[string]ProfileNode{
		"primary":  {ID: "primary", Backups: []string{"backup-b", "backup-a"}},
		"backup-a": {ID: "backup-a"},
		"backup-b": {ID: "backup-b", Backups: []string{"backup-a"}},
	}
	plan, err := BuildBackupPlan("primary", base)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan, []string{"primary", "backup-b", "backup-a"}) {
		t.Fatalf("plan = %#v", plan)
	}

	tests := []struct {
		name     string
		profiles map[string]ProfileNode
		want     string
	}{
		{name: "self", profiles: map[string]ProfileNode{"p": {ID: "p", Backups: []string{"p"}}}, want: "itself"},
		{name: "duplicate", profiles: map[string]ProfileNode{"p": {ID: "p", Backups: []string{"b", "b"}}, "b": {ID: "b"}}, want: "duplicate"},
		{name: "missing", profiles: map[string]ProfileNode{"p": {ID: "p", Backups: []string{"missing"}}}, want: "not found"},
		{name: "cycle", profiles: map[string]ProfileNode{"p": {ID: "p", Backups: []string{"b"}}, "b": {ID: "b", Backups: []string{"p"}}}, want: "cycle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildBackupPlan("p", test.profiles)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	depthFive := map[string]ProfileNode{}
	for index := 0; index <= 5; index++ {
		id := string(rune('0' + index))
		node := ProfileNode{ID: id}
		if index < 5 {
			node.Backups = []string{string(rune('0' + index + 1))}
		}
		depthFive[id] = node
	}
	if _, err := BuildBackupPlan("0", depthFive); err != nil {
		t.Fatalf("depth five rejected: %v", err)
	}
	depthFive["5"] = ProfileNode{ID: "5", Backups: []string{"6"}}
	depthFive["6"] = ProfileNode{ID: "6"}
	if _, err := BuildBackupPlan("0", depthFive); err == nil {
		t.Fatal("depth six accepted")
	}

	for _, category := range []retry.Category{retry.CategoryNetwork, retry.CategoryRateLimit, retry.CategoryServer} {
		if !BackupEligible(retry.Classification{Category: category}) {
			t.Fatalf("%s should be backup eligible", category)
		}
	}
	if BackupEligible(retry.Classification{Category: retry.CategoryParse}) {
		t.Fatal("parse error should not be backup eligible")
	}
	if BackupEligible(retry.Classification{Category: retry.CategoryProvider}) {
		t.Fatal("provider retry directive should stay on the current profile")
	}
}

func TestBackupEligibilityParityCapturedSource(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/parity/generated/retry-classification.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name             string `json:"name"`
			FallbackEligible bool   `json:"fallbackEligible"`
			Classification   struct {
				Category retry.Category `json:"category"`
			} `json:"classification"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range fixture.Cases {
		if got := BackupEligible(retry.Classification{Category: testCase.Classification.Category}); got != testCase.FallbackEligible {
			t.Errorf("%s backup eligibility = %t, want %t", testCase.Name, got, testCase.FallbackEligible)
		}
	}
}
