// Package api validates wire values against the canonical embedded OpenAPI contract.
package api

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

//go:embed openapi.yaml
var contract []byte

// Compile against the complete embedded document so component and property
// references retain their original JSON pointers under draft 2020-12.
var compiledSchemas = sync.OnceValues(func() (map[string]*jsonschema.Schema, error) {
	const resource = "urn:harden-llm:openapi"
	var doc struct {
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(contract, &doc); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(jsonschema.SchemeURLLoader{})
	if err := compiler.AddResource(resource, map[string]any{"components": map[string]any{"schemas": doc.Components.Schemas}}); err != nil {
		return nil, err
	}
	compiled := make(map[string]*jsonschema.Schema, len(doc.Components.Schemas))
	for name := range doc.Components.Schemas {
		schema, err := compiler.Compile(resource + "#/components/schemas/" + name)
		if err != nil {
			return nil, fmt.Errorf("api: compile %s: %w", name, err)
		}
		compiled[name] = schema
	}
	return compiled, nil
})

// Validate uses the same schema as the REST and reference client contracts.
// The document has local references only; loading and validation perform no I/O.
func Validate(name string, data []byte) error {
	_, err := Decode(name, data)
	return err
}

// Decode validates once and returns the same JSON value for consumers that
// also need correlation or accounting fields. Do not parse a large body again.
func Decode(name string, data []byte) (any, error) {
	schemas, err := compiledSchemas()
	if err != nil {
		return nil, err
	}
	schema := schemas[name]
	if schema == nil {
		return nil, fmt.Errorf("api: unknown schema %q", name)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("api: expected one valid JSON value: %w", err)
	}
	if err := schema.Validate(value); err != nil {
		return nil, err
	}
	return value, nil
}
