package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-002

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestImplementationBoundaries(t *testing.T) {
	root := repositoryRoot(t)
	exported := rootExports(t, root)
	required := []string{
		"New", "Client", "Options", "Request", "Result", "Profile", "ProfileCatalog",
		"CredentialResolver", "EndpointPolicy", "CacheStore", "ArtifactStore", "ArtifactRef",
	}
	for _, name := range required {
		if !exported[name] {
			t.Errorf("root package must export %s", name)
		}
	}
	for _, forbidden := range []string{"SimpleCall", "DetailedCall", "ExpandedResult", "NewOpenAIProvider", "NewGeminiProvider", "NewAnthropicProvider"} {
		if exported[forbidden] {
			t.Errorf("root package exposes forbidden execution or provider surface %s", forbidden)
		}
	}

	gatewayRoot := filepath.Join(root, "internal", "gateway")
	_ = filepath.WalkDir(gatewayRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Errorf("parse gateway imports in %s: %v", path, parseErr)
			return nil
		}
		for _, imported := range file.Imports {
			pathValue, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				continue
			}
			for _, forbidden := range []string{"/internal/runtime", "/internal/providers", "/internal/retry", "/internal/schema", "/internal/cachekey"} {
				if strings.Contains(pathValue, forbidden) {
					t.Errorf("gateway bypasses root package through %s", pathValue)
				}
			}
		}
		return nil
	})

	homes := map[string][]string{
		"provider payloads": {"internal/providers"},
		"retry classifier":  {"internal/retry"},
		"schema transform":  {"internal/schema"},
		"cache hashing":     {"internal/cachekey"},
		"pricing":           {"internal/pricing"},
		"trace projection":  {"internal/traces"},
		"redaction":         {"internal/redact"},
	}
	for concern, candidates := range homes {
		seen := 0
		for _, candidate := range candidates {
			if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(candidate))); err == nil && info.IsDir() {
				seen++
			}
		}
		if seen > 1 {
			t.Errorf("%s has %d implementation homes", concern, seen)
		}
	}

	postgresPackage, err := build.Default.ImportDir(filepath.Join(root, "internal", "postgres"), 0)
	if err != nil {
		t.Fatalf("resolve production Postgres files: %v", err)
	}
	forbiddenStoreMethods := map[string]bool{
		"SaveRun": false, "SaveTrace": false, "SaveArtifact": false,
		"DeleteExecution": false, "ClearExecutions": false,
	}
	for _, name := range postgresPackage.GoFiles {
		file, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(postgresPackage.Dir, name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parse production Postgres file %s: %v", name, parseErr)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil {
				continue
			}
			if _, forbidden := forbiddenStoreMethods[function.Name.Name]; forbidden {
				forbiddenStoreMethods[function.Name.Name] = true
			}
		}
	}
	for method, found := range forbiddenStoreMethods {
		if found {
			t.Errorf("production Postgres exposes independent execution mutation method %s", method)
		}
	}
}

func rootExports(t *testing.T, root string) map[string]bool {
	t.Helper()
	packages, err := parser.ParseDir(token.NewFileSet(), root, func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg := packages["hardenllm"]
	if pkg == nil {
		t.Fatal("root package hardenllm not found")
	}
	result := make(map[string]bool)
	for _, file := range pkg.Files {
		for name := range file.Scope.Objects {
			if ast.IsExported(name) {
				result[name] = true
			}
		}
	}
	return result
}
