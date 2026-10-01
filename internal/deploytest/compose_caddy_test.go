//go:build compose

package deploytest

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-033

import (
	"bytes"
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

var productionServices = []string{
	"harden-llm-gateway", "harden-postgres", "otel-collector",
	"prometheus", "loki", "tempo", "grafana",
}

func TestComposeDeploymentContract(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	basePath := filepath.Join(root, "docker-compose.yml")
	frontendPath := filepath.Join(root, "deploy", "frontend", "compose.frontend.yml")
	smokePath := filepath.Join(root, "deploy", "test", "compose.smoke.yml")
	frontendSmokePath := filepath.Join(root, "deploy", "test", "compose.frontend-smoke.yml")

	backend := renderCompose(t, root, basePath)
	assertEffectiveTopology(t, backend)
	assertImageManifest(t, filepath.Join(root, "deploy", "images.lock.json"), backend)
	production := renderCompose(t, root, basePath, frontendPath)
	assertServiceNames(
		t,
		production,
		append(append([]string(nil), productionServices...), "harden-llm-web", "otel-collector-state-init"),
		"production",
	)
	assertProductionFrontend(t, production)

	smoke := renderCompose(t, root, basePath, smokePath)
	assertSmokeTopology(t, root, smoke, false)
	frontendSmoke := renderCompose(t, root, basePath, smokePath, frontendPath, frontendSmokePath)
	assertSmokeTopology(t, root, frontendSmoke, true)
}

func renderCompose(t *testing.T, root string, files ...string) map[string]any {
	return renderComposeWithEnvironment(t, root, composeContractEnvironment(), files...)
}

func renderComposeWithEnvironment(t *testing.T, root string, environment []string, files ...string) map[string]any {
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
	command.Env = mergeEnvironment(os.Environ(), environment)
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

func TestCollectorLaminarEndpointOverride(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	endpoint := "127.0.0.1:18001"
	environment := append(composeContractEnvironment(), "HARDEN_LLM_LAMINAR_ENDPOINT="+endpoint)
	config := renderComposeWithEnvironment(t, root, environment,
		filepath.Join(root, "docker-compose.yml"),
		filepath.Join(root, "deploy", "frontend", "compose.frontend.yml"),
	)
	services := objectField(t, config, "services")
	collector := asObject(t, services["otel-collector"], "otel-collector")
	collectorEnv := environmentValueMap(t, collector["environment"])
	if got := collectorEnv["HARDEN_LLM_LAMINAR_ENDPOINT"]; got != endpoint {
		t.Fatalf("Collector Laminar endpoint = %q, want the explicit test endpoint", got)
	}
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
		if len(ports) != 0 {
			t.Errorf("non-edge service %s publishes host ports: %#v", name, ports)
		}
		if !valueContains(service["networks"], "harden-private") {
			t.Errorf("service %s is not attached to harden-private", name)
		}
	}

	baseOwned := []string{"harden-llm-gateway", "harden-postgres", "otel-collector", "prometheus", "loki", "tempo", "grafana"}
	for _, name := range baseOwned {
		service := asObject(t, services[name], name)
		image := stringField(t, service, "image")
		if image == "" || strings.Contains(image, ":latest") || (!strings.Contains(image, "@sha256:") && !regexp.MustCompile(`:[v]?[0-9]+(?:\.[0-9]+){1,2}(?:[-.][a-zA-Z0-9]+)*$`).MatchString(image)) {
			t.Errorf("Harden-owned image %s is not release pinned: %q", name, image)
		}
	}

	gatewayEnv := environmentValueMap(t, asObject(t, services["harden-llm-gateway"], "gateway")["environment"])
	if endpoint := gatewayEnv["HARDEN_LLM_ARTIFACT_ENDPOINT"]; endpoint != "http://garage-shared:3900" {
		t.Errorf("gateway artifact endpoint = %q", endpoint)
	}
	gateway := asObject(t, services["harden-llm-gateway"], "gateway")
	if !valueContains(gateway["networks"], "harden-private") || !valueContains(gateway["networks"], "prls-observability") {
		t.Errorf("gateway networks = %#v, want private and shared networks", gateway["networks"])
	}
	service := asObject(t, services["loki"], "loki")
	if !valueContains(service["networks"], "prls-observability") {
		t.Error("loki is not attached to the shared Garage client network")
	}
	collectorEnv := environmentValueMap(t, asObject(t, services["otel-collector"], "otel-collector")["environment"])
	if collectorEnv["HARDEN_LLM_LAMINAR_ENDPOINT"] != "laminar:8001" {
		t.Errorf("Collector Laminar endpoint default = %q, want laminar:8001", collectorEnv["HARDEN_LLM_LAMINAR_ENDPOINT"])
	}
	if collectorEnv["HARDEN_LLM_LAMINAR_PROJECT_API_KEY"] != "contract-harden-laminar-project-key" ||
		collectorEnv["HARDEN_LLM_LAMINAR_PROJECT_API_KEY"] == collectorEnv["PRLS_LAMINAR_PROJECT_API_KEY"] {
		t.Error("Collector does not resolve a distinct Harden LLM Laminar project key")
	}

	volumes := objectField(t, config, "volumes")
	for name := range volumes {
		if strings.Contains(strings.ToLower(name), "garage") {
			t.Errorf("production Compose owns Garage volume %s", name)
		}
	}
	for _, name := range []string{
		"harden-postgres-data",
		"prometheus-data", "loki-data", "tempo-data", "grafana-data",
	} {
		if _, ok := volumes[name]; !ok {
			t.Errorf("effective Compose omits named volume %s", name)
		}
	}
	networks := objectField(t, config, "networks")
	observability := asObject(t, networks["prls-observability"], "shared observability network")
	if !boolField(t, observability, "external") || stringField(t, observability, "name") != "prls-observability" {
		t.Errorf("shared observability network = %#v, want the existing external network", observability)
	}
}

func assertServiceNames(t *testing.T, config map[string]any, wanted []string, model string) {
	t.Helper()
	services := objectField(t, config, "services")
	got := sortedKeys(services)
	wanted = append([]string(nil), wanted...)
	sort.Strings(wanted)
	if !equalStrings(got, wanted) {
		t.Fatalf("%s services = %v, want %v", model, got, wanted)
	}
}

func assertProductionFrontend(t *testing.T, config map[string]any) {
	t.Helper()
	services := objectField(t, config, "services")
	web := asObject(t, services["harden-llm-web"], "production harden-llm-web")
	ports, _ := web["ports"].([]any)
	if len(ports) != 0 {
		t.Errorf("production web service publishes host ports: %#v", ports)
	}
	assertPrivateAndSharedAlias(t, "production harden-llm-web", web, "hllm-prod-web")
}

func assertSmokeTopology(t *testing.T, root string, config map[string]any, frontend bool) {
	t.Helper()
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve HLLM root %q: %v", root, err)
	}
	wanted := append(
		append([]string(nil), productionServices...),
		"caddy", "fake-provider", "garage", "laminar", "otel-collector-state-init",
	)
	model := "backend smoke"
	if frontend {
		wanted = append(wanted, "harden-llm-web")
		model = "frontend smoke"
	}
	assertServiceNames(t, config, wanted, model)

	services := objectField(t, config, "services")
	caddy := asObject(t, services["caddy"], model+" caddy")
	if image := stringField(t, caddy, "image"); image != "caddy:2.11.4-alpine@sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648" {
		t.Errorf("%s Caddy image = %q, want the pinned isolated test image", model, image)
	}
	if _, exists := caddy["build"]; exists {
		t.Errorf("%s Caddy unexpectedly has a build context", model)
	}

	environment := environmentValueMap(t, caddy["environment"])
	for _, name := range []string{"HARDEN_LLM_API_HOST", "HARDEN_LLM_ARTIFACT_HOST", "HARDEN_LLM_GRAFANA_HOST"} {
		if strings.TrimSpace(environment[name]) == "" {
			t.Errorf("%s Caddy environment omits %s", model, name)
		}
	}
	for _, name := range []string{"PRLS_ALLURE_HOST", "PRLS_TESTS_BASIC_AUTH_USER", "PRLS_TESTS_BASIC_AUTH_HASH"} {
		if _, exists := environment[name]; exists {
			t.Errorf("%s Caddy unexpectedly retains shared-edge variable %s", model, name)
		}
	}
	if frontend && environment["HARDEN_LLM_WEB_HOST"] != "app.harden.test" {
		t.Errorf("frontend smoke Caddy web host = %q, want app.harden.test", environment["HARDEN_LLM_WEB_HOST"])
	}

	wantedDependencies := []string{"garage", "grafana", "harden-llm-gateway"}
	if frontend {
		wantedDependencies = append(wantedDependencies, "harden-llm-web")
	}
	sort.Strings(wantedDependencies)
	if got := sortedKeys(objectField(t, caddy, "depends_on")); !equalStrings(got, wantedDependencies) {
		t.Errorf("%s Caddy dependencies = %v, want %v", model, got, wantedDependencies)
	}
	if got := sortedKeys(objectField(t, caddy, "networks")); !equalStrings(got, []string{"harden-private", "prls-observability"}) {
		t.Errorf("%s Caddy networks = %v, want its isolated backend and artifact networks", model, got)
	}
	assertNetworkSubnet(t, config, "harden-private", "203.0.113.0/24")
	assertNetworkSubnet(t, config, "prls-observability", "198.18.0.0/24")

	laminar := asObject(t, services["laminar"], model+" Laminar test sink")
	if image := stringField(t, laminar, "image"); image != "otel/opentelemetry-collector-contrib:0.156.0@sha256:125bdbeb7590cc1952c5b3430ecf14063568980c2c93d5b38676cc0446ed8108" {
		t.Errorf("%s Laminar sink image = %q, want the pinned Collector image", model, image)
	}
	if _, exists := laminar["build"]; exists {
		t.Errorf("%s Laminar sink unexpectedly has a build context", model)
	}
	if ports, ok := laminar["ports"].([]any); ok && len(ports) != 0 {
		t.Errorf("%s Laminar sink publishes host ports: %#v", model, ports)
	}
	if got := sortedKeys(objectField(t, laminar, "networks")); !equalStrings(got, []string{"prls-observability"}) ||
		!valueContains(objectField(t, laminar, "networks")["prls-observability"], "laminar") {
		t.Errorf("%s Laminar sink network/alias = %#v, want only the isolated test network with alias laminar", model, laminar["networks"])
	}
	laminarVolumes := laminar["volumes"].([]any)
	if len(laminarVolumes) != 1 {
		t.Fatalf("%s Laminar sink volumes = %#v, want one read-only test config", model, laminar["volumes"])
	}
	laminarConfig := asObject(t, laminarVolumes[0], model+" Laminar sink config mount")
	if stringField(t, laminarConfig, "source") != filepath.Join(absoluteRoot, "deploy", "test", "laminar-sink.yaml") ||
		stringField(t, laminarConfig, "target") != "/etc/otelcol-contrib/config.yaml" || !boolField(t, laminarConfig, "read_only") {
		t.Errorf("%s Laminar sink config mount = %#v", model, laminarConfig)
	}
	collector := asObject(t, services["otel-collector"], model+" Collector")
	collectorDependencies := objectField(t, collector, "depends_on")
	laminarDependency := objectField(t, collectorDependencies, "laminar")
	if stringField(t, laminarDependency, "condition") != "service_healthy" {
		t.Errorf("%s Collector Laminar dependency = %#v, want service_healthy", model, laminarDependency)
	}

	ports, ok := caddy["ports"].([]any)
	if !ok || len(ports) != 2 {
		t.Fatalf("%s Caddy ports = %#v, want loopback HTTP and HTTPS ports", model, caddy["ports"])
	}
	wantPorts := map[string]bool{"127.0.0.1:18080:80": false, "127.0.0.1:18443:443": false}
	for _, raw := range ports {
		port := asObject(t, raw, model+" Caddy port")
		key := fmt.Sprintf("%s:%s:%s", fmt.Sprint(port["host_ip"]), fmt.Sprint(port["published"]), fmt.Sprint(port["target"]))
		if _, exists := wantPorts[key]; !exists {
			t.Errorf("%s Caddy has unexpected published port %s", model, key)
			continue
		}
		wantPorts[key] = true
	}
	for port, found := range wantPorts {
		if !found {
			t.Errorf("%s Caddy is missing loopback binding %s", model, port)
		}
	}

	wantedTargets := map[string]string{
		"/etc/caddy/Caddyfile": filepath.Join(absoluteRoot, "deploy", "test", "Caddyfile.smoke"),
		"/data":                "caddy-smoke-data",
		"/config":              "caddy-smoke-config",
	}
	if frontend {
		wantedTargets["/etc/caddy/Caddyfile"] = filepath.Join(absoluteRoot, "deploy", "test", "Caddyfile.frontend-smoke")
		wantedTargets["/etc/caddy/test"] = filepath.Join(absoluteRoot, "deploy", "test")
	}
	volumes, ok := caddy["volumes"].([]any)
	if !ok {
		t.Fatalf("%s Caddy volumes = %#v, want explicit mounts", model, caddy["volumes"])
	}
	for _, raw := range volumes {
		volume := asObject(t, raw, model+" Caddy volume")
		target := stringField(t, volume, "target")
		source, expected := wantedTargets[target]
		if !expected {
			t.Errorf("%s Caddy has unexpected mount target %s", model, target)
			continue
		}
		delete(wantedTargets, target)
		if target == "/data" || target == "/config" {
			if stringField(t, volume, "type") != "volume" || !strings.HasSuffix(stringField(t, volume, "source"), source) {
				t.Errorf("%s Caddy state at %s is not project-owned volume %s", model, target, source)
			}
		} else {
			if stringField(t, volume, "source") != source || !boolField(t, volume, "read_only") {
				t.Errorf("%s Caddy config mount at %s is not the expected read-only source", model, target)
			}
		}
	}
	for target := range wantedTargets {
		t.Errorf("%s Caddy is missing explicit mount target %s", model, target)
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
		"HARDEN_LLM_LAMINAR_ENDPOINT=",
		"HARDEN_LLM_API_HOST=api.harden.test", "HARDEN_LLM_GRAFANA_HOST=grafana.harden.test",
		"HARDEN_LLM_ARTIFACT_HOST=artifacts.harden.test",
		"HARDEN_LLM_WEB_HOST=app.harden.test",
		"HARDEN_LLM_ARTIFACT_EXTERNAL_ENDPOINT=https://artifacts.harden.test",
		"HARDEN_LLM_BIND_ADDRESS=127.0.0.1", "HARDEN_LLM_HTTP_PORT=18080", "HARDEN_LLM_HTTPS_PORT=18443",
		"PRLS_SMOKE_OBSERVABILITY_NETWORK=harden-llm-contract-observability",
		"SMOKE_CA_CERT=/tmp/harden-llm-contract/ca.crt",
		"SMOKE_PROVIDER_CERT=/tmp/harden-llm-contract/provider.crt",
		"SMOKE_PROVIDER_KEY=/tmp/harden-llm-contract/provider.key",
		"HARDEN_LLM_TLS_MODE=internal", "HARDEN_LLM_POSTGRES_PASSWORD=contract-harden-db-7Y2qN5",
		"HARDEN_LLM_ARTIFACT_ACCESS_KEY_ID=GKCONTRACT000000000000000000000001",
		"HARDEN_LLM_ARTIFACT_SECRET_ACCESS_KEY=contractGarageKey_4Ys8zQ1xN7pV9kM2rT6wE3aB5cD0fH",
		"GARAGE_RPC_SECRET=contractGarageRPCSecret_6mN4vQ8zR2pT5xK9aC1dF7hJ3wE0yB",
		`HARDEN_LLM_ENCRYPTION_KEYS={"primary":"R1BKT3pKV0M1akY2WnlYYU45Sm5UTW82dzBuXzJ4bTk"}`,
		"HARDEN_LLM_ACTIVE_ENCRYPTION_KEY_ID=primary",
		"HARDEN_LLM_RELEASE=contract-1", "HARDEN_LLM_WEB_SECRET_KEY_BASE=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"HARDEN_LLM_WEB_SESSION_SIGNING_SALT=contract-signing-salt",
		"HARDEN_LLM_WEB_SESSION_ENCRYPTION_SALT=contract-encryption-salt",
		"GRAFANA_ADMIN_PASSWORD=contract-grafana-8Zt4pW",
		"COMPOSE_PROJECT_NAME=harden-llm-contract",
	}
}

func mergeEnvironment(base, overrides []string) []string {
	values := make(map[string]string, len(overrides))
	order := make([]string, 0, len(overrides))
	for _, item := range overrides {
		name, value, ok := strings.Cut(item, "=")
		if !ok || name == "" {
			continue
		}
		if _, exists := values[name]; !exists {
			order = append(order, name)
		}
		values[name] = value
	}
	filtered := make([]string, 0, len(base)+len(order))
	for _, item := range base {
		name, _, _ := strings.Cut(item, "=")
		if _, overridden := values[name]; !overridden {
			filtered = append(filtered, item)
		}
	}
	for _, name := range order {
		filtered = append(filtered, name+"="+values[name])
	}
	return filtered
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
