package hardenllm

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-006 TEST-220

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
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
	sequence []error
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
	if len(executor.sequence) > 0 {
		err := executor.sequence[0]
		executor.sequence = executor.sequence[1:]
		return executor.result, err
	}
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
				Output:     test.output,
				Accounting: testLedger(12, 0, 0, 3, 0, accounting.ExactCost(0.0000225, "calculated")),
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
				ProfileID:      "primary",
				Profiles:       testProfiles(),
				UserPrompt:     "deterministic fixture",
				CallType:       test.callType,
				Schema:         test.schema,
				CacheMode:      CacheModeOff,
				CacheVersion:   "operation-v2",
				RecoveryPolicy: RecoveryPolicy{MaxAttempts: 1, RetryOn: []RecoveryCategory{"network", "rate_limit", "server_error", "empty_response", "provider_retry"}, Backoff: RecoveryBackoff{}},
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
			if result.Accounting.Result.Usage.TotalTokens != 15 || result.Accounting.Result.Cost.Status != "exact" || len(result.Attempts) != 1 {
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
			CallType:       CallTypeText,
			RecoveryPolicy: RecoveryPolicy{MaxAttempts: 1, RetryOn: []RecoveryCategory{"network", "rate_limit", "server_error", "empty_response", "provider_retry"}, Backoff: RecoveryBackoff{}},
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
			RecoveryPolicy: RecoveryPolicy{MaxAttempts: 1, RetryOn: []RecoveryCategory{"network", "rate_limit", "server_error", "empty_response", "provider_retry"}, Backoff: RecoveryBackoff{}},
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
		{"missing recovery policy", func(catalog ProfileCatalog) {
			profile := catalog["primary"]
			profile.RecoveryPolicy = RecoveryPolicy{}
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

func testProfiles() ProfileCatalog {
	return ProfileCatalog{
		"primary": {
			SchemaVersion: 3, LLMProfile: "primary", Provider: "openai",
			APIInferenceType: "responses", EndpointCredentialScope: "global",
			BaseURL: "https://api.openai.com/v1", ModelID: "gpt-test",
			DefaultOptions: map[string]any{}, RecoveryPolicy: DefaultRecoveryPolicy(),
		},
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-204
func TestRecoveryRepairPayload(t *testing.T) {
	for _, protocol := range []string{"chat-completions", "responses", "gemini-generate-content", "anthropic-messages"} {
		t.Run(protocol, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var requests []map[string]any
			var paths []string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				mu.Lock()
				requests = append(requests, payload)
				paths = append(paths, r.URL.Path)
				number := len(requests)
				mu.Unlock()
				output := `{"zip":42}`
				if number > 1 {
					output = `{"zip":"02139"}`
				}
				var response any
				switch protocol {
				case "chat-completions":
					response = map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": output}, "finish_reason": "stop"}}}
				case "responses":
					response = map[string]any{"output_text": output, "status": "completed"}
				case "gemini-generate-content":
					response = map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": output}}}, "finishReason": "STOP"}}}
				case "anthropic-messages":
					response = map[string]any{"content": []any{map[string]any{"type": "text", "text": output}}, "stop_reason": "end_turn"}
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			endpoint, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client, err := New(Options{
				Credentials: fixedCredentialResolver{},
				EndpointPolicy: EndpointPolicy{
					AllowedHosts: []string{endpoint.Hostname()}, PrivateAllowedHosts: []string{endpoint.Hostname()},
					TLSConfig: server.Client().Transport.(*http.Transport).TLSClientConfig.Clone(),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			catalog := testProfiles()
			profile := catalog["primary"]
			profile.BaseURL = server.URL
			profile.APIInferenceType = protocol
			profile.SupportsContractedStructuredOutput = true
			profile.SupportsTemperature = true
			profile.DefaultOptions = map[string]any{"temperature": 0.3}
			catalog["primary"] = profile
			contract := json.RawMessage(`{"type":"object","properties":{"zip":{"type":"string"}},"required":["zip"],"additionalProperties":false}`)
			result, callErr := client.Call(context.Background(), Request{
				ProfileID: "primary", Profiles: catalog, SystemPrompt: "Preserve string values.", UserPrompt: "Extract the postal code.",
				CallType: CallTypeStructured, Schema: contract, CacheMode: CacheModeOff,
				RecoveryPolicy: generationRepairPolicy(2, []RecoveryCategory{}, RecoveryBackoff{}),
			})
			if callErr != nil || !reflect.DeepEqual(result.Output, map[string]any{"zip": "02139"}) {
				t.Errorf("repair did not preserve direct schema value: output=%#v error=%v", result.Output, callErr)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 2 || len(result.Attempts) != 2 {
				t.Fatalf("provider requests/attempts = %d/%d, want 2/2", len(requests), len(result.Attempts))
			}
			if paths[0] != paths[1] || result.Attempts[0].Target != result.Attempts[1].Target || !result.Attempts[1].Repair {
				t.Errorf("repair changed target or operation: paths=%v attempts=%#v", paths, result.Attempts)
			}
			for index, payload := range requests {
				if !recoveryPayloadHasOriginalSchema(payload) {
					t.Errorf("request %d does not use the original schema: %#v", index+1, payload)
				}
			}
			repairJSON, err := json.Marshal(requests[1])
			if err != nil || !strings.Contains(string(repairJSON), "Extract the postal code.") || !strings.Contains(string(repairJSON), "Preserve string values.") {
				t.Errorf("repair lost the original task: %s/%v", repairJSON, err)
			}
			if !strings.Contains(string(repairJSON), "Validation feedback:") {
				t.Error("repair is missing validation feedback")
			}
			if strings.Contains(string(repairJSON), "non-empty explanation") {
				t.Error("repair still requests an unnecessary metadata envelope")
			}
		})
	}
}

func recoveryPayloadHasOriginalSchema(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if properties, ok := typed["properties"].(map[string]any); ok {
			if _, envelope := properties["repair"]; envelope {
				return false
			}
			if len(properties) == 1 && reflect.DeepEqual(properties["zip"], map[string]any{"type": "string"}) {
				return true
			}
		}
		for _, child := range typed {
			if recoveryPayloadHasOriginalSchema(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if recoveryPayloadHasOriginalSchema(child) {
				return true
			}
		}
	}
	return false
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-202
func TestRecoveryPolicy(t *testing.T) {
	t.Run("complete public field", func(t *testing.T) {
		if _, ok := reflect.TypeOf(Request{}).FieldByName("RecoveryPolicy"); !ok {
			t.Error("Request does not carry the complete recovery policy")
		}
	})
	t.Run("missing policy rejected before execution", func(t *testing.T) {
		client, err := New(Options{Credentials: fixedCredentialResolver{}})
		if err != nil {
			t.Fatal(err)
		}
		executor := &fixedExecutor{result: coreruntime.ProviderResult{Output: "ok"}}
		client.executor = executor
		_, err = client.Call(context.Background(), Request{ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "fixture", CallType: CallTypeText})
		if err == nil || executor.executed != 0 || executor.prepared != 0 {
			t.Fatalf("missing policy did not fail at the boundary: error=%v prepare/execute=%d/%d", err, executor.prepared, executor.executed)
		}
	})
	t.Run("explicit disabled categories stay disabled", func(t *testing.T) {
		client, err := New(Options{Credentials: fixedCredentialResolver{}})
		if err != nil {
			t.Fatal(err)
		}
		failure := &retry.ProviderError{Code: "ECONNRESET"}
		executor := &fixedExecutor{sequence: []error{failure, nil}, result: coreruntime.ProviderResult{Output: "ok"}}
		client.executor = executor
		result, err := client.Call(context.Background(), Request{
			ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "fixture", CallType: CallTypeText,
			RecoveryPolicy: RecoveryPolicy{MaxAttempts: 3, RetryOn: []RecoveryCategory{}, Backoff: RecoveryBackoff{}},
		})
		if !errors.Is(err, failure) || executor.executed != 1 || len(result.Attempts) != 1 {
			t.Fatalf("disabled recovery repeated work: error=%v executions=%d attempts=%#v", err, executor.executed, result.Attempts)
		}
	})
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-202
func TestRecoveryPolicyValues(t *testing.T) {
	t.Parallel()
	defaults := DefaultRecoveryPolicy()
	expected := DefaultStructuredRecoveryPolicy()
	if !reflect.DeepEqual(defaults, expected) {
		t.Fatalf("defaults=%#v", defaults)
	}
	defaults.RetryOn[0] = "changed"
	if !reflect.DeepEqual(DefaultRecoveryPolicy(), expected) {
		t.Fatal("default policies share mutable categories")
	}
	for _, test := range []struct {
		name   string
		change func(*RecoveryPolicy)
		valid  bool
	}{
		{"minimum explicit disabled", func(p *RecoveryPolicy) {
			p.MaxAttempts = 1
			p.RetryOn = []RecoveryCategory{}
			p.JSONRepair = nil
			p.Rerun = nil
			p.Backoff = RecoveryBackoff{}
		}, true},
		{"maximum", func(p *RecoveryPolicy) {
			p.MaxAttempts = 10
			p.Backoff = RecoveryBackoff{BaseDelayMS: 60000, MaxDelayMS: 600000}
		}, true},
		{"missing categories", func(p *RecoveryPolicy) { p.RetryOn = nil }, false},
		{"unknown category", func(p *RecoveryPolicy) { p.RetryOn = []RecoveryCategory{"parse_error"} }, false},
		{"duplicate category", func(p *RecoveryPolicy) { p.RetryOn = []RecoveryCategory{"network", "network"} }, false},
		{"zero attempts", func(p *RecoveryPolicy) { p.MaxAttempts = 0 }, false},
		{"negative attempts", func(p *RecoveryPolicy) { p.MaxAttempts = -1 }, false},
		{"excess attempts", func(p *RecoveryPolicy) { p.MaxAttempts = 11 }, false},
		{"negative base", func(p *RecoveryPolicy) { p.Backoff.BaseDelayMS = -1 }, false},
		{"excess base", func(p *RecoveryPolicy) { p.Backoff = RecoveryBackoff{BaseDelayMS: 60001, MaxDelayMS: 60001} }, false},
		{"inverted backoff", func(p *RecoveryPolicy) { p.Backoff.MaxDelayMS = 499 }, false},
		{"excess cap", func(p *RecoveryPolicy) { p.Backoff.MaxDelayMS = 600001 }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultRecoveryPolicy()
			test.change(&policy)
			executor := &fixedExecutor{result: coreruntime.ProviderResult{Output: "ok"}}
			client, err := New(Options{Credentials: fixedCredentialResolver{}})
			if err != nil {
				t.Fatal(err)
			}
			client.executor = executor
			_, err = client.Call(context.Background(), Request{ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "fixture", CallType: CallTypeText, RecoveryPolicy: policy})
			if (err == nil) != test.valid || (!test.valid && (executor.prepared != 0 || executor.executed != 0)) {
				t.Fatalf("valid=%t prepares/calls=%d/%d error=%v", test.valid, executor.prepared, executor.executed, err)
			}
		})
	}
	for _, raw := range []string{
		"null", `{}`, `{"maxAttempts":4}`,
		`{"maxAttempts":4,"retryOn":null,"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`,
		`{"maxAttempts":4,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0}}`,
		`{"maxAttempts":4,"retryOn":[],"repairInvalidOutput":false,"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`,
		`{"maxAttempts":4,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0},"extra":true}`,
	} {
		var policy RecoveryPolicy
		if err := json.Unmarshal([]byte(raw), &policy); err == nil {
			t.Errorf("incomplete policy accepted: %s", raw)
		}
	}
	for _, key := range []string{"structuredRepairRetry", "enableRetryOn429", "maxAttempts", "retryParse", "repairEscalation", "backupProfiles"} {
		executor := &fixedExecutor{}
		client, err := New(Options{Credentials: fixedCredentialResolver{}})
		if err != nil {
			t.Fatal(err)
		}
		client.executor = executor
		_, err = client.Call(context.Background(), Request{ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "fixture", CallType: CallTypeText, RecoveryPolicy: DefaultRecoveryPolicy(), ProviderOptions: map[string]any{key: false}})
		if err == nil || executor.prepared != 0 || executor.executed != 0 {
			t.Errorf("retired option %s reached provider: %v", key, err)
		}
	}
}

func generationRepairPolicy(maxAttempts int, retryOn []RecoveryCategory, backoff RecoveryBackoff) RecoveryPolicy {
	initial := RecoveryTarget{Source: "generation"}
	escalation := initial
	return RecoveryPolicy{
		MaxAttempts: maxAttempts,
		RetryOn:     retryOn,
		Backoff:     backoff,
		JSONRepair:  &JSONRepairPlan{Initial: initial, Escalation: &escalation},
	}
}
