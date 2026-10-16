package schema

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-472

import (
	"encoding/json"
	"testing"
)

func TestNullableContractTypes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		wrong any
		child map[string]any
	}{
		{"string", "ok", 42, nil}, {"number", 1.5, "1.5", nil}, {"integer", 2, 1.5, nil}, {"boolean", true, "true", nil},
		{"array", []any{"ok"}, "array", map[string]any{"items": map[string]any{"type": "string"}}},
		{"object", map[string]any{"a": "ok"}, "object", map[string]any{"properties": map[string]any{"a": map[string]any{"type": "string"}}, "required": []string{"a"}, "additionalProperties": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, types := range [][]string{{tc.name, "null"}, {"null", tc.name}} {
				child := map[string]any{"type": types}
				for k, v := range tc.child {
					child[k] = v
				}
				raw, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"value": child}, "required": []string{"value"}, "additionalProperties": false})
				normalized, err := Normalize(raw)
				if err != nil {
					t.Fatal(err)
				}
				for _, value := range []any{nil, tc.value} {
					if err := ValidateValue(normalized, map[string]any{"value": value}); err != nil {
						t.Fatalf("rejected %#v: %v", value, err)
					}
				}
				if err := ValidateValue(normalized, map[string]any{"value": tc.wrong}); err == nil {
					t.Fatalf("accepted wrong value %#v", tc.wrong)
				}
				if err := ValidateValue(normalized, map[string]any{}); err == nil {
					t.Fatal("nullable field became optional")
				}
			}
		})
	}
}

func TestNullableContractRejectsUnsupportedUnionsAndHonorsEnum(t *testing.T) {
	for _, types := range []any{[]any{}, []string{"string"}, []string{"string", "string"}, []string{"null", "null"}, []string{"string", "number"}, []string{"string", "null", "boolean"}, []any{"string", nil}} {
		raw, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": types}}, "required": []string{"value"}, "additionalProperties": false})
		if err := ValidateContract(raw); err == nil {
			t.Fatalf("accepted type declaration %#v", types)
		}
	}
	if err := ValidateContract(json.RawMessage(`{"type":["object","null"],"properties":{},"required":[],"additionalProperties":false}`)); err == nil {
		t.Fatal("accepted nullable root")
	}
	for _, enum := range [][]any{{"ok"}, {"ok", nil}} {
		raw, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": []string{"string", "null"}, "enum": enum}}, "required": []string{"value"}, "additionalProperties": false})
		normalized, err := Normalize(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateValue(normalized, map[string]any{"value": "ok"}); err != nil {
			t.Fatal(err)
		}
		err = ValidateValue(normalized, map[string]any{"value": nil})
		if (err == nil) != (len(enum) == 2) {
			t.Fatalf("null bypassed enum: %v", err)
		}
	}
}
