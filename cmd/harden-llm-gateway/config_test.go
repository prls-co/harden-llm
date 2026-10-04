package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-023

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestServerConfiguration(t *testing.T) {
	environment := validServerEnvironment()
	config, err := loadServerConfig(mapEnvironment(environment))
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != defaultListenAddress || config.maxRunDuration != 60*time.Second || config.controlPlaneURL != "http://control-plane:8080" ||
		len(config.encryptionKeys) != 1 || len(config.privateAllowedHosts) != 1 || len(config.privateAllowlist) != 2 {
		t.Fatalf("configuration = %#v", config)
	}

	environment[staticTokenEnvironment] = strings.Repeat("s", 43)
	environment[staticTokenUserEnvironment] = "verification-user"
	environment[jinaAPIKeyEnvironment] = "fixture-jina-key"
	config, err = loadServerConfig(mapEnvironment(environment))
	if err != nil || config.staticToken != strings.Repeat("s", 43) || config.staticTokenUserID != "verification-user" || config.jinaAPIKey != "fixture-jina-key" {
		t.Fatalf("static token configuration = %#v, %v", config, err)
	}

	environment = validServerEnvironment()
	environment[staticTokenEnvironment] = strings.Repeat("s", 43)
	if _, err := loadServerConfig(mapEnvironment(environment)); err != nil {
		t.Fatalf("BFF service credential without machine account = %v", err)
	}
	environment = validServerEnvironment()
	delete(environment, staticTokenEnvironment)
	environment[staticTokenUserEnvironment] = "verification-user"
	if _, err := loadServerConfig(mapEnvironment(environment)); err == nil || !strings.Contains(err.Error(), staticTokenEnvironment) {
		t.Fatalf("machine account without service credential = %v", err)
	}

	environment = validServerEnvironment()
	environment[staticTokenEnvironment] = strings.Repeat("s", 31)
	environment[staticTokenUserEnvironment] = "verification-user"
	if _, err := loadServerConfig(mapEnvironment(environment)); err == nil || !strings.Contains(err.Error(), staticTokenEnvironment) {
		t.Fatalf("short static token configuration error = %v", err)
	}

	environment = validServerEnvironment()
	environment[maxRunDurationEnvironment] = "60001"
	if _, err := loadServerConfig(mapEnvironment(environment)); err == nil || !strings.Contains(err.Error(), maxRunDurationEnvironment) {
		t.Fatalf("invalid maximum run duration = %v", err)
	}
	environment = validServerEnvironment()
	environment[encryptionKeysEnvironment] = `{"key-1":"secret-key-material-must-not-leak"}`
	if _, err := loadServerConfig(mapEnvironment(environment)); err == nil || strings.Contains(err.Error(), "secret-key-material") {
		t.Fatalf("invalid key error leaked configuration: %v", err)
	}
	environment = validServerEnvironment()
	environment[environmentEnvironment] = "production"
	delete(environment, staticTokenEnvironment)
	if _, err := loadServerConfig(mapEnvironment(environment)); err == nil || !strings.Contains(err.Error(), staticTokenEnvironment) {
		t.Fatalf("missing production service credential = %v", err)
	}
	environment = validServerEnvironment()
	environment[environmentEnvironment] = "production"
	environment[artifactSecretKeyEnvironment] = strings.Repeat("0", 64)
	if _, err := loadServerConfig(mapEnvironment(environment)); err == nil || strings.Contains(err.Error(), strings.Repeat("0", 64)) {
		t.Fatalf("production default secret = %v", err)
	}
	environment = validServerEnvironment()
	environment[environmentEnvironment] = "production"
	environment[artifactExternalEnvironment] = "http://artifacts.internal:3900"
	if _, err := loadServerConfig(mapEnvironment(environment)); err == nil || !strings.Contains(err.Error(), artifactExternalEnvironment) {
		t.Fatalf("production artifact origin = %v", err)
	}
	environment = validServerEnvironment()
	environment[environmentEnvironment] = "staging"
	environment[artifactAccessKeyEnvironment] = strings.Repeat("0", 34)
	if _, err := loadServerConfig(mapEnvironment(environment)); err == nil {
		t.Fatal("non-development environment bypassed production secret checks")
	}
	environment = validServerEnvironment()
	environment[environmentEnvironment] = "production"
	delete(environment, otelEndpointEnvironment)
	if _, err := loadServerConfig(mapEnvironment(environment)); err == nil || !strings.Contains(err.Error(), otelEndpointEnvironment) {
		t.Fatalf("missing production telemetry endpoint = %v", err)
	}
	environment = validServerEnvironment()
	environment[releaseEnvironment] = "release\nforged"
	if _, err := loadServerConfig(mapEnvironment(environment)); err == nil || !strings.Contains(err.Error(), releaseEnvironment) {
		t.Fatalf("invalid release label = %v", err)
	}
	environment = validServerEnvironment()
	environment[listenAddressEnvironment] = "bad_host:8080"
	if _, err := loadServerConfig(mapEnvironment(environment)); err == nil || !strings.Contains(err.Error(), listenAddressEnvironment) {
		t.Fatalf("invalid listen address = %v", err)
	}
}

func validServerEnvironment() map[string]string {
	key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	return map[string]string{
		databaseURLEnvironment:         "postgres://harden:database-random-value@postgres:5432/harden?sslmode=require",
		encryptionKeysEnvironment:      `{"key-1":"` + key + `"}`,
		activeEncryptionKeyEnvironment: "key-1",
		artifactEndpointEnvironment:    "http://garage:3900",
		artifactExternalEnvironment:    "https://artifacts.example.test",
		artifactBucketEnvironment:      "harden-llm-artifacts",
		artifactAccessKeyEnvironment:   "GK12345678901234567890123456789012",
		artifactSecretKeyEnvironment:   "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ01",
		controlPlaneURLEnvironment:     "http://control-plane:8080",
		controlPlaneTokenEnvironment:   "control-plane-internal-token",
		staticTokenEnvironment:         "fixture-harden-llm-service-token-0123456789",
		environmentEnvironment:         "development",
		releaseEnvironment:             "v0.1.0-test",
		otelEndpointEnvironment:        "http://otel-collector:4317",
		privateAllowlistEnvironment:    "provider.internal,10.0.0.0/8,fd00::/8",
	}
}

func mapEnvironment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestRetiredCompanyTokenScopeCannotEnableDirectAccess(t *testing.T) {
	env := validServerEnvironment()
	env["HARDEN_LLM_STATIC_TOKEN_ACCOUNT_ID"] = "11111111-1111-4111-8111-111111111111"
	config, err := loadServerConfig(mapEnvironment(env))
	if err != nil || config.staticTokenUserID != "" {
		t.Fatalf("retired scope accepted: %v", err)
	}
	env[staticTokenUserEnvironment] = " padded"
	if _, err := loadServerConfig(mapEnvironment(env)); err == nil {
		t.Fatal("padded identity was normalized instead of rejected")
	}
}
