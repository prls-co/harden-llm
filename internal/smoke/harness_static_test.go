package smoke

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-034 TEST-060

import (
	"os"
	"strings"
	"testing"
)

func TestComposeHarnessUsesStatelessOpenAIProxyContract(t *testing.T) {
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
	for _, retired := range []string{"HARDEN_LLM_STATIC_TOKEN", "HARDEN_LLM_STATIC_TOKEN_USER_ID", "profileId", "api/v1/profiles", "api/v1/traces"} {
		if strings.Contains(text, retired) {
			t.Fatalf("Compose smoke still uses retired profile/account storage contract %q", retired)
		}
	}
	for _, required := range []string{"HARDEN_LLM_CONFIG_FILE", "HARDEN_LLM_TOKEN", "CPA_API_KEY", "/v1/models", "/v1/responses", "assertProxyComposeBoundary"} {
		if !strings.Contains(text, required) {
			t.Fatalf("Compose smoke omits current proxy boundary %q", required)
		}
	}
	if strings.Contains(text+frontendText, "bootstrap-user") || strings.Contains(text, "/api/v1/auth/login") {
		t.Fatal("Compose smoke still provisions a product-owned human identity")
	}
	if !strings.Contains(frontendText, "startFrontendControlPlaneFixture") {
		t.Fatal("frontend Compose smoke does not use its test-only shared identity boundary")
	}
}
