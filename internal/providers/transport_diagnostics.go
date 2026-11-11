package providers

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/prls-co/harden-llm/internal/runtime"
)

// Capture only finite labels before transport normalization discards the raw
// error. Never retain Error(), addresses, URLs or provider/credential material.
// These labels describe local observations, not the identity of a failed hop.
func captureTransportFailure(d *runtime.StreamDiagnostics, parent context.Context, err error, phase string) {
	d.TransportErrorClass = transportErrorClass(err)
	// net/http's header timeout can wrap DeadlineExceeded while the logical
	// context is still live. Only the parent's own expiry proves that deadline.
	if errors.Is(err, context.DeadlineExceeded) && parent != nil && errors.Is(parent.Err(), context.DeadlineExceeded) {
		d.TransportErrorClass = "deadline"
	}
	d.TransportErrorPhase = phase
}

func transportErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	case errors.Is(err, io.EOF):
		return "eof"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.EPIPE):
		return "broken_pipe"
	case errors.Is(err, net.ErrClosed):
		return "connection_closed"
	case permanentTransportError(err):
		return "tls_or_transport_configuration"
	}
	var policy *endpointPolicyError
	if errors.As(err, &policy) {
		return "endpoint_policy"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return "network_error"
}
