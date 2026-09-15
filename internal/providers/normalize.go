package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
	contractschema "github.com/prls-co/harden-llm/internal/schema"
)

func normalizeResponse(prepared preparedRequest, body []byte) (runtime.ProviderResult, error) {
	response, err := decodeJSONObject(body)
	if err != nil {
		return runtime.ProviderResult{}, &retry.ProviderError{Err: errors.New("provider returned malformed JSON"), Code: "MALFORMED_RESPONSE", Category: retry.CategoryOther}
	}
	return normalizeDecodedResponse(prepared, response)
}

func normalizeDecodedResponse(prepared preparedRequest, response map[string]any) (runtime.ProviderResult, error) {
	projection := response
	if prepared.searchMode == "native" && prepared.protocol == "openai.responses" {
		// Search may emit a commentary message before the final answer. Never
		// return that preamble as a successful searched answer.
		projection = cloneMap(response)
		projection["output"] = finalResponseOutput(response)
		delete(projection, "output_text")
	}
	usage, usageErr := normalizeUsage(prepared.protocol, response)
	cost, costErr := normalizeCost(response, usage, prepared.pricing)
	partial := runtime.ProviderResult{Accounting: accounting.Ledger{Usage: usage, Cost: cost}}
	partial.RawProviderEnvelope, _ = rawProviderEnvelope(prepared, response)
	if usageErr != nil {
		return partial, accountingProviderError(usageErr)
	}
	if costErr != nil {
		return partial, accountingProviderError(costErr)
	}
	if completionErr := validateCompletion(prepared.protocol, response); completionErr != nil {
		return partial, completionErr
	}
	if prepared.searchMode == "native" && prepared.protocol == "anthropic.messages" {
		for _, item := range arrayValue(response["content"]) {
			part := objectValue(item)
			if part["type"] == "web_search_tool_result" && objectValue(part["content"])["type"] == "web_search_tool_result_error" {
				return partial, &retry.ProviderError{Err: errors.New("native web search tool failed"), Code: "WEB_SEARCH_TOOL_ERROR", Category: retry.CategoryOther}
			}
		}
	}
	output, text, refusal, empty := extractProviderOutput(prepared.protocol, projection)
	if refusal {
		return partial, &retry.ProviderError{Err: errors.New("provider refusal or content filter"), Code: "PROVIDER_REFUSAL", Category: retry.CategoryRefusal}
	}
	if empty {
		return partial, &retry.ProviderError{
			Err: errors.New("provider returned an empty or null response"), Code: "empty_response",
			RawResponse: string(mustJSON(response)), Category: retry.CategoryEmpty,
		}
	}
	if prepared.callType == "structured" && output == nil {
		parsed, _, parseErr := contractschema.ParseProviderOutput(text)
		if parseErr != nil {
			return partial, &retry.ProviderError{
				Err: errors.New("provider returned malformed structured output"), Code: "STRUCTURED_PARSE",
				Category: retry.CategoryParse, RawResponse: text,
			}
		}
		output = parsed
	} else if output == nil {
		output = text
	}
	envelope, err := rawProviderEnvelope(prepared, response)
	if err != nil {
		return partial, &retry.ProviderError{Err: errors.New("provider response normalization failed"), Code: "NORMALIZATION", Category: retry.CategoryOther}
	}
	return runtime.ProviderResult{
		Search: normalizeSearch(prepared, response),
		Output: output, Accounting: accounting.Ledger{Usage: usage, Cost: cost}, RawProviderEnvelope: envelope,
	}, nil
}

func rawProviderEnvelope(prepared preparedRequest, response map[string]any) ([]byte, error) {
	return json.Marshal(map[string]any{
		"schemaVersion": rawEnvelopeVersion,
		"provider":      prepared.provider,
		"protocol":      prepared.protocol,
		"response":      response,
	})
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func accountingProviderError(err error) error {
	if err == nil {
		return nil
	}
	return &retry.ProviderError{Err: errors.New("provider accounting is invalid"), Code: "ACCOUNTING_INVALID", Category: retry.CategoryOther}
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

func validateCompletion(protocol string, response map[string]any) error {
	other := func(code string) error {
		return &retry.ProviderError{Err: errors.New("provider response did not report a supported completion"), Code: code, Category: retry.CategoryOther}
	}
	switch protocol {
	case "openai.responses":
		status, ok := response["status"].(string)
		if !ok || strings.TrimSpace(status) == "" {
			return other("COMPLETION_REQUIRED")
		}
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "completed":
			return nil
		case "failed":
			return normalizeResponsesFailure(response)
		case "incomplete":
			details := objectValue(response["incomplete_details"])
			if strings.EqualFold(stringValue(details["reason"]), "content_filter") {
				return &retry.ProviderError{Err: errors.New("provider refusal or content filter"), Code: "PROVIDER_REFUSAL", Category: retry.CategoryRefusal}
			}
			return other("COMPLETION_INCOMPLETE")
		default:
			return other("COMPLETION_UNSUPPORTED")
		}
	case "openai.chat.completions", "openai-compatible.chat.completions":
		choices := arrayValue(response["choices"])
		if len(choices) == 0 {
			return other("COMPLETION_REQUIRED")
		}
		finish, ok := objectValue(choices[0])["finish_reason"].(string)
		if !ok || strings.TrimSpace(finish) == "" {
			return other("COMPLETION_REQUIRED")
		}
		switch strings.ToLower(strings.TrimSpace(finish)) {
		case "stop":
			return nil
		case "content_filter":
			return &retry.ProviderError{Err: errors.New("provider refusal or content filter"), Code: "PROVIDER_REFUSAL", Category: retry.CategoryRefusal}
		case "length":
			return other("COMPLETION_LIMIT")
		default:
			return other("COMPLETION_UNSUPPORTED")
		}
	case "google.gemini.generateContent":
		if feedback := objectValue(response["promptFeedback"]); strings.TrimSpace(stringValue(feedback["blockReason"])) != "" {
			return &retry.ProviderError{Err: errors.New("provider refusal or content filter"), Code: "PROVIDER_REFUSAL", Category: retry.CategoryRefusal}
		}
		candidates := arrayValue(response["candidates"])
		if len(candidates) == 0 {
			return other("COMPLETION_REQUIRED")
		}
		candidate := objectValue(candidates[0])
		finish, ok := candidate["finishReason"].(string)
		if !ok || strings.TrimSpace(finish) == "" {
			return other("COMPLETION_REQUIRED")
		}
		switch strings.ToUpper(strings.TrimSpace(finish)) {
		case "STOP":
			return nil
		case "SAFETY", "BLOCKED", "PROHIBITED_CONTENT", "SPII":
			return &retry.ProviderError{Err: errors.New("provider refusal or content filter"), Code: "PROVIDER_REFUSAL", Category: retry.CategoryRefusal}
		case "MAX_TOKENS":
			return other("COMPLETION_LIMIT")
		default:
			return other("COMPLETION_UNSUPPORTED")
		}
	case "anthropic.messages":
		finish, ok := response["stop_reason"].(string)
		if !ok || strings.TrimSpace(finish) == "" {
			return other("COMPLETION_REQUIRED")
		}
		switch strings.ToLower(strings.TrimSpace(finish)) {
		case "end_turn", "stop_sequence":
			return nil
		case "refusal":
			return &retry.ProviderError{Err: errors.New("provider refusal or content filter"), Code: "PROVIDER_REFUSAL", Category: retry.CategoryRefusal}
		case "max_tokens":
			return other("COMPLETION_LIMIT")
		default:
			return other("COMPLETION_UNSUPPORTED")
		}
	default:
		return other("COMPLETION_UNSUPPORTED")
	}
}

func normalizeResponsesFailure(response map[string]any) error {
	errorObject := objectValue(response["error"])
	if len(errorObject) == 0 {
		errorObject = objectValue(objectValue(response["response"])["error"])
	}
	code := boundedCode(stringValue(errorObject["code"]))
	typeName := boundedCode(stringValue(errorObject["type"]))
	category := retry.CategoryOther
	switch strings.ToLower(code) {
	case "rate_limit_exceeded", "rate_limit":
		category = retry.CategoryRateLimit
	case "server_error", "server_is_overloaded", "service_unavailable":
		category = retry.CategoryServer
	case "provider_retry":
		category = retry.CategoryProvider
	case "refusal", "content_filter":
		category = retry.CategoryRefusal
	}
	return &retry.ProviderError{Err: errors.New("provider response reported failure"), Code: code, Type: typeName, Category: category}
}

func normalizeUsage(protocol string, response map[string]any) (runtime.Usage, error) {
	switch protocol {
	case "openai.responses", "openai.chat.completions", "openai-compatible.chat.completions":
		return normalizeOpenAIUsage(response)
	case "google.gemini.generateContent":
		return normalizeGeminiUsage(response)
	case "anthropic.messages":
		return normalizeAnthropicUsage(response)
	default:
		return accounting.UnavailableUsage(), nil
	}
}

func normalizeOpenAIUsage(response map[string]any) (runtime.Usage, error) {
	usage, present, err := usageObject(response, "usage")
	if err != nil || !present {
		return accounting.UnavailableUsage(), err
	}
	details := optionalObject(usage, "prompt_tokens_details", "input_tokens_details")
	cacheReadValue, cacheReadPresent := firstPresent(details, "cached_tokens")
	if !cacheReadPresent {
		cacheReadValue, cacheReadPresent = firstPresent(usage, "cached_tokens", "cache_read_input_tokens")
	}
	cacheRead, err := tokenField(cacheReadValue, cacheReadPresent)
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	cacheCreationValue, cacheCreationPresent := firstPresent(details, "cache_write_tokens", "cache_creation_tokens")
	if !cacheCreationPresent {
		cacheCreationValue, cacheCreationPresent = firstPresent(usage, "cache_creation_input_tokens")
	}
	cacheCreation, err := tokenField(cacheCreationValue, cacheCreationPresent)
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	inputValue, inputPresent := firstPresent(usage, "prompt_tokens", "input_tokens")
	totalInput, err := tokenField(inputValue, inputPresent)
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	outputDetails := optionalObject(usage, "completion_tokens_details", "output_tokens_details")
	reasoningValue, reasoningPresent := firstPresent(outputDetails, "reasoning_tokens")
	if !reasoningPresent {
		reasoningValue, reasoningPresent = firstPresent(usage, "reasoning_tokens")
	}
	reasoning, err := tokenField(reasoningValue, reasoningPresent)
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	outputValue, outputPresent := firstPresent(usage, "completion_tokens", "output_tokens")
	totalOutput, err := tokenField(outputValue, outputPresent)
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	return usageFromTotals(totalInput, inputPresent, cacheRead, cacheReadPresent, cacheCreation, cacheCreationPresent, totalOutput, outputPresent, reasoning, reasoningPresent, true, true)
}

func normalizeGeminiUsage(response map[string]any) (runtime.Usage, error) {
	usage, present, err := usageObject(response, "usageMetadata")
	if err != nil || !present {
		return accounting.UnavailableUsage(), err
	}
	input, inputPresent, err := tokenFieldFromObject(usage, "promptTokenCount")
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	cacheRead, cacheReadPresent, err := tokenFieldFromObject(usage, "cachedContentTokenCount")
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	output, outputPresent, err := tokenFieldFromObject(usage, "candidatesTokenCount")
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	reasoning, reasoningPresent, err := tokenFieldFromObject(usage, "thoughtsTokenCount")
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	return usageFromTotals(input, inputPresent, cacheRead, cacheReadPresent, 0, false, output, outputPresent, reasoning, reasoningPresent, false, true)
}

func normalizeAnthropicUsage(response map[string]any) (runtime.Usage, error) {
	usage, present, err := usageObject(response, "usage")
	if err != nil || !present {
		return accounting.UnavailableUsage(), err
	}
	input, inputPresent, err := tokenFieldFromObject(usage, "input_tokens")
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	cacheRead, cacheReadPresent, err := tokenFieldFromObject(usage, "cache_read_input_tokens")
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	cacheCreation, cacheCreationPresent, err := tokenFieldFromObject(usage, "cache_creation_input_tokens")
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	output, outputPresent, err := tokenFieldFromObject(usage, "output_tokens")
	if err != nil {
		return accounting.UnavailableUsage(), err
	}
	return usageFromTotals(input, inputPresent, cacheRead, cacheReadPresent, cacheCreation, cacheCreationPresent, output, outputPresent, 0, false, false, false)
}

func usageObject(response map[string]any, key string) (map[string]any, bool, error) {
	value, exists := response[key]
	if !exists || value == nil {
		return nil, false, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("usage is not an object")
	}
	if len(object) == 0 {
		return nil, false, nil
	}
	return object, true, nil
}

func optionalObject(object map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := object[key]; ok && value != nil {
			if nested, ok := value.(map[string]any); ok {
				return nested
			}
		}
	}
	return nil
}

func tokenFieldFromObject(object map[string]any, key string) (int64, bool, error) {
	value, present := firstPresent(object, key)
	parsed, err := tokenField(value, present)
	return parsed, present, err
}

func tokenField(value any, present bool) (int64, error) {
	if !present || value == nil {
		return 0, nil
	}
	return parseToken(value)
}

func parseToken(value any) (int64, error) {
	var parsed int64
	switch typed := value.(type) {
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			parsed = integer
		} else {
			floating, floatErr := strconv.ParseFloat(typed.String(), 64)
			if floatErr != nil || math.IsNaN(floating) || math.IsInf(floating, 0) || floating < 0 || math.Trunc(floating) != floating || floating >= float64(math.MaxInt64) {
				return 0, errors.New("token count is not a nonnegative integer")
			}
			parsed = int64(floating)
		}
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed < 0 || math.Trunc(typed) != typed || typed >= float64(math.MaxInt64) {
			return 0, errors.New("token count is not a nonnegative integer")
		}
		parsed = int64(typed)
	case int:
		if typed < 0 {
			return 0, errors.New("token count is not a nonnegative integer")
		}
		parsed = int64(typed)
	case int64:
		if typed < 0 {
			return 0, errors.New("token count is not a nonnegative integer")
		}
		parsed = typed
	default:
		return 0, errors.New("token count is not a nonnegative integer")
	}
	if parsed < 0 {
		return 0, errors.New("token count is not a nonnegative integer")
	}
	return parsed, nil
}

func usageFromTotals(input int64, inputPresent bool, cacheRead int64, cacheReadPresent bool, cacheCreation int64, cacheCreationPresent bool, output int64, outputPresent bool, reasoning int64, reasoningPresent bool, outputIncludesReasoning bool, inputIncludesCaches bool) (runtime.Usage, error) {
	if inputPresent && inputIncludesCaches {
		if cacheRead > input || cacheCreation > input-cacheRead {
			return accounting.UnavailableUsage(), errors.New("cache token components exceed input tokens")
		}
		input -= cacheRead + cacheCreation
	}
	if outputPresent && outputIncludesReasoning {
		if reasoning > output {
			return accounting.UnavailableUsage(), errors.New("reasoning token component exceeds output tokens")
		}
		output -= reasoning
	}
	known := inputPresent || outputPresent || cacheReadPresent || cacheCreationPresent || reasoningPresent
	complete := inputPresent && outputPresent
	if !known {
		return accounting.UnavailableUsage(), nil
	}
	status := accounting.UsagePartial
	if complete {
		status = accounting.UsageComplete
	}
	usage := runtime.Usage{InputTokens: input, CacheReadTokens: cacheRead, CacheCreationTokens: cacheCreation, OutputTokens: output, ReasoningTokens: reasoning, Status: status}
	if err := usage.Validate(); err != nil {
		return accounting.UnavailableUsage(), err
	}
	return usage, nil
}

func normalizeCost(response map[string]any, usage runtime.Usage, pricing runtime.Pricing) (runtime.Cost, error) {
	usageObject := objectValue(response["usage"])
	for _, value := range []any{usageObject["cost"], usageObject["total_cost"], response["cost"], response["total_cost"]} {
		if value == nil {
			continue
		}
		reported, err := parseCost(value)
		if err != nil {
			return accounting.UnavailableCost(), err
		}
		return accounting.ExactCost(reported, "reported"), nil
	}
	if usage.Status == accounting.UsageUnavailable {
		return accounting.UnavailableCost(), nil
	}
	cost, err := accounting.ResolveCost(usage, pricing, nil)
	if err != nil {
		return accounting.UnavailableCost(), err
	}
	if usage.Status == accounting.UsagePartial {
		if cost.Status == accounting.CostExact && cost.KnownObservations > 0 {
			cost.Status = accounting.CostPartial
			cost.KnownObservations = 1
			cost.UnknownObservations = 1
		} else if cost.Status == accounting.CostExact {
			cost = accounting.UnknownCost("partial_usage")
		}
	}
	return cost, cost.Validate()
}

func parseCost(value any) (float64, error) {
	parsed, ok := nonnegativeFloat(value)
	if !ok {
		return 0, errors.New("reported cost is invalid")
	}
	return parsed, nil
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
