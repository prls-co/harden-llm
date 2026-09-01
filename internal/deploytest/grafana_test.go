package deploytest

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-032

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGrafanaArtifacts(t *testing.T) {
	root := filepath.Join("..", "..", "deploy", "grafana")
	datasourceConfig := readYAMLObject(t, filepath.Join(root, "provisioning", "datasources", "datasources.yaml"))
	datasources := sliceField(t, datasourceConfig, "datasources")
	if len(datasources) != 6 {
		t.Fatalf("provisioned datasource count = %d, want 3 protected + 3 PRLS Loki", len(datasources))
	}
	byUID := make(map[string]map[string]any, len(datasources))
	for _, raw := range datasources {
		datasource := asObject(t, raw, "datasource")
		uid := stringField(t, datasource, "uid")
		if boolField(t, datasource, "editable") {
			t.Errorf("datasource %s must be immutable", uid)
		}
		byUID[uid] = datasource
	}
	for uid, contract := range map[string]struct{ kind, url string }{
		"harden-prometheus": {kind: "prometheus", url: "http://prometheus:9090"},
		"harden-loki":       {kind: "loki", url: "http://loki:3100"},
		"harden-tempo":      {kind: "tempo", url: "http://tempo:3200"},
	} {
		datasource, ok := byUID[uid]
		if !ok {
			t.Errorf("missing stable datasource UID %q", uid)
			continue
		}
		if stringField(t, datasource, "type") != contract.kind || stringField(t, datasource, "url") != contract.url {
			t.Errorf("datasource %s = %#v", uid, datasource)
		}
	}
	assertDatasourceCorrelation(t, byUID)

	providerConfig := readYAMLObject(t, filepath.Join(root, "provisioning", "dashboards", "dashboards.yaml"))
	providers := sliceField(t, providerConfig, "providers")
	if len(providers) != 2 {
		t.Fatalf("dashboard provider count = %d, want 2", len(providers))
	}
	wantProviderPaths := map[string]bool{
		"/var/lib/grafana/dashboards":          false,
		"/var/lib/grafana/frontend-dashboards": false,
	}
	for _, raw := range providers {
		provider := asObject(t, raw, "dashboard provider")
		if stringField(t, provider, "type") != "file" || boolField(t, provider, "editable") || !boolField(t, provider, "disableDeletion") {
			t.Fatalf("dashboard provider is not immutable: %#v", provider)
		}
		path := stringField(t, objectField(t, provider, "options"), "path")
		if _, ok := wantProviderPaths[path]; !ok {
			t.Fatalf("unexpected dashboard provider path = %#v", provider)
		}
		wantProviderPaths[path] = true
	}
	for path, found := range wantProviderPaths {
		if !found {
			t.Errorf("dashboard provider path %q is missing", path)
		}
	}

	dashboardPath := filepath.Join(root, "dashboards", "harden-llm-overview.json")
	data, err := os.ReadFile(dashboardPath)
	if err != nil {
		t.Fatal(err)
	}
	var dashboard map[string]any
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatalf("parse %s: %v", dashboardPath, err)
	}
	if dashboard["uid"] != "harden-llm-overview" || dashboard["title"] != "Harden LLM Overview" {
		t.Fatalf("dashboard identity = %q/%q", dashboard["uid"], dashboard["title"])
	}
	panels, ok := dashboard["panels"].([]any)
	if !ok || len(panels) == 0 {
		t.Fatalf("dashboard panels = %#v", dashboard["panels"])
	}
	requiredPanels := map[string]bool{
		"gateway": false, "provider": false, "retry": false, "cache": false,
		"usage / cost": false, "schema / repair": false, "postgres": false,
		"garage artifacts": false, "artifact lifecycle": false, "collector": false, "persistence failures": false,
	}
	allowedUIDs := map[string]bool{"harden-prometheus": true, "harden-loki": true, "harden-tempo": true}
	for _, raw := range panels {
		panel := asObject(t, raw, "dashboard panel")
		title := strings.ToLower(stringField(t, panel, "title"))
		for required := range requiredPanels {
			if strings.Contains(title, required) {
				requiredPanels[required] = true
			}
		}
		datasource := objectField(t, panel, "datasource")
		uid := stringField(t, datasource, "uid")
		if !allowedUIDs[uid] {
			t.Errorf("panel %q uses unprovisioned datasource UID %q", title, uid)
		}
		for _, rawTarget := range sliceField(t, panel, "targets") {
			target := asObject(t, rawTarget, "panel target")
			expression := stringField(t, target, "expr")
			if strings.TrimSpace(expression) == "" {
				t.Errorf("panel %q has an empty query", title)
			}
			assertBoundedDashboardQuery(t, uid, expression)
		}
	}
	for panel, found := range requiredPanels {
		if !found {
			t.Errorf("dashboard is missing required %q panel", panel)
		}
	}
}

func assertDatasourceCorrelation(t *testing.T, byUID map[string]map[string]any) {
	t.Helper()
	prometheusData := objectField(t, byUID["harden-prometheus"], "jsonData")
	exemplars := sliceField(t, prometheusData, "exemplarTraceIdDestinations")
	if len(exemplars) != 1 || stringField(t, asObject(t, exemplars[0], "exemplar link"), "datasourceUid") != "harden-tempo" {
		t.Errorf("Prometheus-to-Tempo exemplar link = %#v", exemplars)
	}
	lokiData := objectField(t, byUID["harden-loki"], "jsonData")
	derivedFields := sliceField(t, lokiData, "derivedFields")
	if len(derivedFields) != 1 {
		t.Fatalf("Loki derived fields = %#v", derivedFields)
	}
	derived := asObject(t, derivedFields[0], "Loki derived field")
	if stringField(t, derived, "datasourceUid") != "harden-tempo" || !strings.Contains(stringField(t, derived, "matcherRegex"), "trace_id") {
		t.Errorf("Loki-to-Tempo trace link = %#v", derived)
	}
	tempoData := objectField(t, byUID["harden-tempo"], "jsonData")
	tracesToLogs := objectField(t, tempoData, "tracesToLogsV2")
	if stringField(t, tracesToLogs, "datasourceUid") != "harden-loki" || !boolField(t, tracesToLogs, "filterByTraceID") {
		t.Errorf("Tempo-to-Loki correlation = %#v", tracesToLogs)
	}
	tracesToMetrics := objectField(t, tempoData, "tracesToMetrics")
	if stringField(t, tracesToMetrics, "datasourceUid") != "harden-prometheus" {
		t.Errorf("Tempo-to-Prometheus correlation = %#v", tracesToMetrics)
	}
}

var (
	labelBlockPattern = regexp.MustCompile(`\{([^{}]*)\}`)
	labelNamePattern  = regexp.MustCompile(`(?:^|,)\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:!?=|=~|!~)`)
	groupingPattern   = regexp.MustCompile(`\b(?:by|without)\s*\(([^)]*)\)`)
)

func assertBoundedDashboardQuery(t *testing.T, datasourceUID, expression string) {
	t.Helper()
	allowed := map[string]bool{
		"route": true, "method": true, "provider": true, "outcome": true, "category": true,
		"cache_outcome": true, "call_type": true, "operation": true, "store": true,
		"scope": true, "token_type": true, "source": true, "kind": true, "repair": true,
		"service_name": true, "le": true,
	}
	if datasourceUID == "harden-tempo" {
		if !strings.Contains(expression, `resource.service.name`) {
			t.Errorf("Tempo query is not scoped to the gateway resource: %s", expression)
		}
		return
	}
	for _, block := range labelBlockPattern.FindAllStringSubmatch(expression, -1) {
		for _, match := range labelNamePattern.FindAllStringSubmatch(block[1], -1) {
			if !allowed[match[1]] {
				t.Errorf("query uses unbounded/undefined label %q: %s", match[1], expression)
			}
		}
	}
	for _, grouping := range groupingPattern.FindAllStringSubmatch(expression, -1) {
		for _, label := range strings.Split(grouping[1], ",") {
			label = strings.TrimSpace(label)
			if label != "" && !allowed[label] {
				t.Errorf("query groups by unbounded/undefined label %q: %s", label, expression)
			}
		}
	}
	if datasourceUID == "harden-prometheus" {
		for _, forbidden := range []string{"profile_id", "model_id", "user_id", "owner_id", "request_id", "run_id", "trace_id", "span_id", "url", "error_message"} {
			if strings.Contains(expression, forbidden) {
				t.Errorf("Prometheus query contains forbidden high-cardinality field %q: %s", forbidden, expression)
			}
		}
	}
}
