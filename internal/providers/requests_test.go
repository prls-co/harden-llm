package providers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-012

func TestProviderRequestParity(t *testing.T) {
	t.Parallel()
	router, err := NewRouter(Config{EndpointPolicy: EndpointPolicy{
		Resolver: staticResolver{
			"api.openai.com":                    {netip.MustParseAddr("104.18.7.192")},
			"generativelanguage.googleapis.com": {netip.MustParseAddr("142.250.72.234")},
			"api.anthropic.com":                 {netip.MustParseAddr("160.79.104.10")},
			"api.vendor.example":                {netip.MustParseAddr("93.184.216.34")},
		},
	}})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)
	baseCall := runtime.Call{
		SystemPrompt: "Be exact.", UserPrompt: "Answer.", CallType: "structured", Schema: schema,
		ReasoningEffort: "highest", ProviderOptions: map[string]any{
			"max_tokens": float64(42), "timeout": float64(5000),
			"tools": []any{map[string]any{"type": "function", "name": "lookup"}}, "provider_native": "preserve-where-supported",
		},
	}

	tests := []struct {
		name           string
		profile        runtime.Profile
		wantProtocol   string
		wantPath       string
		wantProvider   string
		assertPayload  func(*testing.T, map[string]any)
		assertPrepared func(*testing.T, preparedRequest)
	}{
		{
			name:         "OpenAI Responses",
			profile:      runtime.Profile{ID: "openai", Provider: "openai", APIInferenceType: "responses", BaseURL: "https://api.openai.com/v1", ModelID: "gpt-5.4", SupportsStructuredOutput: true, SupportsTemperature: false, ResponsesTokensParam: "max_output_tokens", ReasoningEffortMap: map[string]map[string]any{"highest": {"reasoning": map[string]any{"effort": "high"}}}},
			wantProtocol: "openai.responses", wantPath: "/responses", wantProvider: "openai",
			assertPayload: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if payload["model"] != "gpt-5.4" || payload["max_output_tokens"] != float64(42) {
					t.Fatalf("unexpected Responses payload: %#v", payload)
				}
				if _, ok := payload["max_tokens"]; ok {
					t.Fatal("max_tokens was not remapped")
				}
				if !reflect.DeepEqual(payload["reasoning"], map[string]any{"effort": "high"}) {
					t.Fatalf("reasoning was not mapped: %#v", payload["reasoning"])
				}
				if _, ok := payload["temperature"]; ok {
					t.Fatal("temperature leaked to unsupported model")
				}
				if _, ok := payload["tools"]; !ok || payload["provider_native"] != "preserve-where-supported" {
					t.Fatalf("Responses native options were dropped: %#v", payload)
				}
			},
		},
		{
			name:         "OpenAI Chat",
			profile:      runtime.Profile{ID: "chat", Provider: "openai", APIInferenceType: "chat-completions", BaseURL: "https://api.openai.com/v1", ModelID: "gpt-4.1", SupportsStructuredOutput: true, SupportsTemperature: true, TokensParam: "max_completion_tokens", ReasoningEffortMap: map[string]map[string]any{"highest": {}}},
			wantProtocol: "openai-compatible.chat.completions", wantPath: "/chat/completions", wantProvider: "openai",
			assertPayload: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if payload["max_completion_tokens"] != float64(42) || payload["temperature"] != 0.3 {
					t.Fatalf("unexpected Chat payload: %#v", payload)
				}
				if _, ok := payload["response_format"]; !ok {
					t.Fatal("structured response_format is missing")
				}
				if _, ok := payload["tools"]; !ok {
					t.Fatal("Chat tools were dropped")
				}
			},
		},
		{
			name:         "Gemini GenerateContent",
			profile:      runtime.Profile{ID: "gemini", Provider: "google", APIInferenceType: "gemini-generate-content", BaseURL: "https://generativelanguage.googleapis.com", ModelID: "models/gemini-2.5-flash", SupportsStructuredOutput: true, SupportsTemperature: true, ReasoningEffortMap: map[string]map[string]any{"highest": {}}},
			wantProtocol: "google.gemini.generateContent", wantPath: "/v1beta/models/gemini-2.5-flash:generateContent", wantProvider: "google",
			assertPayload: func(t *testing.T, payload map[string]any) {
				t.Helper()
				config := payload["generationConfig"].(map[string]any)
				if config["maxOutputTokens"] != float64(42) || config["response_mime_type"] != "application/json" {
					t.Fatalf("unexpected Gemini config: %#v", config)
				}
				if _, ok := payload["provider_native"]; ok {
					t.Fatal("unknown OpenAI-native option leaked into Gemini")
				}
			},
			assertPrepared: func(t *testing.T, request preparedRequest) {
				t.Helper()
				if request.headers.Get("X-Goog-Api-Key") != "test-secret" || request.url.RawQuery != "" {
					t.Fatalf("Gemini credential must be header-only: %s %#v", request.url.String(), request.headers)
				}
			},
		},
		{
			name:         "Anthropic Messages",
			profile:      runtime.Profile{ID: "anthropic", Provider: "anthropic", APIInferenceType: "anthropic-messages", BaseURL: "https://api.anthropic.com/v1", ModelID: "claude-sonnet-4-5", SupportsStructuredOutput: true, SupportsTemperature: true, ReasoningEffortMap: map[string]map[string]any{"highest": {}}},
			wantProtocol: "anthropic.messages", wantPath: "/messages", wantProvider: "anthropic",
			assertPayload: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if payload["max_tokens"] != float64(42) || payload["system"] != "Be exact." {
					t.Fatalf("unexpected Anthropic payload: %#v", payload)
				}
				if _, ok := payload["tools"]; !ok {
					t.Fatal("Anthropic-native tools were dropped")
				}
			},
			assertPrepared: func(t *testing.T, request preparedRequest) {
				t.Helper()
				if request.headers.Get("Anthropic-Version") != "2023-06-01" || request.headers.Get("X-Api-Key") != "test-secret" {
					t.Fatalf("Anthropic headers missing: %#v", request.headers)
				}
			},
		},
		{
			name:         "Generic OpenAI compatible",
			profile:      runtime.Profile{ID: "vendor", Provider: "vendor", APIInferenceType: "chat-completions", BaseURL: "https://api.vendor.example/v1", ModelID: "vendor/model", SupportsStructuredOutput: true, SupportsTemperature: false, DefaultOptions: map[string]any{"provider": map[string]any{"only": []any{"fast"}}}, ReasoningEffortMap: map[string]map[string]any{"highest": {}}},
			wantProtocol: "openai-compatible.chat.completions", wantPath: "/chat/completions", wantProvider: "vendor",
			assertPayload: func(t *testing.T, payload map[string]any) {
				t.Helper()
				if _, ok := payload["temperature"]; ok {
					t.Fatal("temperature leaked to unsupported generic provider")
				}
				if _, ok := payload["provider"]; !ok {
					t.Fatalf("provider-native option was dropped: %#v", payload)
				}
				if payload["provider_native"] != "preserve-where-supported" {
					t.Fatalf("generic native option was dropped: %#v", payload)
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared, prepareErr := router.Prepare(context.Background(), test.profile, runtime.Credential{APIKey: "test-secret"}, baseCall)
			if prepareErr != nil {
				t.Fatalf("Prepare: %v", prepareErr)
			}
			if prepared.Operation.Protocol != test.wantProtocol || prepared.Operation.Endpoint.Path != test.wantPath || prepared.Operation.ResponseProjection.Provider != test.wantProvider {
				t.Fatalf("unexpected operation: %#v", prepared.Operation)
			}
			payload := prepared.Operation.Payload.(map[string]any)
			for _, runtimeKey := range []string{"timeout", "maxRetries", "reasoningEffort", "cacheMode"} {
				if _, ok := payload[runtimeKey]; ok {
					t.Fatalf("runtime option %q leaked into payload", runtimeKey)
				}
			}
			test.assertPayload(t, payload)
			request := prepared.Opaque.(preparedRequest)
			if request.url.User != nil || request.url.RawQuery != "" || request.headers.Get("Authorization") == "Bearer " {
				t.Fatalf("unsafe prepared request: %#v", request)
			}
			if test.assertPrepared != nil {
				test.assertPrepared(t, request)
			}
		})
	}
}

func TestProviderRequestParityCapturedSource(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../fixtures/parity/generated/provider-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name                string             `json:"name"`
			Operation           cachekey.Operation `json:"operation"`
			StructuredOperation cachekey.Operation `json:"structuredOperation"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{EndpointPolicy: EndpointPolicy{Resolver: staticResolver{
		"api.openai.com": {netip.MustParseAddr("104.18.7.192")}, "api.vendor.example": {netip.MustParseAddr("93.184.216.34")},
		"generativelanguage.googleapis.com": {netip.MustParseAddr("142.250.72.234")}, "api.anthropic.com": {netip.MustParseAddr("160.79.104.10")},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	request := runtime.Call{
		SystemPrompt: "Be exact.", UserPrompt: "Answer the deterministic fixture.", CallType: "text",
		ProviderOptions: map[string]any{"max_tokens": float64(42), "timeout": float64(5000)},
	}
	profiles := map[string]runtime.Profile{
		"openai-responses": {
			ID: "responses", Provider: "openai", APIInferenceType: "responses", BaseURL: "https://api.openai.com/v1", ModelID: "gpt-5.4",
			SupportsTemperature: false, SupportsStructuredOutput: true, ResponsesTokensParam: "max_output_tokens", DefaultOptions: map[string]any{"reasoning": map[string]any{"effort": "high"}},
		},
		"openai-chat": {
			ID: "chat", Provider: "openai", APIInferenceType: "responses", BaseURL: "https://api.openai.com/v1", ModelID: "gpt-4.1",
			SupportsTemperature: true, SupportsStructuredOutput: true, TokensParam: "max_completion_tokens", DefaultOptions: map[string]any{},
		},
		"generic-openai-compatible": {
			ID: "vendor", Provider: "vendor", APIInferenceType: "chat-completions", BaseURL: "https://api.vendor.example/v1", ModelID: "vendor/model",
			SupportsTemperature: false, SupportsStructuredOutput: true, DefaultOptions: map[string]any{"provider": map[string]any{"only": []any{"fast"}}},
		},
		"gemini-generate-content": {
			ID: "gemini", Provider: "google", APIInferenceType: "gemini-generate-content", BaseURL: "https://generativelanguage.googleapis.com", ModelID: "models/gemini-2.5-flash",
			SupportsTemperature: true, SupportsStructuredOutput: true, DefaultOptions: map[string]any{},
		},
		"anthropic-messages": {
			ID: "anthropic", Provider: "anthropic", APIInferenceType: "anthropic-messages", BaseURL: "https://api.anthropic.com/v1", ModelID: "claude-sonnet-4-5",
			SupportsTemperature: true, SupportsStructuredOutput: true, DefaultOptions: map[string]any{},
		},
	}
	for _, captured := range fixture.Cases {
		captured := captured
		t.Run(captured.Name, func(t *testing.T) {
			t.Parallel()
			call := request
			call.ProviderOptions = cloneMap(request.ProviderOptions)
			if captured.Name == "openai-chat" {
				call.ProviderOptions["useResponsesApi"] = false
			}
			prepared, prepareErr := router.Prepare(context.Background(), profiles[captured.Name], runtime.Credential{APIKey: "fixture-secret"}, call)
			if prepareErr != nil {
				t.Fatalf("Prepare: %v", prepareErr)
			}
			wantOperation := captured.Operation
			wantOperation.ResponseProjection.Version = "v3"
			if !jsonEquivalent(prepared.Operation, wantOperation) {
				got, _ := json.MarshalIndent(prepared.Operation, "", "  ")
				want, _ := json.MarshalIndent(wantOperation, "", "  ")
				t.Fatalf("captured operation mismatch:\n got %s\nwant %s", got, want)
			}

			structuredCall := call
			structuredCall.CallType = "structured"
			structuredCall.Schema = json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"},"count":{"type":"number"}},"required":["answer","count"],"additionalProperties":false}`)
			structured, structuredErr := router.Prepare(context.Background(), profiles[captured.Name], runtime.Credential{APIKey: "fixture-secret"}, structuredCall)
			if structuredErr != nil {
				t.Fatalf("Prepare structured: %v", structuredErr)
			}
			wantStructuredOperation := captured.StructuredOperation
			wantStructuredOperation.ResponseProjection.Version = "v3"
			if !jsonEquivalent(structured.Operation, wantStructuredOperation) {
				got, _ := json.MarshalIndent(structured.Operation, "", "  ")
				want, _ := json.MarshalIndent(wantStructuredOperation, "", "  ")
				t.Fatalf("captured structured operation mismatch:\n got %s\nwant %s", got, want)
			}
		})
	}
}

func TestCPAResponsesEvalRequestParityCapturedSource(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/parity/source/evals/cpa-gpt-5.4-mini-responses-call.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ProviderRequest struct {
			APIInferenceType  string         `json:"apiInferenceType"`
			BaseURL           string         `json:"baseUrl"`
			Body              map[string]any `json:"body"`
			ForbiddenBodyKeys []string       `json:"forbiddenBodyKeys"`
			Method            string         `json:"method"`
			Path              string         `json:"path"`
			Provider          string         `json:"provider"`
			URL               string         `json:"url"`
		} `json:"providerRequest"`
		UtilityCall struct {
			Arguments struct {
				ModelID      string `json:"modelId"`
				SystemPrompt string `json:"systemPrompt"`
				UserPrompt   string `json:"userPrompt"`
			} `json:"arguments"`
		} `json:"utilityCall"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Config{EndpointPolicy: EndpointPolicy{Resolver: staticResolver{
		"cpa.prls.co": {netip.MustParseAddr("93.184.216.34")},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	profile := runtime.Profile{
		ID: "cpa-eval", Provider: fixture.ProviderRequest.Provider,
		APIInferenceType: fixture.ProviderRequest.APIInferenceType,
		BaseURL:          fixture.ProviderRequest.BaseURL, ModelID: fixture.UtilityCall.Arguments.ModelID,
		SupportsTemperature: false, ResponsesTokensParam: "max_output_tokens",
		DefaultOptions: map[string]any{"max_output_tokens": float64(16000), "stream": true},
	}
	prepared, err := router.Prepare(context.Background(), profile, runtime.Credential{APIKey: "fixture-secret"}, runtime.Call{
		CallType: "text", SystemPrompt: fixture.UtilityCall.Arguments.SystemPrompt, UserPrompt: fixture.UtilityCall.Arguments.UserPrompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := prepared.Opaque.(preparedRequest)
	if prepared.Operation.Endpoint.Method != fixture.ProviderRequest.Method || prepared.Operation.Endpoint.Path != fixture.ProviderRequest.Path ||
		prepared.Operation.ResponseProjection.Provider != fixture.ProviderRequest.Provider || request.url.String() != fixture.ProviderRequest.URL {
		t.Fatalf("CPA request routing mismatch: operation=%#v url=%s", prepared.Operation, request.url)
	}
	if !jsonEquivalent(prepared.Operation.Payload, fixture.ProviderRequest.Body) {
		got, _ := json.MarshalIndent(prepared.Operation.Payload, "", "  ")
		want, _ := json.MarshalIndent(fixture.ProviderRequest.Body, "", "  ")
		t.Fatalf("CPA request body mismatch:\n got %s\nwant %s", got, want)
	}
	for _, key := range fixture.ProviderRequest.ForbiddenBodyKeys {
		if _, present := prepared.Operation.Payload.(map[string]any)[key]; present {
			t.Errorf("forbidden CPA request key %q is present", key)
		}
	}
	if request.headers.Get("Content-Type") != "application/json" || request.headers.Get("Authorization") != "Bearer fixture-secret" {
		t.Fatalf("CPA request headers mismatch: %#v", request.headers)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-214
func TestRecoveryBoundaryTransport(t *testing.T) {
	t.Run("observed model headers set dispatch fact", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"status":"completed","output_text":"ok"}`)
		}))
		defer server.Close()
		requestURL, err := url.Parse(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		operation := cachekey.Operation{Protocol: "openai.responses", Endpoint: cachekey.Endpoint{Identity: requestURL.String(), Method: http.MethodPost, Path: "/"}}
		prepared := preparedRequest{url: requestURL, headers: http.Header{"Authorization": {"Bearer fixture"}}, body: []byte(`{}`), protocol: operation.Protocol, operation: operation}
		result, executeErr := (&Router{client: server.Client(), maxResponseBytes: defaultMaxResponseBytes, now: time.Now}).Execute(context.Background(), runtime.PreparedOperation{Operation: operation, Opaque: prepared})
		if executeErr != nil || result.Output != "ok" || !result.ProviderDispatched {
			t.Fatalf("dispatch result = %#v, %v", result, executeErr)
		}
	})

	t.Run("HTTP status wins when diagnostic body is interrupted", func(t *testing.T) {
		requestURL, _ := url.Parse("https://provider.example/run")
		operation := cachekey.Operation{Protocol: "openai.responses", Endpoint: cachekey.Endpoint{Identity: requestURL.String(), Method: http.MethodPost, Path: "/run"}}
		router := &Router{client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: failingBody{}}, nil
		})}, now: time.Now}
		_, executeErr := router.Execute(context.Background(), runtime.PreparedOperation{Operation: operation, Opaque: preparedRequest{url: requestURL, headers: make(http.Header), body: []byte(`{}`), protocol: operation.Protocol, operation: operation}})
		if executeErr == nil || retry.Classify(executeErr, retry.DefaultPolicy()).Category != retry.CategoryServer {
			t.Fatalf("interrupted HTTP failure = %v", executeErr)
		}
	})

	transient := &net.DNSError{Err: "temporary failure", IsTemporary: true}
	if category, _ := classifyResolutionError(&endpointResolutionError{err: transient}); category != retry.CategoryNetwork {
		t.Fatalf("transient DNS category = %q", category)
	}
	permanent := &net.DNSError{Err: "no such host"}
	if category, _ := classifyResolutionError(&endpointResolutionError{err: permanent}); category != retry.CategoryOther {
		t.Fatalf("permanent DNS category = %q", category)
	}

	now := time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC)
	searcher := &jinaSearcher{
		client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {now.Add(37 * time.Second).Format(http.TimeFormat)}}, Body: http.NoBody}, nil
		})},
		apiKey: "fixture", baseURL: mustURL(t, "https://s.jina.ai/"), now: func() time.Time { return now },
	}
	_, searchErr := searcher.Search(context.Background(), "query")
	classification := retry.Classify(searchErr, retry.DefaultPolicy())
	if classification.Category != retry.CategoryRateLimit || classification.RetryAfter != 37*time.Second {
		t.Fatalf("Jina Retry-After = %#v, want rate_limit/37s", classification)
	}

	certificateURL := mustURL(t, "https://provider.example/run")
	certificateOperation := cachekey.Operation{Protocol: "openai.responses", Endpoint: cachekey.Endpoint{Identity: certificateURL.String(), Method: http.MethodPost, Path: "/run"}}
	certificateRouter := &Router{client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "tls", URL: certificateURL.String(), Err: x509.UnknownAuthorityError{}}
	})}, now: time.Now}
	_, certificateErr := certificateRouter.Execute(context.Background(), runtime.PreparedOperation{Operation: certificateOperation, Opaque: preparedRequest{url: certificateURL, headers: make(http.Header), body: []byte(`{}`), protocol: certificateOperation.Protocol, operation: certificateOperation}})
	if certificateErr == nil || retry.Classify(certificateErr, retry.DefaultPolicy()).Category != retry.CategoryOther {
		t.Fatalf("certificate failure = %v", certificateErr)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-217
func TestRecoveryBoundaryAccountingCacheRead(t *testing.T) {
	requestURL := mustURL(t, "https://provider.example/run")
	operation := cachekey.Operation{Protocol: "openai.responses", Endpoint: cachekey.Endpoint{Identity: requestURL.String(), Method: http.MethodPost, Path: "/run"}}
	router := &Router{client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: &errorReadCloser{reader: strings.NewReader(`{"status":"completed","output_text":"must not be accepted","usage":{"input_tokens":11,"output_tokens":3}}`), err: errors.New("connection reset after body")},
		}, nil
	})}, maxResponseBytes: defaultMaxResponseBytes, now: time.Now}
	result, err := router.Execute(context.Background(), runtime.PreparedOperation{Operation: operation, Opaque: preparedRequest{
		url: requestURL, headers: make(http.Header), body: []byte(`{}`), protocol: operation.Protocol, operation: operation,
	}})
	if err == nil || retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryNetwork || result.Output != nil || result.Accounting.Usage.InputTokens != 11 || result.Accounting.Usage.OutputTokens != 3 {
		t.Fatalf("partial JSON read = %#v / %v", result, err)
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (fn transportFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("interrupted diagnostic body") }
func (failingBody) Close() error             { return nil }

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestReasoningEffortParityCapturedSource(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../fixtures/parity/generated/reasoning-effort-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ContractedEfforts []string       `json:"contractedEfforts"`
		Mapped            map[string]any `json:"mapped"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture.ContractedEfforts, []string{"lowest", "middle", "highest"}) {
		t.Fatalf("captured reasoning contract changed: %#v", fixture.ContractedEfforts)
	}
	profile := runtime.Profile{
		ID: "fixture-model", APIInferenceType: "responses", DefaultOptions: map[string]any{},
		ReasoningEffortMap: map[string]map[string]any{"highest": {"reasoning": map[string]any{"effort": "high"}, "budget": float64(9)}},
	}
	options, err := mergedOptions(profile, runtime.Call{
		ReasoningEffort: "highest", ProviderOptions: map[string]any{"budget": float64(1), "providerOption": true},
	})
	if err != nil || !jsonEquivalent(options, fixture.Mapped) {
		t.Fatalf("mapped reasoning mismatch: %#v %v", options, err)
	}
	for name, call := range map[string]runtime.Call{
		"native-without-portable": {ProviderOptions: map[string]any{"thinking_budget": float64(10)}},
		"unsupported-alias":       {ReasoningEffort: "high", ProviderOptions: map[string]any{}},
	} {
		if _, err = mergedOptions(profile, call); err == nil {
			t.Fatalf("%s reasoning contract violation was accepted", name)
		}
	}
}

func TestProviderOptionEdgeParity(t *testing.T) {
	t.Parallel()
	profile := runtime.Profile{
		ID: "responses", APIInferenceType: "responses", ModelID: "gpt", ResponsesTokensParam: "max_output_tokens",
		DefaultOptions: map[string]any{}, ReasoningEffortMap: map[string]map[string]any{"highest": {}},
	}
	options, err := mergedOptions(profile, runtime.Call{ProviderOptions: map[string]any{"reasoning": nil}})
	if err != nil || options["reasoning"] != nil {
		t.Fatalf("nil native reasoning option should remain non-conflicting: %#v %v", options, err)
	}
	payload := buildResponsesPayload(profile, runtime.Call{}, map[string]any{
		"max_output_tokens": float64(9), "max_tokens": float64(8), "max_completion_tokens": float64(7),
	}, nil)
	for key, want := range map[string]any{"max_output_tokens": float64(9), "max_tokens": float64(8), "max_completion_tokens": float64(7)} {
		if payload[key] != want {
			t.Fatalf("Responses token option %q = %#v, want %#v; source retains legacy options when target exists", key, payload[key], want)
		}
	}

	gemini := runtime.Profile{
		ID: "gemini", APIInferenceType: "gemini-generate-content",
		DefaultOptions:     map[string]any{"thinkingConfig": map[string]any{"thinkingBudget": float64(128), "includeThoughts": true}},
		ReasoningEffortMap: map[string]map[string]any{"highest": {"thinkingConfig": map[string]any{"thinkingLevel": "HIGH"}}},
	}
	options, err = mergedOptions(gemini, runtime.Call{ReasoningEffort: "highest", ProviderOptions: map[string]any{}})
	if err != nil || !jsonEquivalent(options["thinkingConfig"], map[string]any{
		"thinkingBudget": float64(128), "thinkingLevel": "HIGH", "includeThoughts": true,
	}) {
		t.Fatalf("Gemini thinkingConfig merge mismatch: %#v %v", options, err)
	}

	maximum := positiveIntegerOption(map[string]any{"max_tokens": json.Number("42.9")}, "max_tokens")
	if maximum != float64(42) {
		t.Fatalf("Anthropic decimal max token truncation = %#v, want 42", maximum)
	}
}

func jsonEquivalent(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	var leftValue, rightValue any
	if json.Unmarshal(leftBytes, &leftValue) != nil || json.Unmarshal(rightBytes, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-206
func TestRecoveryRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		header string
		want   time.Duration
	}{
		{"30", 30 * time.Second}, {" 30 ", 30 * time.Second}, {"0", 0}, {"-3", 0}, {"bad", 0}, {"1.5", 0},
		{now.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second},
		{now.Add(-time.Second).Format(http.TimeFormat), 0},
		{"9223372036854775807", time.Duration(1<<63 - 1)},
		{"999999999999999999999999999999", time.Duration(1<<63 - 1)},
		{"999999999999999999999999999999bad", 0},
		{"+30", 0},
	} {
		if got := parseRetryAfter(c.header, now); got != c.want {
			t.Errorf("header=%q delay=%v want=%v", c.header, got, c.want)
		}
	}
	fractionalNow := now.Add(500 * time.Nanosecond)
	if got := parseRetryAfter(now.Add(time.Second).Format(http.TimeFormat), fractionalNow); got != time.Second {
		t.Fatalf("fractional date minimum = %v, want %v", got, time.Second)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-223
func TestRecoveryIntegrityTransport(t *testing.T) {
	t.Parallel()
	t.Run("redirect rejection is typed and does not contact target", func(t *testing.T) {
		var initialRequests, targetRequests atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/initial" {
				initialRequests.Add(1)
				http.Redirect(writer, request, "/target", http.StatusFound)
				return
			}
			targetRequests.Add(1)
			writer.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		pool := x509.NewCertPool()
		pool.AddCert(server.Certificate())
		client, err := newSafeHTTPClient(EndpointPolicy{
			PrivateAllowedHosts: []string{"127.0.0.1"},
			TLSConfig:           &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
		})
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/initial", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Do(request)
		var policyErr *endpointPolicyError
		if err == nil || !errors.As(err, &policyErr) {
			t.Fatalf("redirect error = %v, want endpoint policy error", err)
		}
		if initialRequests.Load() != 1 || targetRequests.Load() != 0 {
			t.Fatalf("redirect requests initial=%d target=%d", initialRequests.Load(), targetRequests.Load())
		}
	})

	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		status := status
		t.Run(fmt.Sprintf("response size wins over HTTP %d", status), func(t *testing.T) {
			var requests atomic.Int32
			router := &Router{
				client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
					requests.Add(1)
					return &http.Response{
						StatusCode: status,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", 17))),
					}, nil
				})},
				maxResponseBytes: 8,
				now:              time.Now,
			}
			operation := cachekey.Operation{Protocol: "openai.responses", Endpoint: cachekey.Endpoint{Identity: "https://provider.example/run", Method: http.MethodPost, Path: "/run"}}
			result, err := router.Execute(context.Background(), runtime.PreparedOperation{
				Operation: operation,
				Opaque:    preparedRequest{url: mustURL(t, "https://provider.example/run"), headers: make(http.Header), body: []byte(`{}`), protocol: operation.Protocol, operation: operation},
			})
			var providerErr *retry.ProviderError
			if err == nil || !errors.As(err, &providerErr) || providerErr.Code != "RESPONSE_TOO_LARGE" || providerErr.Category != retry.CategoryOther {
				t.Fatalf("size/status result=%#v error=%v", result, err)
			}
			if requests.Load() != 1 || result.Output != nil {
				t.Fatalf("size/status requests=%d result=%#v", requests.Load(), result)
			}
		})
	}

	t.Run("size wins when reader returns bytes and an error", func(t *testing.T) {
		router := &Router{
			client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: &bytesErrorBody{data: []byte(strings.Repeat("x", 17)), err: errors.New("connection reset")}}, nil
			})},
			maxResponseBytes: 8,
			now:              time.Now,
		}
		operation := cachekey.Operation{Protocol: "openai.responses", Endpoint: cachekey.Endpoint{Identity: "https://provider.example/run", Method: http.MethodPost, Path: "/run"}}
		_, err := router.Execute(context.Background(), runtime.PreparedOperation{
			Operation: operation,
			Opaque:    preparedRequest{url: mustURL(t, "https://provider.example/run"), headers: make(http.Header), body: []byte(`{}`), protocol: operation.Protocol, operation: operation},
		})
		var providerErr *retry.ProviderError
		if err == nil || !errors.As(err, &providerErr) || providerErr.Code != "RESPONSE_TOO_LARGE" {
			t.Fatalf("simultaneous size/read error=%v", err)
		}
	})

	t.Run("status and retry-after survive interrupted body", func(t *testing.T) {
		now := time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC)
		router := &Router{
			client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Header:     http.Header{"Retry-After": {now.Add(37 * time.Second).Format(http.TimeFormat)}},
					Body:       &errorReadCloser{reader: strings.NewReader(`{"error":{"code":"server_error"}}`), err: errors.New("connection reset")},
				}, nil
			})},
			now:              func() time.Time { return now },
			maxResponseBytes: defaultMaxResponseBytes,
		}
		operation := cachekey.Operation{Protocol: "openai.responses", Endpoint: cachekey.Endpoint{Identity: "https://provider.example/run", Method: http.MethodPost, Path: "/run"}}
		_, err := router.Execute(context.Background(), runtime.PreparedOperation{
			Operation: operation,
			Opaque:    preparedRequest{url: mustURL(t, "https://provider.example/run"), headers: make(http.Header), body: []byte(`{}`), protocol: operation.Protocol, operation: operation},
		})
		classification := retry.Classify(err, retry.DefaultPolicy())
		if classification.Category != retry.CategoryServer || classification.Status != http.StatusServiceUnavailable || classification.Code != "server_error" || classification.RetryAfter != 37*time.Second {
			t.Fatalf("interrupted status classification=%#v error=%v", classification, err)
		}
	})

	t.Run("parent cancellation during model body read remains terminal", func(t *testing.T) {
		started := make(chan struct{})
		var once sync.Once
		router := &Router{
			client: &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
					Body: &contextBody{ctx: request.Context(), started: started, once: &once},
				}, nil
			})},
			maxResponseBytes: defaultMaxResponseBytes, now: time.Now,
		}
		requestURL := mustURL(t, "https://provider.example/run")
		operation := cachekey.Operation{Protocol: "openai.responses", Endpoint: cachekey.Endpoint{Identity: requestURL.String(), Method: http.MethodPost, Path: "/run"}}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		resultCh := make(chan struct {
			result runtime.ProviderResult
			err    error
		}, 1)
		go func() {
			result, err := router.Execute(ctx, runtime.PreparedOperation{
				Operation: operation,
				Opaque:    preparedRequest{url: requestURL, headers: make(http.Header), body: []byte(`{}`), protocol: operation.Protocol, operation: operation},
			})
			resultCh <- struct {
				result runtime.ProviderResult
				err    error
			}{result, err}
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("model body read did not start")
		}
		cancel()
		select {
		case result := <-resultCh:
			if !errors.Is(result.err, context.Canceled) || result.result.Output != nil {
				t.Fatalf("canceled body result=%#v error=%v", result.result, result.err)
			}
		case <-time.After(time.Second):
			t.Fatal("canceled body read did not return")
		}
	})

	transient := &net.DNSError{Err: "temporary failure", IsTemporary: true}
	if category, _ := classifyResolutionError(&endpointResolutionError{err: transient}); category != retry.CategoryNetwork {
		t.Fatalf("transient DNS category = %q", category)
	}
	permanent := &net.DNSError{Err: "no such host"}
	if category, _ := classifyResolutionError(&endpointResolutionError{err: permanent}); category != retry.CategoryOther {
		t.Fatalf("permanent DNS category = %q", category)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-225
func TestRecoveryIntegrityTimeout(t *testing.T) {
	t.Parallel()
	newRouter := func(t *testing.T, searcher Searcher) *Router {
		t.Helper()
		router, err := NewRouter(Config{
			EndpointPolicy: EndpointPolicy{Resolver: staticResolver{"provider.example": {netip.MustParseAddr("93.184.216.34")}}},
			WebSearcher:    searcher,
		})
		if err != nil {
			t.Fatal(err)
		}
		return router
	}
	profile := runtime.Profile{
		ID: "p", Provider: "cpa", APIInferenceType: "responses", BaseURL: "https://provider.example/v1", ModelID: "fixture",
		DefaultOptions: map[string]any{}, SupportsWebSearch: false,
	}
	call := func(timeoutMS float64) runtime.Call {
		return runtime.Call{CallType: "text", UserPrompt: "query", WebSearch: true, ProviderOptions: map[string]any{"timeout": timeoutMS}}
	}
	config := func(policy retry.Policy) retry.Config {
		return retry.Config{Policy: policy, Wait: func(context.Context, time.Duration) error { return nil }}
	}
	credentials := func(context.Context, runtime.Profile) (runtime.Credential, error) {
		return runtime.Credential{APIKey: "fixture"}, nil
	}

	t.Run("attempt timeout during search remains network while parent lives", func(t *testing.T) {
		searcher := &recoveryBlockingSearcher{}
		router := newRouter(t, searcher)
		record, err := runtime.Execute(context.Background(), router, credentials, profile.ID, map[string]runtime.Profile{profile.ID: profile}, call(10), config(retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{retry.CategoryNetwork}, Backoff: retry.Backoff{}}), nil, cachekey.ModeOff, "operation-v2", "call", "trace")
		classification := retry.Classify(err, retry.DefaultPolicy())
		if err == nil || classification.Category != retry.CategoryNetwork || len(record.Attempts) != 2 || searcher.calls.Load() != 2 {
			t.Fatalf("attempt timeout record=%#v calls=%d classification=%#v error=%v", record, searcher.calls.Load(), classification, err)
		}
		if record.Attempts[0].ProviderUsed || record.Attempts[1].ProviderUsed {
			t.Fatalf("search-only timeout claimed model dispatch: %#v", record.Attempts)
		}
	})

	t.Run("parent cancellation stops search and retry", func(t *testing.T) {
		searcher := &recoveryBlockingSearcher{started: make(chan struct{})}
		router := newRouter(t, searcher)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		resultCh := make(chan struct {
			record runtime.CallRecord
			err    error
		}, 1)
		go func() {
			record, err := runtime.Execute(ctx, router, credentials, profile.ID, map[string]runtime.Profile{profile.ID: profile}, call(500), config(retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{retry.CategoryNetwork}, Backoff: retry.Backoff{}}), nil, cachekey.ModeOff, "operation-v2", "call", "trace")
			resultCh <- struct {
				record runtime.CallRecord
				err    error
			}{record, err}
		}()
		select {
		case <-searcher.started:
		case <-time.After(time.Second):
			t.Fatal("search did not start")
		}
		cancel()
		select {
		case result := <-resultCh:
			if !errors.Is(result.err, context.Canceled) || len(result.record.Attempts) != 1 || searcher.calls.Load() != 1 {
				t.Fatalf("canceled result=%#v calls=%d error=%v", result.record, searcher.calls.Load(), result.err)
			}
		case <-time.After(time.Second):
			t.Fatal("canceled search did not return")
		}
	})

	t.Run("parent deadline takes precedence over attempt timeout", func(t *testing.T) {
		searcher := &recoveryBlockingSearcher{}
		router := newRouter(t, searcher)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		record, err := runtime.Execute(ctx, router, credentials, profile.ID, map[string]runtime.Profile{profile.ID: profile}, call(500), config(retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{retry.CategoryNetwork}, Backoff: retry.Backoff{}}), nil, cachekey.ModeOff, "operation-v2", "call", "trace")
		if !errors.Is(err, context.DeadlineExceeded) || len(record.Attempts) != 1 || searcher.calls.Load() != 1 || retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryTimeout {
			t.Fatalf("parent deadline record=%#v calls=%d error=%v", record, searcher.calls.Load(), err)
		}
	})

	t.Run("Jina-local timeout remains network", func(t *testing.T) {
		var requests atomic.Int32
		jina := &jinaSearcher{
			client: &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
				requests.Add(1)
				<-request.Context().Done()
				return nil, request.Context().Err()
			})},
			apiKey: "fixture", baseURL: mustURL(t, "https://s.jina.ai/"), timeout: 5 * time.Millisecond, maxResponseBytes: defaultJinaMaxResponseBytes,
		}
		router := newRouter(t, jina)
		record, err := runtime.Execute(context.Background(), router, credentials, profile.ID, map[string]runtime.Profile{profile.ID: profile}, call(50), config(retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{retry.CategoryNetwork}, Backoff: retry.Backoff{}}), nil, cachekey.ModeOff, "operation-v2", "call", "trace")
		if err == nil || retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryNetwork || len(record.Attempts) != 2 || requests.Load() != 2 {
			t.Fatalf("Jina-local timeout record=%#v requests=%d error=%v", record, requests.Load(), err)
		}
	})

	t.Run("search memo survives model attempt timeouts", func(t *testing.T) {
		searcher := &recordingSearcher{result: "evidence"}
		router := newRouter(t, searcher)
		var modelRequests atomic.Int32
		router.client = &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
			modelRequests.Add(1)
			<-request.Context().Done()
			return nil, request.Context().Err()
		})}
		record, err := runtime.Execute(context.Background(), router, credentials, profile.ID, map[string]runtime.Profile{profile.ID: profile}, call(10), config(retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{retry.CategoryNetwork}, Backoff: retry.Backoff{}}), nil, cachekey.ModeOff, "operation-v2", "call", "trace")
		if err == nil || retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryNetwork || len(record.Attempts) != 2 || searcher.calls != 1 || modelRequests.Load() != 2 {
			t.Fatalf("memo/model timeout record=%#v search=%d model=%d error=%v", record, searcher.calls, modelRequests.Load(), err)
		}
	})

	for _, test := range []struct {
		name   string
		policy retry.Policy
	}{
		{name: "retry disabled", policy: retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}}},
		{name: "one attempt", policy: retry.Policy{MaxAttempts: 1, RetryOn: []retry.Category{retry.CategoryNetwork}, Backoff: retry.Backoff{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			searcher := &recoveryBlockingSearcher{}
			router := newRouter(t, searcher)
			record, err := runtime.Execute(context.Background(), router, credentials, profile.ID, map[string]runtime.Profile{profile.ID: profile}, call(10), config(test.policy), nil, cachekey.ModeOff, "operation-v2", "call", "trace")
			if err == nil || len(record.Attempts) != 1 || searcher.calls.Load() != 1 {
				t.Fatalf("control record=%#v calls=%d error=%v", record, searcher.calls.Load(), err)
			}
		})
	}
}

type recoveryBlockingSearcher struct {
	calls   atomic.Int32
	started chan struct{}
	once    sync.Once
}

func (searcher *recoveryBlockingSearcher) Search(ctx context.Context, _ string) (string, error) {
	searcher.calls.Add(1)
	if searcher.started != nil {
		searcher.once.Do(func() { close(searcher.started) })
	}
	<-ctx.Done()
	return "", ctx.Err()
}

type bytesErrorBody struct {
	data []byte
	err  error
	done bool
}

type contextBody struct {
	ctx     context.Context
	started chan struct{}
	once    *sync.Once
}

func (body *contextBody) Read([]byte) (int, error) {
	body.once.Do(func() { close(body.started) })
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}

func (body *contextBody) Close() error { return nil }

func (body *bytesErrorBody) Read(buffer []byte) (int, error) {
	if body.done {
		return 0, body.err
	}
	body.done = true
	count := copy(buffer, body.data)
	return count, body.err
}

func (body *bytesErrorBody) Close() error { return nil }

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-226
func TestRecoveryIntegrityAccountingProviderResponses(t *testing.T) {
	t.Parallel()
	newRouter := func(body io.ReadCloser, status int) *Router {
		return &Router{
			client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}, nil
			})},
			maxResponseBytes: defaultMaxResponseBytes,
			now:              time.Now,
		}
	}
	operation := cachekey.Operation{Protocol: "openai.responses", Endpoint: cachekey.Endpoint{Identity: "https://provider.example/run", Method: http.MethodPost, Path: "/run"}}
	prepared := func(router *Router) (runtime.ProviderResult, error) {
		return router.Execute(context.Background(), runtime.PreparedOperation{
			Operation: operation,
			Opaque:    preparedRequest{url: mustURL(t, "https://provider.example/run"), headers: make(http.Header), body: []byte(`{}`), protocol: operation.Protocol, operation: operation},
		})
	}

	t.Run("complete accounting survives non-2xx response", func(t *testing.T) {
		body := io.NopCloser(strings.NewReader(`{"error":{"code":"unavailable"},"usage":{"input_tokens":2,"output_tokens":1},"cost":0.01}`))
		result, err := prepared(newRouter(body, http.StatusServiceUnavailable))
		var providerErr *retry.ProviderError
		if err == nil || !errors.As(err, &providerErr) || providerErr.Status != http.StatusServiceUnavailable || result.Accounting.Usage.InputTokens != 2 || result.Accounting.Usage.OutputTokens != 1 || result.Accounting.Cost != (runtime.Cost{KnownSubtotalUSD: 0.01, Status: "exact", Source: "reported", KnownObservations: 1}) {
			t.Fatalf("non-2xx accounting result=%#v error=%v", result, err)
		}
	})

	t.Run("complete accounting survives an interrupted 2xx body", func(t *testing.T) {
		body := &errorReadCloser{reader: strings.NewReader(`{"status":"completed","output_text":"ok","usage":{"input_tokens":2,"output_tokens":1},"cost":0.01}`), err: errors.New("connection reset after body")}
		result, err := prepared(newRouter(body, http.StatusOK))
		if err == nil || retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryNetwork || result.Accounting.Usage.InputTokens != 2 || result.Accounting.Usage.OutputTokens != 1 || result.Accounting.Cost.KnownSubtotalUSD != 0.01 || result.Output != nil {
			t.Fatalf("interrupted complete accounting result=%#v error=%v", result, err)
		}
	})

	t.Run("invalid usage is terminal while reported cost survives interrupted body", func(t *testing.T) {
		body := &errorReadCloser{reader: strings.NewReader(`{"status":"completed","output_text":"ok","usage":{"input_tokens":-1,"output_tokens":1},"cost":0.01}`), err: errors.New("connection reset after body")}
		result, err := prepared(newRouter(body, http.StatusOK))
		var providerErr *retry.ProviderError
		if err == nil || !errors.As(err, &providerErr) || providerErr.Code != "ACCOUNTING_INVALID" || result.Accounting.Usage.Status != "unavailable" || result.Accounting.Cost.KnownSubtotalUSD != 0.01 || result.Output != nil {
			t.Fatalf("invalid interrupted accounting result=%#v error=%v", result, err)
		}
	})

	t.Run("truncated JSON contributes no guessed accounting", func(t *testing.T) {
		body := &errorReadCloser{reader: strings.NewReader(`{"status":"completed","output_text":"ok","usage":{"input_tokens":2`), err: errors.New("connection reset")}
		result, err := prepared(newRouter(body, http.StatusOK))
		if err == nil || result.Accounting.Usage.Status != "unavailable" || result.Accounting.Cost.Status != "unavailable" {
			t.Fatalf("truncated accounting result=%#v error=%v", result, err)
		}
	})
}
