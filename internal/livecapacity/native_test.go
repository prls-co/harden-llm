package livecapacity

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-471

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeCompletionDoesNotWaitForConnectionEOF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["harden"] != nil || body["stream"] != true || body["model"] != "gpt-6-luna" || body["reasoning"].(map[string]any)["effort"] != "low" {
			t.Error("wrong native workload")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-luna\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"{\\\"nonce\\\":\\\"fixture-0\\\"}\"}]}],\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	caller := NativeCaller{URL: server.URL, Key: "synthetic", Client: server.Client(), Timeout: time.Second}
	m := caller.Call(ctx, "fixture", 0, 1)
	if m.Kind != "completed" || m.Terminal != "response.completed" || m.Nonce != "fixture-0" {
		t.Fatalf("native completion lost: %+v", m)
	}
}
func TestNativeThrottleAndMalformedResults(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, kind string
	}{{429, `{"error":{"code":"rate_limit_exceeded"}}`, "rejected"}, {403, `{"error":{"code":"account_deactivated"}}`, "policy"}, {200, `{"status":"completed","model":"gpt-6-luna","output_text":"{\"nonce\":\"wrong\"}","usage":{"input_tokens":10,"output_tokens":2}}`, "failed"}} {
		t.Run(tc.kind, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			m := (NativeCaller{URL: s.URL, Key: "synthetic", Client: s.Client(), Timeout: time.Second}).Call(context.Background(), "fixture", 0, 1)
			if m.Kind != tc.kind || m.RetryAfterMS != 7000 {
				t.Fatalf("incorrect native result: %+v", m)
			}
		})
	}
}
