package providers

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-542.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/runtime"
)

func TestTransportFailureLabelsDoNotRetainRawErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err   error
		class string
	}{
		{io.ErrUnexpectedEOF, "unexpected_eof"},
		{io.EOF, "eof"},
		{syscall.ECONNRESET, "connection_reset"},
		{syscall.ECONNREFUSED, "connection_refused"},
		{syscall.EPIPE, "broken_pipe"},
		{net.ErrClosed, "connection_closed"},
		{&net.DNSError{Name: "private-host", Err: "private driver text"}, "dns"},
		{context.DeadlineExceeded, "timeout"},
		{context.Canceled, "canceled"},
		{&endpointPolicyError{err: errors.New("private endpoint")}, "endpoint_policy"},
		{errors.New("private unknown error"), "network_error"},
	} {
		t.Run(tc.class, func(t *testing.T) {
			raw := &url.Error{Op: "Post", URL: "https://private-key@private-host", Err: fmt.Errorf("private SQL or provider output: %w", tc.err)}
			var d runtime.StreamDiagnostics
			captureTransportFailure(&d, t.Context(), raw, "response_headers")
			if d.TransportErrorClass != tc.class || d.TransportErrorPhase != "response_headers" {
				t.Fatalf("safe error classification lost: %+v", d)
			}
			encoded, err := json.Marshal(d)
			if err != nil || string(encoded) != "{}" {
				t.Fatal("internal labels changed serialized diagnostics", string(encoded), err)
			}
		})
	}
	parent, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
	defer cancel()
	var d runtime.StreamDiagnostics
	captureTransportFailure(&d, parent, context.DeadlineExceeded, "response_stream")
	if d.TransportErrorClass != "deadline" {
		t.Fatal("confirmed logical deadline mislabeled", d)
	}
	captureTransportFailure(&d, parent, syscall.ECONNRESET, "response_stream")
	if d.TransportErrorClass != "connection_reset" {
		t.Fatal("logical stop erased the original socket failure", d)
	}
	local, stop := context.WithDeadline(t.Context(), time.Unix(0, 0))
	defer stop()
	captureTransportFailure(&d, t.Context(), local.Err(), "response_stream")
	if d.TransportErrorClass != "timeout" {
		t.Fatal("narrow transport deadline mislabeled as logical deadline", d)
	}
}
