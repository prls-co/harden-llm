//go:build compose

package smoke

// SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-012

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

const frontendSmokeSessionCookie = "frontend-smoke-session"

type frontendControlPlaneFixture struct {
	internalToken string
	origin        string
	email         string
	password      string
	context       map[string]any
	mu            sync.Mutex
	active        bool
}

func startFrontendControlPlaneFixture(t *testing.T, internalToken, origin, email, password, userID string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen for frontend Control Plane fixture: %v", err)
	}
	fixture := &frontendControlPlaneFixture{
		internalToken: internalToken,
		origin:        origin,
		email:         email,
		password:      password,
		context: map[string]any{
			"user_id":     userID,
			"email":       email,
			"name":        "Frontend Smoke User",
			"role":        "member",
			"session_ref": "frontend-smoke-session-reference",
			"account":     nil,
		},
	}
	server := &http.Server{Handler: fixture}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			_ = listener.Close()
		}
	}()
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	})

	port := listener.Addr().(*net.TCPAddr).Port
	return "http://host.docker.internal:" + strconv.Itoa(port)
}

func (fixture *frontendControlPlaneFixture) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/health/ready" && request.Method == http.MethodGet:
		writeFrontendControlPlaneJSON(writer, http.StatusOK, map[string]string{"status": "ok", "service": "control-plane-fixture"})
	case request.URL.Path == "/api/auth/sign-in/email" && request.Method == http.MethodPost:
		fixture.signIn(writer, request)
	case request.URL.Path == "/api/auth/sign-out" && request.Method == http.MethodPost:
		fixture.signOut(writer, request)
	case request.URL.Path == "/internal/v1/access-context" && request.Method == http.MethodGet:
		if !fixture.internalAuthorized(request) || !fixture.sessionActive(request) {
			writeFrontendControlPlaneJSON(writer, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
			return
		}
		writeFrontendControlPlaneJSON(writer, http.StatusOK, fixture.context)
	default:
		http.NotFound(writer, request)
	}
}

func (fixture *frontendControlPlaneFixture) signIn(writer http.ResponseWriter, request *http.Request) {
	if !fixture.internalAuthorized(request) || request.Header.Get("Origin") != fixture.origin {
		writeFrontendControlPlaneJSON(writer, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	var credentials struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4096)).Decode(&credentials) != nil ||
		credentials.Email != fixture.email || credentials.Password != fixture.password {
		writeFrontendControlPlaneJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication_failed"})
		return
	}
	fixture.mu.Lock()
	fixture.active = true
	fixture.mu.Unlock()
	http.SetCookie(writer, &http.Cookie{
		Name: "prls.session_token", Value: frontendSmokeSessionCookie, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	writeFrontendControlPlaneJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (fixture *frontendControlPlaneFixture) signOut(writer http.ResponseWriter, request *http.Request) {
	if !fixture.internalAuthorized(request) || request.Header.Get("Origin") != fixture.origin || !fixture.sessionActive(request) {
		writeFrontendControlPlaneJSON(writer, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	fixture.mu.Lock()
	fixture.active = false
	fixture.mu.Unlock()
	http.SetCookie(writer, &http.Cookie{
		Name: "prls.session_token", Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	writeFrontendControlPlaneJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (fixture *frontendControlPlaneFixture) internalAuthorized(request *http.Request) bool {
	values := request.Header.Values("Authorization")
	want := "Bearer " + fixture.internalToken
	return len(values) == 1 && subtle.ConstantTimeCompare([]byte(values[0]), []byte(want)) == 1
}

func (fixture *frontendControlPlaneFixture) sessionActive(request *http.Request) bool {
	references := request.Header.Values("X-PRLS-Session-Reference")
	cookies := request.Cookies()
	var sessionCookies []string
	for _, cookie := range cookies {
		if cookie.Name == "prls.session_token" {
			sessionCookies = append(sessionCookies, cookie.Value)
		}
	}
	validCookie := len(references) == 0 && len(sessionCookies) == 1 && sessionCookies[0] == frontendSmokeSessionCookie
	validReference := len(references) == 1 && len(sessionCookies) == 0 && references[0] == fixture.context["session_ref"]
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.active && (validCookie || validReference)
}

func writeFrontendControlPlaneJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

var _ http.Handler = (*frontendControlPlaneFixture)(nil)
