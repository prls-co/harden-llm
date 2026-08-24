package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-054

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTestFeedbackTraceability(t *testing.T) {
	root := repositoryRoot(t)

	requiredFiles := []string{
		"AGENTS.md",
		"README.md",
		"frontend/README.md",
		"plans/from_utility-llm/harden-llm-parallel-test-feedback-plan.md",
		"plans/from_utility-llm/harden-llm-self-hosted-test-spec.md",
		"plans/from_utility-llm/phoenix-liveview-frontend-spec.md",
		"plans/implementation-status.json",
		"plans/parallel-test-feedback-hierarchy-implementation-plan.md",
		"docs/architecture.md",
		"docs/requirements-traceability.md",
		"docs/release-certification.md",
		"docs/adr/ADR-HLLM-015-parallel-test-feedback-hierarchy.md",
		"docs/adr/README.md",
		"ker/test-feedback/README.md",
		"ker/test-feedback/baseline.json",
		".github/workflows/test-hierarchy.yml",
		"frontend/test/browser/deployed_canary_test.exs",
		"scripts/run-deployed-browser-test.mjs",
		"test/test-tiers.json",
	}
	for _, relativePath := range requiredFiles {
		if _, err := os.Stat(filepath.Join(root, relativePath)); err != nil {
			t.Errorf("required lifecycle surface %s: %v", relativePath, err)
		}
	}

	assertContains := func(relativePath string, fragments ...string) {
		t.Helper()
		contents := string(readFile(t, filepath.Join(root, relativePath)))
		for _, fragment := range fragments {
			if !strings.Contains(contents, fragment) {
				t.Errorf("%s is missing traceability fragment %q", relativePath, fragment)
			}
		}
	}

	assertContains("AGENTS.md",
		"make test-fast",
		"lowest sufficient tier",
		"expensive-tier defect",
		"serial exception",
		"Happy DOM",
	)
	assertContains("README.md", "make test-fast", "make test-integration", "make test-browser", "make test-release")
	assertContains("frontend/README.md", "test-fast", "T0", "T1", "T2", "T3", "T4", "T5", "Happy DOM", "deployed")
	assertContains(
		"plans/from_utility-llm/harden-llm-self-hosted-test-spec.md",
		"### TEST-041",
		"### TEST-042",
		"### TEST-043",
		"lowest sufficient tier",
		"expensive-tier defect",
	)
	assertContains(
		"plans/from_utility-llm/phoenix-liveview-frontend-spec.md",
		"WEB-TEST-044",
		"WEB-TEST-045",
		"WEB-TEST-046",
		"WEB-TEST-047",
		"WEB-TEST-048",
		"no DOM emulator",
	)
	assertContains(
		"plans/parallel-test-feedback-hierarchy-implementation-plan.md",
		"TEST-054",
		"TEST-055",
		"TEST-056",
		"EVAL-006",
		"EVAL-007",
		"P06: Traceable commands",
		"P07: Merged, deployed, publicly certified, documented, and clean final state",
	)
	assertContains(
		"docs/requirements-traceability.md",
		"TFH-REQ-001",
		"TFH-REQ-014",
		"TEST-054",
		"TEST-055",
		"TEST-056",
	)
	assertContains(
		"docs/architecture.md",
		"T0-T2",
		"service pool",
		"exclusive",
		"Chromium",
	)
	assertContains(
		"docs/release-certification.md",
		"test-release",
		"TEST-055",
		"TEST-056",
		"application-bearing",
	)
	assertContains(
		"docs/adr/ADR-HLLM-015-parallel-test-feedback-hierarchy.md",
		"TEST-042",
		"TEST-043",
		"TEST-053",
		"EVAL-006",
		"expensive-tier defect",
	)
	assertContains("ker/test-feedback/README.md", "EVAL-006", "EVAL-007", "race", "service pool")

	workflow := string(readFile(t, filepath.Join(root, ".github", "workflows", "test-hierarchy.yml")))
	for _, job := range []string{"fast:", "integration:", "browser:", "release:"} {
		if !strings.Contains(workflow, job) {
			t.Errorf("workflow is missing %s job", job)
		}
	}
	for _, command := range []string{"make test-fast", "make test-integration", "make test-browser", "make test-release"} {
		if !strings.Contains(workflow, command) {
			t.Errorf("workflow is missing canonical command %q", command)
		}
	}
	if strings.Contains(workflow, "go test") || strings.Contains(workflow, "mix test") || strings.Contains(workflow, "node --test") {
		t.Error("workflow duplicates suite command composition outside the manifest/Make targets")
	}

	launcher := string(readFile(t, filepath.Join(root, "scripts", "run-deployed-browser-test.mjs")))
	assertContains("scripts/run-deployed-browser-test.mjs", "deploy/langfuse/docker-compose.upstream.yml", "HARDEN_LLM_EXPECTED_RELEASE", "HARDEN_LLM_COMPOSE_ENV_FILE", "--env-file", "HARDEN_LLM_RELEASE = expectedRelease", ".env", "HARDEN_LLM_LOCAL_OPERATOR_EMAIL", "HARDEN_LLM_LOCAL_OPERATOR_PASSWORD", "HARDEN_LLM_WEB_HOST", "HARDEN_LLM_API_HOST", "mix local.hex --force", "mix local.rebar --force", "mix deps.get", "main().then((exitCode)", "process.exitCode = exitCode")
	if strings.Contains(launcher, "console.log(process.env") || strings.Contains(launcher, "JSON.stringify(process.env") {
		t.Error("deployed launcher exposes the process environment")
	}
	if strings.Contains(launcher, "--password") || strings.Contains(launcher, "--api-key") {
		t.Error("deployed launcher places a credential in command arguments")
	}
	canary := string(readFile(t, filepath.Join(root, "frontend", "test", "browser", "deployed_canary_test.exs")))
	for _, fragment := range []string{"@moduletag :deployed", "WEB-TEST-048", "TEST-056", "CPA GPT-5.6 Luna", "History", "logout"} {
		if !strings.Contains(canary, fragment) {
			t.Errorf("deployed canary is missing %q", fragment)
		}
	}

	var status struct {
		TestFeedbackHierarchy struct {
			DocumentID         string   `json:"documentId"`
			ADR                string   `json:"adr"`
			KER                string   `json:"ker"`
			CompletedPhases    []string `json:"completedPhases"`
			CurrentPhase       string   `json:"currentPhase"`
			Status             string   `json:"status"`
			ApplicationRelease any      `json:"applicationRelease"`
		} `json:"testFeedbackHierarchy"`
	}
	statusBytes := readFile(t, filepath.Join(root, "plans", "implementation-status.json"))
	if err := json.Unmarshal(statusBytes, &status); err != nil {
		t.Fatalf("parse implementation status: %v", err)
	}
	hierarchy := status.TestFeedbackHierarchy
	if hierarchy.DocumentID != "PLAN-HARDEN-LLM-TEST-FEEDBACK-002" || hierarchy.ADR != "ADR-HLLM-015" || hierarchy.KER != "KER-HLLM-TEST-FEEDBACK-001" {
		t.Errorf("status does not identify the feedback hierarchy contract: %+v", hierarchy)
	}
	for _, phase := range []string{"P00", "P01", "P02", "P03", "P04", "P05", "P06", "P07"} {
		if !contains(hierarchy.CompletedPhases, phase) {
			t.Errorf("status does not record completed %s", phase)
		}
	}
	if hierarchy.Status == "P06-candidate" {
		if hierarchy.CurrentPhase != "P06" || contains(hierarchy.CompletedPhases, "P06") {
			t.Errorf("candidate status has an invalid phase boundary: completed=%v currentPhase=%q", hierarchy.CompletedPhases, hierarchy.CurrentPhase)
		}
	} else if hierarchy.Status == "P06-complete" {
		if hierarchy.CurrentPhase != "P07" || !contains(hierarchy.CompletedPhases, "P06") {
			t.Errorf("completed P06 status has an invalid phase boundary: completed=%v currentPhase=%q", hierarchy.CompletedPhases, hierarchy.CurrentPhase)
		}
	} else if hierarchy.Status == "P07-complete" {
		if hierarchy.CurrentPhase != "P07" || !contains(hierarchy.CompletedPhases, "P07") {
			t.Errorf("completed P07 status has an invalid phase boundary: completed=%v currentPhase=%q", hierarchy.CompletedPhases, hierarchy.CurrentPhase)
		}
	} else {
		t.Errorf("status is neither a P06 checkpoint nor the completed P07 state: status=%q currentPhase=%q", hierarchy.Status, hierarchy.CurrentPhase)
	}
}
