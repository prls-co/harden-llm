package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

func (api *API) models(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	for key := range query {
		if key != "upstream" || len(query[key]) != 1 {
			writeError(writer, http.StatusBadRequest, "invalid_request_error", "unsupported_query", "Only one upstream query parameter is supported.", key)
			return
		}
	}
	ctx, cancel := context.WithTimeout(request.Context(), api.maxRunDuration)
	defer cancel()
	models, err := api.client.Models(ctx, query.Get("upstream"))
	if err != nil {
		api.writeCallError(writer, err, hardenllm.Result{}, false)
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		entry := map[string]any{"id": model.ID, "object": "model", "created": int64(0), "owned_by": model.OwnedBy}
		if model.Label != "" {
			entry["name"] = model.Label
		}
		data = append(data, entry)
	}
	writeJSON(writer, http.StatusOK, map[string]any{"object": "list", "data": data}, maximumResponseBytes)
}

func (api *API) chatCompletions(writer http.ResponseWriter, request *http.Request) {
	var input chatRequest
	if !api.decodeRequest(writer, request, &input) {
		return
	}
	if input.N != nil && *input.N != 1 {
		writeError(writer, http.StatusBadRequest, "invalid_request_error", "unsupported_value", "Only n=1 is supported.", "n")
		return
	}
	if strings.TrimSpace(input.Model) == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request_error", "invalid_request", "model is required.", "model")
		return
	}
	if input.MaxTokens != nil && input.MaxCompletionTokens != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request_error", "conflicting_fields", "max_tokens and max_completion_tokens cannot both be set.", "max_tokens")
		return
	}
	if !validatePositiveInt(writer, "max_tokens", input.MaxTokens) ||
		!validatePositiveInt(writer, "max_completion_tokens", input.MaxCompletionTokens) ||
		!validateNumberRange(writer, "temperature", input.Temperature, 0, 2) ||
		!validateNumberRange(writer, "top_p", input.TopP, 0, 1) ||
		!validateNumberRange(writer, "frequency_penalty", input.FrequencyPenalty, -2, 2) ||
		!validateNumberRange(writer, "presence_penalty", input.PresencePenalty, -2, 2) {
		return
	}
	messages, err := validateTextMessages(input.Messages)
	if err != nil {
		writeRequestError(writer, err, "messages")
		return
	}
	tools, _, err := decodeTools(input.Tools, false)
	if err != nil {
		writeRequestError(writer, err, "tools")
		return
	}
	choice, err := decodeToolChoice(input.ToolChoice, false)
	if err != nil {
		writeRequestError(writer, err, "tool_choice")
		return
	}
	callType, schema, err := chatOutputContract(input.ResponseFormat)
	if err != nil {
		writeRequestError(writer, err, "response_format")
		return
	}
	connection, cache, timeout, recovery, diagnostics, err := parseHarden(input.Harden)
	if err != nil {
		writeRequestError(writer, err, "harden")
		return
	}
	options := make(map[string]any)
	putValue(options, "max_tokens", input.MaxTokens)
	putValue(options, "max_completion_tokens", input.MaxCompletionTokens)
	putValue(options, "temperature", input.Temperature)
	putValue(options, "top_p", input.TopP)
	putValue(options, "frequency_penalty", input.FrequencyPenalty)
	putValue(options, "presence_penalty", input.PresencePenalty)
	putValue(options, "seed", input.Seed)
	if len(input.Stop) > 0 {
		value, err := decodeStopSequences(input.Stop)
		if err != nil {
			writeRequestError(writer, errors.New("stop must be a string or an array of strings"), "stop")
			return
		}
		options["stop"] = value
	}
	if callType == hardenllm.CallTypeStructured {
		if len(tools) > 0 {
			writeRequestError(writer, errors.New("structured output and function tools cannot be combined"), "tools")
			return
		}
	}
	result, callErr := api.execute(request.Context(), connection, cache, timeout, recovery, hardenllm.Request{
		ModelID: input.Model, Messages: messages, CallType: callType, Schema: schema, Tools: tools,
		ToolChoice: choice, ReasoningEffort: hardenllm.ReasoningEffort(input.ReasoningEffort),
		ProviderOptions: options,
	})
	if callErr != nil {
		api.writeCallError(writer, callErr, result, diagnostics)
		return
	}
	created := time.Now().Unix()
	includeUsage := !input.Stream || input.StreamOptions != nil && input.StreamOptions.IncludeUsage
	response, err := chatResponse(input.Model, result, created, includeUsage, diagnostics)
	if err != nil {
		writeRequestError(writer, err, "response")
		return
	}
	if input.Stream {
		if err := validateResponseSize(response); err != nil {
			writeRequestError(writer, err, "response")
			return
		}
		api.streamChat(writer, request, input.Model, result, created, input.StreamOptions != nil && input.StreamOptions.IncludeUsage, diagnostics)
		return
	}
	writeJSON(writer, http.StatusOK, response, maximumResponseBytes)
}

func (api *API) responses(writer http.ResponseWriter, request *http.Request) {
	var input responsesRequest
	if !api.decodeRequest(writer, request, &input) {
		return
	}
	if input.PreviousResponseID != "" || input.Background != nil && *input.Background {
		writeError(writer, http.StatusBadRequest, "invalid_request_error", "unsupported_field", "Server-side response state is not supported by this stateless API.", "previous_response_id")
		return
	}
	if len(input.Include) > 0 {
		writeError(writer, http.StatusBadRequest, "invalid_request_error", "unsupported_field", "The include field is not supported by this stateless hardening response contract.", "include")
		return
	}
	if strings.TrimSpace(input.Model) == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request_error", "invalid_request", "model is required.", "model")
		return
	}
	if input.N != nil && *input.N != 1 {
		writeError(writer, http.StatusBadRequest, "invalid_request_error", "unsupported_value", "Only n=1 is supported.", "n")
		return
	}
	if !validatePositiveInt(writer, "max_output_tokens", input.MaxOutputTokens) ||
		!validateNumberRange(writer, "temperature", input.Temperature, 0, 2) ||
		!validateNumberRange(writer, "top_p", input.TopP, 0, 1) {
		return
	}
	if input.Truncation != "" && input.Truncation != "auto" && input.Truncation != "disabled" {
		writeError(writer, http.StatusBadRequest, "invalid_request_error", "unsupported_value", "truncation must be auto or disabled.", "truncation")
		return
	}
	messages, err := decodeResponsesInput(input.Input, input.Instructions)
	if err != nil {
		writeRequestError(writer, err, "input")
		return
	}
	tools, webSearch, err := decodeTools(input.Tools, true)
	if err != nil {
		writeRequestError(writer, err, "tools")
		return
	}
	choice, err := decodeToolChoice(input.ToolChoice, true)
	if err != nil {
		writeRequestError(writer, err, "tool_choice")
		return
	}
	callType, schema, err := responsesOutputContract(input.Text)
	if err != nil {
		writeRequestError(writer, err, "text")
		return
	}
	connection, cache, timeout, recovery, diagnostics, err := parseHarden(input.Harden)
	if err != nil {
		writeRequestError(writer, err, "harden")
		return
	}
	if webSearch && len(tools) > 0 {
		writeRequestError(writer, errors.New("web search cannot be combined with function tools"), "tools")
		return
	}
	if webSearch && len(input.ToolChoice) > 0 {
		writeRequestError(writer, errors.New("web_search tool selection uses the configured native search behavior"), "tool_choice")
		return
	}
	if callType == hardenllm.CallTypeStructured && len(tools) > 0 {
		writeRequestError(writer, errors.New("structured output and function tools cannot be combined"), "tools")
		return
	}
	options := make(map[string]any)
	putValue(options, "max_output_tokens", input.MaxOutputTokens)
	putValue(options, "temperature", input.Temperature)
	putValue(options, "top_p", input.TopP)
	putValue(options, "truncation", input.Truncation)
	putValue(options, "parallel_tool_calls", input.ParallelToolCalls)
	reasoningEffort, err := responsesReasoningEffort(input.Reasoning)
	if err != nil {
		writeRequestError(writer, err, "reasoning")
		return
	}
	result, callErr := api.execute(request.Context(), connection, cache, timeout, recovery, hardenllm.Request{
		ModelID: input.Model, Messages: messages, CallType: callType, Schema: schema, Tools: tools,
		ToolChoice: choice, ReasoningEffort: hardenllm.ReasoningEffort(reasoningEffort), WebSearch: webSearch,
		ProviderOptions: options,
	})
	if callErr != nil {
		api.writeCallError(writer, callErr, result, diagnostics)
		return
	}
	created := time.Now().Unix()
	parallelToolCalls := true
	if input.ParallelToolCalls != nil {
		parallelToolCalls = *input.ParallelToolCalls
	}
	response, err := responsesResponse(input.Model, result, created, diagnostics, parallelToolCalls)
	if err != nil {
		writeRequestError(writer, err, "response")
		return
	}
	if input.Stream {
		if err := validateResponseSize(response); err != nil {
			writeRequestError(writer, err, "response")
			return
		}
		api.streamResponses(writer, request, input.Model, result, created, diagnostics, parallelToolCalls)
		return
	}
	writeJSON(writer, http.StatusOK, response, maximumResponseBytes)
}

func validatePositiveInt(writer http.ResponseWriter, field string, value *int) bool {
	if value == nil || *value > 0 {
		return true
	}
	writeError(writer, http.StatusBadRequest, "invalid_request_error", "invalid_value", field+" must be a positive integer.", field)
	return false
}

func validateNumberRange(writer http.ResponseWriter, field string, value *float64, minimum, maximum float64) bool {
	if value == nil || *value >= minimum && *value <= maximum {
		return true
	}
	writeError(writer, http.StatusBadRequest, "invalid_request_error", "invalid_value", fmt.Sprintf("%s must be between %g and %g.", field, minimum, maximum), field)
	return false
}

func (api *API) execute(parent context.Context, connection string, cache hardenllm.CacheMode, timeoutMS int64, recovery hardenllm.RecoveryPolicy, input hardenllm.Request) (hardenllm.Result, error) {
	if cache != hardenllm.CacheModeOff {
		return hardenllm.Result{}, &hardenllm.ValidationError{Field: "harden.cache", Message: "is unavailable because no Go cache is configured"}
	}
	if timeoutMS == 0 {
		timeoutMS = api.maxRunDuration.Milliseconds()
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout > api.maxRunDuration {
		timeout = api.maxRunDuration
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	input.ConnectionID, input.CacheMode, input.RecoveryPolicy = connection, cache, recovery
	return api.client.Call(ctx, input)
}

func (api *API) writeCallError(writer http.ResponseWriter, err error, result hardenllm.Result, diagnostics bool) {
	status := http.StatusBadGateway
	typeName, code, message := "server_error", "upstream_error", "The upstream request could not be completed."
	var parameter any
	var providerError *hardenllm.ProviderError
	var validation *hardenllm.ValidationError
	switch {
	case errors.Is(err, context.Canceled):
		status, code, message = http.StatusRequestTimeout, "request_canceled", "The request was canceled."
	case errors.Is(err, context.DeadlineExceeded):
		status, code, message = http.StatusGatewayTimeout, "request_timeout", "The request exceeded the configured time limit."
	case errors.As(err, &validation):
		status, typeName, code, message = http.StatusBadRequest, "invalid_request_error", "invalid_request", "The request failed validation."
		if validation.Field != "" {
			parameter = validation.Field
		}
	case errors.As(err, &providerError):
		code = safeCode(providerError.Code, "upstream_error")
		if providerError.Status == http.StatusTooManyRequests {
			status, typeName, code = http.StatusTooManyRequests, "rate_limit_error", safeCode(providerError.Code, "rate_limit_exceeded")
		} else if providerError.Status >= 400 && providerError.Status < 500 {
			status, typeName, code = http.StatusBadRequest, "invalid_request_error", safeCode(providerError.Code, "upstream_rejected_request")
		}
		if providerError.Category == "timeout" {
			status, typeName, code = http.StatusGatewayTimeout, "server_error", "upstream_timeout"
		}
	}
	response := errorResponse{Error: openAIError{Message: message, Type: typeName, Code: code, Param: parameter}}
	if diagnostics {
		response.Harden = hardenExtension(result)
	}
	writeJSON(writer, status, response, maximumResponseBytes)
}

func writeRequestError(writer http.ResponseWriter, err error, parameter string) {
	writeError(writer, http.StatusBadRequest, "invalid_request_error", "invalid_request", err.Error(), parameter)
}

func safeCode(value, fallback string) string {
	if value == "" || len(value) > 128 {
		return fallback
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' && char != '-' {
			return fallback
		}
	}
	return value
}

func putValue(target map[string]any, key string, value any) {
	switch item := value.(type) {
	case string:
		if item != "" {
			target[key] = item
		}
	case []string:
		if item != nil {
			target[key] = item
		}
	case *int:
		if item != nil {
			target[key] = *item
		}
	case *int64:
		if item != nil {
			target[key] = *item
		}
	case *float64:
		if item != nil {
			target[key] = *item
		}
	case *bool:
		if item != nil {
			target[key] = *item
		}
	}
}

func decodeJSONValue(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func decodeStopSequences(encoded []byte) (any, error) {
	var value any
	if err := decodeJSONValue(encoded, &value); err != nil {
		return nil, err
	}
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []any:
		if len(typed) > 4 {
			return nil, errors.New("stop accepts at most four sequences")
		}
		sequences := make([]string, len(typed))
		for index, item := range typed {
			sequence, ok := item.(string)
			if !ok {
				return nil, errors.New("stop sequences must all be strings")
			}
			sequences[index] = sequence
		}
		return sequences, nil
	default:
		return nil, errors.New("stop must be a string or an array of strings")
	}
}

func chatOutputContract(encoded json.RawMessage) (hardenllm.CallType, json.RawMessage, error) {
	if len(encoded) == 0 {
		return hardenllm.CallTypeText, nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var format chatResponseFormat
	if err := decoder.Decode(&format); err != nil {
		return "", nil, errors.New("response_format has an unsupported shape")
	}
	switch format.Type {
	case "json_object":
		return hardenllm.CallTypeStructured, json.RawMessage(`{"type":"object"}`), nil
	case "json_schema":
		var schema chatJSONSchemaFormat
		strictDecoder := json.NewDecoder(bytes.NewReader(format.JSONSchema))
		strictDecoder.DisallowUnknownFields()
		if err := strictDecoder.Decode(&schema); err != nil || schema.Name == "" || len(schema.Schema) == 0 {
			return "", nil, errors.New("json_schema requires name and schema")
		}
		return hardenllm.CallTypeStructured, schema.Schema, nil
	default:
		return "", nil, errors.New("response_format type must be json_schema or json_object")
	}
}

func responsesOutputContract(encoded json.RawMessage) (hardenllm.CallType, json.RawMessage, error) {
	if len(encoded) == 0 {
		return hardenllm.CallTypeText, nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var value responsesTextFormat
	if err := decoder.Decode(&value); err != nil || len(value.Format) == 0 {
		return "", nil, errors.New("text requires a supported format")
	}
	var format responsesSchemaFormat
	strictDecoder := json.NewDecoder(bytes.NewReader(value.Format))
	strictDecoder.DisallowUnknownFields()
	if err := strictDecoder.Decode(&format); err != nil {
		return "", nil, errors.New("text.format has an unsupported shape")
	}
	switch format.Type {
	case "json_object":
		return hardenllm.CallTypeStructured, json.RawMessage(`{"type":"object"}`), nil
	case "json_schema":
		if format.Name == "" || len(format.Schema) == 0 {
			return "", nil, errors.New("json_schema requires name and schema")
		}
		return hardenllm.CallTypeStructured, format.Schema, nil
	default:
		return "", nil, errors.New("text.format type must be json_schema or json_object")
	}
}

func responsesReasoningEffort(encoded json.RawMessage) (string, error) {
	if len(encoded) == 0 || bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		return "", nil
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var value struct {
		Effort string `json:"effort"`
	}
	if err := decoder.Decode(&value); err != nil || value.Effort == "" {
		return "", errors.New("reasoning requires a supported effort")
	}
	return value.Effort, nil
}
