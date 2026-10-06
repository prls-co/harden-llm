package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	hardenllm "github.com/prls-co/harden-llm"
)

func (api *API) streamChat(writer http.ResponseWriter, request *http.Request, model string, result hardenllm.Result, created int64, includeUsage, diagnostics bool) {
	if _, err := chatResponse(model, result, created, includeUsage, diagnostics); err != nil {
		writeRequestError(writer, err, "response")
		return
	}
	content, calls, err := outputContent(result.Output)
	if err != nil {
		writeRequestError(writer, err, "response")
		return
	}
	if request.Context().Err() != nil {
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	flusher := http.NewResponseController(writer)
	id, object := "chatcmpl-"+result.CallID, "chat.completion.chunk"
	writeChunk := func(chunk map[string]any) bool {
		chunk["id"], chunk["object"], chunk["created"], chunk["model"] = id, object, created, model
		return writeSSEData(writer, flusher, chunk)
	}
	if !writeChunk(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}}}) {
		return
	}
	for _, chunk := range outputChunks(content, 64<<10) {
		if !writeChunk(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": chunk}, "finish_reason": nil}}}) {
			return
		}
	}
	for index, call := range calls {
		arguments := outputChunks(call.Function.Arguments, 64<<10)
		if len(arguments) == 0 {
			arguments = []string{""}
		}
		for partIndex, value := range arguments {
			toolCall := map[string]any{"index": index, "function": map[string]any{"arguments": value}}
			function := toolCall["function"].(map[string]any)
			if partIndex == 0 {
				toolCall["id"], toolCall["type"] = call.ID, "function"
				function["name"] = call.Function.Name
			}
			if !writeChunk(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{toolCall}}, "finish_reason": nil}}}) {
				return
			}
		}
	}
	finish := "stop"
	if len(calls) > 0 {
		finish = "tool_calls"
	}
	final := map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}}
	if diagnostics {
		final["harden"] = hardenExtension(result)
	}
	if !writeChunk(final) {
		return
	}
	if includeUsage {
		if usage := chatUsage(result.Accounting.Result.Usage); usage != nil {
			if !writeChunk(map[string]any{"choices": []any{}, "usage": usage}) {
				return
			}
		}
	}
	_, _ = fmt.Fprint(writer, "data: [DONE]\n\n")
	_ = flusher.Flush()
}

func (api *API) streamResponses(writer http.ResponseWriter, request *http.Request, model string, result hardenllm.Result, created int64, diagnostics, parallelToolCalls bool) {
	final, err := responsesResponse(model, result, created, diagnostics, parallelToolCalls)
	if err != nil {
		writeRequestError(writer, err, "response")
		return
	}
	content, calls, err := outputContent(result.Output)
	if err != nil {
		writeRequestError(writer, err, "response")
		return
	}
	if request.Context().Err() != nil {
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	flusher := http.NewResponseController(writer)
	sequence := 0
	responseID := "resp_" + result.CallID
	base := map[string]any{"id": responseID, "object": "response", "created_at": created, "status": "in_progress", "model": model, "output": []any{}}
	if !writeResponseEvent(writer, flusher, &sequence, "response.created", map[string]any{"response": base}) {
		return
	}
	if !writeResponseEvent(writer, flusher, &sequence, "response.in_progress", map[string]any{"response": base}) {
		return
	}
	outputIndex := 0
	if content != "" || len(calls) == 0 {
		itemID := "msg_" + result.CallID
		item := map[string]any{"id": itemID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}
		if !writeResponseEvent(writer, flusher, &sequence, "response.output_item.added", map[string]any{"output_index": outputIndex, "item": item}) {
			return
		}
		part := map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
		if !writeResponseEvent(writer, flusher, &sequence, "response.content_part.added", map[string]any{"output_index": outputIndex, "content_index": 0, "part": part}) {
			return
		}
		for _, chunk := range outputChunks(content, 64<<10) {
			if !writeResponseEvent(writer, flusher, &sequence, "response.output_text.delta", map[string]any{"output_index": outputIndex, "content_index": 0, "delta": chunk}) {
				return
			}
		}
		part["text"] = content
		if !writeResponseEvent(writer, flusher, &sequence, "response.output_text.done", map[string]any{"output_index": outputIndex, "content_index": 0, "text": content}) {
			return
		}
		if !writeResponseEvent(writer, flusher, &sequence, "response.content_part.done", map[string]any{"output_index": outputIndex, "content_index": 0, "part": part}) {
			return
		}
		item["status"], item["content"] = "completed", []any{part}
		if !writeResponseEvent(writer, flusher, &sequence, "response.output_item.done", map[string]any{"output_index": outputIndex, "item": item}) {
			return
		}
		outputIndex++
	}
	for _, call := range calls {
		itemID := call.ItemID
		if itemID == "" {
			itemID = "fc_" + result.CallID + "_" + strconv.Itoa(outputIndex)
		}
		item := map[string]any{
			"id": itemID, "type": "function_call", "status": "in_progress", "call_id": call.ID,
			"name": call.Function.Name, "arguments": "",
		}
		if !writeResponseEvent(writer, flusher, &sequence, "response.output_item.added", map[string]any{"output_index": outputIndex, "item": item}) {
			return
		}
		for _, chunk := range outputChunks(call.Function.Arguments, 64<<10) {
			if !writeResponseEvent(writer, flusher, &sequence, "response.function_call_arguments.delta", map[string]any{"output_index": outputIndex, "item_id": itemID, "delta": chunk}) {
				return
			}
		}
		if !writeResponseEvent(writer, flusher, &sequence, "response.function_call_arguments.done", map[string]any{"output_index": outputIndex, "item_id": itemID, "arguments": call.Function.Arguments}) {
			return
		}
		item["status"], item["arguments"] = "completed", call.Function.Arguments
		if !writeResponseEvent(writer, flusher, &sequence, "response.output_item.done", map[string]any{"output_index": outputIndex, "item": item}) {
			return
		}
		outputIndex++
	}
	final["sequence_number"] = sequence
	final["type"] = "response.completed"
	if !writeSSEEvent(writer, flusher, "response.completed", final) {
		return
	}
}

func writeResponseEvent(writer http.ResponseWriter, flusher *http.ResponseController, sequence *int, name string, fields map[string]any) bool {
	fields["type"] = name
	fields["sequence_number"] = *sequence
	(*sequence)++
	return writeSSEEvent(writer, flusher, name, fields)
}

func writeSSEEvent(writer http.ResponseWriter, flusher *http.ResponseController, name string, value any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", name, encoded); err != nil {
		return false
	}
	return flusher.Flush() == nil
}

func writeSSEData(writer http.ResponseWriter, flusher *http.ResponseController, value any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(writer, "data: %s\n\n", encoded); err != nil {
		return false
	}
	return flusher.Flush() == nil
}
