package deploytest

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-432

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReferenceTopology(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(filepath.Join("..", ".."))
	base := readYAMLObject(t, filepath.Join(root, "docker-compose.yml"))
	front := readYAMLObject(t, filepath.Join(root, "deploy/frontend/compose.frontend.yml"))
	services := objectField(t, base, "services")
	frontServices := objectField(t, front, "services")
	gateway := objectField(t, services, "harden-llm-gateway")
	gatewayEnv := objectField(t, gateway, "environment")
	webEnv := objectField(t, objectField(t, frontServices, "harden-llm-web"), "environment")
	t.Run("one shared database attachment and no Phoenix storage", func(t *testing.T) {
		if _, exists := frontServices["harden-postgres"]; exists {
			t.Error("dedicated HLLM PostgreSQL must be retired in favor of Agent Platform services.postgres")
		}
		if _, exists := objectField(t, front, "volumes")["harden-postgres-data"]; exists {
			t.Error("production no longer owns a dedicated database volume")
		}
		for _, name := range []string{"HARDEN_LLM_DATABASE_URL", "HARDEN_LLM_CREDENTIAL_KEY"} {
			value, ok := gatewayEnv[name].(string)
			if !ok || !strings.Contains(value, "${"+name+":?") {
				t.Errorf("gateway must receive required protected %s", name)
			}
			if _, exists := webEnv[name]; exists {
				t.Errorf("Phoenix must not receive %s", name)
			}
		}
		if gatewayEnv["HARDEN_LLM_HISTORY_ENABLED"] != "true" {
			t.Error("reference production must explicitly enable history")
		}
		networks := objectField(t, base, "networks")
		shared, ok := networks["shared-postgres"].(map[string]any)
		if !ok || shared["external"] != true || shared["name"] != "${HARDEN_LLM_POSTGRES_NETWORK:?set the shared PostgreSQL owner network}" {
			t.Error("shared PostgreSQL must attach through the owner-provided external network")
		}
		attached, ok := gateway["networks"].([]any)
		if !ok || !containsNetwork(attached, "shared-postgres") {
			t.Error("gateway must join the single shared PostgreSQL network")
		}
	})
	t.Run("same selected key and host-only human admission", func(t *testing.T) {
		if gatewayEnv["HARDEN_LLM_TOKEN"] != webEnv["HARDEN_LLM_TOKEN"] {
			t.Error("gateway and frontend must select the same deployment bearer")
		}
		for name := range gatewayEnv {
			if strings.Contains(name, "CONTROL_PLANE") || strings.Contains(name, "PORTAL") {
				t.Errorf("Go must not depend on human identity authority: %s", name)
			}
		}
		if !strings.Contains(webEnv["HARDEN_LLM_WEB_API_TIMEOUT_MS"].(string), "75000") {
			t.Error("reference transport must use the accepted 75-second bound")
		}
	})
	t.Run("schema command reuses the gateway image", func(t *testing.T) {
		value, exists := services["gateway-schema"]
		if !exists {
			t.Fatal("production requires the accepted gateway image's one-shot schema command")
		}
		schema := value.(map[string]any)
		if schema["image"] != gateway["image"] || !reflect.DeepEqual(schema["command"], []any{"schema"}) {
			t.Error("schema must use the same accepted image with command [schema]")
		}
		if _, exists := schema["build"]; exists {
			t.Error("schema service must not build another artifact")
		}
	})
	t.Run("one hostname routes both bearer APIs", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(root, "scripts/preview-environment.mjs"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "@api path /v1/* /api/v1/* /healthz /readyz") {
			t.Error("existing shared ingress route must send both REST surfaces to Go")
		}
	})
	t.Run("clean cut is confined to obsolete HLLM storage", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(root, "deploy/reference-reset.json"))
		if err != nil {
			t.Fatal("explicit HLLM-only reset manifest is required")
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"composeProject": "harden-llm",
			"containers":     []any{"harden-llm-harden-postgres-1"},
			"volumes":        []any{"harden-llm_harden-postgres-data"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Error("reset may remove only the old HLLM database container and volume; shared databases, identities, artifacts and keys are excluded")
		}
	})
}

func containsNetwork(networks []any, name string) bool {
	for _, network := range networks {
		if network == name {
			return true
		}
	}
	return false
}
