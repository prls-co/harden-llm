package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-517

import (
	"context"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful/batch"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOrdinaryAdmissionUnconfirmedPreservesOpenAIErrorAndStopsSDKReplay(t *testing.T) {
	t.Parallel()
	for _, partial := range []bool{false, true} {
		result := hardenllm.ProviderResult{ProviderDispatched: true}
		providerErr := &hardenllm.ProviderError{Category: hardenllm.RecoveryCategory("network"), Code: "NETWORK_ERROR"}
		want := 502
		if partial {
			result.Stream.OutputBytes = 1
			providerErr.Status = 429
			providerErr.Category = hardenllm.RecoveryCategory("rate_limit")
			providerErr.RetryAfter = 1500 * time.Millisecond
			want = 429
		}
		_, err := batch.ObserveProviderDispatch(result, providerErr)
		w := httptest.NewRecorder()
		(&API{}).writeCallError(w, err, hardenllm.Result{}, false)
		if w.Code != want || w.Header().Get("x-should-retry") != "false" {
			t.Fatal("uncertain inference changed wire status or permitted SDK replay", w.Code, w.Header(), w.Body)
		}
		if partial && w.Header().Get("Retry-After") != "2" {
			t.Fatal("trusted provider wait floor changed", w.Header())
		}
	}
	_, err := batch.ObserveProviderDispatch(hardenllm.ProviderResult{ProviderDispatched: true}, context.DeadlineExceeded)
	w := httptest.NewRecorder()
	(&API{}).writeCallError(w, err, hardenllm.Result{}, false)
	if w.Code != 504 || w.Header().Get("x-should-retry") != "false" {
		t.Fatal("active timeout lost uncertainty", w.Code, w.Header())
	}
}
