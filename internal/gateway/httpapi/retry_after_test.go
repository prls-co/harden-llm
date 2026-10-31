package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-467

import (
	hardenllm "github.com/prls-co/harden-llm"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRetryAfterSurvivesGateway(t *testing.T) {
	for _, status := range []int{429, 503} {
		w := httptest.NewRecorder()
		(&API{}).writeCallError(w, &hardenllm.ProviderError{Status: status, RetryAfter: 1500 * time.Millisecond}, hardenllm.Result{}, false)
		if got := w.Header().Get("Retry-After"); got != "2" {
			t.Fatalf("status %d Retry-After = %q, want ceiling 2", status, got)
		}
	}
}
