package deploytest

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-030
// PRLS TEST-005: physical shared-topology contract.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const prlsSharedNetwork = "prls-observability"

var prlsTenants = []string{"prod", "nonprod", "test"}

func TestPRLSComposeSharedOwner(t *testing.T) {
	// TEST-009: live collector restarts must resume file-log ingestion from a
	// durable offset instead of replaying historical files into Loki.
	root := filepath.Clean(filepath.Join("..", ".."))
	config := readYAMLObject(t, filepath.Join(root, "docker-compose.yml"))

	networks := objectField(t, config, "networks")
	shared := objectField(t, networks, prlsSharedNetwork)
	if !boolField(t, shared, "external") || stringField(t, shared, "name") != prlsSharedNetwork {
		t.Fatalf("shared network = %#v, want named external network %q", shared, prlsSharedNetwork)
	}

	services := objectField(t, config, "services")
	wantServices := []string{
		"caddy", "garage", "grafana", "harden-llm-gateway", "harden-postgres",
		"loki", "otel-collector", "otel-collector-state-init", "prometheus", "tempo",
	}
	if got := sortedKeys(services); !reflect.DeepEqual(got, wantServices) {
		t.Fatalf("harden-owned services = %v, want exactly %v", got, wantServices)
	}
	for _, forbidden := range []string{"allure", "laminar", "temporal", "temporal-ui"} {
		if _, exists := services[forbidden]; exists {
			t.Errorf("physical owner must reuse external %s rather than declare it", forbidden)
		}
	}

	sharedServices := []string{"caddy", "garage", "grafana", "loki", "otel-collector", "prometheus"}
	for _, name := range sharedServices {
		service := asObject(t, services[name], name)
		if !prlsValueContains(service["networks"], prlsSharedNetwork) {
			t.Errorf("shared service %s is not attached to %s", name, prlsSharedNetwork)
		}
	}
	for name, raw := range services {
		service := asObject(t, raw, name)
		ports, _ := service["ports"].([]any)
		if name != "caddy" && len(ports) != 0 {
			t.Errorf("backend service %s publishes host ports: %#v", name, ports)
		}
	}

	caddyEnv := prlsEnvironmentMap(t, asObject(t, services["caddy"], "caddy"))
	for _, key := range []string{"PRLS_ALLURE_HOST", "PRLS_TESTS_BASIC_AUTH_USER", "PRLS_TESTS_BASIC_AUTH_HASH"} {
		if strings.TrimSpace(caddyEnv[key]) == "" {
			t.Errorf("Caddy environment omits %s", key)
		}
	}
	collectorEnv := prlsEnvironmentMap(t, asObject(t, services["otel-collector"], "otel-collector"))
	if strings.TrimSpace(collectorEnv["PRLS_LAMINAR_PROJECT_API_KEY"]) == "" {
		t.Error("Collector environment omits PRLS_LAMINAR_PROJECT_API_KEY")
	}
	collector := asObject(t, services["otel-collector"], "otel-collector")
	if !prlsValueContains(collector["volumes"], "otel-collector-state:/var/lib/otelcol") {
		t.Error("Collector does not persist file-log offsets in a named volume")
	}
	collectorDepends := objectField(t, collector, "depends_on")
	collectorInitDependency := objectField(t, collectorDepends, "otel-collector-state-init")
	if got := stringField(t, collectorInitDependency, "condition"); got != "service_completed_successfully" {
		t.Errorf("Collector state initializer dependency = %q", got)
	}
	initializer := asObject(t, services["otel-collector-state-init"], "otel-collector-state-init")
	if stringField(t, initializer, "image") != "postgres:17.9-alpine@sha256:c7526c0f6c3f30260a563d7bcf8ad778effac59a44f8ffa86678c35418338609" ||
		stringField(t, initializer, "user") != "0:0" || stringField(t, initializer, "network_mode") != "none" ||
		!boolField(t, initializer, "read_only") || stringField(t, initializer, "restart") != "no" {
		t.Errorf("Collector state initializer is not a bounded pinned one-shot: %#v", initializer)
	}
	assertStringSliceEqual(t, "collector state initializer entrypoint", stringSliceField(t, initializer, "entrypoint"), []string{"/bin/chown"})
	assertStringSliceEqual(t, "collector state initializer command", stringSliceField(t, initializer, "command"), []string{"10001:10001", "/var/lib/otelcol"})
	if !prlsValueContains(initializer["volumes"], "otel-collector-state:/var/lib/otelcol") {
		t.Error("Collector state initializer does not own the durable volume")
	}
	loki := asObject(t, services["loki"], "loki")
	lokiEnv := prlsEnvironmentMap(t, loki)
	for _, key := range []string{"PRLS_LOKI_S3_ACCESS_KEY", "PRLS_LOKI_S3_SECRET_KEY"} {
		if strings.TrimSpace(lokiEnv[key]) == "" {
			t.Errorf("Loki environment omits %s", key)
		}
	}
	if !prlsValueContains(loki["command"], "-config.expand-env=true") {
		t.Error("Loki does not enable environment expansion for S3 credentials")
	}
	if !prlsValueContains(asObject(t, services["prometheus"], "prometheus")["volumes"], "deploy/prometheus/rules") {
		t.Error("Prometheus does not mount the PRLS alert rules read-only")
	}
}

func TestPRLSCollectorIsolation(t *testing.T) {
	config := readYAMLObject(t, filepath.Join("..", "..", "deploy", "otel", "collector.yaml"))
	extensions := objectField(t, config, "extensions")
	fileStorage := objectField(t, extensions, "file_storage/harden_llm_web")
	if got := stringField(t, fileStorage, "directory"); got != "/var/lib/otelcol" {
		t.Errorf("collector file storage directory = %q, want /var/lib/otelcol", got)
	}
	if !boolField(t, fileStorage, "create_directory") {
		t.Error("collector file storage does not create its named-volume directory")
	}
	service := objectField(t, config, "service")
	if !prlsContains(stringSliceField(t, service, "extensions"), "file_storage/harden_llm_web") {
		t.Error("collector service does not enable durable file-log offset storage")
	}
	frontendConfig := readYAMLObject(t, filepath.Join("..", "..", "deploy", "frontend", "otel.frontend.yaml"))
	frontendFilelog := objectField(t, objectField(t, frontendConfig, "receivers"), "filelog/harden_llm_web")
	if got := stringField(t, frontendFilelog, "start_at"); got != "end" {
		t.Errorf("frontend file-log initial position = %q, want end", got)
	}
	if got := stringField(t, frontendFilelog, "storage"); got != "file_storage/harden_llm_web" {
		t.Errorf("frontend file-log storage = %q", got)
	}
	receivers := objectField(t, config, "receivers")
	for index, tenant := range prlsTenants {
		name := "otlp/prls_" + tenant
		protocols := objectField(t, objectField(t, receivers, name), "protocols")
		wantGRPC := fmt.Sprintf("0.0.0.0:%d4317", index+1)
		wantHTTP := fmt.Sprintf("0.0.0.0:%d4318", index+1)
		if got := stringField(t, objectField(t, protocols, "grpc"), "endpoint"); got != wantGRPC {
			t.Errorf("%s gRPC endpoint = %q, want %q", name, got, wantGRPC)
		}
		if got := stringField(t, objectField(t, protocols, "http"), "endpoint"); got != wantHTTP {
			t.Errorf("%s HTTP endpoint = %q, want %q", name, got, wantHTTP)
		}
	}
	allureCheck := objectField(t, receivers, "http_check/allure")
	if durationField(t, allureCheck, "collection_interval") > 30*time.Second {
		t.Errorf("Allure HTTP check interval is not bounded: %#v", allureCheck)
	}
	checkTargets := sliceField(t, allureCheck, "targets")
	if len(checkTargets) != 1 {
		t.Fatalf("Allure HTTP check targets = %#v, want one", checkTargets)
	}
	checkTarget := asObject(t, checkTargets[0], "Allure HTTP check target")
	if got := stringField(t, checkTarget, "endpoint"); got != "http://allure:3000/api/ping" {
		t.Errorf("Allure HTTP check endpoint = %q", got)
	}
	if got := stringField(t, checkTarget, "method"); got != "GET" {
		t.Errorf("Allure HTTP check method = %q", got)
	}

	processors := objectField(t, config, "processors")
	credentials := objectField(t, processors, "attributes/prls_credentials")
	deleted := map[string]bool{}
	for _, raw := range sliceField(t, credentials, "actions") {
		action := asObject(t, raw, "PRLS credential action")
		if stringField(t, action, "action") == "delete" {
			deleted[stringField(t, action, "key")] = true
		}
	}
	for _, key := range []string{
		"http.request.header.authorization", "http.request.header.proxy_authorization",
		"http.request.header.cookie", "http.response.header.set_cookie",
		"db.connection_string", "gen_ai.request.api_key", "prls.auth.token",
	} {
		if !deleted[key] {
			t.Errorf("PRLS credential processor does not delete %q", key)
		}
	}
	for _, contentKey := range []string{
		"gen_ai.prompt", "gen_ai.completion", "gen_ai.request.messages",
		"gen_ai.response.text", "http.request.body", "http.response.body", "source.content",
	} {
		if deleted[contentKey] {
			t.Errorf("PRLS credential-only processor deletes non-secret content %q", contentKey)
		}
	}

	for _, tenant := range prlsTenants {
		processor := objectField(t, processors, "resource/prls_"+tenant)
		actions := sliceField(t, processor, "attributes")
		foundEnvironment := false
		foundNamespace := false
		for _, raw := range actions {
			action := asObject(t, raw, "PRLS resource action")
			if stringField(t, action, "key") == "deployment.environment" {
				foundEnvironment = stringField(t, action, "action") == "upsert" && stringField(t, action, "value") == tenant
			}
			if stringField(t, action, "key") == "service.namespace" {
				foundNamespace = stringField(t, action, "action") == "upsert" && stringField(t, action, "value") == "prls-agents"
			}
		}
		if !foundEnvironment {
			t.Errorf("resource/prls_%s does not enforce deployment.environment=%s", tenant, tenant)
		}
		if !foundNamespace {
			t.Errorf("resource/prls_%s does not enforce the bounded prls-agents namespace", tenant)
		}
	}
	for _, name := range []string{"batch/prls_traces", "batch/prls_logs", "batch/prls_metrics"} {
		batch := objectField(t, processors, name)
		if durationField(t, batch, "timeout") > 5*time.Second || intField(t, batch, "send_batch_max_size") > 2048 {
			t.Errorf("%s is not bounded: %#v", name, batch)
		}
	}

	exporters := objectField(t, config, "exporters")
	hardenLoki := objectField(t, exporters, "otlphttp/loki")
	if got := stringField(t, objectField(t, hardenLoki, "headers"), "X-Scope-OrgID"); got != "fake" {
		t.Errorf("protected harden-LLM Loki tenant header = %q", got)
	}
	for _, tenant := range prlsTenants {
		name := "otlphttp/loki_prls_" + tenant
		exporter := objectField(t, exporters, name)
		if stringField(t, exporter, "endpoint") != "http://loki:3100/otlp" {
			t.Errorf("%s has an unexpected endpoint", name)
		}
		if got := stringField(t, objectField(t, exporter, "headers"), "X-Scope-OrgID"); got != tenant {
			t.Errorf("%s tenant header = %q, want %q", name, got, tenant)
		}
		assertBoundedExporter(t, name, exporter)
	}
	laminar := objectField(t, exporters, "otlp/prls_laminar")
	if stringField(t, laminar, "endpoint") != "laminar:8001" {
		t.Errorf("PRLS Laminar endpoint = %q, want laminar:8001", stringField(t, laminar, "endpoint"))
	}
	if got := stringField(t, objectField(t, laminar, "headers"), "authorization"); got != "Bearer ${env:PRLS_LAMINAR_PROJECT_API_KEY}" {
		t.Errorf("PRLS Laminar authorization metadata = %q", got)
	}
	assertBoundedExporter(t, "otlp/prls_laminar", laminar)

	pipelines := objectField(t, objectField(t, config, "service"), "pipelines")
	wantPRLSPipelines := map[string]pipelineContract{}
	for _, tenant := range prlsTenants {
		receiver := []string{"otlp/prls_" + tenant}
		common := []string{"memory_limiter", "attributes/prls_credentials", "resource/prls_" + tenant}
		wantPRLSPipelines["traces/prls_"+tenant] = pipelineContract{
			receivers: receiver, processors: append(append([]string{}, common...), "batch/prls_traces"), exporters: []string{"otlp/prls_laminar"},
		}
		wantPRLSPipelines["logs/prls_"+tenant] = pipelineContract{
			receivers: receiver, processors: append(append([]string{}, common...), "batch/prls_logs"), exporters: []string{"otlphttp/loki_prls_" + tenant},
		}
		wantPRLSPipelines["metrics/prls_"+tenant] = pipelineContract{
			receivers: receiver, processors: append(append([]string{}, common...), "batch/prls_metrics"), exporters: []string{"prometheus"},
		}
	}
	if len(pipelines) != 14 {
		t.Errorf("Collector pipeline count = %d, want 4 protected + 9 PRLS + 1 Allure health", len(pipelines))
	}
	for name, want := range wantPRLSPipelines {
		pipeline := objectField(t, pipelines, name)
		assertStringSliceEqual(t, name+" receivers", stringSliceField(t, pipeline, "receivers"), want.receivers)
		assertStringSliceEqual(t, name+" processors", stringSliceField(t, pipeline, "processors"), want.processors)
		assertStringSliceEqual(t, name+" exporters", stringSliceField(t, pipeline, "exporters"), want.exporters)
	}
	for _, protected := range []string{"traces/tempo", "traces/langfuse", "metrics", "logs"} {
		if _, exists := pipelines[protected]; !exists {
			t.Errorf("protected harden-LLM pipeline %s is missing", protected)
		}
	}
	allurePipeline := objectField(t, pipelines, "metrics/allure_health")
	assertStringSliceEqual(t, "Allure health receivers", stringSliceField(t, allurePipeline, "receivers"), []string{"http_check/allure"})
	assertStringSliceEqual(t, "Allure health processors", stringSliceField(t, allurePipeline, "processors"), []string{"memory_limiter", "batch/prls_metrics"})
	assertStringSliceEqual(t, "Allure health exporters", stringSliceField(t, allurePipeline, "exporters"), []string{"prometheus"})
}

func TestPRLSLokiTenancyAndStorage(t *testing.T) {
	config := readYAMLObject(t, filepath.Join("..", "..", "deploy", "loki", "loki.yaml"))
	if !boolField(t, config, "auth_enabled") {
		t.Fatal("Loki multi-tenancy is disabled")
	}

	storage := objectField(t, config, "storage_config")
	filesystem := objectField(t, storage, "filesystem")
	if stringField(t, filesystem, "directory") != "/loki/chunks" {
		t.Errorf("historical filesystem directory = %#v", filesystem)
	}
	aws := objectField(t, storage, "aws")
	wantAWS := map[string]string{
		"endpoint": "garage:3900", "region": "garage", "bucketnames": "prls-loki",
		"access_key_id": "${PRLS_LOKI_S3_ACCESS_KEY}", "secret_access_key": "${PRLS_LOKI_S3_SECRET_KEY}",
	}
	for key, want := range wantAWS {
		if got := stringField(t, aws, key); got != want {
			t.Errorf("Loki S3 %s = %q, want %q", key, got, want)
		}
	}
	if !boolField(t, aws, "s3forcepathstyle") || !boolField(t, aws, "insecure") {
		t.Errorf("Loki Garage S3 compatibility flags = %#v", aws)
	}

	periods := sliceField(t, objectField(t, config, "schema_config"), "configs")
	if len(periods) != 2 {
		t.Fatalf("Loki schema period count = %d, want historical and Garage-backed periods", len(periods))
	}
	historical := asObject(t, periods[0], "historical schema period")
	if stringField(t, historical, "from") != "2024-01-01" || stringField(t, historical, "object_store") != "filesystem" || stringField(t, historical, "store") != "tsdb" || stringField(t, historical, "schema") != "v13" {
		t.Errorf("historical schema period changed incompatibly: %#v", historical)
	}
	current := asObject(t, periods[1], "current schema period")
	if stringField(t, current, "from") != "2026-08-23" || stringField(t, current, "object_store") != "s3" || stringField(t, current, "store") != "tsdb" || stringField(t, current, "schema") != "v13" {
		t.Errorf("Garage-backed schema period = %#v", current)
	}

	limits := objectField(t, config, "limits_config")
	if durationField(t, limits, "retention_period") < 120*24*time.Hour {
		t.Errorf("Loki retention = %s, want at least 120 days", stringField(t, limits, "retention_period"))
	}
	attributes := sliceField(t, objectField(t, objectField(t, limits, "otlp_config"), "resource_attributes"), "attributes_config")
	if len(attributes) != 1 {
		t.Fatalf("Loki OTLP resource attribute rules = %#v", attributes)
	}
	indexed := stringSliceField(t, asObject(t, attributes[0], "index-label rule"), "attributes")
	sort.Strings(indexed)
	wantIndexed := []string{"deployment.environment", "prls.agent", "service.name", "service.namespace"}
	if !reflect.DeepEqual(indexed, wantIndexed) {
		t.Errorf("Loki indexed resource attributes = %v, want low-cardinality %v", indexed, wantIndexed)
	}
	for _, forbidden := range []string{"prls.test_run_id", "prls.test_case_id", "prls.job_id", "trace_id", "span_id", "request_id"} {
		if prlsContains(indexed, forbidden) {
			t.Errorf("correlation identifier %q must remain structured metadata", forbidden)
		}
	}
	if stringField(t, objectField(t, config, "compactor"), "delete_request_store") != "s3" {
		t.Error("Loki compactor delete requests are not Garage-backed")
	}
}

func TestPRLSGrafanaPrometheusAndCaddy(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	for _, name := range []string{"alerting", "plugins"} {
		path := filepath.Join(root, "deploy", "grafana", "provisioning", name)
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Errorf("Grafana provisioning directory %s is absent: %v", name, err)
		}
	}
	datasourceConfig := readYAMLObject(t, filepath.Join(root, "deploy", "grafana", "provisioning", "datasources", "datasources.yaml"))
	datasources := sliceField(t, datasourceConfig, "datasources")
	if len(datasources) != 6 {
		t.Errorf("Grafana datasource count = %d, want 3 protected + 3 PRLS Loki", len(datasources))
	}
	byUID := map[string]map[string]any{}
	for _, raw := range datasources {
		datasource := asObject(t, raw, "Grafana datasource")
		byUID[stringField(t, datasource, "uid")] = datasource
	}
	for _, tenant := range prlsTenants {
		uid := "prls-loki-" + tenant
		datasource := byUID[uid]
		if datasource == nil {
			t.Errorf("Grafana omits datasource %s", uid)
			continue
		}
		if stringField(t, datasource, "type") != "loki" || stringField(t, datasource, "url") != "http://loki:3100" || boolField(t, datasource, "editable") {
			t.Errorf("Grafana datasource %s = %#v", uid, datasource)
		}
		if got := stringField(t, objectField(t, datasource, "jsonData"), "httpHeaderName1"); got != "X-Scope-OrgID" {
			t.Errorf("datasource %s tenant header name = %q", uid, got)
		}
		if got := stringField(t, objectField(t, datasource, "secureJsonData"), "httpHeaderValue1"); got != tenant {
			t.Errorf("datasource %s tenant header value = %q, want %q", uid, got, tenant)
		}
	}
	hardenLoki := byUID["harden-loki"]
	if hardenLoki == nil || stringField(t, objectField(t, hardenLoki, "secureJsonData"), "httpHeaderValue1") != "fake" {
		t.Error("protected harden-LLM Loki datasource has no explicit tenant")
	}

	dashboardPath := filepath.Join(root, "deploy", "grafana", "dashboards", "prls-test-observability.json")
	data, err := os.ReadFile(dashboardPath)
	if err != nil {
		t.Fatal(err)
	}
	var dashboard map[string]any
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatalf("parse PRLS dashboard: %v", err)
	}
	if dashboard["uid"] != "prls-test-observability" || dashboard["title"] != "PRLS Test Observability" {
		t.Errorf("PRLS dashboard identity = %q/%q", dashboard["uid"], dashboard["title"])
	}
	panels, ok := dashboard["panels"].([]any)
	if !ok || len(panels) < 4 {
		t.Fatalf("PRLS dashboard panels = %#v, want tenant and exporter panels", dashboard["panels"])
	}
	encodedDashboard := strings.ToLower(string(data))
	for _, tenant := range prlsTenants {
		if !strings.Contains(encodedDashboard, "prls-loki-"+tenant) {
			t.Errorf("PRLS dashboard does not use %s tenant datasource", tenant)
		}
	}
	for _, forbidden := range []string{"by (trace_id", "by(trace_id", "by (span_id", "by(span_id", "by (request_id", "by(request_id"} {
		if strings.Contains(encodedDashboard, forbidden) {
			t.Errorf("PRLS dashboard groups by high-cardinality identifier %q", forbidden)
		}
	}

	prometheus := readYAMLObject(t, filepath.Join(root, "deploy", "prometheus", "prometheus.yaml"))
	ruleFiles := stringSliceField(t, prometheus, "rule_files")
	if !reflect.DeepEqual(ruleFiles, []string{"/etc/prometheus/rules/prls-test-observability.yaml"}) {
		t.Errorf("Prometheus rule files = %v", ruleFiles)
	}
	rules := readYAMLObject(t, filepath.Join(root, "deploy", "prometheus", "rules", "prls-test-observability.yaml"))
	groups := sliceField(t, rules, "groups")
	if len(groups) != 1 {
		t.Fatalf("PRLS Prometheus rule groups = %d, want 1", len(groups))
	}
	alerts := map[string]string{}
	for _, raw := range sliceField(t, asObject(t, groups[0], "PRLS alert group"), "rules") {
		rule := asObject(t, raw, "PRLS alert")
		alerts[stringField(t, rule, "alert")] = strings.TrimSpace(stringField(t, rule, "expr"))
	}
	for _, name := range []string{"PRLSCollectorExportFailures", "PRLSCollectorQueueNearCapacity", "PRLSCollectorRefusedTelemetry", "PRLSLokiDiscardedLogs", "PRLSAllureStorageUnavailable"} {
		if alerts[name] == "" {
			t.Errorf("Prometheus omits non-empty alert %s", name)
		}
	}
	if expression := alerts["PRLSAllureStorageUnavailable"]; !strings.Contains(expression, "httpcheck_status") || !strings.Contains(expression, `http_status_class="2xx"`) {
		t.Errorf("Allure availability alert does not use the Collector HTTP check: %q", expression)
	}

	caddyPath := filepath.Join(root, "deploy", "caddy", "conf.d", "prls-tests.caddy")
	caddy, err := os.ReadFile(caddyPath)
	if err != nil {
		t.Fatal(err)
	}
	caddyText := string(caddy)
	for _, required := range []string{
		"{$PRLS_ALLURE_HOST}", "tls {$HARDEN_LLM_TLS_MODE}", "basic_auth",
		"{$PRLS_TESTS_BASIC_AUTH_USER}", "{$PRLS_TESTS_BASIC_AUTH_HASH}",
		"reverse_proxy allure:3000", "import security_headers",
	} {
		if !strings.Contains(caddyText, required) {
			t.Errorf("authenticated PRLS Caddy include omits %q", required)
		}
	}
	for _, forbidden := range []string{"file_server", "root *", "garage:3901", "garage:3903", "password "} {
		if strings.Contains(strings.ToLower(caddyText), forbidden) {
			t.Errorf("PRLS Caddy include contains forbidden directive %q", forbidden)
		}
	}
}

func prlsEnvironmentMap(t *testing.T, service map[string]any) map[string]string {
	t.Helper()
	result := map[string]string{}
	switch value := service["environment"].(type) {
	case map[string]any:
		for key, raw := range value {
			result[key] = fmt.Sprint(raw)
		}
	case []any:
		for _, raw := range value {
			key, item, _ := strings.Cut(fmt.Sprint(raw), "=")
			result[key] = item
		}
	default:
		t.Fatalf("service environment = %#v, want object or list", service["environment"])
	}
	return result
}

func prlsValueContains(value any, wanted string) bool {
	encoded, _ := json.Marshal(value)
	return strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(wanted))
}

func prlsContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
