package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-454 TEST-458

import (
	"encoding/json"
	"fmt"
	"github.com/prls-co/harden-llm/stateful"
	"maps"
	"reflect"
	"strings"
)

// Expectations come from declared setup values and reviewed native wire facts,
// never from the production resolver, option merger or provider serializer.
func presetNativeExpectation(catalog stateful.Catalog, id, model, effort, mode string) (nativeRequestExpectation, error) {
	preset, ok := catalog[id]
	if !ok {
		return nativeRequestExpectation{}, fmt.Errorf("unknown expected profile %s", id)
	}
	var d struct {
		Provider  string                    `json:"provider"`
		Protocol  string                    `json:"apiInferenceType"`
		Model     string                    `json:"modelId"`
		Options   map[string]any            `json:"defaultOptions"`
		Reasoning map[string]map[string]any `json:"reasoningEffortMap"`
	}
	if err := json.Unmarshal(preset.Definition, &d); err != nil {
		return nativeRequestExpectation{}, err
	}
	if model == "" {
		model = d.Model
	}
	e := nativeRequestExpectation{header: "Authorization", fields: map[string]any{}, absent: []string{"temperature"}}
	schemaPath, tokenPath := "", ""
	switch d.Protocol {
	case "responses":
		e.inputPath = "input"
		e.path = "/v1/responses"
		if d.Provider == "perplexity" {
			e.path = "/v1/agent"
		}
		tokenPath = "max_output_tokens"
		schemaPath = "text.format.schema"
		if mode == "structured" {
			e.fields["text.format.type"] = "json_schema"
			e.fields["text.format.strict"] = true
		} else {
			e.absent = append(e.absent, "text.format")
		}
	case "chat-completions":
		e.inputPath = "messages"
		e.path = "/v1/chat/completions"
		tokenPath = "max_tokens"
		schemaPath = "response_format.json_schema.schema"
		if mode == "structured" {
			if d.Provider == "novita" {
				e.fields["response_format.type"] = "json_object"
				schemaPath = ""
			} else {
				e.fields["response_format.type"] = "json_schema"
				e.fields["response_format.json_schema.strict"] = true
			}
		} else {
			e.absent = append(e.absent, "response_format")
		}
	case "anthropic-messages":
		e.inputPath = "messages"
		e.path = "/v1/messages"
		e.header = "X-Api-Key"
		tokenPath = "max_tokens"
		schemaPath = "output_config.format.schema"
		if mode == "structured" {
			e.fields["output_config.format.type"] = "json_schema"
		} else {
			e.absent = append(e.absent, "output_config.format")
		}
	case "gemini-generate-content":
		e.inputPath = "contents"
		e.path = "/v1beta/models/" + model + ":generateContent"
		e.header = "X-Goog-Api-Key"
		tokenPath = "generationConfig.maxOutputTokens"
		schemaPath = "generationConfig.response_schema"
		e.absent = append(e.absent, "model", "generationConfig.temperature")
		if mode == "structured" {
			e.fields["generationConfig.response_mime_type"] = "application/json"
		} else {
			e.absent = append(e.absent, schemaPath, "generationConfig.response_mime_type")
		}
	default:
		return e, fmt.Errorf("unknown native protocol")
	}
	if d.Protocol != "gemini-generate-content" {
		e.fields["model"] = model
	}
	e.fields[tokenPath] = d.Options["max_tokens"]
	if stream, present := d.Options["stream"]; present {
		e.fields["stream"] = stream
	}
	if provider, present := d.Options["provider"]; present {
		e.fields["provider"] = provider
	}
	mapped := d.Reasoning[effort]
	if len(mapped) == 0 {
		e.absent = append(e.absent, "reasoning", "reasoning_effort", "thinking", "generationConfig.thinkingConfig")
	}
	var flatten func(string, any)
	flatten = func(prefix string, value any) {
		if object, ok := value.(map[string]any); ok {
			for key, v := range object {
				flatten(prefix+"."+key, v)
			}
		} else {
			e.fields[strings.TrimPrefix(prefix, ".")] = value
		}
	}
	for key, value := range mapped {
		prefix := key
		if d.Protocol == "gemini-generate-content" {
			prefix = "generationConfig." + key
		}
		flatten(prefix, value)
	}
	if mode == "structured" && schemaPath != "" {
		// All shipped templates intentionally have this reviewed two-field schema.
		e.fields[schemaPath+".type"] = "object"
		e.fields[schemaPath+".properties.joke.type"] = "string"
		e.fields[schemaPath+".properties.explanation.type"] = "string"
		e.fields[schemaPath+".required"] = []any{"joke", "explanation"}
		if d.Protocol == "gemini-generate-content" {
			e.absent = append(e.absent, schemaPath+".additionalProperties")
		} else {
			e.fields[schemaPath+".additionalProperties"] = false
		}
	}
	return e, nil
}

func workflowNativeExpectations(catalog stateful.Catalog, input stateful.RunInput) ([]nativeRequestExpectation, error) {
	// The ordinary matrix edits only prompt and mode. A broken UI must not
	// redefine the shipped starting inputs used by the wire oracle.
	if input.UserPrompt != "fixture-force-recovery" {
		var declared stateful.RunInput
		if err := json.Unmarshal(catalog[input.ProfileID].Inputs, &declared); err != nil {
			return nil, err
		}
		var definition struct {
			Options map[string]any `json:"defaultOptions"`
		}
		if err := json.Unmarshal(catalog[input.ProfileID].Definition, &definition); err != nil {
			return nil, err
		}
		options := maps.Clone(definition.Options)
		maps.Copy(options, declared.ProviderOptions)
		if input.ModelID != declared.ModelID || input.SystemPrompt != declared.SystemPrompt || input.ReasoningEffort != declared.ReasoningEffort || !reflect.DeepEqual(input.ProviderOptions, options) {
			return nil, fmt.Errorf("submitted generation differs from declared preset inputs")
		}
		if string(input.CallType) == "structured" {
			var observedSchema, declaredSchema any
			if json.Unmarshal(input.Schema, &observedSchema) != nil || json.Unmarshal(declared.Schema, &declaredSchema) != nil || !reflect.DeepEqual(observedSchema, declaredSchema) {
				return nil, fmt.Errorf("submitted schema differs from declared preset inputs")
			}
		}
	}
	first, err := presetNativeExpectation(catalog, input.ProfileID, input.ModelID, input.ReasoningEffort, string(input.CallType))
	if err != nil {
		return nil, err
	}
	first.prompt = input.UserPrompt
	expectations := []nativeRequestExpectation{first}
	if input.UserPrompt != "fixture-force-recovery" {
		return expectations, nil
	}
	var policy map[string]any
	if err = json.Unmarshal(input.RecoveryPolicy, &policy); err != nil {
		return nil, err
	}
	for _, path := range [][]string{{"jsonRepair", "initial"}, {"jsonRepair", "escalation"}, {"rerun", "target"}, {"rerun", "jsonRepair", "initial"}, {"rerun", "jsonRepair", "escalation"}} {
		var node any = policy
		for _, key := range path {
			object, ok := node.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("missing expected recovery target")
			}
			node = object[key]
		}
		target, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("missing expected recovery leaf")
		}
		id, _ := target["profileId"].(string)
		model, _ := target["modelId"].(string)
		effort, _ := target["reasoningEffort"].(string)
		e, err := presetNativeExpectation(catalog, id, model, effort, "structured")
		if err != nil {
			return nil, err
		}
		e.prompt = input.UserPrompt
		expectations = append(expectations, e)
	}
	return expectations, nil
}
