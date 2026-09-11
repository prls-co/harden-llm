package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-062

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestSyncProfilesRejectsInvalidInputWithoutLeakingSecrets(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"secret-fixture", `{"unknown":"secret-fixture"}`, `{} {"secret-fixture":true}`} {
		err := runSyncProfiles(context.Background(), []string{"--email", "guest@example.test"}, strings.NewReader(input), io.Discard, func(string) string { return "" })
		if err == nil || strings.Contains(err.Error(), "secret-fixture") {
			t.Fatal("invalid input accepted or leaked")
		}
	}
}
