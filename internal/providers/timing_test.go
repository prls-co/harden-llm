package providers

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-474
import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/prls-co/harden-llm/internal/runtime"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderHeaderTimingAndExplicitTransportCapacity(t *testing.T) {
	t.Parallel()
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Cpa-Trace-Id", "synthetic-private-prefix-0123abcd")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output_text\":\"ok\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	p := EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots}, MaxConnsPerHost: 256}
	router, err := NewRouter(Config{EndpointPolicy: p})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := router.Prepare(context.Background(), runtime.Connection{ID: "cpa", Provider: "cpa", APIInferenceType: "responses", BaseURL: s.URL + "/v1"}, runtime.Credential{APIKey: "synthetic"}, runtime.Call{ModelID: "gpt-6-luna", CallType: "text", Messages: []runtime.Message{{Role: "user", Content: []byte(`"ok"`)}}, ProviderOptions: map[string]any{"stream": true}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.Execute(context.Background(), operation)
	if err != nil || result.Stream.HeadersMs == nil || *result.Stream.HeadersMs < 0 || result.Stream.CorrelationID != "0123abcd" {
		t.Fatalf("header timing missing: %+v %v", result.Stream, err)
	}
	transport := router.client.Transport.(*safeRoundTripper)
	for _, tr := range transport.transports {
		if tr.MaxConnsPerHost != 256 {
			t.Fatal("transport concealed configured capacity")
		}
	}
	p.MaxConnsPerHost = -1
	if _, err := NewRouter(Config{EndpointPolicy: p}); err == nil {
		t.Fatal("invalid capacity accepted")
	}
}
