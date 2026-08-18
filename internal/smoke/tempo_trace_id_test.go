package smoke

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-034

import "testing"

func TestNormalizeTempoTraceID(t *testing.T) {
	const want = "0123456789abcdef0123456789abcdef"
	tests := map[string]string{
		"full":         want,
		"omitted zero": want[1:],
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if got := normalizeTempoTraceID(input); got != want {
				t.Fatalf("normalizeTempoTraceID(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestNormalizeTempoTraceIDRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "xyz", "123", "0123456789abcdef0123456789abcdef0"} {
		if got := normalizeTempoTraceID(value); got != "" {
			t.Errorf("normalizeTempoTraceID(%q) = %q, want empty", value, got)
		}
	}
}
