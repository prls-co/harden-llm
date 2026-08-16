// Package schema owns contracted structured-output normalization and validation.
package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/kaptinlin/jsonrepair"
)

var ErrValueInvalid = errors.New("structured output value is invalid")

var (
	markdownFenceStart = regexp.MustCompile(`(?i)^\s*\x60\x60\x60(?:json)?\s*`)
	markdownFenceEnd   = regexp.MustCompile(`(?i)\s*\x60\x60\x60\s*$`)
	numericString      = regexp.MustCompile(`^-?\d+(?:\.\d+)?$`)
)

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
	value, diagnostic, err := ParseProviderOutput(raw, "")
	if err != nil {
		return nil, diagnostic, err
	}
	if err := ValidateValue(contract, value); err != nil {
		diagnostic := newDiagnostic("schema_validation", raw, err)
		return nil, diagnostic, err
	}
	return normalizeParsedNumbers(value), nil, nil
}

// ParseProviderOutput reproduces the source parser boundary. Contracted
// structured output is repaired locally for every provider except Gemini,
// whose source adapter only strips fences/prose and escapes literal controls.
func ParseProviderOutput(raw, protocol string) (any, *Diagnostic, error) {
	if protocol == "google.gemini.generateContent" {
		value, err := parseGeminiJSON(raw)
		if err != nil {
			diagnostic := newDiagnostic("gemini_json_parse", raw, err)
			return nil, diagnostic, fmt.Errorf("structured Gemini output parse: %w", err)
		}
		return NormalizeNumericStringsDeep(value), nil, nil
	}

	value, err := decodeOneJSON(raw)
	if err == nil {
		return NormalizeNumericStringsDeep(value), nil, nil
	}
	initialErr := err
	repaired, repairErr := jsonrepair.Repair(raw)
	if repairErr != nil {
		diagnostic := newDiagnostic("json_repair", raw, repairErr)
		return nil, diagnostic, fmt.Errorf("structured output repair: %w", repairErr)
	}
	value, err = decodeOneJSON(repaired)
	if err != nil {
		diagnostic := newDiagnostic("repaired_json_parse", raw, err)
		return nil, diagnostic, fmt.Errorf("structured repaired output parse: %w (initial parse: %v)", err, initialErr)
	}
	return NormalizeNumericStringsDeep(value), nil, nil
}

// NormalizeNumericStringsDeep matches the source schema-validation projection.
func NormalizeNumericStringsDeep(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			result[key] = NormalizeNumericStringsDeep(child)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = NormalizeNumericStringsDeep(child)
		}
		return result
	case string:
		trimmed := strings.TrimSpace(typed)
		if numericString.MatchString(trimmed) {
			if number, err := strconv.ParseFloat(trimmed, 64); err == nil {
				return number
			}
		}
	}
	return value
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

func parseGeminiJSON(raw string) (any, error) {
	trimmed := strings.TrimSpace(markdownFenceEnd.ReplaceAllString(markdownFenceStart.ReplaceAllString(raw, ""), ""))
	trimmed = extractTopLevelJSON(trimmed)
	value, err := decodeOneJSON(trimmed)
	if err == nil {
		return value, nil
	}
	return decodeOneJSON(escapeControlsInsideStrings(trimmed))
}

func extractTopLevelJSON(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return trimmed
	}
	if first, last := strings.Index(trimmed, "{"), strings.LastIndex(trimmed, "}"); first >= 0 && last > first {
		return trimmed[first : last+1]
	}
	if first, last := strings.Index(trimmed, "["), strings.LastIndex(trimmed, "]"); first >= 0 && last > first {
		return trimmed[first : last+1]
	}
	return trimmed
}

func escapeControlsInsideStrings(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	inString := false
	escaped := false
	for _, character := range value {
		if escaped {
			output.WriteRune(character)
			escaped = false
			continue
		}
		if character == '\\' {
			output.WriteRune(character)
			escaped = true
			continue
		}
		if character == '"' {
			inString = !inString
			output.WriteRune(character)
			continue
		}
		if inString {
			switch character {
			case '\n':
				output.WriteString(`\n`)
				continue
			case '\r':
				output.WriteString(`\r`)
				continue
			case '\t':
				output.WriteString(`\t`)
				continue
			}
		}
		output.WriteRune(character)
	}
	return output.String()
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
			if reflect.DeepEqual(normalizeParsedNumbers(candidate), normalizeParsedNumbers(value)) {
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
	var number float64
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return false
		}
		number = parsed
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
	return !integer || math.Trunc(number) == number
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

func normalizeParsedNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return float64(integer)
		}
		number, _ := typed.Float64()
		return number
	case []any:
		for index, item := range typed {
			typed[index] = normalizeParsedNumbers(item)
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalizeParsedNumbers(item)
		}
	}
	return value
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
