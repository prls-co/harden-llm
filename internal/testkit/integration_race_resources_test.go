package testkit_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-048
// SQL deadline/scale measurements own host CPU while running; other
// compiler/test processes are not part of the workload being measured.
func TestIntegrationOwnsTimingResources(t *testing.T) {
	var manifest testFeedbackManifest
	if err := json.Unmarshal(readFile(t, filepath.Join(repositoryRoot(t), "test", "test-tiers.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"go-integration": false, "go-integration-race": false}
	for _, task := range manifest.Tasks {
		if _, ok := wanted[task.ID]; !ok {
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
			t.Errorf("%s SQL deadlines must own the host timing resources; independent compilers must not overlap", task.ID)
		}
		wanted[task.ID] = true
	}
	for id, found := range wanted {
		if !found {
			t.Errorf("required integration task %s is missing", id)
		}
	}
}
