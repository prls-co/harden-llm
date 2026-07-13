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
}
