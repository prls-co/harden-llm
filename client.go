package hardenllm

import (
	"context"
	"errors"
	"log/slog"
)

var errRuntimeUnavailable = errors.New("hardenllm: runtime is not initialized")

// Client owns immutable dependencies for provider-neutral calls.
type Client struct {
	options Options
}

// New constructs a client without changing global logging or telemetry state.
func New(options Options) (*Client, error) {
	if options.Logger == nil {
		options.Logger = slog.New(slog.DiscardHandler)
	}
	return &Client{options: options}, nil
}

// Call executes one provider-neutral LLM request.
func (client *Client) Call(_ context.Context, _ Request) (Result, error) {
	return Result{}, errRuntimeUnavailable
}
