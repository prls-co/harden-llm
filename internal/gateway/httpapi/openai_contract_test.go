package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-403

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"github.com/prls-co/harden-llm"
)

const proxyTestToken = "synthetic-harden-token-0123456789abcdef"

func TestOpenAIContractOfficialSDK(t *testing.T) {
	var dispatched atomic.Int32
	var upstreamAuth atomic.Value
	provider := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/models" {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"object":"list","data":[{"id":"fixture-model","object":"model","owned_by":"fixture"}]}`)
			return
		}
		if request.URL.Path != "/v1/responses" {
			http.NotFound(writer, request)
			return
		}
		dispatched.Add(1)
		upstreamAuth.Store(request.Header.Get("Authorization"))
		var input struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		if len(input.Tools) > 0 {
			_, _ = io.WriteString(writer, `{"id":"upstream-response","status":"completed","output":[{"type":"function_call","id":"fc_fixture","call_id":"call_fixture","name":"lookup","arguments":"{\"query\":\"hello\"}"}],"usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}`)
			return
		}
		_, _ = io.WriteString(writer, `{"id":"upstream-response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"fixture answer"}]}],"output_text":"fixture answer","usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}`)
	}))
	defer provider.Close()

	proxy := newContractProxy(t, provider)
	defer proxy.Close()
	client := openai.NewClient(
		option.WithBaseURL(proxy.URL+"/v1"), option.WithAPIKey(proxyTestToken), option.WithMaxRetries(0),
	)

	chat, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model: "fixture-model",
		Messages: []openai.ChatCompletionMessageParamUnion{{OfUser: &openai.ChatCompletionUserMessageParam{
			Content: openai.ChatCompletionUserMessageParamContentUnion{OfString: openai.String("hello")},
		}}},
	})
	if err != nil {
		t.Fatalf("official Chat Completions SDK call: %v", err)
	}
	if chat.Object != "chat.completion" || len(chat.Choices) != 1 || chat.Choices[0].Message.Content != "fixture answer" || chat.Choices[0].FinishReason != "stop" {
		t.Fatalf("Chat Completions response did not match the SDK contract: %#v", chat)
	}
	if chat.Usage.TotalTokens != 7 {
		t.Fatalf("complete upstream accounting was not exposed: %#v", chat.Usage)
	}

	response, err := client.Responses.New(context.Background(), responses.ResponseNewParams{
		Model: "fixture-model", Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("hello")},
	})
	if err != nil {
		t.Fatalf("official Responses SDK call: %v", err)
	}
	if response.Object != "response" || response.Status != "completed" || response.OutputText() != "fixture answer" {
		t.Fatalf("Responses response did not match the SDK contract: %#v", response)
	}
	if dispatched.Load() != 2 || upstreamAuth.Load() != "Bearer synthetic-upstream-secret" {
		t.Fatalf("upstream dispatch/auth = %d, %#v", dispatched.Load(), upstreamAuth.Load())
	}
}

func TestOpenAIContractFunctionToolsAndFinalOnlyStreams(t *testing.T) {
	var dispatched atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			http.NotFound(writer, request)
			return
		}
		dispatched.Add(1)
		var input struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		if len(input.Tools) > 0 {
			_, _ = io.WriteString(writer, `{"status":"completed","output":[{"type":"function_call","id":"fc_fixture","call_id":"call_fixture","name":"lookup","arguments":"{\"query\":\"hello\"}"}]}`)
			return
		}
		_, _ = io.WriteString(writer, `{"status":"completed","output_text":"buffered answer","output":[{"type":"message","content":[{"type":"output_text","text":"buffered answer"}]}]}`)
	}))
	defer provider.Close()
	proxy := newContractProxy(t, provider)
	defer proxy.Close()
	client := openai.NewClient(option.WithBaseURL(proxy.URL+"/v1"), option.WithAPIKey(proxyTestToken), option.WithMaxRetries(0))
	function := openai.ChatCompletionToolParam{
		Type: "function",
		Function: shared.FunctionDefinitionParam{Name: "lookup", Parameters: shared.FunctionParameters{
			"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}, "additionalProperties": false,
		}},
	}
	completion, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model: "fixture-model", Messages: []openai.ChatCompletionMessageParamUnion{{OfUser: &openai.ChatCompletionUserMessageParam{
			Content: openai.ChatCompletionUserMessageParamContentUnion{OfString: openai.String("hello")},
		}}}, Tools: []openai.ChatCompletionToolParam{function},
	})
	if err != nil {
		t.Fatalf("tool request through SDK: %v", err)
	}
	if len(completion.Choices) != 1 || len(completion.Choices[0].Message.ToolCalls) != 1 || completion.Choices[0].Message.ToolCalls[0].ID != "call_fixture" || completion.Choices[0].Message.ToolCalls[0].Function.Name != "lookup" {
		t.Fatalf("function call IDs or shape changed: %#v", completion)
	}

	chatStream := client.Chat.Completions.NewStreaming(context.Background(), openai.ChatCompletionNewParams{
		Model: "fixture-model", Messages: []openai.ChatCompletionMessageParamUnion{{OfUser: &openai.ChatCompletionUserMessageParam{
			Content: openai.ChatCompletionUserMessageParamContentUnion{OfString: openai.String("hello")},
		}}},
	})
	var streamText strings.Builder
	for chatStream.Next() {
		streamText.WriteString(chatStream.Current().Choices[0].Delta.Content)
	}
	if err := chatStream.Err(); err != nil || streamText.String() != "buffered answer" {
		t.Fatalf("final-only Chat stream = %q, %v", streamText.String(), err)
	}

	responseStream := client.Responses.NewStreaming(context.Background(), responses.ResponseNewParams{
		Model: "fixture-model", Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("hello")},
	})
	var textDelta strings.Builder
	var terminalEvents int
	for responseStream.Next() {
		event := responseStream.Current()
		switch event.Type {
		case "response.output_text.delta":
			textDelta.WriteString(event.AsResponseOutputTextDelta().Delta)
		case "response.completed":
			terminalEvents++
		}
	}
	if err := responseStream.Err(); err != nil || textDelta.String() != "buffered answer" || terminalEvents != 1 {
		t.Fatalf("final-only Responses stream = %q, terminal=%d, err=%v", textDelta.String(), terminalEvents, err)
	}
	if dispatched.Load() != 3 {
		t.Fatalf("provider dispatches = %d, want one per SDK operation", dispatched.Load())
	}
}

func TestOpenAIContractAuthStateAndRetiredRoutes(t *testing.T) {
	var dispatched atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		dispatched.Add(1)
		_, _ = io.WriteString(writer, `{"status":"completed","output_text":"ok","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer provider.Close()
	proxy := newContractProxy(t, provider)
	defer proxy.Close()

	for _, test := range []struct {
		name   string
		path   string
		method string
		header string
		body   string
		status int
		param  string
	}{
		{name: "invalid token", path: "/v1/responses", method: http.MethodPost, header: "Bearer wrong", body: `{"model":"fixture-model","input":"hello"}`, status: http.StatusUnauthorized},
		{name: "stateful response", path: "/v1/responses", method: http.MethodPost, header: "Bearer " + proxyTestToken, body: `{"model":"fixture-model","input":"hello","store":true}`, status: http.StatusBadRequest},
		{name: "unreturned response include", path: "/v1/responses", method: http.MethodPost, header: "Bearer " + proxyTestToken, body: `{"model":"fixture-model","input":"hello","include":["reasoning.encrypted_content"]}`, status: http.StatusBadRequest, param: "include"},
		{name: "unsupported upstream sampling field", path: "/v1/chat/completions", method: http.MethodPost, header: "Bearer " + proxyTestToken, body: `{"model":"fixture-model","messages":[{"role":"user","content":"hello"}],"frequency_penalty":0.5}`, status: http.StatusBadRequest, param: "frequency_penalty"},
		{name: "temperature outside OpenAI range", path: "/v1/chat/completions", method: http.MethodPost, header: "Bearer " + proxyTestToken, body: `{"model":"fixture-model","messages":[{"role":"user","content":"hello"}],"temperature":2.1}`, status: http.StatusBadRequest, param: "temperature"},
		{name: "stop is not a string list", path: "/v1/chat/completions", method: http.MethodPost, header: "Bearer " + proxyTestToken, body: `{"model":"fixture-model","messages":[{"role":"user","content":"hello"}],"stop":{"x":1}}`, status: http.StatusBadRequest, param: "stop"},
		{name: "retired native run API", path: "/api/v1/run", method: http.MethodPost, header: "Bearer " + proxyTestToken, body: `{}`, status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(test.method, proxy.URL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", test.header)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.status {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status=%d body=%s, want %d", response.StatusCode, body, test.status)
			}
			var body map[string]any
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			errorObject, ok := body["error"].(map[string]any)
			if !ok {
				t.Fatalf("error response did not use OpenAI shape: %#v", body)
			}
			if test.param != "" && errorObject["param"] != test.param {
				t.Fatalf("error param = %#v, want %q", errorObject["param"], test.param)
			}
		})
	}
	if dispatched.Load() != 0 {
		t.Fatalf("rejected requests dispatched to upstream %d times", dispatched.Load())
	}
}

func newContractProxy(t *testing.T, upstream *httptest.Server) *httptest.Server {
	t.Helper()
	certificatePool := x509.NewCertPool()
	certificatePool.AddCert(upstream.Certificate())
	client, err := hardenllm.New(hardenllm.Options{
		Connections: []hardenllm.Connection{{
			ID: "cpa", Provider: "fixture", Protocol: "responses", BaseURL: upstream.URL + "/v1",
			CacheDomain: "fixture-credential-1", APIKey: "synthetic-upstream-secret",
		}},
		DefaultConnection: "cpa",
		EndpointPolicy: hardenllm.EndpointPolicy{
			PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
			TLSConfig: &tls.Config{RootCAs: certificatePool, MinVersion: tls.VersionTLS12},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(Config{Token: proxyTestToken, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(api.Handler())
}
