package runtime

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-008 TEST-009 TEST-214 TEST-215 TEST-244 TEST-245

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

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-226
func TestRecoveryIntegrityAccounting(t *testing.T) {
	t.Parallel()
	profile := Profile{ID: "selected", Provider: "fixture", APIInferenceType: "responses", BaseURL: "https://example.test", ModelID: "selected-model"}
	catalog := map[string]Profile{profile.ID: profile}
	credentials := func(context.Context, Profile) (Credential, error) { return Credential{}, nil }
	measuredUsage, err := accounting.CompleteUsage(2, 0, 0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	measured := Ledger{Usage: measuredUsage, Cost: accounting.ExactCost(0.01, "reported")}
	empty := accounting.EmptyLedger()
	policy := retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{retry.CategoryNetwork}, Backoff: retry.Backoff{}}
	run := func(t *testing.T, executor *accountingSequenceExecutor, call Call, selectedPolicy retry.Policy) (CallRecord, error) {
		t.Helper()
		return Execute(context.Background(), executor, credentials, profile.ID, catalog, call, retry.Config{Policy: selectedPolicy, Wait: func(context.Context, time.Duration) error { return nil }}, nil, cachekey.ModeOff, "operation-v2", "call", "trace")
	}

	t.Run("unknown dispatched work remains partial after measured success", func(t *testing.T) {
		executor := &accountingSequenceExecutor{
			results:  []ProviderResult{{ProviderDispatched: true, Accounting: empty}, {ProviderDispatched: true, Output: "accepted", Accounting: measured}},
			failures: []error{&retry.ProviderError{Category: retry.CategoryNetwork}, nil},
		}
		record, err := run(t, executor, Call{CallType: "text"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		provider := record.Accounting.Provider
		if provider.Usage.Status != accounting.UsagePartial || provider.Usage.TotalTokens() != 3 || provider.Cost.Status != accounting.CostPartial || provider.Cost.KnownSubtotalUSD != 0.01 || provider.Cost.KnownObservations != 1 || provider.Cost.UnknownObservations != 1 {
			t.Fatalf("unknown then measured provider ledger = %#v", provider)
		}
		if record.Accounting.Result != measured {
			t.Fatalf("accepted result ledger changed = %#v", record.Accounting.Result)
		}
	})

	t.Run("measured then unknown has the same aggregate coverage", func(t *testing.T) {
		executor := &accountingSequenceExecutor{
			results:  []ProviderResult{{ProviderDispatched: true, Accounting: measured}, {ProviderDispatched: true, Output: "accepted", Accounting: empty}},
			failures: []error{&retry.ProviderError{Category: retry.CategoryNetwork}, nil},
		}
		record, err := run(t, executor, Call{CallType: "text"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		provider := record.Accounting.Provider
		if provider.Usage.Status != accounting.UsagePartial || provider.Usage.TotalTokens() != 3 || provider.Cost.Status != accounting.CostPartial || provider.Cost.KnownSubtotalUSD != 0.01 || provider.Cost.KnownObservations != 1 || provider.Cost.UnknownObservations != 1 {
			t.Fatalf("measured then unknown provider ledger = %#v", provider)
		}
		if record.Accounting.Result != empty {
			t.Fatalf("result ledger was inferred from prior attempt = %#v", record.Accounting.Result)
		}
	})

	t.Run("all unknown dispatched work is represented without known amounts", func(t *testing.T) {
		executor := &accountingSequenceExecutor{
			results:  []ProviderResult{{ProviderDispatched: true, Accounting: empty}, {ProviderDispatched: true, Accounting: empty}},
			failures: []error{&retry.ProviderError{Category: retry.CategoryNetwork}, &retry.ProviderError{Category: retry.CategoryNetwork}},
		}
		record, err := run(t, executor, Call{CallType: "text"}, policy)
		if err == nil || record.Accounting.Provider.Usage.Status != accounting.UsageUnavailable || record.Accounting.Provider.Cost.Status != accounting.CostUnknown || record.Accounting.Provider.Cost.KnownSubtotalUSD != 0 || record.Accounting.Provider.Cost.KnownObservations != 0 || record.Accounting.Provider.Cost.UnknownObservations != 2 {
			t.Fatalf("all unknown provider ledger=%#v error=%v", record.Accounting.Provider, err)
		}
	})

	t.Run("pre-dispatch failure adds no unknown observation", func(t *testing.T) {
		executor := &accountingSequenceExecutor{
			results:  []ProviderResult{{Accounting: empty}, {ProviderDispatched: true, Output: "accepted", Accounting: measured}},
			failures: []error{&retry.ProviderError{Category: retry.CategoryNetwork}, nil},
		}
		record, err := run(t, executor, Call{CallType: "text"}, policy)
		if err != nil || record.Accounting.Provider != measured {
			t.Fatalf("pre-dispatch provider ledger=%#v error=%v", record.Accounting.Provider, err)
		}
	})

	t.Run("measured zero plus unknown preserves known zero", func(t *testing.T) {
		zeroUsage, usageErr := accounting.CompleteUsage(0, 0, 0, 0, 0)
		if usageErr != nil {
			t.Fatal(usageErr)
		}
		zero := Ledger{Usage: zeroUsage, Cost: accounting.ExactCost(0, "reported")}
		executor := &accountingSequenceExecutor{
			results:  []ProviderResult{{ProviderDispatched: true, Accounting: zero}, {ProviderDispatched: true, Output: "accepted", Accounting: empty}},
			failures: []error{&retry.ProviderError{Category: retry.CategoryNetwork}, nil},
		}
		record, err := run(t, executor, Call{CallType: "text"}, policy)
		if err != nil || record.Accounting.Provider.Usage.Status != accounting.UsagePartial || record.Accounting.Provider.Usage.TotalTokens() != 0 || record.Accounting.Provider.Cost.KnownSubtotalUSD != 0 || record.Accounting.Provider.Cost.KnownObservations != 1 || record.Accounting.Provider.Cost.UnknownObservations != 1 {
			t.Fatalf("zero plus unknown provider ledger=%#v error=%v", record.Accounting.Provider, err)
		}
	})

	t.Run("usage and cost coverage are independent", func(t *testing.T) {
		usageOnly := Ledger{Usage: measuredUsage, Cost: accounting.UnavailableCost()}
		costOnly := Ledger{Usage: accounting.UnavailableUsage(), Cost: accounting.ExactCost(0.02, "reported")}
		executor := &accountingSequenceExecutor{
			results:  []ProviderResult{{ProviderDispatched: true, Accounting: usageOnly}, {ProviderDispatched: true, Output: "accepted", Accounting: costOnly}},
			failures: []error{&retry.ProviderError{Category: retry.CategoryNetwork}, nil},
		}
		record, err := run(t, executor, Call{CallType: "text"}, policy)
		provider := record.Accounting.Provider
		if err != nil || provider.Usage.Status != accounting.UsagePartial || provider.Usage.TotalTokens() != 3 || provider.Cost.Status != accounting.CostPartial || provider.Cost.KnownSubtotalUSD != 0.02 || provider.Cost.KnownObservations != 1 || provider.Cost.UnknownObservations != 1 {
			t.Fatalf("independent coverage provider ledger=%#v error=%v", provider, err)
		}
	})

	t.Run("invalid dimension remains terminal while valid dimension is retained", func(t *testing.T) {
		invalidUsage := Ledger{Usage: Usage{InputTokens: -1, Status: accounting.UsagePartial}, Cost: accounting.ExactCost(0.02, "reported")}
		executor := &accountingSequenceExecutor{results: []ProviderResult{{ProviderDispatched: true, Accounting: invalidUsage}}, failures: []error{nil}}
		record, err := run(t, executor, Call{CallType: "text"}, retry.Policy{MaxAttempts: 1, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}})
		var providerErr *retry.ProviderError
		if err == nil || !errors.As(err, &providerErr) || providerErr.Code != "ACCOUNTING_INVALID" || record.Accounting.Provider.Cost != accounting.ExactCost(0.02, "reported") || record.Output != nil {
			t.Fatalf("invalid dimension record=%#v error=%v", record, err)
		}
	})
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

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-284
func TestProgressSnapshotContracts(t *testing.T) {
	t.Run("simple execution snapshots stream and completed attempt", func(t *testing.T) {
		profile := progressProfile("primary", "primary-model")
		base := time.Now().Truncate(time.Millisecond)
		deadline := base.Add(time.Hour)
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		stream := StreamDiagnostics{ReceivedBytes: 5, EventCount: 2, OutputBytes: 3, OutputCodePoints: 2}
		executor := &progressContractExecutor{outcomes: []progressContractOutcome{
			{result: progressResult("done", stream, 7)},
		}}
		var snapshots []ProgressSnapshot
		call := Call{
			CallType: "text", ReasoningEffort: "middle",
			Context:  ObservabilityContext{RunID: "simple-run"},
			Origin:   Origin{Client: "workspace", OperationID: "simple-operation"},
			Progress: func(snapshot ProgressSnapshot) { snapshots = append(snapshots, snapshot) },
		}
		config := retry.Config{
			Policy: retry.Policy{MaxAttempts: 3, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}},
			Now:    progressClock(base),
		}

		record, err := Execute(ctx, executor, progressCredentials, profile.ID,
			map[string]Profile{profile.ID: profile}, call, config, nil,
			cachekey.ModeOff, "v1", "simple-call", "simple-trace")
		if err != nil || len(record.Attempts) != 1 || executor.executes != 1 {
			t.Fatalf("record=%#v executes=%d error=%v", record, executor.executes, err)
		}

		assertProgressEvents(t, snapshots, "simple-run", "simple-call", "simple-trace", []progressEventExpectation{
			{eventType: "run.started", stage: stageOriginalGenerate, profileID: profile.ID, reasoning: "middle", remaining: 3},
			{eventType: "run.progress", stage: stageOriginalGenerate, profileID: profile.ID, reasoning: "middle", attempt: 1, remaining: 3},
			{eventType: "run.progress", stage: stageOriginalGenerate, profileID: profile.ID, reasoning: "middle", attempt: 1, used: 1, remaining: 2},
			{eventType: "run.terminal", stage: stageOriginalGenerate, profileID: profile.ID, reasoning: "middle", attempt: 1, used: 1, remaining: 2, terminal: true},
		})
		assertProgressCounters(t, snapshots, []progressCounters{
			{},
			{received: 5, events: 2, output: 3, codePoints: 2},
			{received: 5, events: 2, output: 3, codePoints: 2},
			{received: 5, events: 2, output: 3, codePoints: 2},
		})
		assertProgressClockSampling(t, snapshots, deadline)
		if snapshots[0].Attempts != nil || snapshots[0].Accounting != nil || snapshots[1].Accounting != nil {
			t.Fatalf("empty-attempt snapshots must omit accounting: %#v", snapshots[:2])
		}
		if snapshots[2].Accounting == nil || snapshots[2].Accounting.Provider.Usage.InputTokens != 7 ||
			!reflect.DeepEqual(snapshots[2].Origin, call.Origin) || snapshots[2].EffectiveTimeout == nil {
			t.Fatalf("completed snapshot omitted copied accounting or call facts: %#v", snapshots[2])
		}
		if snapshots[2].DeadlineRemainingMs == nil || *snapshots[2].EffectiveTimeout != *record.Diagnostics.EffectiveTimeout {
			t.Fatalf("deadline fields = %#v", snapshots[2])
		}

		recordProfile := record.Attempts[0].ProfileID
		recordInput := record.Accounting.Provider.Usage.InputTokens
		recordTimeout := *record.Diagnostics.EffectiveTimeout
		snapshots[2].Attempts[0].ProfileID = "mutated-snapshot"
		snapshots[2].Accounting.Provider.Usage.InputTokens = 999
		*snapshots[2].EffectiveTimeout = -1
		if record.Attempts[0].ProfileID != recordProfile ||
			record.Accounting.Provider.Usage.InputTokens != recordInput ||
			*record.Diagnostics.EffectiveTimeout != recordTimeout {
			t.Fatal("progress snapshot mutation changed the execution record")
		}
	})

	t.Run("explicit recovery snapshots follow active work identity", func(t *testing.T) {
		profiles := planProfiles()
		base := time.Now().Truncate(time.Millisecond)
		deadline := base.Add(time.Hour)
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		firstStream := StreamDiagnostics{ReceivedBytes: 5, EventCount: 1, OutputBytes: 3, OutputCodePoints: 2}
		secondStream := StreamDiagnostics{ReceivedBytes: 7, EventCount: 2, OutputBytes: 4, OutputCodePoints: 3}
		executor := &progressContractExecutor{outcomes: []progressContractOutcome{
			{result: progressResult(map[string]any{"answer": false}, firstStream, 2)},
			{result: progressResult(map[string]any{"answer": true}, secondStream, 3)},
		}}
		var snapshots []ProgressSnapshot
		call := progressStructuredCall("explicit-run")
		call.Progress = func(snapshot ProgressSnapshot) { snapshots = append(snapshots, snapshot) }
		policy := progressExplicitPolicy()
		config := retry.Config{Policy: policy, Now: progressClock(base)}

		record, err := Execute(ctx, executor, progressCredentials, "original", profiles, call, config,
			nil, cachekey.ModeOff, "v1", "explicit-call", "explicit-trace")
		if err != nil || len(record.Attempts) != 2 || executor.executes != 2 {
			t.Fatalf("record=%#v executes=%d error=%v", record, executor.executes, err)
		}

		assertProgressEvents(t, snapshots, "explicit-run", "explicit-call", "explicit-trace", []progressEventExpectation{
			{eventType: "run.started", stage: stageOriginalGenerate, profileID: "original", reasoning: "highest", remaining: 2},
			{eventType: "run.progress", stage: stageOriginalGenerate, profileID: "original", reasoning: "highest", remaining: 2},
			{eventType: "run.progress", stage: stageOriginalGenerate, profileID: "original", reasoning: "highest", attempt: 1, remaining: 2},
			{eventType: "run.progress", stage: stageOriginalGenerate, profileID: "original", reasoning: "highest", attempt: 1, used: 1, remaining: 1},
			{eventType: "run.progress", stage: stageOriginalRepairInitial, profileID: "repair-l", reasoning: "lowest", attempt: 1, used: 1, remaining: 1},
			{eventType: "run.progress", stage: stageOriginalRepairInitial, profileID: "repair-l", reasoning: "lowest", attempt: 2, used: 1, remaining: 1},
			{eventType: "run.progress", stage: stageOriginalRepairInitial, profileID: "repair-l", reasoning: "lowest", attempt: 2, used: 2},
			{eventType: "run.terminal", stage: stageOriginalRepairInitial, profileID: "repair-l", reasoning: "lowest", attempt: 2, used: 2, terminal: true},
		})
		assertProgressCounters(t, snapshots, []progressCounters{
			{}, {},
			{received: 5, events: 1, output: 3, codePoints: 2},
			{received: 5, events: 1, output: 3, codePoints: 2},
			{received: 5, events: 1, output: 3, codePoints: 2},
			{received: 12, events: 3, output: 7, codePoints: 5},
			{received: 12, events: 3, output: 7, codePoints: 5},
			{received: 12, events: 3, output: 7, codePoints: 5},
		})
		assertProgressClockSampling(t, snapshots, deadline)
		wantAttemptCounts := []int{0, 0, 0, 1, 1, 1, 2, 2}
		for index, snapshot := range snapshots {
			if len(snapshot.Attempts) != wantAttemptCounts[index] {
				t.Errorf("snapshot %d attempts=%d, want %d", index, len(snapshot.Attempts), wantAttemptCounts[index])
			}
		}
		if snapshots[3].Accounting == nil || snapshots[3].Accounting.Provider.Usage.InputTokens != 2 ||
			snapshots[6].Accounting == nil || snapshots[6].Accounting.Provider.Usage.InputTokens != 5 ||
			snapshots[7].StopReason != "succeeded" {
			t.Fatalf("attempt or terminal accounting snapshots = %#v", snapshots[3:])
		}
		originalProfile := record.Attempts[0].ProfileID
		snapshots[3].Attempts[0].ProfileID = "mutated-snapshot"
		if record.Attempts[0].ProfileID != originalProfile {
			t.Fatal("snapshot attempt slice aliases the execution record")
		}
	})

	t.Run("preflight failures keep path-specific event boundaries", func(t *testing.T) {
		profile := progressProfile("primary", "primary-model")
		simpleCtx, cancelSimple := context.WithCancel(context.Background())
		cancelSimple()
		var simpleSnapshots []ProgressSnapshot
		simpleCall := Call{CallType: "text", Progress: func(snapshot ProgressSnapshot) {
			simpleSnapshots = append(simpleSnapshots, snapshot)
		}}
		simpleExecutor := &progressContractExecutor{}
		simpleRecord, simpleErr := Execute(simpleCtx, simpleExecutor, progressCredentials, profile.ID,
			map[string]Profile{profile.ID: profile}, simpleCall,
			retry.Config{Policy: retry.Policy{MaxAttempts: 1, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}}}, nil,
			cachekey.ModeOff, "v1", "simple-expired", "trace")
		if !errors.Is(simpleErr, context.Canceled) || simpleExecutor.prepares != 0 {
			t.Fatalf("simple expired call record=%#v prepares=%d error=%v", simpleRecord, simpleExecutor.prepares, simpleErr)
		}
		if got := progressEventTypes(simpleSnapshots); !reflect.DeepEqual(got, []string{"run.started", "run.terminal"}) {
			t.Fatalf("simple expired events=%v", got)
		}

		explicitCtx, cancelExplicit := context.WithCancel(context.Background())
		cancelExplicit()
		var explicitSnapshots []ProgressSnapshot
		explicitCall := progressStructuredCall("explicit-expired")
		explicitCall.Progress = func(snapshot ProgressSnapshot) {
			explicitSnapshots = append(explicitSnapshots, snapshot)
		}
		explicitExecutor := &progressContractExecutor{}
		explicitRecord, explicitErr := Execute(explicitCtx, explicitExecutor, progressCredentials,
			"original", planProfiles(), explicitCall, retry.Config{Policy: progressExplicitPolicy()}, nil,
			cachekey.ModeOff, "v1", "explicit-expired", "trace")
		if !errors.Is(explicitErr, context.Canceled) || explicitExecutor.prepares != 0 || len(explicitSnapshots) != 0 {
			t.Fatalf("explicit expired call record=%#v prepares=%d snapshots=%#v error=%v", explicitRecord, explicitExecutor.prepares, explicitSnapshots, explicitErr)
		}

		var noWorkSnapshots []ProgressSnapshot
		explicitCall.Progress = func(snapshot ProgressSnapshot) {
			noWorkSnapshots = append(noWorkSnapshots, snapshot)
		}
		_, noWorkErr := Execute(context.Background(), &progressContractExecutor{}, func(context.Context, Profile) (Credential, error) {
			return Credential{}, errors.New("fixture credential unavailable")
		}, "original", planProfiles(), explicitCall, retry.Config{Policy: progressExplicitPolicy()}, nil,
			cachekey.ModeOff, "v1", "explicit-no-work", "trace")
		if noWorkErr == nil || len(noWorkSnapshots) != 0 {
			t.Fatalf("explicit nil-work error=%v snapshots=%#v", noWorkErr, noWorkSnapshots)
		}
	})

	t.Run("terminal failure carries finalized stop reason", func(t *testing.T) {
		profile := progressProfile("primary", "primary-model")
		failure := errors.New("synthetic terminal failure")
		executor := &progressContractExecutor{outcomes: []progressContractOutcome{{err: failure}}}
		var snapshots []ProgressSnapshot
		call := Call{CallType: "text", Progress: func(snapshot ProgressSnapshot) {
			snapshots = append(snapshots, snapshot)
		}}
		record, err := Execute(context.Background(), executor, progressCredentials, profile.ID,
			map[string]Profile{profile.ID: profile}, call,
			retry.Config{Policy: retry.Policy{MaxAttempts: 1, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}}}, nil,
			cachekey.ModeOff, "v1", "failed-call", "failed-trace")
		if !errors.Is(err, failure) || record.StopReason == "" {
			t.Fatalf("record=%#v error=%v", record, err)
		}
		if len(snapshots) == 0 {
			t.Fatal("terminal failure emitted no progress snapshots")
		}
		last := snapshots[len(snapshots)-1]
		if last.Type != "run.terminal" || !last.Terminal || last.StopReason != record.StopReason || last.AttemptsUsed != 1 {
			t.Fatalf("terminal snapshot=%#v, record stop reason=%q", last, record.StopReason)
		}
	})

	t.Run("cache hits emit start and terminal without attempt events", func(t *testing.T) {
		t.Run("simple execution", func(t *testing.T) {
			profile := progressProfile("primary", "primary-model")
			cache := &telemetryCache{}
			call := Call{CallType: "text", UserPrompt: "cache fixture", Context: ObservabilityContext{RunID: "simple-cache"}}
			freshExecutor := &progressContractExecutor{outcomes: []progressContractOutcome{{
				result: progressResult("cached", StreamDiagnostics{}, 1),
			}}}
			config := retry.Config{Policy: retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}}}
			_, err := Execute(context.Background(), freshExecutor, progressCredentials, profile.ID,
				map[string]Profile{profile.ID: profile}, call, config, cache,
				cachekey.ModeCache, "v1", "fresh-call", "trace")
			if err != nil {
				t.Fatal(err)
			}
			cache.found = true
			var snapshots []ProgressSnapshot
			call.Progress = func(snapshot ProgressSnapshot) { snapshots = append(snapshots, snapshot) }
			cacheExecutor := &progressContractExecutor{}
			record, err := Execute(context.Background(), cacheExecutor, progressCredentials, profile.ID,
				map[string]Profile{profile.ID: profile}, call, config, cache,
				cachekey.ModeCache, "v1", "cached-call", "trace")
			if err != nil || !record.Cache.Served || len(record.Attempts) != 0 || cacheExecutor.executes != 0 {
				t.Fatalf("cache record=%#v executes=%d error=%v", record, cacheExecutor.executes, err)
			}
			assertProgressEvents(t, snapshots, "simple-cache", "cached-call", "trace", []progressEventExpectation{
				{eventType: "run.started", stage: stageOriginalGenerate, profileID: profile.ID, remaining: 2},
				{eventType: "run.terminal", stage: stageOriginalGenerate, profileID: profile.ID, remaining: 2, terminal: true},
			})
		})

		t.Run("explicit recovery", func(t *testing.T) {
			profiles := planProfiles()
			cache := &telemetryCache{}
			call := progressStructuredCall("explicit-cache")
			freshExecutor := &progressContractExecutor{outcomes: []progressContractOutcome{{
				result: progressResult(map[string]any{"answer": true}, StreamDiagnostics{}, 1),
			}}}
			config := retry.Config{Policy: progressExplicitPolicy()}
			_, err := Execute(context.Background(), freshExecutor, progressCredentials, "original", profiles,
				call, config, cache, cachekey.ModeCache, "v1", "fresh-call", "trace")
			if err != nil {
				t.Fatal(err)
			}
			cache.found = true
			var snapshots []ProgressSnapshot
			call.Progress = func(snapshot ProgressSnapshot) { snapshots = append(snapshots, snapshot) }
			cacheExecutor := &progressContractExecutor{}
			record, err := Execute(context.Background(), cacheExecutor, progressCredentials, "original", profiles,
				call, config, cache, cachekey.ModeCache, "v1", "cached-call", "trace")
			if err != nil || !record.Cache.Served || len(record.Attempts) != 0 || cacheExecutor.executes != 0 {
				t.Fatalf("cache record=%#v executes=%d error=%v", record, cacheExecutor.executes, err)
			}
			assertProgressEvents(t, snapshots, "explicit-cache", "cached-call", "trace", []progressEventExpectation{
				{eventType: "run.started", stage: stageOriginalGenerate, profileID: "original", reasoning: "highest", remaining: 2},
				{eventType: "run.terminal", stage: stageOriginalGenerate, profileID: "original", reasoning: "highest", remaining: 2, terminal: true},
			})
		})
	})
}

type progressEventExpectation struct {
	eventType string
	stage     string
	profileID string
	reasoning string
	attempt   int
	used      int
	remaining int
	terminal  bool
}

type progressCounters struct {
	received   int64
	events     int64
	output     int64
	codePoints int64
}

type progressContractOutcome struct {
	result ProviderResult
	err    error
}

type progressContractExecutor struct {
	outcomes   []progressContractOutcome
	prepareErr error
	prepares   int
	executes   int
}

func (executor *progressContractExecutor) Prepare(ctx context.Context, profile Profile, credential Credential, call Call) (PreparedOperation, error) {
	executor.prepares++
	if executor.prepareErr != nil {
		return PreparedOperation{}, executor.prepareErr
	}
	prepared, err := (&identityExecutor{}).Prepare(ctx, profile, credential, call)
	if err != nil {
		return PreparedOperation{}, err
	}
	prepared.Opaque = call
	return prepared, nil
}

func (executor *progressContractExecutor) Execute(_ context.Context, operation PreparedOperation) (ProviderResult, error) {
	if executor.executes >= len(executor.outcomes) {
		return ProviderResult{}, errors.New("unexpected progress contract execution")
	}
	outcome := executor.outcomes[executor.executes]
	executor.executes++
	if operation.StreamProgress != nil {
		operation.StreamProgress(outcome.result.Stream)
	}
	return outcome.result, outcome.err
}

func progressProfile(id, model string) Profile {
	return Profile{ID: id, Provider: "fixture", APIInferenceType: "responses", BaseURL: "https://fixture.example", ModelID: model}
}

func progressCredentials(context.Context, Profile) (Credential, error) {
	return Credential{APIKey: "synthetic-progress-key"}, nil
}

func progressResult(output any, stream StreamDiagnostics, inputTokens int64) ProviderResult {
	return ProviderResult{
		ProviderDispatched: true,
		Output:             output,
		Stream:             stream,
		Accounting: Ledger{
			Usage: completeUsageWithoutTest(inputTokens, 0, 0, 1, 0),
			Cost:  accounting.UnavailableCost(),
		},
	}
}

func progressClock(base time.Time) func() time.Time {
	calls := 0
	return func() time.Time {
		calls++
		return base.Add(time.Duration(calls) * time.Millisecond)
	}
}

func progressExplicitPolicy() retry.Policy {
	return retry.Policy{
		MaxAttempts: 2,
		RetryOn:     []retry.Category{},
		Backoff:     retry.Backoff{},
		JSONRepair: &retry.RepairPlan{
			Initial: retry.RecoveryTarget{Source: "profile", ProfileID: "repair-l", ReasoningEffort: "lowest"},
		},
	}
}

func progressStructuredCall(runID string) Call {
	return Call{
		CallType: "structured", Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"boolean"}},"required":["answer"],"additionalProperties":false}`),
		ReasoningEffort: "highest", Context: ObservabilityContext{RunID: runID},
		Origin: Origin{Client: "workspace", OperationID: "progress-operation"},
		ValidateStructured: func(value any) error {
			object, ok := value.(map[string]any)
			if !ok || object["answer"] != true {
				return errors.New("answer must be true")
			}
			return nil
		},
	}
}

func assertProgressEvents(t *testing.T, snapshots []ProgressSnapshot, runID, callID, traceID string, expected []progressEventExpectation) {
	t.Helper()
	if len(snapshots) != len(expected) {
		t.Fatalf("progress event count=%d, want %d: %#v", len(snapshots), len(expected), progressEventTypes(snapshots))
	}
	for index, want := range expected {
		got := snapshots[index]
		if got.Sequence != uint64(index+1) || got.RunID != runID || got.CallID != callID || got.TraceID != traceID ||
			got.Type != want.eventType || got.Stage != want.stage || got.Branch != "original" ||
			got.ProfileID != want.profileID || got.ReasoningEffort != want.reasoning || got.Attempt != want.attempt ||
			got.AttemptsUsed != want.used || got.AttemptsRemaining != want.remaining || got.Terminal != want.terminal {
			t.Errorf("progress snapshot %d = %#v, want %#v", index, got, want)
		}
	}
}

func assertProgressCounters(t *testing.T, snapshots []ProgressSnapshot, expected []progressCounters) {
	t.Helper()
	if len(snapshots) != len(expected) {
		t.Fatalf("counter expectations=%d snapshots=%d", len(expected), len(snapshots))
	}
	for index, want := range expected {
		got := progressCounters{
			received: snapshots[index].ReceivedBytes, events: snapshots[index].EventCount,
			output: snapshots[index].OutputBytes, codePoints: snapshots[index].OutputCodePoints,
		}
		if got != want {
			t.Errorf("snapshot %d counters=%#v, want %#v", index, got, want)
		}
	}
}

func assertProgressClockSampling(t *testing.T, snapshots []ProgressSnapshot, deadline time.Time) {
	t.Helper()
	var startedAt time.Time
	for index, snapshot := range snapshots {
		elapsedSample := snapshot.LastActivity.Add(-time.Millisecond)
		observedStart := elapsedSample.Add(-time.Duration(snapshot.ElapsedMs) * time.Millisecond)
		if index == 0 {
			startedAt = observedStart
		} else if !observedStart.Equal(startedAt) {
			t.Errorf("snapshot %d elapsed sample implies start %s, want %s", index, observedStart, startedAt)
		}
		if snapshot.DeadlineRemainingMs == nil {
			t.Fatalf("snapshot %d omitted deadline remaining", index)
		}
		wantRemaining := deadline.Sub(snapshot.LastActivity).Milliseconds() - 1
		if *snapshot.DeadlineRemainingMs != wantRemaining {
			t.Errorf("snapshot %d deadline remaining=%d, want %d", index, *snapshot.DeadlineRemainingMs, wantRemaining)
		}
	}
}

func progressEventTypes(snapshots []ProgressSnapshot) []string {
	eventTypes := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		eventTypes = append(eventTypes, snapshot.Type)
	}
	return eventTypes
}
