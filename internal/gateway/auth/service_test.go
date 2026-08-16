package auth

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-022

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/postgres"
)

func TestStaticTokenAuthenticatesConfiguredOwner(t *testing.T) {
	staticToken := strings.Repeat("s", 43)
	service, err := NewService(Config{
		Store:              &staticAuthStore{user: postgres.User{ID: "operator-01", Email: "operator@example.test"}},
		SessionTTL:         time.Hour,
		StaticToken:        staticToken,
		StaticTokenOwnerID: "operator-01",
	})
	if err != nil {
		t.Fatal(err)
	}

	principal, err := service.AuthenticateHeader(context.Background(), []string{"Bearer " + staticToken})
	if err != nil {
		t.Fatal(err)
	}
	if principal.OwnerID != "operator-01" || principal.Email != "operator@example.test" ||
		principal.SessionID != staticTokenSessionID || principal.ExpiresAt != staticTokenExpiresAt || !principal.staticToken {
		t.Fatalf("static principal = %#v", principal)
	}
	encoded, err := json.Marshal(principal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), staticToken) || strings.Contains(string(encoded), "staticToken") {
		t.Fatalf("static token leaked through principal JSON: %s", encoded)
	}

	for _, header := range []string{"Bearer " + strings.Repeat("x", 43), "bearer " + staticToken, "Bearer  " + staticToken} {
		if _, err := service.AuthenticateHeader(context.Background(), []string{header}); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("header %q returned %v", header, err)
		}
	}
	if err := service.LogoutPrincipal(context.Background(), principal); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("static token logout = %v, want unauthenticated", err)
	}
}

func TestStaticTokenRequiresOwnerAndMinimumEntropy(t *testing.T) {
	store := &staticAuthStore{}
	for name, config := range map[string]Config{
		"token without owner": {Store: store, SessionTTL: time.Hour, StaticToken: strings.Repeat("s", 43)},
		"owner without token": {Store: store, SessionTTL: time.Hour, StaticTokenOwnerID: "operator-01"},
		"short token":         {Store: store, SessionTTL: time.Hour, StaticToken: strings.Repeat("s", 31), StaticTokenOwnerID: "operator-01"},
		"whitespace token":    {Store: store, SessionTTL: time.Hour, StaticToken: " " + strings.Repeat("s", 42), StaticTokenOwnerID: "operator-01"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewService(config); err == nil {
				t.Fatal("invalid static-token configuration was accepted")
			}
		})
	}
}

type staticAuthStore struct {
	user postgres.User
}

func (store *staticAuthStore) CreateUser(context.Context, postgres.User) error {
	return errors.New("unused")
}

func (store *staticAuthStore) UserByEmail(context.Context, string) (postgres.User, error) {
	return postgres.User{}, errors.New("unused")
}

func (store *staticAuthStore) UserByID(_ context.Context, id string) (postgres.User, error) {
	if store.user.ID != id {
		return postgres.User{}, postgres.ErrNotFound
	}
	return store.user, nil
}

func (store *staticAuthStore) CreateSession(context.Context, postgres.Session) error {
	return errors.New("unused")
}

func (store *staticAuthStore) SessionByDigest(context.Context, []byte) (postgres.Session, error) {
	return postgres.Session{}, postgres.ErrNotFound
}

func (store *staticAuthStore) RevokeSession(context.Context, string, []byte, time.Time) error {
	return errors.New("unused")
}

func (store *staticAuthStore) RevokeSessionByID(context.Context, string, string, time.Time) error {
	return errors.New("unused")
}
