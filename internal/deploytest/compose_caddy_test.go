//go:build compose

package deploytest

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-033

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	langfuseComposeSHA256 = "26510ab5cc9163bf2212d5dfb991b3a71e1ce5cf7d032b595e7eee122bec1687"
	langfuseCommit        = "a914a47f357f5d1cf5611e1387ea68678410c671"
)

var productionServices = []string{
	"caddy", "harden-llm-gateway", "harden-postgres", "garage", "otel-collector",
	"prometheus", "loki", "tempo", "grafana", "langfuse-web", "langfuse-worker",
	"postgres", "clickhouse", "redis", "minio",
}

func TestComposeCaddyContract(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	upstreamPath := filepath.Join(root, "deploy", "langfuse", "docker-compose.upstream.yml")
	basePath := filepath.Join(root, "docker-compose.yml")
	overlayPath := filepath.Join(root, "deploy", "langfuse", "compose.private.yml")

	assertLangfuseProvenance(t, upstreamPath, filepath.Join(root, "deploy", "langfuse", "UPSTREAM.md"))
	assertUpstreamLangfuseGraph(t, upstreamPath)
	assertNarrowLangfuseOverlay(t, overlayPath)

	effective := renderCompose(t, root, basePath, upstreamPath, overlayPath)
	assertEffectiveTopology(t, effective)
	assertCaddyContract(t, filepath.Join(root, "deploy", "caddy", "Caddyfile"), filepath.Join(root, "deploy", "caddy", "conf.d"))
	assertImageManifest(t, filepath.Join(root, "deploy", "images.lock.json"), effective)
}

func assertLangfuseProvenance(t *testing.T, composePath, provenancePath string) {
	t.Helper()
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if got := hex.EncodeToString(digest[:]); got != langfuseComposeSHA256 {
		t.Fatalf("upstream Langfuse Compose SHA-256 = %s, want %s", got, langfuseComposeSHA256)
	}
	provenance, err := os.ReadFile(provenancePath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(provenance)
	for _, required := range []string{
		"v3.225.5", langfuseCommit, langfuseComposeSHA256,
		"https://raw.githubusercontent.com/langfuse/langfuse/" + langfuseCommit + "/docker-compose.yml",
		"Apache-2.0",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("Langfuse provenance omits %q", required)
		}
	}
}

func assertUpstreamLangfuseGraph(t *testing.T, path string) {
	t.Helper()
	config := readYAMLObject(t, path)
	services := objectField(t, config, "services")
	wantServices := []string{"clickhouse", "langfuse-web", "langfuse-worker", "minio", "postgres", "redis"}
	if got := sortedKeys(services); !equalStrings(got, wantServices) {
		t.Fatalf("upstream services = %v, want %v", got, wantServices)
	}
	wantImages := map[string]string{
		"langfuse-web": "docker.io/langfuse/langfuse:3", "langfuse-worker": "docker.io/langfuse/langfuse-worker:3",
		"clickhouse": "docker.io/clickhouse/clickhouse-server", "minio": "cgr.dev/chainguard/minio",
		"redis": "docker.io/redis:7", "postgres": "docker.io/postgres:${POSTGRES_VERSION:-17}",
	}
	for service, image := range wantImages {
		if got := stringField(t, asObject(t, services[service], service), "image"); got != image {
			t.Errorf("upstream %s image = %q, want %q", service, got, image)
		}
	}
	for _, service := range []string{"langfuse-web", "langfuse-worker"} {
		dependencies := objectField(t, asObject(t, services[service], service), "depends_on")
		if got := sortedKeys(dependencies); !equalStrings(got, []string{"clickhouse", "minio", "postgres", "redis"}) {
			t.Errorf("upstream %s dependencies = %v", service, got)
		}
	}
	wantVolumes := []string{
		"langfuse_clickhouse_data", "langfuse_clickhouse_logs", "langfuse_minio_data",
		"langfuse_postgres_data", "langfuse_redis_data",
	}
	if got := sortedKeys(objectField(t, config, "volumes")); !equalStrings(got, wantVolumes) {
		t.Errorf("upstream volumes = %v, want %v", got, wantVolumes)
	}
}

func assertNarrowLangfuseOverlay(t *testing.T, path string) {
	t.Helper()
	config := readYAMLObject(t, path)
	services := objectField(t, config, "services")
	want := []string{"clickhouse", "langfuse-web", "langfuse-worker", "minio", "postgres", "redis"}
	if got := sortedKeys(services); !equalStrings(got, want) {
		t.Fatalf("Langfuse overlay services = %v, want %v", got, want)
	}
	allowedFields := map[string]bool{"environment": true, "image": true, "networks": true, "ports": true}
	for name, raw := range services {
		service := asObject(t, raw, "overlay service "+name)
		for field := range service {
			if !allowedFields[field] {
				t.Errorf("Langfuse overlay changes forbidden %s.%s", name, field)
			}
		}
		if _, ok := service["ports"]; !ok {
			t.Errorf("Langfuse overlay does not explicitly reset %s host ports", name)
		}
		if !valueContains(service["networks"], "harden-private") {
			t.Errorf("Langfuse overlay does not attach %s to harden-private", name)
		}
	}
	webEnv := environmentMap(t, asObject(t, services["langfuse-web"], "langfuse-web"))
	for _, name := range []string{
		"LANGFUSE_INIT_ORG_ID", "LANGFUSE_INIT_ORG_NAME", "LANGFUSE_INIT_PROJECT_ID", "LANGFUSE_INIT_PROJECT_NAME",
		"LANGFUSE_INIT_PROJECT_PUBLIC_KEY", "LANGFUSE_INIT_PROJECT_SECRET_KEY", "LANGFUSE_INIT_USER_EMAIL",
		"LANGFUSE_INIT_USER_NAME", "LANGFUSE_INIT_USER_PASSWORD", "NEXTAUTH_URL",
	} {
		if strings.TrimSpace(webEnv[name]) == "" {
			t.Errorf("Langfuse headless initialization omits %s", name)
		}
	}
	for name, raw := range services {
		encoded, _ := json.Marshal(raw)
		lower := strings.ToLower(string(encoded))
		if strings.Contains(lower, "garage") {
			t.Errorf("Langfuse overlay service %s contains a Garage setting", name)
		}
	}
}

func renderCompose(t *testing.T, root string, files ...string) map[string]any {
	t.Helper()
	args := []string{"compose", "--project-name", "harden-llm-contract"}
	for _, file := range files {
		absolute, err := filepath.Abs(file)
		if err != nil {
			t.Fatalf("resolve Compose file %s: %v", file, err)
		}
		args = append(args, "-f", absolute)
	}
	args = append(args, "config", "--format", "json")
	command := exec.Command("docker", args...)
	command.Dir = root
	command.Env = append(os.Environ(), composeContractEnvironment()...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config: %v\n%s", err, output)
	}
	var config map[string]any
	if err := json.Unmarshal(output, &config); err != nil {
		t.Fatalf("parse effective Compose JSON: %v\n%s", err, output)
	}
	return config
}

func assertEffectiveTopology(t *testing.T, config map[string]any) {
	t.Helper()
	services := objectField(t, config, "services")
	sort.Strings(productionServices)
	actualServices := make(map[string]any, len(services))
	for name, service := range services {
		if name != "otel-collector-state-init" {
			actualServices[name] = service
		}
	}
	if got := sortedKeys(actualServices); !equalStrings(got, productionServices) {
		t.Fatalf("effective services = %v, want %v", got, productionServices)
	}
	stateInit := asObject(t, services["otel-collector-state-init"], "otel-collector-state-init")
	if stringField(t, stateInit, "network_mode") != "none" {
		t.Errorf("otel-collector-state-init network mode = %q, want none", stateInit["network_mode"])
	}
	for name, raw := range services {
		if name == "otel-collector-state-init" {
			continue
		}
		service := asObject(t, raw, "effective service "+name)
		ports, _ := service["ports"].([]any)
		if name == "caddy" {
			if len(ports) != 2 {
				t.Errorf("Caddy published port count = %d, want 2", len(ports))
			}
		} else if len(ports) != 0 {
			t.Errorf("non-edge service %s publishes host ports: %#v", name, ports)
		}
		if !valueContains(service["networks"], "harden-private") {
			t.Errorf("service %s is not attached to harden-private", name)
		}
	}

	baseOwned := []string{"caddy", "harden-llm-gateway", "harden-postgres", "garage", "otel-collector", "prometheus", "loki", "tempo", "grafana"}
	for _, name := range baseOwned {
		service := asObject(t, services[name], name)
		image := stringField(t, service, "image")
		if image == "" || strings.Contains(image, ":latest") || (!strings.Contains(image, "@sha256:") && !regexp.MustCompile(`:[v]?[0-9]+(?:\.[0-9]+){1,2}(?:[-.][a-zA-Z0-9]+)*$`).MatchString(image)) {
			t.Errorf("Harden-owned image %s is not release pinned: %q", name, image)
		}
	}

	garage := asObject(t, services["garage"], "garage")
	command := stringListValue(t, garage["command"], "garage command")
	if !equalStrings(command, []string{"/garage", "server"}) {
		t.Errorf("Garage command = %v", command)
	}
	garageEnv := environmentValueMap(t, garage["environment"])
	gatewayEnv := environmentValueMap(t, asObject(t, services["harden-llm-gateway"], "gateway")["environment"])
	for garageName, gatewayName := range map[string]string{
		"GARAGE_DEFAULT_ACCESS_KEY": "HARDEN_LLM_ARTIFACT_ACCESS_KEY_ID",
		"GARAGE_DEFAULT_SECRET_KEY": "HARDEN_LLM_ARTIFACT_SECRET_ACCESS_KEY",
		"GARAGE_DEFAULT_BUCKET":     "HARDEN_LLM_ARTIFACT_BUCKET",
	} {
		if garageEnv[garageName] == "" || garageEnv[garageName] != gatewayEnv[gatewayName] {
			t.Errorf("Garage/gateway credential mapping differs for %s/%s", garageName, gatewayName)
		}
	}
	if endpoint := strings.ToLower(gatewayEnv["HARDEN_LLM_ARTIFACT_ENDPOINT"]); !strings.Contains(endpoint, "garage:3900") || strings.Contains(endpoint, "minio") {
		t.Errorf("gateway artifact endpoint = %q", endpoint)
	}
	for _, name := range []string{"langfuse-web", "langfuse-worker"} {
		env := environmentValueMap(t, asObject(t, services[name], name)["environment"])
		encoded, _ := json.Marshal(env)
		lower := strings.ToLower(string(encoded))
		if !strings.Contains(lower, "minio") || strings.Contains(lower, "garage") {
			t.Errorf("%s object storage ownership is invalid", name)
		}
	}
	collectorEnv := environmentValueMap(t, asObject(t, services["otel-collector"], "otel-collector")["environment"])
	if collectorEnv["LANGFUSE_PUBLIC_KEY"] != environmentValueMap(t, asObject(t, services["langfuse-web"], "langfuse-web")["environment"])["LANGFUSE_INIT_PROJECT_PUBLIC_KEY"] ||
		collectorEnv["LANGFUSE_SECRET_KEY"] != environmentValueMap(t, asObject(t, services["langfuse-web"], "langfuse-web")["environment"])["LANGFUSE_INIT_PROJECT_SECRET_KEY"] {
		t.Error("Collector does not receive the headlessly initialized Langfuse project keys")
	}

	volumes := objectField(t, config, "volumes")
	for _, name := range []string{
		"caddy-data", "caddy-config", "harden-postgres-data", "garage-metadata", "garage-data",
		"prometheus-data", "loki-data", "tempo-data", "grafana-data", "langfuse_postgres_data",
		"langfuse_clickhouse_data", "langfuse_clickhouse_logs", "langfuse_minio_data", "langfuse_redis_data",
	} {
		if _, ok := volumes[name]; !ok {
			t.Errorf("effective Compose omits named volume %s", name)
		}
	}
}

func assertCaddyContract(t *testing.T, path, extensionDir string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"{$HARDEN_LLM_API_HOST}", "reverse_proxy harden-llm-gateway:8080",
		"{$HARDEN_LLM_GRAFANA_HOST}", "reverse_proxy grafana:3000",
		"{$HARDEN_LLM_LANGFUSE_HOST}", "reverse_proxy langfuse-web:3000",
		"{$HARDEN_LLM_ARTIFACT_HOST}", "reverse_proxy garage:3900",
		"Strict-Transport-Security", "X-Content-Type-Options", "Referrer-Policy", "Content-Security-Policy",
		"request_body", "max_size", "tls {$HARDEN_LLM_TLS_MODE}",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("Caddyfile omits %q", required)
		}
	}
	if count := strings.Count(text, "import /etc/caddy/conf.d/*.caddy"); count != 1 {
		t.Errorf("trusted conf.d import count = %d, want 1", count)
	}
	if count := strings.Count(text, "import /etc/caddy/overlays/*.frontend"); count != 1 {
		t.Errorf("trusted frontend overlay import count = %d, want 1", count)
	}
	for _, forbidden := range []string{"file_server", "root *", "php_fastcgi", "garage:3901", "garage:3903"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("Caddyfile contains forbidden route/directive %q", forbidden)
		}
	}
	entries, err := os.ReadDir(extensionDir)
	if err != nil {
		t.Fatal(err)
	}
	wantEntries := map[string]bool{
		".gitkeep":             false,
		"prls-agents.caddy":    false,
		"prls-analytics.caddy": false,
		"prls-tests.caddy":     false,
	}
	for _, entry := range entries {
		if _, ok := wantEntries[entry.Name()]; !ok {
			t.Errorf("trusted conf.d contains unreviewed entry %s", entry.Name())
			continue
		}
		wantEntries[entry.Name()] = true
	}
	for name, found := range wantEntries {
		if !found {
			t.Errorf("trusted conf.d omits reviewed entry %s", name)
		}
	}
}

func assertImageManifest(t *testing.T, path string, effective map[string]any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SchemaVersion int               `json:"schemaVersion"`
		Images        map[string]string `json:"images"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse image manifest: %v", err)
	}
	if manifest.SchemaVersion != 1 || len(manifest.Images) != len(productionServices) {
		t.Errorf("image manifest schema/count = %d/%d", manifest.SchemaVersion, len(manifest.Images))
	}
	services := objectField(t, effective, "services")
	for _, name := range productionServices {
		image := stringField(t, asObject(t, services[name], name), "image")
		locked, ok := manifest.Images[name]
		if !ok || locked == "" {
			t.Errorf("image manifest omits %s", name)
			continue
		}
		if locked != image {
			t.Errorf("locked image %s = %q, effective %q", name, locked, image)
		}
		if !strings.Contains(locked, "@sha256:") && name != "harden-llm-gateway" {
			t.Errorf("locked image %s has no manifest digest: %q", name, locked)
		}
	}
}

func composeContractEnvironment() []string {
	return []string{
		"HARDEN_LLM_API_HOST=api.harden.test", "HARDEN_LLM_GRAFANA_HOST=grafana.harden.test",
		"HARDEN_LLM_LANGFUSE_HOST=langfuse.harden.test", "HARDEN_LLM_ARTIFACT_HOST=artifacts.harden.test",
		"PRLS_ALLURE_HOST=allure.harden.test", "PRLS_TESTS_BASIC_AUTH_USER=contract-operator",
		"PRLS_TESTS_BASIC_AUTH_HASH=$2a$14$contractOnlyNotAProductionHash0000000000000000000000",
		"HARDEN_LLM_ARTIFACT_EXTERNAL_ENDPOINT=https://artifacts.harden.test",
		"HARDEN_LLM_TLS_MODE=internal", "HARDEN_LLM_POSTGRES_PASSWORD=contract-harden-db-7Y2qN5",
		"HARDEN_LLM_ARTIFACT_ACCESS_KEY_ID=GKCONTRACT000000000000000000000001",
		"HARDEN_LLM_ARTIFACT_SECRET_ACCESS_KEY=contractGarageKey_4Ys8zQ1xN7pV9kM2rT6wE3aB5cD0fH",
		"HARDEN_LLM_GARAGE_RPC_SECRET=6e9ec8720db72c83cf0e33928113fb09bfd42c9e0f9a35b04b12a1673cd78f1a",
		`HARDEN_LLM_ENCRYPTION_KEYS={"primary":"R1BKT3pKV0M1akY2WnlYYU45Sm5UTW82dzBuXzJ4bTk"}`,
		"HARDEN_LLM_ACTIVE_ENCRYPTION_KEY_ID=primary",
		"HARDEN_LLM_RELEASE=contract-1", "GRAFANA_ADMIN_PASSWORD=contract-grafana-8Zt4pW",
		"LANGFUSE_POSTGRES_PASSWORD=contract-langfuse-db-8bQ2mK", "LANGFUSE_SALT=contractSalt9aZ5nC2",
		"LANGFUSE_ENCRYPTION_KEY=3ff4a321a56028d03b26d90c2e5adeba6702dc1c3f267b603884e3c1f959fcb4",
		"LANGFUSE_NEXTAUTH_SECRET=contractNextAuth4sY9kQ2nP7wX",
		"CLICKHOUSE_PASSWORD=contract-clickhouse-9aR3pV", "REDIS_AUTH=contract-redis-2mW8qT",
		"MINIO_ROOT_USER=contractminio", "MINIO_ROOT_PASSWORD=contract-minio-8bQ4pT2z",
		"LANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk-lf-contract000000000000000000000000",
		"LANGFUSE_INIT_PROJECT_SECRET_KEY=sk-lf-contract000000000000000000000000",
		"PRLS_LAMINAR_PROJECT_API_KEY=contract-laminar-project-key",
		"PRLS_LOKI_S3_ACCESS_KEY=GKCONTRACT000000000000000000000002",
		"PRLS_LOKI_S3_SECRET_KEY=contractLokiGarageKey_7Jt3sM9qP2vW6xN8cR4aD1fH5kB0zE",
		"LANGFUSE_INIT_USER_PASSWORD=contract-user-9mQ2vN7p", "COMPOSE_PROJECT_NAME=harden-llm-contract",
	}
}

func environmentMap(t *testing.T, service map[string]any) map[string]string {
	t.Helper()
	return environmentValueMap(t, service["environment"])
}

func environmentValueMap(t *testing.T, value any) map[string]string {
	t.Helper()
	result := make(map[string]string)
	switch typed := value.(type) {
	case map[string]any:
		for name, raw := range typed {
			result[name] = fmt.Sprint(raw)
		}
	case []any:
		for _, raw := range typed {
			name, value, _ := strings.Cut(fmt.Sprint(raw), "=")
			result[name] = value
		}
	case nil:
		return result
	default:
		t.Fatalf("environment = %#v, want object or list", value)
	}
	return result
}

func stringListValue(t *testing.T, value any, label string) []string {
	t.Helper()
	raw, ok := value.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want list", label, value)
	}
	result := make([]string, len(raw))
	for index, item := range raw {
		result[index] = fmt.Sprint(item)
	}
	return result
}

func valueContains(value any, wanted string) bool {
	encoded, _ := json.Marshal(value)
	return bytes.Contains(bytes.ToLower(encoded), bytes.ToLower([]byte(wanted)))
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
