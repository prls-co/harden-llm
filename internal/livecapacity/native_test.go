package livecapacity

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-471

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-483
func TestNativeBoundedTextRequiresIndependentExactContent(t *testing.T) {
	for _, n := range []int{511, 512, 513} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["stream"] != true || body["model"] != "gpt-6-luna" {
					t.Error("wrong native body")
				}
				out, _ := json.Marshal(map[string]any{"nonce": "fixture-0", "payload": strings.TrimSpace(strings.Repeat("x ", n))})
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "model": "gpt-6-luna", "output_text": string(out), "usage": map[string]int{"input_tokens": 100, "output_tokens": 550}})
			}))
			defer s.Close()
			m := (NativeCaller{URL: s.URL, Key: "synthetic", Client: s.Client(), Timeout: time.Second, BoundedText: true}).Call(context.Background(), "fixture", 0, 1)
			if (m.Kind == "completed") != (n == 512) || m.ExpectedWords != 512 {
				t.Fatalf("bounded text qualification changed: %+v", m)
			}
		})
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

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-483
func TestNativeErrorTerminalsWithoutUsageRemainUnconfirmed(t *testing.T) {
	for _, body := range []string{
		`{"type":"error","error":{"message":"synthetic upstream failure"}}`,
		`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error"}}}`,
		`{"type":"response.incomplete","response":{"status":"incomplete"}}`,
		`{"type":"response.completed","response":{"status":"completed","model":"gpt-6-luna","output_text":"{\"nonce\":\"fixture-0\"}"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\n", body)
			}))
			defer s.Close()
			m := (NativeCaller{URL: s.URL, Key: "synthetic", Client: s.Client(), Timeout: time.Second}).Call(context.Background(), "fixture", 0, 1)
			if m.Kind != "unconfirmed" || m.Code == "qualification_mismatch" {
				t.Fatalf("missing usage lost the uncertain-outcome budget reserve: %+v", m)
			}
		})
	}
}

func TestNativeCapturesOnlyBoundedCPACorrelationID(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{"synthetic-private-prefix-0123abcd", "0123abcd"},
		{"synthetic-private-prefix-not-an-id", ""},
		{"synthetic-private-prefix-0123abcd-extra", ""},
	} {
		t.Run(tc.header, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Cpa-Trace-Id", tc.header)
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, `{"error":{"code":"rate_limit_exceeded"}}`)
			}))
			defer s.Close()
			m := (NativeCaller{URL: s.URL, Key: "synthetic", Client: s.Client(), Timeout: time.Second}).Call(context.Background(), "fixture", 0, 1)
			if m.CPARequestID != tc.want || m.Kind != "rejected" {
				t.Fatalf("unsafe or missing correlation ID: %+v", m)
			}
		})
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-473
func TestNativeOutputLengthAndUsageAreValidated(t *testing.T) {
	for _, n := range []int{1, 2} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request["input"] == nil {
					t.Error("missing length instruction")
				}
				items := make([]string, n)
				for i := range items {
					items[i] = "x"
				}
				out, _ := json.Marshal(map[string]any{"nonce": "fixture-0", "items": items})
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "model": "gpt-6-luna", "output_text": string(out), "usage": map[string]any{"input_tokens": 10, "output_tokens": 20, "input_tokens_details": map[string]int{"cached_tokens": 4}, "output_tokens_details": map[string]int{"reasoning_tokens": 3}}})
			}))
			defer s.Close()
			m := (NativeCaller{URL: s.URL, Key: "synthetic", Client: s.Client(), Timeout: time.Second, OutputItems: 2}).Call(context.Background(), "fixture", 0, 1)
			if (m.Kind == "completed") != (n == 2) || m.OutputTokens != 20 || m.ReasoningTokens == nil || *m.ReasoningTokens != 3 || m.CachedInputTokens == nil || *m.CachedInputTokens != 4 {
				t.Fatalf("incorrect length/usage qualification: %+v", m)
			}
		})
	}
}
