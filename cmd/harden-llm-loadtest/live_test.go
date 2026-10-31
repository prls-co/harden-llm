//go:build live

package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-469

import (
	"os"
	"testing"
)

func TestLiveCampaign(t *testing.T) {
	output := os.Getenv("HARDEN_LLM_CAPACITY_OUTPUT")
	if output == "" {
		t.Fatal("set HARDEN_LLM_CAPACITY_OUTPUT to a new private artifact directory; live campaigns are explicit opt-ins")
	}
	if err := run([]string{"--live", "--output", output}); err != nil {
		t.Fatal(err)
	}
}
