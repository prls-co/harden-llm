//go:build integration && frontendintegration

package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-464 WEB-TEST-130

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type interruptedSSEWriter struct {
	http.ResponseWriter
	event       string
	beforeClose func() error
	closed      bool
}

func (w *interruptedSSEWriter) FlushError() error {
	if w.closed {
		return net.ErrClosed
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *interruptedSSEWriter) Write(data []byte) (int, error) {
	if w.closed {
		return 0, fmt.Errorf("owned test connection closed")
	}
	n, err := w.ResponseWriter.Write(data)
	if err == nil && bytes.Contains(data, []byte("event: "+w.event+"\n")) {
		if w.beforeClose != nil {
			if err = w.beforeClose(); err != nil {
				return n, err
			}
		}
		if err = w.FlushError(); err != nil {
			return n, err
		}
		connection, _, hijackErr := w.ResponseWriter.(http.Hijacker).Hijack()
		if hijackErr != nil {
			return n, hijackErr
		}
		w.closed = true
		err = connection.Close()
	}
	return n, err
}

func TestFrontendTransportInterruption(t *testing.T) {
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	shutdown := make(chan struct{})
	native := func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if bytes.Contains(data, []byte("interrupt-before-terminal")) {
			select {
			case started <- struct{}{}:
			case <-shutdown:
				return
			}
			select {
			case <-r.Context().Done():
				select {
				case canceled <- struct{}{}:
				case <-shutdown:
				}
			case <-shutdown:
				// Failure cleanup may release our fixture, but never proves cancellation.
			}
			return
		}
		var input map[string]any
		if err = json.Unmarshal(data, &input); err != nil {
			t.Error(err)
			return
		}
		writePresetNativeResponse(w, r, input, "OK")
	}
	fixture := referenceHTTPConfigured(t, true, false, nil, native)
	fixture.server.Close()
	handler := fixture.api.Handler()
	var lock sync.Mutex
	closed := map[string]int{}
	done := make(chan string, 2)
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/run" {
			handler.ServeHTTP(w, r)
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		var input struct {
			Prompt string `json:"userPrompt"`
		}
		if err = json.Unmarshal(data, &input); err != nil {
			t.Error(err)
			return
		}
		event := "run.completed"
		var barrier func() error
		if input.Prompt == "interrupt-before-terminal" {
			event = "run.progress"
			barrier = func() error {
				select {
				case <-started:
					return nil
				case <-time.After(5 * time.Second):
					return fmt.Errorf("provider did not start before interruption")
				}
			}
		} else if input.Prompt != "interrupt-after-terminal" {
			t.Errorf("unexpected transport fixture prompt")
			return
		}
		writer := &interruptedSSEWriter{ResponseWriter: w, event: event, beforeClose: barrier}
		handler.ServeHTTP(writer, r)
		if !writer.closed {
			t.Errorf("did not close owned transport at %s", event)
		}
		lock.Lock()
		closed[input.Prompt]++
		lock.Unlock()
		select {
		case done <- input.Prompt:
		default:
			t.Error("unexpected extra transport handler")
		}
	}))
	t.Cleanup(func() {
		close(shutdown)
		fixture.server.Close()
	})
	root, err := filepath.Abs("../../../frontend")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "mix", "test", "test/harden_llm_web/live/gateway_workflows_test.exs", "--only", "transport_workflow", "--seed", "104729")
	command.Dir = root
	command.Env = append(os.Environ(), "PORT=0", "HARDEN_LLM_TEST_GATEWAY_URL="+fixture.server.URL, "HARDEN_LLM_TEST_GATEWAY_TOKEN="+fixture.keyA, "MIX_BUILD_PATH="+filepath.Join(t.TempDir(), "mix-build"))
	output, err := command.CombinedOutput()
	logFrontendTestOutput(t, output)
	if err != nil {
		t.Fatalf("real transport workflow: %v", err)
	}
	for range 2 {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("interrupted handler did not terminate")
		}
	}
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("actual upstream request did not observe cancellation")
	}
	lock.Lock()
	defer lock.Unlock()
	if fixture.calls.Load() != 2 || closed["interrupt-before-terminal"] != 1 || closed["interrupt-after-terminal"] != 1 {
		t.Fatalf("transport replay/counts: calls=%d closures=%v", fixture.calls.Load(), closed)
	}
	// API reads establish that the detached real PostgreSQL writes have finished;
	// the UI tests already read both exact records through this same API.
	var count int
	if err = fixture.pool.QueryRow(context.Background(), "SELECT count(*) FROM history").Scan(&count); err != nil || count != 2 {
		t.Fatalf("terminal history count=%d err=%v", count, err)
	}
}
