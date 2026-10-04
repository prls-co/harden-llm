package smoke

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-034 TEST-060

import (
	"os"
	"strings"
	"testing"
)

func TestComposeHarnessUsesMachineAccountAndCurrentArtifactLifecycle(t *testing.T) {
	contents, err := os.ReadFile("harness_compose.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	frontendFixture, err := os.ReadFile("frontend_fixture_test.go")
	if err != nil {
		t.Fatal(err)
	}
	frontendText := string(frontendFixture)
	if strings.Contains(text, "llm_artifacts WHERE owner_id='smoke-owner' AND available") {
		t.Fatal("Compose cleanup still queries the removed artifact availability column")
	}
	if !strings.Contains(text, "HARDEN_LLM_STATIC_TOKEN_USER_ID") || !strings.Contains(text, "HARDEN_LLM_STATIC_TOKEN") {
		t.Fatal("Compose smoke does not use the scoped machine credential path")
	}
	if strings.Contains(text+frontendText, "bootstrap-user") || strings.Contains(text, "/api/v1/auth/login") {
		t.Fatal("Compose smoke still provisions a product-owned human identity")
	}
	if !strings.Contains(frontendText, "startFrontendControlPlaneFixture") {
		t.Fatal("frontend Compose smoke does not use its test-only shared identity boundary")
	}
	if !strings.Contains(text, "llm_artifacts WHERE owner_id='%s' AND state='available'") {
		t.Fatal("Compose smoke does not assert the current artifact lifecycle state for its account")
	}
}
