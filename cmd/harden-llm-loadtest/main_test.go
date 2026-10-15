package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-468

import "testing"

func TestLiveOptInAndBudgetValidation(t *testing.T) {
	for _, args := range [][]string{nil, {"--live", "--output", "unused", "--api-budget", "NaN"}, {"--live", "--output", "unused", "--api-budget", "+Inf"}, {"--live", "--output", "unused", "--api-budget", "6"}, {"--live", "--output", "unused", "--duration", "3h"}} {
		if err := run(args); err == nil {
			t.Fatal("invalid live input accepted", args)
		}
	}
}
