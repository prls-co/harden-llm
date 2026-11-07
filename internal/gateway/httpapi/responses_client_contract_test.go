package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-422 / NER migration case 502.
import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/httpclient"
)

func TestResponsesClientActualGateway(t *testing.T) {
	text := strings.Repeat("x", 128<<10)
	var calls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 2 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"temporary","type":"server_error","code":"unavailable"}}`)
			return
		}
		var req struct {
			Input []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if len(req.Input) != 4 {
			t.Errorf("input message count=%d", len(req.Input))
		} else {
			for i, role := range []string{"system", "user", "assistant", "user"} {
				if req.Input[i].Role != role {
					t.Errorf("message %d role=%q", i, req.Input[i].Role)
				}
			}
			if !strings.Contains(string(req.Input[1].Content), text) {
				t.Error("source input changed")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp-provider","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"ok\":true}"}]}],"usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}`)
	}))
	t.Cleanup(provider.Close)
	gateway := newContractProxy(t, provider)
	t.Cleanup(gateway.Close)
	client, err := httpclient.New(httpclient.Config{BaseURL: gateway.URL, APIKey: proxyTestToken, HTTPClient: gateway.Client()})
	if err != nil {
		t.Fatal(err)
	}
	var policy hardenllm.RecoveryPolicy
	if err = json.Unmarshal([]byte(`{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`), &policy); err != nil {
		t.Fatal(err)
	}
	req := httpclient.ResponsesInput{Model: "fixture-model", Input: []httpclient.ResponseMessage{{Role: "system", Content: "rules"}, {Role: "user", Content: text}, {Role: "assistant", Content: `{"ok":false}`}, {Role: "user", Content: "correct"}}, Text: httpclient.ResponseText{Format: httpclient.ResponseFormat{Type: "json_schema", Name: "result", Schema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`), Strict: true}}, Harden: httpclient.ResponseControls{Cache: hardenllm.CacheModeOff, Recovery: &policy, Diagnostics: true}}
	result, err := client.Responses(context.Background(), req)
	if err != nil {
		t.Fatalf("Responses: %T %v", err, err)
	}
	if result.OutputText != `{"ok":true}` || result.Model != "fixture-model" || calls.Load() != 1 {
		t.Fatalf("output=%q model=%q provider calls=%d", result.OutputText, result.Model, calls.Load())
	}
	var evidence struct {
		ExecutionID string            `json:"execution_id"`
		Attempts    []json.RawMessage `json:"attempts"`
	}
	if err := json.Unmarshal(result.Harden, &evidence); err != nil || evidence.ExecutionID == "" || len(evidence.Attempts) != 1 {
		t.Fatalf("missing execution evidence: %s, %v", result.Harden, err)
	}

	if err = json.Unmarshal([]byte(`{"maxAttempts":2,"retryOn":["server_error"],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`), &policy); err != nil {
		t.Fatal(err)
	}
	result, err = client.Responses(context.Background(), req)
	if err != nil {
		t.Fatalf("bounded transport retry: %T %v", err, err)
	}
	if err = json.Unmarshal(result.Harden, &evidence); err != nil || len(evidence.Attempts) != 2 || calls.Load() != 3 || result.OutputText != `{"ok":true}` {
		t.Fatalf("retry accounting: attempts=%d provider calls=%d err=%v", len(evidence.Attempts), calls.Load(), err)
	}
}
