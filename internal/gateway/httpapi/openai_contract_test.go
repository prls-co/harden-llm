package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-403

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
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
		// Reference /api/v1/run is restored by ADR-HLLM-033; TEST-441 owns its stronger contract.
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
	return httptest.NewServer(newContractAPI(t, upstream).Handler())
}

func newContractAPI(t *testing.T, upstream *httptest.Server) *API {
	t.Helper()
	return newContractAPIWithPool(t, upstream, nil)
}
func newContractAPIWithPool(t *testing.T, upstream *httptest.Server, pool *pgxpool.Pool) *API {
	t.Helper()
	service, err := stateful.NewService(stateful.Config{Pool: pool, Client: contractClientOptions(upstream)})
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(Config{APIKeys: []string{proxyTestToken}, Service: service})
	if err != nil {
		t.Fatal(err)
	}
	return api
}

func contractClientOptions(upstream *httptest.Server) stateful.ClientOptions {
	certificatePool := x509.NewCertPool()
	certificatePool.AddCert(upstream.Certificate())
	return stateful.ClientOptions{
		Connections: []hardenllm.Connection{{
			ID: "cpa", Provider: "fixture", Protocol: "responses", BaseURL: upstream.URL + "/v1",
			CacheDomain: "fixture-credential-1", APIKey: "synthetic-upstream-secret",
		}},
		DefaultConnection: "cpa",
		EndpointPolicy: hardenllm.EndpointPolicy{
			PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
			TLSConfig: &tls.Config{RootCAs: certificatePool, MinVersion: tls.VersionTLS12},
		},
	}
}

func postOrdinary(api *API, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+proxyTestToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)
	return w
}

type countedContractCache struct {
	projectionCache
	reads, writes int
}

func (c *countedContractCache) Get(ctx context.Context, key string) (hardenllm.CacheRecord, bool, error) {
	c.reads++
	return c.projectionCache.Get(ctx, key)
}
func (c *countedContractCache) Set(ctx context.Context, key string, record hardenllm.CacheRecord) error {
	c.writes++
	return c.projectionCache.Set(ctx, key, record)
}

func TestOpenAIContractFunctionCacheAdmission(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"completed","output":[{"type":"function_call","id":"fc_fixture","call_id":"call_fixture","name":"lookup","arguments":"{}"}]}`)
	}))
	defer upstream.Close()
	cache := &countedContractCache{projectionCache: projectionCache{records: map[string]hardenllm.CacheRecord{}}}
	opts := contractClientOptions(upstream)
	opts.Cache = cache
	client, err := stateful.NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	api := newContractAPI(t, upstream)
	api.clientForKey = func(string) (*hardenllm.Client, error) { return client, nil }
	_, err = client.Call(context.Background(), hardenllm.Request{CallType: hardenllm.CallTypeText, RecoveryPolicy: hardenllm.DefaultRecoveryPolicy(), ModelID: "fixture", Messages: []hardenllm.Message{{Role: "user", Content: json.RawMessage(`"hello"`)}}, Tools: []hardenllm.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}, CacheMode: hardenllm.CacheModeRefresh})
	if err != nil || cache.writes != 1 {
		t.Fatalf("seed function entry: writes=%d err=%v", cache.writes, err)
	}
	for _, tc := range []struct{ path, fields string }{
		{"/v1/responses", `"input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`},
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]`},
	} {
		for _, mode := range []string{"", "off", "cache", "refresh"} {
			options := ""
			if mode != "" {
				options = fmt.Sprintf(`,"harden":{"cache":%q}`, mode)
			}
			variants := []string{""}
			reject := mode == "cache" || mode == "refresh"
			if reject {
				variants = append(variants, `,"tool_choice":"none","stream":true`)
			}
			for _, extra := range variants {
				before, reads, writes := calls.Load(), cache.reads, cache.writes
				w := postOrdinary(api, tc.path, `{"model":"fixture",`+tc.fields+options+extra+"}")
				if reject {
					if w.Code != 400 || !strings.Contains(w.Body.String(), `"param":"harden.cache"`) || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || calls.Load() != before || reads != cache.reads || writes != cache.writes {
						t.Errorf("%s %s %s: HTTP %d, model/read/write %d/%d/%d", tc.path, mode, extra, w.Code, calls.Load()-before, cache.reads-reads, cache.writes-writes)
					}
					continue
				}
				if w.Code != 200 || calls.Load() != before+1 || reads != cache.reads || writes != cache.writes {
					t.Fatalf("uncached %s %s: %d %s", tc.path, mode, w.Code, w.Body.String())
				}
				if tc.path == "/v1/responses" {
					var res responses.Response
					if json.Unmarshal(w.Body.Bytes(), &res) != nil || len(res.Output) != 1 {
						t.Fatal(w.Body.String())
					}
					call := res.Output[0].AsFunctionCall()
					if call.ID != "fc_fixture" || call.CallID != "call_fixture" || call.Name != "lookup" || call.Arguments != "{}" {
						t.Fatal(call)
					}
				} else {
					var res openai.ChatCompletion
					if json.Unmarshal(w.Body.Bytes(), &res) != nil || len(res.Choices) != 1 || res.Choices[0].FinishReason != "tool_calls" || len(res.Choices[0].Message.ToolCalls) != 1 {
						t.Fatal(w.Body.String())
					}
					call := res.Choices[0].Message.ToolCalls[0]
					if call.ID != "call_fixture" || call.Function.Name != "lookup" || call.Function.Arguments != "{}" {
						t.Fatal(call)
					}
				}
			}
		}
	}
}

func TestOpenAIContractOrdinaryPreparation(t *testing.T) {
	for _, tc := range []struct{ extra, want string }{
		{`"store":false,"temperature":0,"max_output_tokens":128,"fixture_option":{"values":[null,false,9007199254740993]}`, `{"fixture_option":{"values":[null,false,9007199254740993]},"max_output_tokens":128,"store":false,"temperature":0}`},
		{`"text":{"verbosity":"low"}`, `{"text":{"verbosity":"low"}}`},
		{`"text":{"verbosity":"low","format":{"type":"json_object"}}`, `{"text":{"verbosity":"low"}}`},
		{`"reasoning":{"effort":"low","fixture_sibling":false}`, `{"reasoning":{"fixture_sibling":false}}`},
	} {
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"fixture","input":"hello","harden":{"cache":"off"},`+tc.extra+"}"))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		var input responsesRequest
		if !(&API{}).decodeRequest(w, r, &input) {
			t.Errorf("%s: %s", tc.extra, w.Body.String())
			continue
		}
		prepared, ok := prepareResponses(w, input)
		if !ok {
			t.Errorf("%s: %s", tc.extra, w.Body.String())
			continue
		}
		got, err := json.Marshal(prepared.Request.ProviderOptions)
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: options=%s, want %s (%v)", tc.extra, got, tc.want, err)
		}
	}
}

func TestOpenAIContractOrdinaryAdmission(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer upstream.Close()
	api := newContractAPI(t, upstream)
	cache := &countedContractCache{projectionCache: projectionCache{records: map[string]hardenllm.CacheRecord{}}}
	opts := contractClientOptions(upstream)
	opts.Connections[0].SupportsWebSearch = true
	opts.Cache = cache
	client, err := stateful.NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	api.clientForKey = func(string) (*hardenllm.Client, error) { return client, nil }
	for _, tc := range []struct{ path, input, extra, field string }{
		{"/v1/responses", `"input":"hello"`, `"reasoning":{"summary":"auto"}`, "reasoning.summary"},
		{"/v1/responses", `"input":"hello"`, `"reasoning":{"effort":"low","summary":"auto"}`, "reasoning.summary"},
		{"/v1/responses", `"input":"hello"`, `"reasoning":{"effort":"low"}`, ""},
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"hello"}]`, `"logprobs":true`, "logprobs"},
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"hello"}]`, `"top_logprobs":2`, "top_logprobs"},
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"hello"}]`, `"logprobs":false`, ""},
	} {
		before := calls.Load()
		extra := tc.extra
		if tc.field == "reasoning.summary" {
			extra += `,"tools":[{"type":"web_search"}],"harden":{"cache":"cache"}`
		}
		reads, writes := cache.reads, cache.writes
		w := postOrdinary(api, tc.path, `{"model":"fixture",`+tc.input+","+extra+"}")
		if tc.field != "" && (cache.reads != reads || cache.writes != writes) {
			t.Fatalf("%s reached cache", tc.field)
		}
		if tc.field == "" {
			if w.Code != 200 || calls.Load() != before+1 {
				t.Errorf("%s: HTTP %d %s", tc.extra, w.Code, w.Body.String())
			}
		} else if w.Code != 400 || !strings.Contains(w.Body.String(), `"param":"`+tc.field+`"`) || calls.Load() != before {
			t.Errorf("%s: HTTP %d, calls %d, %s", tc.extra, w.Code, calls.Load()-before, w.Body.String())
		}
	}
	for _, field := range []string{"timeout", "cacheMode", "useResponsesApi", "__harden_llm_web_search", "maxRetries", "api_key", "baseURL", "headers"} {
		before := calls.Load()
		w := postOrdinary(api, "/v1/responses", fmt.Sprintf(`{"model":"fixture","input":"hello",%q:"opaque-sensitive-value"}`, field))
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"param":"`+field+`"`) || strings.Contains(w.Body.String(), "opaque-sensitive-value") || calls.Load() != before {
			t.Errorf("%s: HTTP %d %s", field, w.Code, w.Body.String())
		}
	}
}

func TestOpenAIContractNativeOptionWire(t *testing.T) {
	for _, protocol := range []string{"responses", "chat-completions"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int32
			var received atomic.Value
			path, input, limit := "/v1/responses", `"input":"hello"`, "max_output_tokens"
			if protocol == "chat-completions" {
				path, input, limit = "/v1/chat/completions", `"messages":[{"role":"user","content":"hello"}]`, "max_completion_tokens"
			}
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.URL.Path != path {
					t.Errorf("unexpected upstream %s %s", r.Method, r.URL.Path)
				}
				var captured map[string]any
				decoder := json.NewDecoder(r.Body)
				decoder.UseNumber()
				if err := decoder.Decode(&captured); err != nil {
					t.Error(err)
				}
				received.Store(captured)
				w.Header().Set("Content-Type", "application/json")
				if protocol == "responses" {
					io.WriteString(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"answer\":\"ok\"}"}]}]}`)
				} else {
					io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}]}`)
				}
			}))
			defer upstream.Close()
			api := newContractAPI(t, upstream)
			opts := contractClientOptions(upstream)
			opts.Connections[0].Protocol = protocol
			if protocol == "responses" {
				opts.Connections[0].Provider = "cpa"
			}
			client, err := stateful.NewClient(opts)
			if err != nil {
				t.Fatal(err)
			}
			api.clientForKey = func(string) (*hardenllm.Client, error) { return client, nil }
			for _, store := range []string{"", `,"store":false`, `,"store":true`} {
				before := calls.Load()
				w := postOrdinary(api, path, fmt.Sprintf(`{"model":"fixture",%s,%q:128,"temperature":0,"fixture_option":{"values":[null,false,9007199254740993]},"harden":{"cache":"off"}%s}`, input, limit, store))
				if store == `,"store":true` {
					if w.Code != 400 || !strings.Contains(w.Body.String(), `"param":"store"`) || calls.Load() != before {
						t.Errorf("store true: %d %s", w.Code, w.Body.String())
					}
					continue
				}
				if w.Code != 200 || calls.Load() != before+1 {
					t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
				}
				captured := received.Load().(map[string]any)
				v, present := captured["store"]
				opaque, _ := json.Marshal(captured["fixture_option"])
				if present != (store != "") || present && v != false || captured[limit] != json.Number("128") || captured["temperature"] != json.Number("0") || string(opaque) != `{"values":[null,false,9007199254740993]}` {
					t.Errorf("wire = %v", captured)
				}
				if _, ok := captured["harden"]; ok {
					t.Fatal("harden reached upstream")
				}
			}
			// These are opaque caller data, including a key name often used in schemas.
			large := strings.Repeat("x", 33<<10)
			w := postOrdinary(api, path, fmt.Sprintf(`{"model":"fixture",%s,"metadata":{"secret":%q}}`, input, large))
			if w.Code != 200 {
				t.Fatalf("native metadata HTTP %d: %s", w.Code, w.Body.String())
			}
			metadata, _ := received.Load().(map[string]any)["metadata"].(map[string]any)
			if metadata["secret"] != large {
				t.Fatal("opaque nested metadata was altered")
			}
			if protocol == "responses" {
				for _, format := range []string{"", `,"format":{"type":"json_schema","name":"answer","schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}}`} {
					w := postOrdinary(api, path, `{"model":"fixture","input":"hello","text":{"verbosity":"low"`+format+`},"reasoning":{"effort":"low","fixture_sibling":true}}`)
					captured := received.Load().(map[string]any)
					text, _ := captured["text"].(map[string]any)
					reasoning, _ := captured["reasoning"].(map[string]any)
					f, hasFormat := text["format"].(map[string]any)
					if w.Code != 200 || text["verbosity"] != "low" || hasFormat != (format != "") || hasFormat && f["type"] != "json_schema" || reasoning["effort"] != "low" || reasoning["fixture_sibling"] != true {
						t.Errorf("HTTP %d: %s text=%v reasoning=%v", w.Code, w.Body.String(), text, reasoning)
					}
				}
			}
		})
	}
}

func TestOpenAIContractRecoveryOptionWire(t *testing.T) {
	for _, tc := range []struct{ name, opts, limit, protocol, field string }{
		{"inherit", "", "128", "responses", ""},
		{"override", `,"providerOptions":{"max_output_tokens":512,"temperature":0.5,"text":{"verbosity":"high"},"reasoning":{"effort":"medium"}}`, "512", "responses", ""},
		{"foreign target", "", "", "gemini-generate-content", "store"},
		{"protected storage", `,"providerOptions":{"store":true}`, "", "responses", "harden.recovery.jsonRepair.initial.providerOptions.store"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bodies := make(chan map[string]any, 4)
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				d := json.NewDecoder(r.Body)
				d.UseNumber()
				if err := d.Decode(&body); err != nil {
					t.Error(err)
					return
				}
				bodies <- body
				w.Header().Set("Content-Type", "application/json")
				answer := `{"ok":true}`
				if body["model"] == "first" {
					answer = `{"ok":"invalid"}`
				}
				fmt.Fprintf(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":%q}]}],"usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}`, answer)
			}))
			defer upstream.Close()
			opts := contractClientOptions(upstream)
			target := opts.Connections[0]
			target.ID = "second"
			target.Protocol = tc.protocol
			opts.Connections = append(opts.Connections, target)
			client, err := stateful.NewClient(opts)
			if err != nil {
				t.Fatal(err)
			}
			api := newContractAPI(t, upstream)
			api.clientForKey = func(string) (*hardenllm.Client, error) { return client, nil }
			reasoning := ""
			if tc.protocol == "responses" {
				reasoning = `,"reasoning":{"effort":"low","fixture_sibling":true}`
			}
			textSibling := ""
			if tc.protocol == "responses" {
				textSibling = `"verbosity":"low",`
			}
			w := postOrdinary(api, "/v1/responses", `{"model":"first","input":"hello","store":false,"max_output_tokens":128,"temperature":0`+reasoning+`,"text":{`+textSibling+`"format":{"type":"json_schema","name":"answer","schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}},"harden":{"diagnostics":true,"recovery":{"maxAttempts":2,"retryOn":[],"backoff":{"baseDelayMs":0,"maxDelayMs":0},"jsonRepair":{"initial":{"source":"model","upstream":"second","modelId":"second"`+tc.opts+`},"escalation":null},"rerun":null}}}`)
			if tc.name == "protected storage" {
				if w.Code != 400 || len(bodies) != 0 || !strings.Contains(w.Body.String(), `"param":"`+tc.field+`"`) {
					t.Fatalf("%d calls %d: %s", w.Code, len(bodies), w.Body.String())
				}
				return
			}
			if tc.field != "" {
				if w.Code != 400 || len(bodies) != 1 || !strings.Contains(w.Body.String(), `"param":"`+tc.field+`"`) {
					t.Fatalf("%d calls %d: %s", w.Code, len(bodies), w.Body.String())
				}
				var failure struct {
					Harden struct {
						Accounting struct {
							Provider struct {
								Usage struct {
									InputTokens int `json:"inputTokens"`
								}
							}
						}
					}
				}
				if json.Unmarshal(w.Body.Bytes(), &failure) != nil || failure.Harden.Accounting.Provider.Usage.InputTokens != 4 {
					t.Fatalf("lost accounting: %s", w.Body.String())
				}
				return
			}
			if w.Code != 200 || len(bodies) != 2 {
				t.Fatalf("%d, %d calls: %s", w.Code, len(bodies), w.Body.String())
			}
			for i, want := range []string{"128", tc.limit} {
				got := <-bodies
				wantEffort := "low"
				wantVerbosity := "low"
				text, _ := got["text"].(map[string]any)
				reasoning, _ := got["reasoning"].(map[string]any)
				wantTemperature := json.Number("0")
				if i == 1 && tc.name == "override" {
					wantTemperature = json.Number("0.5")
					wantEffort = "medium"
					wantVerbosity = "high"
				}
				if got["max_output_tokens"] != json.Number(want) || got["store"] != false || got["temperature"] != wantTemperature || reasoning["effort"] != wantEffort || reasoning["fixture_sibling"] != true || text["verbosity"] != wantVerbosity {
					t.Errorf("attempt %d options: %v", i+1, got)
				}
			}
		})
	}
}
