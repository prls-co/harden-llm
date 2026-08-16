package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/prls-co/harden-llm/internal/runtime"
)

var runtimeOptionKeys = map[string]struct{}{
	"timeout": {}, "maxRetries": {}, "overallTimeoutMs": {}, "maxAttempts": {},
	"baseDelayMs": {}, "maxDelayMs": {}, "enableRetryOn429": {}, "enableRetryOn5xx": {},
	"enableRetryOnNetworkError": {}, "enableRetryOnParseError": {}, "cacheMode": {},
	"cacheVersion": {}, "callType": {}, "reasoningEffort": {}, "structuredRepairRetry": {},
	"useResponsesApi": {},
}

func buildPayload(profile runtime.Profile, call runtime.Call) (string, string, string, map[string]any, map[string]any, error) {
	if call.Repair != nil {
		profile, call = repairInputs(profile, call)
	}
	options, err := mergedOptions(profile, call)
	if err != nil {
		return "", "", "", nil, nil, err
	}
	schema, err := decodedSchema(call)
	if err != nil {
		return "", "", "", nil, nil, err
	}
	switch profile.APIInferenceType {
	case "responses":
		if useResponses, configured := call.ProviderOptions["useResponsesApi"].(bool); configured && !useResponses {
			return profile.Provider, "openai.chat.completions", "/chat/completions", buildChatPayload(profile, call, options, schema), map[string]any{}, nil
		}
		return profile.Provider, "openai.responses", "/responses", buildResponsesPayload(profile, call, options, schema), map[string]any{}, nil
	case "chat-completions":
		return profile.Provider, "openai-compatible.chat.completions", "/chat/completions", buildChatPayload(profile, call, options, schema), map[string]any{}, nil
	case "gemini-generate-content":
		path := "/v1beta/" + strings.TrimLeft(profile.ModelID, "/") + ":generateContent"
		return "google", "google.gemini.generateContent", path, buildGeminiPayload(profile, call, options, schema), map[string]any{}, nil
	case "anthropic-messages":
		return "anthropic", "anthropic.messages", "/messages", buildAnthropicPayload(profile, call, options, schema), map[string]any{"anthropic-version": defaultAnthropicVersion}, nil
	default:
		return "", "", "", nil, nil, fmt.Errorf("providers: unsupported API inference type %q", profile.APIInferenceType)
	}
}

func decodedSchema(call runtime.Call) (any, error) {
	if call.CallType != "structured" {
		return nil, nil
	}
	var schema any
	decoder := json.NewDecoder(strings.NewReader(string(call.Schema)))
	decoder.UseNumber()
	if err := decoder.Decode(&schema); err != nil {
		return nil, fmt.Errorf("providers: decode normalized schema: %w", err)
	}
	return schema, nil
}

func mergedOptions(profile runtime.Profile, call runtime.Call) (map[string]any, error) {
	options := cloneMap(profile.DefaultOptions)
	for key := range runtimeOptionKeys {
		delete(options, key)
	}
	if hasReasoningOption(call.ProviderOptions) {
		return nil, errors.New("providers: portable and native reasoning options conflict")
	}
	for key, value := range call.ProviderOptions {
		if _, runtimeOnly := runtimeOptionKeys[key]; runtimeOnly {
			continue
		}
		options = mergeProviderOption(profile, options, key, value)
	}
	effort := strings.TrimSpace(call.ReasoningEffort)
	if call.Repair != nil && call.Repair.ReasoningEffort != "" {
		effort = strings.TrimSpace(call.Repair.ReasoningEffort)
	}
	if effort != "" {
		switch effort {
		case "lowest", "middle", "highest":
		default:
			return nil, fmt.Errorf("providers: reasoning effort %q is unsupported by profile %q", effort, profile.ID)
		}
		mapped, ok := profile.ReasoningEffortMap[effort]
		if !ok {
			return nil, fmt.Errorf("providers: reasoning effort %q is unsupported by profile %q", effort, profile.ID)
		}
		for key, value := range mapped {
			options = mergeProviderOption(profile, options, key, value)
		}
	}
	return options, nil
}

func hasReasoningOption(options map[string]any) bool {
	for _, key := range []string{
		"reasoning", "reasoning_effort", "thinking", "thinkingConfig", "thinkingBudget", "thinking_budget",
		"thinkingLevel", "thinking_level", "enable_thinking",
	} {
		if value, ok := options[key]; ok && value != nil {
			return true
		}
	}
	return false
}

func mergeProviderOption(profile runtime.Profile, options map[string]any, key string, value any) map[string]any {
	if profile.APIInferenceType == "gemini-generate-content" && key == "thinkingConfig" {
		if override, ok := value.(map[string]any); ok {
			if current, currentOK := options[key].(map[string]any); currentOK {
				options[key] = mergeNested(current, override)
				return options
			}
		}
	}
	options[key] = cloneJSONValue(value)
	return options
}

func repairInputs(profile runtime.Profile, call runtime.Call) (runtime.Profile, runtime.Call) {
	repair := call.Repair
	if repair.ModelID != "" {
		profile.ModelID = repair.ModelID
	}
	call.SystemPrompt = strings.TrimSpace(call.SystemPrompt + "\n\n" +
		"Repair the prior output. Return exactly one JSON object with keys repair and data. " +
		"repair must contain a non-empty explanation and a changes array; data must strictly match the target schema.")
	call.UserPrompt = fmt.Sprintf(
		"Original request:\n%s\n\nPrior output:\n%s\n\nTarget schema:\n%s\n\nRepair attempt %d of %d.",
		call.UserPrompt, repair.PreviousOutput, string(repair.TargetSchema), repair.Attempt, repair.MaxAttempts,
	)
	call.Schema = repairEnvelopeSchema(repair.TargetSchema)
	return profile, call
}

func repairEnvelopeSchema(target json.RawMessage) json.RawMessage {
	var targetValue any
	if json.Unmarshal(target, &targetValue) != nil {
		targetValue = map[string]any{}
	}
	value := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repair": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"explanation": map[string]any{"type": "string", "minLength": 1},
					"changes":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []any{"explanation", "changes"}, "additionalProperties": false,
			},
			"data": targetValue,
		},
		"required": []any{"repair", "data"}, "additionalProperties": false,
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

func buildResponsesPayload(profile runtime.Profile, call runtime.Call, options map[string]any, schema any) map[string]any {
	normalized := cloneMap(options)
	normalizeResponsesTokenOptions(normalized, profile.ResponsesTokensParam)
	if !profile.SupportsTemperature {
		delete(normalized, "temperature")
	} else if _, ok := normalized["temperature"]; !ok {
		normalized["temperature"] = defaultTemperature
	}
	payload := map[string]any{
		"model": profile.ModelID,
		"input": []any{
			map[string]any{"role": "system", "content": []any{map[string]any{"type": "input_text", "text": call.SystemPrompt}}},
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": call.UserPrompt}}},
		},
	}
	if call.SystemPrompt == "" {
		payload["input"] = payload["input"].([]any)[1:]
	}
	for key, value := range normalized {
		payload[key] = value
	}
	if call.CallType == "structured" {
		payload["text"] = map[string]any{
			"format":    map[string]any{"type": "json_schema", "name": "structured_output_schema", "strict": true, "schema": schema},
			"verbosity": "medium",
		}
	}
	return payload
}

func buildChatPayload(profile runtime.Profile, call runtime.Call, options map[string]any, schema any) map[string]any {
	normalized := cloneMap(options)
	delete(normalized, "reasoning")
	normalizeTokenOption(normalized, profile.TokensParam, "max_tokens")
	if !profile.SupportsTemperature {
		delete(normalized, "temperature")
	} else if _, ok := normalized["temperature"]; !ok {
		normalized["temperature"] = defaultTemperature
	}
	payload := map[string]any{
		"model": profile.ModelID,
		"messages": []any{
			map[string]any{"role": "system", "content": call.SystemPrompt},
			map[string]any{"role": "user", "content": call.UserPrompt},
		},
	}
	for key, value := range normalized {
		payload[key] = value
	}
	if call.CallType == "structured" {
		if strings.EqualFold(profile.Provider, "novita") {
			payload["response_format"] = map[string]any{"type": "json_object"}
		} else {
			jsonSchema := map[string]any{"name": "structured_output_schema", "strict": true, "schema": schema}
			if strings.EqualFold(profile.Provider, "groq") || strings.EqualFold(profile.Provider, "openrouter-cerebras") {
				delete(jsonSchema, "name")
			}
			if strings.EqualFold(profile.Provider, "sambaNova") {
				jsonSchema["strict"] = false
			}
			payload["response_format"] = map[string]any{"type": "json_schema", "json_schema": jsonSchema}
		}
	}
	return payload
}

func buildGeminiPayload(profile runtime.Profile, call runtime.Call, options map[string]any, schema any) map[string]any {
	config := make(map[string]any)
	if profile.SupportsTemperature {
		config["temperature"] = numericOptionDefault(options, defaultTemperature, "temperature")
	}
	copyNumericOption(config, "maxOutputTokens", options, "maxOutputTokens", "max_output_tokens", "max_tokens", "max_completion_tokens")
	copyNumericOption(config, "topP", options, "topP", "top_p")
	copyNumericOption(config, "topK", options, "topK", "top_k")
	if value, ok := firstOption(options, "stop", "stopSequences"); ok {
		if values, arrayOK := value.([]any); arrayOK && len(values) > 0 {
			config["stopSequences"] = cloneJSONValue(values)
		} else if values, stringArrayOK := value.([]string); stringArrayOK && len(values) > 0 {
			config["stopSequences"] = append([]string(nil), values...)
		}
	}
	if thinking := geminiThinking(options); len(thinking) > 0 {
		config["thinkingConfig"] = thinking
	}
	if call.CallType == "structured" {
		config["response_mime_type"] = "application/json"
		config["response_schema"] = sanitizeGeminiSchema(schema)
	}
	payload := map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": call.UserPrompt}}}},
		"generationConfig": config,
	}
	systemParts := make([]any, 0, 2)
	if call.SystemPrompt != "" {
		systemParts = append(systemParts, map[string]any{"text": call.SystemPrompt})
	}
	if call.CallType == "structured" {
		systemParts = append(systemParts, map[string]any{"text": "Return ONLY a valid JSON value that strictly conforms to response_schema. Do not include markdown code fences, explanations, or any text before or after the JSON."})
	}
	if len(systemParts) > 0 {
		payload["system_instruction"] = map[string]any{"parts": systemParts}
	}
	return payload
}

func buildAnthropicPayload(profile runtime.Profile, call runtime.Call, options map[string]any, schema any) map[string]any {
	normalized := cloneMap(options)
	maximum := positiveIntegerOption(normalized, "max_tokens", "maxTokens", "max_completion_tokens")
	if maximum == nil {
		maximum = float64(1024)
	}
	for _, key := range []string{"max_tokens", "maxTokens", "max_completion_tokens"} {
		delete(normalized, key)
	}
	if !profile.SupportsTemperature {
		delete(normalized, "temperature")
	} else if _, ok := normalized["temperature"]; !ok {
		normalized["temperature"] = defaultTemperature
	}
	payload := map[string]any{
		"model": profile.ModelID, "max_tokens": maximum,
		"messages": []any{map[string]any{"role": "user", "content": call.UserPrompt}},
	}
	for key, value := range normalized {
		payload[key] = value
	}
	if call.SystemPrompt != "" {
		payload["system"] = call.SystemPrompt
	}
	if call.CallType == "structured" {
		payload["output_config"] = map[string]any{"format": map[string]any{"type": "json_schema", "schema": anthropicSchema(schema)}}
	}
	return payload
}

func anthropicSchema(schema any) any {
	object, ok := cloneJSONValue(schema).(map[string]any)
	if !ok {
		return schema
	}
	object["$schema"] = "http://json-schema.org/draft-07/schema#"
	return object
}

func sanitizeGeminiSchema(value any) any {
	switch typed := cloneJSONValue(value).(type) {
	case map[string]any:
		for _, key := range []string{
			"$schema", "$defs", "definitions", "additionalProperties", "patternProperties", "unevaluatedProperties",
			"dependentSchemas", "dependencies", "const", "default", "examples", "minItems", "maxItems", "prefixItems", "uniqueItems",
		} {
			delete(typed, key)
		}
		if properties, ok := typed["properties"].(map[string]any); ok {
			for key, child := range properties {
				properties[key] = sanitizeGeminiSchema(child)
			}
		}
		if items, ok := typed["items"]; ok {
			if array, arrayOK := items.([]any); arrayOK {
				if len(array) == 0 {
					typed["items"] = map[string]any{}
				} else {
					typed["items"] = sanitizeGeminiSchema(array[0])
				}
			} else {
				typed["items"] = sanitizeGeminiSchema(items)
			}
		}
		return typed
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = sanitizeGeminiSchema(child)
		}
		return result
	default:
		return typed
	}
}

func normalizeTokenOption(options map[string]any, target string, sources ...string) {
	if target == "" {
		return
	}
	if _, exists := options[target]; exists {
		for _, source := range sources {
			if source != target {
				delete(options, source)
			}
		}
		return
	}
	for _, source := range sources {
		if value, ok := options[source]; ok {
			options[target] = value
			delete(options, source)
			return
		}
	}
}

func normalizeResponsesTokenOptions(options map[string]any, target string) {
	if target == "" {
		return
	}
	for _, source := range []string{"max_tokens", "max_completion_tokens"} {
		value, sourceExists := options[source]
		_, targetExists := options[target]
		if sourceExists && !targetExists {
			options[target] = value
			if source != target {
				delete(options, source)
			}
		}
	}
}

func copyNumericOption(target map[string]any, targetKey string, options map[string]any, keys ...string) {
	if value := numericOption(options, keys...); value != nil {
		target[targetKey] = value
	}
}

func numericOption(options map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := numericValue(options[key]); ok {
			return value
		}
	}
	return nil
}

func numericOptionDefault(options map[string]any, fallback any, keys ...string) any {
	if value := numericOption(options, keys...); value != nil {
		return value
	}
	return fallback
}

func positiveIntegerOption(options map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := numericValue(options[key]); ok {
			switch number := value.(type) {
			case float64:
				if number > 0 {
					return float64(int64(number))
				}
			case json.Number:
				if parsed, err := number.Float64(); err == nil && parsed > 0 {
					return float64(int64(parsed))
				}
			}
		}
	}
	return nil
}

func numericValue(value any) (any, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case int32:
		return float64(number), true
	case json.Number:
		if parsed, err := number.Float64(); err == nil {
			return parsed, true
		}
	}
	return nil, false
}

func firstOption(options map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := options[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func geminiThinking(options map[string]any) map[string]any {
	result := make(map[string]any)
	nested, _ := options["thinkingConfig"].(map[string]any)
	if value := numericOption(nested, "thinkingBudget", "thinking_budget"); value != nil {
		result["thinkingBudget"] = value
	} else if value := numericOption(options, "thinkingBudget", "thinking_budget"); value != nil {
		result["thinkingBudget"] = value
	}
	if value, ok := stringOption(nested, "thinkingLevel", "thinking_level"); ok {
		result["thinkingLevel"] = strings.ToUpper(value)
	} else if value, ok := stringOption(options, "thinkingLevel", "thinking_level"); ok {
		result["thinkingLevel"] = strings.ToUpper(value)
	}
	return result
}

func stringOption(options map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := options[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return make(map[string]any)
	}
	result := maps.Clone(input)
	for key, value := range result {
		result[key] = cloneJSONValue(value)
	}
	return result
}

func mergeNested(base, override map[string]any) map[string]any {
	result := cloneMap(base)
	for key, value := range override {
		if right, ok := value.(map[string]any); ok {
			if left, leftOK := result[key].(map[string]any); leftOK {
				result[key] = mergeNested(left, right)
				continue
			}
		}
		result[key] = cloneJSONValue(value)
	}
	return result
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneJSONValue(item)
		}
		return result
	case []string:
		return append([]string(nil), typed...)
	case http.Header:
		return typed.Clone()
	default:
		return value
	}
}
