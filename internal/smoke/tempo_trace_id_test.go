package smoke

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-034

import (
	"strings"
	"testing"
)

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

func TestNormalizeTempoTraceIDRestoresEveryOmittedZero(t *testing.T) {
	// Tempo's TraceIDToHexString removes all leading zero nibbles.
	for width := 1; width <= 32; width++ {
		input := strings.Repeat("a", width)
		want := strings.Repeat("0", 32-width) + input
		if got := normalizeTempoTraceID(input); got != want {
			t.Fatalf("width %d: got %q, want %q", width, got, want)
		}
	}
	const input = "543d371270d7bb53e93776ba94471e"
	if got := normalizeTempoTraceID(input); got != "00543d371270d7bb53e93776ba94471e" {
		t.Fatalf("nightly regression: got %q", got)
	}
}

func TestNormalizeTempoTraceIDRejectsInvalidValues(t *testing.T) {
	// Short nonzero hex strings are valid Tempo trace IDs, including "123".
	for _, value := range []string{"", "xyz", "0", strings.Repeat("0", 32), "0123456789abcdef0123456789abcdef0"} {
		if got := normalizeTempoTraceID(value); got != "" {
			t.Errorf("normalizeTempoTraceID(%q) = %q, want empty", value, got)
		}
	}
}
