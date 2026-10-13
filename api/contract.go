// Package api validates wire values against the canonical embedded OpenAPI contract.
package api

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/tidwall/gjson"
)

//go:embed openapi.yaml
var contract []byte

var contractOnce sync.Once
var document *openapi3.T
var contractError error

// Validate uses the same schema as the REST and reference client contracts.
// The document has local references only; loading and validation perform no I/O.
func Validate(name string, data []byte) error {
	_, err := Decode(name, data)
	return err
}

// Decode validates once and returns the same JSON value for consumers that
// also need correlation or accounting fields. Do not parse a large body again.
func Decode(name string, data []byte) (any, error) {
	contractOnce.Do(func() {
		loader := openapi3.NewLoader()
		loader.IsExternalRefsAllowed = false
		document, contractError = loader.LoadFromData(contract)
	})
	if contractError != nil {
		return nil, contractError
	}
	schema, ok := document.Components.Schemas[name]
	if !ok || schema.Value == nil {
		return nil, fmt.Errorf("api: unknown schema %q", name)
	}
	if !gjson.ValidBytes(data) {
		return nil, fmt.Errorf("api: expected one valid JSON value")
	}
	value := jsonValue(gjson.ParseBytes(data))
	if err := schema.Value.VisitJSON(value); err != nil {
		return nil, err
	}
	return value, nil
}

// Preserve number lexemes while converting the parser tree into the JSON value
// types expected by the canonical schema validator and accounting consumers.
func jsonValue(value gjson.Result) any {
	switch value.Type {
	case gjson.Number:
		return json.Number(value.Raw)
	case gjson.JSON:
		if value.IsArray() {
			items := []any{}
			value.ForEach(func(_, item gjson.Result) bool {
				items = append(items, jsonValue(item))
				return true
			})
			return items
		}
		fields := map[string]any{}
		value.ForEach(func(key, item gjson.Result) bool {
			fields[key.Str] = jsonValue(item)
			return true
		})
		return fields
	default:
		return value.Value()
	}
}
