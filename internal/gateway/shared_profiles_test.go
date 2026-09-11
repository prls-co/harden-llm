package gateway

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-017 TEST-062

import (
	"bytes"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/profiles"
)

func TestSharedProfileRecords(t *testing.T) {
	t.Parallel()
	catalog, err := profiles.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	vault, err := profiles.NewCredentialVault("test", map[string][]byte{"test": bytes.Repeat([]byte{7}, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := SharedProfiles{Profiles: catalog, Credentials: map[string]profiles.CredentialPayload{
		"CPA GPT-5.6 Luna": {APIKey: "fixture-key"},
	}}
	rows, credentials, err := sharedProfileRecords(config, "owner", vault, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(catalog) || len(credentials) != 1 {
		t.Fatal("catalog or credentials lost")
	}
	for _, row := range rows {
		if row.OwnerID != "owner" {
			t.Fatal("owner mismatch")
		}
		if (row.ID == "CPA GPT-5.6 Luna") != (row.CredentialID != "") {
			t.Fatal("credential scope escaped configured profile")
		}
	}
	public, err := publicCredentialRecord(credentials[0])
	if err != nil {
		t.Fatal(err)
	}
	payload, err := vault.Open(public.Encrypted, public.Binding)
	if err != nil || payload.APIKey != "fixture-key" {
		t.Fatal("encrypted key did not round trip")
	}
	if bytes.Contains(credentials[0].Ciphertext, []byte("fixture-key")) {
		t.Fatal("plaintext stored")
	}
	config.Credentials["missing"] = profiles.CredentialPayload{APIKey: "fixture-key"}
	if _, _, err := sharedProfileRecords(config, "owner", vault, time.Now()); err == nil {
		t.Fatal("unknown profile credential accepted")
	}
	delete(config.Credentials, "missing")
	config.Credentials["CPA GPT-5.6 Luna"] = profiles.CredentialPayload{}
	if _, _, err := sharedProfileRecords(config, "owner", vault, time.Now()); err == nil {
		t.Fatal("empty credential accepted")
	}
}
