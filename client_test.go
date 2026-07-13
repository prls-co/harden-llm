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
			client.newID = func() string {
				id := ids[0]
				ids = ids[1:]
				return id
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
		client.newID = func() string { return "fixed" }
		_, err := client.Call(context.Background(), Request{
			ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "fixture",
			RetryPolicy: RetryPolicy{MaxAttempts: 1},
		})
		if err == nil {
			t.Fatal("Call succeeded, want provider error")
		}
	})
}

func testProfiles() ProfileCatalog {
	return ProfileCatalog{
		"primary": {
			SchemaVersion: 1, LLMProfile: "primary", Provider: "openai",
			APIInferenceType: "openai.responses", EndpointCredentialScope: "global",
			BaseURL: "https://api.openai.com/v1", ModelID: "gpt-test",
		},
	}
}
