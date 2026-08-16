package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/pricing"
	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
)

type capturedProviderNormalization struct {
	Result          any             `json:"result"`
	ResponsePayload json.RawMessage `json:"responsePayload"`
	Usage           pricing.Usage   `json:"usage"`
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-013

func TestProviderNormalization(t *testing.T) {
	t.Parallel()
	inputRate, cacheRate, outputRate, reasoningRate := 0.001, 0.0001, 0.002, 0.003
	pricing := runtime.Pricing{Input: &inputRate, CacheRead: &cacheRate, Output: &outputRate, Reasoning: &reasoningRate}
	tests := []struct {
		name       string
		protocol   string
		callType   string
		body       string
		wantOutput any
		wantUsage  runtime.Usage
		wantCost   runtime.Cost
	}{
		{
			name: "OpenAI Responses", protocol: "openai.responses", callType: "text",
			body:       `{"output_text":"responses-ok","usage":{"input_tokens":20,"input_tokens_details":{"cached_tokens":4},"output_tokens":8,"output_tokens_details":{"reasoning_tokens":3}}}`,
			wantOutput: "responses-ok", wantUsage: runtime.Usage{InputTokens: 16, CacheReadTokens: 4, OutputTokens: 5, ReasoningTokens: 3, TotalTokens: 28},
			wantCost: runtime.Cost{TotalUSD: 0.0354, Known: true, Source: "profile"},
		},
		{
			name: "OpenAI-compatible structured", protocol: "openai-compatible.chat.completions", callType: "structured",
			body:       `{"choices":[{"message":{"content":"{\"answer\":\"chat-ok\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"cost":0.125}}`,
			wantOutput: map[string]any{"answer": "chat-ok"}, wantUsage: runtime.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5},
			wantCost: runtime.Cost{TotalUSD: 0.125, Known: true, Source: "reported"},
		},
		{
			name: "Gemini", protocol: "google.gemini.generateContent", callType: "text",
			body:       `{"candidates":[{"content":{"parts":[{"text":"gemini-"},{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":2,"candidatesTokenCount":4,"thoughtsTokenCount":1}}`,
			wantOutput: "gemini-", wantUsage: runtime.Usage{InputTokens: 8, CacheReadTokens: 2, OutputTokens: 4, ReasoningTokens: 1, TotalTokens: 15},
			wantCost: runtime.Cost{TotalUSD: 0.0192, Known: true, Source: "profile"},
		},
		{
			name: "OpenAI nullish usage precedence", protocol: "openai.responses", callType: "text",
			body:       `{"output_text":"ok","usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":0},"cached_tokens":5,"output_tokens":4,"output_tokens_details":{"reasoning_tokens":0},"reasoning_tokens":3}}`,
			wantOutput: "ok", wantUsage: runtime.Usage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14},
			wantCost: runtime.Cost{TotalUSD: 0.018, Known: true, Source: "profile"},
		},
		{
			name: "Anthropic", protocol: "anthropic.messages", callType: "text",
			body:       `{"content":[{"type":"text","text":"anthropic-"},{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"cache_read_input_tokens":2,"output_tokens":3}}`,
			wantOutput: "anthropic-ok", wantUsage: runtime.Usage{InputTokens: 5, CacheReadTokens: 2, OutputTokens: 3, TotalTokens: 10},
			wantCost: runtime.Cost{TotalUSD: 0.0112, Known: true, Source: "profile"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := normalizeResponse(preparedRequest{provider: "fixture", protocol: test.protocol, callType: test.callType, pricing: pricing}, []byte(test.body))
			if err != nil {
				t.Fatalf("normalizeResponse: %v", err)
			}
			if !deepEqualJSON(result.Output, test.wantOutput) {
				t.Fatalf("output mismatch: got %#v want %#v", result.Output, test.wantOutput)
			}
			if result.Usage != test.wantUsage {
				t.Fatalf("usage mismatch: got %#v want %#v", result.Usage, test.wantUsage)
			}
			if !costNear(result.Cost, test.wantCost) {
				t.Fatalf("cost mismatch: got %#v want %#v", result.Cost, test.wantCost)
			}
			if strings.Contains(string(result.RawProviderEnvelope), "authorization") {
				t.Fatalf("raw envelope contains credential material: %s", result.RawProviderEnvelope)
			}
		})
	}
}

func TestProviderNormalizationParityCapturedSource(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../fixtures/parity/generated/provider-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name                 string                        `json:"name"`
			Operation            cachekey.Operation            `json:"operation"`
			Normalized           capturedProviderNormalization `json:"normalized"`
			StructuredOperation  cachekey.Operation            `json:"structuredOperation"`
			StructuredNormalized capturedProviderNormalization `json:"structuredNormalized"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, captured := range fixture.Cases {
		captured := captured
		t.Run(captured.Name, func(t *testing.T) {
			t.Parallel()
			for _, variant := range []struct {
				name       string
				callType   string
				operation  cachekey.Operation
				normalized capturedProviderNormalization
			}{
				{name: "text", callType: "text", operation: captured.Operation, normalized: captured.Normalized},
				{name: "structured", callType: "structured", operation: captured.StructuredOperation, normalized: captured.StructuredNormalized},
			} {
				variant := variant
				t.Run(variant.name, func(t *testing.T) {
					rates := variant.normalized.Usage.Items
					prepared := preparedRequest{
						provider: variant.operation.ResponseProjection.Provider, protocol: variant.operation.Protocol, callType: variant.callType,
						pricing: runtime.Pricing{
							Input: rates[pricing.ItemInput].RatePerToken, CacheRead: rates[pricing.ItemCacheRead].RatePerToken,
							CacheCreation: rates[pricing.ItemCacheCreation].RatePerToken, Output: rates[pricing.ItemOutput].RatePerToken,
							Reasoning: rates[pricing.ItemReasoning].RatePerToken,
						},
					}
					result, normalizeErr := normalizeResponse(prepared, variant.normalized.ResponsePayload)
					if normalizeErr != nil {
						t.Fatalf("normalizeResponse: %v", normalizeErr)
					}
					if !deepEqualJSON(result.Output, variant.normalized.Result) {
						t.Fatalf("output mismatch: got %#v want %#v", result.Output, variant.normalized.Result)
					}
					wantUsage := runtime.Usage{
						InputTokens: rates[pricing.ItemInput].Tokens, CacheReadTokens: rates[pricing.ItemCacheRead].Tokens,
						CacheCreationTokens: rates[pricing.ItemCacheCreation].Tokens, OutputTokens: rates[pricing.ItemOutput].Tokens,
						ReasoningTokens: rates[pricing.ItemReasoning].Tokens,
					}
					wantUsage.TotalTokens = wantUsage.InputTokens + wantUsage.CacheReadTokens + wantUsage.CacheCreationTokens + wantUsage.OutputTokens + wantUsage.ReasoningTokens
					if result.Usage != wantUsage {
						t.Fatalf("usage mismatch: got %#v want %#v", result.Usage, wantUsage)
					}
					summary, summaryErr := pricing.Summarize(variant.normalized.Usage)
					if summaryErr != nil || summary.TotalCost == nil || !result.Cost.Known || math.Abs(result.Cost.TotalUSD-*summary.TotalCost) > 1e-12 {
						t.Fatalf("cost mismatch: got %#v source=%#v error=%v", result.Cost, summary, summaryErr)
					}
				})
			}
		})
	}
}

func TestProviderNormalizationClassifiesSafeFailures(t *testing.T) {
	t.Parallel()
	policy := retry.DefaultPolicy()
	providerCases := []struct {
		name         string
		protocol     string
		body         string
		wantCategory retry.Category
	}{
		{"refusal", "openai-compatible.chat.completions", `{"choices":[{"finish_reason":"content_filter","message":{"content":""}}]}`, retry.CategoryRefusal},
		{"empty", "anthropic.messages", `{"content":[],"stop_reason":"end_turn"}`, retry.CategoryEmpty},
		{"malformed", "openai.responses", `{not-json`, retry.CategoryParse},
		{"structured malformed", "openai-compatible.chat.completions", `{"choices":[{"message":{"content":"{\"answer\":\"\\uZZZZ\"}"}}]}`, retry.CategoryParse},
	}
	for _, test := range providerCases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := normalizeResponse(preparedRequest{provider: "fixture", protocol: test.protocol, callType: map[bool]string{true: "structured", false: "text"}[test.name == "structured malformed"]}, []byte(test.body))
			if err == nil {
				t.Fatal("expected normalization failure")
			}
			if got := retry.Classify(err, policy).Category; got != test.wantCategory {
				t.Fatalf("category mismatch: got %q want %q (%v)", got, test.wantCategory, err)
			}
			if test.wantCategory == retry.CategoryEmpty {
				providerErr, ok := err.(*retry.ProviderError)
				if !ok || providerErr.RawResponse != test.body {
					t.Fatalf("empty response evidence = %#v, want %q", err, test.body)
				}
			}
		})
	}
	partial, parseErr := normalizeResponse(
		preparedRequest{provider: "fixture", protocol: "openai-compatible.chat.completions", callType: "structured"},
		[]byte(`{"choices":[{"message":{"content":"{\"answer\":\"\\uZZZZ\"}"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`),
	)
	if parseErr == nil || partial.Usage != (runtime.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}) {
		t.Fatalf("structured parse failure lost billable usage: %#v %v", partial, parseErr)
	}

	httpCases := []struct {
		status       int
		body         string
		wantCategory retry.Category
		wantRetry    bool
	}{
		{400, `{"error":{"message":"Bearer super-secret","type":"invalid_request_error"}}`, retry.CategoryOther, false},
		{429, `{"error":{"message":"Bearer super-secret","type":"rate_limit"}}`, retry.CategoryRateLimit, true},
		{503, `{"error":{"message":"Bearer super-secret","type":"service_unavailable"}}`, retry.CategoryServer, true},
	}
	for _, test := range httpCases {
		response := &http.Response{StatusCode: test.status, Header: http.Header{"Retry-After": {"2"}}}
		err := providerHTTPError(response, []byte(test.body))
		classification := retry.Classify(err, policy)
		if classification.Category != test.wantCategory || classification.Retryable != test.wantRetry {
			t.Fatalf("HTTP %d classification mismatch: %#v", test.status, classification)
		}
		if strings.Contains(err.Error(), "super-secret") || strings.Contains(err.Error(), "Bearer") {
			t.Fatalf("HTTP %d error leaked credential material: %v", test.status, err)
		}
	}
}

func TestProviderNormalizationNetworkAndTimeoutErrorsAreSafe(t *testing.T) {
	t.Parallel()
	operation := cachekey.Operation{Protocol: "openai.responses", Endpoint: cachekey.Endpoint{Identity: "https://api.example", Path: "/responses"}}
	parsedURL, _ := url.Parse("https://api.example/responses")
	prepared := preparedRequest{url: parsedURL, headers: http.Header{"Authorization": {"Bearer super-secret"}}, body: []byte(`{}`), protocol: operation.Protocol, operation: operation}

	networkRouter := &Router{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed with super-secret")
	})}}
	_, err := networkRouter.Execute(context.Background(), runtime.PreparedOperation{Operation: operation, Opaque: prepared})
	if err == nil || retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryNetwork {
		t.Fatalf("network error classification mismatch: %v", err)
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("network error leaked credential: %v", err)
	}

	timeoutRouter := &Router{client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err = timeoutRouter.Execute(ctx, runtime.PreparedOperation{Operation: operation, Opaque: prepared})
	if !errors.Is(err, context.DeadlineExceeded) || retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryTimeout {
		t.Fatalf("timeout classification mismatch: %v", err)
	}

	perRequest := prepared
	perRequest.timeout = 10 * time.Millisecond
	perRequestRouter := &Router{client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}}
	started := time.Now()
	_, err = perRequestRouter.Execute(context.Background(), runtime.PreparedOperation{Operation: operation, Opaque: perRequest})
	if err == nil || retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryTimeout || time.Since(started) > time.Second {
		t.Fatalf("provider option timeout was not bounded: elapsed=%s error=%v", time.Since(started), err)
	}
	for input, want := range map[any]time.Duration{float64(12.9): 12*time.Millisecond + 900*time.Microsecond, "15": 15 * time.Millisecond} {
		got, timeoutErr := requestTimeout(map[string]any{"timeout": input})
		if timeoutErr != nil || got != want {
			t.Fatalf("request timeout %#v = %s/%v, want %s", input, got, timeoutErr, want)
		}
	}
}

func TestProviderNormalizationCollectsResponsesEventStream(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"stream-"}`,
		`data: {"type":"response.output_text.done","text":"stream-ok"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2,"output_tokens":1}}}`,
		`data: [DONE]`,
	}, "\n\n")
	collected, err := collectResponsesEventStream([]byte(body))
	if err != nil {
		t.Fatalf("collectResponsesEventStream: %v", err)
	}
	result, err := normalizeResponse(preparedRequest{provider: "openai", protocol: "openai.responses", callType: "text"}, collected)
	if err != nil {
		t.Fatalf("normalizeResponse: %v", err)
	}
	if result.Output != "stream-ok" || result.Usage.TotalTokens != 3 {
		t.Fatalf("unexpected stream result: %#v", result)
	}
}

func TestProviderNormalizationParityClassifiesResponsesProviderRetryDirective(t *testing.T) {
	t.Parallel()
	fixtureBytes, err := os.ReadFile("../../fixtures/parity/source/providers/openai-responses-stream-retry-error.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		PositiveCase struct {
			Message  string `json:"message"`
			Expected struct {
				Code              string `json:"code"`
				ProviderRequestID string `json:"providerRequestId"`
			} `json:"expected"`
		} `json:"positiveCase"`
	}
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatal(err)
	}
	positive := fixture.PositiveCase.Message
	_, err = collectResponsesEventStream([]byte(
		`data: {"type":"response.output_text.delta","delta":"{\"stale\":"}` + "\n\n" +
			`data: {"type":"response.failed","error":{"message":"` + positive + `"}}` + "\n\n" +
			"data: [DONE]\n\n",
	))
	if err == nil {
		t.Fatal("expected provider retry directive to fail the stream")
	}
	providerErr, ok := err.(*retry.ProviderError)
	if !ok {
		t.Fatalf("error type = %T, want *retry.ProviderError", err)
	}
	classification := retry.Classify(err, retry.Policy{ServerError: false})
	if classification.Category != retry.CategoryProvider || !classification.Retryable || providerErr.Code != fixture.PositiveCase.Expected.Code ||
		providerErr.ProviderRequestID != fixture.PositiveCase.Expected.ProviderRequestID || providerErr.Status != 0 || providerErr.Type != "" {
		t.Fatalf("provider retry normalization = %#v / %#v", providerErr, classification)
	}

	structured := `data: {"type":"response.failed","error":{"message":"` + positive + `","status":503,"code":"server_error","type":"server_error"}}` + "\n\n"
	_, err = collectResponsesEventStream([]byte(structured))
	if err == nil {
		t.Fatal("expected structured provider failure")
	}
	providerErr, ok = err.(*retry.ProviderError)
	if !ok || providerErr.Code != "server_error" || providerErr.Type != "server_error" || providerErr.ProviderRequestID != "" || providerErr.Status != 503 ||
		retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryServer {
		t.Fatalf("structured provider failure = %#v / %#v", providerErr, retry.Classify(err, retry.DefaultPolicy()))
	}
}

func TestProviderNormalizationClassifiesResponsesIteratorReadDirective(t *testing.T) {
	t.Parallel()
	message := "An error occurred while processing your request. You can retry your request. Please include the request ID req_fixture_0002 in your message."
	operation := cachekey.Operation{
		Protocol: "openai.responses",
		Endpoint: cachekey.Endpoint{Identity: "https://api.example", Method: http.MethodPost, Path: "/v1/responses"},
	}
	parsedURL, err := url.Parse("https://api.example/v1/responses")
	if err != nil {
		t.Fatal(err)
	}
	router := &Router{
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body:       &errorReadCloser{reader: strings.NewReader(`data: {"type":"response.output_text.delta","delta":"{"}`), err: errors.New(message)},
			}, nil
		})},
		maxResponseBytes: defaultMaxResponseBytes,
	}
	_, err = router.Execute(context.Background(), runtime.PreparedOperation{
		Operation: operation,
		Opaque:    preparedRequest{url: parsedURL, headers: http.Header{}, body: []byte(`{}`), protocol: operation.Protocol, operation: operation},
	})
	if err == nil || retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryProvider {
		t.Fatalf("iterator read error = %v / %#v", err, retry.Classify(err, retry.DefaultPolicy()))
	}
	providerErr, ok := err.(*retry.ProviderError)
	if !ok || providerErr.Code != "provider_retry" || providerErr.ProviderRequestID != "req_fixture_0002" {
		t.Fatalf("iterator read provider error = %#v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type errorReadCloser struct {
	reader *strings.Reader
	err    error
}

func (reader *errorReadCloser) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	if count > 0 {
		return count, nil
	}
	if err == io.EOF {
		return 0, reader.err
	}
	return count, err
}

func (reader *errorReadCloser) Close() error { return nil }

func deepEqualJSON(left, right any) bool {
	leftJSON, _ := jsonMarshal(left)
	rightJSON, _ := jsonMarshal(right)
	return string(leftJSON) == string(rightJSON)
}

func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

func costNear(left, right runtime.Cost) bool {
	return left.Known == right.Known && left.Source == right.Source && math.Abs(left.TotalUSD-right.TotalUSD) < 1e-12
}
