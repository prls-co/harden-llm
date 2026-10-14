package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-461

import (
	"errors"
	"reflect"
	"testing"
)

func TestProfileOptionDecisionWitnesses(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	tokenParam := "max_completion_tokens"
	profile := Profile{APIInferenceType: "responses", SupportsTemperature: &yes, DefaultOptions: map[string]any{"max_tokens": float64(16)}, ReasoningEffortMap: map[string]map[string]any{"lowest": {"reasoning": map[string]any{"effort": "low"}}}}
	for _, tc := range []struct {
		name, effort    string
		overrides, want map[string]any
		field           string
	}{
		{"portable and native conflict", "lowest", map[string]any{"reasoning": map[string]any{"effort": "high"}}, nil, "providerOptions"},
		{"native only", "", map[string]any{"reasoning": map[string]any{"effort": "high"}}, map[string]any{"max_tokens": float64(16), "reasoning": map[string]any{"effort": "high"}}, ""},
		{"portable only", "lowest", nil, map[string]any{"max_tokens": float64(16), "reasoning": map[string]any{"effort": "low"}}, ""},
		{"unsupported portable", "highest", nil, nil, "reasoningEffort"},
		{"explicit false zero and null", "", map[string]any{"stream": false, "max_tokens": float64(0), "stop": nil}, map[string]any{"stream": false, "max_tokens": float64(0), "stop": nil}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := profileOptions(profile, tc.effort, tc.overrides)
			if tc.field != "" {
				var invalid *ValidationError
				if !errors.As(err, &invalid) || invalid.FieldErrors[0].Field != tc.field {
					t.Fatalf("field=%s error=%v", tc.field, err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("options=%#v want=%#v error=%v", got, tc.want, err)
			}
		})
	}
	for _, support := range []*bool{nil, &no, &yes} {
		profile.SupportsTemperature = support
		got, err := profileOptions(profile, "", map[string]any{"temperature": float64(.2)})
		if err != nil {
			t.Fatal(err)
		}
		_, present := got["temperature"]
		if present != (support != nil && *support) {
			t.Fatal("temperature capability differs")
		}
	}
	profile.APIInferenceType = "chat-completions"
	profile.TokensParam = &tokenParam
	for _, tc := range []struct{ overrides, want map[string]any }{
		{nil, map[string]any{"max_completion_tokens": float64(16)}},
		{map[string]any{"max_completion_tokens": float64(8)}, map[string]any{"max_completion_tokens": float64(8)}},
	} {
		got, err := profileOptions(profile, "", tc.overrides)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("token mapping=%#v want=%#v err=%v", got, tc.want, err)
		}
	}
	if !reflect.DeepEqual(profile.DefaultOptions, map[string]any{"max_tokens": float64(16)}) {
		t.Fatal("profile defaults mutated")
	}
}
