package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	hardenllm "github.com/prls-co/harden-llm"
)

type chatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []chatCompletionChoice `json:"choices"`
	Usage   *chatCompletionUsage   `json:"usage,omitempty"`
	Harden  map[string]any         `json:"harden,omitempty"`
}

type chatCompletionChoice struct {
	Index        int               `json:"index"`
	Message      chatMessageOutput `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type chatMessageOutput struct {
	Role      string               `json:"role"`
	Content   any                  `json:"content"`
	ToolCalls []chatToolCallOutput `json:"tool_calls,omitempty"`
}

type chatToolCallOutput struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function hardenllm.FunctionCall `json:"function"`
}

type chatCompletionUsage struct {
	PromptTokens     int64                      `json:"prompt_tokens"`
	CompletionTokens int64                      `json:"completion_tokens"`
	TotalTokens      int64                      `json:"total_tokens"`
	PromptDetails    *chatPromptTokenDetail     `json:"prompt_tokens_details,omitempty"`
	CompletionDetail *chatCompletionTokenDetail `json:"completion_tokens_details,omitempty"`
}

type chatPromptTokenDetail struct {
	CachedTokens int64 `json:"cached_tokens,omitempty"`
}

type chatCompletionTokenDetail struct {
	ReasoningTokens int64 `json:"reasoning_tokens,omitempty"`
}

func chatResponse(model string, result hardenllm.Result, created int64, includeUsage, diagnostics bool) (chatCompletionResponse, error) {
	content, calls, err := outputContent(result.Output)
	if err != nil {
		return chatCompletionResponse{}, err
	}
	message := chatMessageOutput{Role: "assistant"}
	if content != "" || len(calls) == 0 {
		message.Content = content
	}
	for _, call := range calls {
		message.ToolCalls = append(message.ToolCalls, chatToolCallOutput{
			ID: call.ID, Type: "function", Function: call.Function,
		})
	}
	finish := "stop"
	if len(calls) > 0 {
		finish = "tool_calls"
	}
	response := chatCompletionResponse{
		ID: "chatcmpl-" + result.CallID, Object: "chat.completion", Created: created, Model: model,
		Choices: []chatCompletionChoice{{Index: 0, Message: message, FinishReason: finish}},
	}
	if includeUsage {
		response.Usage = chatUsage(result.Accounting.Result.Usage)
	}
	if diagnostics {
		response.Harden = hardenExtension(result)
	}
	if err := validateHardenSize(response.Harden); err != nil {
		return chatCompletionResponse{}, err
	}
	return response, nil
}

func responsesResponse(model string, result hardenllm.Result, created int64, diagnostics bool, parallelToolCalls bool) (map[string]any, error) {
	content, calls, err := outputContent(result.Output)
	if err != nil {
		return nil, err
	}
	id := "resp_" + result.CallID
	output := make([]any, 0, 1+len(calls))
	if content != "" || len(calls) == 0 {
		output = append(output, map[string]any{
			"id": "msg_" + result.CallID, "type": "message", "status": "completed", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": content, "annotations": []any{}}},
		})
	}
	for index, call := range calls {
		itemID := call.ItemID
		if itemID == "" {
			itemID = "fc_" + result.CallID + "_" + strconv.Itoa(index)
		}
		output = append(output, map[string]any{
			"id": itemID, "type": "function_call", "status": "completed", "call_id": call.ID,
			"name": call.Function.Name, "arguments": call.Function.Arguments,
		})
	}
	response := map[string]any{
		"id": id, "object": "response", "created_at": created, "status": "completed", "model": model,
		"output": output, "output_text": content, "parallel_tool_calls": parallelToolCalls,
	}
	if usage := responsesUsage(result.Accounting.Result.Usage); usage != nil {
		response["usage"] = usage
	}
	if diagnostics {
		extension := hardenExtension(result)
		if err := validateHardenSize(extension); err != nil {
			return nil, err
		}
		response["harden"] = extension
	}
	return response, nil
}

func outputContent(value any) (string, []hardenllm.AssistantToolCall, error) {
	switch output := value.(type) {
	case string:
		return output, nil, nil
	case hardenllm.AssistantOutput:
		return output.Content, output.ToolCalls, nil
	case *hardenllm.AssistantOutput:
		if output == nil {
			return "", nil, errors.New("the provider returned no output")
		}
		return output.Content, output.ToolCalls, nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", nil, errors.New("the structured result could not be encoded")
		}
		return string(encoded), nil, nil
	}
}

func chatUsage(value hardenllm.Usage) *chatCompletionUsage {
	if value.Status != "complete" {
		return nil
	}
	usage := &chatCompletionUsage{
		PromptTokens: value.PromptTokens, CompletionTokens: value.CompletionTokens,
		TotalTokens: value.PromptTokens + value.CompletionTokens,
	}
	if value.CacheReadTokens > 0 {
		usage.PromptDetails = &chatPromptTokenDetail{CachedTokens: value.CacheReadTokens}
	}
	if value.ReasoningTokens > 0 {
		usage.CompletionDetail = &chatCompletionTokenDetail{ReasoningTokens: value.ReasoningTokens}
	}
	return usage
}

func responsesUsage(value hardenllm.Usage) map[string]any {
	if value.Status != "complete" {
		return nil
	}
	return map[string]any{
		"input_tokens": value.PromptTokens, "output_tokens": value.CompletionTokens,
		"total_tokens":          value.PromptTokens + value.CompletionTokens,
		"input_tokens_details":  map[string]any{"cached_tokens": value.CacheReadTokens},
		"output_tokens_details": map[string]any{"reasoning_tokens": value.ReasoningTokens},
	}
}

func hardenExtension(result hardenllm.Result) map[string]any {
	return map[string]any{
		"execution_id": result.CallID, "trace_id": result.TraceID,
		"selected_target": result.SelectedTarget, "generation_target": result.GenerationTarget,
		"result_source": result.ResultSource, "accounting": result.Accounting,
		"attempts": result.Attempts, "cache": result.Cache, "diagnostics": result.Diagnostics,
	}
}

func validateHardenSize(value any) error {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maximumHardenBytes {
		return fmt.Errorf("harden diagnostics exceed the %d byte limit", maximumHardenBytes)
	}
	return nil
}

func validateResponseSize(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("response could not be encoded")
	}
	if len(encoded) > maximumResponseBytes {
		return errors.New("response exceeds the 16 MiB limit")
	}
	return nil
}

func outputChunks(value string, maximum int) []string {
	if maximum < 1 {
		return nil
	}
	chunks := make([]string, 0, (len(value)+maximum-1)/maximum)
	for len(value) > maximum {
		end := maximum
		for end > 0 && end < len(value) && !utf8.RuneStart(value[end]) {
			end--
		}
		chunks = append(chunks, value[:end])
		value = value[end:]
	}
	if value != "" {
		chunks = append(chunks, value)
	}
	return chunks
}
