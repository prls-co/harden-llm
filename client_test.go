package hardenllm

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-006

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/prls-co/harden-llm/internal/cachekey"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
)

type fixedCredentialResolver struct{}

func (fixedCredentialResolver) ResolveCredential(context.Context, CredentialRequest) (Credential, error) {
	return Credential{APIKey: "fixture-only-key"}, nil
}

type fixedExecutor struct {
	prepared int
	executed int
	result   coreruntime.ProviderResult
	err      error
}

func (executor *fixedExecutor) Prepare(_ context.Context, profile coreruntime.Profile, _ coreruntime.Credential, call coreruntime.Call) (coreruntime.PreparedOperation, error) {
	executor.prepared++
	return coreruntime.PreparedOperation{
		Operation: cachekey.Operation{
			SchemaVersion: "utility-llm.operation.v1",
			Protocol:      profile.APIInferenceType,
			Endpoint: cachekey.Endpoint{
				Identity: "https://api.openai.com:443",
				Method:   "POST",
				Path:     "/v1/responses",
			},
			Model:   profile.ModelID,
			Payload: map[string]any{"input": call.UserPrompt, "model": profile.ModelID},
			ResponseProjection: cachekey.ResponseProjection{
				Provider: "openai",
				Kind:     "responses",
				Version:  "v1",
			},
		},
	}, nil
}

func (executor *fixedExecutor) Execute(context.Context, coreruntime.PreparedOperation) (coreruntime.ProviderResult, error) {
	executor.executed++
	return executor.result, executor.err
}

func TestClientCallResult(t *testing.T) {
	tests := []struct {
		name     string
		callType CallType
		output   any
		schema   json.RawMessage
	}{
		{name: "text", callType: CallTypeText, output: "Apples, bananas"},
		{
			name: "structured", callType: CallTypeStructured,
			output: map[string]any{"items": []any{"apples", "bananas"}},
			schema: json.RawMessage(`{"type":"object","properties":{"items":{"type":"array","items":{"type":"string"}}},"required":["items"],"additionalProperties":false}`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &fixedExecutor{result: coreruntime.ProviderResult{
				Output:              test.output,
				Usage:               coreruntime.Usage{InputTokens: 12, OutputTokens: 3, TotalTokens: 15},
				Cost:                coreruntime.Cost{TotalUSD: 0.0000225, Known: true, Source: "calculated"},
				RawProviderEnvelope: json.RawMessage(`{"id":"fixture-response"}`),
			}}
			client, err := New(Options{Credentials: fixedCredentialResolver{}})
			if err != nil {
				t.Fatal(err)
			}
			client.executor = executor
			ids := []string{"call-fixed", "trace-fixed"}
			client.newID = func() (string, error) {
				id := ids[0]
				ids = ids[1:]
				return id, nil
			}
			var observed coreruntime.CallRecord
			client.observeRecord = func(record coreruntime.CallRecord) { observed = record }

			result, callErr := client.Call(context.Background(), Request{
				ProfileID:    "primary",
				Profiles:     testProfiles(),
				UserPrompt:   "deterministic fixture",
				CallType:     test.callType,
				Schema:       test.schema,
				CacheMode:    CacheModeOff,
				CacheVersion: "operation-v2",
				RetryPolicy:  RetryPolicy{MaxAttempts: 1},
			})
			if callErr != nil {
				t.Fatal(callErr)
			}
			if !reflect.DeepEqual(result.Output, test.output) {
				t.Fatalf("Output = %#v, want %#v", result.Output, test.output)
			}
			if result.CallID != "call-fixed" || result.TraceID != "trace-fixed" {
				t.Fatalf("unexpected IDs: %#v", result)
			}
			if result.Usage.TotalTokens != 15 || !result.Cost.Known || len(result.Attempts) != 1 {
				t.Fatalf("incomplete normalized result: %#v", result)
			}
			if observed.CallID != result.CallID || observed.TraceID != result.TraceID || !reflect.DeepEqual(observed.Output, result.Output) {
				t.Fatalf("observer and Result did not derive from one record: observed=%#v result=%#v", observed, result)
			}
			if executor.prepared != 1 || executor.executed != 1 {
				t.Fatalf("prepare/execute counts = %d/%d, want 1/1", executor.prepared, executor.executed)
			}
		})
	}

	t.Run("provider error", func(t *testing.T) {
		client, _ := New(Options{Credentials: fixedCredentialResolver{}})
		client.executor = &fixedExecutor{err: errors.New("provider failed")}
		client.newID = func() (string, error) { return "fixed", nil }
		_, err := client.Call(context.Background(), Request{
			ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "fixture",
			CallType:    CallTypeText,
			RetryPolicy: RetryPolicy{MaxAttempts: 1},
		})
		if err == nil {
			t.Fatal("Call succeeded, want provider error")
		}
	})

	t.Run("identity source failure", func(t *testing.T) {
		client, _ := New(Options{Credentials: fixedCredentialResolver{}})
		executor := &fixedExecutor{}
		client.executor = executor
		client.newID = func() (string, error) { return "", errors.New("entropy unavailable") }
		_, err := client.Call(context.Background(), Request{
			ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "fixture", CallType: CallTypeText,
			RetryPolicy: RetryPolicy{MaxAttempts: 1},
		})
		if err == nil || executor.prepared != 0 || executor.executed != 0 {
			t.Fatalf("identity source failure was not returned before provider execution: %v %#v", err, executor)
		}
	})
}

func TestRuntimeProfilesEnforcesStrictCatalogContract(t *testing.T) {
	t.Parallel()
	if _, err := runtimeProfiles(testProfiles()); err != nil {
		t.Fatalf("valid catalog rejected: %v", err)
	}
	normalizedInput := testProfiles()
	profile := normalizedInput["primary"]
	profile.Provider = " openai "
	profile.BaseURL = "https://api.openai.com/v1/"
	profile.ModelID = " gpt-test "
	normalizedInput["primary"] = profile
	normalized, err := runtimeProfiles(normalizedInput)
	if err != nil || normalized["primary"].Provider != "openai" || normalized["primary"].BaseURL != "https://api.openai.com/v1" || normalized["primary"].ModelID != "gpt-test" {
		t.Fatalf("typed catalog was not normalized through the source contract: %#v %v", normalized, err)
	}
	tests := []struct {
		name   string
		mutate func(ProfileCatalog)
	}{
		{"key mismatch", func(catalog ProfileCatalog) {
			profile := catalog["primary"]
			profile.LLMProfile = "other"
			catalog["primary"] = profile
		}},
		{"unsupported API", func(catalog ProfileCatalog) {
			profile := catalog["primary"]
			profile.APIInferenceType = "openai.responses"
			catalog["primary"] = profile
		}},
		{"insecure endpoint", func(catalog ProfileCatalog) {
			profile := catalog["primary"]
			profile.BaseURL = "http://api.openai.com/v1"
			catalog["primary"] = profile
		}},
		{"credential in defaults", func(catalog ProfileCatalog) {
			profile := catalog["primary"]
			profile.DefaultOptions = map[string]any{"apiKey": "forbidden"}
			catalog["primary"] = profile
		}},
		{"invalid reasoning level", func(catalog ProfileCatalog) {
			profile := catalog["primary"]
			profile.ReasoningEffortMap = map[string]map[string]any{"high": {}}
			catalog["primary"] = profile
		}},
		{"missing backup", func(catalog ProfileCatalog) {
			profile := catalog["primary"]
			profile.BackupProfiles = []string{"missing"}
			catalog["primary"] = profile
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			catalog := testProfiles()
			test.mutate(catalog)
			if _, err := runtimeProfiles(catalog); err == nil {
				t.Fatal("invalid typed catalog was accepted")
			}
		})
	}
}

func TestRetryAndStructuredRepairOptionParity(t *testing.T) {
	t.Parallel()
	if !retryConfig(RetryPolicy{}, true).Policy.ParseError {
		t.Fatal("structured parse retries must be enabled by default")
	}
	if retryConfig(RetryPolicy{}, false).Policy.ParseError {
		t.Fatal("text calls must not enable parse retries")
	}
	disabled := false
	if retryConfig(RetryPolicy{RetryParse: &disabled}, true).Policy.ParseError {
		t.Fatal("explicit structured parse retry disable was ignored")
	}
	valid, err := runtimeRepairPolicy(StructuredRepairPolicy{
		Enabled: true, Escalation: &RepairEscalation{Attempt: 2, ModelID: " repair-model ", ReasoningEffort: ReasoningEffortHighest},
	}, 3, true)
	if err != nil || valid.Escalation == nil || valid.Escalation.ModelID != "repair-model" {
		t.Fatalf("valid repair policy mismatch: %#v %v", valid, err)
	}
	invalid := []struct {
		policy      StructuredRepairPolicy
		maxAttempts int
		structured  bool
	}{
		{policy: StructuredRepairPolicy{Escalation: &RepairEscalation{Attempt: 2}}, maxAttempts: 2, structured: true},
		{policy: StructuredRepairPolicy{Enabled: true}, maxAttempts: 2, structured: false},
		{policy: StructuredRepairPolicy{Enabled: true, Escalation: &RepairEscalation{Attempt: 1, ModelID: "model"}}, maxAttempts: 2, structured: true},
		{policy: StructuredRepairPolicy{Enabled: true, Escalation: &RepairEscalation{Attempt: 3, ModelID: "model"}}, maxAttempts: 2, structured: true},
		{policy: StructuredRepairPolicy{Enabled: true, Escalation: &RepairEscalation{Attempt: 2}}, maxAttempts: 2, structured: true},
		{policy: StructuredRepairPolicy{Enabled: true, Escalation: &RepairEscalation{Attempt: 2, ModelID: "model", ReasoningEffort: "high"}}, maxAttempts: 2, structured: true},
	}
	for index, testCase := range invalid {
		if _, err := runtimeRepairPolicy(testCase.policy, testCase.maxAttempts, testCase.structured); err == nil {
			t.Fatalf("invalid repair policy %d was accepted", index)
		}
	}
}

func testProfiles() ProfileCatalog {
	return ProfileCatalog{
		"primary": {
			SchemaVersion: 1, LLMProfile: "primary", Provider: "openai",
			APIInferenceType: "responses", EndpointCredentialScope: "global",
			BaseURL: "https://api.openai.com/v1", ModelID: "gpt-test",
			DefaultOptions: map[string]any{}, BackupProfiles: []string{},
		},
	}
}
