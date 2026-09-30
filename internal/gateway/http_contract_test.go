package gateway_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-023 TEST-229 TEST-231

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestHTTPContract(t *testing.T) {
	identity := &fakeHTTPAuth{}
	postgresReadyCalls, artifactReadyCalls := 0, 0
	api, err := httpapi.New(httpapi.Config{
		Auth: identity,
		Readiness: []httpapi.ReadinessCheck{
			func(context.Context) error { postgresReadyCalls++; return nil },
			func(context.Context) error { artifactReadyCalls++; return nil },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	response := request(t, server.Client(), http.MethodGet, server.URL+"/healthz", nil, nil)
	if response.Code != http.StatusOK || response.JSON["status"] != "ok" || postgresReadyCalls != 0 || artifactReadyCalls != 0 {
		t.Fatalf("liveness = %#v; readiness calls=%d,%d", response, postgresReadyCalls, artifactReadyCalls)
	}
	response = request(t, server.Client(), http.MethodGet, server.URL+"/readyz", nil, nil)
	if response.Code != http.StatusOK || response.JSON["status"] != "ok" || postgresReadyCalls != 1 || artifactReadyCalls != 1 {
		t.Fatalf("readiness = %#v; calls=%d,%d", response, postgresReadyCalls, artifactReadyCalls)
	}

	unready, err := httpapi.New(httpapi.Config{Auth: identity, Readiness: []httpapi.ReadinessCheck{func(context.Context) error { return errors.New("database password=do-not-leak") }}})
	if err != nil {
		t.Fatal(err)
	}
	unreadyRecorder := httptest.NewRecorder()
	unready.Handler().ServeHTTP(unreadyRecorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if unreadiness := unreadBody(t, unreadyRecorder.Body.Bytes()); unreadyRecorder.Code != http.StatusServiceUnavailable || unreadiness["status"] != "unavailable" || strings.Contains(unreadyRecorder.Body.String(), "do-not-leak") {
		t.Fatalf("unready response = %d %s", unreadyRecorder.Code, unreadyRecorder.Body.String())
	}

	response = request(t, server.Client(), http.MethodPost, server.URL+"/api/v1/auth/login", []byte(`{"email":"a@example.test","password":"not-accepted-here"}`), jsonHeaders())
	assertEnvelope(t, response, http.StatusNotFound, true)

	for _, test := range []struct {
		name    string
		method  string
		path    string
		body    []byte
		headers map[string][]string
		status  int
		code    string
	}{
		{name: "unknown field", method: http.MethodPut, path: "/api/v1/profiles/profile-a", body: []byte(`{"profile":{},"admin":true}`), headers: jsonHeaders(), status: 400, code: "invalid_request"},
		{name: "trailing JSON", method: http.MethodPut, path: "/api/v1/profiles/profile-a", body: append([]byte(`{"profile":{}}`), []byte(` {}`)...), headers: jsonHeaders(), status: 400, code: "invalid_request"},
		{name: "wrong content type", method: http.MethodPut, path: "/api/v1/profiles/profile-a", body: []byte(`{"profile":{}}`), status: 415, code: "unsupported_media_type"},
		{name: "oversized body", method: http.MethodPut, path: "/api/v1/profiles/profile-a", body: []byte(`{"profile":{"defaultOptions":{"value":"` + strings.Repeat("x", 300<<10) + `"}}}`), headers: jsonHeaders(), status: 413, code: "request_too_large"},
		{name: "unknown route", method: http.MethodGet, path: "/api/v1/unknown", status: 404, code: "not_found"},
		{name: "wrong method", method: http.MethodPatch, path: "/api/v1/state", status: 405, code: "method_not_allowed"},
		{name: "missing bearer", method: http.MethodGet, path: "/api/v1/state", headers: map[string][]string{}, status: 401, code: "unauthenticated"},
		{name: "duplicate bearer", method: http.MethodGet, path: "/api/v1/state", headers: map[string][]string{"Authorization": {"Bearer valid-token", "Bearer second-token"}}, status: 401, code: "unauthenticated"},
		{name: "malformed bearer", method: http.MethodGet, path: "/api/v1/state", headers: map[string][]string{"Authorization": {"bearer valid-token"}}, status: 401, code: "unauthenticated"},
		{name: "unknown query", method: http.MethodGet, path: "/api/v1/history?debug=true", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "duplicate query", method: http.MethodGet, path: "/api/v1/history?limit=1&limit=2", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "history page and cursor", method: http.MethodGet, path: "/api/v1/history?page=2&cursor=", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "empty history page", method: http.MethodGet, path: "/api/v1/history?page=", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "overflow history page", method: http.MethodGet, path: "/api/v1/history?page=9223372036854775808", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "zero history page", method: http.MethodGet, path: "/api/v1/history?page=0", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "negative history page", method: http.MethodGet, path: "/api/v1/history?page=-1", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "fractional history page", method: http.MethodGet, path: "/api/v1/history?page=1.5", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "empty numbered history limit", method: http.MethodGet, path: "/api/v1/history?page=1&limit=", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "zero numbered history limit", method: http.MethodGet, path: "/api/v1/history?page=1&limit=0", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "large numbered history limit", method: http.MethodGet, path: "/api/v1/history?page=1&limit=101", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "unexpected body", method: http.MethodGet, path: "/api/v1/history", body: []byte(`{}`), headers: map[string][]string{"Authorization": {"Bearer valid-token"}, "Content-Type": {"application/json"}}, status: 400, code: "invalid_request"},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers := test.headers
			if headers == nil {
				headers = map[string][]string{"Authorization": {"Bearer valid-token"}}
			} else if _, ok := headers["Authorization"]; !ok && test.name != "missing bearer" {
				headers = cloneHeaders(headers)
				headers["Authorization"] = []string{"Bearer valid-token"}
			}
			response := request(t, server.Client(), test.method, server.URL+test.path, test.body, headers)
			assertEnvelope(t, response, test.status, true)
			apiError := response.JSON["error"].(map[string]any)
			if apiError["code"] != test.code || strings.Contains(string(response.Body), "correct horse") || response.Headers.Get("Cache-Control") != "no-store" {
				t.Fatalf("response = %#v headers=%v body=%s", response.JSON, response.Headers, response.Body)
			}
		})
	}

	panicking, err := httpapi.New(httpapi.Config{
		Auth: identity,
		Readiness: []httpapi.ReadinessCheck{func(context.Context) error {
			panic("password=panic-secret")
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	panicRecorder := httptest.NewRecorder()
	panicking.Handler().ServeHTTP(panicRecorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	panicBody := unreadBody(t, panicRecorder.Body.Bytes())
	if panicRecorder.Code != http.StatusInternalServerError || panicBody["error"].(map[string]any)["code"] != "internal_error" || strings.Contains(panicRecorder.Body.String(), "panic-secret") {
		t.Fatalf("panic response = %d %s", panicRecorder.Code, panicRecorder.Body.String())
	}
}

func cloneHeaders(source map[string][]string) map[string][]string {
	result := make(map[string][]string, len(source)+1)
	for name, values := range source {
		result[name] = append([]string(nil), values...)
	}
	return result
}

func TestHTTPTraceContextPropagation(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	telemetry, err := gateway.NewTelemetry(tracerProvider, nil)
	if err != nil {
		t.Fatal(err)
	}
	api, err := httpapi.New(httpapi.Config{Auth: &fakeHTTPAuth{}, Telemetry: telemetry})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set(
		"traceparent",
		"00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
	)
	recorder := httptest.NewRecorder()
	api.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("health response status = %d", recorder.Code)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("gateway HTTP span count = %d, want 1", len(spans))
	}
	span := spans[0]
	if got := span.SpanContext.TraceID().String(); got != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("gateway trace ID = %s", got)
	}
	if got := span.Parent.SpanID().String(); got != "0123456789abcdef" || !span.Parent.IsRemote() {
		t.Fatalf("gateway parent = %s remote=%t", got, span.Parent.IsRemote())
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-025 TEST-039
func TestHTTPRunDurationLimit(t *testing.T) {
	for _, duration := range []time.Duration{0, time.Millisecond, 50 * time.Millisecond, 60 * time.Second} {
		if _, err := httpapi.New(httpapi.Config{Auth: &fakeHTTPAuth{}, MaxRunDuration: duration}); err != nil {
			t.Fatalf("valid run duration %v rejected: %v", duration, err)
		}
	}
	for _, duration := range []time.Duration{-time.Millisecond, time.Microsecond, 60*time.Second + time.Millisecond} {
		if _, err := httpapi.New(httpapi.Config{Auth: &fakeHTTPAuth{}, MaxRunDuration: duration}); err == nil {
			t.Fatalf("gateway accepted invalid run duration %v", duration)
		}
	}
}

type fakeHTTPAuth struct {
	lastAuthorization string
	ownerID           string
}

func (identity *fakeHTTPAuth) AuthenticateRequest(request *http.Request) (auth.Principal, error) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 || values[0] != "Bearer valid-token" {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	identity.lastAuthorization = values[0]
	ownerID := identity.ownerID
	if ownerID == "" {
		ownerID = "owner-a"
	}
	return auth.Principal{OwnerID: ownerID}, nil
}

type recordedResponse struct {
	Code    int
	Headers http.Header
	Body    []byte
	JSON    map[string]any
}

func request(t *testing.T, client *http.Client, method, url string, body []byte, headers map[string][]string) recordedResponse {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	bodyBytes := unreadRecorderBody(response.Body)
	if err := json.Unmarshal(bodyBytes, &decoded); err != nil {
		t.Fatalf("decode response %d: %v; body=%s", response.StatusCode, err, bodyBytes)
	}
	return recordedResponse{Code: response.StatusCode, Headers: response.Header.Clone(), Body: bodyBytes, JSON: decoded}
}

func assertEnvelope(t *testing.T, response recordedResponse, status int, wantError bool) {
	t.Helper()
	if response.Code != status || len(response.JSON) != 3 || response.JSON["state"] == nil || (response.JSON["error"] != nil) != wantError {
		t.Fatalf("response = %d %#v", response.Code, response.JSON)
	}
}

func jsonHeaders() map[string][]string {
	return map[string][]string{"Content-Type": {"application/json"}}
}

func unreadRecorderBody(reader interface{ Read([]byte) (int, error) }) []byte {
	var result bytes.Buffer
	_, _ = result.ReadFrom(reader)
	return result.Bytes()
}

func unreadBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
