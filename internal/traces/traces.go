// Package traces projects runtime call records into persistence-neutral traces,
// observations, and canonical artifact payloads.
package traces

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/prls-co/harden-llm/internal/redaction"
	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
)

const (
	StatusSuccess  = "success"
	StatusFailure  = "failure"
	StatusTimeout  = "timeout"
	StatusCanceled = "canceled"

	ArtifactKindTrace                = "trace"
	ArtifactKindParseFailureResponse = "parse-failure-response"
)

type Trace struct {
	SchemaVersion       string                       `json:"schemaVersion"`
	CallID              string                       `json:"callId"`
	TraceID             string                       `json:"traceId"`
	Status              string                       `json:"status"`
	StartedAt           time.Time                    `json:"startedAt"`
	CompletedAt         time.Time                    `json:"completedAt"`
	TotalCallDurationMs int64                        `json:"totalCallDurationMs"`
	TotalWaitMs         int64                        `json:"totalWaitMs"`
	LastErrorCategory   string                       `json:"lastErrorCategory,omitempty"`
	LastErrorStatus     *int                         `json:"lastErrorStatus"`
	SelectedTarget      runtime.ExecutionTarget      `json:"selectedTarget"`
	ResultSource        runtime.ResultSource         `json:"resultSource"`
	Accounting          runtime.Accounting           `json:"accounting"`
	Attempts            []Attempt                    `json:"attempts"`
	Cache               runtime.CacheFacts           `json:"cache"`
	ProviderInvoked     bool                         `json:"providerInvoked"`
	UsedRepair          bool                         `json:"usedRepair"`
	Context             runtime.ObservabilityContext `json:"context"`
	Observations        []Observation                `json:"observations"`
}

type Attempt struct {
	Number            int                     `json:"number"`
	RetryLocalNumber  int                     `json:"retryLocalNumber"`
	ProfileID         string                  `json:"profileId"`
	BackupIndex       int                     `json:"backupIndex"`
	Target            runtime.ExecutionTarget `json:"target"`
	ProviderUsed      bool                    `json:"providerUsed"`
	Category          retry.Category          `json:"category"`
	Status            int                     `json:"status,omitempty"`
	Code              string                  `json:"code,omitempty"`
	Type              string                  `json:"type,omitempty"`
	ProviderRequestID string                  `json:"providerRequestId,omitempty"`
	Retryable         bool                    `json:"retryable"`
	DelayMs           int64                   `json:"delayMs"`
	DurationMs        int64                   `json:"durationMs"`
	Repair            bool                    `json:"repair"`
}

type Observation struct {
	Sequence int            `json:"sequence"`
	Kind     string         `json:"kind"`
	Outcome  string         `json:"outcome"`
	Fields   map[string]any `json:"fields,omitempty"`
}

type ArtifactProjection struct {
	ArtifactID  string          `json:"artifactId"`
	Kind        string          `json:"kind"`
	Key         string          `json:"key"`
	Content     json.RawMessage `json:"content"`
	ContentType string          `json:"contentType"`
}

func Project(record runtime.CallRecord, callContext runtime.ObservabilityContext, started, completed time.Time, terminalErr error) Trace {
	if completed.Before(started) {
		completed = started
	}
	trace := Trace{
		SchemaVersion: "harden-llm.trace.v2", CallID: record.CallID, TraceID: record.TraceID,
		Status: statusFor(terminalErr), StartedAt: started.UTC(), CompletedAt: completed.UTC(),
		TotalCallDurationMs: completed.Sub(started).Milliseconds(),
		SelectedTarget:      record.SelectedTarget, ResultSource: record.ResultSource, Accounting: record.Accounting,
		Cache: record.Cache, ProviderInvoked: providerWasInvoked(record.Attempts), Context: cloneContext(callContext),
		Attempts: make([]Attempt, 0, len(record.Attempts)), Observations: make([]Observation, 0, len(record.Attempts)*3+2),
	}
	if record.Cache.Mode != "" {
		trace.appendObservation("cache.lookup", record.Cache.Status, map[string]any{
			"mode": record.Cache.Mode, "served": record.Cache.Served, "version": record.Cache.Version,
		})
	}
	for _, source := range record.Attempts {
		attempt := Attempt{
			Number: source.Number, RetryLocalNumber: source.RetryLocalNumber,
			ProfileID: source.ProfileID, BackupIndex: source.BackupIndex,
			Target: source.Target, ProviderUsed: source.ProviderUsed,
			Category: source.Category, Status: source.Status, Retryable: source.Retryable,
			Code: source.Code, Type: source.Type, ProviderRequestID: source.ProviderRequestID,
			DelayMs: source.Delay.Milliseconds(), DurationMs: source.Duration.Milliseconds(), Repair: source.Repair,
		}
		trace.Attempts = append(trace.Attempts, attempt)
		trace.appendObservation("provider.attempt", string(source.Category), map[string]any{
			"attempt": source.Number, "profileId": source.ProfileID, "status": source.Status,
			"code": source.Code, "type": source.Type, "providerRequestId": source.ProviderRequestID,
		})
		if source.Delay > 0 {
			trace.TotalWaitMs += source.Delay.Milliseconds()
			trace.appendObservation("retry.wait", "completed", map[string]any{"delayMs": source.Delay.Milliseconds()})
		}
		if source.Repair {
			trace.UsedRepair = true
			trace.appendObservation("repair", "attempted", map[string]any{"attempt": source.Number})
		}
	}
	if record.Cache.Written {
		trace.appendObservation("cache.write", "success", map[string]any{"version": record.Cache.Version})
	}
	if terminalErr != nil {
		if len(record.Attempts) > 0 {
			trace.LastErrorCategory = string(record.Attempts[len(record.Attempts)-1].Category)
			if status := record.Attempts[len(record.Attempts)-1].Status; status > 0 {
				trace.LastErrorStatus = &status
			}
		}
		if trace.Status == StatusTimeout {
			trace.LastErrorCategory = string(retry.CategoryTimeout)
		} else if trace.Status == StatusCanceled {
			trace.LastErrorCategory = string(retry.CategoryCanceled)
		} else if trace.LastErrorCategory == "" || trace.LastErrorCategory == string(retry.CategorySuccess) {
			trace.LastErrorCategory = string(retry.CategoryOther)
		}
	}
	return trace
}

func providerWasInvoked(attempts []runtime.AttemptRecord) bool {
	for _, attempt := range attempts {
		if attempt.ProviderUsed {
			return true
		}
	}
	return false
}

func (trace *Trace) appendObservation(kind, outcome string, fields map[string]any) {
	trace.Observations = append(trace.Observations, Observation{
		Sequence: len(trace.Observations) + 1, Kind: kind, Outcome: outcome, Fields: fields,
	})
}

func AddObservation(trace *Trace, kind, outcome string, fields map[string]any) {
	if trace == nil {
		return
	}
	trace.appendObservation(kind, outcome, fields)
}

func statusFor(err error) string {
	if err == nil {
		return StatusSuccess
	}
	if errors.Is(err, context.Canceled) {
		return StatusCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return StatusTimeout
	}
	if retry.Classify(err, retry.DefaultPolicy()).Category == retry.CategoryTimeout {
		return StatusTimeout
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return StatusTimeout
	}
	return StatusFailure
}

func ArtifactProjections(trace Trace, parseFailureResponse json.RawMessage, secrets ...string) ([]ArtifactProjection, error) {
	redactor := redaction.New(secrets...)
	encodedTrace, err := json.Marshal(trace)
	if err != nil {
		return nil, fmt.Errorf("traces: encode trace artifact: %w", err)
	}
	redactedTrace, err := redactor.JSON(encodedTrace)
	if err != nil {
		return nil, fmt.Errorf("traces: redact trace artifact: %w", err)
	}
	prefix := path.Join(
		"llm-traces", SafeObjectKeyComponent(trace.Context.OrganizationID),
		SafeObjectKeyComponent(trace.Context.TaskID), SafeObjectKeyComponent(trace.TraceID),
	)
	traceArtifactID := SafeObjectKeyComponent(trace.CallID) + "-trace"
	result := []ArtifactProjection{{
		ArtifactID: traceArtifactID,
		Kind:       ArtifactKindTrace, Key: path.Join(prefix, traceArtifactID+".json"),
		Content: redactedTrace, ContentType: "application/json",
	}}
	parseAttempt := 0
	for _, attempt := range trace.Attempts {
		if attempt.Category == retry.CategoryParse {
			parseAttempt = attempt.Number
			break
		}
	}
	if parseAttempt > 0 {
		if !json.Valid(parseFailureResponse) {
			parseFailureResponse, _ = json.Marshal(map[string]any{
				"schemaVersion": "harden-llm.parse-failure.v1",
				"rawResponse":   string(parseFailureResponse),
			})
		}
		redactedResponse, redactErr := redactor.JSON(parseFailureResponse)
		if redactErr != nil {
			return nil, fmt.Errorf("traces: redact parse failure artifact: %w", redactErr)
		}
		artifactID := fmt.Sprintf("%s-attempt-%d-raw", SafeObjectKeyComponent(trace.CallID), parseAttempt)
		result = append(result, ArtifactProjection{
			ArtifactID: artifactID,
			Kind:       ArtifactKindParseFailureResponse,
			Key:        path.Join(prefix, artifactID+".json"),
			Content:    redactedResponse, ContentType: "application/json",
		})
	}
	return result, nil
}

func SafeObjectKeyComponent(value string) string {
	original := strings.TrimSpace(value)
	if original == "" {
		return "unknown"
	}
	var builder strings.Builder
	for _, character := range original {
		if character <= unicode.MaxASCII && (unicode.IsLetter(character) || unicode.IsDigit(character) || character == '-' || character == '_' || character == '.') {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('_')
		}
		if builder.Len() >= 96 {
			break
		}
	}
	result := strings.Trim(builder.String(), ".")
	if result == "" || result == "." || result == ".." || strings.HasPrefix(result, "__") && strings.HasSuffix(result, "__") {
		result = "value"
	}
	if result != original {
		digest := sha256.Sum256([]byte(original))
		result += "-" + hex.EncodeToString(digest[:6])
	}
	return result
}

func containsUnsafeObjectKey(key string) bool {
	return strings.Contains(key, "\\") || strings.Contains(key, "//") || strings.Contains(key, "/../") || strings.HasPrefix(key, "/")
}

func cloneContext(value runtime.ObservabilityContext) runtime.ObservabilityContext {
	result := value
	result.PromptLabels = append([]string(nil), value.PromptLabels...)
	result.Tags = cloneStrings(value.Tags)
	result.Metadata = cloneStrings(value.Metadata)
	return result
}

func cloneStrings(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
