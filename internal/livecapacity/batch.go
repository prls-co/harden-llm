package livecapacity

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BatchRequestBody uses HLLM's supported schema subset. A single-value enum
// expresses exact nonce equality without the unsupported const keyword.
func BatchRequestBody(nonce string, long bool) json.RawMessage {
	properties := map[string]any{"nonce": map[string]any{"type": "string", "enum": []string{nonce}}}
	required := []string{"nonce"}
	prompt := "Return exactly a JSON object with nonce equal to " + nonce + ". No other text."
	if long {
		properties["items"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"x"}}}
		required = append(required, "items")
		prompt += " Include items as exactly 512 strings each equal to x."
	}
	raw, _ := json.Marshal(map[string]any{"model": "gpt-6-luna", "input": prompt, "reasoning": map[string]any{"effort": "low"}, "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "bench_nonce", "strict": true, "schema": map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}}})
	return raw
}

// ValidateBatchOutput is independent of the submitted schema: the benchmark's
// length oracle remains exact even though HLLM does not support min/maxItems.
func ValidateBatchOutput(nonce, text string, long bool) error {
	var v struct {
		Nonce string   `json:"nonce"`
		Items []string `json:"items"`
	}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return err
	}
	if v.Nonce != nonce {
		return fmt.Errorf("nonce mismatch")
	}
	if long {
		if len(v.Items) != 512 {
			return fmt.Errorf("array length mismatch")
		}
		for _, x := range v.Items {
			if x != "x" {
				return fmt.Errorf("array value mismatch")
			}
		}
	}
	return nil
}

// BoundedTextBatchRequestBody is a distinct token-load experiment. A scalar
// enum bounds the output without requiring the model to count array elements.
func BoundedTextBatchRequestBody(nonce string) json.RawMessage {
	payload := strings.TrimSpace(strings.Repeat("x ", 512))
	properties := map[string]any{
		"nonce":   map[string]any{"type": "string", "enum": []string{nonce}},
		"payload": map[string]any{"type": "string", "enum": []string{payload}},
	}
	raw, _ := json.Marshal(map[string]any{"model": "gpt-6-luna", "input": "Return the nonce " + nonce + " and the exact payload required by the schema. No other text.", "reasoning": map[string]any{"effort": "low"}, "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "bench_text", "strict": true, "schema": map[string]any{"type": "object", "properties": properties, "required": []string{"nonce", "payload"}, "additionalProperties": false}}}})
	return raw
}

func ValidateBoundedTextBatchOutput(nonce, text string) error {
	var v struct {
		Nonce   string `json:"nonce"`
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return err
	}
	if v.Nonce != nonce {
		return fmt.Errorf("nonce mismatch")
	}
	words := strings.Fields(v.Payload)
	if len(words) != 512 {
		return fmt.Errorf("word count mismatch")
	}
	for _, word := range words {
		if word != "x" {
			return fmt.Errorf("word value mismatch")
		}
	}
	return nil
}
