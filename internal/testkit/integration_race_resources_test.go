package testkit_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-048
// Race-instrumented SQL deadline measurements own host CPU while running; other
// compiler/test processes are not part of the workload being measured.
func TestIntegrationRaceOwnsTimingResources(t *testing.T) {
	var manifest testFeedbackManifest
	if err := json.Unmarshal(readFile(t, filepath.Join(repositoryRoot(t), "test", "test-tiers.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	for _, task := range manifest.Tasks {
		if task.ID != "go-integration-race" {
			continue
		}
		var resource struct {
			Exclusive bool `json:"exclusive"`
			Slots     int  `json:"slots"`
		}
		if err := json.Unmarshal(manifest.ResourceClasses[task.ResourceClass], &resource); err != nil {
			t.Fatal(err)
		}
		if !resource.Exclusive || resource.Slots != 1 {
			t.Fatal("race-instrumented SQL deadlines must own the host timing resources; independent compilers must not overlap")
		}
		return
	}
	t.Fatal("required integration race task is missing")
}
