package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-001

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTargetLayout(t *testing.T) {
	root := repositoryRoot(t)
	goMod := string(readFile(t, filepath.Join(root, "go.mod")))
	if !strings.Contains(goMod, "module github.com/prls-co/harden-llm") {
		t.Error("go.mod must declare module github.com/prls-co/harden-llm")
	}

	required := []string{
		"cmd/harden-llm-gateway/main.go",
		"api/openapi.yaml",
		"internal/testkit",
		"internal/artifacts",
		"scripts",
		"fixtures/parity",
		"plans/implementation-status.json",
	}
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Errorf("required foundation path %s: %v", name, err)
		}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	foundRootPackage := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(root, entry.Name()), nil, parser.PackageClauseOnly)
		if parseErr != nil {
			t.Errorf("parse root file %s: %v", entry.Name(), parseErr)
			continue
		}
		foundRootPackage = true
		if file.Name.Name != "hardenllm" {
			t.Errorf("root file %s declares package %s, want hardenllm", entry.Name(), file.Name.Name)
		}
	}
	if !foundRootPackage {
		t.Error("no root hardenllm package files found")
	}

	command := exec.Command("go", "list", ".")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("root package is not importable: %v\n%s", err, output)
	}
}
