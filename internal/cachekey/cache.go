package cachekey

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

const DefaultVersion = "operation-v2"

type Mode string

const (
	ModeOff     Mode = "off"
	ModeCache   Mode = "cache"
	ModeRefresh Mode = "refresh"
)

var forbiddenSemanticHeader = regexp.MustCompile(`(?i)(^|[-_])(authorization|api[-_]?key|secret|token|password|credential|cf-access-client-secret)([-_]|$)`)

func ResolveMode(value string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case "", ModeOff:
		return ModeOff, nil
	case ModeCache:
		return ModeCache, nil
	case ModeRefresh:
		return ModeRefresh, nil
	default:
		return "", fmt.Errorf("unsupported cache mode %q", value)
	}
}

func Normalize(operation Operation) (Operation, error) {
	if strings.TrimSpace(operation.SchemaVersion) != OperationSchemaVersion {
		return Operation{}, fmt.Errorf("unsupported operation schema version %q", operation.SchemaVersion)
	}
	normalized := Operation{
		SchemaVersion: OperationSchemaVersion,
		Protocol:      strings.TrimSpace(operation.Protocol),
		Endpoint: Endpoint{
			Identity: strings.TrimSpace(operation.Endpoint.Identity),
			Method:   strings.ToUpper(strings.TrimSpace(operation.Endpoint.Method)),
			Path:     strings.TrimSpace(operation.Endpoint.Path),
		},
		Model:           strings.TrimSpace(operation.Model),
		Payload:         operation.Payload,
		SemanticHeaders: operation.SemanticHeaders,
		ResponseProjection: ResponseProjection{
			Provider: strings.TrimSpace(operation.ResponseProjection.Provider),
			Kind:     strings.TrimSpace(operation.ResponseProjection.Kind),
			Version:  strings.TrimSpace(operation.ResponseProjection.Version),
		},
	}
	if normalized.Protocol == "" || normalized.Endpoint.Identity == "" || normalized.Endpoint.Method == "" || normalized.Endpoint.Path == "" || normalized.Model == "" || normalized.ResponseProjection.Provider == "" || normalized.ResponseProjection.Kind == "" || normalized.ResponseProjection.Version == "" {
		return Operation{}, errors.New("operation requires protocol, endpoint identity/method/path, model, and response projection")
	}
	if normalized.SemanticHeaders == nil {
		normalized.SemanticHeaders = map[string]any{}
	}
	for header := range normalized.SemanticHeaders {
		if forbiddenSemanticHeader.MatchString(header) {
			return Operation{}, fmt.Errorf("semantic header %q may contain credentials", header)
		}
	}
	if err := validateJSONSafe(normalized.Payload, "operation.payload", make(map[visit]struct{})); err != nil {
		return Operation{}, err
	}
	if err := validateJSONSafe(normalized.SemanticHeaders, "operation.semanticHeaders", make(map[visit]struct{})); err != nil {
		return Operation{}, err
	}
	return normalized, nil
}

func Hash(operation Operation, cacheVersion string) (string, error) {
	normalized, err := Normalize(operation)
	if err != nil {
		return "", err
	}
	cacheVersion = strings.TrimSpace(cacheVersion)
	if cacheVersion == "" {
		cacheVersion = DefaultVersion
	}
	payload, err := StableJSON(map[string]any{"operation": normalized, "cacheVersion": cacheVersion})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func StableJSON(value any) ([]byte, error) {
	if err := validateJSONSafe(value, "$", make(map[visit]struct{})); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return nil, fmt.Errorf("decode canonical JSON: %w", err)
	}
	var buffer bytes.Buffer
	if err := writeCanonical(&buffer, generic); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeCanonical(buffer *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		buffer.WriteString("null")
	case bool:
		if typed {
			buffer.WriteString("true")
		} else {
			buffer.WriteString("false")
		}
	case string:
		encoded, _ := json.Marshal(typed)
		buffer.Write(encoded)
	case json.Number:
		buffer.WriteString(typed.String())
	case []any:
		buffer.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := writeCanonical(buffer, item); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buffer.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			encodedKey, _ := json.Marshal(key)
			buffer.Write(encodedKey)
			buffer.WriteByte(':')
			if err := writeCanonical(buffer, typed[key]); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON value %T", value)
	}
	return nil
}

type visit struct {
	typeName reflect.Type
	pointer  uintptr
}

func validateJSONSafe(value any, path string, seen map[visit]struct{}) error {
	if value == nil {
		return nil
	}
	return validateReflect(reflect.ValueOf(value), path, seen)
}

func validateReflect(value reflect.Value, path string, seen map[visit]struct{}) error {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return validateReflect(value.Elem(), path, seen)
	}
	switch value.Kind() {
	case reflect.Bool, reflect.String:
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return nil
	case reflect.Float32, reflect.Float64:
		if number := value.Float(); mathIsInvalid(number) {
			return fmt.Errorf("%s contains a non-finite number", path)
		}
		return nil
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		return validateReflect(value.Elem(), path, seen)
	case reflect.Slice:
		if value.IsNil() {
			return nil
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return nil
		}
		key := visit{typeName: value.Type(), pointer: value.Pointer()}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%s contains a cycle", path)
		}
		seen[key] = struct{}{}
		defer delete(seen, key)
		for index := 0; index < value.Len(); index++ {
			if err := validateReflect(value.Index(index), fmt.Sprintf("%s[%d]", path, index), seen); err != nil {
				return err
			}
		}
		return nil
	case reflect.Array:
		for index := 0; index < value.Len(); index++ {
			if err := validateReflect(value.Index(index), fmt.Sprintf("%s[%d]", path, index), seen); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("%s must use string object keys", path)
		}
		if value.IsNil() {
			return nil
		}
		key := visit{typeName: value.Type(), pointer: value.Pointer()}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%s contains a cycle", path)
		}
		seen[key] = struct{}{}
		defer delete(seen, key)
		iterator := value.MapRange()
		for iterator.Next() {
			if err := validateReflect(iterator.Value(), path+"."+iterator.Key().String(), seen); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if !field.IsExported() || field.Tag.Get("json") == "-" {
				continue
			}
			if err := validateReflect(value.Field(index), path+"."+field.Name, seen); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%s contains unsupported value %s", path, value.Kind())
	}
}

func mathIsInvalid(value float64) bool {
	return value != value || value > mathMaxFloat || value < -mathMaxFloat
}

const mathMaxFloat = 1.7976931348623157e+308
