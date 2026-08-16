package runtime

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-007

import (
	"reflect"
	"testing"
)

func TestObservabilityContext(t *testing.T) {
	defaults := ObservabilityContext{
		Environment:  "production",
		Release:      "2026.07.1",
		TaskID:       "default-task",
		PromptLabels: []string{"default"},
		Tags:         map[string]string{"team": "platform", "shared": "default"},
		Metadata:     map[string]string{"safe": "default"},
	}
	request := ObservabilityContext{
		TaskID:       "request-task",
		RunID:        "run-1",
		PromptLabels: []string{"request"},
		Tags:         map[string]string{"shared": "request"},
		Metadata:     map[string]string{"safe": "request", "request": "value"},
	}
	merged := MergeObservabilityContext(defaults, request)
	if merged.Environment != "production" || merged.Release != "2026.07.1" || merged.TaskID != "request-task" || merged.RunID != "run-1" {
		t.Fatalf("unexpected scalar merge: %#v", merged)
	}
	if !reflect.DeepEqual(merged.PromptLabels, []string{"request"}) {
		t.Fatalf("prompt labels = %#v", merged.PromptLabels)
	}
	if !reflect.DeepEqual(merged.Tags, map[string]string{"team": "platform", "shared": "request"}) {
		t.Fatalf("tags = %#v", merged.Tags)
	}
	if !reflect.DeepEqual(merged.Metadata, map[string]string{"safe": "request", "request": "value"}) {
		t.Fatalf("metadata = %#v", merged.Metadata)
	}

	labels := MetricLabels(merged)
	if !reflect.DeepEqual(labels, map[string]string{"environment": "production"}) {
		t.Fatalf("metric labels contain high-cardinality context: %#v", labels)
	}
	merged.Tags["new"] = "value"
	if _, ok := defaults.Tags["new"]; ok {
		t.Fatal("merge aliased default tags")
	}
}
