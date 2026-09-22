package capacity

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const providerRequestBodyLimit = 1 << 20

// ScriptedProvider is a local-only upstream fixture for capacity tests. It
// accepts only the test Responses route, bounds request bodies, and never
// echoes requests or credentials into responses or logs.
type ScriptedProvider struct {
	mu     sync.RWMutex
	script string
	delay  time.Duration
	calls  atomic.Int64
}

func NewScriptedProvider(script string, delay time.Duration) (*ScriptedProvider, error) {
	if !oneOf(script, "success", "rate-limit-once", "rate-limit", "malformed-json", "truncated-stream", "slow-success") {
		return nil, errors.New("unsupported local provider script")
	}
	if delay < 0 || delay > 10*time.Second || (script == "slow-success") != (delay > 0) {
		return nil, errors.New("local provider delay must be between zero and ten seconds and required only for slow-success")
	}
	return &ScriptedProvider{script: script, delay: delay}, nil
}

func (provider *ScriptedProvider) RequestCount() int64 { return provider.calls.Load() }

// Configure changes the local script only between scenario runs and resets its
// measured dispatch counter. Active handlers retain the script they started
// with; callers must drain a population before reconfiguration.
func (provider *ScriptedProvider) Configure(script string, delay time.Duration) error {
	if provider == nil {
		return errors.New("local provider is required")
	}
	if !oneOf(script, "success", "rate-limit-once", "rate-limit", "malformed-json", "truncated-stream", "slow-success") {
		return errors.New("unsupported local provider script")
	}
	if delay < 0 || delay > 10*time.Second || (script == "slow-success") != (delay > 0) {
		return errors.New("local provider delay must be between zero and ten seconds and required only for slow-success")
	}
	provider.mu.Lock()
	provider.script, provider.delay = script, delay
	provider.calls.Store(0)
	provider.mu.Unlock()
	return nil
}

func (provider *ScriptedProvider) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	provider.mu.RLock()
	script, delay := provider.script, provider.delay
	provider.mu.RUnlock()
	if request.Method != http.MethodPost || request.URL.Path != "/v1/responses" {
		http.NotFound(writer, request)
		return
	}
	defer request.Body.Close()
	request.Body = http.MaxBytesReader(writer, request.Body, providerRequestBodyLimit)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(writer, `{"error":{"code":"invalid_request"}}`, http.StatusBadRequest)
		return
	}
	var payload struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&payload); err != nil || payload.Model == "" {
		http.Error(writer, `{"error":{"code":"invalid_request"}}`, http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		http.Error(writer, `{"error":{"code":"invalid_request"}}`, http.StatusBadRequest)
		return
	}
	// The real gateway profile-save flow sends a harmless text probe. It must
	// succeed for every script and is not a measured generation dispatch.
	if bytes.Contains(payload.Input, []byte("Reply with OK.")) {
		writeScriptedResponse(writer, `{"ok":true}`)
		return
	}
	call := provider.calls.Add(1)

	if script == "rate-limit" || script == "rate-limit-once" && call == 1 {
		writer.Header().Set("Retry-After", "0")
		http.Error(writer, `{"error":{"code":"rate_limit"}}`, http.StatusTooManyRequests)
		return
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-request.Context().Done():
			return
		case <-timer.C:
		}
	}
	if script == "truncated-stream" {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
		return
	}
	output := `{"ok":true}`
	if script == "malformed-json" && call == 1 {
		output = "{not-json"
	}
	writeScriptedResponse(writer, output)
}

func writeScriptedResponse(writer http.ResponseWriter, output string) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"id":          "resp_capacity_fixture",
		"object":      "response",
		"status":      "completed",
		"output_text": output,
		"usage": map[string]int64{
			"input_tokens":  10,
			"output_tokens": 5,
			"total_tokens":  15,
		},
	})
}
