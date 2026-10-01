package deploytest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-289
func TestSharedIngressOwnership(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))

	production := readYAMLObject(t, filepath.Join(root, "docker-compose.yml"))
	productionServices := objectField(t, production, "services")
	if _, exists := productionServices["caddy"]; exists {
		t.Error("production Compose still owns Caddy; ingress is owned by caddy-shared")
	}
	frontendOverlay := readYAMLObject(t, filepath.Join(root, "deploy", "frontend", "compose.frontend.yml"))
	if _, exists := objectField(t, frontendOverlay, "services")["caddy"]; exists {
		t.Error("production frontend overlay still modifies Caddy")
	}
	productionVolumes := objectField(t, production, "volumes")
	for _, name := range []string{"caddy-data", "caddy-config"} {
		if _, exists := productionVolumes[name]; exists {
			t.Errorf("production Compose still declares Caddy volume %s", name)
		}
	}

	imageData, err := os.ReadFile(filepath.Join(root, "deploy", "images.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var imageManifest struct {
		Images map[string]string `json:"images"`
	}
	if err := json.Unmarshal(imageData, &imageManifest); err != nil {
		t.Fatalf("parse production image manifest: %v", err)
	}
	if _, exists := imageManifest.Images["caddy"]; exists {
		t.Error("production image manifest still owns Caddy")
	}

	productionDescriptorData, err := os.ReadFile(filepath.Join(root, "config", "production-config.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var productionDescriptor struct {
		RequiredVariables []string                   `json:"requiredVariables"`
		Services          map[string]json.RawMessage `json:"services"`
	}
	if err := json.Unmarshal(productionDescriptorData, &productionDescriptor); err != nil {
		t.Fatalf("parse example production descriptor: %v", err)
	}
	if _, exists := productionDescriptor.Services["caddy"]; exists {
		t.Error("example production descriptor still selects Caddy for HLLM management")
	}
	for _, name := range []string{"PRLS_ALLURE_HOST", "PRLS_TESTS_BASIC_AUTH_USER", "PRLS_TESTS_BASIC_AUTH_HASH"} {
		for _, required := range productionDescriptor.RequiredVariables {
			if required == name {
				t.Errorf("production descriptor still requires edge-owned variable %s", name)
			}
		}
	}
	for _, relative := range []string{"deploy/caddy", "deploy/frontend/Caddyfile.frontend"} {
		_, err := os.Stat(filepath.Join(root, relative))
		if err == nil {
			t.Errorf("HLLM still carries the shared production Caddy source %s", relative)
		} else if !os.IsNotExist(err) {
			t.Errorf("inspect shared production Caddy source %s: %v", relative, err)
		}
	}

	integration := readYAMLObject(t, filepath.Join(root, "deploy", "test", "compose.integration.yml"))
	assertNetworkSubnet(t, integration, "default", "198.51.100.0/24")

	smoke := readYAMLObject(t, filepath.Join(root, "deploy", "test", "compose.smoke.yml"))
	assertNetworkSubnet(t, smoke, "harden-private", "203.0.113.0/24")
	assertNetworkSubnet(t, smoke, "prls-observability", "198.18.0.0/24")
	smokeServices := objectField(t, smoke, "services")
	smokeCaddy := asObject(t, smokeServices["caddy"], "smoke-owned caddy")
	for _, field := range []string{"image", "environment", "depends_on", "ports", "volumes", "networks"} {
		if _, exists := smokeCaddy[field]; !exists {
			t.Errorf("smoke Compose does not explicitly own Caddy field %s", field)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "deploy", "test", "Caddyfile.smoke")); err != nil {
		t.Errorf("isolated smoke Caddyfile is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "deploy", "test", "compose.frontend-smoke.yml")); err != nil {
		t.Errorf("isolated frontend Caddy overlay is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "deploy", "test", "Caddyfile.frontend-smoke")); err != nil {
		t.Errorf("isolated frontend Caddyfile is missing: %v", err)
	}
	smokeHarness, err := os.ReadFile(filepath.Join(root, "internal", "smoke", "harness_compose.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(smokeHarness), `"--label", "com.docker.compose.project=" + runner.project`) {
		t.Error("one-shot frontend Caddy validation container is not owned by the smoke cleanup project")
	}
	frontendSmoke := readYAMLObject(t, filepath.Join(root, "deploy", "test", "compose.frontend-smoke.yml"))
	frontendSmokeServices := objectField(t, frontendSmoke, "services")
	frontendSmokeCaddy := asObject(t, frontendSmokeServices["caddy"], "frontend smoke caddy")
	if _, exists := objectField(t, frontendSmokeCaddy, "environment")["HARDEN_LLM_WEB_HOST"]; !exists {
		t.Error("frontend smoke Caddy overlay does not supply HARDEN_LLM_WEB_HOST")
	}
	frontendCaddyfile, err := os.ReadFile(filepath.Join(root, "deploy", "test", "Caddyfile.frontend-smoke"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"import /etc/caddy/test/Caddyfile.smoke", "{$HARDEN_LLM_WEB_HOST}",
		"respond @private_metrics 404", "reverse_proxy harden-llm-web:4000",
	} {
		if !strings.Contains(string(frontendCaddyfile), required) {
			t.Errorf("frontend smoke Caddyfile omits %q", required)
		}
	}

	preview := readYAMLObject(t, filepath.Join(root, "deploy", "preview", "host.compose.yml"))
	previewServices := objectField(t, preview, "services")
	if _, exists := previewServices["edge"]; !exists {
		t.Error("branch preview no longer owns its isolated Caddy edge")
	}
	if _, exists := previewServices["tunnel"]; !exists {
		t.Error("branch preview no longer owns its separate tunnel")
	}
}

func assertNetworkSubnet(t *testing.T, config map[string]any, networkName, wantSubnet string) {
	t.Helper()
	networks := objectField(t, config, "networks")
	network := asObject(t, networks[networkName], networkName+" network")
	ipam := objectField(t, network, "ipam")
	entries, ok := ipam["config"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("%s IPAM config = %#v, want one explicit subnet", networkName, ipam["config"])
	}
	entry := asObject(t, entries[0], networkName+" IPAM entry")
	if got := stringField(t, entry, "subnet"); got != wantSubnet {
		t.Errorf("%s subnet = %q, want %q", networkName, got, wantSubnet)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-287
func TestSharedIngressAttachments(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))

	frontend := readYAMLObject(t, filepath.Join(root, "deploy", "frontend", "compose.frontend.yml"))
	frontendServices := objectField(t, frontend, "services")
	assertPrivateAndSharedAlias(t, "harden-llm-web", asObject(t, frontendServices["harden-llm-web"], "harden-llm-web"), "hllm-prod-web")

}

func assertPrivateAndSharedAlias(t *testing.T, name string, service map[string]any, alias string) {
	t.Helper()
	got := sharedIngressNetworks(t, service)
	if len(got) != 2 {
		t.Errorf("%s networks = %v, want only harden-private and prls-observability", name, got)
	}
	if _, ok := got["harden-private"]; !ok {
		t.Errorf("%s lost its existing harden-private attachment", name)
	}
	if aliases, ok := got["prls-observability"]; !ok || !reflect.DeepEqual(aliases, []string{alias}) {
		t.Errorf("%s prls-observability aliases = %v, want [%s]", name, aliases, alias)
	}
}

func sharedIngressNetworks(t *testing.T, service map[string]any) map[string][]string {
	t.Helper()
	value, ok := service["networks"]
	if !ok {
		t.Fatal("service has no networks field")
	}
	result := make(map[string][]string)
	switch networks := value.(type) {
	case []any:
		for _, raw := range networks {
			name, ok := raw.(string)
			if !ok {
				t.Fatalf("network entry = %#v, want a network name", raw)
			}
			result[name] = nil
		}
	case map[string]any:
		for name, raw := range networks {
			result[name] = nil
			if raw == nil {
				continue
			}
			config, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("network %s config = %#v, want object", name, raw)
			}
			if aliasesRaw, exists := config["aliases"]; exists {
				aliases, ok := aliasesRaw.([]any)
				if !ok {
					t.Fatalf("network %s aliases = %#v, want list", name, aliasesRaw)
				}
				for _, rawAlias := range aliases {
					alias, ok := rawAlias.(string)
					if !ok {
						t.Fatalf("network %s alias = %#v, want string", name, rawAlias)
					}
					result[name] = append(result[name], alias)
				}
			}
		}
	default:
		t.Fatalf("service networks = %#v, want list or mapping", value)
	}
	return result
}
