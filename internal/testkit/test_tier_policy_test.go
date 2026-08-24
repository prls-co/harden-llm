package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-041 TEST-050

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type testTierPolicyManifest struct {
	Tasks []struct {
		ID             string            `json:"id"`
		Tier           string            `json:"tier"`
		ResourceClass  string            `json:"resourceClass"`
		Command        []string          `json:"command"`
		DependsOn      []string          `json:"dependsOn"`
		Network        string            `json:"network"`
		CredentialKeys []string          `json:"credentialKeys"`
		RequiredFor    []string          `json:"requiredFor"`
		Container      json.RawMessage   `json:"container"`
		Environment    map[string]string `json:"environment"`
	} `json:"tasks"`
}

func TestTestTierPolicy(t *testing.T) {
	root := repositoryRoot(t)
	makefile := string(readFile(t, filepath.Join(root, "Makefile")))

	for _, target := range []string{"test-fast", "test-browser", "test-release", "test-live", "benchmark-test-feedback"} {
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:`).MatchString(makefile) {
			t.Errorf("Makefile is missing additive target %q", target)
		}
	}
	verifyLine := "verify: format lint build test-static test-unit test-parity test-integration test-integration-race test-api test-observability test-race test-vulnerability"
	if !strings.Contains(makefile, verifyLine) {
		t.Errorf("Makefile verify dependency contract changed or is missing: %q", verifyLine)
	}
	for _, selector := range []string{"fast", "browser", "release", "live"} {
		body := makeTargetBody(makefile, "test-"+selector)
		want := "$(NODE) scripts/run-test-tier.mjs --task " + selector
		if !strings.Contains(body, want) {
			t.Errorf("test-%s does not delegate to the canonical runner with %q", selector, want)
		}
		if strings.Contains(body, "go test") || strings.Contains(body, "mix test") {
			t.Errorf("test-%s duplicates task command composition in Makefile", selector)
		}
	}
	if !strings.Contains(makeTargetBody(makefile, "benchmark-test-feedback"), "scripts/benchmark-test-feedback.mjs") {
		t.Error("benchmark-test-feedback does not delegate to the benchmark harness")
	}

	manifestPath := filepath.Join(root, "test", "test-tiers.json")
	var manifest testTierPolicyManifest
	if err := json.Unmarshal(readFile(t, manifestPath), &manifest); err != nil {
		t.Fatalf("parse %s: %v", manifestPath, err)
	}
	byID := make(map[string]struct {
		Tier           string
		ResourceClass  string
		Command        []string
		DependsOn      []string
		Network        string
		CredentialKeys []string
		Container      json.RawMessage
		Environment    map[string]string
	}, len(manifest.Tasks))
	for _, task := range manifest.Tasks {
		byID[task.ID] = struct {
			Tier           string
			ResourceClass  string
			Command        []string
			DependsOn      []string
			Network        string
			CredentialKeys []string
			Container      json.RawMessage
			Environment    map[string]string
		}{task.Tier, task.ResourceClass, task.Command, task.DependsOn, task.Network, task.CredentialKeys, task.Container, task.Environment}
	}
	selected := map[string]bool{}
	var selectTask func(string)
	selectTask = func(taskID string) {
		if selected[taskID] {
			return
		}
		selected[taskID] = true
		task, ok := byID[taskID]
		if !ok {
			t.Fatalf("fast task dependency %q is absent from the manifest", taskID)
		}
		for _, dependency := range task.DependsOn {
			selectTask(dependency)
		}
	}
	for _, task := range manifest.Tasks {
		if contains(task.RequiredFor, "fast") {
			selectTask(task.ID)
		}
	}
	if len(selected) == 0 {
		t.Fatal("manifest selects no tasks for fast")
	}
	for taskID := range selected {
		task := byID[taskID]
		if task.Tier != "T0" && task.Tier != "T1" && task.Tier != "T2" {
			t.Errorf("fast task %q has expensive tier %q", taskID, task.Tier)
		}
		if task.Network != "forbidden" || len(task.CredentialKeys) != 0 {
			t.Errorf("fast task %q is not offline and credential-free: network=%q credentials=%v", taskID, task.Network, task.CredentialKeys)
		}
		if len(task.Container) != 0 && string(task.Container) != "null" {
			t.Errorf("fast task %q starts a container", taskID)
		}
		for key := range task.Environment {
			if regexp.MustCompile(`(?i)(password|secret|token|api[_-]?key|access[_-]?key)`).MatchString(key) {
				t.Errorf("fast task %q declares credential-shaped environment key %q", taskID, key)
			}
		}
		command := strings.ToLower(strings.Join(task.Command, " "))
		for _, forbidden := range []string{"integration", "compose", "browser", "live-structured-call", "-tags=", "govulncheck"} {
			if strings.Contains(command, forbidden) {
				t.Errorf("fast task %q contains forbidden expensive selector %q: %v", taskID, forbidden, task.Command)
			}
		}
	}

	runnerPath := filepath.Join(root, "scripts", "run-test-tier.mjs")
	runner := string(readFile(t, runnerPath))
	for _, primitive := range []string{"HARDEN_LLM_TEST_OFFLINE", "HARDEN_LLM_TEST_NETWORK", "SIGTERM", "SIGKILL", "container.id", "truncatedOutputBytes"} {
		if !strings.Contains(runner, primitive) {
			t.Errorf("canonical runner is missing required policy primitive %q", primitive)
		}
	}
	validatorPath := filepath.Join(root, "scripts", "verify-test-tiers.mjs")
	if !fileExists(t, validatorPath) {
		t.Errorf("tier validator %s is missing", validatorPath)
	}
}

func makeTargetBody(makefile, target string) string {
	pattern := regexp.MustCompile(`(?ms)^` + regexp.QuoteMeta(target) + `:\n(?P<body>(?:\t.*\n|\n)*)`)
	match := pattern.FindStringSubmatch(makefile)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}
