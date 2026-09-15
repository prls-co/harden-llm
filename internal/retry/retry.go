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
	Category          Category
	Code              string
	Type              string
	ProviderRequestID string
	RawResponse       string
	Status            int
	RetryAfter        time.Duration
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
				RetryAfter: nonnegativeDuration(providerError.RetryAfter),
			}
		}
		// A category assigned by the protocol normalizer is authoritative for a
		// decoded envelope. An unclassified HTTP response is classified from its
		// status below; free-form diagnostics never override either fact.
		if providerError.Category != "" {
			category := providerError.Category
			return metadata(category, policy.Allows(category) && category != CategoryOther && category != CategoryParse && category != CategoryRefusal && category != CategoryTimeout && category != CategoryCanceled)
		}
		if providerError.Status == 429 {
			classification := metadata(CategoryRateLimit, policy.Allows(CategoryRateLimit))
			classification.RetryAfter = nonnegativeDuration(providerError.RetryAfter)
			return classification
		}
		if providerError.Status >= 500 && providerError.Status <= 599 {
			return metadata(CategoryServer, policy.Allows(CategoryServer))
		}
		if providerError.Status != 0 && (providerError.Status < 200 || providerError.Status > 299) {
			return metadata(CategoryOther, false)
		}
		category := categoryForCode(providerError.Code)
		if category == CategoryParse {
			return metadata(CategoryParse, false)
		}
		return metadata(category, policy.Allows(category) && category != CategoryOther && category != CategoryParse && category != CategoryRefusal && category != CategoryTimeout && category != CategoryCanceled)
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
	window := int64(max(backoff.BaseDelayMS, 0))
	maximum := int64(max(backoff.MaxDelayMS, 0))
	if window > maximum {
		window = maximum
	}
	for step := 1; step < max(attempt, 1) && window < maximum; step++ {
		if window > maximum/2 {
			window = maximum
			break
		}
		window *= 2
	}
	jitter := int64(math.Floor(float64(window) * clampRandom(random)))
	delay := time.Duration(jitter) * time.Millisecond
	return max(delay, nonnegativeDuration(retryAfter))
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
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func categoryForCode(code string) Category {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "etimedout", "econnreset", "enotfound", "eai_again", "epipe", "econnaborted", "network_error", "web_search_network", "provider_response_read":
		return CategoryNetwork
	case "empty_response", "web_search_empty":
		return CategoryEmpty
	case "provider_retry":
		return CategoryProvider
	case "server_is_overloaded", "service_unavailable", "server_error":
		return CategoryServer
	case "provider_refusal", "web_search_tool_error":
		return CategoryRefusal
	case "structured_parse":
		return CategoryParse
	case "provider_request_timeout", "web_search_timeout":
		return CategoryTimeout
	default:
		return CategoryOther
	}
}

func nonnegativeDuration(value time.Duration) time.Duration {
	return max(value, 0)
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
