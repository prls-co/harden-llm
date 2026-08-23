package deploytest

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-030

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestCollectorPipelines(t *testing.T) {
	config := readYAMLObject(t, filepath.Join("..", "..", "deploy", "otel", "collector.yaml"))
	receivers := objectField(t, config, "receivers")
	otlp := objectField(t, receivers, "otlp")
	protocols := objectField(t, otlp, "protocols")
	for protocol, endpoint := range map[string]string{"grpc": "0.0.0.0:4317", "http": "0.0.0.0:4318"} {
		if got := stringField(t, objectField(t, protocols, protocol), "endpoint"); got != endpoint {
			t.Errorf("OTLP %s endpoint = %q, want %q", protocol, got, endpoint)
		}
	}

	processors := objectField(t, config, "processors")
	memoryLimiter := objectField(t, processors, "memory_limiter")
	if durationField(t, memoryLimiter, "check_interval") > 10*time.Second ||
		intField(t, memoryLimiter, "limit_mib") < 256 || intField(t, memoryLimiter, "spike_limit_mib") < 64 {
		t.Fatalf("memory limiter is not production bounded: %#v", memoryLimiter)
	}
	redactionProcessor := objectField(t, processors, "attributes/redact")
	deleted := make(map[string]bool)
	for _, raw := range sliceField(t, redactionProcessor, "actions") {
		action := asObject(t, raw, "redaction action")
		if stringField(t, action, "action") == "delete" {
			deleted[stringField(t, action, "key")] = true
		}
	}
	for _, key := range []string{
		"http.request.header.authorization", "http.request.header.cookie",
		"gen_ai.prompt", "gen_ai.completion", "db.statement", "url.full",
	} {
		if !deleted[key] {
			t.Errorf("redaction processor does not delete %q", key)
		}
	}

	exporters := objectField(t, config, "exporters")
	for name, endpoint := range map[string]string{
		"otlp/tempo":        "tempo:4317",
		"otlphttp/loki":     "http://loki:3100/otlp",
		"otlphttp/langfuse": "http://langfuse-web:3000/api/public/otel",
		"prometheus":        "0.0.0.0:9464",
	} {
		if got := stringField(t, objectField(t, exporters, name), "endpoint"); got != endpoint {
			t.Errorf("exporter %s endpoint = %q, want %q", name, got, endpoint)
		}
	}
	for _, name := range []string{"otlp/tempo", "otlphttp/loki", "otlphttp/langfuse"} {
		assertBoundedExporter(t, name, objectField(t, exporters, name))
	}
	langfuseExporter := objectField(t, exporters, "otlphttp/langfuse")
	if stringField(t, objectField(t, langfuseExporter, "auth"), "authenticator") != "basicauth/langfuse" {
		t.Error("Langfuse OTLP/HTTP exporter does not use the Basic Auth extension")
	}
	if stringField(t, objectField(t, langfuseExporter, "headers"), "x-langfuse-ingestion-version") != "4" {
		t.Error("Langfuse exporter does not pin ingestion version 4")
	}

	service := objectField(t, config, "service")
	assertStringSliceEqual(t, "service extensions", stringSliceField(t, service, "extensions"), []string{"health_check", "basicauth/langfuse"})
	pipelines := objectField(t, service, "pipelines")
	wantPipelines := map[string]pipelineContract{
		"traces/tempo": {
			receivers: []string{"otlp"}, processors: []string{"memory_limiter", "attributes/redact", "batch/tempo"}, exporters: []string{"otlp/tempo"},
		},
		"traces/langfuse": {
			receivers: []string{"otlp"}, processors: []string{"memory_limiter", "attributes/redact", "filter/langfuse", "tail_sampling/langfuse", "batch/langfuse"}, exporters: []string{"otlphttp/langfuse"},
		},
		"metrics": {
			receivers: []string{"otlp"}, processors: []string{"memory_limiter", "attributes/redact", "batch/metrics"}, exporters: []string{"prometheus"},
		},
		"logs": {
			receivers: []string{"otlp"}, processors: []string{"memory_limiter", "attributes/redact", "batch/logs"}, exporters: []string{"otlphttp/loki"},
		},
	}
	if len(pipelines) != len(wantPipelines)+10 {
		t.Errorf("pipeline count = %d, want %d protected + 9 isolated PRLS + 1 Allure health", len(pipelines), len(wantPipelines))
	}
	langfuseReferences := 0
	for name, want := range wantPipelines {
		pipeline := objectField(t, pipelines, name)
		assertStringSliceEqual(t, name+" receivers", stringSliceField(t, pipeline, "receivers"), want.receivers)
		assertStringSliceEqual(t, name+" processors", stringSliceField(t, pipeline, "processors"), want.processors)
		assertStringSliceEqual(t, name+" exporters", stringSliceField(t, pipeline, "exporters"), want.exporters)
		for _, exporter := range stringSliceField(t, pipeline, "exporters") {
			if strings.Contains(exporter, "langfuse") {
				langfuseReferences++
			}
		}
	}
	if langfuseReferences != 1 {
		t.Errorf("Langfuse exporter path count = %d, want 1", langfuseReferences)
	}

	tailSampler := objectField(t, processors, "tail_sampling/langfuse")
	if durationField(t, tailSampler, "decision_wait") < 65*time.Second {
		t.Error("Langfuse tail sampler can decide before the longest gateway trace completes")
	}
	policies := sliceField(t, tailSampler, "policies")
	if len(policies) != 1 {
		t.Fatalf("Langfuse sampling policy count = %d, want 1", len(policies))
	}
	policy := asObject(t, policies[0], "Langfuse sampling policy")
	attributePolicy := objectField(t, policy, "string_attribute")
	if stringField(t, policy, "type") != "string_attribute" || stringField(t, attributePolicy, "key") != "service.name" ||
		!reflect.DeepEqual(stringSliceField(t, attributePolicy, "values"), []string{"harden-llm-gateway"}) {
		t.Fatalf("Langfuse complete-trace policy = %#v", policy)
	}

	assertCollectorTelemetry(t, service)
	assertCollectorRouting(t, pipelines, processors)
	assertNoLiteralSecrets(t, config)
}

type pipelineContract struct {
	receivers  []string
	processors []string
	exporters  []string
}

type fakeSpan struct {
	traceID string
	spanID  string
	service string
}

func assertCollectorRouting(t *testing.T, pipelines, processors map[string]any) {
	t.Helper()
	spans := []fakeSpan{
		{traceID: "gateway-trace", spanID: "root", service: "harden-llm-gateway"},
		{traceID: "gateway-trace", spanID: "provider", service: "harden-llm-gateway"},
		{traceID: "gateway-trace", spanID: "database", service: "harden-llm-gateway"},
		{traceID: "gateway-trace", spanID: "artifact", service: "harden-llm-gateway"},
		{traceID: "langfuse-internal", spanID: "root", service: "langfuse-web"},
		{traceID: "langfuse-internal", spanID: "database", service: "langfuse-web"},
	}
	routedSpans := make(map[string][]fakeSpan)
	routedSignals := map[string]int{}
	for name, raw := range pipelines {
		pipeline := asObject(t, raw, "pipeline "+name)
		if !contains(stringSliceField(t, pipeline, "receivers"), "otlp") {
			continue
		}
		signal := strings.SplitN(name, "/", 2)[0]
		for _, exporter := range stringSliceField(t, pipeline, "exporters") {
			switch signal {
			case "traces":
				selected := append([]fakeSpan(nil), spans...)
				if contains(stringSliceField(t, pipeline, "processors"), "tail_sampling/langfuse") {
					selected = sampleCompleteGatewayTraces(t, selected, objectField(t, processors, "tail_sampling/langfuse"))
				}
				routedSpans[exporter] = append(routedSpans[exporter], selected...)
			case "metrics", "logs":
				routedSignals[exporter]++
			}
		}
	}
	if len(routedSpans["otlp/tempo"]) != len(spans) {
		t.Errorf("Tempo received %d/%d operational spans", len(routedSpans["otlp/tempo"]), len(spans))
	}
	langfuse := routedSpans["otlphttp/langfuse"]
	if len(langfuse) != 4 {
		t.Fatalf("Langfuse received %d gateway spans, want complete 4-span trace", len(langfuse))
	}
	seen := make(map[string]int)
	for _, span := range langfuse {
		if span.traceID != "gateway-trace" || span.service != "harden-llm-gateway" {
			t.Errorf("Langfuse loop/filter failure: %#v", span)
		}
		seen[span.spanID]++
	}
	for _, spanID := range []string{"root", "provider", "database", "artifact"} {
		if seen[spanID] != 1 {
			t.Errorf("Langfuse span %s export count = %d, want 1", spanID, seen[spanID])
		}
	}
	if routedSignals["prometheus"] != 1 || routedSignals["otlphttp/loki"] != 1 {
		t.Errorf("metric/log fake endpoint counts = %#v", routedSignals)
	}
}

func sampleCompleteGatewayTraces(t *testing.T, spans []fakeSpan, sampler map[string]any) []fakeSpan {
	t.Helper()
	policy := asObject(t, sliceField(t, sampler, "policies")[0], "sampling policy")
	values := stringSliceField(t, objectField(t, policy, "string_attribute"), "values")
	selectedTrace := make(map[string]bool)
	for _, span := range spans {
		if contains(values, span.service) {
			selectedTrace[span.traceID] = true
		}
	}
	result := make([]fakeSpan, 0, len(spans))
	for _, span := range spans {
		if selectedTrace[span.traceID] {
			result = append(result, span)
		}
	}
	return result
}

func assertBoundedExporter(t *testing.T, name string, exporter map[string]any) {
	t.Helper()
	queue := objectField(t, exporter, "sending_queue")
	if !boolField(t, queue, "enabled") || intField(t, queue, "queue_size") < 256 || intField(t, queue, "queue_size") > 8192 ||
		intField(t, queue, "num_consumers") < 1 || intField(t, queue, "num_consumers") > 16 {
		t.Errorf("exporter %s queue is not explicitly bounded: %#v", name, queue)
	}
	retry := objectField(t, exporter, "retry_on_failure")
	if !boolField(t, retry, "enabled") || durationField(t, retry, "max_elapsed_time") <= 0 || durationField(t, retry, "max_elapsed_time") > time.Minute ||
		durationField(t, retry, "max_interval") > 10*time.Second {
		t.Errorf("exporter %s retry budget is not bounded: %#v", name, retry)
	}
}

func assertCollectorTelemetry(t *testing.T, service map[string]any) {
	t.Helper()
	telemetry := objectField(t, service, "telemetry")
	metrics := objectField(t, telemetry, "metrics")
	readers := sliceField(t, metrics, "readers")
	if len(readers) != 1 {
		t.Fatalf("Collector internal metric readers = %d, want 1", len(readers))
	}
	pull := objectField(t, asObject(t, readers[0], "metric reader"), "pull")
	prometheus := objectField(t, objectField(t, pull, "exporter"), "prometheus")
	if stringField(t, prometheus, "host") != "0.0.0.0" || intField(t, prometheus, "port") != 8888 {
		t.Fatalf("Collector internal Prometheus endpoint = %#v", prometheus)
	}
}

func assertNoLiteralSecrets(t *testing.T, value any) {
	t.Helper()
	encoded, err := yaml.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"sk-lf-", "pk-lf-", "authorization: basic ", "password: harden", "password: langfuse"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("Collector configuration contains literal secret marker %q", forbidden)
		}
	}
}

func readYAMLObject(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := yaml.Unmarshal(data, &result); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return result
}

func objectField(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := object[key]
	if !ok {
		t.Fatalf("missing object field %q", key)
	}
	return asObject(t, value, key)
}

func asObject(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", label, value)
	}
	return result
}

func sliceField(t *testing.T, object map[string]any, key string) []any {
	t.Helper()
	value, ok := object[key].([]any)
	if !ok {
		t.Fatalf("%s = %#v, want list", key, object[key])
	}
	return value
}

func stringSliceField(t *testing.T, object map[string]any, key string) []string {
	t.Helper()
	raw := sliceField(t, object, key)
	result := make([]string, len(raw))
	for index, value := range raw {
		var ok bool
		result[index], ok = value.(string)
		if !ok {
			t.Fatalf("%s[%d] = %#v, want string", key, index, value)
		}
	}
	return result
}

func stringField(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	value, ok := object[key].(string)
	if !ok {
		t.Fatalf("%s = %#v, want string", key, object[key])
	}
	return value
}

func intField(t *testing.T, object map[string]any, key string) int {
	t.Helper()
	value, ok := object[key].(int)
	if !ok {
		t.Fatalf("%s = %#v, want integer", key, object[key])
	}
	return value
}

func boolField(t *testing.T, object map[string]any, key string) bool {
	t.Helper()
	value, ok := object[key].(bool)
	if !ok {
		t.Fatalf("%s = %#v, want boolean", key, object[key])
	}
	return value
}

func durationField(t *testing.T, object map[string]any, key string) time.Duration {
	t.Helper()
	value := stringField(t, object, key)
	duration, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("%s duration %q: %v", key, value, err)
	}
	return duration
}

func assertStringSliceEqual(t *testing.T, label string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", label, got, want)
	}
}

func contains(values []string, wanted string) bool {
	return strings.Contains("\x00"+strings.Join(values, "\x00")+"\x00", "\x00"+wanted+"\x00")
}

func sortedKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
