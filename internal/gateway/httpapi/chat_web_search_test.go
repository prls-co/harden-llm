package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-537

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/api"
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

func TestChatWebSearchFallback(t *testing.T) {
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
	var searchCalls, providerCalls atomic.Int32
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"fallback answer"}}]}`)
	}))
	upstream.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	upstream.StartTLS()
	defer upstream.Close()
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	dialer := &net.Dialer{}
	options := stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "fallback", Provider: "openai", Protocol: "chat-completions", BaseURL: upstream.URL + "/v1", CacheDomain: "test", APIKey: "synthetic"}}, DefaultConnection: "fallback", WebSearch: stateful.WebSearchOptions{JinaAPIKey: "synthetic"}, Cache: &projectionCache{records: make(map[string]hardenllm.CacheRecord)}, EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Resolver: chatJinaResolver{}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, upstream.Listener.Addr().String())
	}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
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
