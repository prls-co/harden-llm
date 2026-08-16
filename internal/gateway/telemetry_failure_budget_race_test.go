//go:build race

package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-031

import "time"

// The race detector substantially increases the cost of the 12,000+ telemetry
// recording operations in this test under WSL. The application shutdown bound
// remains fixed at two seconds below; this is only a race-build throughput
// margin so scheduling overhead is not mistaken for queue backpressure.
func telemetryFailureRecordingBudgetForTest() time.Duration { return 10 * time.Second }
