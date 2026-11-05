// Package native contains the shared test-only native provider response writer.
package native

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/prls-co/harden-llm/internal/runtime"
)

// WriteResponseHeaders prepares the same native MIME type for early headers
// and completed output. It never writes a fabricated model event or body.
func WriteResponseHeaders(w http.ResponseWriter, r *http.Request, request map[string]any) {
	contentType := "application/json"
	if request["stream"] == true && (strings.HasSuffix(r.URL.Path, "/chat/completions") || strings.HasSuffix(r.URL.Path, "/responses") || strings.HasSuffix(r.URL.Path, "/agent")) {
		contentType = "text/event-stream"
	}
	w.Header().Set("Content-Type", contentType)
}

// WriteResponse preserves the owner fixture responses used by the actual
// provider parsers, including native SSE. Unsupported routes remain HTTP 404.
func WriteResponse(w http.ResponseWriter, r *http.Request, request map[string]any, text string, sources ...runtime.SearchSource) {
	model, _ := request["model"].(string)
	WriteResponseHeaders(w, r, request)
	switch {
	case strings.Contains(r.URL.Path, ":generateContent"):
		_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}, "finishReason": "STOP"}}, "usageMetadata": map[string]any{"promptTokenCount": 2, "candidatesTokenCount": 2, "totalTokenCount": 4}})
	case strings.HasSuffix(r.URL.Path, "/messages"):
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "model": model, "content": []any{map[string]any{"type": "text", "text": text}}, "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 2, "output_tokens": 2}})
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		body := map[string]any{"id": "chatcmpl_fixture", "object": "chat.completion", "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 2, "total_tokens": 4}}
		if request["stream"] == true {
			event, _ := json.Marshal(map[string]any{"id": "chatcmpl_fixture", "object": "chat.completion.chunk", "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": "stop"}}, "usage": body["usage"]})
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", event)
		} else {
			_ = json.NewEncoder(w).Encode(body)
		}
	case strings.HasSuffix(r.URL.Path, "/responses") || strings.HasSuffix(r.URL.Path, "/agent"):
		body := map[string]any{"id": "resp_fixture", "object": "response", "status": "completed", "model": model, "reasoning": request["reasoning"], "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": text}}}}, "output_text": text, "usage": map[string]any{"input_tokens": 2, "output_tokens": 2, "total_tokens": 4}}
		if len(sources) != 0 {
			body["output"] = append([]any{map[string]any{"type": "web_search_call", "status": "completed", "action": map[string]any{"type": "search", "sources": sources}}}, body["output"].([]any)...)
		}
		if request["stream"] == true {
			event, _ := json.Marshal(map[string]any{"type": "response.completed", "response": body})
			_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", event)
		} else {
			_ = json.NewEncoder(w).Encode(body)
		}
	default:
		http.NotFound(w, r)
	}
	_, _ = io.Copy(io.Discard, r.Body)
}
