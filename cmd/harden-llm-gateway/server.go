package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/artifacts"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
	"github.com/prls-co/harden-llm/internal/providers"
	"github.com/prls-co/harden-llm/internal/redaction"
	"github.com/prls-co/harden-llm/internal/traces"
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

type providerModelRefresher struct{ discovery *providers.ModelDiscovery }

func (refresher providerModelRefresher) RefreshModels(ctx context.Context, profile profiles.Profile, credential profiles.CredentialPayload) ([]profiles.Model, error) {
	if refresher.discovery == nil {
		return nil, errors.New("gateway: model discovery is unavailable")
	}
	discovered, err := refresher.discovery.Discover(ctx, providers.ModelDiscoveryRequest{
		BaseURL: profile.BaseURL, APIInferenceType: profile.APIInferenceType,
		APIKey: credential.APIKey, Headers: credential.Headers,
	})
	if err != nil {
		return nil, err
	}
	models := make([]profiles.Model, 0, len(discovered))
	for _, model := range discovered {
		models = append(models, profiles.Model{ID: model.ID, Label: model.Label})
	}
	return models, nil
}

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
	telemetryRuntime, err := gateway.NewTelemetryRuntime(startupContext, gateway.TelemetryRuntimeConfig{
		Endpoint: config.otelEndpoint, ServiceName: config.serviceName, Environment: config.environment,
		Release: config.release, Stdout: stdout, Stderr: stderr, Redactor: redactor,
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
	logger := telemetryRuntime.Logger()
	gatewayTelemetry, err := gateway.NewTelemetry(telemetryRuntime.TracerProvider(), telemetryRuntime.MeterProvider())
	if err != nil {
		return safeStartupError(redactor, "configure gateway telemetry", err)
	}

	store, err := postgres.Open(startupContext, config.databaseURL, postgres.WithTelemetry(
		telemetryRuntime.TracerProvider(), telemetryRuntime.MeterProvider(),
	))
	if err != nil {
		return safeStartupError(redactor, "open application database", err)
	}
	defer store.Close()
	if err := store.Migrate(startupContext); err != nil {
		return safeStartupError(redactor, "apply application migrations", err)
	}
	garageStore, err := artifacts.NewGarage(artifacts.Config{
		Endpoint: config.artifactEndpoint, ExternalEndpoint: config.artifactExternal,
		Bucket: config.artifactBucket, Region: "garage", AccessKeyID: config.artifactAccessKey,
		SecretAccessKey: config.artifactSecretKey, MaxPresignTTL: config.artifactPresignTTL,
		TracerProvider: telemetryRuntime.TracerProvider(), MeterProvider: telemetryRuntime.MeterProvider(),
	})
	if err != nil {
		return safeStartupError(redactor, "configure artifact store", err)
	}
	vault, err := profiles.NewCredentialVault(config.activeEncryptionKey, config.encryptionKeys, nil)
	if err != nil {
		return safeStartupError(redactor, "configure credential encryption", err)
	}
	rootPolicy := hardenllm.EndpointPolicy{
		AllowedHosts: config.allowedHosts, PrivateAllowedHosts: config.privateAllowedHosts,
		PrivateAllowlist: config.privateAllowlist,
	}
	providerPolicy := providers.EndpointPolicy{
		AllowedHosts: config.allowedHosts, PrivateAllowedHosts: config.privateAllowedHosts,
		PrivateAllowlist: config.privateAllowlist,
	}
	modelDiscovery, err := providers.NewModelDiscovery(providerPolicy)
	if err != nil {
		return safeStartupError(redactor, "configure model discovery", err)
	}
	rootOptions := hardenllm.Options{
		EndpointPolicy: rootPolicy, Logger: logger,
		TracerProvider: telemetryRuntime.TracerProvider(), MeterProvider: telemetryRuntime.MeterProvider(),
	}
	profileProber, err := gateway.NewSharedRootProfileProber(rootOptions)
	if err != nil {
		return safeStartupError(redactor, "configure profile probe", err)
	}
	seedCatalog, err := profiles.DefaultCatalog()
	if err != nil {
		return safeStartupError(redactor, "load default profile catalog", err)
	}
	profileService, err := gateway.NewProfileService(gateway.ProfileServiceConfig{
		Store: store, Vault: vault, Prober: profileProber, SeedCatalog: seedCatalog, Logger: logger,
	})
	if err != nil {
		return safeStartupError(redactor, "configure profile service", err)
	}
	artifactPrefix := func(ownerID string) string {
		return "llm-traces/" + traces.SafeObjectKeyComponent(ownerID) + "/"
	}
	artifactCoordinator, err := gateway.NewArtifactCoordinator(gateway.ArtifactCoordinatorConfig{
		Store: store, Logger: logger, Telemetry: gatewayTelemetry,
		Scope: func(ownerID string) (gateway.ArtifactObjectAccess, error) {
			return garageStore.Scoped(artifactPrefix(ownerID))
		},
	})
	if err != nil {
		return safeStartupError(redactor, "configure artifact coordinator", err)
	}
	resourceService, err := gateway.NewResourceService(gateway.ResourceServiceConfig{
		Store: store, Profiles: profileService, ModelRefresher: providerModelRefresher{discovery: modelDiscovery},
		ArtifactTTL: config.artifactPresignTTL,
		Telemetry:   gatewayTelemetry,
		Artifacts:   artifactCoordinator,
	})
	if err != nil {
		return safeStartupError(redactor, "configure resource service", err)
	}
	callerFactory, err := gateway.NewSharedRuntimeCallerFactory(rootOptions)
	if err != nil {
		return safeStartupError(redactor, "configure runtime", err)
	}
	runService, err := gateway.NewRunService(gateway.RunServiceConfig{
		Store: store, Profiles: profileService, CallerFactory: callerFactory,
		ArtifactScope: func(ownerID string) (hardenllm.ArtifactStore, error) {
			return artifactCoordinator.Scoped(ownerID)
		},
		Telemetry: gatewayTelemetry, Logger: logger,
	})
	if err != nil {
		return safeStartupError(redactor, "configure run service", err)
	}
	identity, err := auth.NewService(auth.Config{
		Store: store, SessionTTL: config.sessionTTL,
		StaticToken: config.staticToken, StaticTokenOwnerID: config.staticTokenOwnerID,
	})
	if err != nil {
		return safeStartupError(redactor, "configure identity service", err)
	}
	var accepting atomic.Bool
	accepting.Store(true)
	api, err := httpapi.New(httpapi.Config{
		Auth: identity, Resources: resourceService, Runs: runService, MaxRunDuration: config.maxRunDuration,
		Telemetry: gatewayTelemetry, Logger: logger,
		Readiness: []httpapi.ReadinessCheck{
			func(context.Context) error {
				if !accepting.Load() {
					return errors.New("gateway is draining")
				}
				return nil
			},
			store.Ready,
			garageStore.Ready,
		},
	})
	if err != nil {
		return safeStartupError(redactor, "configure HTTP API", err)
	}

	listener, err := (&net.ListenConfig{}).Listen(startupContext, "tcp", config.listenAddress)
	if err != nil {
		return safeStartupError(redactor, "listen", err)
	}
	defer listener.Close()
	baseContext, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	go artifactCoordinator.RunReconciler(baseContext)
	boundedOperationDuration := config.maxRunDuration
	if boundedOperationDuration < 15*time.Second {
		boundedOperationDuration = 15 * time.Second
	}
	server := &http.Server{
		Handler: api.Handler(), ReadHeaderTimeout: httpReadHeaderTimeout, ReadTimeout: httpReadTimeout,
		WriteTimeout: boundedOperationDuration + httpShutdownMargin, IdleTimeout: httpIdleTimeout,
		MaxHeaderBytes: httpMaxHeaderBytes, ErrorLog: log.New(io.Discard, "", 0),
		BaseContext: func(net.Listener) context.Context { return baseContext },
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	logger.InfoContext(ctx, "gateway listening", "address", listener.Addr().String(), "environment", config.environment, "release", config.release)

	select {
	case serveErr := <-serveErrors:
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return safeStartupError(redactor, "serve HTTP", serveErr)
	case <-ctx.Done():
		accepting.Store(false)
		shutdownContext, cancelShutdown := context.WithTimeout(context.WithoutCancel(ctx), boundedOperationDuration+httpShutdownMargin)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownContext); err != nil {
			cancelBase()
			_ = server.Close()
			return safeStartupError(redactor, "shut down HTTP", err)
		}
		cancelBase()
		serveErr := <-serveErrors
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return safeStartupError(redactor, "serve HTTP", serveErr)
		}
		return nil
	}
}

func configurationRedactor(config serverConfig) *redaction.Redactor {
	secrets := []string{config.databaseURL, config.artifactAccessKey, config.artifactSecretKey, config.staticToken}
	if parsed, err := url.Parse(config.databaseURL); err == nil && parsed.User != nil {
		if password, ok := parsed.User.Password(); ok {
			secrets = append(secrets, password)
		}
	}
	for _, key := range config.encryptionKeys {
		secrets = append(secrets, string(key))
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
