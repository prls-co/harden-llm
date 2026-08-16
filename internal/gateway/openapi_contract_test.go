package gateway_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-026

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
)

func TestOpenAPIContract(t *testing.T) {
	contents, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	document, err := loader.LoadFromData(contents)
	if err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	if document.OpenAPI != "3.1.0" || !document.IsOpenAPI31OrLater() {
		t.Fatalf("OpenAPI version = %q", document.OpenAPI)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI: %v", err)
	}
	securityScheme := document.Components.SecuritySchemes["bearerAuth"]
	if securityScheme == nil || securityScheme.Value == nil || securityScheme.Value.Type != "http" || securityScheme.Value.Scheme != "bearer" {
		t.Fatalf("opaque bearer security scheme = %#v", securityScheme)
	}

	type operationKey struct{ method, path string }
	documented := make(map[operationKey]*openapi3.Operation)
	operationIDs := make(map[string]operationKey)
	for path, item := range document.Paths.Map() {
		for method, operation := range item.Operations() {
			key := operationKey{method: strings.ToUpper(method), path: path}
			if operation == nil || operation.OperationID == "" || operation.Summary == "" || operation.Description == "" {
				t.Fatalf("incomplete operation metadata for %s %s", key.method, path)
			}
			if prior, duplicate := operationIDs[operation.OperationID]; duplicate {
				t.Fatalf("duplicate operationId %q at %#v and %#v", operation.OperationID, prior, key)
			}
			documented[key] = operation
			operationIDs[operation.OperationID] = key
			validateOperationExamples(t, document, operation)
		}
	}

	routes := httpapi.Routes()
	if len(documented) != len(routes) {
		t.Fatalf("documented operations=%d router operations=%d", len(documented), len(routes))
	}
	for _, route := range routes {
		key := operationKey{method: route.Method, path: route.Path}
		operation, ok := documented[key]
		if !ok || operation.OperationID != route.OperationID {
			t.Fatalf("router operation %#v maps to %#v", route, operation)
		}
		security := document.Security
		if operation.Security != nil {
			security = *operation.Security
		}
		if route.Protected {
			if len(security) != 1 || len(security[0]) != 1 {
				t.Fatalf("protected operation %s security = %#v", route.OperationID, security)
			}
			if _, ok := security[0]["bearerAuth"]; !ok {
				t.Fatalf("protected operation %s does not require bearerAuth", route.OperationID)
			}
		} else if len(security) != 0 {
			t.Fatalf("public operation %s security = %#v", route.OperationID, security)
		}
		if (operation.RequestBody != nil) != route.RequestBody {
			t.Fatalf("operation %s request-body mismatch", route.OperationID)
		}
		var queryNames []string
		for _, parameter := range operation.Parameters {
			if parameter.Value != nil && parameter.Value.In == "query" {
				queryNames = append(queryNames, parameter.Value.Name)
			}
		}
		slices.Sort(queryNames)
		allowed := append([]string(nil), route.QueryParameters...)
		slices.Sort(allowed)
		if !slices.Equal(queryNames, allowed) {
			t.Fatalf("operation %s query parameters=%v router=%v", route.OperationID, queryNames, allowed)
		}
		delete(documented, key)
	}
	if len(documented) != 0 {
		t.Fatalf("OpenAPI has undocumented router operations: %#v", documented)
	}

	lower := strings.ToLower(string(contents))
	for _, forbidden := range []string{"phoenix", "liveview", "react", "browser cookie", "vite", "csrf", "jsx", "heex"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("OpenAPI contains frontend-specific term %q", forbidden)
		}
	}
}

func validateOperationExamples(t *testing.T, document *openapi3.T, operation *openapi3.Operation) {
	t.Helper()
	if operation.RequestBody != nil && operation.RequestBody.Value != nil {
		media := operation.RequestBody.Value.Content["application/json"]
		if media == nil || media.Schema == nil || len(media.Examples) == 0 {
			t.Fatalf("operation %s has no JSON request schema/example", operation.OperationID)
		}
		for name, example := range media.Examples {
			if example == nil || example.Value == nil || example.Value.Value == nil {
				t.Fatalf("operation %s request example %s is empty", operation.OperationID, name)
			}
			if err := document.ValidateSchemaJSON(media.Schema.Value, example.Value.Value, openapi3.EnableJSONSchema2020()); err != nil {
				t.Fatalf("operation %s request example %s: %v", operation.OperationID, name, err)
			}
		}
	}
	if operation.Responses == nil || operation.Responses.Len() == 0 {
		t.Fatalf("operation %s has no responses", operation.OperationID)
	}
	hasExample := false
	for status, responseRef := range operation.Responses.Map() {
		if responseRef == nil || responseRef.Value == nil {
			t.Fatalf("operation %s response %s is empty", operation.OperationID, status)
		}
		for contentType, media := range responseRef.Value.Content {
			if media.Schema == nil {
				t.Fatalf("operation %s response %s %s has no schema", operation.OperationID, status, contentType)
			}
			for name, example := range media.Examples {
				hasExample = true
				if example == nil || example.Value == nil || example.Value.Value == nil {
					t.Fatalf("operation %s response example %s is empty", operation.OperationID, name)
				}
				if err := document.ValidateSchemaJSON(media.Schema.Value, example.Value.Value, openapi3.EnableJSONSchema2020()); err != nil {
					t.Fatalf("operation %s response %s example %s: %v", operation.OperationID, status, name, err)
				}
			}
		}
	}
	if operation.OperationID != "getArtifact" && !hasExample {
		t.Fatalf("operation %s has no response example", operation.OperationID)
	}
}
