package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-454

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/prls-co/harden-llm/internal/testfixture/native"
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
	native.WriteResponse(w, r, request, text)
}
