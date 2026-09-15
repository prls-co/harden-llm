// Package retry owns provider-independent retry classification and budgets.
package retry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
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
	CategoryProvider  Category = "provider_retry"
	CategoryParse     Category = "parse_error"
	CategoryRefusal   Category = "refusal"
	CategoryTimeout   Category = "timeout"
	CategoryCanceled  Category = "canceled"
	CategoryOther     Category = "other"
)

// Policy is the complete provider-independent recovery contract. Runtime never
// replaces explicit values with defaults; callers create policies explicitly.
type Policy struct {
	MaxAttempts         int        `json:"maxAttempts"`
	RetryOn             []Category `json:"retryOn"`
	RepairInvalidOutput bool       `json:"repairInvalidOutput"`
	Backoff             Backoff    `json:"backoff"`
}

type Backoff struct {
	BaseDelayMS int `json:"baseDelayMs"`
	MaxDelayMS  int `json:"maxDelayMs"`
}

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (err *ValidationError) Error() string { return err.Field + ": " + err.Message }

func DefaultPolicy() Policy {
	return Policy{MaxAttempts: DefaultMaxAttempts,
		RetryOn:             []Category{CategoryNetwork, CategoryRateLimit, CategoryServer, CategoryEmpty, CategoryProvider},
		RepairInvalidOutput: true,
		Backoff:             Backoff{BaseDelayMS: int(DefaultBaseDelay.Milliseconds()), MaxDelayMS: int(DefaultMaxDelay.Milliseconds())}}
}

func (policy Policy) Validate() error {
	invalid := func(field, message string) error {
		return &ValidationError{Field: "recoveryPolicy." + field, Message: message}
	}
	if policy.MaxAttempts < 1 || policy.MaxAttempts > 10 {
		return invalid("maxAttempts", "must be an integer from 1 through 10")
	}
	if policy.RetryOn == nil {
		return invalid("retryOn", "must be an explicit array; use [] to disable retries")
	}
	for index, category := range policy.RetryOn {
		switch category {
		case CategoryNetwork, CategoryRateLimit, CategoryServer, CategoryEmpty, CategoryProvider:
		default:
			return invalid("retryOn", "contains an unsupported retry category")
		}
		if slices.Contains(policy.RetryOn[:index], category) {
			return invalid("retryOn", "categories must be unique")
		}
	}
	if policy.Backoff.BaseDelayMS < 0 || policy.Backoff.BaseDelayMS > 60000 {
		return invalid("backoff.baseDelayMs", "must be an integer from 0 through 60000")
	}
	if policy.Backoff.MaxDelayMS < policy.Backoff.BaseDelayMS || policy.Backoff.MaxDelayMS > 600000 {
		return invalid("backoff.maxDelayMs", "must be at least baseDelayMs and at most 600000")
	}
	return nil
}

func (policy Policy) Allows(category Category) bool { return slices.Contains(policy.RetryOn, category) }

func (policy *Policy) UnmarshalJSON(data []byte) error {
	type wire Policy
	var decoded wire
	if err := decodeRequired(data, &decoded, "recoveryPolicy", []string{"maxAttempts", "retryOn", "repairInvalidOutput", "backoff"}); err != nil {
		return err
	}
	result := Policy(decoded)
	if err := result.Validate(); err != nil {
		return err
	}
	*policy = result
	return nil
}

func (backoff *Backoff) UnmarshalJSON(data []byte) error {
	type wire Backoff
	var decoded wire
	if err := decodeRequired(data, &decoded, "recoveryPolicy.backoff", []string{"baseDelayMs", "maxDelayMs"}); err != nil {
		return err
	}
	*backoff = Backoff(decoded)
	return nil
}

func decodeRequired(data []byte, target any, prefix string, fields []string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return &ValidationError{Field: prefix, Message: "must be an object"}
	}
	for _, field := range fields {
		value, exists := object[field]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return &ValidationError{Field: prefix + "." + field, Message: "is required"}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &ValidationError{Field: prefix, Message: "has an unknown field or invalid value type"}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return &ValidationError{Field: prefix, Message: "must contain one object"}
	}
	return nil
}

type Classification struct {
	Retryable         bool
	Category          Category
	Status            int
	RetryAfter        time.Duration
	Code              string
	Type              string
	ProviderRequestID string
}

type ProviderError struct {
	Err               error
	Code              string
	Type              string
	ProviderRequestID string
	RawResponse       string
	Status            int
	RetryAfter        time.Duration
	Parse             bool
	Refusal           bool
	Empty             bool
	Timeout           bool
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
	if err == nil {
		return Classification{Category: CategorySuccess}
	}
	if errors.Is(err, context.Canceled) {
		return Classification{Category: CategoryCanceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Classification{Category: CategoryTimeout}
	}

	var providerError *ProviderError
	if errors.As(err, &providerError) {
		metadata := func(category Category, retryable bool) Classification {
			return Classification{
				Retryable: retryable, Category: category, Status: providerError.Status,
				Code: providerError.Code, Type: providerError.Type, ProviderRequestID: providerError.ProviderRequestID,
			}
		}
		if providerError.Refusal || containsRefusal(providerError.Error()) {
			return metadata(CategoryRefusal, false)
		}
		if providerError.Parse {
			return metadata(CategoryParse, false)
		}
		if providerError.Timeout {
			return metadata(CategoryTimeout, false)
		}
		if isNetworkCode(providerError.Code) || containsNetworkFailure(providerError.Error()) {
			return metadata(CategoryNetwork, policy.Allows(CategoryNetwork))
		}
		if providerError.Status == 429 {
			classification := metadata(CategoryRateLimit, policy.Allows(CategoryRateLimit))
			classification.RetryAfter = nonnegativeDuration(providerError.RetryAfter)
			return classification
		}
		if (providerError.Status >= 500 && providerError.Status <= 599) || isProviderServerError(providerError) {
			classification := metadata(CategoryServer, policy.Allows(CategoryServer))
			if providerError.Status == 503 {
				classification.RetryAfter = nonnegativeDuration(providerError.RetryAfter)
			}
			return classification
		}
		if providerError.Empty || strings.EqualFold(strings.TrimSpace(providerError.Code), "empty_response") {
			return metadata(CategoryEmpty, policy.Allows(CategoryEmpty))
		}
		if strings.EqualFold(strings.TrimSpace(providerError.Code), "provider_retry") {
			return metadata(CategoryProvider, policy.Allows(CategoryProvider))
		}
		return metadata(CategoryOther, false)
	}

	if containsRefusal(errorMessage(err)) {
		return Classification{Category: CategoryRefusal}
	}
	return Classification{Category: CategoryOther}
}

// Config supplies the complete policy and process-local timing dependencies to
// the runtime's single execution loop. Only dependencies have defaults.
type Config struct {
	Policy Policy
	Random func() float64
	Wait   func(context.Context, time.Duration) error
	Now    func() time.Time
}

func Delay(attempt int, retryAfter time.Duration, backoff Backoff, random float64) time.Duration {
	rawMilliseconds := float64(backoff.BaseDelayMS) * math.Pow(2, float64(attempt-1))
	delayMilliseconds := rawMilliseconds + rawMilliseconds*(clampRandom(random)*0.5-0.25)
	delayMilliseconds = min(delayMilliseconds, float64(backoff.MaxDelayMS))
	delay := time.Duration(math.Floor(max(0, delayMilliseconds))) * time.Millisecond
	return max(delay, retryAfter)
}

func Wait(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err()
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
	case "ETIMEDOUT", "ECONNRESET", "ENOTFOUND", "EAI_AGAIN", "EPIPE", "ECONNABORTED", "NETWORK_ERROR", "WEB_SEARCH_NETWORK":
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
	value := strings.ToLower(strings.Join([]string{providerError.Code, providerError.Type, providerError.Error()}, " "))
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

// ValidateProviderOptions keeps recovery controls out of free-form provider
// options. All boundaries use this one list; provider serialization cannot
// silently interpret or discard an old recovery setting.
func (Policy) ValidateProviderOptions(options map[string]any) error {
	for _, key := range []string{"maxAttempts", "maxRetries", "max_retries", "baseDelayMs", "maxDelayMs", "initialBackoffMs", "maximumBackoffMs", "enableRetryOn429", "enableRetryOn5xx", "enableRetryOnNetworkError", "enableRetryOnParseError", "structuredRepairRetry", "structuredRepair", "retryNetwork", "retryRateLimit", "retryServerError", "retryEmpty", "retryParse", "repairEscalation", "backupProfiles", "retryPolicy", "recoveryPolicy"} {
		if _, present := options[key]; present {
			return &ValidationError{Field: "providerOptions." + key, Message: "recovery controls belong in recoveryPolicy"}
		}
	}
	return nil
}
