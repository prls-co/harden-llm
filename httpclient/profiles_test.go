package httpclient

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-482

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

func TestResolveReasoningEffortUsesOnlyConfiguredPortableLevels(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, requested, preferred, want string
		levels                           []string
		wantError                        bool
	}{
		{name: "query preference", levels: []string{"highest", "middle", "lowest"}, preferred: "lowest", want: "lowest"},
		{name: "utility preference", levels: []string{"lowest", "highest"}, preferred: "highest", want: "highest"},
		{name: "explicit choice wins", levels: []string{"middle", "highest"}, requested: "middle", preferred: "highest", want: "middle"},
		{name: "missing preference uses configured order", levels: []string{"highest", "middle"}, preferred: "lowest", want: "middle"},
		{name: "only middle", levels: []string{"middle"}, preferred: "highest", want: "middle"},
		{name: "no preference", levels: []string{"highest", "lowest"}, want: "lowest"},
		{name: "absent map", preferred: "highest"},
		{name: "empty map", levels: []string{}, preferred: "lowest"},
		{name: "explicit unconfigured", levels: []string{"middle"}, requested: "highest", preferred: "middle", wantError: true},
		{name: "explicit without map", requested: "lowest", preferred: "highest", wantError: true},
		{name: "no provider spelling", levels: []string{"lowest"}, requested: "low", preferred: "lowest", wantError: true},
		{name: "invalid preferred", levels: []string{"lowest"}, preferred: "provider-high", wantError: true},
		{name: "invalid whitespace", levels: []string{"lowest"}, requested: " lowest ", preferred: "lowest", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition := ProfileDefinition{}
			if test.levels != nil {
				definition.ReasoningEffortMap = map[string]json.RawMessage{}
				for _, level := range test.levels {
					definition.ReasoningEffortMap[level] = json.RawMessage(`{"fixture":"unchanged"}`)
				}
			}
			before, err := json.Marshal(definition)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ResolveReasoningEffort(definition, test.requested, test.preferred)
			if (err != nil) != test.wantError || got != test.want {
				t.Fatalf("got effort %q, error %v; want effort %q, error=%t", got, err, test.want, test.wantError)
			}
			after, marshalErr := json.Marshal(definition)
			if marshalErr != nil || string(before) != string(after) {
				t.Fatal("reasoning projection mutated the owner definition")
			}
		})
	}
}

func TestProfilesRejectInvalidResponsesWithoutFallback(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"denied", "malformed", "unknown_field", "content_type", "oversized"} {
		t.Run(name, func(t *testing.T) {
			client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/json" {
					t.Errorf("catalog lost its GET/content contract: %s %s", r.Method, r.URL.Path)
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
