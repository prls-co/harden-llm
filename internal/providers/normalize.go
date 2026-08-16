package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
	contractschema "github.com/prls-co/harden-llm/internal/schema"
)

func normalizeResponse(prepared preparedRequest, body []byte) (runtime.ProviderResult, error) {
	response, err := decodeJSONObject(body)
	if err != nil {
		return runtime.ProviderResult{}, &retry.ProviderError{Err: errors.New("provider returned malformed JSON"), Code: "MALFORMED_RESPONSE", Parse: true}
	}
	output, text, refusal, empty := extractProviderOutput(prepared.protocol, response)
	if refusal {
		return runtime.ProviderResult{}, &retry.ProviderError{Err: errors.New("provider refusal or content filter"), Code: "PROVIDER_REFUSAL", Refusal: true}
	}
	if empty {
		return runtime.ProviderResult{}, &retry.ProviderError{
			Err: errors.New("provider returned an empty or null response"), Code: "empty_response",
			RawResponse: string(body), Empty: true,
		}
	}
	usage := normalizeUsage(prepared.protocol, response)
	cost := normalizeCost(response, usage, prepared.pricing)
	partial := runtime.ProviderResult{Usage: usage, Cost: cost}
	if prepared.callType == "structured" && output == nil {
		parsed, _, parseErr := contractschema.ParseProviderOutput(text, prepared.protocol)
		if parseErr != nil {
			return partial, &retry.ProviderError{
				Err: errors.New("provider returned malformed structured output"), Code: "STRUCTURED_PARSE",
				Parse: true, RawResponse: text,
			}
		}
		output = parsed
	} else if prepared.callType == "structured" {
		output = contractschema.NormalizeNumericStringsDeep(output)
	} else if output == nil {
		output = text
	}
	envelope, err := json.Marshal(map[string]any{
		"schemaVersion": rawEnvelopeVersion,
		"provider":      prepared.provider,
		"protocol":      prepared.protocol,
		"response":      response,
	})
	if err != nil {
		return runtime.ProviderResult{}, &retry.ProviderError{Err: errors.New("provider response normalization failed"), Code: "NORMALIZATION", Parse: true}
	}
	return runtime.ProviderResult{Output: output, Usage: usage, Cost: cost, RawProviderEnvelope: envelope}, nil
}

func decodeJSONObject(input []byte) (map[string]any, error) {
	value, err := decodeJSONValue(input)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("response is not an object")
	}
	return object, nil
}

func decodeJSONValue(input []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("trailing JSON value")
		}
		return nil, err
	}
	return value, nil
}

func extractProviderOutput(protocol string, response map[string]any) (output any, text string, refusal bool, empty bool) {
	switch protocol {
	case "openai.responses":
		if status, _ := response["status"].(string); status == "incomplete" {
			if details := objectValue(response["incomplete_details"]); stringValue(details["reason"]) == "content_filter" {
				return nil, "", true, false
			}
		}
		if value := stringValue(response["output_text"]); strings.TrimSpace(value) != "" {
			return nil, value, false, false
		}
		for _, message := range arrayValue(response["output"]) {
			for _, partValue := range arrayValue(objectValue(message)["content"]) {
				part := objectValue(partValue)
				if stringValue(part["type"]) == "refusal" || strings.TrimSpace(stringValue(part["refusal"])) != "" {
					return nil, "", true, false
				}
				if parsed := part["parsed"]; parsed != nil {
					return parsed, "", false, false
				}
				if parsed := part["json"]; parsed != nil {
					return parsed, "", false, false
				}
				for _, key := range []string{"text", "content"} {
					if value := stringValue(part[key]); strings.TrimSpace(value) != "" {
						return nil, value, false, false
					}
				}
			}
		}
		return nil, "", false, true
	case "openai.chat.completions", "openai-compatible.chat.completions":
		choices := arrayValue(response["choices"])
		if len(choices) == 0 {
			return nil, "", false, true
		}
		choice := objectValue(choices[0])
		if stringValue(choice["finish_reason"]) == "content_filter" {
			return nil, "", true, false
		}
		message := objectValue(choice["message"])
		if strings.TrimSpace(stringValue(message["refusal"])) != "" {
			return nil, "", true, false
		}
		if value := strings.TrimSpace(stringValue(message["content"])); value != "" {
			return nil, value, false, false
		}
		return nil, "", false, true
	case "google.gemini.generateContent":
		if feedback := objectValue(response["promptFeedback"]); stringValue(feedback["blockReason"]) != "" {
			return nil, "", true, false
		}
		candidates := arrayValue(response["candidates"])
		if len(candidates) == 0 {
			return nil, "", false, true
		}
		candidate := objectValue(candidates[0])
		switch stringValue(candidate["finishReason"]) {
		case "SAFETY", "BLOCKED", "OTHER", "PROHIBITED_CONTENT", "SPII":
			return nil, "", true, false
		}
		parts := arrayValue(objectValue(candidate["content"])["parts"])
		if len(parts) == 0 {
			return nil, "", false, true
		}
		text := stringValue(objectValue(parts[0])["text"])
		if strings.TrimSpace(text) == "" {
			return nil, "", false, true
		}
		return nil, text, false, false
	case "anthropic.messages":
		if stringValue(response["stop_reason"]) == "refusal" {
			return nil, "", true, false
		}
		var builder strings.Builder
		for _, part := range arrayValue(response["content"]) {
			object := objectValue(part)
			if stringValue(object["type"]) == "text" {
				builder.WriteString(stringValue(object["text"]))
			}
		}
		if strings.TrimSpace(builder.String()) == "" {
			return nil, "", false, true
		}
		return nil, builder.String(), false, false
	default:
		return nil, "", false, true
	}
}

func normalizeUsage(protocol string, response map[string]any) runtime.Usage {
	switch protocol {
	case "openai.responses", "openai.chat.completions", "openai-compatible.chat.completions":
		usage := objectValue(response["usage"])
		totalInput := integerValue(firstValue(usage, "prompt_tokens", "input_tokens"))
		details := objectValue(firstValue(usage, "prompt_tokens_details", "input_tokens_details"))
		cacheReadValue, cacheReadPresent := firstPresent(details, "cached_tokens")
		if !cacheReadPresent {
			cacheReadValue, _ = firstPresent(usage, "cached_tokens", "cache_read_input_tokens")
		}
		cacheRead := integerValue(cacheReadValue)
		cacheCreationValue, cacheCreationPresent := firstPresent(details, "cache_write_tokens", "cache_creation_tokens")
		if !cacheCreationPresent {
			cacheCreationValue, _ = firstPresent(usage, "cache_creation_input_tokens")
		}
		cacheCreation := integerValue(cacheCreationValue)
		totalOutput := integerValue(firstValue(usage, "completion_tokens", "output_tokens"))
		outputDetails := objectValue(firstValue(usage, "completion_tokens_details", "output_tokens_details"))
		reasoningValue, reasoningPresent := firstPresent(outputDetails, "reasoning_tokens")
		if !reasoningPresent {
			reasoningValue, _ = firstPresent(usage, "reasoning_tokens")
		}
		reasoning := integerValue(reasoningValue)
		return completedUsage(totalInput-cacheRead-cacheCreation, cacheRead, cacheCreation, totalOutput-reasoning, reasoning)
	case "google.gemini.generateContent":
		usage := objectValue(response["usageMetadata"])
		totalInput := integerValue(usage["promptTokenCount"])
		cacheRead := integerValue(usage["cachedContentTokenCount"])
		return completedUsage(totalInput-cacheRead, cacheRead, 0, integerValue(usage["candidatesTokenCount"]), integerValue(usage["thoughtsTokenCount"]))
	case "anthropic.messages":
		usage := objectValue(response["usage"])
		return completedUsage(
			integerValue(usage["input_tokens"]), integerValue(usage["cache_read_input_tokens"]),
			integerValue(usage["cache_creation_input_tokens"]), integerValue(usage["output_tokens"]), 0,
		)
	default:
		return runtime.Usage{}
	}
}

func completedUsage(input, cacheRead, cacheCreation, output, reasoning int64) runtime.Usage {
	input = max(0, input)
	cacheRead = max(0, cacheRead)
	cacheCreation = max(0, cacheCreation)
	output = max(0, output)
	reasoning = max(0, reasoning)
	return runtime.Usage{
		InputTokens: input, CacheReadTokens: cacheRead, CacheCreationTokens: cacheCreation,
		OutputTokens: output, ReasoningTokens: reasoning,
		TotalTokens: input + cacheRead + cacheCreation + output + reasoning,
	}
}

func normalizeCost(response map[string]any, usage runtime.Usage, pricing runtime.Pricing) runtime.Cost {
	usageObject := objectValue(response["usage"])
	for _, value := range []any{usageObject["cost"], usageObject["total_cost"], response["cost"], response["total_cost"]} {
		if reported, ok := nonnegativeFloat(value); ok {
			return runtime.Cost{TotalUSD: reported, Known: true, Source: "reported"}
		}
	}
	total := float64(0)
	groups := []struct {
		count int64
		rate  *float64
	}{
		{usage.InputTokens, pricing.Input},
		{usage.CacheReadTokens, pricing.CacheRead},
		{usage.CacheCreationTokens, pricing.CacheCreation},
		{usage.OutputTokens, pricing.Output},
		{usage.ReasoningTokens, pricing.Reasoning},
	}
	for _, group := range groups {
		if group.count == 0 {
			continue
		}
		if group.rate == nil || math.IsNaN(*group.rate) || math.IsInf(*group.rate, 0) || *group.rate < 0 {
			return runtime.Cost{Known: false, Source: "unknown"}
		}
		total += float64(group.count) * *group.rate
	}
	if allPricingMissing(pricing) {
		return runtime.Cost{Known: false, Source: "unknown"}
	}
	return runtime.Cost{TotalUSD: total, Known: true, Source: "profile"}
}

func allPricingMissing(pricing runtime.Pricing) bool {
	return pricing.Input == nil && pricing.CacheRead == nil && pricing.CacheCreation == nil && pricing.Output == nil && pricing.Reasoning == nil
}

func objectValue(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return map[string]any{}
}

func arrayValue(value any) []any {
	if array, ok := value.([]any); ok {
		return array
	}
	return nil
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func firstValue(object map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := object[key]; ok && value != nil {
			return value
		}
	}
	return nil
}

func firstPresent(object map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := object[key]; ok && value != nil {
			return value, true
		}
	}
	return nil, false
}

func integerValue(value any) int64 {
	switch number := value.(type) {
	case json.Number:
		if parsed, err := number.Int64(); err == nil && parsed > 0 {
			return parsed
		}
		if parsed, err := number.Float64(); err == nil && parsed > 0 {
			return int64(parsed)
		}
	case float64:
		if number > 0 && !math.IsNaN(number) && !math.IsInf(number, 0) {
			return int64(number)
		}
	case int64:
		if number > 0 {
			return number
		}
	case int:
		if number > 0 {
			return int64(number)
		}
	}
	return 0
}

func nonnegativeFloat(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case float64:
		number = typed
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	default:
		return 0, false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0, false
	}
	return number, true
}

func providerResponseSummary(prepared preparedRequest, body []byte) string {
	return fmt.Sprintf("%s response (%d bytes)", prepared.protocol, len(body))
}
