package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-005

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var testIDPattern = regexp.MustCompile(`\bTEST-\d{3}\b`)
var webTestIDPattern = regexp.MustCompile(`\bWEB-TEST-\d{3}\b`)

func TestTraceability(t *testing.T) {
	root := repositoryRoot(t)
	planPath := filepath.Join(root, "plans", "from_utility-llm", "harden-llm-self-hosted-implementation-plan.md")
	specPath := filepath.Join(root, "plans", "from_utility-llm", "harden-llm-self-hosted-test-spec.md")
	planIDs := uniqueIDs(backendTestIDs(string(readFile(t, planPath))))
	specText := string(readFile(t, specPath))
	defined := make(map[string]int)
	for _, line := range strings.Split(specText, "\n") {
		if strings.HasPrefix(line, "### TEST-") {
			for _, id := range backendTestIDs(line) {
				defined[id]++
			}
		}
	}
	for _, id := range planIDs {
		if defined[id] != 1 {
			t.Errorf("%s is referenced by the plan and defined %d times in the test specification", id, defined[id])
		}
	}

	var status struct {
		CompletedPhases []string `json:"completedPhases"`
	}
	statusBytes, err := os.ReadFile(filepath.Join(root, "plans", "implementation-status.json"))
	if err != nil {
		t.Fatalf("read implementation status: %v", err)
	}
	if err := json.Unmarshal(statusBytes, &status); err != nil {
		t.Fatalf("parse implementation status: %v", err)
	}

	targetIDs := make(map[string]bool)
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, "_test.go") || strings.Contains(filepath.ToSlash(path), "/.codex/") {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Errorf("read %s: %v", path, readErr)
			return nil
		}
		text := string(contents)
		if hasCommentMarker(text, "SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001") {
			if !strings.Contains(text, "WEB-TEST-") {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("frontend support test %s lacks a WEB-TEST ID", filepath.ToSlash(rel))
			}
			return nil
		}
		if !hasCommentMarker(text, "SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001") {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("target test file %s lacks the canonical specification ID", filepath.ToSlash(rel))
		}
		for _, id := range backendTestIDs(text) {
			targetIDs[id] = true
		}
		return nil
	})

	phaseIDs := map[string][]string{
		"P00": {"TEST-001", "TEST-002", "TEST-003", "TEST-004", "TEST-005"},
		"P01": {"TEST-006", "TEST-007", "TEST-008", "TEST-009", "TEST-010", "TEST-011"},
		"P02": {"TEST-012", "TEST-013", "TEST-014", "TEST-015", "TEST-016", "TEST-017", "TEST-018", "TEST-019"},
		"P03": {"TEST-020", "TEST-021", "TEST-022", "TEST-040"},
		"P04": {"TEST-023", "TEST-024", "TEST-025", "TEST-026", "TEST-027"},
		"P05": {"TEST-028", "TEST-029", "TEST-030", "TEST-031", "TEST-032"},
		"P06": {"TEST-033", "TEST-034"},
		"P07": {"TEST-035", "TEST-036", "TEST-037", "TEST-038", "TEST-039"},
	}
	for _, phase := range status.CompletedPhases {
		for _, id := range phaseIDs[phase] {
			if !targetIDs[id] {
				t.Errorf("completed phase %s has no target test carrying %s", phase, id)
			}
		}
	}
}

func backendTestIDs(text string) []string {
	return testIDPattern.FindAllString(webTestIDPattern.ReplaceAllString(text, ""), -1)
}

func hasCommentMarker(text, marker string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "// "+marker) {
			return true
		}
	}
	return false
}

func uniqueIDs(ids []string) []string {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}
