//go:build integration

package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-017 TEST-062

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
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

func TestSharedProfilesRetireEndpointPresets(t *testing.T) {
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
	vault, _ := profiles.NewCredentialVault("test", map[string][]byte{"test": bytes.Repeat([]byte{9}, 32)}, nil)
	service, err := NewProfileService(ProfileServiceConfig{Store: store, Vault: vault, Prober: &recordingProber{}})
	if err != nil {
		t.Fatal(err)
	}
	catalog, _ := profiles.DefaultCatalog()
	old := SharedProfiles{Profiles: profiles.Catalog{}, Credentials: map[string]profiles.CredentialPayload{}}
	for _, name := range []string{"Retired A", "Retired B"} {
		p := catalog["Perplexity GPT-6.1 Sol"]
		p.LLMProfile = name
		old.Profiles[name] = p
		old.Credentials[name] = profiles.CredentialPayload{APIKey: "fixture-endpoint-key"}
	}
	for _, owner := range []string{"retire-a", "retire-b"} {
		if err := ApplySharedProfiles(ctx, store, vault, owner, old); err != nil {
			t.Fatal(err)
		}
	}
	otherProfiles, _ := store.Profiles(ctx, "retire-b")
	otherCredentials, _ := store.Credentials(ctx, "retire-b")
	current := SharedProfiles{Profiles: profiles.Catalog{"Perplexity GPT-6.1 Sol": catalog["Perplexity GPT-6.1 Sol"]}, Credentials: map[string]profiles.CredentialPayload{"Perplexity GPT-6.1 Sol": {APIKey: "fixture-endpoint-key"}}}
	if err := ApplySharedProfiles(ctx, store, vault, "retire-a", current); err != nil {
		t.Fatal(err)
	}
	// The retained endpoint presets now share the current credential. Their old
	// encrypted credential records must be deleted in the same transaction.
	credentials, err := store.Credentials(ctx, "retire-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 {
		t.Fatalf("endpoint rebinding retained unused credentials: got %d, want 1", len(credentials))
	}
	for _, name := range []string{"Retired A", "Retired B"} {
		if err := service.Delete(ctx, "retire-a", name); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		states, err := service.Profiles(ctx, "retire-a")
		if err != nil {
			t.Fatal("profile read after retirement", err)
		}
		if len(states) != 1 || states[0].Profile.LLMProfile != "Perplexity GPT-6.1 Sol" || !states[0].Credential.Configured {
			t.Fatalf("unexpected retained profile: %+v", states)
		}
	}
	if _, _, err := service.RuntimeProfiles(ctx, "retire-a"); err != nil {
		t.Fatal("runtime profiles after retirement", err)
	}
	if result, err := ApplySharedProfilesWithResult(ctx, store, vault, "retire-a", current); err != nil || result.Changed {
		t.Fatalf("retired catalog sync is not idempotent: %+v %v", result, err)
	}
	// A previous deployment may already have left invalid, unreferenced
	// metadata. Unchanged provisioning must repair that storage state too.
	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(ctx)
	if _, err := connection.Exec(ctx, `
		INSERT INTO llm_endpoint_credentials
		SELECT $1, credential_id, key_id, nonce, ciphertext, normalized_origin,
			jsonb_set(metadata, '{apiInferenceTypes}', '[]'::jsonb), created_at, updated_at
		FROM llm_endpoint_credentials WHERE owner_id=$2`, "retire-a", "retire-b"); err != nil {
		t.Fatal(err)
	}
	if result, err := ApplySharedProfilesWithResult(ctx, store, vault, "retire-a", current); err != nil || !result.Changed {
		t.Fatalf("unchanged sync did not remove preexisting unused credentials: %+v %v", result, err)
	}
	if states, err := service.Profiles(ctx, "retire-a"); err != nil || len(states) != 1 {
		t.Fatalf("profile read after preexisting credential cleanup: %d %v", len(states), err)
	}
	if credentials, err := store.Credentials(ctx, "retire-a"); err != nil || len(credentials) != 1 {
		t.Fatalf("preexisting unused credentials remain: %d %v", len(credentials), err)
	}
	if result, err := ApplySharedProfilesWithResult(ctx, store, vault, "retire-a", current); err != nil || result.Changed {
		t.Fatalf("repaired sync is not idempotent: %+v %v", result, err)
	}
	if err := service.Delete(ctx, "retire-a", "Perplexity GPT-6.1 Sol"); err != nil {
		t.Fatal(err)
	}
	credentials, err = store.Credentials(ctx, "retire-a")
	if err != nil || len(credentials) != 0 {
		t.Fatalf("last profile left credentials: %d %v", len(credentials), err)
	}
	profilesAfter, err := store.Profiles(ctx, "retire-b")
	if err != nil {
		t.Fatal(err)
	}
	credentialsAfter, err := store.Credentials(ctx, "retire-b")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(otherProfiles, profilesAfter) || !reflect.DeepEqual(otherCredentials, credentialsAfter) {
		t.Fatal("retirement changed the other owner")
	}
}
