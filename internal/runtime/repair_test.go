package runtime

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-008 TEST-009 TEST-214 TEST-215

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
)

func TestStructuredRepair(t *testing.T) {
	t.Run("execute loop consumes shared attempt budget", func(t *testing.T) {
		executor := &repairSequenceExecutor{}
		contract := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)
		record, err := Execute(
			context.Background(), executor,
			func(context.Context, Profile) (Credential, error) { return Credential{}, nil },
			"primary", map[string]Profile{"primary": {ID: "primary"}},
			Call{
				CallType: "structured", Schema: contract,
				ValidateStructured: func(value any) error {
					object, ok := value.(map[string]any)
					if !ok || object["answer"] != "ok" {
						return errors.New("answer must be ok string")
					}
					return nil
				},
			},
			retry.Config{Policy: retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{"network", "rate_limit", "server_error", "empty_response"}, RepairInvalidOutput: true, Backoff: retry.Backoff{BaseDelayMS: 500, MaxDelayMS: 8000}}, Wait: func(context.Context, time.Duration) error { return nil }},
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
		providerUsage := record.Accounting.Provider.Usage
		if providerUsage.InputTokens != 18 || providerUsage.OutputTokens != 5 || providerUsage.TotalTokens() != 23 {
			t.Fatalf("repair provider usage was not accumulated: %#v", providerUsage)
		}
		if record.Accounting.Result.Usage.TotalTokens() != 13 || record.ResultSource.Kind != ResultSourceProvider || record.ResultSource.AttemptNumber != 2 {
			t.Fatalf("result accounting/source = %#v / %#v", record.Accounting.Result, record.ResultSource)
		}
	})

	t.Run("repair preserves selected profile and credential", func(t *testing.T) {
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
				ValidateStructured: func(value any) error {
					object, ok := value.(map[string]any)
					if !ok || object["answer"] != "ok" {
						return errors.New("answer must be ok string")
					}
					return nil
				},
			},
			retry.Config{Policy: retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{}, RepairInvalidOutput: true, Backoff: retry.Backoff{BaseDelayMS: 500, MaxDelayMS: 8000}}, Wait: func(context.Context, time.Duration) error { return nil }},
			nil, cachekey.ModeOff, "operation-v2", "call", "trace",
		)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(executor.profiles, []string{"primary", "primary"}) {
			t.Fatalf("prepared profiles = %#v", executor.profiles)
		}
		if !reflect.DeepEqual(executor.credentials, []string{"primary-credential", "primary-credential"}) {
			t.Fatalf("prepared credentials = %#v", executor.credentials)
		}
	})

	t.Run("terminal parse failure preserves billable accounting", func(t *testing.T) {
		executor := partialFailureExecutor{}
		record, err := Execute(
			context.Background(), executor,
			func(context.Context, Profile) (Credential, error) { return Credential{}, nil },
			"primary", map[string]Profile{"primary": {ID: "primary"}},
			Call{CallType: "structured", Schema: json.RawMessage(`{"type":"object"}`), ValidateStructured: func(any) error { return nil }},
			retry.Config{Policy: retry.Policy{MaxAttempts: 1, RetryOn: []retry.Category{}, RepairInvalidOutput: true, Backoff: retry.Backoff{BaseDelayMS: 500, MaxDelayMS: 8000}}},
			nil, cachekey.ModeOff, "operation-v2", "call", "trace",
		)
		if err == nil {
			t.Fatal("terminal provider parse failure was accepted")
		}
		if record.Accounting.Provider.Usage != completeUsage(t, 7, 0, 0, 3, 0) {
			t.Fatalf("partial usage was lost: %#v", record.Accounting.Provider.Usage)
		}
		if record.Accounting.Provider.Cost != accounting.ExactCost(0.25, "reported") {
			t.Fatalf("partial cost was lost: %#v", record.Accounting.Provider.Cost)
		}
		if !strings.Contains(string(record.ParseFailureResponse), `"rawResponse":"not-json"`) {
			t.Fatalf("parse failure evidence was lost: %s", record.ParseFailureResponse)
		}
	})
}

func TestExecutionIdentityAndGlobalAttemptBudget(t *testing.T) {
	// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-057
	t.Parallel()

	executor := &identityExecutor{}
	record, err := Execute(
		context.Background(), executor,
		func(context.Context, Profile) (Credential, error) { return Credential{}, nil },
		"primary",
		map[string]Profile{
			"primary": {
				ID: "primary", Provider: "selected-provider", APIInferenceType: "selected-protocol",
				BaseURL: "https://selected.example/v1", ModelID: "selected-model",
			},
			"backup": {
				ID: "backup", Provider: "backup-provider", APIInferenceType: "backup-protocol",
				BaseURL: "https://backup.example/v1", ModelID: "backup-model",
			},
		},
		Call{CallType: "text"},
		retry.Config{Policy: retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{"network"}, RepairInvalidOutput: true, Backoff: retry.Backoff{BaseDelayMS: 500, MaxDelayMS: 8000}}},
		nil, cachekey.ModeOff, "operation-v2", "call", "trace",
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.SelectedTarget.ProfileID != "primary" || record.SelectedTarget.ModelID != "selected-model" {
		t.Fatalf("selected target = %#v", record.SelectedTarget)
	}
	if len(record.Attempts) != 2 || record.Attempts[0].Number != 1 || record.Attempts[1].Number != 2 {
		t.Fatalf("global attempts = %#v", record.Attempts)
	}
	if !record.Attempts[0].ProviderUsed || !record.Attempts[1].ProviderUsed ||
		record.Attempts[0].Target.ModelID != "selected-model" || record.Attempts[1].Target.ModelID != "selected-model" {
		t.Fatalf("attempt targets/lifecycle = %#v", record.Attempts)
	}
	if record.ResultSource.Kind != ResultSourceProvider || record.ResultSource.AttemptNumber != 2 ||
		record.ResultSource.Producer == nil || record.ResultSource.Producer.ProfileID != "primary" || record.ResultSource.Producer.ModelID != "selected-model" {
		t.Fatalf("result source = %#v", record.ResultSource)
	}
	if executor.executes != 2 || record.Accounting.Result.Usage.TotalTokens() != 3 || record.Accounting.Provider.Usage.TotalTokens() != 3 {
		t.Fatalf("execution/accounting = %d %#v", executor.executes, record.Accounting)
	}
}

type identityExecutor struct{ executes int }

func (*identityExecutor) Prepare(_ context.Context, profile Profile, _ Credential, _ Call) (PreparedOperation, error) {
	return PreparedOperation{Operation: cachekey.Operation{
		SchemaVersion: cachekey.OperationSchemaVersion, Protocol: profile.APIInferenceType,
		Endpoint: cachekey.Endpoint{Identity: profile.BaseURL, Method: "POST", Path: "/run"},
		Model:    profile.ModelID, Payload: map[string]any{}, SemanticHeaders: map[string]any{},
		ResponseProjection: cachekey.ResponseProjection{Provider: profile.Provider, Kind: "fixture", Version: "v1"},
	}}, nil
}

func (executor *identityExecutor) Execute(_ context.Context, operation PreparedOperation) (ProviderResult, error) {
	executor.executes++
	if executor.executes == 1 {
		return ProviderResult{ProviderDispatched: true}, &retry.ProviderError{Code: "ECONNRESET", Err: errors.New("connection reset"), Category: retry.CategoryNetwork}
	}
	return ProviderResult{ProviderDispatched: true,
		Output: "selected-result",
		Accounting: Ledger{
			Usage: completeUsageWithoutTest(2, 0, 0, 1, 0), Cost: accounting.ExactCost(0.01, "reported"),
		},
	}, nil
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
		return ProviderResult{ProviderDispatched: true,
			Output:     map[string]any{"answer": "ok"},
			Accounting: Ledger{Usage: completeUsageWithoutTest(10, 0, 0, 3, 0), Cost: accounting.UnavailableCost()},
		}, nil
	}
	return ProviderResult{ProviderDispatched: true,
		Output:     map[string]any{"answer": float64(42)},
		Accounting: Ledger{Usage: completeUsageWithoutTest(8, 0, 0, 2, 0), Cost: accounting.UnavailableCost()},
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
	return ProviderResult{ProviderDispatched: true,
		Accounting: Ledger{
			Usage: completeUsageWithoutTest(7, 0, 0, 3, 0), Cost: accounting.ExactCost(0.25, "reported"),
		},
	}, &retry.ProviderError{Err: errors.New("invalid structured output"), Category: retry.CategoryParse, RawResponse: "not-json"}
}

func completeUsage(t *testing.T, input, cacheRead, cacheCreation, output, reasoning int64) Usage {
	t.Helper()
	usage, err := accounting.CompleteUsage(input, cacheRead, cacheCreation, output, reasoning)
	if err != nil {
		t.Fatal(err)
	}
	return usage
}

func completeUsageWithoutTest(input, cacheRead, cacheCreation, output, reasoning int64) Usage {
	usage, err := accounting.CompleteUsage(input, cacheRead, cacheCreation, output, reasoning)
	if err != nil {
		panic(err)
	}
	return usage
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-205
func TestRecoveryExecution(t *testing.T) {
	t.Run("repair identity survives transient failure", func(t *testing.T) {
		executor := &recoveryExecutor{failures: []error{nil, &retry.ProviderError{Status: 503}, nil}}
		profile := Profile{ID: "primary", Provider: "fixture", APIInferenceType: "responses", BaseURL: "https://example.test", ModelID: "selected-model"}
		record, err := Execute(context.Background(), executor, func(context.Context, Profile) (Credential, error) { return Credential{}, nil },
			profile.ID, map[string]Profile{profile.ID: profile}, Call{
				CallType: "structured", Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`),
				ValidateStructured: func(value any) error {
					if object, ok := value.(map[string]any); !ok || object["answer"] != "ok" {
						return errors.New("answer must be the string ok")
					}
					return nil
				},
			}, retry.Config{Policy: retry.Policy{MaxAttempts: 3, RetryOn: []retry.Category{"server_error"}, RepairInvalidOutput: true, Backoff: retry.Backoff{BaseDelayMS: 500, MaxDelayMS: 8000}}, Wait: func(context.Context, time.Duration) error { return nil }},
			nil, cachekey.ModeOff, "v1", "call", "trace")
		if err != nil || len(record.Attempts) != 3 || executor.prepares != 2 {
			t.Fatalf("recovery=%#v prepares=%d error=%v", record, executor.prepares, err)
		}
		if !record.Attempts[1].Repair || !record.Attempts[2].Repair || !reflect.DeepEqual(executor.dispatched[1].Repair, executor.dispatched[2].Repair) {
			t.Fatalf("repair context/identity reset: attempts=%#v dispatched=%#v", record.Attempts, executor.dispatched)
		}
		for index, attempt := range record.Attempts {
			if attempt.Number != index+1 || attempt.Target.ModelID != "selected-model" || !attempt.ProviderUsed {
				t.Errorf("incorrect dispatched facts: %#v", attempt)
			}
		}
		if record.ResultSource.AttemptNumber != 3 || record.ResultSource.Producer == nil || record.ResultSource.Producer.ModelID != "selected-model" {
			t.Errorf("incorrect producer: %#v", record.ResultSource)
		}
	})
	t.Run("disabled network failure does not choose another target", func(t *testing.T) {
		executor := &recoveryExecutor{failures: []error{&retry.ProviderError{Code: "ECONNRESET"}, nil}}
		record, err := Execute(context.Background(), executor, func(context.Context, Profile) (Credential, error) { return Credential{}, nil }, "primary",
			map[string]Profile{"primary": {ID: "primary", ModelID: "selected-model"}, "other": {ID: "other", ModelID: "other-model"}},
			Call{CallType: "text"}, retry.Config{Policy: retry.Policy{MaxAttempts: 3, RetryOn: []retry.Category{}, RepairInvalidOutput: true, Backoff: retry.Backoff{BaseDelayMS: 500, MaxDelayMS: 8000}}, Wait: func(context.Context, time.Duration) error { return nil }}, nil, cachekey.ModeOff, "v1", "call", "trace")
		if err == nil || len(executor.dispatched) != 1 || len(record.Attempts) != 1 {
			t.Fatalf("disabled recovery routed elsewhere: record=%#v dispatched=%d error=%v", record, len(executor.dispatched), err)
		}
	})
	t.Run("server minimum cannot fit deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		executor := &recoveryExecutor{failures: []error{&retry.ProviderError{Status: 503, RetryAfter: time.Hour}, nil}}
		waits := 0
		record, err := Execute(ctx, executor, func(context.Context, Profile) (Credential, error) { return Credential{}, nil }, "primary", map[string]Profile{"primary": {ID: "primary"}}, Call{CallType: "text"},
			retry.Config{Policy: retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{"server_error"}, RepairInvalidOutput: false, Backoff: retry.Backoff{BaseDelayMS: 1, MaxDelayMS: 1}}, Wait: func(context.Context, time.Duration) error { waits++; return nil }}, nil, cachekey.ModeOff, "v1", "call", "trace")
		if !errors.Is(err, context.DeadlineExceeded) || len(record.Attempts) != 1 || waits != 0 {
			t.Fatalf("server deadline exceeded: record=%#v waits=%d error=%v", record, waits, err)
		}
	})
}

type recoveryExecutor struct {
	prepares   int
	dispatched []Call
	failures   []error
}

func (executor *recoveryExecutor) Prepare(_ context.Context, profile Profile, _ Credential, call Call) (PreparedOperation, error) {
	executor.prepares++
	return PreparedOperation{Operation: cachekey.Operation{SchemaVersion: cachekey.OperationSchemaVersion, Protocol: profile.APIInferenceType, Endpoint: cachekey.Endpoint{Identity: profile.BaseURL, Method: "POST", Path: "/run"}, Model: profile.ModelID, Payload: map[string]any{}, SemanticHeaders: map[string]any{}, ResponseProjection: cachekey.ResponseProjection{Provider: profile.Provider, Kind: "fixture", Version: "v1"}}, Opaque: call}, nil
}
func (executor *recoveryExecutor) Execute(_ context.Context, operation PreparedOperation) (ProviderResult, error) {
	call := operation.Opaque.(Call)
	executor.dispatched = append(executor.dispatched, call)
	index := len(executor.dispatched) - 1
	if index < len(executor.failures) && executor.failures[index] != nil {
		return ProviderResult{ProviderDispatched: true}, executor.failures[index]
	}
	if call.CallType == "structured" && call.Repair == nil {
		return ProviderResult{ProviderDispatched: true, Output: map[string]any{"answer": 42}}, nil
	}
	return ProviderResult{ProviderDispatched: true, Output: map[string]any{"answer": "ok"}}, nil
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-205
func TestRecoveryExecutionBounds(t *testing.T) {
	profile := Profile{ID: "selected", Provider: "fixture", APIInferenceType: "responses", BaseURL: "https://example.test", ModelID: "selected-model"}
	catalog := map[string]Profile{profile.ID: profile}
	credentials := func(context.Context, Profile) (Credential, error) { return Credential{}, nil }
	for _, budget := range []int{1, 2, 10} {
		t.Run(fmt.Sprintf("repeated invalid repairs budget %d", budget), func(t *testing.T) {
			executor := &recoveryExecutor{}
			policy := retry.DefaultPolicy()
			policy.MaxAttempts = budget
			policy.Backoff = retry.Backoff{}
			record, err := Execute(context.Background(), executor, credentials, profile.ID, catalog, Call{CallType: "structured", Schema: []byte(`{"type":"object"}`), ValidateStructured: func(any) error { return errors.New("still invalid") }}, retry.Config{Policy: policy, Wait: func(context.Context, time.Duration) error { return nil }}, nil, cachekey.ModeOff, "v1", "call", "trace")
			if err == nil || len(record.Attempts) != budget || len(executor.dispatched) != budget || executor.prepares != budget {
				t.Fatalf("budget=%d record=%#v prepares=%d calls=%d error=%v", budget, record, executor.prepares, len(executor.dispatched), err)
			}
			if record.Output != nil || record.ResultSource.Kind != ResultSourceNone {
				t.Fatalf("invalid output exposed: %#v", record)
			}
			for i, a := range record.Attempts {
				if a.Number != i+1 || a.Repair != (i > 0) || a.Target.ModelID != profile.ModelID {
					t.Fatalf("wrong attempt facts: %#v", a)
				}
			}
		})
	}
	t.Run("success and cache hit", func(t *testing.T) {
		executor := &recoveryExecutor{}
		cache := &telemetryCache{}
		call := Call{CallType: "text", WebSearch: true}
		config := retry.Config{Policy: retry.DefaultPolicy()}
		fresh, err := Execute(context.Background(), executor, credentials, profile.ID, catalog, call, config, cache, cachekey.ModeCache, "v1", "fresh", "trace")
		if err != nil || len(fresh.Attempts) != 1 || !fresh.Cache.Written {
			t.Fatalf("fresh record=%#v error=%v", fresh, err)
		}
		cache.found = true
		cached, err := Execute(context.Background(), executor, credentials, profile.ID, catalog, call, config, cache, cachekey.ModeCache, "v1", "cached", "trace")
		if err != nil || len(cached.Attempts) != 0 || len(executor.dispatched) != 1 || !cached.Cache.Served || cached.ResultSource.Kind != ResultSourceCache || !reflect.DeepEqual(cached.ResultSource.Producer, fresh.ResultSource.Producer) {
			t.Fatalf("cache record=%#v calls=%d error=%v", cached, len(executor.dispatched), err)
		}
		if !reflect.DeepEqual(cached.Accounting.Provider, accounting.EmptyLedger()) {
			t.Fatalf("cache hit billed provider: %#v", cached.Accounting)
		}
	})
	t.Run("canceled before preparation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		executor := &recoveryExecutor{}
		record, err := Execute(ctx, executor, credentials, profile.ID, catalog, Call{CallType: "text"}, retry.Config{Policy: retry.DefaultPolicy()}, nil, cachekey.ModeOff, "v1", "call", "trace")
		if !errors.Is(err, context.Canceled) || executor.prepares != 0 || len(executor.dispatched) != 0 || len(record.Attempts) != 0 {
			t.Fatalf("canceled call=%#v prepares=%d error=%v", record, executor.prepares, err)
		}
	})
	t.Run("cancellation during wait prevents next call", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		executor := &recoveryExecutor{failures: []error{&retry.ProviderError{Status: 503}}}
		record, err := Execute(ctx, executor, credentials, profile.ID, catalog, Call{CallType: "text"}, retry.Config{Policy: retry.DefaultPolicy(), Wait: func(context.Context, time.Duration) error { cancel(); return nil }}, nil, cachekey.ModeOff, "v1", "call", "trace")
		if !errors.Is(err, context.Canceled) || len(executor.dispatched) != 1 || len(record.Attempts) != 1 {
			t.Fatalf("post-wait cancellation=%#v error=%v", record, err)
		}
	})
	t.Run("credential failure invokes no provider", func(t *testing.T) {
		executor := &recoveryExecutor{}
		record, err := Execute(context.Background(), executor, func(context.Context, Profile) (Credential, error) {
			return Credential{}, errors.New("missing fixture credential")
		}, profile.ID, catalog, Call{CallType: "text"}, retry.Config{Policy: retry.DefaultPolicy()}, nil, cachekey.ModeOff, "v1", "call", "trace")
		if err == nil || executor.prepares != 0 || len(executor.dispatched) != 0 || len(record.Attempts) != 0 {
			t.Fatalf("prerequisite record=%#v error=%v", record, err)
		}
	})
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-217
func TestRecoveryBoundaryAccountingCache(t *testing.T) {
	t.Parallel()
	profile := Profile{ID: "selected", Provider: "fixture", APIInferenceType: "responses", BaseURL: "https://example.test", ModelID: "selected-model"}
	catalog := map[string]Profile{profile.ID: profile}
	credentials := func(context.Context, Profile) (Credential, error) { return Credential{}, nil }
	policy := retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{retry.CategoryNetwork}, Backoff: retry.Backoff{}}
	cache := &telemetryCache{}
	executor := &accountingSequenceExecutor{results: []ProviderResult{
		{ProviderDispatched: true, Accounting: Ledger{Usage: completeUsageWithoutTest(11, 0, 0, 3, 0), Cost: accounting.ExactCost(0.1, "reported")}},
		{ProviderDispatched: true, Output: "accepted", Accounting: Ledger{Usage: completeUsageWithoutTest(17, 0, 0, 5, 0), Cost: accounting.ExactCost(0.2, "reported")}},
	}, failures: []error{&retry.ProviderError{Category: retry.CategoryNetwork, Code: "ECONNRESET"}, nil}}
	record, err := Execute(context.Background(), executor, credentials, profile.ID, catalog, Call{CallType: "text"}, retry.Config{Policy: policy, Wait: func(context.Context, time.Duration) error { return nil }}, cache, cachekey.ModeCache, "operation-v2", "call", "trace")
	if err != nil {
		t.Fatal(err)
	}
	if record.Accounting.Provider.Usage.InputTokens != 28 || record.Accounting.Provider.Usage.OutputTokens != 8 || record.Accounting.Provider.Cost.Status != accounting.CostExact || record.Accounting.Provider.Cost.KnownSubtotalUSD != 0.3 {
		t.Fatalf("provider ledger totals = %#v", record.Accounting.Provider)
	}
	if record.Accounting.Result.Usage.InputTokens != 17 || record.Accounting.Result.Usage.OutputTokens != 5 || record.Accounting.Result.Cost != accounting.ExactCost(0.2, "reported") || !record.Cache.Written {
		t.Fatalf("result ledger/cache = %#v / %#v", record.Accounting.Result, record.Cache)
	}

	cache.found = true
	hitExecutor := &accountingSequenceExecutor{}
	hit, err := Execute(context.Background(), hitExecutor, credentials, profile.ID, catalog, Call{CallType: "text"}, retry.Config{Policy: policy}, cache, cachekey.ModeCache, "operation-v2", "hit", "trace")
	if err != nil || !hit.Cache.Served || hitExecutor.executes != 0 || hit.Accounting.Provider.Usage.Status != accounting.UsageUnavailable {
		t.Fatalf("cache replay = %#v executes=%d error=%v", hit, hitExecutor.executes, err)
	}

	invalid := &accountingSequenceExecutor{results: []ProviderResult{
		{ProviderDispatched: true, Accounting: Ledger{Usage: completeUsageWithoutTest(11, 0, 0, 3, 0), Cost: accounting.ExactCost(0.1, "reported")}},
		{ProviderDispatched: true, Output: "must not publish", Accounting: Ledger{Usage: Usage{InputTokens: -1, Status: accounting.UsagePartial}, Cost: accounting.UnavailableCost()}},
	}, failures: []error{&retry.ProviderError{Category: retry.CategoryNetwork}, nil}}
	failed, err := Execute(context.Background(), invalid, credentials, profile.ID, catalog, Call{CallType: "text"}, retry.Config{Policy: policy, Wait: func(context.Context, time.Duration) error { return nil }}, nil, cachekey.ModeOff, "operation-v2", "invalid", "trace")
	var providerErr *retry.ProviderError
	if err == nil || !errors.As(err, &providerErr) || providerErr.Code != "ACCOUNTING_INVALID" || failed.Accounting.Provider.Usage.InputTokens != 11 || failed.Output != nil {
		t.Fatalf("invalid accounting transition = %#v / %v", failed, err)
	}
}

type accountingSequenceExecutor struct {
	results  []ProviderResult
	failures []error
	executes int
}

func (executor *accountingSequenceExecutor) Prepare(_ context.Context, profile Profile, _ Credential, _ Call) (PreparedOperation, error) {
	return PreparedOperation{Operation: cachekey.Operation{
		SchemaVersion: cachekey.OperationSchemaVersion, Protocol: profile.APIInferenceType,
		Endpoint: cachekey.Endpoint{Identity: profile.BaseURL, Method: "POST", Path: "/run"}, Model: profile.ModelID,
		Payload: map[string]any{}, SemanticHeaders: map[string]any{}, ResponseProjection: cachekey.ResponseProjection{Provider: profile.Provider, Kind: "fixture", Version: "v3"},
	}}, nil
}

func (executor *accountingSequenceExecutor) Execute(context.Context, PreparedOperation) (ProviderResult, error) {
	index := executor.executes
	executor.executes++
	var result ProviderResult
	if index < len(executor.results) {
		result = executor.results[index]
	}
	var err error
	if index < len(executor.failures) {
		err = executor.failures[index]
	}
	return result, err
}
