package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-490

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestRequestTrackingContract(t *testing.T) {
	t.Parallel()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := doc.Paths.Find("/v1/requests/{trackingID}")
	if path == nil || path.Get == nil {
		t.Fatal("authenticated request-status lookup is missing from OpenAPI")
	}
	for _, route := range []string{"/v1/responses", "/v1/chat/completions"} {
		found := false
		for _, ref := range doc.Paths.Find(route).Post.Parameters {
			if ref.Value != nil && ref.Value.Name == "X-HLLM-Tracking-ID" && ref.Value.In == "header" && !ref.Value.Required {
				found = true
			}
		}
		if !found {
			t.Errorf("%s has no optional tracking header", route)
		}
	}
}

func TestRequestTrackingLookupAdmission(t *testing.T) {
	t.Parallel()
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("lookup dispatched provider work")
		w.WriteHeader(500)
	}))
	defer provider.Close()
	proxy := newContractProxy(t, provider)
	defer proxy.Close()
	for _, token := range []string{"", proxyTestToken} {
		r, _ := http.NewRequest("GET", proxy.URL+"/v1/requests/a60f21da-604f-4fe3-a039-23382ec6b313", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := proxy.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		want := 503
		if token == "" {
			want = 401
		}
		if resp.StatusCode != want || resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("lookup status/cache=%d/%q, want %d/no-store", resp.StatusCode, resp.Header.Get("Cache-Control"), want)
		}
	}
}
