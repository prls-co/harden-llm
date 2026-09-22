// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-276

package capacity

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCapacityProviderRejectsInconsistentDelay(t *testing.T) {
	for _, testCase := range []struct {
		script string
		delay  time.Duration
	}{
		{script: "success", delay: time.Millisecond},
		{script: "slow-success"},
	} {
		if _, err := NewScriptedProvider(testCase.script, testCase.delay); err == nil {
			t.Errorf("NewScriptedProvider(%q, %s) accepted inconsistent delay", testCase.script, testCase.delay)
		}
	}
}

func TestCapacityDriverUsesOnlyLocalScriptedProviderBehavior(t *testing.T) {
	provider, err := NewScriptedProvider("rate-limit-once", 0)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(provider)
	defer server.Close()
	if err := ValidateEndpoint(server.URL, nil); err != nil {
		t.Fatalf("httptest loopback endpoint rejected: %v", err)
	}

	client := server.Client()
	call := func() *http.Response {
		t.Helper()
		request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/responses", bytes.NewBufferString(`{"model":"synthetic-local-model"}`))
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	first := call()
	_, _ = io.Copy(io.Discard, first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("first scripted response status = %d, want 429", first.StatusCode)
	}
	second := call()
	_, _ = io.Copy(io.Discard, second.Body)
	second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second scripted response status = %d, want 200", second.StatusCode)
	}
	if provider.RequestCount() != 2 {
		t.Fatalf("scripted provider counted %d requests, want 2", provider.RequestCount())
	}

	malformed, err := NewScriptedProvider("malformed-json", 0)
	if err != nil {
		t.Fatal(err)
	}
	malformedServer := httptest.NewTLSServer(malformed)
	defer malformedServer.Close()
	requestMalformed := func() string {
		t.Helper()
		request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, malformedServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"synthetic-local-model"}`))
		if err != nil {
			t.Fatal(err)
		}
		response, err := malformedServer.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body struct {
			OutputText string `json:"output_text"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body.OutputText
	}
	if first, second := requestMalformed(), requestMalformed(); first != "{not-json" || second != `{"ok":true}` || malformed.RequestCount() != 2 {
		t.Fatalf("JSON repair script responses/count = %q / %q / %d", first, second, malformed.RequestCount())
	}

	slow, err := NewScriptedProvider("slow-success", 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	slowServer := httptest.NewTLSServer(slow)
	defer slowServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, slowServer.URL+"/v1/responses", bytes.NewBufferString(`{"model":"synthetic-local-model"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = slowServer.Client().Do(request)
	if err == nil {
		t.Fatal("slow local provider ignored request cancellation")
	}
}
