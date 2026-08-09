package traces

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-016

func TestParityTraceProjectionAndObservations(t *testing.T) {
	t.Parallel()
	record := runtime.CallRecord{
		CallID: "call-1", TraceID: "trace-1", Output: map[string]any{"ok": true},
		Usage: runtime.Usage{InputTokens: 10, CacheReadTokens: 2, OutputTokens: 4, ReasoningTokens: 1, TotalTokens: 17},
		Cost:  runtime.Cost{TotalUSD: 0.125, Known: true, Source: "profile"},
		Attempts: []retry.Attempt{
			{Number: 1, ProfileID: "Primary", Category: retry.CategoryParse, Retryable: true, Delay: 500 * time.Millisecond, Duration: 10 * time.Millisecond},
			{Number: 2, ProfileID: "Primary", Category: retry.CategorySuccess, Repair: true, Duration: 20 * time.Millisecond},
		},
		Cache: runtime.CacheFacts{Mode: cachekey.ModeCache, Status: "miss", OperationHash: "sha256:operation", Version: "v1", Written: true},
	}
	context := runtime.ObservabilityContext{OrganizationID: "org/unsafe", TaskID: "task 1", TaskSlug: "generate_questions", RunID: "run-1"}
	started := time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC)
	trace := Project(record, context, started, started.Add(time.Second), nil)
	if trace.Status != StatusSuccess || trace.CallID != "call-1" || trace.Usage.TotalTokens != 17 || !trace.Cost.Known || !trace.UsedRepair {
		t.Fatalf("unexpected trace projection: %#v", trace)
	}
	wantKinds := []string{"cache.lookup", "provider.attempt", "retry.wait", "provider.attempt", "repair", "cache.write"}
	gotKinds := make([]string, 0, len(trace.Observations))
	for _, observation := range trace.Observations {
		gotKinds = append(gotKinds, observation.Kind)
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("observation sequence = %#v, want %#v", gotKinds, wantKinds)
	}
	artifacts, err := ArtifactProjections(trace, json.RawMessage(`{"apiKey":"fake-secret","answer":"ok"}`))
	if err != nil {
		t.Fatalf("ArtifactProjections: %v", err)
	}
	if len(artifacts) != 2 || artifacts[0].Kind != ArtifactKindTrace || artifacts[1].Kind != ArtifactKindParseFailureResponse {
		t.Fatalf("unexpected artifact kinds: %#v", artifacts)
	}
	for _, artifact := range artifacts {
		if artifact.Key == "" || containsUnsafeObjectKey(artifact.Key) || string(artifact.Content) == "" {
			t.Fatalf("unsafe artifact projection: %#v", artifact)
		}
		if string(artifact.Content) == `{"apiKey":"fake-secret","answer":"ok"}` {
			t.Fatal("raw failure response was not redacted")
		}
	}
}

func TestParityTraceFailureAndCacheHit(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC)
	failure := Project(runtime.CallRecord{CallID: "failed", TraceID: "trace-failed", Attempts: []retry.Attempt{{Number: 1, Category: retry.CategoryTimeout}}}, runtime.ObservabilityContext{}, started, started.Add(time.Second), contextDeadlineError{})
	if failure.Status != StatusTimeout || failure.LastErrorCategory != string(retry.CategoryTimeout) {
		t.Fatalf("unexpected failure trace: %#v", failure)
	}
	providerTimeout := Project(runtime.CallRecord{CallID: "failed-provider", TraceID: "trace-provider", Attempts: []retry.Attempt{{Number: 1, Category: retry.CategoryTimeout}}}, runtime.ObservabilityContext{}, started, started.Add(time.Second), &retry.ProviderError{Timeout: true})
	if providerTimeout.Status != StatusTimeout || providerTimeout.LastErrorCategory != string(retry.CategoryTimeout) {
		t.Fatalf("provider timeout trace mismatch: %#v", providerTimeout)
	}
	providerRetry := Project(runtime.CallRecord{
		CallID: "failed-provider-retry", TraceID: "trace-provider-retry",
		Attempts: []retry.Attempt{{Number: 1, Category: retry.CategoryProvider, Code: "provider_retry", ProviderRequestID: "req_fixture_0001"}},
	}, runtime.ObservabilityContext{}, started, started.Add(time.Second), &retry.ProviderError{Code: "provider_retry", ProviderRequestID: "req_fixture_0001"})
	if providerRetry.LastErrorCategory != string(retry.CategoryProvider) || providerRetry.LastErrorStatus != nil ||
		providerRetry.Attempts[0].ProviderRequestID != "req_fixture_0001" {
		t.Fatalf("provider retry trace mismatch: %#v", providerRetry)
	}
	serverFailure := Project(runtime.CallRecord{
		CallID: "failed-server", TraceID: "trace-server",
		Attempts: []retry.Attempt{{Number: 1, Category: retry.CategoryServer, Status: 503}},
	}, runtime.ObservabilityContext{}, started, started.Add(time.Second), &retry.ProviderError{Status: 503})
	if serverFailure.LastErrorStatus == nil || *serverFailure.LastErrorStatus != 503 {
		t.Fatalf("server status trace mismatch: %#v", serverFailure)
	}
	cacheHit := Project(runtime.CallRecord{CallID: "cached", TraceID: "trace-cached", Cache: runtime.CacheFacts{Mode: cachekey.ModeCache, Status: "hit", Served: true}}, runtime.ObservabilityContext{}, started, started, nil)
	if cacheHit.Status != StatusSuccess || !cacheHit.Cache.Served || cacheHit.ProviderInvoked {
		t.Fatalf("unexpected cache-hit trace: %#v", cacheHit)
	}
}

type contextDeadlineError struct{}

func (contextDeadlineError) Error() string   { return "context deadline exceeded" }
func (contextDeadlineError) Timeout() bool   { return true }
func (contextDeadlineError) Temporary() bool { return false }
