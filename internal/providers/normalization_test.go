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

	"github.com/prls-co/harden-llm/internal/accounting"
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
			body:       `{"status":"completed","output_text":"responses-ok","usage":{"input_tokens":20,"input_tokens_details":{"cached_tokens":4},"output_tokens":8,"output_tokens_details":{"reasoning_tokens":3}}}`,
			wantOutput: "responses-ok", wantUsage: completeProviderUsage(16, 4, 0, 5, 3),
			wantCost: accounting.ExactCost(0.0354, "profile"),
		},
		{
			name: "OpenAI-compatible structured", protocol: "openai-compatible.chat.completions", callType: "structured",
			body:       `{"choices":[{"message":{"content":"{\"answer\":\"chat-ok\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"cost":0.125}}`,
			wantOutput: map[string]any{"answer": "chat-ok"}, wantUsage: completeProviderUsage(3, 0, 0, 2, 0),
			wantCost: accounting.ExactCost(0.125, "reported"),
		},
		{
			name: "Gemini", protocol: "google.gemini.generateContent", callType: "text",
			body:       `{"candidates":[{"content":{"parts":[{"text":"gemini-"},{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":2,"candidatesTokenCount":4,"thoughtsTokenCount":1}}`,
			wantOutput: "gemini-", wantUsage: completeProviderUsage(8, 2, 0, 4, 1),
			wantCost: accounting.ExactCost(0.0192, "profile"),
		},
		{
			name: "OpenAI nullish usage precedence", protocol: "openai.responses", callType: "text",
			body:       `{"status":"completed","output_text":"ok","usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":0},"cached_tokens":5,"output_tokens":4,"output_tokens_details":{"reasoning_tokens":0},"reasoning_tokens":3}}`,
			wantOutput: "ok", wantUsage: completeProviderUsage(10, 0, 0, 4, 0),
			wantCost: accounting.ExactCost(0.018, "profile"),
		},
		{
			name: "Anthropic", protocol: "anthropic.messages", callType: "text",
			body:       `{"content":[{"type":"text","text":"anthropic-"},{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"cache_read_input_tokens":2,"output_tokens":3}}`,
			wantOutput: "anthropic-ok", wantUsage: completeProviderUsage(5, 2, 0, 3, 0),
			wantCost: accounting.ExactCost(0.0112, "profile"),
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
			if result.Accounting.Usage != test.wantUsage {
				t.Fatalf("usage mismatch: got %#v want %#v", result.Accounting.Usage, test.wantUsage)
			}
			if !costNear(result.Accounting.Cost, test.wantCost) {
				t.Fatalf("cost mismatch: got %#v want %#v", result.Accounting.Cost, test.wantCost)
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
					responsePayload := variant.normalized.ResponsePayload
					if variant.operation.Protocol == "openai.responses" {
						var response map[string]any
						if err := json.Unmarshal(responsePayload, &response); err != nil {
							t.Fatalf("decode captured Responses payload: %v", err)
						}
						response["status"] = "completed"
						annotated, marshalErr := json.Marshal(response)
						if marshalErr != nil {
							t.Fatalf("annotate captured Responses payload: %v", marshalErr)
						}
						responsePayload = annotated
					}
					result, normalizeErr := normalizeResponse(prepared, responsePayload)
					if normalizeErr != nil {
						t.Fatalf("normalizeResponse: %v", normalizeErr)
					}
					if !deepEqualJSON(result.Output, variant.normalized.Result) {
						t.Fatalf("output mismatch: got %#v want %#v", result.Output, variant.normalized.Result)
					}
					wantUsage := completeProviderUsage(
						rates[pricing.ItemInput].Tokens, rates[pricing.ItemCacheRead].Tokens,
						rates[pricing.ItemCacheCreation].Tokens, rates[pricing.ItemOutput].Tokens,
						rates[pricing.ItemReasoning].Tokens,
					)
					if result.Accounting.Usage != wantUsage {
						t.Fatalf("usage mismatch: got %#v want %#v", result.Accounting.Usage, wantUsage)
					}
					summary, summaryErr := pricing.Summarize(variant.normalized.Usage)
					if summaryErr != nil || summary.TotalCost == nil || result.Accounting.Cost.Status != accounting.CostExact || math.Abs(result.Accounting.Cost.KnownSubtotalUSD-*summary.TotalCost) > 1e-12 {
						t.Fatalf("cost mismatch: got %#v source=%#v error=%v", result.Accounting.Cost, summary, summaryErr)
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
		{"malformed", "openai.responses", `{not-json`, retry.CategoryOther},
		{"structured malformed", "openai-compatible.chat.completions", `{"choices":[{"finish_reason":"stop","message":{"content":"{\"answer\":\"\\uZZZZ\"}"}}]}`, retry.CategoryParse},
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
	if parseErr == nil || partial.Accounting.Usage != completeProviderUsage(3, 0, 0, 2, 0) {
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
	if err == nil || retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryNetwork || time.Since(started) > time.Second {
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
		`data: {"type":"response.completed","response":{"status":"completed","output_text":"stream-ok","usage":{"input_tokens":2,"output_tokens":1}}}`,
		`data: [DONE]`,
	}, "\n\n")
	collected, err := collectResponsesEventStream([]byte(body))
	if err != nil {
		t.Fatalf("collectResponsesEventStream: %v", err)
	}
	result, err := normalizeDecodedResponse(preparedRequest{provider: "openai", protocol: "openai.responses", callType: "text"}, collected)
	if err != nil {
		t.Fatalf("normalizeResponse: %v", err)
	}
	if result.Output != "stream-ok" || result.Accounting.Usage.TotalTokens() != 3 {
		t.Fatalf("unexpected stream result: %#v", result)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-216
func TestRecoveryBoundaryCompletion(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		body     string
		wantKind retry.Category
		wantCode string
	}{
		{name: "Responses delta only is interrupted", body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", wantKind: retry.CategoryNetwork, wantCode: "STREAM_TERMINAL_REQUIRED"},
		{name: "Responses done without terminal is interrupted", body: "data: {\"type\":\"response.output_text.done\",\"text\":\"partial\"}\n\n", wantKind: retry.CategoryNetwork, wantCode: "STREAM_TERMINAL_REQUIRED"},
		{name: "Responses incomplete is terminal rejection", body: `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output_text":"looks complete"}`, wantKind: retry.CategoryOther, wantCode: "COMPLETION_INCOMPLETE"},
		{name: "Responses missing status is rejected", body: `{"output_text":"looks complete"}`, wantKind: retry.CategoryOther, wantCode: "COMPLETION_REQUIRED"},
		{name: "Responses malformed output is terminal rejection", body: `{"status":"completed","output":123}`, wantKind: retry.CategoryOther, wantCode: "OUTPUT_MALFORMED"},
		{name: "Responses malformed secondary output is not hidden", body: `{"status":"completed","output_text":"ok","output":123}`, wantKind: retry.CategoryOther, wantCode: "OUTPUT_MALFORMED"},
		{name: "Chat length is terminal rejection", body: `{"choices":[{"finish_reason":"length","message":{"content":"partial"}}]}`, wantKind: retry.CategoryOther, wantCode: "COMPLETION_LIMIT"},
		{name: "Chat malformed content is terminal rejection", body: `{"choices":[{"finish_reason":"stop","message":{"content":[]}}]}`, wantKind: retry.CategoryOther, wantCode: "OUTPUT_MALFORMED"},
		{name: "Gemini max tokens is terminal rejection", body: `{"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[{"text":"partial"}]}}]}`, wantKind: retry.CategoryOther, wantCode: "COMPLETION_LIMIT"},
		{name: "Gemini malformed content is terminal rejection", body: `{"candidates":[{"finishReason":"STOP","content":"partial"}]}`, wantKind: retry.CategoryOther, wantCode: "OUTPUT_MALFORMED"},
		{name: "Anthropic max tokens is terminal rejection", body: `{"stop_reason":"max_tokens","content":[{"type":"text","text":"partial"}]}`, wantKind: retry.CategoryOther, wantCode: "COMPLETION_LIMIT"},
		{name: "Anthropic malformed content is terminal rejection", body: `{"stop_reason":"end_turn","content":{}}`, wantKind: retry.CategoryOther, wantCode: "OUTPUT_MALFORMED"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared := preparedRequest{provider: "fixture", protocol: "openai.responses", callType: "text"}
			if strings.HasPrefix(test.name, "Chat") {
				prepared.protocol = "openai-compatible.chat.completions"
			} else if strings.HasPrefix(test.name, "Gemini") {
				prepared.protocol = "google.gemini.generateContent"
			} else if strings.HasPrefix(test.name, "Anthropic") {
				prepared.protocol = "anthropic.messages"
			}
			var err error
			if strings.HasPrefix(test.name, "Responses ") && strings.Contains(test.name, "only") || strings.Contains(test.name, "without terminal") {
				_, err = collectResponsesEventStream([]byte(test.body))
			} else {
				_, err = normalizeResponse(prepared, []byte(test.body))
			}
			if err == nil {
				t.Fatal("completion failure was accepted")
			}
			classification := retry.Classify(err, retry.DefaultPolicy())
			if classification.Category != test.wantKind || classification.Code != test.wantCode {
				t.Fatalf("completion classification = %#v, want %s/%s", classification, test.wantKind, test.wantCode)
			}
		})
	}

	collected, err := collectResponsesEventStream([]byte(strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"stale"}`,
		`data: {"type":"response.completed","response":{"status":"completed","output_text":"authoritative"}}`,
	}, "\n\n")))
	if err != nil {
		t.Fatal(err)
	}
	result, err := normalizeDecodedResponse(preparedRequest{provider: "fixture", protocol: "openai.responses", callType: "text"}, collected)
	if err != nil || result.Output != "authoritative" {
		t.Fatalf("terminal output authority = %#v / %v", result, err)
	}

	structured, err := normalizeResponse(preparedRequest{provider: "fixture", protocol: "openai-compatible.chat.completions", callType: "structured"}, []byte(`{"choices":[{"finish_reason":"stop","message":{"content":"{not-json"}}]}`))
	if err == nil || retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryParse || structured.Output != nil {
		t.Fatalf("completed invalid structured output = %#v / %v", structured, err)
	}

	_, err = collectResponsesEventStream([]byte(`data: {"type":"response.completed" ,"response":"invalid"}`))
	if err == nil || retry.Classify(err, retry.DefaultPolicy()).Code != "COMPLETION_MALFORMED" {
		t.Fatalf("malformed completed event = %v", err)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-217
func TestRecoveryBoundaryAccountingCache(t *testing.T) {
	t.Parallel()
	pricingInput, pricingOutput := 0.001, 0.002
	pricing := runtime.Pricing{Input: &pricingInput, Output: &pricingOutput}
	prepared := preparedRequest{provider: "fixture", protocol: "openai.responses", callType: "text", pricing: pricing}

	failed, err := normalizeResponse(prepared, []byte(`{"status":"completed","output_text":"","usage":{"input_tokens":11,"output_tokens":3,"cost":0.1}}`))
	if err == nil || retry.Classify(err, retry.DefaultPolicy()).Category != retry.CategoryEmpty {
		t.Fatalf("completed empty response = %#v / %v", failed, err)
	}
	if failed.Accounting.Usage.Status != accounting.UsageComplete || failed.Accounting.Usage.PromptTokens() != 11 || failed.Accounting.Usage.CompletionTokens() != 3 || failed.Accounting.Cost != accounting.ExactCost(0.1, "reported") {
		t.Fatalf("failed response accounting = %#v", failed.Accounting)
	}

	partial, err := normalizeResponse(prepared, []byte(`{"status":"completed","output_text":"known","usage":{"input_tokens":11}}`))
	if err != nil || partial.Accounting.Usage.Status != accounting.UsagePartial || partial.Accounting.Cost.Status != accounting.CostPartial {
		t.Fatalf("partial accounting = %#v / %v", partial, err)
	}

	for _, body := range []string{
		`{"status":"completed","output_text":"ok","usage":{"input_tokens":-1,"output_tokens":2}}`,
		`{"status":"completed","output_text":"ok","usage":{"input_tokens":1.5,"output_tokens":2}}`,
		`{"status":"completed","output_text":"ok","usage":{"input_tokens":2,"input_tokens_details":{"cached_tokens":3},"output_tokens":2}}`,
		`{"status":"completed","output_text":"ok","usage":{"input_tokens":2,"input_tokens_details":123,"output_tokens":2}}`,
		`{"status":"completed","output_text":"ok","usage":{"input_tokens":null,"output_tokens":2}}`,
		`{"status":"completed","output_text":"ok","usage":null}`,
	} {
		result, normalizeErr := normalizeResponse(prepared, []byte(body))
		if normalizeErr == nil || retry.Classify(normalizeErr, retry.DefaultPolicy()).Code != "ACCOUNTING_INVALID" || result.Accounting.Usage.Status == accounting.UsageInconsistent {
			t.Fatalf("invalid accounting accepted: result=%#v error=%v", result, normalizeErr)
		}
	}

	reportedPartial, err := normalizeResponse(prepared, []byte(`{"status":"completed","output_text":"ok","usage":{"input_tokens":11,"cost":0.1}}`))
	if err != nil || reportedPartial.Accounting.Usage.Status != accounting.UsagePartial || reportedPartial.Accounting.Cost != accounting.ExactCost(0.1, "reported") {
		t.Fatalf("reported exact cost with partial usage = %#v / %v", reportedPartial.Accounting, err)
	}

	maxValue, err := normalizeResponse(prepared, []byte(`{"status":"completed","output_text":"ok","usage":{"input_tokens":9223372036854775807.0,"output_tokens":0}}`))
	if err != nil || maxValue.Accounting.Usage.InputTokens != math.MaxInt64 {
		t.Fatalf("maximum integral token count = %#v / %v", maxValue.Accounting.Usage, err)
	}
	overflow, err := normalizeResponse(prepared, []byte(`{"status":"completed","output_text":"ok","usage":{"input_tokens":9223372036854775808.0,"output_tokens":0}}`))
	if err == nil || retry.Classify(err, retry.DefaultPolicy()).Code != "ACCOUNTING_INVALID" || overflow.Accounting.Usage.Status == accounting.UsageInconsistent {
		t.Fatalf("overflow token count accepted: %#v / %v", overflow, err)
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
	classification := retry.Classify(err, retry.Policy{RetryOn: []retry.Category{retry.CategoryProvider}})
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
	return left.Status == right.Status && left.Source == right.Source &&
		left.KnownObservations == right.KnownObservations && left.UnknownObservations == right.UnknownObservations &&
		math.Abs(left.KnownSubtotalUSD-right.KnownSubtotalUSD) < 1e-12
}

func completeProviderUsage(input, cacheRead, cacheCreation, output, reasoning int64) runtime.Usage {
	usage, err := accounting.CompleteUsage(input, cacheRead, cacheCreation, output, reasoning)
	if err != nil {
		panic(err)
	}
	return usage
}
