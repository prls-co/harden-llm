//go:build compose

package smoke

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-034

import (
	"strings"
	"testing"
)

func TestComposeSmoke(t *testing.T) {
	report := RunComposeSmoke(t)
	if report.ReadyServices != report.TotalServices || report.CorrelatedBackends != report.CorrelationBackends {
		t.Fatalf("incomplete Compose report: %#v", report)
	}
}

func TestComposeFailureDiagnosticsRedactPrivateModuleToken(t *testing.T) {
	token := "private-module-token-fixture"
	runner := composeRunner{environment: map[string]string{"PRIVATE_MODULE_TOKEN": token}}
	got := runner.redact("docker compose failed: " + token)
	if strings.Contains(got, token) || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("build secret was not redacted from Compose diagnostics: %q", got)
	}
}
