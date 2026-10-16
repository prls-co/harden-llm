package httpclient

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-472

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type failedReader struct{ cause error }

func (r failedReader) Read([]byte) (int, error) { return 0, r.cause }

type observedBody struct {
	io.Reader
	closed bool
}

func (body *observedBody) Close() error { body.closed = true; return nil }

func responseClient(t *testing.T, status int, contentType string, body io.ReadCloser) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	transport := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.GetBody != nil || request.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Error("inference request became replayable or lost its bearer")
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: body}, nil
	})}
	client, err := New(Config{BaseURL: "https://fixture.example", APIKey: testAPIKey, HTTPClient: transport})
	if err != nil {
		t.Fatal(err)
	}
	return client, &calls
}

func TestRunRejectsInvalidStream(t *testing.T) {
	t.Parallel()
	valid := eventBytes(t, fixtureEvents(t))
	for _, name := range []string{"empty", "progress_only", "malformed_json", "outer_type", "cross_call", "cross_trace", "early_history", "duplicate_progress", "oversized_frame", "aggregate_limit"} {
		t.Run(name, func(t *testing.T) {
			stream := valid
			switch name {
			case "empty":
				stream = nil
			case "progress_only":
				stream = eventBytes(t, fixtureEvents(t)[:2])
			case "malformed_json":
				stream = []byte("event: run.started\ndata: {bad}\n\n")
			case "outer_type":
				stream = bytes.Replace(valid, []byte("event: run.completed"), []byte("event: run.failed"), 1)
			case "cross_call", "cross_trace":
				events := fixtureEvents(t)
				field := "callId"
				if name == "cross_trace" {
					field = "traceId"
				}
				events[1][field] = "00000000-0000-4000-8000-000000000002"
				stream = eventBytes(t, events)
			case "early_history":
				events := fixtureEvents(t)
				events[0]["type"], events[0]["data"] = "history.recorded", events[3]["data"]
				stream = eventBytes(t, events)
			case "duplicate_progress":
				events := fixtureEvents(t)
				stream = append(eventBytes(t, events[:2]), eventBytes(t, events[1:])...)
			case "oversized_frame":
				stream = []byte("data: " + strings.Repeat("x", maximumResponseBytes) + "\n\n")
			case "aggregate_limit":
				// Each comment is small, but discarded diagnostics are not allowed
				// to turn a bounded stream into an unbounded input.
				stream = []byte(strings.Repeat(":"+strings.Repeat("x", 1021)+"\n\n", maximumStreamBytes/1024+1))
			}
			body := &observedBody{Reader: bytes.NewReader(stream)}
			client, calls := responseClient(t, 200, "text/event-stream", body)
			out, err := client.Run(context.Background(), fixtureRequest(t))
			var unknown *UnknownOutcomeError
			if !errors.As(err, &unknown) || out.Evidence != nil || out.Result.RunID != "" || calls.Load() != 1 || !body.closed {
				t.Fatalf("accepted invalid %s or replayed/leaked body: err=%v calls=%d closed=%t", name, err, calls.Load(), body.closed)
			}
		})
	}
}

func TestRunReadErrorBeforeAndAfterTerminal(t *testing.T) {
	t.Parallel()
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprint(known), func(t *testing.T) {
			count := 2
			if known {
				count = 3
			}
			body := &observedBody{Reader: io.MultiReader(bytes.NewReader(eventBytes(t, fixtureEvents(t)[:count])), failedReader{io.ErrUnexpectedEOF})}
			client, calls := responseClient(t, 200, "text/event-stream", body)
			out, err := client.Run(context.Background(), fixtureRequest(t))
			if known {
				if err != nil || out.Evidence == nil || out.Result.Status != "succeeded" || out.Recording.Status != "unconfirmed" {
					t.Fatalf("lost known inference on body failure: %v", err)
				}
			} else {
				var unknown *UnknownOutcomeError
				if !errors.As(err, &unknown) || !errors.Is(err, io.ErrUnexpectedEOF) || out.Evidence != nil {
					t.Fatalf("partial inference became known: %v", err)
				}
			}
			if calls.Load() != 1 || !body.closed {
				t.Fatal("body failure replayed request or leaked its body")
			}
		})
	}
}

func TestRunTransportErrorIsUnknownWithoutReplay(t *testing.T) {
	t.Parallel()
	cause := errors.New("synthetic transport failure " + testAPIKey)
	var calls atomic.Int32
	client, err := New(Config{BaseURL: "https://fixture.example", APIKey: testAPIKey, HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, cause
	})}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Run(context.Background(), fixtureRequest(t))
	var unknown *UnknownOutcomeError
	if !errors.As(err, &unknown) || !errors.Is(err, cause) || calls.Load() != 1 || strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("transport failure not safely unknown: %v calls=%d", err, calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Run(ctx, fixtureRequest(t))
	if !errors.Is(err, context.Canceled) || errors.As(err, &unknown) || calls.Load() != 1 {
		t.Fatalf("already cancelled request dispatched or was misclassified: %v", err)
	}
}

func TestRunRejectsMalformedHTTPResponsesWithoutReplay(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"success_json", "success_html", "error_html", "error_json", "error_oversize"} {
		t.Run(name, func(t *testing.T) {
			status, kind, data := 200, "application/json", `{"result":{}}`
			switch name {
			case "success_html":
				kind = "text/html"
			case "error_html":
				status, kind = 503, "text/html"
			case "error_json":
				status, data = 502, `{"error":"not the error contract"}`
			case "error_oversize":
				status, data = 429, strings.Repeat("x", maximumErrorBytes+1)
			}
			body := &observedBody{Reader: strings.NewReader(data)}
			client, calls := responseClient(t, status, kind, body)
			_, err := client.Run(context.Background(), fixtureRequest(t))
			var unknown *UnknownOutcomeError
			if !errors.As(err, &unknown) || calls.Load() != 1 || !body.closed {
				t.Fatalf("malformed %s did not remain unknown/closed: %v", name, err)
			}
		})
	}
}

func TestRunPreservesNumberLexemesAndMultilineSSE(t *testing.T) {
	t.Parallel()
	events := fixtureEvents(t)
	const number = "9007199254740993123456789"
	terminalResult(events)["output"] = map[string]any{"n": json.Number(number)}
	var stream bytes.Buffer
	for _, event := range events {
		data, err := json.MarshalIndent(event, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&stream, ": comment\nevent: %s\n", event["type"])
		for _, line := range bytes.Split(data, []byte("\n")) {
			fmt.Fprintf(&stream, "data: %s\n", line)
		}
		stream.WriteByte('\n')
	}
	body := &observedBody{Reader: bytes.NewReader(stream.Bytes())}
	client, calls := responseClient(t, 200, "text/event-stream; charset=utf-8", body)
	input := fixtureRequest(t)
	input.Schema = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"n":{"type":"integer"}},"required":["n"]}`)
	out, err := client.Run(context.Background(), input)
	if err != nil || !bytes.Contains(out.Result.Output, []byte(number)) || !bytes.Contains(out.Result.Raw, []byte(number)) || calls.Load() != 1 || !body.closed {
		t.Fatalf("multiline SSE rounded/lost output: output=%s err=%v", out.Result.Output, err)
	}
}

func TestStreamReaderBoundsBeforeAccumulatingLongLines(t *testing.T) {
	t.Parallel()
	reader := streamReader{reader: bufio.NewReader(strings.NewReader("data: " + strings.Repeat("x", maximumResponseBytes)))}
	_, _, err := reader.frame()
	var protocol *ProtocolError
	if !errors.As(err, &protocol) || reader.bytes > maximumResponseBytes+4096 {
		t.Fatalf("frame allocation was not bounded: bytes=%d err=%v", reader.bytes, err)
	}
}
