package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/stateful"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type configurationError struct{ Setting, Message string }

func (e *configurationError) Error() string { return "configuration: " + e.Setting + ": " + e.Message }

type referenceSettings struct {
	databaseURL                                                                            string
	poolSize                                                                               int32
	vault                                                                                  *stateful.CredentialVault
	credentialKey                                                                          string
	historyEnabled                                                                         bool
	cacheTTL                                                                               time.Duration
	storageState, historyState, credentialsState, artifactState                            string
	storageError, artifactError                                                            *configurationError
	artifactEndpoint, artifactBucket, artifactRegion, artifactAccessKey, artifactSecretKey string
}

func referenceArtifacts(settings referenceSettings) (*stateful.ArtifactStore, *stateful.Diagnostic) {
	if settings.artifactState == "disabled" {
		return nil, nil
	}
	failure := func() (*stateful.ArtifactStore, *stateful.Diagnostic) {
		return nil, &stateful.Diagnostic{Code: "reference_configuration_error", Component: "artifacts", Operation: "configure", RequestID: "", InferenceOutcome: "independent", PersistenceOutcome: "unavailable", Cause: "configuration", Message: "Garage artifact settings are incomplete or invalid.", Action: "Configure HARDEN_LLM_ARTIFACT_ENDPOINT, BUCKET, ACCESS_KEY and SECRET_KEY together."}
	}
	if settings.artifactState != "configured" {
		return failure()
	}
	store, err := stateful.NewArtifactStore(stateful.ArtifactOptions{Endpoint: settings.artifactEndpoint, Bucket: settings.artifactBucket, Region: settings.artifactRegion, AccessKeyID: settings.artifactAccessKey, SecretAccessKey: settings.artifactSecretKey})
	if err != nil {
		return failure()
	}
	return store, nil
}

func parseAccessKeys(names []string, getenv func(string) string) ([]string, error) {
	if len(names) == 0 {
		return nil, &configurationError{Setting: "api_key_envs", Message: "a nonempty array of environment names is required"}
	}
	seenNames := map[string]bool{}
	seenValues := map[string]bool{}
	values := make([]string, 0, len(names))
	for _, name := range names {
		if !validEnvironmentName(name) || len(name) > 128 {
			return nil, &configurationError{Setting: "api_key_envs", Message: "contains an invalid environment name"}
		}
		if seenNames[name] {
			return nil, &configurationError{Setting: name, Message: "environment name is duplicated in api_key_envs"}
		}
		seenNames[name] = true
		value := getenv(name)
		if !auth.ValidToken(value) {
			return nil, &configurationError{Setting: name, Message: "a bounded printable bearer token is required"}
		}
		if seenValues[value] {
			return nil, &configurationError{Setting: name, Message: "resolves to a duplicate access token"}
		}
		seenValues[value] = true
		values = append(values, value)
	}
	return values, nil
}
func positiveInteger(getenv func(string) string, name string, defaultValue int64) (int64, error) {
	value := getenv(name)
	if value == "" {
		return defaultValue, nil
	}
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil || n <= 0 {
		return 0, &configurationError{Setting: name, Message: "must be a positive integer"}
	}
	return n, nil
}
func databaseSettings(getenv func(string) string) (referenceSettings, error) {
	result := referenceSettings{storageState: "disabled", databaseURL: strings.TrimSpace(getenv("HARDEN_LLM_DATABASE_URL"))}
	size, err := positiveInteger(getenv, "HARDEN_LLM_DATABASE_POOL_SIZE", 10)
	if err != nil {
		return result, err
	}
	result.poolSize = int32(size)
	if result.databaseURL == "" {
		return result, nil
	}
	parsed, err := url.Parse(result.databaseURL)
	if err != nil || parsed.Host == "" || parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		result.storageState = "configuration_error"
		result.storageError = &configurationError{Setting: "HARDEN_LLM_DATABASE_URL", Message: "must be a valid PostgreSQL connection URL"}
		return result, nil
	}
	if _, err = pgxpool.ParseConfig(result.databaseURL); err != nil {
		result.storageState = "configuration_error"
		result.storageError = &configurationError{Setting: "HARDEN_LLM_DATABASE_URL", Message: "PostgreSQL connection settings are invalid"}
		return result, nil
	}
	result.storageState = "configured"
	return result, nil
}
func loadReferenceSettings(getenv func(string) string) (referenceSettings, error) {
	result, err := databaseSettings(getenv)
	if err != nil {
		return result, err
	}
	result.historyState = "disabled"
	history := getenv("HARDEN_LLM_HISTORY_ENABLED")
	if history != "" && history != "true" && history != "false" {
		return result, &configurationError{Setting: "HARDEN_LLM_HISTORY_ENABLED", Message: "must be true or false"}
	}
	result.historyEnabled = history == "true"
	if result.historyEnabled {
		result.historyState = "configured"
		if result.storageState != "configured" {
			result.historyState = "configuration_error"
		}
	}
	result.credentialsState = "configuration_error"
	result.credentialKey = getenv("HARDEN_LLM_CREDENTIAL_KEY")
	if result.credentialKey != "" {
		result.vault, err = stateful.NewCredentialVault(result.credentialKey)
		if err != nil {
			return result, &configurationError{Setting: "HARDEN_LLM_CREDENTIAL_KEY", Message: "must be standard base64 of exactly 32 bytes"}
		}
		result.credentialsState = "configured"
	}
	seconds, err := positiveInteger(getenv, "HARDEN_LLM_CACHE_TTL_SECONDS", 86400)
	if err != nil {
		return result, err
	}
	result.cacheTTL = time.Duration(seconds) * time.Second
	result.artifactState = "disabled"
	result.artifactRegion = "garage"
	fields := []struct {
		name  string
		value *string
	}{{"HARDEN_LLM_ARTIFACT_ENDPOINT", &result.artifactEndpoint}, {"HARDEN_LLM_ARTIFACT_BUCKET", &result.artifactBucket}, {"HARDEN_LLM_ARTIFACT_ACCESS_KEY_ID", &result.artifactAccessKey}, {"HARDEN_LLM_ARTIFACT_SECRET_ACCESS_KEY", &result.artifactSecretKey}}
	present := 0
	for _, field := range fields {
		*field.value = getenv(field.name)
		if *field.value != "" {
			present++
		}
	}
	if present != 0 {
		result.artifactState = "configured"
		if present != len(fields) {
			result.artifactState = "configuration_error"
			result.artifactError = &configurationError{Setting: "HARDEN_LLM_ARTIFACT_*", Message: "endpoint, bucket, access key and secret key must be configured together"}
		}
		parsed, err := url.Parse(result.artifactEndpoint)
		if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			result.artifactState = "configuration_error"
			result.artifactError = &configurationError{Setting: "HARDEN_LLM_ARTIFACT_ENDPOINT", Message: "must be an HTTP or HTTPS endpoint without credentials, query or fragment"}
		}
	}
	return result, nil
}
func openReferencePool(ctx context.Context, settings referenceSettings) (*pgxpool.Pool, error) {
	if settings.storageState != "configured" {
		if settings.storageError != nil {
			return nil, settings.storageError
		}
		return nil, nil
	}
	config, err := pgxpool.ParseConfig(settings.databaseURL)
	if err != nil {
		return nil, &configurationError{Setting: "HARDEN_LLM_DATABASE_URL", Message: "PostgreSQL connection settings are invalid"}
	}
	config.MaxConns = settings.poolSize
	config.MinConns = 0
	config.MinIdleConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, &configurationError{Setting: "HARDEN_LLM_DATABASE_URL", Message: "cannot construct the PostgreSQL pool"}
	}
	return pool, nil
}
func runSchemaCommand(ctx context.Context, getenv func(string) string, apply func(context.Context, *pgxpool.Pool) error) error {
	settings, err := databaseSettings(getenv)
	if err != nil {
		return err
	}
	if settings.storageState != "configured" {
		return &configurationError{Setting: "HARDEN_LLM_DATABASE_URL", Message: "a valid PostgreSQL URL is required for schema initialization"}
	}
	if apply == nil {
		return errors.New("schema: schema initializer is required")
	}
	pool, err := openReferencePool(ctx, settings)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = apply(ctx, pool); err != nil {
		return fmt.Errorf("schema: initialization failed; inspect the HLLM database schema and permissions")
	}
	return nil
}
