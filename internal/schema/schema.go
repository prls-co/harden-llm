// Package schema owns contracted structured-output normalization and validation.
package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"unicode"
)

var ErrValueInvalid = errors.New("structured output value is invalid")

var schemaKeywords = map[string]struct{}{
	"$schema": {}, "$defs": {}, "additionalProperties": {}, "allOf": {}, "anyOf": {}, "const": {},
	"default": {}, "definitions": {}, "description": {}, "enum": {}, "examples": {}, "exclusiveMaximum": {},
	"exclusiveMinimum": {}, "format": {}, "items": {}, "maxItems": {}, "maxLength": {}, "maximum": {},
	"minItems": {}, "minLength": {}, "minimum": {}, "multipleOf": {}, "not": {}, "oneOf": {}, "pattern": {},
	"prefixItems": {}, "properties": {}, "propertyOrdering": {}, "required": {}, "title": {}, "type": {}, "uniqueItems": {},
}

var contractedKeywords = map[string]struct{}{
	"type": {}, "properties": {}, "required": {}, "additionalProperties": {}, "items": {}, "description": {}, "enum": {},
}

var typeAliases = map[string]string{
	"array": "array", "bool": "boolean", "boolean": "boolean", "double": "number", "float": "number",
	"int": "integer", "integer": "integer", "list": "array", "null": "null", "number": "number",
	"object": "object", "str": "string", "string": "string", "text": "string",
}

type ContractError struct {
	Path    string
	Keyword string
	Message string
}

func (contractError *ContractError) Error() string {
	return fmt.Sprintf("unsupported structured output schema at %s: %s", contractError.Path, contractError.Message)
}

type Diagnostic struct {
	Category  string
	Stage     string
	Message   string
	RawLength int
	RawTail   string
}

func Normalize(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode structured output schema: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, contractError("/", "schema", "root schema must be an object")
	}
	normalized := normalizeObject(object)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("encode normalized schema: %w", err)
	}
	if err := ValidateContract(encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

func normalizeObject(object map[string]any) map[string]any {
	if !isSchemaObject(object) {
		properties := make(map[string]any, len(object))
		required := make([]string, 0, len(object))
		keys := sortedKeys(object)
		for _, key := range keys {
			properties[key] = normalizeDescriptor(object[key])
			required = append(required, key)
		}
		return map[string]any{
			"type": "object", "properties": properties, "required": required, "additionalProperties": false,
		}
	}
	copy := cloneMap(object)
	if value, ok := copy["type"].(string); ok {
		copy["type"] = normalizeType(value)
	}
	if values, ok := copy["type"].([]any); ok {
		for index, value := range values {
			if text, ok := value.(string); ok {
				values[index] = normalizeType(text)
			}
		}
	}
	if properties, ok := copy["properties"].(map[string]any); ok {
		for key, value := range properties {
			properties[key] = normalizeDescriptor(value)
		}
	}
	if items, exists := copy["items"]; exists {
		if list, ok := items.([]any); ok {
			normalized := make([]any, len(list))
			for index, item := range list {
				normalized[index] = normalizeDescriptor(item)
			}
			copy["items"] = normalized
		} else {
			copy["items"] = normalizeDescriptor(items)
		}
	}
	return copy
}

func normalizeDescriptor(value any) any {
	switch typed := value.(type) {
	case string:
		return map[string]any{"type": normalizeType(typed)}
	case []any:
		item := any("string")
		if len(typed) > 0 {
			item = typed[0]
		}
		return map[string]any{"type": "array", "items": normalizeDescriptor(item)}
	case map[string]any:
		return normalizeObject(typed)
	default:
		return map[string]any{"type": "string"}
	}
}

func ValidateContract(raw json.RawMessage) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return contractError("/", "schema", "schema must be valid JSON")
	}
	if err := requireEOF(decoder); err != nil {
		return contractError("/", "schema", "schema must contain one JSON value")
	}
	node, ok := value.(map[string]any)
	if !ok {
		return contractError("/", "schema", "schema node must be an object")
	}
	return validateNode(node, "", true)
}

func validateNode(node map[string]any, path string, root bool) error {
	for key := range node {
		if _, ok := contractedKeywords[key]; !ok {
			return contractError(joinPath(path, key), key, fmt.Sprintf("%s is not part of the contracted schema subset", key))
		}
	}
	typeName, ok := node["type"].(string)
	if !ok {
		return contractError(joinPath(path, "type"), "type", "type must be a contracted string type")
	}
	switch typeName {
	case "object", "array", "string", "number", "integer", "boolean":
	default:
		return contractError(joinPath(path, "type"), "type", fmt.Sprintf("%s is not a contracted schema type", typeName))
	}
	if root && typeName != "object" {
		return contractError(joinPath(path, "type"), "type", "root schema must be an object")
	}
	if description, exists := node["description"]; exists {
		if _, ok := description.(string); !ok {
			return contractError(joinPath(path, "description"), "description", "description must be a string")
		}
	}
	if enum, exists := node["enum"]; exists {
		values, ok := enum.([]any)
		if !ok || len(values) == 0 {
			return contractError(joinPath(path, "enum"), "enum", "enum must contain scalar values")
		}
		for _, value := range values {
			switch value.(type) {
			case nil, string, bool, json.Number:
			default:
				return contractError(joinPath(path, "enum"), "enum", "enum must contain only scalar values")
			}
		}
	}
	if typeName == "object" {
		properties, ok := node["properties"].(map[string]any)
		if !ok {
			return contractError(joinPath(path, "properties"), "properties", "object schemas must define properties")
		}
		additional, ok := node["additionalProperties"].(bool)
		if !ok || additional {
			return contractError(joinPath(path, "additionalProperties"), "additionalProperties", "object schemas must set additionalProperties: false")
		}
		requiredValues, ok := node["required"].([]any)
		if !ok {
			return contractError(joinPath(path, "required"), "required", "object schemas must list all required properties")
		}
		required := make(map[string]struct{}, len(requiredValues))
		for _, rawName := range requiredValues {
			name, ok := rawName.(string)
			if !ok {
				return contractError(joinPath(path, "required"), "required", "required entries must be strings")
			}
			if _, duplicate := required[name]; duplicate {
				return contractError(joinPath(path, "required"), "required", fmt.Sprintf("required property %q is duplicated", name))
			}
			if _, exists := properties[name]; !exists {
				return contractError(joinPath(path, "required"), "required", fmt.Sprintf("required property %q is not defined", name))
			}
			required[name] = struct{}{}
		}
		for name, rawChild := range properties {
			if _, exists := required[name]; !exists {
				return contractError(joinPath(joinPath(path, "properties"), name), "required", fmt.Sprintf("property %q must be listed in required", name))
			}
			child, ok := rawChild.(map[string]any)
			if !ok {
				return contractError(joinPath(joinPath(path, "properties"), name), "schema", "schema node must be an object")
			}
			if err := validateNode(child, joinPath(joinPath(path, "properties"), name), false); err != nil {
				return err
			}
		}
	}
	if typeName == "array" {
		items, ok := node["items"].(map[string]any)
		if !ok {
			return contractError(joinPath(path, "items"), "items", "array schemas must define one object-form items schema")
		}
		if err := validateNode(items, joinPath(path, "items"), false); err != nil {
			return err
		}
	}
	return nil
}

func ParseAndValidate(raw string, contract json.RawMessage) (any, *Diagnostic, error) {
	if err := ValidateContract(contract); err != nil {
		return nil, nil, err
	}
	value, diagnostic, err := ParseProviderOutput(raw)
	if err != nil {
		return nil, diagnostic, err
	}
	if err := ValidateValue(contract, value); err != nil {
		diagnostic := newDiagnostic("schema_validation", raw, err)
		return nil, diagnostic, err
	}
	return value, nil, nil
}

// ParseProviderOutput decodes exactly one JSON value without changing its types.
// Protocol envelopes are extracted by providers before this shared boundary.
func ParseProviderOutput(raw string) (any, *Diagnostic, error) {
	value, err := decodeOneJSON(raw)
	if err != nil {
		return nil, newDiagnostic("json_parse", raw, err), fmt.Errorf("structured output parse: %w", err)
	}
	return value, nil, nil
}

func decodeOneJSON(raw string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := requireEOF(decoder); err != nil {
		return nil, err
	}
	return value, nil
}

func ValidateValue(contract json.RawMessage, value any) error {
	var schemaValue map[string]any
	decoder := json.NewDecoder(bytes.NewReader(contract))
	decoder.UseNumber()
	if err := decoder.Decode(&schemaValue); err != nil {
		return err
	}
	if err := validateValueNode(schemaValue, value, "$"); err != nil {
		return fmt.Errorf("%w: %v", ErrValueInvalid, err)
	}
	return nil
}

func validateValueNode(node map[string]any, value any, path string) error {
	typeName, _ := node["type"].(string)
	if enum, ok := node["enum"].([]any); ok {
		matched := false
		for _, candidate := range enum {
			if equalJSONValue(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s is not an allowed enum value", path)
		}
	}
	switch typeName {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		properties := node["properties"].(map[string]any)
		for key := range object {
			if _, exists := properties[key]; !exists {
				return fmt.Errorf("%s.%s is not allowed", path, key)
			}
		}
		for key, rawChild := range properties {
			childValue, exists := object[key]
			if !exists {
				return fmt.Errorf("%s.%s is required", path, key)
			}
			if err := validateValueNode(rawChild.(map[string]any), childValue, path+"."+key); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		itemSchema := node["items"].(map[string]any)
		for index, item := range items {
			if err := validateValueNode(itemSchema, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	case "number":
		if !isNumber(value, false) {
			return fmt.Errorf("%s must be a number", path)
		}
	case "integer":
		if !isNumber(value, true) {
			return fmt.Errorf("%s must be an integer", path)
		}
	}
	return nil
}

func isNumber(value any, integer bool) bool {
	number, ok := numericValue(value)
	return ok && (!integer || number.exponent.Sign() >= 0)
}

type decimalValue struct {
	digits   string
	exponent *big.Int
}

// Compare canonical decimal digits and an exponent without expanding powers
// of ten. Work stays proportional to the input, even for very large exponents.
// This representation is only for validation; provider values remain intact.
func numericValue(value any) (decimalValue, bool) {
	switch value.(type) {
	case json.Number, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
	default:
		return decimalValue{}, false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return decimalValue{}, false
	}
	text := strings.ToLower(string(encoded))
	negative := strings.HasPrefix(text, "-")
	mantissa, power, hasPower := strings.Cut(strings.TrimPrefix(text, "-"), "e")
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	exponent := new(big.Int)
	if digits == "" {
		return decimalValue{digits: "0", exponent: exponent}, true
	}
	if hasPower {
		if _, ok := exponent.SetString(power, 10); !ok {
			return decimalValue{}, false
		}
	}
	trimmed := strings.TrimRight(digits, "0")
	exponent.Add(exponent, big.NewInt(int64(len(digits)-len(trimmed)-len(fraction))))
	if negative {
		trimmed = "-" + trimmed
	}
	return decimalValue{digits: trimmed, exponent: exponent}, true
}

func equalJSONValue(left, right any) bool {
	if number, ok := numericValue(left); ok {
		other, ok := numericValue(right)
		return ok && number.digits == other.digits && number.exponent.Cmp(other.exponent) == 0
	}
	switch left := left.(type) {
	case map[string]any:
		right, ok := right.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for key, value := range left {
			other, exists := right[key]
			if !exists || !equalJSONValue(value, other) {
				return false
			}
		}
		return true
	case []any:
		right, ok := right.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for index, value := range left {
			if !equalJSONValue(value, right[index]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(left, right)
	}
}

func newDiagnostic(stage, raw string, err error) *Diagnostic {
	message := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, err.Error())
	if len(message) > 256 {
		message = message[:256]
	}
	return &Diagnostic{
		Category: "parse_error", Stage: stage, Message: message,
		RawLength: utf16Length(raw), RawTail: safeTail(raw, 128),
	}
}

func safeTail(raw string, limit int) string {
	if limit <= 0 {
		return ""
	}
	redacted := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return '*'
		}
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, raw)
	runes := []rune(redacted)
	units := 0
	start := len(runes)
	for start > 0 {
		width := 1
		if runes[start-1] > 0xffff {
			width = 2
		}
		if units+width > limit {
			break
		}
		units += width
		start--
	}
	return string(runes[start:])
}

// utf16Length matches JavaScript String.length, which is part of the source
// parse-diagnostic contract captured from utility-llm.
func utf16Length(value string) int {
	length := 0
	for _, character := range value {
		length++
		if character > 0xffff {
			length++
		}
	}
	return length
}

func isSchemaObject(object map[string]any) bool {
	for key := range object {
		if key != "type" {
			if _, ok := schemaKeywords[key]; ok {
				return true
			}
		}
	}
	if _, exists := object["type"]; !exists {
		return false
	}
	for key := range object {
		if _, ok := schemaKeywords[key]; !ok {
			return false
		}
	}
	return true
}

func normalizeType(value string) string {
	if normalized, ok := typeAliases[strings.ToLower(strings.TrimSpace(value))]; ok {
		return normalized
	}
	return value
}

func cloneMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func sortedKeys(input map[string]any) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func joinPath(path, segment string) string {
	segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
	return path + "/" + segment
}

func contractError(path, keyword, message string) error {
	if path == "" {
		path = "/"
	}
	return &ContractError{Path: path, Keyword: keyword, Message: message}
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("unexpected trailing JSON value")
}
