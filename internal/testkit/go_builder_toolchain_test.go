package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-539

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestGoBuilderToolchainsAgree(t *testing.T) {
	root := repositoryRoot(t)
	image := regexp.MustCompile(`golang:[^\s]+`)
	var canonical string
	for _, path := range []string{"Dockerfile", "frontend/Dockerfile.browser", "deploy/test/fake-provider.Dockerfile"} {
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		builder := image.FindString(string(body))
		if builder == "" {
			t.Fatalf("%s has no Go builder", path)
		}
		if canonical == "" {
			canonical = builder
		} else if builder != canonical {
			t.Errorf("%s must use the gateway's Go builder: got %s, want %s", path, builder, canonical)
		}
	}
}
