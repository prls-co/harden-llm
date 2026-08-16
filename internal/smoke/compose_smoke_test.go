//go:build compose

package smoke

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-034

import "testing"

func TestComposeSmoke(t *testing.T) {
	report := RunComposeSmoke(t)
	if report.ReadyServices != report.TotalServices || report.CorrelatedBackends != report.CorrelationBackends {
		t.Fatalf("incomplete Compose report: %#v", report)
	}
}
