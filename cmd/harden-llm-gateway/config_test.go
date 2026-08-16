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
	if config.listenAddress != defaultListenAddress || config.maxRunDuration != 60*time.Second || config.sessionTTL != 24*time.Hour ||
		len(config.encryptionKeys) != 1 || len(config.privateAllowedHosts) != 1 || len(config.privateAllowlist) != 2 {
		t.Fatalf("configuration = %#v", config)
	}

	environment[staticTokenEnvironment] = strings.Repeat("s", 43)
	environment[staticTokenOwnerEnvironment] = "operator-01"
	config, err = loadServerConfig(mapEnvironment(environment))
	if err != nil || config.staticToken != strings.Repeat("s", 43) || config.staticTokenOwnerID != "operator-01" {
		t.Fatalf("static token configuration = %#v, %v", config, err)
	}

	for name, values := range map[string]string{
		"missing owner": staticTokenEnvironment,
		"missing token": staticTokenOwnerEnvironment,
	} {
		environment = validServerEnvironment()
		if name == "missing owner" {
			environment[staticTokenEnvironment] = strings.Repeat("s", 43)
		} else {
			environment[staticTokenOwnerEnvironment] = "operator-01"
		}
		if _, err := loadServerConfig(mapEnvironment(environment)); err == nil || !strings.Contains(err.Error(), values) {
			t.Fatalf("%s configuration error = %v", name, err)
		}
	}

	environment = validServerEnvironment()
	environment[staticTokenEnvironment] = strings.Repeat("s", 31)
	environment[staticTokenOwnerEnvironment] = "operator-01"
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
		environmentEnvironment:         "development",
		releaseEnvironment:             "v0.1.0-test",
		otelEndpointEnvironment:        "http://otel-collector:4317",
		privateAllowlistEnvironment:    "provider.internal,10.0.0.0/8,fd00::/8",
	}
}

func mapEnvironment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
