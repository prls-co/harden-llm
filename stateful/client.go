// Package stateful composes concrete effects around the stateless hardening core.
package stateful

import (
	"context"
	"fmt"
	"github.com/prls-co/harden-llm/internal/observability"
	"github.com/prls-co/harden-llm/internal/providers"
	"log/slog"
	"strings"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type ClientOptions struct {
	Connections       []hardenllm.Connection
	DefaultConnection string
	Cache             hardenllm.CacheStore
	EndpointPolicy    hardenllm.EndpointPolicy
	WebSearch         WebSearchOptions
	TracerProvider    trace.TracerProvider
	MeterProvider     metric.MeterProvider
	Logger            *slog.Logger
}

func NewClient(options ClientOptions) (*hardenllm.Client, error) {
	coreOptions, err := newEffects(options)
	if err != nil {
		return nil, err
	}
	return hardenllm.New(coreOptions)
}

func newEffects(options ClientOptions) (hardenllm.Options, error) {
	connections := make(hardenllm.ConnectionCatalog, len(options.Connections))
	for _, connection := range options.Connections {
		id := strings.TrimSpace(connection.ID)
		if _, exists := connections[id]; exists {
			return hardenllm.Options{}, fmt.Errorf("stateful: connection %q is duplicated", id)
		}
		connection.ID = id
		connection = connection.Clone()
		connections[id] = connection
	}

	policy := providers.EndpointPolicy{AllowedHosts: options.EndpointPolicy.AllowedHosts, PrivateAllowedHosts: options.EndpointPolicy.PrivateAllowedHosts, PrivateAllowlist: options.EndpointPolicy.PrivateAllowlist, Resolver: options.EndpointPolicy.Resolver, DialContext: options.EndpointPolicy.DialContext, TLSConfig: options.EndpointPolicy.TLSConfig, ConnectTimeout: options.EndpointPolicy.ConnectTimeout, TLSHandshakeTimeout: options.EndpointPolicy.TLSHandshakeTimeout, ResponseHeaderTimeout: options.EndpointPolicy.ResponseHeaderTimeout, MaxConnsPerHost: options.EndpointPolicy.MaxConnsPerHost}
	executor, err := providers.NewRouter(providers.Config{EndpointPolicy: policy, Logger: options.Logger, JinaAPIKey: options.WebSearch.JinaAPIKey, JinaTimeout: options.WebSearch.JinaTimeout, JinaMaxResponseBytes: options.WebSearch.JinaMaxResponseBytes})
	if err != nil {
		return hardenllm.Options{}, err
	}
	discovery, err := providers.NewModelDiscovery(policy)
	if err != nil {
		return hardenllm.Options{}, err
	}
	observer, err := observability.NewTelemetry(options.TracerProvider, options.MeterProvider)
	if err != nil {
		return hardenllm.Options{}, err
	}
	return hardenllm.Options{Connections: connections, DefaultConnection: options.DefaultConnection, Cache: options.Cache, Executor: executor, Observer: observer, DiscoverModels: func(ctx context.Context, c hardenllm.Connection) ([]hardenllm.Model, error) {
		found, err := discovery.Discover(ctx, providers.ModelDiscoveryRequest{BaseURL: c.BaseURL, APIInferenceType: c.Protocol, APIKey: c.APIKey, Headers: c.Headers})
		if err != nil {
			return nil, err
		}
		models := make([]hardenllm.Model, len(found))
		for i, m := range found {
			models[i] = hardenllm.Model{ID: m.ID, Label: m.Label, OwnedBy: c.Provider}
		}
		return models, nil
	}}, nil
}

// WebSearchOptions configures the explicitly selected external search service.
type WebSearchOptions struct {
	JinaAPIKey           string
	JinaTimeout          time.Duration
	JinaMaxResponseBytes int64
}
