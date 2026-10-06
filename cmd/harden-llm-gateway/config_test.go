package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-404

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerConfigurationUsesOneTokenAndStaticConnections(t *testing.T) {
	environment := validServerEnvironment(t)
	config, err := loadServerConfig(mapEnvironment(environment))
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != defaultListenAddress || config.maxRunDuration != 60*time.Second || config.defaultConnection != "cpa" || len(config.connections) != 1 || config.connections[0].ID != "cpa" || config.connections[0].APIKey != environment["CPA_API_KEY"] {
		t.Fatalf("static configuration did not resolve once: %#v", config)
	}

	legacyOnly := validServerEnvironment(t)
	delete(legacyOnly, apiTokenEnvironment)
	legacyOnly["HARDEN_LLM_STATIC_TOKEN"] = strings.Repeat("x", 43)
	if _, err := loadServerConfig(mapEnvironment(legacyOnly)); err == nil || !strings.Contains(err.Error(), apiTokenEnvironment) {
		t.Fatalf("legacy token unexpectedly enabled the proxy: %v", err)
	}

	missingCredential := validServerEnvironment(t)
	delete(missingCredential, "CPA_API_KEY")
	if _, err := loadServerConfig(mapEnvironment(missingCredential)); err == nil || !strings.Contains(err.Error(), "CPA_API_KEY") {
		t.Fatalf("missing upstream credential was accepted: %v", err)
	}

	badDuration := validServerEnvironment(t)
	badDuration["HARDEN_LLM_MAX_RUN_DURATION_MS"] = "60001"
	if _, err := loadServerConfig(mapEnvironment(badDuration)); err == nil {
		t.Fatal("run timeout above 60 seconds was accepted")
	}

	badFile := validServerEnvironment(t)
	if err := os.WriteFile(badFile[connectionFileEnvironment], []byte(`{"default_upstream":"cpa","upstreams":[],"profiles":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServerConfig(mapEnvironment(badFile)); err == nil {
		t.Fatal("connection file accepted an obsolete profile catalog field")
	}

	production := validServerEnvironment(t)
	production[environmentEnvironment] = "production"
	production[releaseEnvironment] = "release-1"
	if _, err := loadServerConfig(mapEnvironment(production)); err != nil {
		t.Fatalf("valid production connection configuration failed: %v", err)
	}

	invalidRelease := validServerEnvironment(t)
	invalidRelease[releaseEnvironment] = "release\nforged"
	if _, err := loadServerConfig(mapEnvironment(invalidRelease)); err == nil {
		t.Fatal("control characters in release identity were accepted")
	}
	invalidListen := validServerEnvironment(t)
	invalidListen[listenAddressEnvironment] = "host.example:8080"
	if _, err := loadServerConfig(mapEnvironment(invalidListen)); err == nil {
		t.Fatal("non-IP listen host was accepted")
	}
}

func validServerEnvironment(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "upstreams.json")
	document := `{"default_upstream":"cpa","upstreams":[{"id":"cpa","provider":"cpa","protocol":"responses","base_url":"https://cpa.example.test/v1","api_key_env":"CPA_API_KEY","cache_domain":"cpa-deployment-1"}]}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		apiTokenEnvironment:       strings.Repeat("t", 43),
		connectionFileEnvironment: path,
		"CPA_API_KEY":             "synthetic-upstream-key",
		listenAddressEnvironment:  defaultListenAddress,
		environmentEnvironment:    "development",
		releaseEnvironment:        "v0.1.0-test",
		otelEndpointEnvironment:   "http://otel-collector:4317",
		privateHostsEnvironment:   "provider.internal,10.0.0.0/8,fd00::/8",
		allowedHostsEnvironment:   "cpa.example.test",
	}
}

func mapEnvironment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}
