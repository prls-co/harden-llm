package retry

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-008 TEST-009

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

func TestRetryContract(t *testing.T) {
	t.Run("classification", func(t *testing.T) {
		tests := []struct {
			name   string
			err    error
			policy Policy
			want   Classification
		}{
			{name: "network", err: &ProviderError{Code: "ECONNRESET"}, policy: DefaultPolicy(), want: Classification{Retryable: true, Category: CategoryNetwork}},
			{name: "rate limit", err: &ProviderError{Status: 429, RetryAfter: 2 * time.Second}, policy: DefaultPolicy(), want: Classification{Retryable: true, Category: CategoryRateLimit, Status: 429, RetryAfter: 2 * time.Second}},
			{name: "server", err: &ProviderError{Status: 503}, policy: DefaultPolicy(), want: Classification{Retryable: true, Category: CategoryServer, Status: 503}},
			{name: "parse disabled", err: &ProviderError{Parse: true}, policy: DefaultPolicy(), want: Classification{Category: CategoryParse}},
			{name: "parse enabled", err: &ProviderError{Parse: true}, policy: Policy{Network: true, RateLimit: true, ServerError: true, EmptyResponse: true, ParseError: true}, want: Classification{Retryable: true, Category: CategoryParse}},
			{name: "refusal", err: &ProviderError{Status: 503, Refusal: true}, policy: DefaultPolicy(), want: Classification{Category: CategoryRefusal, Status: 503}},
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
		attempts, err := Do(context.Background(), Config{
			MaxAttempts: 3,
			BaseDelay:   500 * time.Millisecond,
			MaxDelay:    8 * time.Second,
			Policy:      DefaultPolicy(),
			Random:      random.Float64,
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
		if !reflect.DeepEqual(waits, []time.Duration{601 * time.Millisecond, 1169 * time.Millisecond}) {
			t.Fatalf("waits = %v", waits)
		}
		if attempts[0].Delay != waits[0] || attempts[1].Delay != waits[1] || attempts[2].Category != CategorySuccess {
			t.Fatalf("attempt metadata = %#v", attempts)
		}
	})

	t.Run("retry after capped", func(t *testing.T) {
		var wait time.Duration
		calls := 0
		_, err := Do(context.Background(), Config{
			MaxAttempts: 2, MaxDelay: 2 * time.Second, Policy: DefaultPolicy(), Random: func() float64 { return 0.5 },
			Wait: func(_ context.Context, duration time.Duration) error { wait = duration; return nil },
		}, func(context.Context, int) error {
			calls++
			if calls == 1 {
				return &ProviderError{Status: 429, RetryAfter: 10 * time.Second}
			}
			return nil
		})
		if err != nil || wait != 2*time.Second {
			t.Fatalf("err/wait = %v/%v", err, wait)
		}
	})

	t.Run("cancellation before attempt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		_, err := Do(ctx, Config{MaxAttempts: 2}, func(context.Context, int) error { calls++; return nil })
		if !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatalf("err/calls = %v/%d", err, calls)
		}
	})

	t.Run("cancellation during wait", func(t *testing.T) {
		calls := 0
		_, err := Do(context.Background(), Config{
			MaxAttempts: 2, Policy: DefaultPolicy(),
			Wait: func(context.Context, time.Duration) error { return context.Canceled },
		}, func(context.Context, int) error { calls++; return &ProviderError{Status: 503} })
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("err/calls = %v/%d", err, calls)
		}
	})

	t.Run("instrumentation hooks preserve execution exactly once", func(t *testing.T) {
		type contextKey string
		const key contextKey = "hook"
		attemptHookCalls := 0
		waitHookCalls := 0
		workCalls := 0
		waitCalls := 0
		attempts, err := Do(context.Background(), Config{
			MaxAttempts: 2,
			BaseDelay:   time.Millisecond,
			MaxDelay:    time.Millisecond,
			Policy:      DefaultPolicy(),
			Random:      func() float64 { return 0.5 },
			Wait: func(ctx context.Context, duration time.Duration) error {
				waitCalls++
				if ctx.Value(key) != "wait" || duration != time.Millisecond {
					t.Fatalf("wait context/duration = %v/%v", ctx.Value(key), duration)
				}
				return nil
			},
			Hooks: Hooks{
				Attempt: func(ctx context.Context, number int, work func(context.Context) error) error {
					attemptHookCalls++
					return work(context.WithValue(ctx, key, number))
				},
				Wait: func(ctx context.Context, classification Classification, delay time.Duration, wait func(context.Context, time.Duration) error) error {
					waitHookCalls++
					if classification.Category != CategoryServer || delay != time.Millisecond {
						t.Fatalf("classification/delay = %#v/%v", classification, delay)
					}
					return wait(context.WithValue(ctx, key, "wait"), delay)
				},
			},
		}, func(ctx context.Context, number int) error {
			workCalls++
			if ctx.Value(key) != number {
				t.Fatalf("work context = %v, want %d", ctx.Value(key), number)
			}
			if number == 1 {
				return &ProviderError{Status: 503}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(attempts) != 2 || workCalls != 2 || attemptHookCalls != 2 || waitCalls != 1 || waitHookCalls != 1 {
			t.Fatalf("attempts/work/attempt hooks/waits/wait hooks = %d/%d/%d/%d/%d", len(attempts), workCalls, attemptHookCalls, waitCalls, waitHookCalls)
		}
	})
}
