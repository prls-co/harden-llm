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
	seed, err := profiles.DefaultCatalog()
	if err != nil {
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
			got, err := service.Profiles(ctx, "seed-owner")
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
		if len(got) != len(wantNames) {
			t.Fatalf("seeded profile count = %d, want %d", len(got), len(wantNames))
		}
		for index, state := range got {
			if state.Profile.LLMProfile != wantNames[index] || state.Credential.Configured {
				t.Fatalf("seeded state[%d] = %#v, want unconfigured %s", index, state, wantNames[index])
			}
		}
	}

	custom := seed["CPA GPT-5.6 Luna"]
	custom.LLMProfile = "Operator Profile"
	document, err := json.Marshal(custom)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProfile(ctx, postgres.ProfileRecord{
		OwnerID: "seed-owner", ID: custom.LLMProfile, Document: document, CreatedAt: now, UpdatedAt: now,
	}, nil); err != nil {
		t.Fatal(err)
	}
	final, err := service.Profiles(ctx, "seed-owner")
	if err != nil {
		t.Fatal(err)
	}
	if len(final) != len(seed)+1 || !slices.ContainsFunc(final, func(state ProfileState) bool { return state.Profile.LLMProfile == custom.LLMProfile }) {
		t.Fatalf("seed overwrote or lost operator profile: %v", final)
	}

	first := seed["CPA GPT-5.6 Luna"]
	second := first
	second.EndpointCredentialScope = "user"
	if got := credentialIDForProfile(first); got == "" || got != credentialIDForProfile(first) || got == credentialIDForProfile(second) {
		t.Fatal("credential ID derivation is not deterministic and scope-bound")
	}
	if _, err := service.Credential(ctx, "seed-owner", "missing"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("missing seed credential lookup = %v", err)
	}
}
