package httpapi

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-403
func TestOpenAIContractOpenAPIRoutes(t *testing.T) {
	t.Parallel()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	document, err := loader.LoadFromFile(filepath.Join("..", "..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load OpenAPI document: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI document: %v", err)
	}

	want := map[string]struct{}{
		"/healthz": {}, "/readyz": {}, "/v1/models": {},
		"/v1/chat/completions": {}, "/v1/responses": {},
	}
	if len(document.Paths.Map()) != len(want) {
		t.Fatalf("OpenAPI paths = %#v, want only %#v", document.Paths.Map(), want)
	}
	for path := range document.Paths.Map() {
		if _, ok := want[path]; !ok {
			t.Errorf("unexpected OpenAPI path %q", path)
		}
	}
	for path := range want {
		if document.Paths.Find(path) == nil {
			t.Errorf("OpenAPI path %q is missing", path)
		}
	}
}
