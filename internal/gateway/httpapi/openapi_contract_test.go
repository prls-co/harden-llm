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
		"/v1/jobs": {}, "/v1/jobs/{jobID}": {}, "/v1/jobs/{jobID}/result": {},
		"/api/v1/status": {}, "/api/v1/profiles": {}, "/api/v1/profiles/{profileID}": {},
		"/api/v1/profiles/{profileID}/models:refresh": {}, "/api/v1/profiles/bundle": {},
		"/api/v1/run": {}, "/api/v1/state": {}, "/api/v1/history": {},
		"/api/v1/history/{runID}": {}, "/api/v1/stats": {}, "/api/v1/traces/{runID}": {},
		"/api/v1/traces/{runID}/artifacts/{artifactID}": {},
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
