//go:build integration

package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-017 TEST-062

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

func TestSharedProfilesProvisionAndRotate(t *testing.T) {
	t.Parallel()
	_, dsn := integrationtest.PostgresLease(t)
	ctx := context.Background()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"shared-a", "shared-b", "shared-c"} {
		if err := store.CreateUser(ctx, postgres.User{ID: owner, Email: owner + "@example.test", PasswordHash: "$argon2id$v=19$fixture", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	vault, _ := profiles.NewCredentialVault("test", map[string][]byte{"test": bytes.Repeat([]byte{9}, 32)}, nil)
	catalog, _ := profiles.DefaultCatalog()
	prober := &recordingProber{}
	service, err := NewProfileService(ProfileServiceConfig{Store: store, Vault: vault, Prober: prober})
	if err != nil {
		t.Fatal(err)
	}
	config := SharedProfiles{Profiles: catalog, Credentials: map[string]profiles.CredentialPayload{"CPA GPT-5.6 Luna": {APIKey: "fixture-original"}}}
	for _, owner := range []string{"shared-a", "shared-b"} {
		result, err := ApplySharedProfilesWithResult(ctx, store, vault, owner, config)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Changed || result.Profiles != len(catalog) || result.Configured != len(config.Credentials) {
			t.Fatalf("unexpected initial sync result: %+v", result)
		}
	}
	beforeProfiles, err := store.Profiles(ctx, "shared-b")
	if err != nil {
		t.Fatal(err)
	}
	beforeCredentials, err := store.Credentials(ctx, "shared-b")
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplySharedProfilesWithResult(ctx, store, vault, "shared-b", config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatalf("unchanged configuration was written: %+v", result)
	}
	afterProfiles, err := store.Profiles(ctx, "shared-b")
	if err != nil {
		t.Fatal(err)
	}
	afterCredentials, err := store.Credentials(ctx, "shared-b")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeProfiles, afterProfiles) || !reflect.DeepEqual(beforeCredentials, afterCredentials) {
		t.Fatal("idempotent sync changed persisted profile or credential records")
	}
	duplicateGroups := make(map[runtimeCredentialKey][]string)
	for name, profile := range catalog {
		origin, err := profileOrigin(profile.BaseURL)
		if err != nil {
			t.Fatal(err)
		}
		key := runtimeCredentialKey{Origin: origin, Scope: profile.EndpointCredentialScope, APIInferenceType: profile.APIInferenceType}
		duplicateGroups[key] = append(duplicateGroups[key], name)
	}
	var duplicateNames []string
	for _, names := range duplicateGroups {
		if len(names) > 1 {
			slices.Sort(names)
			duplicateNames = names[:2]
			break
		}
	}
	if len(duplicateNames) != 2 {
		t.Fatal("catalog fixture must cover duplicate runtime credential keys")
	}
	duplicateConfig := SharedProfiles{Profiles: catalog, Credentials: map[string]profiles.CredentialPayload{
		duplicateNames[0]: {APIKey: "fixture-duplicate"},
		duplicateNames[1]: {APIKey: "fixture-duplicate"},
	}}
	if result, err := ApplySharedProfilesWithResult(ctx, store, vault, "shared-c", duplicateConfig); err != nil || !result.Changed {
		t.Fatalf("duplicate-key initial sync failed: result=%+v err=%v", result, err)
	}
	if result, err := ApplySharedProfilesWithResult(ctx, store, vault, "shared-c", duplicateConfig); err != nil || result.Changed {
		t.Fatalf("duplicate-key sync was not idempotent: result=%+v err=%v", result, err)
	}
	custom := catalog["CPA GPT-5.6 Luna"]
	custom.LLMProfile = "Keep custom"
	if _, err := service.Save(ctx, SaveProfileRequest{OwnerID: "shared-a", ProfileID: custom.LLMProfile, Profile: custom, Credential: &profiles.CredentialPayload{APIKey: "fixture-original"}}); err != nil {
		t.Fatal(err)
	}
	// Reapplication must not delete custom rows and must leave the other owner alone.
	if err := ApplySharedProfiles(ctx, store, vault, "shared-a", config); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Profile(ctx, "shared-a", "Keep custom"); err != nil {
		t.Fatal("custom profile lost", err)
	}
	config.Credentials["CPA GPT-5.6 Luna"] = profiles.CredentialPayload{APIKey: "fixture-rotated"}
	if err := ApplySharedProfiles(ctx, store, vault, "shared-a", config); err != nil {
		t.Fatal(err)
	}
	retained, err := service.Profile(ctx, "shared-a", "Keep custom")
	if err != nil || retained.Profile.ModelID != custom.ModelID {
		t.Fatal("custom model changed during key rotation", err)
	}
	for owner, expected := range map[string]string{"shared-a": "fixture-rotated", "shared-b": "fixture-original"} {
		_, resolver, err := service.RuntimeProfiles(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		p := catalog["CPA GPT-5.6 Luna"]
		credential, err := resolver.ResolveCredential(ctx, hardenllm.CredentialRequest{OwnerID: owner, BaseURL: p.BaseURL, Scope: p.EndpointCredentialScope, APIInferenceType: p.APIInferenceType})
		if err != nil || credential.APIKey != expected {
			t.Fatal("runtime credential mismatch", err)
		}
		if _, err := resolver.ResolveCredential(ctx, hardenllm.CredentialRequest{OwnerID: "another", BaseURL: p.BaseURL, Scope: p.EndpointCredentialScope, APIInferenceType: p.APIInferenceType}); err == nil {
			t.Fatal("cross-owner credential access")
		}
	}
	config.Credentials["missing"] = profiles.CredentialPayload{APIKey: "fixture-invalid"}
	if err := ApplySharedProfiles(ctx, store, vault, "shared-a", config); err == nil {
		t.Fatal("invalid update succeeded")
	}
	state, err := service.Profile(ctx, "shared-a", "CPA GPT-5.6 Luna")
	if err != nil || !state.Credential.Configured {
		t.Fatal("failed update damaged previous configuration", err)
	}
	delete(config.Credentials, "missing")
	delete(config.Credentials, "CPA GPT-5.6 Luna")
	if err := ApplySharedProfiles(ctx, store, vault, "shared-a", config); err != nil {
		t.Fatal(err)
	}
	state, err = service.Profile(ctx, "shared-a", "CPA GPT-5.6 Luna")
	if err != nil || state.Credential.Configured {
		t.Fatal("removed binding remained usable", err)
	}
	if prober.calls != 1 {
		t.Fatal("provisioning contacted a provider")
	}
}
