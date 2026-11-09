package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	hardenllm "github.com/prls-co/harden-llm"
)

func (api *API) streamChat(writer http.ResponseWriter, request *http.Request, model string, result hardenllm.Result, created int64, includeUsage, diagnostics bool) {
	if _, err := chatResponse(model, result, created, includeUsage, diagnostics); err != nil {
		writeRequestError(writer, err, "response")
		return
	}
	content, calls, err := outputContent(result)
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

func (api *API) streamResponses(writer http.ResponseWriter, request *http.Request, final map[string]any) {
	if request.Context().Err() != nil {
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	flusher := http.NewResponseController(writer)
	sequence := 0
	base := map[string]any{"id": final["id"], "object": "response", "created_at": final["created_at"], "status": "in_progress", "model": final["model"], "output": []any{}}
	if !writeResponseEvent(writer, flusher, &sequence, "response.created", map[string]any{"response": base}) {
		return
	}
	if !writeResponseEvent(writer, flusher, &sequence, "response.in_progress", map[string]any{"response": base}) {
		return
	}
	for index, raw := range final["output"].([]any) {
		item := raw.(map[string]any)
		id := item["id"].(string)
		added := make(map[string]any, len(item))
		for key, value := range item {
			added[key] = value
		}
		added["status"] = "in_progress"
		switch item["type"] {
		case "web_search_call":
			if !writeResponseEvent(writer, flusher, &sequence, "response.output_item.added", map[string]any{"output_index": index, "item": added}) {
				return
			}
			events := []string{"in_progress"}
			status, _ := item["status"].(string)
			if status == "searching" || status == "completed" {
				events = append(events, "searching")
			}
			if status == "completed" {
				events = append(events, "completed")
			}
			for _, status := range events {
				if !writeResponseEvent(writer, flusher, &sequence, "response.web_search_call."+status, map[string]any{"output_index": index, "item_id": id}) {
					return
				}
			}
		case "message":
			added["content"] = []any{}
			if !writeResponseEvent(writer, flusher, &sequence, "response.output_item.added", map[string]any{"output_index": index, "item": added}) {
				return
			}
			for partIndex, partRaw := range item["content"].([]any) {
				part := partRaw.(map[string]any)
				text, _ := part["text"].(string)
				blank := map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
				fields := func() map[string]any {
					return map[string]any{"output_index": index, "content_index": partIndex, "item_id": id}
				}
				f := fields()
				f["part"] = blank
				if !writeResponseEvent(writer, flusher, &sequence, "response.content_part.added", f) {
					return
				}
				for _, chunk := range outputChunks(text, 64<<10) {
					f = fields()
					f["delta"] = chunk
					if !writeResponseEvent(writer, flusher, &sequence, "response.output_text.delta", f) {
						return
					}
				}
				for annotationIndex, annotation := range part["annotations"].([]any) {
					f = fields()
					f["annotation_index"] = annotationIndex
					f["annotation"] = annotation
					if !writeResponseEvent(writer, flusher, &sequence, "response.output_text.annotation.added", f) {
						return
					}
				}
				f = fields()
				f["text"] = text
				if !writeResponseEvent(writer, flusher, &sequence, "response.output_text.done", f) {
					return
				}
				f = fields()
				f["part"] = part
				if !writeResponseEvent(writer, flusher, &sequence, "response.content_part.done", f) {
					return
				}
			}
		case "function_call":
			added["arguments"] = ""
			if !writeResponseEvent(writer, flusher, &sequence, "response.output_item.added", map[string]any{"output_index": index, "item": added}) {
				return
			}
			args, _ := item["arguments"].(string)
			for _, chunk := range outputChunks(args, 64<<10) {
				if !writeResponseEvent(writer, flusher, &sequence, "response.function_call_arguments.delta", map[string]any{"output_index": index, "item_id": id, "delta": chunk}) {
					return
				}
			}
			if !writeResponseEvent(writer, flusher, &sequence, "response.function_call_arguments.done", map[string]any{"output_index": index, "item_id": id, "arguments": args}) {
				return
			}
		default:
			return
		}
		if !writeResponseEvent(writer, flusher, &sequence, "response.output_item.done", map[string]any{"output_index": index, "item": item}) {
			return
		}
	}
	writeResponseEvent(writer, flusher, &sequence, "response.completed", map[string]any{"response": final})
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
