package providers

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-024

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestModelDiscovery(t *testing.T) {
	for _, test := range []struct {
		name          string
		inferenceType string
		path          string
		assertRequest func(*testing.T, *http.Request)
		response      func(*http.Request) string
	}{
		{
			name: "openai", inferenceType: "responses", path: "/v1/models",
			assertRequest: func(t *testing.T, request *http.Request) {
				if request.Header.Get("Authorization") != "Bearer model-secret" || request.Header.Get("X-Safe") != "yes" {
					t.Fatalf("OpenAI headers = %v", request.Header)
				}
			},
			response: func(request *http.Request) string {
				if request.URL.Query().Get("after") == "" {
					return `{"data":[{"id":"model-b"}],"has_more":true,"last_id":"model-b"}`
				}
				return `{"data":[{"id":"model-a","display_name":"Model A"},{"id":"model-b"}],"has_more":false}`
			},
		},
		{
			name: "gemini", inferenceType: "gemini-generate-content", path: "/v1beta/models",
			assertRequest: func(t *testing.T, request *http.Request) {
				if request.Header.Get("X-Goog-Api-Key") != "model-secret" || request.URL.Query().Get("pageSize") != "1000" {
					t.Fatalf("Gemini request = %s %v", request.URL, request.Header)
				}
			},
			response: func(request *http.Request) string {
				if request.URL.Query().Get("pageToken") == "" {
					return `{"models":[{"name":"models/gemini-b"}],"nextPageToken":"page-2"}`
				}
				return `{"models":[{"name":"models/gemini-a","displayName":"Gemini A"}]}`
			},
		},
		{
			name: "anthropic", inferenceType: "anthropic-messages", path: "/v1/models",
			assertRequest: func(t *testing.T, request *http.Request) {
				if request.Header.Get("X-Api-Key") != "model-secret" || request.Header.Get("Anthropic-Version") == "" {
					t.Fatalf("Anthropic headers = %v", request.Header)
				}
			},
			response: func(request *http.Request) string {
				if request.URL.Query().Get("after_id") == "" {
					return `{"data":[{"id":"claude-b","display_name":"Claude B"}],"has_more":true,"last_id":"claude-b"}`
				}
				return `{"data":[{"id":"claude-a","display_name":"Claude A"}],"has_more":false}`
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet || request.URL.Path != test.path {
					t.Errorf("request = %s %s", request.Method, request.URL)
					writer.WriteHeader(http.StatusNotFound)
					return
				}
				test.assertRequest(t, request)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(test.response(request)))
			}))
			defer server.Close()
			discovery := newTestModelDiscovery(t, server)
			baseURL := server.URL + "/v1"
			if test.inferenceType == "gemini-generate-content" {
				baseURL = server.URL + "/v1beta"
			}
			models, err := discovery.Discover(context.Background(), ModelDiscoveryRequest{
				BaseURL: baseURL, APIInferenceType: test.inferenceType,
				APIKey: "model-secret", Headers: map[string]string{"X-Safe": "yes", "X-Forwarded-For": "attacker"},
			})
			if err != nil || len(models) != 2 || !strings.HasSuffix(models[0].ID, "a") || !strings.HasSuffix(models[1].ID, "b") {
				t.Fatalf("models = %#v, %v", models, err)
			}
		})
	}
}

func TestModelDiscoveryFailureBounds(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler http.Handler
	}{
		{name: "redirect", handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, "https://attacker.example/secret", http.StatusFound)
		})},
		{name: "oversized", handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{"data":[{"id":"`+strings.Repeat("x", maximumModelPageBytes)+`"}]}`)
		})},
		{name: "wrong content type", handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "text/plain")
			_, _ = writer.Write([]byte(`{"data":[]}`))
		})},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(test.handler)
			defer server.Close()
			discovery := newTestModelDiscovery(t, server)
			_, err := discovery.Discover(context.Background(), ModelDiscoveryRequest{
				BaseURL: server.URL + "/v1", APIInferenceType: "responses", APIKey: "model-secret",
			})
			if err == nil || strings.Contains(err.Error(), "attacker.example") || strings.Contains(err.Error(), "model-secret") {
				t.Fatalf("failure = %v", err)
			}
		})
	}
}

func newTestModelDiscovery(t *testing.T, server *httptest.Server) *ModelDiscovery {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	certificates := x509.NewCertPool()
	certificates.AddCert(server.Certificate())
	discovery, err := NewModelDiscovery(EndpointPolicy{
		PrivateAllowedHosts: []string{parsed.Hostname()},
		TLSConfig:           &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: certificates},
	})
	if err != nil {
		t.Fatal(err)
	}
	return discovery
}
