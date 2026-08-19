//go:build integration

package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-017

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

func TestDefaultProfileSeedParity(t *testing.T) {
	_, dsn := integrationtest.StartPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	if err := store.CreateUser(ctx, postgres.User{
		ID: "seed-owner", Email: "seed@example.test", PasswordHash: "$argon2id$v=19$fixture",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(ctx, postgres.User{
		ID: "existing-owner", Email: "existing@example.test", PasswordHash: "$argon2id$v=19$fixture",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	seed, err := profiles.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	custom := seed["CPA GPT-5.6 Luna"]
	custom.LLMProfile = "Operator Profile"
	document, err := json.Marshal(custom)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProfile(ctx, postgres.ProfileRecord{
		OwnerID: "existing-owner", ID: custom.LLMProfile, Document: document, CreatedAt: now, UpdatedAt: now,
	}, nil); err != nil {
		t.Fatal(err)
	}
	vault, err := profiles.NewCredentialVault("seed-key", map[string][]byte{"seed-key": bytes.Repeat([]byte{0x77}, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewProfileService(ProfileServiceConfig{
		Store: store, Vault: vault, Prober: &recordingProber{}, SeedCatalog: seed,
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	const readers = 8
	states := make(chan []ProfileState, readers)
	errorsByReader := make(chan error, readers)
	var wait sync.WaitGroup
	for range readers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			got, err := service.Profiles(ctx, "existing-owner")
			if err != nil {
				errorsByReader <- err
				return
			}
			states <- got
		}()
	}
	wait.Wait()
	close(states)
	close(errorsByReader)
	for err := range errorsByReader {
		t.Fatal(err)
	}
	wantNames := make([]string, 0, len(seed))
	for name := range seed {
		wantNames = append(wantNames, name)
	}
	slices.Sort(wantNames)
	for got := range states {
		if len(got) != len(wantNames)+1 {
			t.Fatalf("merged profile count = %d, want %d", len(got), len(wantNames)+1)
		}
		for _, name := range wantNames {
			if !slices.ContainsFunc(got, func(state ProfileState) bool {
				return state.Profile.LLMProfile == name && !state.Credential.Configured
			}) {
				t.Fatalf("missing unconfigured seeded profile %q", name)
			}
		}
		if !slices.ContainsFunc(got, func(state ProfileState) bool {
			return state.Profile.LLMProfile == custom.LLMProfile
		}) {
			t.Fatalf("existing operator profile was not preserved: %v", got)
		}
	}

	emptyOwner, err := service.Profiles(ctx, "seed-owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyOwner) != len(seed) {
		t.Fatalf("empty owner profile count = %d, want %d", len(emptyOwner), len(seed))
	}
	first := seed["CPA GPT-5.6 Luna"]
	runtimeCatalog, runtimeCredentials, err := service.RuntimeProfiles(ctx, "seed-owner")
	if err != nil {
		t.Fatalf("unconfigured seed rows blocked runtime catalog: %v", err)
	}
	if len(runtimeCatalog) != len(seed) || runtimeCredentials == nil {
		t.Fatalf("runtime catalog = %d credentials-nil=%t, want %d profiles and a resolver", len(runtimeCatalog), runtimeCredentials == nil, len(seed))
	}
	if _, err := runtimeCredentials.ResolveCredential(ctx, hardenllm.CredentialRequest{
		OwnerID: "seed-owner", BaseURL: first.BaseURL, Scope: first.EndpointCredentialScope,
		APIInferenceType: first.APIInferenceType,
	}); !errors.Is(err, ErrCredentialNotConfigured) {
		t.Fatalf("unconfigured seed credential error = %v, want ErrCredentialNotConfigured", err)
	}

	second := first
	second.EndpointCredentialScope = "user"
	if got := credentialIDForProfile(first); got == "" || got != credentialIDForProfile(first) || got == credentialIDForProfile(second) {
		t.Fatal("credential ID derivation is not deterministic and scope-bound")
	}
	if _, err := service.Credential(ctx, "seed-owner", "missing"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("missing seed credential lookup = %v", err)
	}
}
