package livecapacity

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-483

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeContextLoadPreservesIndependentOutputOracle(t *testing.T) {
	var inputs []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		input, _ := body["input"].(string)
		inputs = append(inputs, input)
		if !strings.Contains(input, "<synthetic_context>") || !strings.Contains(input, "fixture-0") || body["model"] != "gpt-6-luna" {
			t.Error("context replaced the original workload")
		}
		fmt.Fprint(w, `{"status":"completed","model":"gpt-6-luna","output_text":"{\"nonce\":\"wrong\"}","usage":{"input_tokens":100,"output_tokens":10}}`)
	}))
	defer s.Close()
	c := NativeCaller{URL: s.URL, Key: "synthetic", Client: s.Client(), Timeout: time.Second, ContextWords: 128}
	for range 2 {
		m := c.Call(context.Background(), "fixture", 0, 1)
		if m.Kind != "failed" || m.Code != "qualification_mismatch" {
			t.Fatal("context weakened output oracle", m)
		}
	}
	if len(inputs) != 2 || inputs[0] != inputs[1] {
		t.Fatal("context changed across identical logical calls")
	}
	x := strings.Split(strings.Split(inputs[0], "<synthetic_context>\n")[1], "\n</synthetic_context>")[0]
	if len(strings.Fields(x)) != 128 {
		t.Fatal("wrong synthetic context size")
	}
}
