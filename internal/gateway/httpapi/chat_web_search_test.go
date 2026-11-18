package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-534 TEST-537

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/api"
	"github.com/prls-co/harden-llm/internal/diagnostics"
	"github.com/prls-co/harden-llm/stateful"
)

func TestChatWebSearchNative(t *testing.T) {
	fixture, err := os.ReadFile("../../providers/testdata/web_search/openai_responses.json")
	if err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	bodies := make(chan map[string]any, 4)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		count.Add(1)
		bodies <- input
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	}))
	defer upstream.Close()
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	service, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "native", Provider: "openai", Protocol: "responses", BaseURL: upstream.URL + "/v1", CacheDomain: "test", APIKey: "synthetic", SupportsWebSearch: true}}, DefaultConnection: "native", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Config{APIKeys: []string{proxyTestToken}, Service: service})
	if err != nil {
		t.Fatal(err)
	}
	send := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+proxyTestToken)
		w := httptest.NewRecorder()
		handler.Handler().ServeHTTP(w, req)
		return w
	}
	w := send(`{"model":"fixture","messages":[{"role":"user","content":"hello"}],"web_search_options":{"search_context_size":"low","user_location":{"type":"approximate","approximate":{"country":"US"}}}}`)
	if w.Code != 200 {
		t.Fatalf("search failed: status=%d body=%s", w.Code, w.Body.String())
	}
	if err := api.Validate("ChatCompletion", w.Body.Bytes()); err != nil {
		t.Fatalf("chat response violates OpenAPI: %v", err)
	}
	if err := api.Validate("ChatCompletionRequest", []byte(`{"model":"fixture","messages":[{"role":"user","content":"hello"}],"web_search_options":{}}`)); err != nil {
		t.Fatalf("chat request violates OpenAPI: %v", err)
	}
	upstreamBody := <-bodies
	tools, ok := upstreamBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("search tool absent: %#v", upstreamBody)
	}
	tool := tools[0].(map[string]any)
	location, _ := tool["user_location"].(map[string]any)
	choice, _ := upstreamBody["tool_choice"].(map[string]any)
	if tool["type"] != "web_search" || tool["search_context_size"] != "low" || location["country"] != "US" || choice["type"] != "web_search" {
		t.Fatalf("search options changed: %#v", upstreamBody)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	msg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	annotations, _ := msg["annotations"].([]any)
	wantAnnotations := []any{
		map[string]any{"type": "url_citation", "url_citation": map[string]any{"start_index": float64(6), "end_index": float64(10), "url": "https://example.org/a", "title": "A"}},
		map[string]any{"type": "url_citation", "url_citation": map[string]any{"start_index": float64(11), "end_index": float64(12), "url": "https://example.org/a", "title": "A"}},
	}
	if !reflect.DeepEqual(annotations, wantAnnotations) {
		t.Fatalf("incorrect citations: %#v", annotations)
	}
	for _, invalid := range []string{`{"model":"fixture","messages":[{"role":"user","content":"hello"}],"web_search_options":{"user_location":{"unknown":1}}}`, `{"model":"fixture","messages":[{"role":"user","content":"hello"}],"web_search_options":{},"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}]}`} {
		w := send(invalid)
		if w.Code != 400 || count.Load() != 1 {
			t.Fatalf("invalid search request dispatched: status=%d count=%d", w.Code, count.Load())
		}
		if strings.Contains(invalid, `"tools"`) {
			var failure struct {
				Error struct {
					Param string `json:"param"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil || failure.Error.Param != "web_search_options" {
				t.Fatalf("wrong mixed-tools error: %s (%v)", w.Body.String(), err)
			}
		}
	}
	stream := send(`{"model":"fixture","messages":[{"role":"user","content":"hello"}],"web_search_options":{},"stream":true}`)
	if stream.Code != 200 {
		t.Fatalf("stream failed: %d %s", stream.Code, stream.Body.String())
	}
	var text strings.Builder
	var streamedAnnotations []any
	for _, line := range strings.Split(stream.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		for _, raw := range event["choices"].([]any) {
			delta := raw.(map[string]any)["delta"].(map[string]any)
			if content, ok := delta["content"].(string); ok {
				text.WriteString(content)
			}
			if values, ok := delta["annotations"].([]any); ok {
				streamedAnnotations = append(streamedAnnotations, values...)
			}
		}
	}
	if text.String() != msg["content"] || !reflect.DeepEqual(streamedAnnotations, annotations) {
		t.Fatalf("stream lost search answer or citations: %q %#v", text.String(), streamedAnnotations)
	}
}

type chatJinaResolver struct{}

func (chatJinaResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if host == "s.jina.ai" {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	return nil, fmt.Errorf("unexpected DNS lookup %q", host)
}

func searchFixture(t *testing.T, handler http.Handler) (string, hardenllm.EndpointPolicy) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"s.jina.ai"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewUnstartedServer(handler)
	upstream.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	dialer := &net.Dialer{}
	return upstream.URL + "/v1", hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Resolver: chatJinaResolver{}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, upstream.Listener.Addr().String())
	}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
}

func TestChatWebSearchFallback(t *testing.T) {
	var searchCalls, providerCalls, repairCalls atomic.Int32
	baseURL, policy := searchFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			searchCalls.Add(1)
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "https://example.test/source")
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		providerCalls.Add(1)
		var input struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if input.Model == "repair-fixture" {
			answer := `{"ok":true}`
			if repairCalls.Add(1) == 1 {
				answer = `{"ok":`
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": answer}}}})
			return
		}
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"fallback answer"}}]}`)
	}))
	options := stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "fallback", Provider: "openai", Protocol: "chat-completions", BaseURL: baseURL, CacheDomain: "test", APIKey: "synthetic"}}, DefaultConnection: "fallback", WebSearch: stateful.WebSearchOptions{JinaAPIKey: "synthetic"}, Cache: &projectionCache{records: make(map[string]hardenllm.CacheRecord)}, EndpointPolicy: policy}
	service, err := stateful.NewService(stateful.Config{Client: options})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Config{APIKeys: []string{proxyTestToken}, Service: service})
	if err != nil {
		t.Fatal(err)
	}
	client, err := stateful.NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	handler.clientForKey = func(string) (*hardenllm.Client, error) { return client, nil }
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture","messages":[{"role":"user","content":"hello"}],"web_search_options":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+proxyTestToken)
	w := httptest.NewRecorder()
	handler.Handler().ServeHTTP(w, req)
	if w.Code != 200 || searchCalls.Load() != 1 || providerCalls.Load() != 1 {
		t.Fatalf("fallback status=%d search=%d provider=%d body=%s", w.Code, searchCalls.Load(), providerCalls.Load(), w.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	msg := response["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "fallback answer" || msg["annotations"] != nil {
		t.Fatalf("fallback invented citation: %#v", msg)
	}
	const cachedRequest = `{"model":"fixture","input":"hello","tools":[{"type":"web_search"}],"include":["web_search_call.action.sources"],"harden":{"cache":"cache"}}`
	var firstOutput any
	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(cachedRequest))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+proxyTestToken)
		w := httptest.NewRecorder()
		handler.Handler().ServeHTTP(w, req)
		if w.Code != 200 || searchCalls.Load() != 2 || providerCalls.Load() != 2 {
			t.Fatalf("cached fallback attempt=%d status=%d search=%d provider=%d body=%s", attempt, w.Code, searchCalls.Load(), providerCalls.Load(), w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		items := body["output"].([]any)
		if len(items) != 2 {
			t.Fatalf("fallback lost search evidence: %#v", items)
		}
		search := items[0].(map[string]any)
		action := search["action"].(map[string]any)
		wantSources := []any{map[string]any{"type": "url", "url": "https://example.test/source"}}
		if search["type"] != "web_search_call" || action["type"] != "search" || !reflect.DeepEqual(action["sources"], wantSources) {
			t.Fatalf("fallback search evidence changed: %#v", search)
		}
		if attempt == 0 {
			firstOutput = items
		} else if w.Header().Get("Cache-Status") != "harden-llm; hit" || !reflect.DeepEqual(firstOutput, items) {
			t.Fatalf("cached fallback lost evidence or identity: header=%q output=%#v", w.Header().Get("Cache-Status"), items)
		}
	}
	const repairedRequest = `{"model":"repair-fixture","input":"hello","tools":[{"type":"web_search"}],"tool_choice":"required","include":["web_search_call.action.sources"],"harden":{"cache":"cache"},"text":{"format":{"type":"json_schema","name":"answer","strict":true,"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}}}`
	var repairedOutput any
	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(repairedRequest))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+proxyTestToken)
		w := httptest.NewRecorder()
		handler.Handler().ServeHTTP(w, req)
		if w.Code != 200 || searchCalls.Load() != 3 || providerCalls.Load() != 4 || repairCalls.Load() != 2 {
			t.Fatalf("repaired fallback attempt=%d status=%d search=%d provider=%d repair=%d body=%s", attempt, w.Code, searchCalls.Load(), providerCalls.Load(), repairCalls.Load(), w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		items := body["output"].([]any)
		if len(items) != 2 {
			t.Fatalf("repaired fallback lost search evidence: %#v", items)
		}
		action := items[0].(map[string]any)["action"].(map[string]any)
		wantSources := []any{map[string]any{"type": "url", "url": "https://example.test/source"}}
		if action["type"] != "search" || !reflect.DeepEqual(action["sources"], wantSources) || !json.Valid([]byte(body["output_text"].(string))) {
			t.Fatalf("repaired fallback lost source or output: %#v", body)
		}
		if attempt == 0 {
			repairedOutput = items
		} else if w.Header().Get("Cache-Status") != "harden-llm; hit" || !reflect.DeepEqual(repairedOutput, items) {
			t.Fatalf("cached repaired fallback changed: header=%q output=%#v", w.Header().Get("Cache-Status"), items)
		}
	}
}

// The expected JSON is the byte layout of chatResponse on origin/main for the
// same fixed result, before search annotations were added.
func TestChatWebSearchNonSearchGolden(t *testing.T) {
	result := hardenllm.Result{CallID: "fixture", Output: "answer"}
	response, err := chatResponse("model", result, 1, false, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"chatcmpl-fixture","object":"chat.completion","created":1,"model":"model","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}]}`
	if string(raw) != want {
		t.Fatalf("non-search Chat response changed: %s", raw)
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-549.
func TestSearchOperationAttemptOwnership(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                     string
		searchTimeout, modelRetry, schemaFailure bool
		ordinals                                 []int
	}{
		{"search_timeout_then_success", true, false, false, []int{1, 2, 2}},
		{"model_retry_reuses_search", false, true, false, []int{1, 1, 2}},
		{"post_transport_schema_failure", false, false, true, []int{1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var searches, models atomic.Int32
			baseURL, policy := searchFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host == "s.jina.ai" {
					if searches.Add(1) == 1 && tc.searchTimeout {
						<-r.Context().Done()
						return
					}
					fmt.Fprint(w, "https://example.test/source")
					return
				}
				if models.Add(1) == 1 && tc.modelRetry {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"error":{"code":"busy"}}`)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{}"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
			}))
			bundles := make(chan diagnostics.Bundle, 1)
			store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/support-bundle.json") {
					var bundle diagnostics.Bundle
					if err := json.NewDecoder(r.Body).Decode(&bundle); err != nil {
						t.Error(err)
						return
					}
					bundles <- bundle
				}
			}))
			defer store.Close()
			artifacts, err := stateful.NewArtifactStore(stateful.ArtifactOptions{Endpoint: store.URL, Bucket: "bucket", Region: "test", AccessKeyID: "fixture", SecretAccessKey: "fixture-secret", HTTPClient: store.Client()})
			if err != nil {
				t.Fatal(err)
			}
			const id = "OpenRouter DeepSeek V4 Pro"
			catalog, err := stateful.DefaultCatalog()
			if err != nil {
				t.Fatal(err)
			}
			preset := catalog[id]
			var def stateful.Profile
			if err = json.Unmarshal(preset.Definition, &def); err != nil {
				t.Fatal(err)
			}
			def.BaseURL = baseURL
			preset.Definition, err = json.Marshal(def)
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			service, err := stateful.NewService(stateful.Config{Presets: stateful.Catalog{id: preset}, Artifacts: artifacts, Client: stateful.ClientOptions{
				Connections: []hardenllm.Connection{{ID: preset.Binding.DeploymentUpstreamID, Provider: def.Provider, Protocol: def.APIInferenceType, BaseURL: baseURL, APIKey: "fixture-model-key"}}, DefaultConnection: preset.Binding.DeploymentUpstreamID,
				Logger: slog.New(slog.NewJSONHandler(&logs, nil)), EndpointPolicy: policy, WebSearch: stateful.WebSearchOptions{JinaAPIKey: "fixture-search-key", JinaTimeout: 250 * time.Millisecond},
			}})
			if err != nil {
				t.Fatal(err)
			}
			input := stateful.RunInput{ProfileID: id, UserPrompt: "Search for docs", WebSearch: true, CacheMode: hardenllm.CacheModeOff, CallType: hardenllm.CallTypeText, TimeoutMS: 5000, RecoveryPolicy: json.RawMessage(`{"maxAttempts":2,"retryOn":["network","server_error"],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`)}
			if tc.schemaFailure {
				input.CallType = hardenllm.CallTypeStructured
				input.Schema = json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)
			}
			out, _, err := service.Run(t.Context(), strings.Repeat("a", 64), input)
			if tc.schemaFailure != (err != nil) || len(out.Attempts) != tc.ordinals[len(tc.ordinals)-1] || out.ArtifactDiagnostic != nil {
				t.Fatalf("run status=%s err=%v attempts=%v", out.Status, err, out.Attempts)
			}
			var bundle diagnostics.Bundle
			select {
			case bundle = <-bundles:
			default:
				t.Fatal("support bundle not written")
			}
			wantCode := ""
			if tc.schemaFailure {
				wantCode = "STRUCTURED_SCHEMA"
			}
			if len(bundle.Trace.Attempts) != len(out.Attempts) || bundle.Trace.Attempts[len(out.Attempts)-1].Code != wantCode {
				t.Fatalf("trace attempts=%+v", bundle.Trace.Attempts)
			}
			operations := []string{}
			ordinals := []int{}
			for _, line := range bundle.Logs {
				var rec struct {
					Stage    string
					Attempt  int    `json:"attempt_number"`
					Category string `json:"error_category"`
					Code     string `json:"error_code"`
				}
				if err := json.Unmarshal([]byte(line), &rec); err != nil {
					t.Fatal(err)
				}
				operations = append(operations, rec.Stage+"/"+rec.Category+"/"+rec.Code)
				ordinals = append(ordinals, rec.Attempt)
			}
			expected := []string{"web_search/success/", "provider_model/success/"}
			if tc.searchTimeout {
				expected = append([]string{"web_search/network/ETIMEDOUT"}, expected...)
			}
			if tc.modelRetry {
				expected = slices.Insert(expected, 1, "provider_model/server_error/busy")
			}
			if !slices.Equal(ordinals, tc.ordinals) || !slices.Equal(operations, expected) {
				t.Fatalf("bundle ordinals=%v operations=%v", ordinals, operations)
			}
			dec := json.NewDecoder(&logs)
			var logOrdinals []int
			for {
				var rec struct {
					Attempt int `json:"attempt_number"`
					Stage   string
				}
				err := dec.Decode(&rec)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if rec.Stage == "web_search" || rec.Stage == "provider_model" {
					logOrdinals = append(logOrdinals, rec.Attempt)
				}
			}
			if !slices.Equal(logOrdinals, tc.ordinals) {
				t.Fatalf("process log ordinals=%v", logOrdinals)
			}
			wantSearch, wantModel := int32(1), int32(1)
			if tc.searchTimeout {
				wantSearch++
			}
			if tc.modelRetry {
				wantModel++
			}
			if searches.Load() != wantSearch || models.Load() != wantModel {
				t.Fatalf("search=%d model=%d", searches.Load(), models.Load())
			}
		})
	}
}
