//go:build !race

package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-031

import "time"

func telemetryFailureRecordingBudgetForTest() time.Duration { return telemetryFailureRecordingBudget }
