// Package eval computes release-threshold metrics from committed parity evidence.
package eval

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type CoverageReport struct {
	Covered       int      `json:"covered"`
	Required      int      `json:"required"`
	Percent       float64  `json:"percent"`
	ProviderCases int      `json:"providerCases"`
	Missing       []string `json:"missing"`
}

func EvaluateParity(repositoryRoot string) (CoverageReport, error) {
	requiredTests := []string{"TEST-012", "TEST-013", "TEST-014", "TEST-015", "TEST-016", "TEST-017", "TEST-018", "TEST-019"}
	requiredClasses := []string{"providers", "usage", "profiles", "traces", "llm-stats-totals", "diagnostics"}
	requiredProviders := []string{"openai-responses", "openai-chat", "generic-openai-compatible", "gemini-generate-content", "anthropic-messages"}
	report := CoverageReport{Required: len(requiredTests) + len(requiredClasses) + len(requiredProviders)}

	testEvidence := make(map[string]bool, len(requiredTests))
	err := filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".codex" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(data)
		for _, testID := range requiredTests {
			if strings.Contains(text, "SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001") && strings.Contains(text, testID) {
				testEvidence[testID] = true
			}
		}
		return nil
	})
	if err != nil {
		return CoverageReport{}, fmt.Errorf("eval: scan test evidence: %w", err)
	}
	for _, testID := range requiredTests {
		if testEvidence[testID] {
			report.Covered++
		} else {
			report.Missing = append(report.Missing, "test:"+testID)
		}
	}

	manifestData, err := os.ReadFile(filepath.Join(repositoryRoot, "fixtures", "parity", "manifest.json"))
	if err != nil {
		return CoverageReport{}, fmt.Errorf("eval: read parity manifest: %w", err)
	}
	var manifest struct {
		Fixtures []struct {
			Class string `json:"class"`
			Path  string `json:"path"`
		} `json:"fixtures"`
	}
	if err = json.Unmarshal(manifestData, &manifest); err != nil {
		return CoverageReport{}, fmt.Errorf("eval: parse parity manifest: %w", err)
	}
	classes := make(map[string]bool)
	for _, fixture := range manifest.Fixtures {
		classes[fixture.Class] = true
	}
	for _, class := range requiredClasses {
		if classes[class] {
			report.Covered++
		} else {
			report.Missing = append(report.Missing, "fixture-class:"+class)
		}
	}

	providerData, err := os.ReadFile(filepath.Join(repositoryRoot, "fixtures", "parity", "generated", "provider-cases.json"))
	if err != nil {
		return CoverageReport{}, fmt.Errorf("eval: read provider fixtures: %w", err)
	}
	var providerFixture struct {
		Cases []struct {
			Name string `json:"name"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(providerData, &providerFixture); err != nil {
		return CoverageReport{}, fmt.Errorf("eval: parse provider fixtures: %w", err)
	}
	providerNames := make(map[string]bool, len(providerFixture.Cases))
	for _, providerCase := range providerFixture.Cases {
		providerNames[providerCase.Name] = true
	}
	report.ProviderCases = len(providerNames)
	for _, name := range requiredProviders {
		if providerNames[name] {
			report.Covered++
		} else {
			report.Missing = append(report.Missing, "provider:"+name)
		}
	}
	if report.Required > 0 {
		report.Percent = float64(report.Covered) * 100 / float64(report.Required)
	}
	slices.Sort(report.Missing)
	return report, nil
}
