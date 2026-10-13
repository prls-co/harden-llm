package smoke

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-034 TEST-433

import "testing"

func TestSmokeHTTPSPort(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"127.0.0.1:43210", "127.0.0.1:43210\n"} {
		port, err := parseSmokeHTTPSPort(input)
		if err != nil || port != 43210 {
			t.Fatalf("assigned port: %d %v", port, err)
		}
	}
	for _, input := range []string{"", "127.0.0.1:0", "127.0.0.1:65536", "0.0.0.0:43210", "127.0.0.1:not-a-port", "127.0.0.1:43210\n127.0.0.1:43211"} {
		if _, err := parseSmokeHTTPSPort(input); err == nil {
			t.Fatalf("invalid Docker publication accepted: %q", input)
		}
	}
}
