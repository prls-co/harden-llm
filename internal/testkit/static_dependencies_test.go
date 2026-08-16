package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-003

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForbiddenDependencies(t *testing.T) {
	root := repositoryRoot(t)
	backendRoots := []string{"go.mod", "go.sum", "cmd", "internal", "api", "scripts", "deploy"}
	forbidden := []string{
		"firebase", "firestore", "firebase auth", "firebase-functions", "firebase hosting",
		"modernc.org/sqlite", "mattn/go-sqlite", "go.temporal.io", "sentry-go",
		"langfuse-go", "langfuse sdk",
	}
	for _, name := range backendRoots {
		path := filepath.Join(root, name)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			scanForbidden(t, root, path, forbidden)
			continue
		}
		_ = filepath.WalkDir(path, func(candidate string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, candidate)
			if strings.HasPrefix(filepath.ToSlash(rel), "frontend/") || strings.HasPrefix(filepath.ToSlash(rel), "deploy/frontend/") || strings.HasSuffix(candidate, "static_dependencies_test.go") {
				return nil
			}
			if strings.HasSuffix(candidate, "_test.go") {
				return nil
			}
			scanForbidden(t, root, candidate, forbidden)
			return nil
		})
	}

	collector := filepath.Join(root, "deploy", "otel", "collector.yaml")
	if contents, err := os.ReadFile(collector); err == nil {
		count := strings.Count(strings.ToLower(string(contents)), "otlphttp/langfuse:")
		if count != 1 {
			t.Errorf("Collector must own exactly one Langfuse OTLP/HTTP exporter, found %d", count)
		}
	}
}

func scanForbidden(t *testing.T, root, path string, forbidden []string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read %s: %v", path, err)
		return
	}
	lower := strings.ToLower(string(contents))
	for _, term := range forbidden {
		if strings.Contains(lower, term) {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("forbidden backend dependency %q in %s", term, filepath.ToSlash(rel))
		}
	}
}
