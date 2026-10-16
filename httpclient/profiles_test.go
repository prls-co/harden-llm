package httpclient

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-472

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProfilesRejectInvalidResponsesWithoutFallback(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"denied", "malformed", "unknown_field", "content_type", "oversized"} {
		t.Run(name, func(t *testing.T) {
			client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/json" {
					t.Error("catalog lost its GET/content contract")
				}
				w.Header().Set("Content-Type", "application/json")
				switch name {
				case "denied":
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, `{"error":{"code":"forbidden","message":"fixture denied"}}`)
				case "malformed":
					_, _ = io.WriteString(w, `{"profiles":[{}]}`)
				case "unknown_field":
					_, _ = io.WriteString(w, `{"profiles":[],"apiKey":"not a public field"}`)
				case "content_type":
					w.Header().Set("Content-Type", "text/html")
					_, _ = io.WriteString(w, `{"profiles":[]}`)
				case "oversized":
					_, _ = io.WriteString(w, strings.Repeat("x", maximumResponseBytes+1))
				}
			})
			_, err := client.ListProfiles(context.Background())
			if err == nil || calls.Load() != 1 {
				t.Fatalf("invalid catalog accepted or retried: %v calls=%d", err, calls.Load())
			}
			if name == "denied" {
				var rejected *RejectedError
				if !errors.As(err, &rejected) || rejected.StatusCode != http.StatusForbidden {
					t.Fatalf("catalog denial misclassified: %v", err)
				}
			}
		})
	}
}

func TestClientBoundsDeadlinesAndRedactsConfiguration(t *testing.T) {
	t.Parallel()
	for _, timeout := range []time.Duration{-time.Second, 0, time.Second, 2 * maximumClientDuration} {
		shared := &http.Client{Timeout: timeout}
		config := Config{BaseURL: "https://fixture.example", APIKey: testAPIKey, HTTPClient: shared}
		client, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		want := timeout
		if timeout <= 0 || timeout > maximumClientDuration {
			want = maximumClientDuration
		}
		if client.http.Timeout != want || shared.Timeout != timeout || shared.CheckRedirect != nil {
			t.Fatal("timeout was unbounded or shared client was mutated")
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{string(encoded), fmt.Sprintf("%v %+v %#v", config, config, config), fmt.Sprintf("%v %+v %#v", client, client, client)} {
			if strings.Contains(text, testAPIKey) {
				t.Fatal("configuration formatting/JSON exposed the server-held key")
			}
		}
	}
}
