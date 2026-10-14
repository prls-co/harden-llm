package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-458

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/prls-co/harden-llm/stateful"
)

type nativeRequestExpectation struct {
	path      string
	header    string
	fields    map[string]any
	absent    []string
	inputPath string
	prompt    string
}

func nativeField(body map[string]any, path string) (any, bool) {
	var value any = body
	for _, key := range strings.Split(path, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[key]
		if !ok {
			return nil, false
		}
	}
	return value, true
}

func verifyNativeRequest(r *http.Request, expected nativeRequestExpectation) error {
	if r.Method != http.MethodPost || r.URL.Path != expected.path || r.URL.RawQuery != "" {
		return fmt.Errorf("native method/path/query differs")
	}
	secret := "fixture-provider-secret"
	if expected.header == "Authorization" {
		secret = "Bearer " + secret
	}
	if r.Header.Get(expected.header) != secret {
		return fmt.Errorf("native credential placement differs")
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	var body map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err = decoder.Decode(&body); err != nil || body == nil {
		return fmt.Errorf("invalid native JSON")
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing native JSON")
	}
	if expected.inputPath != "" {
		value, _ := nativeField(body, expected.inputPath)
		items, ok := value.([]any)
		if !ok || len(items) == 0 {
			return fmt.Errorf("native input is empty")
		}
		if expected.prompt != "" && !nativeUserInputContains(items, expected.prompt) {
			return fmt.Errorf("original prompt missing from native user input")
		}
	}
	for path, want := range expected.fields {
		got, present := nativeField(body, path)
		if !present || !reflect.DeepEqual(got, want) {
			return fmt.Errorf("native field %s differs", path)
		}
	}
	for _, path := range expected.absent {
		if _, present := nativeField(body, path); present {
			return fmt.Errorf("unexpected native field %s", path)
		}
	}
	return nil
}

func nativeUserInputContains(items []any, prompt string) bool {
	for _, item := range items {
		message, ok := item.(map[string]any)
		if !ok || message["role"] != "user" {
			continue
		}
		if content, ok := message["content"].(string); ok && strings.Contains(content, prompt) {
			return true
		}
		for _, field := range []string{"content", "parts"} {
			blocks, _ := message[field].([]any)
			for _, block := range blocks {
				part, _ := block.(map[string]any)
				text, _ := part["text"].(string)
				if strings.Contains(text, prompt) {
					return true
				}
			}
		}
	}
	return false
}

func TestPresetNativeFixtureOracle(t *testing.T) {
	t.Parallel()
	expected := nativeRequestExpectation{path: "/v1/responses", header: "Authorization", fields: map[string]any{"model": "fixture-model", "reasoning.effort": "low", "text.format.type": "json_schema", "max_output_tokens": float64(16)}, absent: []string{"temperature"}, inputPath: "input", prompt: "original intent"}
	valid := `{"model":"fixture-model","reasoning":{"effort":"low"},"text":{"format":{"type":"json_schema"}},"max_output_tokens":16,"input":[{"role":"user","content":[{"type":"input_text","text":"original intent"}]}]}`
	for _, tc := range []struct {
		name, body, method, path, key string
		valid                         bool
	}{
		{"valid", valid, "POST", expected.path, "Bearer fixture-provider-secret", true},
		{"malformed", "{", "POST", expected.path, "Bearer fixture-provider-secret", false},
		{"trailing", valid + ` {}`, "POST", expected.path, "Bearer fixture-provider-secret", false},
		{"wrong model", strings.Replace(valid, "fixture-model", "wrong", 1), "POST", expected.path, "Bearer fixture-provider-secret", false},
		{"wrong effort", strings.Replace(valid, `"low"`, `"high"`, 1), "POST", expected.path, "Bearer fixture-provider-secret", false},
		{"wrong schema", strings.Replace(valid, "json_schema", "text", 1), "POST", expected.path, "Bearer fixture-provider-secret", false},
		{"wrong options", strings.Replace(valid, ":16", ":32", 1), "POST", expected.path, "Bearer fixture-provider-secret", false},
		{"unexpected field", strings.TrimSuffix(valid, "}") + `,"temperature":0.2}`, "POST", expected.path, "Bearer fixture-provider-secret", false},
		{"wrong method", valid, "GET", expected.path, "Bearer fixture-provider-secret", false},
		{"wrong path", valid, "POST", "/v1/chat/completions", "Bearer fixture-provider-secret", false},
		{"query credential", valid, "POST", expected.path + "?key=fixture-provider-secret", "Bearer fixture-provider-secret", false},
		{"wrong header", valid, "POST", expected.path, "", false},
		{"prompt only in metadata", strings.TrimSuffix(strings.Replace(valid, `"text":"original intent"`, `"text":"other"`, 1), "}") + `,"metadata":{"note":"original intent"}}`, "POST", expected.path, "Bearer fixture-provider-secret", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Authorization", tc.key)
			err := verifyNativeRequest(request, expected)
			if (err == nil) != tc.valid {
				t.Fatalf("oracle accepted=%v want %v: %v", err == nil, tc.valid, err)
			}
		})
	}
}

func TestPresetNativeFixtureRejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	response := httptest.NewRecorder()
	presetNativeResponse(response, httptest.NewRequest("POST", "/v1/responses", strings.NewReader("{")))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed native request accepted: HTTP %d", response.Code)
	}
}

func TestPresetWorkflowGenerationOracle(t *testing.T) {
	t.Parallel()
	catalog, err := stateful.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var input stateful.RunInput
	if err = json.Unmarshal(catalog["CPA gpt-6.1-sol"].Inputs, &input); err != nil {
		t.Fatal(err)
	}
	input.ProfileID = "CPA gpt-6.1-sol"
	input.ProviderOptions = map[string]any{"max_tokens": float64(16000), "stream": true}
	if _, err = workflowNativeExpectations(catalog, input); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*stateful.RunInput){
		"lost reasoning": func(i *stateful.RunInput) { i.ReasoningEffort = "" },
		"wrong model":    func(i *stateful.RunInput) { i.ModelID = "other" },
		"lost options":   func(i *stateful.RunInput) { i.ProviderOptions = nil },
		"wrong schema":   func(i *stateful.RunInput) { i.Schema = json.RawMessage(`{"type":"string"}`) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := input
			mutate(&changed)
			if _, err := workflowNativeExpectations(catalog, changed); err == nil {
				t.Fatal("changed preset inputs redefined the native expectations")
			}
		})
	}
}
