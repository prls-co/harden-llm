package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-027

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFirebaseFrontendAbsent(t *testing.T) {
	root := repositoryRoot(t)
	forbiddenProductionTerms := []string{
		"firebase", "firestore", "firebase-functions", "firebase hosting", "firebasestorage",
		"google_application_credentials", "gcloud", "phoenix", "liveview", "react", "vite",
		"browser-session", "browser session", "csrf", "set-cookie", "text/html", "html/template",
	}
	forbiddenExtensions := map[string]bool{
		".heex": true, ".eex": true, ".html": true, ".jsx": true, ".tsx": true, ".css": true,
	}

	backendRoots := []string{
		"artifacts.go", "cache.go", "client.go", "client_artifacts.go", "client_cache.go", "doc.go", "profiles.go", "types.go",
		"go.mod", "go.sum", "Makefile", "cmd", "internal", "api", "scripts", "deploy",
		filepath.Join("fixtures", "parity", "generated"),
	}
	for _, name := range backendRoots {
		path := filepath.Join(root, name)
		info, err := os.Stat(path)
		if errorsIsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if !info.IsDir() {
			scanBackendProductionFile(t, root, path, forbiddenProductionTerms)
			continue
		}
		err = filepath.WalkDir(path, func(candidate string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, _ := filepath.Rel(root, candidate)
			rel = filepath.ToSlash(rel)
			if entry.IsDir() {
				if rel == "frontend" || rel == "deploy/frontend" || strings.HasPrefix(rel, "frontend/") || strings.HasPrefix(rel, "deploy/frontend/") {
					return filepath.SkipDir
				}
				base := strings.ToLower(entry.Name())
				if (base == "assets" || base == "priv" || base == "templates") && (strings.HasPrefix(rel, "cmd/") || strings.HasPrefix(rel, "internal/")) {
					t.Errorf("backend implementation contains frontend directory %s", rel)
				}
				return nil
			}
			if forbiddenExtensions[strings.ToLower(filepath.Ext(candidate))] {
				t.Errorf("backend implementation contains frontend file %s", rel)
			}
			if strings.HasSuffix(candidate, "_test.go") {
				return nil
			}
			scanBackendProductionFile(t, root, candidate, forbiddenProductionTerms)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", name, err)
		}
	}

	rootMakefile := strings.ToLower(string(readFile(t, filepath.Join(root, "Makefile"))))
	for _, command := range []string{"frontend/", "deploy/frontend", "mix test", "npm ", "pnpm ", "yarn ", "vite"} {
		if strings.Contains(rootMakefile, command) {
			t.Errorf("backend Makefile invokes frontend command %q", command)
		}
	}

	for _, directory := range []string{"cmd", "internal"} {
		_ = filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if parseErr != nil {
				t.Errorf("parse imports in %s: %v", path, parseErr)
				return nil
			}
			for _, imported := range file.Imports {
				value, unquoteErr := strconv.Unquote(imported.Path.Value)
				if unquoteErr != nil {
					continue
				}
				lower := strings.ToLower(value)
				for _, forbidden := range []string{"firebase", "firestore", "phoenix", "liveview", "react", "vite", "html/template"} {
					if strings.Contains(lower, forbidden) {
						rel, _ := filepath.Rel(root, path)
						t.Errorf("backend import %q in %s", value, filepath.ToSlash(rel))
					}
				}
			}
			return nil
		})
	}
}

func scanBackendProductionFile(t *testing.T, root, path string, forbidden []string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read %s: %v", path, err)
		return
	}
	if strings.IndexByte(string(contents), 0) >= 0 {
		return
	}
	lower := strings.ToLower(string(contents))
	for _, term := range forbidden {
		if strings.Contains(lower, term) {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("backend production surface contains forbidden term %q in %s", term, filepath.ToSlash(rel))
		}
	}
}

func errorsIsNotExist(err error) bool { return err != nil && os.IsNotExist(err) }
