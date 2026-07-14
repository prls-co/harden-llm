package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-035 TEST-036

import (
	"strings"
	"testing"
)

func TestAggregateReleaseGate(t *testing.T) {
	makefile := string(readFile(t, repositoryRoot(t)+"/Makefile"))
	parity := makeTarget(t, makefile, "test-parity")
	for _, required := range []string{"scripts/verify-parity-fixtures.mjs", "Parity|Contract|Identity|Replay"} {
		if !strings.Contains(parity, required) {
			t.Errorf("test-parity omits %q", required)
		}
	}
	if strings.Contains(parity, "capture-utility-llm") || strings.Contains(parity, "/utility-llm") {
		t.Fatal("aggregate parity gate reads the source repository at runtime")
	}

	verify := makeTarget(t, makefile, "verify")
	verifyHeader := strings.SplitN(verify, "\n", 2)[0]
	dependencies := make(map[string]bool)
	for _, dependency := range strings.Fields(strings.TrimPrefix(verifyHeader, "verify:")) {
		dependencies[dependency] = true
	}
	for _, required := range []string{
		"format", "lint", "build", "test-static", "test-unit", "test-parity",
		"test-integration", "test-integration-race", "test-api", "test-observability",
		"test-race", "test-vulnerability",
	} {
		if !dependencies[required] {
			t.Errorf("verify omits %s", required)
		}
	}
	for _, forbidden := range []string{"frontend", "mix ", "-tags=live", "test-compose"} {
		if strings.Contains(verify, forbidden) {
			t.Errorf("deterministic backend verify includes %q", forbidden)
		}
	}
	if !strings.Contains(makeTarget(t, makefile, "test-integration-race"), "-race") ||
		!strings.Contains(makeTarget(t, makefile, "test-vulnerability"), "govulncheck") {
		t.Fatal("verify does not carry integration-race and vulnerability implementations")
	}
}

func makeTarget(t *testing.T, makefile, target string) string {
	t.Helper()
	lines := strings.Split(makefile, "\n")
	prefix := target + ":"
	for index, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		body := []string{line}
		for next := index + 1; next < len(lines); next++ {
			line = lines[next]
			if line != "" && line[0] != '\t' && line[0] != '#' {
				break
			}
			body = append(body, line)
		}
		return strings.Join(body, "\n")
	}
	t.Fatalf("Makefile target %s not found", target)
	return ""
}
