//go:build integration

package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-022

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/internal/gateway/command"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

func TestAuthProfileContract(t *testing.T) {
	_, dsn := integrationtest.StartPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	authService, err := auth.NewService(auth.Config{Store: store, SessionTTL: time.Hour, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	bootstrapUser, err := command.BootstrapUser(ctx, command.BootstrapUserConfig{
		DatabaseURL: dsn, OwnerID: "owner-a", Email: "a@example.test",
		Password: "correct horse battery staple A", Clock: clock,
	})
	if err != nil || bootstrapUser.ID != "owner-a" || bootstrapUser.PasswordHash != "" {
		t.Fatalf("bootstrap command = %#v, %v", bootstrapUser, err)
	}
	if _, err := authService.BootstrapUser(ctx, "owner-b", "b@example.test", "correct horse battery staple B"); err != nil {
		t.Fatalf("bootstrap owner-b: %v", err)
	}
	if _, err := authService.Login(ctx, "a@example.test", "wrong password value"); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("wrong password did not use stable auth failure: %v", err)
	}
	loginA, err := authService.Login(ctx, "A@EXAMPLE.TEST", "correct horse battery staple A")
	if err != nil {
		t.Fatal(err)
	}
	loginB, err := authService.Login(ctx, "b@example.test", "correct horse battery staple B")
	if err != nil {
		t.Fatal(err)
	}
	if loginA.AccessToken == "" || loginA.AccessToken == loginB.AccessToken || loginA.Principal.OwnerID != "owner-a" || loginA.ExpiresAt != now.Add(time.Hour) {
		t.Fatalf("invalid login results: %#v %#v", loginA, loginB)
	}
	principal, err := authService.AuthenticateHeader(ctx, []string{"Bearer " + loginA.AccessToken})
	if err != nil || principal.OwnerID != "owner-a" || principal.SessionID == "" {
		t.Fatalf("authenticate = %#v, %v", principal, err)
	}
	encodedPrincipal, _ := json.Marshal(principal)
	if bytes.Contains(encodedPrincipal, []byte(loginA.AccessToken)) {
		t.Fatalf("principal retained bearer token: %s", encodedPrincipal)
	}
	for _, headers := range [][]string{
		nil,
		{},
		{"bearer " + loginA.AccessToken},
		{"Bearer  " + loginA.AccessToken},
		{"Bearer " + loginA.AccessToken + " extra"},
		{"Bearer " + loginA.AccessToken, "Bearer " + loginB.AccessToken},
		{"Basic " + loginA.AccessToken},
		{"Bearer unknown-token"},
	} {
		if _, err := authService.AuthenticateHeader(ctx, headers); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Errorf("malformed header %#v returned %v", headers, err)
		}
	}
	assertTokenNotPersisted(t, ctx, dsn, loginA.AccessToken, loginB.AccessToken)

	if err := authService.Logout(ctx, loginA.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err := authService.AuthenticateHeader(ctx, []string{"Bearer " + loginA.AccessToken}); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("revoked token authenticated: %v", err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := authService.AuthenticateHeader(ctx, []string{"Bearer " + loginB.AccessToken}); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("expired token authenticated: %v", err)
	}
	now = now.Add(-2 * time.Hour)

	profile := sourceProfile(t)
	vault, err := profiles.NewCredentialVault("key-2026", map[string][]byte{"key-2026": bytes.Repeat([]byte{0x44}, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	probe := &recordingProber{}
	profileService, err := NewProfileService(ProfileServiceConfig{Store: store, Vault: vault, Prober: probe, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	request := SaveProfileRequest{
		OwnerID: "owner-a", ProfileID: profile.LLMProfile, Profile: profile, CredentialID: "credential-a",
		Credential: &profiles.CredentialPayload{APIKey: "fixture-provider-secret", Headers: map[string]string{"X-Safe-Feature": "enabled"}},
	}
	state, err := profileService.Save(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	encodedState, _ := json.Marshal(state)
	if bytes.Contains(encodedState, []byte("fixture-provider-secret")) || bytes.Contains(encodedState, []byte("ciphertext")) || !state.Credential.Configured {
		t.Fatalf("profile state exposed credential internals: %s", encodedState)
	}
	storedProfile, err := store.Profile(ctx, "owner-a", profile.LLMProfile)
	if err != nil {
		t.Fatal(err)
	}
	storedCredential, err := store.Credential(ctx, "owner-a", "credential-a")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(storedProfile.Document, []byte("fixture-provider-secret")) || bytes.Contains(storedCredential.Ciphertext, []byte("fixture-provider-secret")) || storedCredential.Origin != "https://api.openai.com" {
		t.Fatalf("plaintext credential persisted: profile=%s credential=%#v", storedProfile.Document, storedCredential)
	}
	opened, err := profileService.Credential(ctx, "owner-a", "credential-a")
	if err != nil || opened.APIKey != "fixture-provider-secret" || opened.Headers["X-Safe-Feature"] != "enabled" {
		t.Fatalf("credential could not be reopened: %#v %v", opened, err)
	}
	if _, err := profileService.Credential(ctx, "owner-b", "credential-a"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("cross-owner credential read = %v", err)
	}

	originalProfile := append([]byte(nil), storedProfile.Document...)
	originalCiphertext := append([]byte(nil), storedCredential.Ciphertext...)
	changed := profile
	changed.ModelID = "must-not-persist"
	probe.err = errors.New("provider probe failed")
	probe.during = func() {
		visible, readErr := store.Profile(ctx, "owner-a", profile.LLMProfile)
		if readErr != nil || !jsonBytesEqual(visible.Document, originalProfile) {
			t.Errorf("prior profile was unavailable during probe: %s %v", visible.Document, readErr)
		}
	}
	request.Profile = changed
	request.Credential = &profiles.CredentialPayload{APIKey: "must-not-persist"}
	if _, err := profileService.Save(ctx, request); err == nil {
		t.Fatal("failed profile probe was accepted")
	}
	afterProfile, _ := store.Profile(ctx, "owner-a", profile.LLMProfile)
	afterCredential, _ := store.Credential(ctx, "owner-a", "credential-a")
	if !jsonBytesEqual(afterProfile.Document, originalProfile) || !bytes.Equal(afterCredential.Ciphertext, originalCiphertext) {
		t.Fatal("failed probe partially modified profile state")
	}

	probe.err = nil
	probe.during = nil
	chatTokensParam := "max_completion_tokens"
	sharedProfile := profile
	sharedProfile.LLMProfile = "SharedChat"
	sharedProfile.APIInferenceType = "chat-completions"
	sharedProfile.ModelID = "gpt-chat"
	sharedProfile.TokensParam = &chatTokensParam
	sharedProfile.ResponsesTokensParam = nil
	sharedProfile.BackupProfiles = nil
	if _, err := profileService.Save(ctx, SaveProfileRequest{
		OwnerID: "owner-a", ProfileID: sharedProfile.LLMProfile, Profile: sharedProfile, CredentialID: "credential-a",
	}); err != nil {
		t.Fatalf("save profile sharing a credential: %v", err)
	}
	runtimeCatalog, runtimeCredentials, err := profileService.RuntimeProfiles(ctx, "owner-a")
	if err != nil || len(runtimeCatalog) != 2 {
		t.Fatalf("load profiles sharing a credential: profiles=%d error=%v", len(runtimeCatalog), err)
	}
	runtimeCredential, err := runtimeCredentials.ResolveCredential(ctx, hardenllm.CredentialRequest{
		OwnerID: "owner-a", BaseURL: sharedProfile.BaseURL, Scope: sharedProfile.EndpointCredentialScope,
		APIInferenceType: sharedProfile.APIInferenceType,
	})
	if err != nil || runtimeCredential.APIKey != "fixture-provider-secret" {
		t.Fatalf("resolve shared runtime credential: %#v %v", runtimeCredential, err)
	}
	assertCredentialInferenceTypes(t, ctx, store, "credential-a", "chat-completions", "responses")

	beforeOriginChangeProfile, err := store.Profile(ctx, "owner-a", profile.LLMProfile)
	if err != nil {
		t.Fatal(err)
	}
	beforeOriginChangeCredential, err := store.Credential(ctx, "owner-a", "credential-a")
	if err != nil {
		t.Fatal(err)
	}
	changedOrigin := profile
	changedOrigin.BaseURL = "https://example.test/v1"
	if _, err := profileService.Save(ctx, SaveProfileRequest{
		OwnerID: "owner-a", ProfileID: changedOrigin.LLMProfile, Profile: changedOrigin, CredentialID: "credential-a",
	}); err == nil {
		t.Fatal("shared credential origin change was accepted")
	}
	afterOriginChangeProfile, _ := store.Profile(ctx, "owner-a", profile.LLMProfile)
	afterOriginChangeCredential, _ := store.Credential(ctx, "owner-a", "credential-a")
	if !jsonBytesEqual(beforeOriginChangeProfile.Document, afterOriginChangeProfile.Document) ||
		!bytes.Equal(beforeOriginChangeCredential.Ciphertext, afterOriginChangeCredential.Ciphertext) ||
		!jsonBytesEqual(beforeOriginChangeCredential.Metadata, afterOriginChangeCredential.Metadata) {
		t.Fatal("rejected shared credential origin change modified persisted state")
	}

	if _, err := profileService.Save(ctx, SaveProfileRequest{
		OwnerID: "owner-a", ProfileID: sharedProfile.LLMProfile, Profile: sharedProfile, CredentialID: "credential-b",
		Credential: &profiles.CredentialPayload{APIKey: "second-fixture-provider-secret"},
	}); err != nil {
		t.Fatalf("move profile to a new credential: %v", err)
	}
	assertCredentialInferenceTypes(t, ctx, store, "credential-a", "responses")
	assertCredentialInferenceTypes(t, ctx, store, "credential-b", "chat-completions")
	if err := profileService.Delete(ctx, "owner-a", profile.LLMProfile); err != nil {
		t.Fatalf("delete final profile for first credential: %v", err)
	}
	if _, err := store.Credential(ctx, "owner-a", "credential-a"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("orphaned first credential remained after delete: %v", err)
	}
	if err := profileService.Delete(ctx, "owner-a", sharedProfile.LLMProfile); err != nil {
		t.Fatalf("delete final profile for second credential: %v", err)
	}
	if _, err := store.Credential(ctx, "owner-a", "credential-b"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("orphaned second credential remained after delete: %v", err)
	}

	privateProfile := profile
	privateProfile.LLMProfile = "Private"
	privateProfile.BaseURL = "https://127.0.0.1/v1"
	privateProfile.BackupProfiles = nil
	dials := 0
	rootProber := RootProfileProber{EndpointPolicy: hardenllm.EndpointPolicy{DialContext: func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("unexpected dial")
	}}}
	privateService, err := NewProfileService(ProfileServiceConfig{Store: store, Vault: vault, Prober: rootProber, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	_, err = privateService.Save(ctx, SaveProfileRequest{
		OwnerID: "owner-b", ProfileID: "Private", Profile: privateProfile, CredentialID: "credential-private",
		Credential: &profiles.CredentialPayload{APIKey: "private-fixture-secret"},
	})
	if err == nil || dials != 0 {
		t.Fatalf("unsafe endpoint reached a dial: dials=%d error=%v", dials, err)
	}
	if _, err := store.Profile(ctx, "owner-b", "Private"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("unsafe profile was persisted: %v", err)
	}
}

func assertCredentialInferenceTypes(t *testing.T, ctx context.Context, store *postgres.Store, credentialID string, want ...string) {
	t.Helper()
	record, err := store.Credential(ctx, "owner-a", credentialID)
	if err != nil {
		t.Fatal(err)
	}
	var metadata credentialMetadata
	if err := json.Unmarshal(record.Metadata, &metadata); err != nil {
		t.Fatalf("decode credential metadata: %v", err)
	}
	if strings.Join(metadata.APIInferenceTypes, ",") != strings.Join(want, ",") {
		t.Fatalf("credential %s inference types = %v, want %v", credentialID, metadata.APIInferenceTypes, want)
	}
}

type recordingProber struct {
	calls  int
	err    error
	during func()
}

func (prober *recordingProber) Probe(context.Context, profiles.Profile, profiles.CredentialPayload) error {
	prober.calls++
	if prober.during != nil {
		prober.during()
	}
	return prober.err
}

func sourceProfile(t *testing.T) profiles.Profile {
	t.Helper()
	contents, err := os.ReadFile("../../fixtures/parity/generated/profile-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(contents, &fixture); err != nil {
		t.Fatal(err)
	}
	catalog, err := profiles.ParseCatalog(fixture.Input)
	if err != nil {
		t.Fatal(err)
	}
	return catalog["Backup"]
}

func assertTokenNotPersisted(t *testing.T, ctx context.Context, dsn string, tokens ...string) {
	t.Helper()
	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(ctx)
	rows, err := connection.Query(ctx, `SELECT token_digest FROM user_sessions`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var digest []byte
		if err := rows.Scan(&digest); err != nil {
			t.Fatal(err)
		}
		for _, token := range tokens {
			if bytes.Contains(digest, []byte(token)) || strings.Contains(string(digest), token) {
				t.Fatalf("raw token persisted: %q", token)
			}
		}
	}
}

func jsonBytesEqual(left, right []byte) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil && jsonEqualValue(leftValue, rightValue)
}

func jsonEqualValue(left, right any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}
