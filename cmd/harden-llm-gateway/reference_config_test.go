package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-449

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
	"os"
	"strings"
	"testing"
)

func referenceEnvironment(t *testing.T) map[string]string {
	t.Helper()
	env := validServerEnvironment(t)
	setIncomingNames(t, env, []string{"HARDEN_LLM_TOKEN"})
	return env
}
func setIncomingNames(t *testing.T, env map[string]string, names []string) {
	t.Helper()
	raw, err := os.ReadFile(env[connectionFileEnvironment])
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["api_key_envs"] = names
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(env[connectionFileEnvironment], raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestReferenceConfig(t *testing.T) {
	t.Parallel()
	t.Run("accepted access keys resolve once from required metadata", func(t *testing.T) {
		env := referenceEnvironment(t)
		reads := map[string]int{}
		cfg, err := loadServerConfig(func(name string) string { reads[name]++; return env[name] })
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.apiKeys) != 1 || cfg.apiKeys[0] != env["HARDEN_LLM_TOKEN"] || reads["HARDEN_LLM_TOKEN"] != 1 {
			t.Fatal("accepted keys missing or reread")
		}
		env = referenceEnvironment(t)
		raw, err := os.ReadFile(env[connectionFileEnvironment])
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		_ = json.Unmarshal(raw, &doc)
		delete(doc, "api_key_envs")
		raw, _ = json.Marshal(doc)
		_ = os.WriteFile(env[connectionFileEnvironment], raw, 0600)
		_, err = loadServerConfig(mapEnvironment(env))
		var typed *configurationError
		if !errors.As(err, &typed) || !strings.Contains(err.Error(), "api_key_envs") {
			t.Fatal("missing array must reject scalar fallback", err)
		}
	})
	t.Run("each invalid name and resolved secret is typed and never echoed", func(t *testing.T) {
		for _, kind := range []string{"empty", "invalid-name", "duplicate-name", "unset", "empty-value", "invalid-value", "duplicate-value"} {
			t.Run(kind, func(t *testing.T) {
				env := referenceEnvironment(t)
				names := []string{"SECOND_TOKEN"}
				env["SECOND_TOKEN"] = "synthetic-secret-sentinel-0123456789abcdef"
				switch kind {
				case "empty":
					names = []string{""}
				case "invalid-name":
					names = []string{"invalid-name"}
				case "duplicate-name":
					names = []string{"HARDEN_LLM_TOKEN", "HARDEN_LLM_TOKEN"}
				case "unset":
					delete(env, "SECOND_TOKEN")
				case "empty-value":
					env["SECOND_TOKEN"] = ""
				case "invalid-value":
					env["SECOND_TOKEN"] = "secret-sentinel\n"
				case "duplicate-value":
					names = []string{"HARDEN_LLM_TOKEN", "SECOND_TOKEN"}
					env["SECOND_TOKEN"] = env["HARDEN_LLM_TOKEN"]
				}
				setIncomingNames(t, env, names)
				_, err := loadServerConfig(mapEnvironment(env))
				var typed *configurationError
				if !errors.As(err, &typed) {
					t.Fatal("invalid access key config is not typed", err)
				}
				if strings.Contains(err.Error(), "secret-sentinel") || strings.Contains(err.Error(), env["HARDEN_LLM_TOKEN"]) {
					t.Fatal("configuration error exposed a key")
				}
			})
		}
	})
	t.Run("literal key field is not a connection metadata field", func(t *testing.T) {
		env := referenceEnvironment(t)
		raw, _ := os.ReadFile(env[connectionFileEnvironment])
		var doc map[string]any
		_ = json.Unmarshal(raw, &doc)
		doc["api_key"] = "secret-sentinel"
		raw, _ = json.Marshal(doc)
		_ = os.WriteFile(env[connectionFileEnvironment], raw, 0600)
		if _, err := loadServerConfig(mapEnvironment(env)); err == nil || strings.Contains(err.Error(), "secret-sentinel") {
			t.Fatal("literal incoming key accepted or echoed", err)
		}
	})
	t.Run("absent database and disabled history have explicit feature states", func(t *testing.T) {
		env := referenceEnvironment(t)
		env["HARDEN_LLM_HISTORY_ENABLED"] = "false"
		cfg, err := loadServerConfig(mapEnvironment(env))
		if err != nil || cfg.reference.storageState != "disabled" || cfg.reference.historyState != "disabled" {
			t.Fatal("disabled features misreported", err, cfg.reference.storageState, cfg.reference.historyState)
		}
	})
	t.Run("enabled history with missing or invalid database remains configuration error", func(t *testing.T) {
		for _, url := range []string{"", "not-a-postgres-url"} {
			env := referenceEnvironment(t)
			env["HARDEN_LLM_HISTORY_ENABLED"] = "true"
			env["HARDEN_LLM_DATABASE_URL"] = url
			cfg, err := loadServerConfig(mapEnvironment(env))
			if err != nil || !cfg.reference.historyEnabled || cfg.reference.historyState != "configuration_error" || len(cfg.connections) != 1 {
				t.Fatal("history configuration prevented independent inference or became disabled", err)
			}
		}
	})
	t.Run("invalid history boolean is a startup error", func(t *testing.T) {
		env := referenceEnvironment(t)
		env["HARDEN_LLM_HISTORY_ENABLED"] = "yes"
		if _, err := loadServerConfig(mapEnvironment(env)); err == nil {
			t.Fatal("non-boolean history setting accepted")
		}
	})
	t.Run("pool defaults ten and rejects nonpositive sizes", func(t *testing.T) {
		env := referenceEnvironment(t)
		cfg, err := loadServerConfig(mapEnvironment(env))
		if err != nil || cfg.reference.poolSize != 10 {
			t.Fatal("pool default differs", err)
		}
		for _, value := range []string{"0", "-1", "bad"} {
			env["HARDEN_LLM_DATABASE_POOL_SIZE"] = value
			if _, err := loadServerConfig(mapEnvironment(env)); err == nil {
				t.Fatal("invalid pool size accepted")
			}
		}
	})
	t.Run("credential key absent is diagnosed and malformed value never echoed", func(t *testing.T) {
		env := referenceEnvironment(t)
		cfg, err := loadServerConfig(mapEnvironment(env))
		if err != nil || cfg.reference.credentialsState != "configuration_error" {
			t.Fatal("missing credential encryption key not diagnosed", err)
		}
		env["HARDEN_LLM_CREDENTIAL_KEY"] = "secret-sentinel-invalid-base64"
		_, err = loadServerConfig(mapEnvironment(env))
		if err == nil || strings.Contains(err.Error(), env["HARDEN_LLM_CREDENTIAL_KEY"]) {
			t.Fatal("invalid key accepted or exposed", err)
		}
		env["HARDEN_LLM_CREDENTIAL_KEY"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
		cfg, err = loadServerConfig(mapEnvironment(env))
		if err != nil || cfg.reference.vault == nil || cfg.reference.credentialsState != "configured" {
			t.Fatal("valid encryption key failed", err)
		}
	})
	t.Run("cache TTL default and invalid values", func(t *testing.T) {
		env := referenceEnvironment(t)
		cfg, err := loadServerConfig(mapEnvironment(env))
		if err != nil || cfg.reference.cacheTTL.Seconds() != 86400 {
			t.Fatal("cache TTL default differs", err)
		}
		for _, value := range []string{"0", "-1", "bad"} {
			env["HARDEN_LLM_CACHE_TTL_SECONDS"] = value
			if _, err := loadServerConfig(mapEnvironment(env)); err == nil {
				t.Fatal("invalid cache TTL accepted")
			}
		}
	})
	t.Run("artifact absence disabled and partial config typed without blocking inference", func(t *testing.T) {
		env := referenceEnvironment(t)
		cfg, err := loadServerConfig(mapEnvironment(env))
		if err != nil || cfg.reference.artifactState != "disabled" {
			t.Fatal("absent artifacts not disabled", err)
		}
		env["HARDEN_LLM_ARTIFACT_ENDPOINT"] = "https://garage.example"
		cfg, err = loadServerConfig(mapEnvironment(env))
		if err != nil || cfg.reference.artifactState != "configuration_error" || cfg.reference.artifactError == nil {
			t.Fatal("partial artifacts not independently diagnosed", err)
		}
	})
	t.Run("config construction performs no network lookup or key generation", func(t *testing.T) {
		env := referenceEnvironment(t)
		env["HARDEN_LLM_DATABASE_URL"] = "postgresql://fixture:secret-sentinel@127.0.0.1:1/harden_llm"
		env["HARDEN_LLM_HISTORY_ENABLED"] = "true"
		env["HARDEN_LLM_CONTROL_PLANE_URL"] = "http://127.0.0.1:1"
		env["HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN"] = "control-plane-sentinel"
		cfg, err := loadServerConfig(mapEnvironment(env))
		if err != nil || cfg.reference.storageState != "configured" || cfg.reference.historyState != "configured" || cfg.reference.vault != nil {
			t.Fatal("value construction touched an external system or generated encryption key", err)
		}
	})
	t.Run("schema command invokes apply once without reset path", func(t *testing.T) {
		calls := 0
		env := map[string]string{"HARDEN_LLM_DATABASE_URL": "postgresql://fixture:secret-sentinel@127.0.0.1:1/harden_llm"}
		err := runSchemaCommand(context.Background(), mapEnvironment(env), func(ctx context.Context, pool *pgxpool.Pool) error {
			calls++
			if pool == nil || pool.Stat().AcquireCount() != 0 {
				t.Error("schema constructor opened connections")
			}
			return nil
		})
		if err != nil || calls != 1 {
			t.Fatal("schema command did not invoke apply exactly once", err, calls)
		}
		calls = 0
		err = runSchemaCommand(context.Background(), mapEnvironment(map[string]string{}), func(context.Context, *pgxpool.Pool) error { calls++; return nil })
		if err == nil || calls != 0 {
			t.Fatal("schema command accepted missing DB")
		}
		if err := run(context.Background(), []string{"schema", "--reset"}, &bytes.Buffer{}, &bytes.Buffer{}, mapEnvironment(env)); err == nil {
			t.Fatal("schema command accepted reset option")
		}
	})
	t.Run("preview has schema dependency and gateway storage without artifacts", func(t *testing.T) {
		raw, err := os.ReadFile("../../deploy/preview/compose.yml")
		if err != nil {
			t.Fatal(err)
		}
		var compose struct {
			Services map[string]struct {
				Image       string
				Command     []string
				Environment map[string]string
				DependsOn   map[string]struct {
					Condition string `yaml:"condition"`
				} `yaml:"depends_on"`
			}
		}
		if err = yaml.Unmarshal(raw, &compose); err != nil {
			t.Fatal(err)
		}
		gateway := compose.Services["gateway"]
		schema := compose.Services["gateway-schema"]
		if gateway.Environment["HARDEN_LLM_DATABASE_URL"] == "" || gateway.Environment["HARDEN_LLM_HISTORY_ENABLED"] != "true" || gateway.Environment["HARDEN_LLM_CREDENTIAL_KEY"] == "" || schema.Image != gateway.Image || len(schema.Command) != 1 || schema.Command[0] != "schema" || gateway.DependsOn["gateway-schema"].Condition != "service_completed_successfully" {
			t.Fatal("preview schema/storage composition missing")
		}
		for _, service := range compose.Services {
			for key := range service.Environment {
				if strings.HasPrefix(key, "HARDEN_LLM_ARTIFACT_") {
					t.Fatal("preview artifacts must remain disabled")
				}
			}
		}
	})
}
