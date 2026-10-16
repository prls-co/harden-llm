package httpclient

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-472

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/api"
)

const testAPIKey = "synthetic-http-client-key-0000000000000001"

func fixtureRequest(t *testing.T) RunInput {
	t.Helper()
	policy, err := json.Marshal(hardenllm.DefaultStructuredRecoveryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return RunInput{ProfileID: "cpa-json", UserPrompt: "Return the fixture object.", CallType: hardenllm.CallTypeStructured,
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"ok":{"type":"boolean"}},"required":["ok"]}`),
		ReasoningEffort: "lowest", CacheMode: hardenllm.CacheModeOff, RecoveryPolicy: policy,
		Origin: hardenllm.Origin{Client: "query-analytics", OperationID: "qa-fixture-op"}}
}

func fixtureEvents(t *testing.T) []map[string]any {
	t.Helper()
	data, err := os.ReadFile("../test/fixtures/reference/workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Stream []map[string]any `json:"stream"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	for _, event := range fixture.Stream {
		if event["type"] == "run.completed" {
			result := event["data"].(map[string]any)["result"].(map[string]any)
			result["origin"] = map[string]any{"client": "query-analytics", "operationId": "qa-fixture-op"}
		}
	}
	return fixture.Stream
}

func eventBytes(t *testing.T, events []map[string]any) []byte {
	t.Helper()
	var result bytes.Buffer
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if err := api.Validate("ReferenceRunProgressEvent", data); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&result, "event: %s\ndata: %s\n\n", event["type"], data)
	}
	return result.Bytes()
}

func terminalResult(events []map[string]any) map[string]any {
	return events[2]["data"].(map[string]any)["result"].(map[string]any)
}

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Errorf("wrong bearer on %s %s", r.Method, r.URL.Path)
		}
		for _, name := range []string{"X-PRLS-Account-ID", "X-PRLS-Service", "Idempotency-Key", "Last-Event-ID"} {
			if r.Header.Get(name) != "" {
				t.Errorf("unexpected %s", name)
			}
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, APIKey: testAPIKey, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return client, &calls
}

func TestRunEncodesReferenceContract(t *testing.T) {
	t.Parallel()
	input := fixtureRequest(t)
	events := eventBytes(t, fixtureEvents(t))
	client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/run" || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("wrong method/path/headers: %s %s %v", r.Method, r.URL, r.Header)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if err := api.Validate("RunInput", body); err != nil {
			t.Error(err)
		}
		var got RunInput
		if err := json.Unmarshal(body, &got); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(got, input) {
			t.Errorf("request changed: got %+v", got)
		}
		if bytes.Contains(body, []byte(testAPIKey)) || bytes.Contains(body, []byte("accountId")) {
			t.Error("secret or product account sent")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(events)
	})
	out, err := client.Run(context.Background(), input)
	if err != nil || out.Evidence == nil || out.Evidence.OperationID != input.Origin.OperationID || out.Evidence.ProviderAttemptCount != 2 || out.Recording.Status != "saved" || calls.Load() != 1 {
		t.Fatalf("outcome=%+v err=%v calls=%d", out, err, calls.Load())
	}
	if string(out.Result.Output) != `{"ok":true}` {
		t.Fatalf("output=%s", out.Result.Output)
	}
}

func TestRunKeepsTerminalWhenRecordingIsUnconfirmed(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{"", "event: history.recorded\ndata: {bad}\n\n", "event: run.completed\ndata: {}\n\n"} {
		t.Run(fmt.Sprint(len(suffix)), func(t *testing.T) {
			stream := append(eventBytes(t, fixtureEvents(t)[:3]), []byte(suffix)...)
			client, calls := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write(stream)
			})
			out, err := client.Run(context.Background(), fixtureRequest(t))
			if err != nil || out.Evidence == nil || out.Result.Status != "succeeded" || out.Recording.Status != "unconfirmed" || calls.Load() != 1 {
				t.Fatalf("lost known inference: %+v %v calls=%d", out, err, calls.Load())
			}
		})
	}
}

func TestRunNeverReplaysInference(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusTemporaryRedirect} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Location", "/api/v1/run")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"code":"fixture_rejected","message":"`+testAPIKey+`"}}`)
			})
			_, err := client.Run(context.Background(), fixtureRequest(t))
			var rejected *RejectedError
			if !errors.As(err, &rejected) || rejected.StatusCode != status || calls.Load() != 1 || strings.Contains(err.Error(), testAPIKey) {
				t.Fatalf("classification/dispatch/redaction: %v calls=%d", err, calls.Load())
			}
		})
	}
	client, calls := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: run.progress\ndata: {}\n\n")
	})
	_, err := client.Run(context.Background(), fixtureRequest(t))
	var unknown *UnknownOutcomeError
	if !errors.As(err, &unknown) || calls.Load() != 1 {
		t.Fatalf("missing terminal is not unknown: %v calls=%d", err, calls.Load())
	}
}

func TestRunRejectsInvalidTerminalEvidence(t *testing.T) {
	t.Parallel()
	for _, mutation := range []string{"origin", "accounting", "state", "profile", "invoked", "output", "sequence", "run", "type"} {
		t.Run(mutation, func(t *testing.T) {
			events := fixtureEvents(t)
			result := terminalResult(events)
			switch mutation {
			case "origin":
				result["origin"] = map[string]any{"operationId": "another-operation"}
			case "accounting":
				delete(result, "accounting")
			case "state":
				events[2]["data"].(map[string]any)["state"].(map[string]any)["lastRunId"] = "other-run"
			case "profile":
				result["selectedTarget"].(map[string]any)["profileId"] = "other-profile"
			case "invoked":
				result["providerInvoked"] = false
			case "output":
				result["output"] = map[string]any{"unexpected": true}
			case "sequence":
				events[1]["sequence"] = events[0]["sequence"]
			case "run":
				events[1]["runId"] = "other-run"
			case "type":
				events[2]["type"] = "run.failed"
			}
			stream := eventBytes(t, events)
			client, calls := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write(stream)
			})
			out, err := client.Run(context.Background(), fixtureRequest(t))
			var unknown *UnknownOutcomeError
			if !errors.As(err, &unknown) || out.Evidence != nil || calls.Load() != 1 {
				t.Fatalf("invalid evidence accepted: %+v %v", out, err)
			}
		})
	}
}

func TestRunPreservesTerminalFailureEvidence(t *testing.T) {
	t.Parallel()
	events := fixtureEvents(t)
	events[2]["type"] = "run.failed"
	terminalResult(events)["status"] = "failed"
	terminalResult(events)["output"] = nil
	terminalResult(events)["resultSource"] = map[string]any{"kind": "none"}
	// The canonical workspace fixture deliberately has unavailable usage.
	// Give this preservation test observed accounting, rather than assuming
	// that the number of provider attempts implies measured tokens.
	usage := terminalResult(events)["accounting"].(map[string]any)["provider"].(map[string]any)["usage"].(map[string]any)
	usage["inputTokens"], usage["promptTokens"] = 11, 11
	usage["outputTokens"], usage["completionTokens"] = 6, 6
	usage["totalTokens"], usage["status"] = 17, "complete"
	events[2]["data"].(map[string]any)["error"] = map[string]any{"code": "attempts_exhausted", "message": "fixture failed"}
	stream := eventBytes(t, events)
	client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(stream)
	})
	out, err := client.Run(context.Background(), fixtureRequest(t))
	var failed *RunFailure
	if !errors.As(err, &failed) || out.Evidence == nil || out.Evidence.ProviderAttemptCount != 2 || len(out.Result.Attempts) != 2 || out.Result.Accounting.Provider.Usage.TotalTokens != 17 || out.Result.Accounting.Provider.Usage.InputTokens != 11 {
		t.Fatalf("lost complete failure evidence: attempts=%d accounting=%+v err=%v", len(out.Result.Attempts), out.Result.Accounting, err)
	}
}

func TestRunRejectsInvalidInputBeforeDispatch(t *testing.T) {
	t.Parallel()
	client, calls := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid request dispatched") })
	for _, mutation := range []string{"profile", "schema", "timeout", "reasoning", "policy", "options", "body"} {
		input := fixtureRequest(t)
		switch mutation {
		case "profile":
			input.ProfileID = "bad/profile"
		case "schema":
			input.Schema = json.RawMessage(`[]`)
		case "timeout":
			input.TimeoutMS = 60001
		case "reasoning":
			input.ReasoningEffort = "high"
		case "policy":
			input.RecoveryPolicy = nil
		case "options":
			input.ProviderOptions = map[string]any{"big": strings.Repeat("x", 33<<10)}
		case "body":
			input.SystemPrompt = strings.Repeat("x", 257<<10)
		}
		if _, err := client.Run(context.Background(), input); err == nil {
			t.Errorf("accepted invalid %s", mutation)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid inputs caused %d requests", calls.Load())
	}
}

func TestClientRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{"", "ftp://localhost", "https://user:password@example.test", "https://example.test/prefix", "https://example.test?scope=other", "https://example.test#fragment", "http://external.example"} {
		if _, err := New(Config{BaseURL: origin, APIKey: testAPIKey}); err == nil {
			t.Errorf("invalid origin accepted: %q", origin)
		}
	}
	for _, key := range []string{"", "short", strings.Repeat("x", 513), testAPIKey + "\n", strings.Repeat("λ", 32)} {
		if _, err := New(Config{BaseURL: "https://example.test", APIKey: key}); err == nil {
			t.Error("invalid bearer accepted")
		}
	}
}

func TestRunCancellationPreservesUnknownClassification(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	stopped := make(chan struct{})
	release := make(chan struct{})
	client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		// net/http observes the disconnected client after the request body is
		// consumed. Waiting with an unread POST body deadlocks fixture cleanup.
		_, err := io.Copy(io.Discard, r.Body)
		close(started)
		if err != nil {
			t.Errorf("read cancellation fixture request: %v", err)
			return
		}
		select {
		case <-r.Context().Done():
			close(stopped)
		case <-release:
		}
	})
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-started; cancel() }()
	_, err := client.Run(ctx, fixtureRequest(t))
	var unknown *UnknownOutcomeError
	if !errors.As(err, &unknown) || !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("cancel classified incorrectly: %v calls=%d", err, calls.Load())
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("request cancellation did not reach the HTTP server")
	}
}

func TestProfilesUseTheSameBearerScope(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../test/fixtures/reference/workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Profiles json.RawMessage            `json:"profiles"`
		Details  map[string]json.RawMessage `json:"details"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/profiles":
			_, _ = w.Write(fixture.Profiles)
		case "/api/v1/profiles/CPA Responses":
			_, _ = w.Write(fixture.Details["CPA Responses"])
		default:
			t.Errorf("wrong path %s", r.URL)
			http.NotFound(w, r)
		}
	})
	listed, err := client.ListProfiles(context.Background())
	if err != nil || len(listed.Profiles) == 0 {
		t.Fatalf("list failed: %+v %v", listed, err)
	}
	detail, err := client.GetProfile(context.Background(), "CPA Responses")
	if err != nil || detail.Definition.ModelID == "" || calls.Load() != 2 {
		t.Fatalf("detail failed: %+v %v", detail, err)
	}
	if _, err := client.GetProfile(context.Background(), "bad/profile"); err == nil || calls.Load() != 2 {
		t.Fatal("invalid selector reached server")
	}
}

type byteReader struct{ data []byte }

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRunDecodesFragmentedAndLargeFrames(t *testing.T) {
	t.Parallel()
	for _, ending := range []string{"\n", "\r\n"} {
		events := fixtureEvents(t)
		payload := map[string]any{"text": strings.Repeat("λ🚀", 20000)}
		terminalResult(events)["output"] = payload
		stream := strings.ReplaceAll(string(eventBytes(t, events)), "\n", ending)
		httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(&byteReader{data: []byte(": comment" + ending + ending + stream)})}, nil
		})}
		client, err := New(Config{BaseURL: "https://fixture.example", APIKey: testAPIKey, HTTPClient: httpClient})
		if err != nil {
			t.Fatal(err)
		}
		input := fixtureRequest(t)
		input.Schema = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"text":{"type":"string"}},"required":["text"]}`)
		out, err := client.Run(context.Background(), input)
		if err != nil || len(out.Result.Output) < 64000 || out.Recording.Status != "saved" {
			t.Fatalf("fragmented/large frame failed: bytes=%d err=%v", len(out.Result.Output), err)
		}
	}
}

func TestRunDoesNotMutateSharedHTTPClient(t *testing.T) {
	t.Parallel()
	shared := &http.Client{Timeout: time.Minute}
	if _, err := New(Config{BaseURL: "https://fixture.example", APIKey: testAPIKey, HTTPClient: shared}); err != nil {
		t.Fatal(err)
	}
	if shared.CheckRedirect != nil || shared.Timeout != time.Minute {
		t.Fatal("client changed host transport configuration")
	}
}
