// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-276

package capacity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCapacityDriverGatewayHTTPTransportParsesTerminalDiagnostics(t *testing.T) {
	token := "synthetic-capacity-token-never-production-001"
	requestBody := ""
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/run" {
			t.Errorf("gateway request route = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Error("gateway did not receive configured synthetic bearer")
		}
		if request.Header.Get("Accept") != "text/event-stream" {
			t.Error("gateway client did not request SSE progress")
		}
		body, _ := io.ReadAll(request.Body)
		requestBody = string(body)
		var submitted struct {
			Origin         RequestOrigin  `json:"origin"`
			CacheMode      string         `json:"cacheMode"`
			RecoveryPolicy map[string]any `json:"recoveryPolicy"`
		}
		if err := json.Unmarshal(body, &submitted); err != nil {
			t.Errorf("decode gateway request: %v", err)
		}
		writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(writer, "event: run.started\ndata: {\"type\":\"run.started\",\"data\":{}}\n\n")
		terminal := map[string]any{"result": map[string]any{
			"status": "succeeded", "output": map[string]any{"ok": true}, "callId": "call-synthetic-1",
			"runId": "run-synthetic-1", "traceId": "trace-synthetic-1", "origin": submitted.Origin,
			"attempts":   []any{map[string]any{"stage": "original.generate", "providerUsed": true, "target": map[string]any{"modelId": "synthetic-model"}}},
			"cache":      map[string]any{"served": false},
			"accounting": map[string]any{"provider": map[string]any{"usage": map[string]any{"inputTokens": 10, "cacheReadTokens": 2, "cacheCreationTokens": 0, "outputTokens": 5, "status": "complete"}}},
		}}
		encoded, err := json.Marshal(map[string]any{"type": "run.completed", "data": terminal})
		if err != nil {
			t.Errorf("encode terminal event: %v", err)
		}
		_, _ = fmt.Fprintf(writer, "event: run.completed\ndata: %s\n\n", encoded)
	}))
	defer server.Close()
	transport, err := NewGatewayHTTPTransport(GatewayHTTPTransportConfig{
		Endpoint: server.URL + "/api/v1/run", BearerToken: token, ProfileID: "synthetic-profile",
		ScenarioID: "valid-case", TestRunID: "run-synthetic-1", SourceRevision: "abcdef0123", Client: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.Send(context.Background(), Request{ID: 7, Population: "measurement", ScheduledAt: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.StreamTerminal != "run.completed" || response.ExecutionStatus != "succeeded" || !response.JSONValid {
		t.Fatalf("gateway stream result = %#v", response)
	}
	if !response.FirstEventKnown || response.EventCount != 2 || response.ReceivedBytes == 0 {
		t.Fatalf("gateway stream diagnostics = %#v", response)
	}
	if response.CallID != "call-synthetic-1" || response.RunID != "run-synthetic-1" || response.TraceID != "trace-synthetic-1" || response.Origin.JobID != "measurement-request-7" {
		t.Fatalf("gateway correlation = call:%q run:%q trace:%q origin:%#v", response.CallID, response.RunID, response.TraceID, response.Origin)
	}
	if len(response.ProviderCalls) != 1 || response.ProviderCalls[0].Stage != "original.generate" || response.ProviderCalls[0].Model != "synthetic-model" || !response.ProviderCalls[0].TokenUsageKnown {
		t.Fatalf("provider stage usage = %#v", response.ProviderCalls)
	}
	call := response.ProviderCalls[0]
	if call.InputTokens != 12 || call.CachedInputTokens != 2 || call.OutputTokens != 5 {
		t.Fatalf("provider usage = %#v", call)
	}
	if strings.Contains(requestBody, token) || !strings.Contains(requestBody, `"testId":"TEST-277"`) || !strings.Contains(requestBody, `"testRunId":"run-synthetic-1"`) || !strings.Contains(requestBody, `"jobId":"measurement-request-7"`) {
		t.Fatalf("request body leaked secret or omitted synthetic origin: %s", requestBody)
	}
}

func TestCapacityDriverGatewayHTTPTransportUsesDeclaredCacheAndRecoveryMode(t *testing.T) {
	token := "synthetic-capacity-token-never-production-002"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			CacheMode      string         `json:"cacheMode"`
			RecoveryPolicy map[string]any `json:"recoveryPolicy"`
			UserPrompt     string         `json:"userPrompt"`
			Origin         RequestOrigin  `json:"origin"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode gateway input: %v", err)
		}
		if payload.CacheMode != "cache" || payload.RecoveryPolicy["maxAttempts"] != float64(2) {
			t.Errorf("cache/recovery inputs = %#v", payload)
		}
		if payload.RecoveryPolicy["jsonRepair"] == nil || payload.RecoveryPolicy["rerun"] != nil {
			t.Errorf("repair policy branches = %#v", payload.RecoveryPolicy)
		}
		if payload.UserPrompt != "Synthetic capacity cache fixture cache-case" {
			t.Errorf("cache-hit input was not stable across warmup and measurement: %q", payload.UserPrompt)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		terminal, err := json.Marshal(map[string]any{"type": "run.completed", "data": map[string]any{"result": map[string]any{
			"status": "succeeded", "output": map[string]any{"ok": true}, "origin": payload.Origin,
		}}})
		if err != nil {
			t.Errorf("encode terminal event: %v", err)
		}
		_, _ = fmt.Fprintf(writer, "event: run.completed\ndata: %s\n\n", terminal)
	}))
	defer server.Close()
	transport, err := NewGatewayHTTPTransport(GatewayHTTPTransportConfig{
		Endpoint: server.URL + "/api/v1/run", BearerToken: token, ProfileID: "synthetic-profile",
		ScenarioID: "cache-case", TestRunID: "run-synthetic-3", Client: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.Send(context.Background(), Request{
		ID: 2, Population: "measurement", CacheMode: "hit", RecoveryPolicy: "json-repair",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCapacityDriverGatewayHTTPTransportRejectsMismatchedReturnedOrigin(t *testing.T) {
	token := "synthetic-capacity-token-never-production-003"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "event: run.completed\ndata: {\"type\":\"run.completed\",\"data\":{\"result\":{\"status\":\"succeeded\",\"output\":{\"ok\":true},\"origin\":{\"client\":\"wrong-origin\"}}}}\n\n")
	}))
	defer server.Close()
	transport, err := NewGatewayHTTPTransport(GatewayHTTPTransportConfig{
		Endpoint: server.URL + "/api/v1/run", BearerToken: token, ProfileID: "synthetic-profile",
		ScenarioID: "origin-case", TestRunID: "run-synthetic-4", Client: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Send(context.Background(), Request{ID: 1, Population: "measurement"}); err == nil {
		t.Fatal("terminal response with mismatched origin was accepted")
	}
}

func TestCapacityDriverGatewayHTTPTransportRejectsRedirectAndRequiresTerminalSuccess(t *testing.T) {
	token := "synthetic-capacity-token-never-production-001"
	redirectCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		redirectCalls++
		writer.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", target.URL)
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	transport, err := NewGatewayHTTPTransport(GatewayHTTPTransportConfig{
		Endpoint: server.URL + "/api/v1/run", BearerToken: token, ProfileID: "synthetic-profile",
		ScenarioID: "truncated-case", TestRunID: "run-synthetic-2", Client: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.Send(context.Background(), Request{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusFound || redirectCalls != 0 {
		t.Fatalf("redirect status/calls = %d/%d, want 302/0", response.StatusCode, redirectCalls)
	}

	truncated := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "event: run.started\ndata: {\"type\":\"run.started\",\"data\":{}}\n\n")
	}))
	defer truncated.Close()
	transport, err = NewGatewayHTTPTransport(GatewayHTTPTransportConfig{
		Endpoint: truncated.URL + "/api/v1/run", BearerToken: token, ProfileID: "synthetic-profile",
		ScenarioID: "truncated-case", TestRunID: "run-synthetic-2", Client: truncated.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err = transport.Send(context.Background(), Request{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if response.StreamTerminal != "" {
		t.Fatalf("EOF without a terminal event was accepted as %q", response.StreamTerminal)
	}
	result, err := Run(context.Background(), Scenario{
		OfferedRPS: 1, Measurement: time.Second, Drain: time.Second, MaxRequests: 1, MaxInflight: 1,
	}, fixedTransport{response: response}, &immediateClock{now: time.Unix(0, 0)})
	if err != nil || result.Failed != 1 || result.Succeeded != 0 {
		t.Fatalf("EOF without run.completed outcome = %#v, error = %v", result, err)
	}
}

type fixedTransport struct{ response Response }

func (transport fixedTransport) Send(context.Context, Request) (Response, error) {
	return transport.response, nil
}
