package profiles

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-018

func TestCredentialBundleEncryptionAndAAD(t *testing.T) {
	t.Parallel()
	keyOne := bytes.Repeat([]byte{0x11}, 32)
	keyTwo := bytes.Repeat([]byte{0x22}, 32)
	nonces := append(bytes.Repeat([]byte{0x01}, 12), bytes.Repeat([]byte{0x02}, 12)...)
	vault, err := NewCredentialVault("key-1", map[string][]byte{"key-1": keyOne, "key-2": keyTwo}, bytes.NewReader(nonces))
	if err != nil {
		t.Fatalf("NewCredentialVault: %v", err)
	}
	binding := CredentialBinding{OwnerID: "owner-1", CredentialID: "credential-1", Origin: "https://api.example:443"}
	payload := CredentialPayload{APIKey: "fake-provider-secret", Headers: map[string]string{"X-Secret": "fake-header-secret"}}
	first, err := vault.Seal(payload, binding)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := vault.Seal(payload, binding)
	if err != nil {
		t.Fatalf("Seal second: %v", err)
	}
	if first.KeyID != "key-1" || first.Algorithm != "AES-256-GCM" || first.Nonce == second.Nonce {
		t.Fatalf("invalid encrypted records: %#v %#v", first, second)
	}
	opened, err := vault.Open(first, binding)
	if err != nil || opened.APIKey != payload.APIKey || opened.Headers["X-Secret"] != payload.Headers["X-Secret"] {
		t.Fatalf("Open mismatch: %#v %v", opened, err)
	}

	wrongBindings := []CredentialBinding{
		{OwnerID: "other", CredentialID: binding.CredentialID, Origin: binding.Origin},
		{OwnerID: binding.OwnerID, CredentialID: "other", Origin: binding.Origin},
		{OwnerID: binding.OwnerID, CredentialID: binding.CredentialID, Origin: "https://other.example"},
	}
	for _, wrong := range wrongBindings {
		if _, err = vault.Open(first, wrong); err == nil {
			t.Fatalf("wrong AAD binding was accepted: %#v", wrong)
		}
	}
	tampered := first
	tampered.Ciphertext = tampered.Ciphertext[:len(tampered.Ciphertext)-1] + map[bool]string{true: "A", false: "B"}[strings.HasSuffix(tampered.Ciphertext, "A")]
	if _, err = vault.Open(tampered, binding); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
	wrongKeyVault, err := NewCredentialVault("key-1", map[string][]byte{"key-1": keyTwo}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wrongKeyVault.Open(first, binding); err == nil {
		t.Fatal("wrong key was accepted")
	}
	duplicateMaterialVault, err := NewCredentialVault("key-1", map[string][]byte{"key-1": keyOne, "key-2": keyOne}, nil)
	if err != nil {
		t.Fatal(err)
	}
	relabelled := first
	relabelled.KeyID = "key-2"
	if _, err = duplicateMaterialVault.Open(relabelled, binding); err == nil {
		t.Fatal("authenticated key ID was relabelled despite duplicate key material")
	}
	for _, origin := range []string{
		"http://api.example", "https://user:pass@api.example", "https://api.example/v1",
		"https://api.example?key=secret", "https://api.example#fragment", "https://api.example:99999",
	} {
		invalid := binding
		invalid.Origin = origin
		if _, err = vault.Seal(payload, invalid); err == nil {
			t.Fatalf("non-origin credential binding was accepted: %q", origin)
		}
	}
}

func TestCredentialBundleCanonicalRoundTripAndPublicState(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x33}, 32)
	vault, err := NewCredentialVault("key-live", map[string][]byte{"key-live": key}, bytes.NewReader(bytes.Repeat([]byte{0x04}, 24)))
	if err != nil {
		t.Fatal(err)
	}
	binding := CredentialBinding{OwnerID: "owner-1", CredentialID: "credential-1", Origin: "https://api.example"}
	encrypted, err := vault.Seal(CredentialPayload{APIKey: "fake-provider-secret"}, binding)
	if err != nil {
		t.Fatal(err)
	}
	record := CredentialRecord{
		SchemaVersion: 1, Binding: binding, Scope: "user", APIInferenceTypes: []string{"responses"},
		Encrypted: encrypted, CreatedAt: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC),
	}
	bundle, err := vault.ExportBundle([]CredentialRecord{record}, "bundle-1", time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}
	if !json.Valid(bundle) || bytes.Contains(bundle, []byte("fake-provider-secret")) {
		t.Fatalf("invalid or plaintext bundle: %s", bundle)
	}
	imported, err := vault.ImportBundle(bundle, "owner-1")
	if err != nil || len(imported) != 1 {
		t.Fatalf("ImportBundle: %#v %v", imported, err)
	}
	opened, err := vault.Open(imported[0].Encrypted, imported[0].Binding)
	if err != nil || opened.APIKey != "fake-provider-secret" {
		t.Fatalf("imported credential could not be opened: %#v %v", opened, err)
	}
	if _, err = vault.ImportBundle(bundle, "other-owner"); err == nil {
		t.Fatal("bundle imported under a different owner")
	}

	state := PublicCredentialState(record)
	encodedState, _ := json.Marshal(state)
	for _, forbidden := range []string{"fake-provider-secret", encrypted.Ciphertext, encrypted.Nonce, "ciphertext", "nonce"} {
		if strings.Contains(string(encodedState), forbidden) {
			t.Fatalf("public state exposed encrypted internals: %s", encodedState)
		}
	}
	if !state.Configured || state.CredentialID != binding.CredentialID || state.Origin != binding.Origin {
		t.Fatalf("invalid public state: %#v", state)
	}
}
