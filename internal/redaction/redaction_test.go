package redaction

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-019

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSharedRedactorHandlesEmbeddedAndMalformedInputs(t *testing.T) {
	t.Parallel()
	redactor := New("literal-secret")
	input := "request literal-secret failed at https://user:pass@example.com/v1?api_key=query-secret&safe=ok with Bearer bearer-secret"
	output := redactor.Text(input)
	for _, secret := range []string{"literal-secret", "user:pass", "query-secret", "bearer-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("text redaction leaked %q: %s", secret, output)
		}
	}
	if !strings.Contains(output, "safe=ok") {
		t.Fatalf("safe URL query value was removed: %s", output)
	}

	malformed := []byte(`{"authorization":"Bearer hidden"} trailing-secret`)
	redacted, err := New("trailing-secret").JSON(malformed)
	if err != nil || !json.Valid(redacted) || strings.Contains(string(redacted), "hidden") || strings.Contains(string(redacted), "trailing-secret") {
		t.Fatalf("malformed JSON redaction mismatch: %s %v", redacted, err)
	}

	content, err := New().JSON([]byte(`{
		"systemPrompt":"do not expose", "response":"private output",
		"rawProviderEnvelope":{"safe":false}, "baseURL":"https://example.test/private",
		"responseStatusCode":200, "safe":"visible"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var contentValue map[string]any
	if err := json.Unmarshal(content, &contentValue); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"systemPrompt", "response", "rawProviderEnvelope", "baseURL"} {
		if contentValue[key] != Replacement {
			t.Errorf("content field %s = %#v, want redacted", key, contentValue[key])
		}
	}
	if contentValue["responseStatusCode"] != float64(200) || contentValue["safe"] != "visible" {
		t.Errorf("safe fields changed: %#v", contentValue)
	}
}
