package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-003
// ADR-HLLM-035 permits self-hosted Temporal only at durable composition boundaries.
import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestTemporalSDKStaysOutsideStatelessCoreAndClients(t *testing.T) {
	root := repositoryRoot(t)
	allowed := func(path string) bool {
		return strings.HasPrefix(path, "stateful/batch/") || path == "cmd/harden-llm-gateway/batch.go" || path == "internal/integrationtest/temporal.go" || path == "internal/gateway/httpapi/jobs_temporal_integration_test.go"
	}
	for _, dir := range []string{".", "cmd", "internal", "stateful", "httpclient", "api"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if dir == "." && path != base {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				name, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				if strings.HasPrefix(name, "go.temporal.io/") && !allowed(rel) {
					t.Errorf("Temporal SDK import outside approved durable boundary: %s", rel)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
