package auth

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-022

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prls-co/prls-control-plane/go/access"
)

const (
	serviceToken  = "test-harden-llm-service-token-0123456789"
	staticAccount = "11111111-1111-4111-8111-111111111111"
	otherAccount  = "22222222-2222-4222-8222-222222222222"
)

func TestAuthenticateHumanRequestUsesCurrentControlPlaneAccount(t *testing.T) {
	var requests int
	owner := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method != http.MethodGet || request.URL.Path != "/internal/v1/access-context" ||
			request.Header.Get("Authorization") != "Bearer control-plane-internal-token" ||
			request.Header.Get("X-PRLS-Session-Reference") != "current-reference" || request.Header.Get("Cookie") != "" {
			t.Error("Control Plane request did not use the current server-side session reference")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"user_id":"user-1","email":"user@example.test","name":"Test User","role":"member","session_ref":"current-reference","account":{"account_id":"` + otherAccount + `","name":"Other Account","products":["harden-llm"]}}`))
	}))
	defer owner.Close()

	client, err := access.New(owner.URL, "control-plane-internal-token")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{ControlPlane: client, ServiceToken: serviceToken, StaticAccountID: staticAccount})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles", nil)
	request.Header.Set("Authorization", "Bearer "+serviceToken)
	request.Header.Set("X-PRLS-Session-Reference", "current-reference")

	principal, err := service.AuthenticateRequest(request)
	if err != nil || principal.OwnerID != otherAccount || requests != 1 {
		t.Fatalf("principal = %#v, err = %v, Control Plane requests = %d", principal, err, requests)
	}
}

func TestAuthenticateRejectsProductDenialAndDoesNotUseMachineScope(t *testing.T) {
	owner := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"user_id":"user-1","email":"user@example.test","name":"Test User","role":"member","session_ref":"current-reference","account":{"account_id":"` + otherAccount + `","name":"Other Account","products":["knowledge"]}}`))
	}))
	defer owner.Close()
	client, err := access.New(owner.URL, "control-plane-internal-token")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{ControlPlane: client, ServiceToken: serviceToken, StaticAccountID: staticAccount})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles", nil)
	request.Header.Set("Authorization", "Bearer "+serviceToken)
	request.Header.Set("X-PRLS-Session-Reference", "current-reference")
	if principal, err := service.AuthenticateRequest(request); !errors.Is(err, ErrForbidden) || principal.OwnerID != "" {
		t.Fatalf("denied request principal = %#v, err = %v", principal, err)
	}
}

func TestAuthenticateMapsRevocationAndControlPlaneFailure(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		status int
		want   error
	}{
		{name: "revoked", status: http.StatusUnauthorized, want: ErrUnauthenticated},
		{name: "unavailable", status: http.StatusServiceUnavailable, want: ErrUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			owner := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(testCase.status)
			}))
			defer owner.Close()
			client, err := access.New(owner.URL, "control-plane-internal-token")
			if err != nil {
				t.Fatal(err)
			}
			service, err := NewService(Config{ControlPlane: client, ServiceToken: serviceToken, StaticAccountID: staticAccount})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles", nil)
			request.Header.Set("Authorization", "Bearer "+serviceToken)
			request.Header.Set("X-PRLS-Session-Reference", "current-reference")
			if _, err = service.AuthenticateRequest(request); !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestAuthenticateMachineCredentialIsAccountScoped(t *testing.T) {
	owner := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("machine credential must not resolve a human session")
	}))
	defer owner.Close()
	client, err := access.New(owner.URL, "control-plane-internal-token")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{ControlPlane: client, ServiceToken: serviceToken, StaticAccountID: staticAccount})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles", nil)
	request.Header.Set("Authorization", "Bearer "+serviceToken)
	principal, err := service.AuthenticateRequest(request)
	if err != nil || principal.OwnerID != staticAccount {
		t.Fatalf("principal = %#v, err = %v", principal, err)
	}
}

func TestAuthenticateRejectsMalformedOrMissingAuthority(t *testing.T) {
	ownerCalls := 0
	owner := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		ownerCalls++
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer owner.Close()
	client, err := access.New(owner.URL, "control-plane-internal-token")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{ControlPlane: client, ServiceToken: serviceToken, StaticAccountID: staticAccount})
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		auth   []string
		refs   []string
		cookie []string
	}{
		{name: "missing service token"},
		{name: "wrong service token", auth: []string{"Bearer another-service-token-0123456789"}, refs: []string{"current-reference"}},
		{name: "duplicate authorization", auth: []string{"Bearer " + serviceToken, "Bearer " + serviceToken}},
		{name: "unexpected cookie", auth: []string{"Bearer " + serviceToken}, cookie: []string{"session=secret"}},
		{name: "duplicate session reference", auth: []string{"Bearer " + serviceToken}, refs: []string{"one", "two"}},
		{name: "empty session reference", auth: []string{"Bearer " + serviceToken}, refs: []string{""}},
		{name: "padded session reference", auth: []string{"Bearer " + serviceToken}, refs: []string{" current-reference "}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles", nil)
			for _, value := range testCase.auth {
				request.Header.Add("Authorization", value)
			}
			for _, value := range testCase.refs {
				request.Header.Add("X-PRLS-Session-Reference", value)
			}
			for _, value := range testCase.cookie {
				request.Header.Add("Cookie", value)
			}
			if _, err := service.AuthenticateRequest(request); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("error = %v, want unauthenticated", err)
			}
		})
	}
	if ownerCalls != 0 {
		t.Fatalf("malformed requests reached Control Plane %d times", ownerCalls)
	}
}

func TestValidateServiceToken(t *testing.T) {
	for _, testCase := range []struct {
		name, token, account string
		valid                bool
	}{
		{name: "disabled", valid: true},
		{name: "account without token", account: staticAccount},
		{name: "token without machine account", token: serviceToken, valid: true},
		{name: "invalid account", token: serviceToken, account: "operator-local"},
		{name: "valid", token: serviceToken, account: staticAccount, valid: true},
		{name: "short token", token: strings.Repeat("x", 31), account: staticAccount},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := ValidateServiceToken(testCase.token, testCase.account) == nil
			if got != testCase.valid {
				t.Fatalf("valid = %v, want %v", got, testCase.valid)
			}
		})
	}
}

func TestControlPlaneClientUsesRequestContext(t *testing.T) {
	owner := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Context().Err() != nil {
			t.Error("request context was canceled before resolution")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"user_id":"user-1","email":"user@example.test","name":"Test User","role":"member","session_ref":"current-reference","account":{"account_id":"` + otherAccount + `","name":"Other Account","products":["harden-llm"]}}`))
	}))
	defer owner.Close()
	client, err := access.New(owner.URL, "control-plane-internal-token")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{ControlPlane: client, ServiceToken: serviceToken, StaticAccountID: staticAccount})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/profiles", nil).WithContext(context.Background())
	request.Header.Set("Authorization", "Bearer "+serviceToken)
	request.Header.Set("X-PRLS-Session-Reference", "current-reference")
	if _, err := service.AuthenticateRequest(request); err != nil {
		t.Fatal(err)
	}
}
