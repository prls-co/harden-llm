package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
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
	Role        string               `json:"role"`
	Content     any                  `json:"content"`
	ToolCalls   []chatToolCallOutput `json:"tool_calls,omitempty"`
	Annotations []any                `json:"annotations,omitempty"`
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
	content, calls, err := outputContent(result)
	if err != nil {
		return chatCompletionResponse{}, err
	}
	message := chatMessageOutput{Role: "assistant"}
	message.Annotations = annotations(result, content, chatCitation)
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

func outputItemID(id, prefix, callID string, index int) string {
	if id != "" {
		return id
	}
	return prefix + "_" + callID + "_" + strconv.Itoa(index)
}

// Cached search items retain the producer's item IDs while the response keeps
// the current call ID for request tracking.
func projectionCallID(result hardenllm.Result) string {
	if result.Search != nil && result.Cache.Served && result.ProjectionCallID != "" {
		return result.ProjectionCallID
	}
	return result.CallID
}

func searchCallItems(result hardenllm.Result, includeSources bool) []any {
	items := []any{}
	if result.Search == nil {
		return items
	}
	for index, call := range result.Search.Calls {
		item := map[string]any{"id": outputItemID(call.ID, "ws", projectionCallID(result), index), "type": "web_search_call", "status": call.Status}
		if call.Action != nil {
			a := call.Action
			action := map[string]any{"type": a.Type}
			if a.Query != "" {
				action["query"] = a.Query
			}
			if a.URL != "" {
				action["url"] = a.URL
			}
			if a.Pattern != "" {
				action["pattern"] = a.Pattern
			}
			if includeSources {
				sources := make([]any, 0, len(call.Sources))
				for _, s := range call.Sources {
					sources = append(sources, map[string]any{"type": "url", "url": s.URL})
				}
				action["sources"] = sources
			}
			item["action"] = action
		}
		items = append(items, item)
	}
	return items
}

func annotations(result hardenllm.Result, text string, shape func(hardenllm.SearchCitation) any) []any {
	output := []any{}
	if result.Search == nil || len(result.Search.Citations) == 0 {
		return output
	}
	sum := sha256.Sum256([]byte(text))
	if hex.EncodeToString(sum[:]) != result.Search.CitationTextSHA256 {
		return output
	}
	for _, citation := range result.Search.Citations {
		output = append(output, shape(citation))
	}
	return output
}

func responsesCitation(c hardenllm.SearchCitation) any {
	return map[string]any{"type": "url_citation", "start_index": c.StartIndex, "end_index": c.EndIndex, "url": c.URL, "title": c.Title}
}

func chatCitation(c hardenllm.SearchCitation) any {
	return map[string]any{"type": "url_citation", "url_citation": map[string]any{"start_index": c.StartIndex, "end_index": c.EndIndex, "url": c.URL, "title": c.Title}}
}

func responsesResponse(model string, result hardenllm.Result, created int64, diagnostics bool, parallelToolCalls bool, includeSources bool) (map[string]any, error) {
	content, calls, err := outputContent(result)
	if err != nil {
		return nil, err
	}
	id := "resp_" + result.CallID
	output := searchCallItems(result, includeSources)
	if content != "" || len(calls) == 0 {
		output = append(output, map[string]any{
			"id": "msg_" + projectionCallID(result), "type": "message", "status": "completed", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": content, "annotations": annotations(result, content, responsesCitation)}},
		})
	}
	for index, call := range calls {
		itemID := outputItemID(call.ItemID, "fc", result.CallID, index)
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

func outputContent(result hardenllm.Result) (string, []hardenllm.AssistantToolCall, error) {
	var content string
	var calls []hardenllm.AssistantToolCall
	switch output := result.Output.(type) {
	case string:
		content = output
	case hardenllm.AssistantOutput:
		content, calls = output.Content, output.ToolCalls
	case *hardenllm.AssistantOutput:
		if output == nil {
			return "", nil, errors.New("the provider returned no output")
		}
		content, calls = output.Content, output.ToolCalls
	default:
		// Native object outputs have no original text. Text-based structured
		// responses use their exact original string instead of reserialization.
		if result.AssistantText == "" {
			encoded, err := json.Marshal(result.Output)
			if err != nil {
				return "", nil, errors.New("the structured result could not be encoded")
			}
			content = string(encoded)
		}
	}
	if result.AssistantText != "" {
		content = result.AssistantText
	}
	return content, calls, nil
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
	chunks := make([]string, 0)
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
