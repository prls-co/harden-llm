//go:build compose

package eval

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 EVAL-004

import (
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/smoke"
)

func TestComposeReadinessEval(t *testing.T) {
	report := smoke.RunComposeSmoke(t)
	readyRate := float64(report.ReadyServices) / float64(report.TotalServices)
	correlationRate := float64(report.CorrelatedBackends) / float64(report.CorrelationBackends)

	if readyRate != 1.0 {
		t.Fatalf("compose_service_ready_rate = %.3f, want 1.0: %#v", readyRate, report)
	}
	if report.Readiness > 300*time.Second {
		t.Fatalf("compose_readiness_seconds = %.3f, want <= 300", report.Readiness.Seconds())
	}
	if correlationRate != 1.0 {
		t.Fatalf("backend_correlation_rate = %.3f, want 1.0: %#v", correlationRate, report)
	}
	t.Logf("EVAL-004: compose_service_ready_rate=%.3f compose_readiness_seconds=%.3f backend_correlation_rate=%.3f",
		readyRate, report.Readiness.Seconds(), correlationRate)
}
