package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-261 TEST-262 TEST-263

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway"
)

func sseTestOutcome(err error) runOutcome {
	return runOutcome{
		result: gateway.RunOutput{SchemaVersion: 4, RunID: "run-sse", Output: "ok", CallID: "call-sse", TraceID: "trace-sse"},
		state:  gateway.RunState{LastRunID: "run-sse", LastTraceID: "trace-sse"},
		err:    err,
	}
}

func sseTestProgress() hardenllm.ProgressEvent {
	return hardenllm.ProgressEvent{
		SchemaVersion: 1, Sequence: 1, RunID: "run-sse", CallID: "call-sse", TraceID: "trace-sse",
		Type: "run.started", Stage: "original.generate", Branch: "original", Terminal: false,
	}
}

func waitSSE(t *testing.T, run func()) string {
	t.Helper()
	done := make(chan struct{})
	go func() {
		run()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE stream did not terminate within the lifecycle watchdog")
	}
	return ""
}

func decodeSSEEvents(t *testing.T, body string) []streamEnvelope {
	t.Helper()
	var events []streamEnvelope
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event streamEnvelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatalf("decode SSE event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func assertOneTerminal(t *testing.T, events []streamEnvelope, expected string) {
	t.Helper()
	terminal := 0
	for _, event := range events {
		if event.Type == "run.completed" || event.Type == "run.failed" {
			terminal++
			if event.Type != expected {
				t.Fatalf("terminal event = %q, want %q", event.Type, expected)
			}
		}
	}
	if terminal != 1 {
		t.Fatalf("terminal event count = %d, want one; events=%#v", terminal, events)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event sequence at %d = %d", index, event.Sequence)
		}
	}
}

func TestSSEPendingAndLaterOutcomesTerminateOnce(t *testing.T) {
	t.Run("pending outcome is emitted without a second receive", func(t *testing.T) {
		progress := make(chan hardenllm.ProgressEvent, 1)
		progress <- sseTestProgress()
		close(progress)
		outcomes := make(chan runOutcome)
		recorder := httptest.NewRecorder()
		requestContext := context.Background()
		executionContext := context.Background()
		pending := sseTestOutcome(nil)

		waitSSE(t, func() {
			streamSSE(recorder, requestContext, executionContext, progress, outcomes, &pending, nil)
		})
		events := decodeSSEEvents(t, recorder.Body.String())
		assertOneTerminal(t, events, "run.completed")
		if len(events) != 2 || events[0].Type != "run.started" {
			t.Fatalf("pending stream events = %#v", events)
		}
		if recorder.Code != http.StatusOK {
			t.Fatalf("pending stream status = %d", recorder.Code)
		}
	})

	t.Run("pending completion wins over an expired execution context", func(t *testing.T) {
		progress := make(chan hardenllm.ProgressEvent)
		close(progress)
		executionContext, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		recorder := httptest.NewRecorder()
		pending := sseTestOutcome(nil)
		waitSSE(t, func() {
			streamSSE(recorder, context.Background(), executionContext, progress, make(chan runOutcome), &pending, nil)
		})
		events := decodeSSEEvents(t, recorder.Body.String())
		assertOneTerminal(t, events, "run.completed")
	})

	t.Run("later outcome is emitted after progress closes", func(t *testing.T) {
		progress := make(chan hardenllm.ProgressEvent, 1)
		progress <- sseTestProgress()
		close(progress)
		outcomes := make(chan runOutcome, 1)
		recorder := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			streamSSE(recorder, context.Background(), context.Background(), progress, outcomes, nil, nil)
			close(done)
		}()
		outcomes <- sseTestOutcome(nil)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("later-outcome stream did not terminate")
		}
		events := decodeSSEEvents(t, recorder.Body.String())
		assertOneTerminal(t, events, "run.completed")
		if len(events) != 2 {
			t.Fatalf("later stream events = %#v", events)
		}
	})
}

func TestSSEDeadlineAndCancellationReleaseHandler(t *testing.T) {
	t.Run("pre-admission expiry retains JSON timeout", func(t *testing.T) {
		requestContext := context.Background()
		executionContext, cancel := context.WithCancel(context.Background())
		cancel()
		admission := awaitSSEAdmission(requestContext, executionContext, make(chan struct{}), make(chan runOutcome))
		if admission.admitted || !admission.timedOut || admission.canceled {
			t.Fatalf("expired admission = %#v", admission)
		}
		recorder := httptest.NewRecorder()
		api := &API{}
		api.writeRunError(recorder, executionContext, runOutcome{err: context.DeadlineExceeded})
		if recorder.Code != http.StatusGatewayTimeout || !strings.Contains(recorder.Body.String(), "run_timeout") {
			t.Fatalf("pre-admission timeout = %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("post-admission expiry emits one failure", func(t *testing.T) {
		executionContext, cancelExecution := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancelExecution()
		recorder := httptest.NewRecorder()
		waitSSE(t, func() {
			streamSSE(recorder, context.Background(), executionContext, make(chan hardenllm.ProgressEvent), make(chan runOutcome), nil, nil)
		})
		events := decodeSSEEvents(t, recorder.Body.String())
		assertOneTerminal(t, events, "run.failed")
		if !strings.Contains(recorder.Body.String(), "run_timeout") {
			t.Fatalf("post-admission timeout omitted run_timeout: %s", recorder.Body.String())
		}
	})

	t.Run("client cancellation writes no terminal", func(t *testing.T) {
		requestContext, cancelRequest := context.WithCancel(context.Background())
		cancelRequest()
		cancelCalled := make(chan struct{})
		cancel := func() { close(cancelCalled) }
		recorder := httptest.NewRecorder()
		waitSSE(t, func() {
			streamSSE(recorder, requestContext, context.Background(), make(chan hardenllm.ProgressEvent), make(chan runOutcome), nil, cancel)
		})
		if recorder.Body.Len() != 0 {
			t.Fatalf("canceled stream wrote %s", recorder.Body.String())
		}
		select {
		case <-cancelCalled:
		case <-time.After(2 * time.Second):
			t.Fatal("client cancellation did not cancel execution")
		}
	})
}

type failingSSEWriter struct {
	header http.Header
	code   int
}

func (writer *failingSSEWriter) Header() http.Header  { return writer.header }
func (writer *failingSSEWriter) WriteHeader(code int) { writer.code = code }
func (writer *failingSSEWriter) Write([]byte) (int, error) {
	return 0, errors.New("fixture write failure")
}
func (writer *failingSSEWriter) Flush() {}

func TestSSEChannelAndWriterOwnershipSurviveTermination(t *testing.T) {
	t.Run("write failure cancels execution", func(t *testing.T) {
		writer := &failingSSEWriter{header: make(http.Header)}
		executionContext, cancelExecution := context.WithCancel(context.Background())
		defer cancelExecution()
		pending := sseTestOutcome(nil)
		waitSSE(t, func() {
			streamSSE(writer, context.Background(), executionContext, make(chan hardenllm.ProgressEvent), make(chan runOutcome), &pending, cancelExecution)
		})
		select {
		case <-executionContext.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("write failure did not cancel execution")
		}
	})

	t.Run("late worker result uses producer-owned channels", func(t *testing.T) {
		progress := make(chan hardenllm.ProgressEvent, 1)
		outcomes := make(chan runOutcome, 1)
		requestContext, cancelRequest := context.WithCancel(context.Background())
		cancelRequest()
		waitSSE(t, func() {
			streamSSE(httptest.NewRecorder(), requestContext, context.Background(), progress, outcomes, nil, nil)
		})
		workerDone := make(chan struct{})
		go func() {
			outcomes <- sseTestOutcome(errors.New("late failure"))
			close(progress)
			close(workerDone)
		}()
		select {
		case <-workerDone:
		case <-time.After(2 * time.Second):
			t.Fatal("late worker result blocked after handler return")
		}
	})
}
