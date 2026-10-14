package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-465

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

type failedFlushWriter struct {
	http.ResponseWriter
	flushes int
}

func (w *failedFlushWriter) FlushError() error {
	w.flushes++
	return net.ErrClosed
}

func TestStatusWriterFlushError(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	failed := &failedFlushWriter{ResponseWriter: recorder}
	writer := &statusWriter{ResponseWriter: failed}
	if err := http.NewResponseController(writer).Flush(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("logging wrapper concealed flush failure: %v", err)
	}
	if !writer.wrote || writer.status != http.StatusOK || recorder.Code != http.StatusOK || failed.flushes != 1 {
		t.Fatal("flush status/dispatch differs")
	}
}

func TestStatusWriterFlushSuccess(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	writer := &statusWriter{ResponseWriter: recorder}
	writer.WriteHeader(http.StatusAccepted)
	if err := http.NewResponseController(writer).Flush(); err != nil {
		t.Fatal(err)
	}
	if !recorder.Flushed || recorder.Code != http.StatusAccepted || writer.status != http.StatusAccepted {
		t.Fatal("successful flush lost explicit status or did not flush")
	}
}

func TestStatusWriterFlushUnsupported(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	// Expose only ResponseWriter, deliberately hiding the recorder's Flusher.
	unflushable := struct{ http.ResponseWriter }{recorder}
	writer := &statusWriter{ResponseWriter: unflushable}
	if err := http.NewResponseController(writer).Flush(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("logging wrapper concealed unsupported flush: %v", err)
	}
	if recorder.Flushed {
		t.Fatal("unsupported writer reported a successful flush")
	}
}
