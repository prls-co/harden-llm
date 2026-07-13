// Package retry owns provider-independent retry classification and budgets.
package retry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"
)

const (
	DefaultMaxAttempts = 4
	DefaultBaseDelay   = 500 * time.Millisecond
	DefaultMaxDelay    = 8 * time.Second
)

type Category string

const (
	CategorySuccess   Category = "success"
	CategoryNetwork   Category = "network"
	CategoryRateLimit Category = "rate_limit"
	CategoryServer    Category = "server_error"
	CategoryEmpty     Category = "empty_response"
	CategoryParse     Category = "parse_error"
	CategoryRefusal   Category = "refusal"
	CategoryTimeout   Category = "timeout"
	CategoryCanceled  Category = "canceled"
	CategoryOther     Category = "other"
)

type Policy struct {
	Network       bool
	RateLimit     bool
	ServerError   bool
	EmptyResponse bool
	ParseError    bool
}

func DefaultPolicy() Policy {
	return Policy{Network: true, RateLimit: true, ServerError: true, EmptyResponse: true}
}

type Classification struct {
	Retryable  bool
	Category   Category
	Status     int
	RetryAfter time.Duration
}

type ProviderError struct {
	Err         error
	Code        string
	RawResponse string
	Status      int
	RetryAfter  time.Duration
	Parse       bool
	Refusal     bool
	Empty       bool
	Timeout     bool
}

func (providerError *ProviderError) Error() string {
	if providerError == nil {
		return "provider error"
	}
	if providerError.Err != nil {
		return providerError.Err.Error()
	}
	if providerError.Code != "" {
		return providerError.Code
	}
	if providerError.Status != 0 {
		return fmt.Sprintf("provider returned HTTP %d", providerError.Status)
	}
	return "provider error"
}

func (providerError *ProviderError) Unwrap() error {
	if providerError == nil {
		return nil
	}
	return providerError.Err
}

func Classify(err error, policy Policy) Classification {
	if errors.Is(err, context.Canceled) {
		return Classification{Category: CategoryCanceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Classification{Category: CategoryTimeout}
	}

	var providerError *ProviderError
	if errors.As(err, &providerError) {
		if providerError.Refusal || containsRefusal(providerError.Error()) {
			return Classification{Category: CategoryRefusal, Status: providerError.Status}
		}
		if providerError.Parse {
			return Classification{Retryable: policy.ParseError, Category: CategoryParse}
		}
		if providerError.Timeout {
			return Classification{Category: CategoryTimeout}
		}
		if isNetworkCode(providerError.Code) || containsNetworkFailure(providerError.Error()) {
			return Classification{Retryable: policy.Network, Category: CategoryNetwork}
		}
		if providerError.Status == 429 {
			return Classification{
				Retryable: policy.RateLimit, Category: CategoryRateLimit, Status: providerError.Status,
				RetryAfter: nonnegativeDuration(providerError.RetryAfter),
			}
		}
		if (providerError.Status >= 500 && providerError.Status <= 599) || isProviderServerError(providerError) {
			return Classification{Retryable: policy.ServerError, Category: CategoryServer, Status: providerError.Status}
		}
		if providerError.Empty || strings.Contains(strings.ToLower(providerError.Error()), "empty or null response") {
			return Classification{Retryable: policy.EmptyResponse, Category: CategoryEmpty, Status: providerError.Status}
		}
		return Classification{Category: CategoryOther, Status: providerError.Status}
	}

	if containsRefusal(errorMessage(err)) {
		return Classification{Category: CategoryRefusal}
	}
	return Classification{Category: CategoryOther}
}

type Config struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Policy      Policy
	Random      func() float64
	Wait        func(context.Context, time.Duration) error
	Hooks       Hooks
}

// Hooks instrument retry execution without changing classification, budgets,
// or wait behavior. A hook must invoke the supplied function exactly once.
type Hooks struct {
	Attempt func(context.Context, int, func(context.Context) error) error
	Wait    func(context.Context, Classification, time.Duration, func(context.Context, time.Duration) error) error
}

type Attempt struct {
	Number      int
	ProfileID   string
	BackupIndex int
	Category    Category
	Status      int
	Retryable   bool
	Delay       time.Duration
	Duration    time.Duration
	Repair      bool
}

func Do(ctx context.Context, config Config, work func(context.Context, int) error) ([]Attempt, error) {
	if ctx == nil {
		return nil, errors.New("retry: context is required")
	}
	if work == nil {
		return nil, errors.New("retry: work function is required")
	}
	config = normalizeConfig(config)
	attempts := make([]Attempt, 0, config.MaxAttempts)
	for number := 1; number <= config.MaxAttempts; number++ {
		if err := ctx.Err(); err != nil {
			return attempts, err
		}
		started := time.Now()
		var err error
		if config.Hooks.Attempt == nil {
			err = work(ctx, number)
		} else {
			err = config.Hooks.Attempt(ctx, number, func(attemptContext context.Context) error {
				return work(attemptContext, number)
			})
		}
		duration := time.Since(started)
		if contextErr := ctx.Err(); contextErr != nil {
			classification := Classify(contextErr, config.Policy)
			attempts = append(attempts, Attempt{
				Number: number, Category: classification.Category, Status: classification.Status, Duration: duration,
			})
			return attempts, contextErr
		}
		if err == nil {
			attempts = append(attempts, Attempt{Number: number, Category: CategorySuccess, Duration: duration})
			return attempts, nil
		}

		classification := Classify(err, config.Policy)
		attempt := Attempt{
			Number: number, Category: classification.Category, Status: classification.Status,
			Retryable: classification.Retryable, Duration: duration,
		}
		attempts = append(attempts, attempt)
		if number >= config.MaxAttempts || !classification.Retryable {
			return attempts, err
		}

		delay := retryDelay(number, classification.RetryAfter, config.BaseDelay, config.MaxDelay, config.Random)
		attempts[len(attempts)-1].Delay = delay
		if config.Hooks.Wait != nil {
			err = config.Hooks.Wait(ctx, classification, delay, config.Wait)
		} else {
			err = config.Wait(ctx, delay)
		}
		if err != nil {
			return attempts, err
		}
	}
	return attempts, errors.New("retry: exhausted attempt loop")
}

func normalizeConfig(config Config) Config {
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = DefaultMaxAttempts
	}
	if config.BaseDelay <= 0 {
		config.BaseDelay = DefaultBaseDelay
	}
	if config.MaxDelay <= 0 {
		config.MaxDelay = DefaultMaxDelay
	}
	if config.Policy == (Policy{}) {
		config.Policy = DefaultPolicy()
	}
	if config.Random == nil {
		config.Random = rand.Float64
	}
	if config.Wait == nil {
		config.Wait = wait
	}
	return config
}

func retryDelay(attempt int, retryAfter, baseDelay, maxDelay time.Duration, random func() float64) time.Duration {
	if retryAfter > 0 {
		return min(retryAfter, maxDelay)
	}
	rawMilliseconds := float64(baseDelay.Milliseconds()) * math.Pow(2, float64(attempt-1))
	delayMilliseconds := rawMilliseconds + rawMilliseconds*(clampRandom(random())*0.5-0.25)
	delayMilliseconds = min(delayMilliseconds, float64(maxDelay.Milliseconds()))
	delayMilliseconds = max(0, delayMilliseconds)
	return time.Duration(math.Floor(delayMilliseconds)) * time.Millisecond
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func clampRandom(value float64) float64 {
	if math.IsNaN(value) || value < 0 {
		return 0
	}
	if value >= 1 {
		return math.Nextafter(1, 0)
	}
	return value
}

func isNetworkCode(code string) bool {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "ETIMEDOUT", "ECONNRESET", "ENOTFOUND", "EAI_AGAIN", "EPIPE", "ECONNABORTED", "NETWORK_ERROR":
		return true
	default:
		return false
	}
}

func containsNetworkFailure(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "premature close") || strings.Contains(message, "socket hang up") || strings.Contains(message, "network timeout")
}

func containsRefusal(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "refusal") || strings.Contains(message, "content filter") || strings.Contains(message, "content_filter") || strings.Contains(message, "safety blocked")
}

func isProviderServerError(providerError *ProviderError) bool {
	value := strings.ToLower(strings.Join([]string{providerError.Code, providerError.Error()}, " "))
	return strings.Contains(value, "server_is_overloaded") || strings.Contains(value, "service_unavailable") || strings.Contains(value, "server is currently overloaded") || strings.Contains(value, "servers are currently overloaded") || strings.Contains(value, "service unavailable")
}

func nonnegativeDuration(value time.Duration) time.Duration {
	return max(value, 0)
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
