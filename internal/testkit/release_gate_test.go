package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-035 TEST-036 TEST-055

import (
	"encoding/json"
	"path/filepath"
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

func TestReleaseTaskComposition(t *testing.T) {
	// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-055
	root := repositoryRoot(t)
	makefile := string(readFile(t, filepath.Join(root, "Makefile")))
	if !strings.Contains(makeTarget(t, makefile, "test-release"), "$(NODE) scripts/run-test-tier.mjs --task release") {
		t.Fatal("test-release does not delegate to the canonical runner")
	}

	var manifest struct {
		Tasks []struct {
			ID          string          `json:"id"`
			Tier        string          `json:"tier"`
			Resource    string          `json:"resourceClass"`
			RequiredFor []string        `json:"requiredFor"`
			Network     string          `json:"network"`
			Container   json.RawMessage `json:"container"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(readFile(t, filepath.Join(root, "test", "test-tiers.json")), &manifest); err != nil {
		t.Fatalf("parse release manifest: %v", err)
	}
	selected := make(map[string]struct{})
	for _, task := range manifest.Tasks {
		for _, selector := range task.RequiredFor {
			if selector == "release" {
				selected[task.ID] = struct{}{}
			}
		}
	}
	required := []string{
		"go-format", "go-lint", "go-build", "go-static", "go-unit", "go-parity",
		"go-integration", "go-integration-race", "garage-restart-exclusive", "go-api",
		"go-observability", "go-compose", "go-race", "go-vulnerability",
		"frontend-format", "frontend-compile", "frontend-deterministic", "client-core",
		"frontend-browser", "frontend-compose", "frontend-deps-audit", "frontend-hex-audit",
		"frontend-assets-deploy", "frontend-release", "backend-verify-baseline",
	}
	for _, taskID := range required {
		if _, ok := selected[taskID]; !ok {
			t.Errorf("release selection omits required task %q", taskID)
		}
	}
	for _, task := range manifest.Tasks {
		if _, ok := selected[task.ID]; !ok {
			continue
		}
		if task.Network == "public" {
			t.Errorf("release selection includes public-network task %q; live is a separate selector", task.ID)
		}
		if task.ID == "frontend-compose" {
			var container map[string]any
			if err := json.Unmarshal(task.Container, &container); err != nil {
				t.Errorf("frontend-compose container is not valid JSON: %v", err)
				continue
			}
			if container["image"] != "harden-llm-browser-test:local" {
				t.Errorf("frontend-compose container image = %v", container["image"])
			}
			if container["dockerSocket"] != true || container["mountAtHostPath"] != true {
				t.Errorf("frontend-compose container must use the Docker socket and host-path mount: %v", container)
			}
		}
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
