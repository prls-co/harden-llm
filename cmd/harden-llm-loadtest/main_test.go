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

func TestCPAConcurrencyBounds(t *testing.T) {
	for _, args := range [][]string{
		{"--live", "--cpa-only", "--output", "unused", "--max-concurrency", "0"},
		{"--live", "--cpa-only", "--output", "unused", "--max-concurrency", "129"},
		{"--live", "--cpa-only", "--output", "unused", "--waves", "0"},
		{"--live", "--cpa-only", "--output", "unused", "--waves", "6"},
		{"--live", "--cpa-only", "--output", "unused", "--min-concurrency", "65"},
		{"--live", "--cpa-only", "--output", "unused", "--steady-calls", "1001"},
		{"--live", "--cpa-only", "--output", "unused", "--start-interval", "-1s"},
	} {
		if err := run(args); err == nil {
			t.Fatal("unbounded concurrency accepted", args)
		}
	}
}
