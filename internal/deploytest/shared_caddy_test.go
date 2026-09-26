package deploytest

import (
	"path/filepath"
	"reflect"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-287
func TestSharedIngressAttachments(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))

	frontend := readYAMLObject(t, filepath.Join(root, "deploy", "frontend", "compose.frontend.yml"))
	frontendServices := objectField(t, frontend, "services")
	assertPrivateAndSharedAlias(t, "harden-llm-web", asObject(t, frontendServices["harden-llm-web"], "harden-llm-web"), "hllm-prod-web")

	langfuse := readYAMLObject(t, filepath.Join(root, "deploy", "langfuse", "compose.private.yml"))
	langfuseServices := objectField(t, langfuse, "services")
	langfuseWeb := asObject(t, langfuseServices["langfuse-web"], "langfuse-web")
	assertPrivateAndSharedAlias(t, "langfuse-web", langfuseWeb, "hllm-prod-langfuse")
	langfuseWebEnvironment := objectField(t, langfuseWeb, "environment")
	if host, ok := langfuseWebEnvironment["HOSTNAME"].(string); !ok || host != "0.0.0.0" {
		t.Errorf("langfuse-web HOSTNAME = %#v, want 0.0.0.0 so it listens on both attached networks", langfuseWebEnvironment["HOSTNAME"])
	}

	for _, name := range []string{"langfuse-worker", "clickhouse", "minio", "redis", "postgres"} {
		assertPrivateOnly(t, name, asObject(t, langfuseServices[name], name))
	}
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

func assertPrivateOnly(t *testing.T, name string, service map[string]any) {
	t.Helper()
	got := sharedIngressNetworks(t, service)
	if len(got) != 1 {
		t.Errorf("%s networks = %v, want only harden-private", name, got)
	}
	if _, ok := got["harden-private"]; !ok {
		t.Errorf("%s lost its existing harden-private attachment", name)
	}
	if _, ok := got["prls-observability"]; ok {
		t.Errorf("%s must not join prls-observability", name)
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
