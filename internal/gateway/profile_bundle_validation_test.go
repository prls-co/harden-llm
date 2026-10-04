package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-018 TEST-022 TEST-024
import (
	"bytes"
	"context"
	"errors"
	"github.com/prls-co/harden-llm/internal/profiles"
	"testing"
	"time"
)

func TestForeignBundleRejectedBeforeProbeOrStorage(t *testing.T) {
	vault, err := profiles.NewCredentialVault("test", map[string][]byte{"test": bytes.Repeat([]byte{0x33}, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := profiles.CredentialBinding{OwnerID: "User_Alpha", CredentialID: "same-id", Origin: "https://provider.example.test"}
	sealed, err := vault.Seal(profiles.CredentialPayload{APIKey: "synthetic-provider-key"}, binding)
	if err != nil {
		t.Fatal(err)
	}
	// Nil storage/prober makes any access past validation fail immediately.
	service := &ProfileService{vault: vault}
	_, err = service.ReplaceBundle(context.Background(), "User_Beta", ProfileBundle{SchemaVersion: profileBundleSchemaVersion, BundleID: "test", CreatedAt: time.Now(), Profiles: profiles.Catalog{}, CredentialIDs: map[string]string{}, Credentials: []profiles.CredentialRecord{{SchemaVersion: 1, Binding: binding, Scope: "user", APIInferenceTypes: []string{"responses"}, Encrypted: sealed, CreatedAt: time.Now()}}})
	var invalid *profiles.ValidationError
	if !errors.As(err, &invalid) || len(invalid.FieldErrors) != 1 || invalid.FieldErrors[0].Field != "credentials" {
		t.Fatalf("expected credential validation error before probe/write, got %T: %v", err, err)
	}
}
