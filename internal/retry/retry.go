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
	MaxAttempts  int         `json:"maxAttempts"`
	RetryOn      []Category  `json:"retryOn"`
	Backoff      Backoff     `json:"backoff"`
	JSONRepair   *RepairPlan `json:"jsonRepair,omitempty"`
	Rerun        *RerunPlan  `json:"rerun,omitempty"`
	explicitPlan bool
}

// UsesExplicitPlan reports whether the policy was decoded or constructed with
// the structured recovery shape. It is intentionally distinct from the
// pointers: a JSON document may explicitly disable both branches with null.
func (policy Policy) UsesExplicitPlan() bool {
	return policy.explicitPlan || policy.JSONRepair != nil || policy.Rerun != nil
}

// RecoveryTarget identifies one leaf provider target. A target never contains
// another recovery policy; saved profile policies are intentionally not walked
// when the profile is selected for a recovery stage.
type RecoveryTarget struct {
	Source          string         `json:"source"`
	ProfileID       string         `json:"profileId,omitempty"`
	ModelID         string         `json:"modelId,omitempty"`
	ReasoningEffort string         `json:"reasoningEffort,omitempty"`
	ProviderOptions map[string]any `json:"providerOptions,omitempty"`
}

// RepairPlan is shared by the original and rerun generation branches.
type RepairPlan struct {
	Initial    RecoveryTarget  `json:"initial"`
	Escalation *RecoveryTarget `json:"escalation"`
}

// RerunPlan starts a fresh generation from the original request, then uses the
// same RepairPlan shape for that branch's invalid structured output.
type RerunPlan struct {
	Target     RecoveryTarget `json:"target"`
	JSONRepair *RepairPlan    `json:"jsonRepair"`
}

func (target *RecoveryTarget) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return &ValidationError{Field: "recoveryTarget", Message: "must be an object"}
	}
	if _, ok := fields["source"]; !ok {
		return &ValidationError{Field: "recoveryTarget.source", Message: "is required"}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value struct {
		Source          string         `json:"source"`
		ProfileID       string         `json:"profileId"`
		ModelID         string         `json:"modelId"`
		ReasoningEffort string         `json:"reasoningEffort"`
		ProviderOptions map[string]any `json:"providerOptions"`
	}
	if err := decoder.Decode(&value); err != nil {
		return &ValidationError{Field: "recoveryTarget", Message: "has an unknown field or invalid value type"}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return &ValidationError{Field: "recoveryTarget", Message: "must contain one object"}
	}
	*target = RecoveryTarget{Source: value.Source, ProfileID: value.ProfileID, ModelID: value.ModelID, ReasoningEffort: value.ReasoningEffort, ProviderOptions: value.ProviderOptions}
	return nil
}

func (plan *RepairPlan) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return &ValidationError{Field: "repairPlan", Message: "must be an object"}
	}
	for _, key := range []string{"initial", "escalation"} {
		if _, ok := fields[key]; !ok {
			return &ValidationError{Field: "repairPlan." + key, Message: "is required"}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value struct {
		Initial    RecoveryTarget  `json:"initial"`
		Escalation *RecoveryTarget `json:"escalation"`
	}
	if err := decoder.Decode(&value); err != nil {
		return &ValidationError{Field: "repairPlan", Message: "has an unknown field or invalid value type"}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return &ValidationError{Field: "repairPlan", Message: "must contain one object"}
	}
	*plan = RepairPlan{Initial: value.Initial, Escalation: value.Escalation}
	return nil
}

func (plan *RerunPlan) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return &ValidationError{Field: "rerun", Message: "must be an object or null"}
	}
	for _, key := range []string{"target", "jsonRepair"} {
		if _, ok := fields[key]; !ok {
			return &ValidationError{Field: "rerun." + key, Message: "is required"}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value struct {
		Target     RecoveryTarget `json:"target"`
		JSONRepair *RepairPlan    `json:"jsonRepair"`
	}
	if err := decoder.Decode(&value); err != nil {
		return &ValidationError{Field: "rerun", Message: "has an unknown field or invalid value type"}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return &ValidationError{Field: "rerun", Message: "must contain one object"}
	}
	*plan = RerunPlan{Target: value.Target, JSONRepair: value.JSONRepair}
	return nil
}

func (target RecoveryTarget) IsZero() bool {
	return target.Source == "" && target.ProfileID == "" && target.ModelID == "" && target.ReasoningEffort == "" && len(target.ProviderOptions) == 0
}

func (target RecoveryTarget) Validate(field string, allowGeneration bool) error {
	invalid := func(message string) error { return &ValidationError{Field: field, Message: message} }
	if target.Source != "profile" && !(allowGeneration && target.Source == "generation") {
		return invalid("source must be profile or generation")
	}
	if target.Source == "profile" && strings.TrimSpace(target.ProfileID) == "" {
		return invalid("profileId is required for a profile target")
	}
	if target.Source == "profile" && target.ModelID != "" && strings.TrimSpace(target.ModelID) == "" {
		return invalid("modelId must not be blank")
	}
	if target.Source == "generation" && (target.ProfileID != "" || target.ModelID != "" || target.ReasoningEffort != "" || len(target.ProviderOptions) != 0) {
		return invalid("generation target cannot override profile settings")
	}
	if len(target.ProfileID) > 1500 || len(target.ModelID) > 512 || len(target.ReasoningEffort) > 32 {
		return invalid("target identifiers exceed their limits")
	}
	if target.ReasoningEffort != "" && target.ReasoningEffort != "lowest" && target.ReasoningEffort != "middle" && target.ReasoningEffort != "highest" {
		return invalid("reasoningEffort must be lowest, middle, or highest")
	}
	if target.ProviderOptions != nil {
		if err := (Policy{}).ValidateProviderOptions(target.ProviderOptions); err != nil {
			return invalid(err.Error())
		}
		if err := validateTargetProviderOptions(target.ProviderOptions); err != nil {
			return invalid(err.Error())
		}
	}
	return nil
}

func validateTargetProviderOptions(options map[string]any) error {
	encoded, err := json.Marshal(options)
	if err != nil {
		return errors.New("providerOptions must contain JSON values only")
	}
	if len(encoded) > 32<<10 {
		return errors.New("providerOptions exceeds the 32 KiB limit")
	}
	if containsRecoverySecretKey(options) {
		return errors.New("providerOptions may not contain credential material")
	}
	return nil
}

func containsRecoverySecretKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", ""), "_", ""))
			for _, prefix := range []string{"authorization", "apikey", "credential", "password", "secret"} {
				if strings.HasPrefix(normalized, prefix) {
					return true
				}
			}
			switch normalized {
			case "token", "accesstoken", "authtoken", "bearertoken", "clienttoken", "idtoken", "refreshtoken", "sessiontoken":
				return true
			}
			if containsRecoverySecretKey(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsRecoverySecretKey(nested) {
				return true
			}
		}
	}
	return false
}

func (plan RepairPlan) Validate(field string) error {
	if err := plan.Initial.Validate(field+".initial", true); err != nil {
		return err
	}
	if plan.Escalation != nil {
		if err := plan.Escalation.Validate(field+".escalation", true); err != nil {
			return err
		}
	}
	return nil
}

func (plan RerunPlan) Validate(field string) error {
	if err := plan.Target.Validate(field+".target", false); err != nil {
		return err
	}
	if plan.Target.Source != "profile" {
		return &ValidationError{Field: field + ".target.source", Message: "rerun target must be a profile target"}
	}
	if plan.JSONRepair != nil {
		if err := plan.JSONRepair.Validate(field + ".jsonRepair"); err != nil {
			return err
		}
	}
	return nil
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
	generation := RecoveryTarget{Source: "generation"}
	escalation := generation
	return Policy{MaxAttempts: DefaultMaxAttempts,
		RetryOn:      []Category{CategoryNetwork, CategoryRateLimit, CategoryServer, CategoryEmpty, CategoryProvider},
		Backoff:      Backoff{BaseDelayMS: int(DefaultBaseDelay.Milliseconds()), MaxDelayMS: int(DefaultMaxDelay.Milliseconds())},
		JSONRepair:   &RepairPlan{Initial: generation, Escalation: &escalation},
		explicitPlan: true,
	}
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
	if policy.JSONRepair != nil {
		if err := policy.JSONRepair.Validate("recoveryPolicy.jsonRepair"); err != nil {
			return err
		}
	}
	if policy.Rerun != nil {
		if err := policy.Rerun.Validate("recoveryPolicy.rerun"); err != nil {
			return err
		}
	}
	return nil
}

func (policy Policy) Allows(category Category) bool { return slices.Contains(policy.RetryOn, category) }

func (policy *Policy) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return &ValidationError{Field: "recoveryPolicy", Message: "must be an object"}
	}
	for _, required := range []string{"maxAttempts", "retryOn", "jsonRepair", "rerun", "backoff"} {
		value, ok := fields[required]
		if !ok || ((required != "jsonRepair" && required != "rerun") && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return &ValidationError{Field: "recoveryPolicy." + required, Message: "is required"}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded struct {
		MaxAttempts int         `json:"maxAttempts"`
		RetryOn     []Category  `json:"retryOn"`
		Backoff     Backoff     `json:"backoff"`
		JSONRepair  *RepairPlan `json:"jsonRepair"`
		Rerun       *RerunPlan  `json:"rerun"`
	}
	if err := decoder.Decode(&decoded); err != nil {
		return &ValidationError{Field: "recoveryPolicy", Message: "has an unknown field or invalid value type"}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return &ValidationError{Field: "recoveryPolicy", Message: "must contain one object"}
	}
	result := Policy{MaxAttempts: decoded.MaxAttempts, RetryOn: decoded.RetryOn, Backoff: decoded.Backoff, JSONRepair: decoded.JSONRepair, Rerun: decoded.Rerun, explicitPlan: true}
	if err := result.Validate(); err != nil {
		return err
	}
	*policy = result
	return nil
}

// MarshalJSON always writes the current explicit recovery shape.
func (policy Policy) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		MaxAttempts int         `json:"maxAttempts"`
		RetryOn     []Category  `json:"retryOn"`
		Backoff     Backoff     `json:"backoff"`
		JSONRepair  *RepairPlan `json:"jsonRepair"`
		Rerun       *RerunPlan  `json:"rerun"`
	}{
		MaxAttempts: policy.MaxAttempts,
		RetryOn:     policy.RetryOn,
		Backoff:     policy.Backoff,
		JSONRepair:  policy.JSONRepair,
		Rerun:       policy.Rerun,
	})
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
	for _, key := range []string{
		"maxAttempts", "maxRetries", "max_retries", "baseDelayMs", "maxDelayMs", "initialBackoffMs", "maximumBackoffMs",
		"enableRetryOn429", "enableRetryOn5xx", "enableRetryOnNetworkError", "enableRetryOnParseError", "structuredRepairRetry",
		"structuredRepair", "retryNetwork", "retryRateLimit", "retryServerError", "retryEmpty", "retryParse", "repairEscalation",
		"backupProfiles", "retryPolicy", "recoveryPolicy", "repairInvalidOutput", "jsonRepair", "rerun", "escalation",
		"cache", "cacheMode", "cacheVersion", "search", "webSearch", "model", "modelId", "reasoningEffort",
	} {
		if _, present := options[key]; present {
			return &ValidationError{Field: "providerOptions." + key, Message: "recovery controls belong in recoveryPolicy"}
		}
	}
	return nil
}
