package retry_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-008 TEST-009

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/cachekey"
	. "github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
)

// TEST-008 consumes the source-owned combinatorial retry decision model
// captured from utility-llm's current main commit.
func TestCurrentSourceRetryDecisionMatrixParity(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/parity/source/combinatorial/retry-decision-matrix.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Rows []struct {
			ID          string `json:"id"`
			Outcome     string `json:"outcome"`
			Policy      string `json:"policy"`
			MaxAttempts int    `json:"maxAttempts"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, row := range fixture.Rows {
		row := row
		t.Run(row.ID, func(t *testing.T) {
			policy := DefaultPolicy()
			enabled := row.Policy == "enabled"
			policy.RetryOn = []Category{}
			policy.RepairInvalidOutput = row.Outcome == "parse_error" && enabled
			if enabled && row.Outcome != "parse_error" && row.Outcome != "refusal" {
				policy.RetryOn = []Category{Category(row.Outcome)}
			}

			failure := sourceMatrixError(row.Outcome)
			calls := 0
			policy.MaxAttempts = row.MaxAttempts
			policy.Backoff = Backoff{BaseDelayMS: 500, MaxDelayMS: 8000}
			_, runErr := executeSequence(context.Background(), Config{Policy: policy, Wait: func(context.Context, time.Duration) error { return nil }}, func(context.Context, int) error {
				calls++
				if calls == 1 {
					return failure
				}
				return nil
			})
			canRetry := row.Outcome != "refusal" && enabled && row.MaxAttempts > 1
			if (runErr == nil) != canRetry || calls != map[bool]int{true: 2, false: 1}[canRetry] {
				t.Fatalf("run result=%v calls=%d canRetry=%t", runErr, calls, canRetry)
			}
			if runErr != nil && Classify(runErr, policy).Category != Category(row.Outcome) {
				t.Fatalf("terminal category=%q want %q", Classify(runErr, policy).Category, row.Outcome)
			}
		})
	}
}

func sourceMatrixError(outcome string) error {
	switch outcome {
	case "network":
		return &ProviderError{Code: "ECONNRESET"}
	case "rate_limit":
		return &ProviderError{Status: 429}
	case "server_error":
		return &ProviderError{Status: 503}
	case "empty_response":
		return &ProviderError{Code: "empty_response", Category: CategoryEmpty}
	case "parse_error":
		return &ProviderError{Category: CategoryParse}
	case "refusal":
		return &ProviderError{Category: CategoryRefusal}
	default:
		return &ProviderError{Err: errors.New("unknown source matrix outcome")}
	}
}

func TestRetryClassificationParityCapturedSource(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/parity/generated/retry-classification.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name   string `json:"name"`
			Policy struct {
				ParseError bool `json:"parseError"`
			} `json:"policy"`
			Classification struct {
				Category     Category `json:"category"`
				Retryable    bool     `json:"retryable"`
				Status       *int     `json:"status"`
				RetryAfterMS *int64   `json:"retryAfterMs"`
			} `json:"classification"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range fixture.Cases {
		testCase := testCase
		t.Run(testCase.Name, func(t *testing.T) {
			policy := DefaultPolicy()
			policy.RepairInvalidOutput = testCase.Policy.ParseError
			// ADR-HLLM-020: parse recovery belongs to the runtime repair path.
			if testCase.Classification.Category == CategoryParse {
				testCase.Classification.Retryable = false
			}
			classification := Classify(capturedClassificationError(testCase.Name), policy)
			wantStatus := 0
			if testCase.Classification.Status != nil {
				wantStatus = *testCase.Classification.Status
			}
			wantRetryAfter := time.Duration(0)
			if testCase.Classification.RetryAfterMS != nil {
				wantRetryAfter = time.Duration(*testCase.Classification.RetryAfterMS) * time.Millisecond
			}
			if classification.Category != testCase.Classification.Category ||
				classification.Retryable != testCase.Classification.Retryable ||
				classification.Status != wantStatus || classification.RetryAfter != wantRetryAfter {
				t.Fatalf("classification = %#v, want category=%q retryable=%t status=%d retryAfter=%v", classification, testCase.Classification.Category, testCase.Classification.Retryable, wantStatus, wantRetryAfter)
			}
		})
	}
}

func capturedClassificationError(name string) error {
	switch name {
	case "network":
		return &ProviderError{Err: errors.New("socket hang up"), Code: "ECONNRESET"}
	case "rate-limit":
		return &ProviderError{Err: errors.New("rate limited"), Status: 429, RetryAfter: 2 * time.Second}
	case "server":
		return &ProviderError{Err: errors.New("unavailable"), Status: 503}
	case "refusal":
		return &ProviderError{Err: errors.New("content_filter refusal"), Status: 503, Category: CategoryRefusal}
	case "timeout":
		return context.DeadlineExceeded
	case "parse-disabled", "parse-enabled":
		return &ProviderError{Err: errors.New("invalid JSON"), Category: CategoryParse}
	default:
		return &ProviderError{Err: errors.New("unknown captured classification")}
	}
}

func TestRetryContract(t *testing.T) {
	t.Run("classification", func(t *testing.T) {
		tests := []struct {
			name   string
			err    error
			policy Policy
			want   Classification
		}{
			{name: "network", err: &ProviderError{Code: "ECONNRESET"}, policy: DefaultPolicy(), want: Classification{Retryable: true, Category: CategoryNetwork, Code: "ECONNRESET"}},
			{name: "rate limit", err: &ProviderError{Status: 429, RetryAfter: 2 * time.Second}, policy: DefaultPolicy(), want: Classification{Retryable: true, Category: CategoryRateLimit, Status: 429, RetryAfter: 2 * time.Second}},
			{name: "server", err: &ProviderError{Status: 503}, policy: DefaultPolicy(), want: Classification{Retryable: true, Category: CategoryServer, Status: 503}},
			{name: "provider retry", err: &ProviderError{Err: errors.New("retry this provider request"), Code: "provider_retry", ProviderRequestID: "req_fixture_0001"}, policy: Policy{RetryOn: []Category{CategoryProvider}}, want: Classification{Retryable: true, Category: CategoryProvider, Code: "provider_retry", ProviderRequestID: "req_fixture_0001"}},
			{name: "empty wording without code", err: &ProviderError{Err: errors.New("provider returned an empty or null response")}, policy: DefaultPolicy(), want: Classification{Category: CategoryOther}},
			{name: "parse disabled", err: &ProviderError{Category: CategoryParse}, policy: DefaultPolicy(), want: Classification{Category: CategoryParse}},
			{name: "parse enabled", err: &ProviderError{Category: CategoryParse}, policy: DefaultPolicy(), want: Classification{Category: CategoryParse}},
			{name: "explicit protocol refusal", err: &ProviderError{Status: 503, Category: CategoryRefusal}, policy: DefaultPolicy(), want: Classification{Category: CategoryRefusal, Status: 503}},
			{name: "invalid request", err: &ProviderError{Status: 400}, policy: DefaultPolicy(), want: Classification{Category: CategoryOther, Status: 400}},
			{name: "auth", err: &ProviderError{Status: 401}, policy: DefaultPolicy(), want: Classification{Category: CategoryOther, Status: 401}},
			{name: "timeout", err: context.DeadlineExceeded, policy: DefaultPolicy(), want: Classification{Category: CategoryTimeout}},
			{name: "cancellation", err: context.Canceled, policy: DefaultPolicy(), want: Classification{Category: CategoryCanceled}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				if got := Classify(test.err, test.policy); !reflect.DeepEqual(got, test.want) {
					t.Fatalf("Classify() = %#v, want %#v", got, test.want)
				}
			})
		}
	})

	t.Run("total attempt budget and jitter", func(t *testing.T) {
		var waits []time.Duration
		calls := 0
		random := rand.New(rand.NewSource(12001))
		policy := DefaultPolicy()
		policy.MaxAttempts = 3
		policy.Backoff = Backoff{BaseDelayMS: 500, MaxDelayMS: 8000}
		attempts, err := executeSequence(context.Background(), Config{Policy: policy, Random: random.Float64,
			Wait: func(_ context.Context, duration time.Duration) error {
				waits = append(waits, duration)
				return nil
			},
		}, func(context.Context, int) error {
			calls++
			if calls < 3 {
				return &ProviderError{Status: 503}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if calls != 3 || len(attempts) != 3 {
			t.Fatalf("calls/attempts = %d/%d, want 3/3", calls, len(attempts))
		}
		if !reflect.DeepEqual(waits, []time.Duration{453 * time.Millisecond, 838 * time.Millisecond}) {
			t.Fatalf("waits = %v", waits)
		}
		if attempts[0].Delay != waits[0] || attempts[1].Delay != waits[1] || attempts[2].Category != CategorySuccess {
			t.Fatalf("attempt metadata = %#v", attempts)
		}
	})

	t.Run("retry after remains a server minimum", func(t *testing.T) {
		var wait time.Duration
		calls := 0
		policy := DefaultPolicy()
		policy.MaxAttempts = 2
		policy.Backoff = Backoff{BaseDelayMS: 500, MaxDelayMS: 2000}
		_, err := executeSequence(context.Background(), Config{Policy: policy, Random: func() float64 { return 0.5 },
			Wait: func(_ context.Context, duration time.Duration) error { wait = duration; return nil },
		}, func(context.Context, int) error {
			calls++
			if calls == 1 {
				return &ProviderError{Status: 429, RetryAfter: 10 * time.Second}
			}
			return nil
		})
		if err != nil || wait != 10*time.Second {
			t.Fatalf("err/wait = %v/%v", err, wait)
		}
	})

	t.Run("cancellation before attempt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		policy := DefaultPolicy()
		policy.MaxAttempts = 2
		policy.Backoff = Backoff{BaseDelayMS: 500, MaxDelayMS: 8000}
		_, err := executeSequence(ctx, Config{Policy: policy}, func(context.Context, int) error { calls++; return nil })
		if !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatalf("err/calls = %v/%d", err, calls)
		}
	})

	t.Run("cancellation during wait", func(t *testing.T) {
		calls := 0
		policy := DefaultPolicy()
		policy.MaxAttempts = 2
		policy.Backoff = Backoff{BaseDelayMS: 500, MaxDelayMS: 8000}
		_, err := executeSequence(context.Background(), Config{Policy: policy, Wait: func(context.Context, time.Duration) error { return context.Canceled }}, func(context.Context, int) error { calls++; return &ProviderError{Status: 503} })
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("err/calls = %v/%d", err, calls)
		}
	})

}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-206
func TestRecoveryBackoff(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(fmt.Sprintf("server minimum/%d", status), func(t *testing.T) {
			classification := Classify(&ProviderError{Status: status, RetryAfter: 30 * time.Second}, DefaultPolicy())
			if classification.RetryAfter != 30*time.Second {
				t.Fatalf("server delay lost: %#v", classification)
			}
			if delay := Delay(1, classification.RetryAfter, Backoff{BaseDelayMS: 1000, MaxDelayMS: 2000}, 0.5); delay != 30*time.Second {
				t.Fatalf("server minimum capped: %v", delay)
			}
		})
	}
	if delay := Delay(1, 100*time.Millisecond, Backoff{BaseDelayMS: 1000, MaxDelayMS: 2000}, 0.5); delay != 500*time.Millisecond {
		t.Errorf("server delay replaced longer backoff: %v", delay)
	}
	if got := Classify(&ProviderError{Code: "provider_retry"}, Policy{}); got.Retryable {
		t.Errorf("disabled provider retry is implicit: %#v", got)
	}
	t.Run("zero calculated delay", func(t *testing.T) {
		var delays []time.Duration
		_, err := executeSequence(context.Background(), Config{Policy: Policy{MaxAttempts: 2, RetryOn: []Category{"network"}, RepairInvalidOutput: false, Backoff: Backoff{BaseDelayMS: 0, MaxDelayMS: 0}}, Random: func() float64 { return 0.5 }, Wait: func(_ context.Context, delay time.Duration) error { delays = append(delays, delay); return nil }}, func(_ context.Context, number int) error {
			if number == 1 {
				return &ProviderError{Code: "ECONNRESET"}
			}
			return nil
		})
		if err != nil || !reflect.DeepEqual(delays, []time.Duration{0}) {
			t.Fatalf("zero delay was defaulted: %v/%v", delays, err)
		}
	})
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-213
func TestRecoveryBoundaryClassification(t *testing.T) {
	policy := DefaultPolicy()
	tests := []struct {
		name     string
		err      error
		category Category
		retry    bool
	}{
		{name: "schema field is not refusal", err: &ProviderError{Err: errors.New("refusalReason is an optional schema property"), Category: CategoryOther}, category: CategoryOther},
		{name: "diagnostic text is not network", err: &ProviderError{Err: errors.New("NETWORK_ERROR in provider diagnostic"), Status: 400}, category: CategoryOther},
		{name: "bad request status wins over body", err: &ProviderError{Err: errors.New("server unavailable"), Status: 400}, category: CategoryOther},
		{name: "rate status is retryable", err: &ProviderError{Err: errors.New("NETWORK_ERROR"), Status: 429, RetryAfter: 37 * time.Second}, category: CategoryRateLimit, retry: true},
		{name: "server status is retryable", err: &ProviderError{Err: errors.New("rate limited"), Status: 503}, category: CategoryServer, retry: true},
		{name: "malformed envelope is terminal protocol", err: &ProviderError{Err: errors.New("invalid JSON"), Code: "MALFORMED_RESPONSE", Category: CategoryOther}, category: CategoryOther},
		{name: "semantic parse is distinct", err: &ProviderError{Err: errors.New("invalid extracted model JSON"), Category: CategoryParse}, category: CategoryParse},
		{name: "provider directive is exact code", err: &ProviderError{Err: errors.New("ordinary provider text"), Code: "provider_retry"}, category: CategoryProvider, retry: true},
		{name: "lookalike directive is terminal", err: &ProviderError{Err: errors.New("provider_retry is mentioned in a prompt")}, category: CategoryOther},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classification := Classify(test.err, policy)
			if classification.Category != test.category || classification.Retryable != test.retry {
				t.Fatalf("classification = %#v, want category=%q retryable=%t", classification, test.category, test.retry)
			}
			if test.category == CategoryRateLimit && classification.RetryAfter != 37*time.Second {
				t.Fatalf("retry-after = %v, want 37s", classification.RetryAfter)
			}
		})
	}
	for _, contextError := range []struct {
		name string
		err  error
		want Category
	}{{"parent canceled", context.Canceled, CategoryCanceled}, {"parent deadline", context.DeadlineExceeded, CategoryTimeout}} {
		t.Run(contextError.name, func(t *testing.T) {
			if got := Classify(contextError.err, policy).Category; got != contextError.want {
				t.Fatalf("category = %q, want %q", got, contextError.want)
			}
		})
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-215
func TestRecoveryBoundaryTiming(t *testing.T) {
	backoff := Backoff{BaseDelayMS: 500, MaxDelayMS: 8000}
	tests := []struct {
		attempt int
		random  float64
		want    time.Duration
	}{
		{1, 0, 0}, {1, 0.25, 125 * time.Millisecond}, {1, 0.5, 250 * time.Millisecond}, {1, 0.75, 375 * time.Millisecond}, {1, 1, 500 * time.Millisecond},
		{5, 0, 0}, {5, 0.5, 4 * time.Second}, {5, 1, 8 * time.Second},
		{10, 0.25, 2 * time.Second}, {10, 0.75, 6 * time.Second},
	}
	for _, test := range tests {
		if got := Delay(test.attempt, 0, backoff, test.random); got != test.want {
			t.Errorf("Delay(attempt=%d, random=%v) = %v, want %v", test.attempt, test.random, got, test.want)
		}
	}
	if got := Delay(1, 37*time.Second, backoff, 0); got != 37*time.Second {
		t.Fatalf("Retry-After minimum = %v, want 37s", got)
	}
	if got := Delay(1, -time.Second, Backoff{BaseDelayMS: 0, MaxDelayMS: 0}, 0.5); got != 0 {
		t.Fatalf("negative/zero delay = %v, want 0", got)
	}
	if got := Delay(1, 0, backoff, math.Inf(1)); got != 0 {
		t.Fatalf("non-finite randomness = %v, want 0", got)
	}
}

// A test-owned executor exercises the production loop; it never implements retries.
type sequenceExecutor struct {
	work  func(context.Context, int) error
	calls int
}

func (*sequenceExecutor) Prepare(context.Context, runtime.Profile, runtime.Credential, runtime.Call) (runtime.PreparedOperation, error) {
	return runtime.PreparedOperation{}, nil
}
func (s *sequenceExecutor) Execute(ctx context.Context, _ runtime.PreparedOperation) (runtime.ProviderResult, error) {
	s.calls++
	return runtime.ProviderResult{Output: map[string]any{}}, s.work(ctx, s.calls)
}
func executeSequence(ctx context.Context, config Config, work func(context.Context, int) error) ([]runtime.AttemptRecord, error) {
	record, err := runtime.Execute(ctx, &sequenceExecutor{work: work}, func(context.Context, runtime.Profile) (runtime.Credential, error) { return runtime.Credential{}, nil }, "selected", map[string]runtime.Profile{"selected": {ID: "selected"}}, runtime.Call{CallType: "structured", Schema: []byte(`{"type":"object"}`), ValidateStructured: func(any) error { return nil }}, config, nil, cachekey.ModeOff, "v1", "call", "trace")
	return record.Attempts, err
}
