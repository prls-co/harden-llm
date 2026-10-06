package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
	"github.com/prls-co/harden-llm/internal/redaction"
)

const (
	startupTimeout        = 30 * time.Second
	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 15 * time.Second
	httpIdleTimeout       = 60 * time.Second
	httpShutdownMargin    = 5 * time.Second
	httpMaxHeaderBytes    = 32 << 10
	telemetryShutdownTime = 2 * time.Second
)

func runGatewayServer(ctx context.Context, stdout, stderr io.Writer, getenv func(string) string) (returnErr error) {
	config, err := loadServerConfig(getenv)
	if err != nil {
		return err
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	redactor := configurationRedactor(config)
	startupContext, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()
	release := config.release
	if release == "" {
		release = version
	}
	telemetryRuntime, err := gateway.NewTelemetryRuntime(startupContext, gateway.TelemetryRuntimeConfig{
		Endpoint: config.otelEndpoint, ServiceName: config.serviceName, Environment: config.environment,
		Release: release, Stdout: stdout, Stderr: stderr, Redactor: redactor,
	})
	if err != nil {
		return safeStartupError(redactor, "configure telemetry", err)
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), telemetryShutdownTime)
		defer cancel()
		if shutdownErr := telemetryRuntime.Shutdown(shutdownContext); shutdownErr != nil && returnErr == nil {
			returnErr = safeStartupError(redactor, "shut down telemetry", shutdownErr)
		}
	}()
	client, err := gateway.NewStaticClient(hardenllm.Options{
		Connections: config.connections, DefaultConnection: config.defaultConnection,
		EndpointPolicy: hardenllm.EndpointPolicy{
			AllowedHosts: config.allowedHosts, PrivateAllowedHosts: config.privateAllowedHosts,
			PrivateAllowlist: config.privateAllowlist,
		},
		WebSearch: hardenllm.WebSearchOptions{JinaAPIKey: config.jinaAPIKey},
		Logger:    telemetryRuntime.Logger(), TracerProvider: telemetryRuntime.TracerProvider(), MeterProvider: telemetryRuntime.MeterProvider(),
	})
	if err != nil {
		return safeStartupError(redactor, "configure shared proxy client", err)
	}
	telemetry, err := gateway.NewTelemetry(telemetryRuntime.TracerProvider(), telemetryRuntime.MeterProvider())
	if err != nil {
		return safeStartupError(redactor, "configure HTTP telemetry", err)
	}
	var accepting atomic.Bool
	accepting.Store(true)
	api, err := httpapi.New(httpapi.Config{
		Token: config.token, Client: client, MaxRunDuration: config.maxRunDuration,
		Telemetry: telemetry, Logger: telemetryRuntime.Logger(),
		Readiness: []httpapi.ReadinessCheck{func(context.Context) error {
			if !accepting.Load() {
				return errors.New("gateway is draining")
			}
			return nil
		}},
	})
	if err != nil {
		return safeStartupError(redactor, "configure OpenAI HTTP API", err)
	}
	listener, err := (&net.ListenConfig{}).Listen(startupContext, "tcp", config.listenAddress)
	if err != nil {
		return safeStartupError(redactor, "listen", err)
	}
	defer listener.Close()
	serverContext, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	server := &http.Server{
		Handler: api.Handler(), ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout: httpReadTimeout, WriteTimeout: config.maxRunDuration + httpShutdownMargin,
		IdleTimeout: httpIdleTimeout, MaxHeaderBytes: httpMaxHeaderBytes,
		ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return serverContext },
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	telemetryRuntime.Logger().InfoContext(ctx, "gateway listening", "address", listener.Addr().String(), "environment", config.environment, "release", release)
	select {
	case serveErr := <-serveErrors:
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return safeStartupError(redactor, "serve HTTP", serveErr)
	case <-ctx.Done():
		accepting.Store(false)
		shutdownContext, cancelShutdown := context.WithTimeout(context.WithoutCancel(ctx), config.maxRunDuration+httpShutdownMargin)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownContext); err != nil {
			cancelServer()
			_ = server.Close()
			return safeStartupError(redactor, "shut down HTTP", err)
		}
		cancelServer()
		serveErr := <-serveErrors
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return safeStartupError(redactor, "serve HTTP", serveErr)
		}
		return nil
	}
}

func configurationRedactor(config serverConfig) *redaction.Redactor {
	secrets := []string{config.token, config.jinaAPIKey}
	for _, connection := range config.connections {
		secrets = append(secrets, connection.APIKey)
		for name, value := range connection.Headers {
			secrets = append(secrets, name, value)
		}
	}
	return redaction.New(secrets...)
}

func safeStartupError(redactor *redaction.Redactor, operation string, err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if redactor != nil {
		message = redactor.Text(message)
	}
	return fmt.Errorf("gateway: %s: %s", operation, message)
}
