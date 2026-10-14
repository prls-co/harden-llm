package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-454

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const presetFixtureText = `{"joke":"A deterministic joke.","explanation":"A deterministic explanation."}`

// Native responses exercise the production executor and parsers, rather than
// replacing Prepare/Execute or accepting requests without response validation.
func presetNativeResponse(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/models") && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"id": "fixture-model", "object": "model", "owned_by": "fixture"}}})
		return
	}
	var request map[string]any
	decoder := json.NewDecoder(r.Body)
	if r.Method != http.MethodPost || decoder.Decode(&request) != nil || request == nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid native fixture request", http.StatusBadRequest)
		return
	}
	model, _ := request["model"].(string)
	text := presetFixtureText
	encoded, _ := json.Marshal(request)
	if strings.Contains(model, "fixture-") || strings.Contains(string(encoded), "fixture-force-recovery") {
		if model != "fixture-rerun-repair-escalation" {
			text = "not-json"
		}
	}
	writePresetNativeResponse(w, r, request, text)
}

func writePresetNativeResponse(w http.ResponseWriter, r *http.Request, request map[string]any, text string) {
	model, _ := request["model"].(string)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.Contains(r.URL.Path, ":generateContent"):
		_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}, "finishReason": "STOP"}}, "usageMetadata": map[string]any{"promptTokenCount": 2, "candidatesTokenCount": 2, "totalTokenCount": 4}})
	case strings.HasSuffix(r.URL.Path, "/messages"):
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "model": model, "content": []any{map[string]any{"type": "text", "text": text}}, "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 2, "output_tokens": 2}})
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		body := map[string]any{"id": "chatcmpl_fixture", "object": "chat.completion", "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 2, "total_tokens": 4}}
		if request["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			event, _ := json.Marshal(map[string]any{"id": "chatcmpl_fixture", "object": "chat.completion.chunk", "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": "stop"}}, "usage": body["usage"]})
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", event)
		} else {
			_ = json.NewEncoder(w).Encode(body)
		}
	case strings.HasSuffix(r.URL.Path, "/responses") || strings.HasSuffix(r.URL.Path, "/agent"):
		body := map[string]any{"id": "resp_fixture", "object": "response", "status": "completed", "model": model, "reasoning": request["reasoning"], "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": text}}}}, "output_text": text, "usage": map[string]any{"input_tokens": 2, "output_tokens": 2, "total_tokens": 4}}
		if request["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
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
