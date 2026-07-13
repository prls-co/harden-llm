package gateway_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-023

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

	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
)

func TestHTTPContract(t *testing.T) {
	fixedExpiry := time.Date(2026, 7, 13, 13, 0, 0, 0, time.UTC)
	identity := &fakeHTTPAuth{
		login: auth.LoginResult{AccessToken: "one-time-token", ExpiresAt: fixedExpiry, Principal: auth.Principal{OwnerID: "owner-a", Email: "a@example.test", SessionID: "session-a", ExpiresAt: fixedExpiry}},
	}
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

	validLogin := []byte(`{"email":"a@example.test","password":"correct horse battery staple"}`)
	response = request(t, server.Client(), http.MethodPost, server.URL+"/api/v1/auth/login", validLogin, map[string][]string{"Content-Type": {"application/json"}})
	assertEnvelope(t, response, http.StatusOK, false)
	result := response.JSON["result"].(map[string]any)
	if result["accessToken"] != "one-time-token" || response.Headers.Get("Set-Cookie") != "" || response.Headers.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("login response = %#v headers=%v", response.JSON, response.Headers)
	}

	for _, test := range []struct {
		name    string
		method  string
		path    string
		body    []byte
		headers map[string][]string
		status  int
		code    string
	}{
		{name: "unknown field", method: http.MethodPost, path: "/api/v1/auth/login", body: []byte(`{"email":"a@example.test","password":"correct horse battery staple","admin":true}`), headers: jsonHeaders(), status: 400, code: "invalid_request"},
		{name: "trailing JSON", method: http.MethodPost, path: "/api/v1/auth/login", body: append(validLogin, []byte(` {}`)...), headers: jsonHeaders(), status: 400, code: "invalid_request"},
		{name: "wrong content type", method: http.MethodPost, path: "/api/v1/auth/login", body: validLogin, status: 415, code: "unsupported_media_type"},
		{name: "oversized body", method: http.MethodPost, path: "/api/v1/auth/login", body: []byte(`{"email":"a@example.test","password":"` + strings.Repeat("x", 70<<10) + `"}`), headers: jsonHeaders(), status: 413, code: "request_too_large"},
		{name: "unknown route", method: http.MethodGet, path: "/api/v1/unknown", status: 404, code: "not_found"},
		{name: "wrong method", method: http.MethodPatch, path: "/api/v1/state", status: 405, code: "method_not_allowed"},
		{name: "missing bearer", method: http.MethodGet, path: "/api/v1/auth/session", status: 401, code: "unauthenticated"},
		{name: "duplicate bearer", method: http.MethodGet, path: "/api/v1/auth/session", headers: map[string][]string{"Authorization": {"Bearer valid-token", "Bearer second-token"}}, status: 401, code: "unauthenticated"},
		{name: "malformed bearer", method: http.MethodGet, path: "/api/v1/auth/session", headers: map[string][]string{"Authorization": {"bearer valid-token"}}, status: 401, code: "unauthenticated"},
		{name: "unknown query", method: http.MethodGet, path: "/api/v1/auth/session?debug=true", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "duplicate query", method: http.MethodGet, path: "/api/v1/history?limit=1&limit=2", headers: map[string][]string{"Authorization": {"Bearer valid-token"}}, status: 400, code: "invalid_request"},
		{name: "unexpected body", method: http.MethodGet, path: "/api/v1/auth/session", body: []byte(`{}`), headers: map[string][]string{"Authorization": {"Bearer valid-token"}, "Content-Type": {"application/json"}}, status: 400, code: "invalid_request"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := request(t, server.Client(), test.method, server.URL+test.path, test.body, test.headers)
			assertEnvelope(t, response, test.status, true)
			apiError := response.JSON["error"].(map[string]any)
			if apiError["code"] != test.code || strings.Contains(string(response.Body), "correct horse") || response.Headers.Get("Cache-Control") != "no-store" {
				t.Fatalf("response = %#v headers=%v body=%s", response.JSON, response.Headers, response.Body)
			}
		})
	}

	response = request(t, server.Client(), http.MethodGet, server.URL+"/api/v1/auth/session", nil, map[string][]string{
		"Authorization": {"Bearer valid-token"}, "Forwarded": {"host=attacker.example;proto=http"}, "X-Forwarded-Host": {"attacker.example"},
	})
	assertEnvelope(t, response, http.StatusOK, false)
	if response.JSON["result"].(map[string]any)["ownerId"] != "owner-a" || identity.lastAuthorization != "Bearer valid-token" {
		t.Fatalf("session response = %#v auth=%q", response.JSON, identity.lastAuthorization)
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

type fakeHTTPAuth struct {
	login             auth.LoginResult
	lastAuthorization string
}

func (identity *fakeHTTPAuth) Login(context.Context, string, string) (auth.LoginResult, error) {
	return identity.login, nil
}

func (identity *fakeHTTPAuth) AuthenticateHeader(_ context.Context, values []string) (auth.Principal, error) {
	if len(values) != 1 || values[0] != "Bearer valid-token" {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	identity.lastAuthorization = values[0]
	return identity.login.Principal, nil
}

func (identity *fakeHTTPAuth) LogoutPrincipal(context.Context, auth.Principal) error { return nil }

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
