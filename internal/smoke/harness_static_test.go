package smoke

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-034 TEST-060

import (
	"os"
	"strings"
	"testing"
)

func TestComposeHarnessUsesCurrentArtifactLifecycleSchema(t *testing.T) {
	contents, err := os.ReadFile("harness_compose.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if strings.Contains(text, "llm_artifacts WHERE owner_id='smoke-owner' AND available") {
		t.Fatal("Compose cleanup still queries the removed artifact availability column")
	}
	if !strings.Contains(text, "llm_artifacts WHERE owner_id='smoke-owner' AND state='available'") {
		t.Fatal("Compose cleanup does not assert the current available artifact lifecycle state")
	}
}
