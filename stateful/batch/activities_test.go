package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-474
import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

func TestAttemptClassificationSeparatesQuotaAccountAndOrdinaryFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		kind string
		code string
		wait int64
	}{
		{"quota", &hardenllm.ProviderError{Status: 429, Code: "rate_limit_exceeded", RetryAfter: time.Second + time.Nanosecond}, "rejected", "rate_limit_exceeded", 1001},
		{"selection cooling", &hardenllm.ProviderError{Status: 503, Code: "auth_unavailable", RetryAfter: 5 * time.Second}, "rejected", "auth_unavailable", 5000},
		{"billing quota", &hardenllm.ProviderError{Status: 429, Code: "insufficient_quota"}, "policy", "insufficient_quota", 0},
		{"revoked authentication", &hardenllm.ProviderError{Status: 401}, "policy", "upstream_error", 0},
		{"denied account", &hardenllm.ProviderError{Status: 403}, "policy", "upstream_error", 0},
		{"ordinary outage", &hardenllm.ProviderError{Status: 503, Code: "server_error"}, "failed", "server_error", 0},
		{"unknown network result", &hardenllm.ProviderError{Code: "NETWORK_ERROR"}, "failed", "NETWORK_ERROR", 0},
		{"generation timeout", context.DeadlineExceeded, "failed", "provider_timeout", 0},
		{"untrusted text", errors.New("private-provider-body"), "failed", "upstream_error", 0},
		{"unsafe provider code", &hardenllm.ProviderError{Code: "private-provider-body with spaces"}, "failed", "upstream_error", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, problem, delay := classifyAttemptError(tc.err)
			if kind != tc.kind || problem.Code != tc.code || delay != tc.wait || strings.Contains(problem.Message, "private-provider-body") {
				t.Fatalf("incorrect classification: %s %+v %d", kind, problem, delay)
			}
			c := NewController(DefaultPolicy())
			at := time.Unix(1000, 0)
			c.Start(at)
			c.Complete(at, at, at, false, kind, time.Duration(delay)*time.Millisecond)
			if c.Paused != (tc.kind == "policy") || c.Cooling != (tc.kind == "rejected") {
				t.Fatal("classification did not control the shared pool correctly")
			}
		})
	}
}
