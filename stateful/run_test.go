package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-426 TEST-442

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/api"
	"testing"
	"time"
)

func TestHistoryRecordValues(t *testing.T) {
	t.Parallel()
	t.Run("pre-dispatch failure has a truthful complete payload", func(t *testing.T) {
		now := time.Date(2026, 10, 12, 17, 0, 0, 0, time.UTC)
		result := projectRun("00000000-0000-4000-8000-000000000001", RunInput{}, resolvedRun{}, hardenllm.Result{CallID: "00000000-0000-4000-8000-000000000002", TraceID: "00000000-0000-4000-8000-000000000003"}, errors.New("validation-fixture"), now, now)
		data, _ := json.Marshal(result)
		if err := api.Validate("RecordedRunOutput", data); err != nil {
			t.Fatal(err)
		}
	})
	for _, field := range []string{"accounting", "cache", "attempts", "selectedTarget"} {
		t.Run("reject corrupt nested "+field, func(t *testing.T) {
			record := historyFixtureRecord(t)
			var payload map[string]any
			if json.Unmarshal(record.Result, &payload) != nil {
				t.Fatal("invalid shared fixture")
			}
			switch field {
			case "accounting":
				payload[field].(map[string]any)["provider"].(map[string]any)["usage"].(map[string]any)["inputTokens"] = "bad-number"
			case "cache":
				payload[field].(map[string]any)["served"] = "bad-boolean"
			case "attempts":
				payload[field] = nil
			case "selectedTarget":
				payload[field] = nil
			}
			record.Result, _ = json.Marshal(payload)
			if err := validateHistoryRecord(record); err == nil {
				t.Fatal("malformed full result admitted for restore", field)
			}
		})
	}
	t.Run("SQL and JSON timestamps use the same microsecond precision", func(t *testing.T) {
		record := historyFixtureRecord(t)
		record.StartedAt = time.Date(2026, 10, 12, 17, 0, 0, 123456789, time.UTC)
		record.FinishedAt = record.StartedAt.Add(time.Second)
		record.Summary.StartedAt = record.StartedAt
		record.Summary.FinishedAt = record.FinishedAt
		normalized := historyStorageRecord(record)
		if normalized.StartedAt.Nanosecond() != 123456000 || normalized.FinishedAt.Nanosecond() != 123456000 || !normalized.Summary.StartedAt.Equal(normalized.StartedAt) || !normalized.Summary.FinishedAt.Equal(normalized.FinishedAt) {
			t.Fatal("JSON will disagree with PostgreSQL timestamp precision")
		}
	})
}

func TestReferenceRunValues(t *testing.T) {
	t.Parallel()
	t.Run("all profile leaves resolve without storage before execution", func(t *testing.T) {
		catalog := fixtureCatalog(t)
		preset := catalog["CPA Responses"]
		var definition Profile
		if json.Unmarshal(preset.Definition, &definition) != nil {
			t.Fatal("fixture definition invalid")
		}
		definition.BaseURL = "https://provider.example/v1"
		preset.Definition, _ = json.Marshal(definition)
		preset.Binding = ProfileCredentialBinding{Source: "deployment", DeploymentUpstreamID: "fixture"}
		s, err := NewService(Config{Client: ClientOptions{Connections: []hardenllm.Connection{{ID: "fixture", Provider: "cpa", Protocol: "responses", BaseURL: definition.BaseURL, APIKey: "fixture-provider-secret"}}, DefaultConnection: "fixture"}, Presets: Catalog{"CPA Responses": preset}})
		if err != nil {
			t.Fatal(err)
		}
		input := RunInput{ProfileID: "CPA Responses", UserPrompt: "fixture", CallType: hardenllm.CallTypeStructured, Schema: json.RawMessage(`{"type":"object"}`), ReasoningEffort: "highest", ProviderOptions: map[string]any{"max_tokens": 8, "stream": false}, RecoveryPolicy: json.RawMessage(`{"maxAttempts":6,"retryOn":[],"jsonRepair":{"initial":{"source":"generation"},"escalation":{"source":"profile","profileId":"CPA Responses","modelId":"repair-model","reasoningEffort":"lowest"}},"rerun":{"target":{"source":"profile","profileId":"CPA Responses","modelId":"rerun-model"},"jsonRepair":{"initial":{"source":"generation"},"escalation":{"source":"profile","profileId":"CPA Responses","modelId":"final-model"}}},"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`)}
		resolved, err := s.resolveRun(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", input)
		if err != nil {
			t.Fatal(err)
		}
		if err = resolved.request.RecoveryPolicy.Validate(); err != nil {
			t.Fatal("generation inheritance or prepared native settings invalid", err)
		}
		if resolved.request.ReasoningEffort != "" || fmt.Sprint(resolved.request.ProviderOptions["max_tokens"]) != "8" || resolved.request.ProviderOptions["stream"] != false || resolved.request.ProviderOptions["reasoning"].(map[string]any)["effort"] != "xhigh" {
			t.Fatal("portable settings or explicit values changed")
		}
		if len(resolved.connections) != 4 || len(resolved.profiles) != 1 || resolved.request.RecoveryPolicy.JSONRepair.Escalation.ModelID != "repair-model" || resolved.request.RecoveryPolicy.Rerun.Target.ModelID != "rerun-model" || resolved.request.RecoveryPolicy.Rerun.JSONRepair.Escalation.ModelID != "final-model" {
			t.Fatal("five leaf catalog was not prepared")
		}
		if fmt.Sprint(resolved.request.RecoveryPolicy.Rerun.Target.ProviderOptions["max_tokens"]) != "16000" {
			t.Fatal("new-model leaf inherited generation override")
		}
		resolved.request.ProviderOptions["max_tokens"] = 99
		if input.ProviderOptions["max_tokens"] != 8 || !sameJSON(s.config.Presets["CPA Responses"].Definition, preset.Definition) {
			t.Fatal("resolved settings mutate caller or template")
		}
	})
	t.Run("detached call preserves callbacks and explicit values", func(t *testing.T) {
		input := RunInput{Schema: json.RawMessage(`{"type":"object"}`), ProviderOptions: map[string]any{"nested": map[string]any{"zero": 0, "enabled": false}}, RecoveryPolicy: json.RawMessage(`{"maxAttempts":1}`), Ready: make(chan struct{}, 1)}
		called := false
		input.Completed = func(RunOutput, RunState, error) { called = true }
		copy, err := cloneRunInput(input)
		if err != nil {
			t.Fatal(err)
		}
		copy.ProviderOptions["nested"].(map[string]any)["zero"] = 99
		copy.Schema[0] = 'X'
		copy.RecoveryPolicy[0] = 'X'
		copy.Completed(RunOutput{}, RunState{}, nil)
		if input.ProviderOptions["nested"].(map[string]any)["zero"] != 0 || !json.Valid(input.Schema) || !json.Valid(input.RecoveryPolicy) || !called || copy.Ready != input.Ready {
			t.Fatal("reference caller can mutate another operation or lose lifecycle callbacks")
		}
	})
	t.Run("history excludes structurally supplied credentials and preserves prompts", func(t *testing.T) {
		input := RunInput{UserPrompt: "Keep the word password and this text exactly.", ProviderOptions: map[string]any{"apiKey": "option-secret-sentinel", "nested": map[string]any{"authorization": "header-secret-sentinel", "enabled": false}}, RecoveryPolicy: json.RawMessage(`{"maxAttempts":1,"jsonRepair":{"initial":{"source":"generation","providerOptions":{"secret":"repair-secret-sentinel","temperature":0}}}}`)}
		safe := safeHistoryInput(input)
		data, err := json.Marshal(safe)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"option-secret-sentinel", "header-secret-sentinel", "repair-secret-sentinel"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("plaintext credential entered history", secret)
			}
		}
		if safe.UserPrompt != input.UserPrompt || safe.ProviderOptions["nested"].(map[string]any)["enabled"] != false {
			t.Fatal("history lost ordinary caller data")
		}
	})
}
