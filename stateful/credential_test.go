package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-444
// Encryption/payload/origin oracles moved from baseline TEST-018 at 754690c.

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCredentialContract(t *testing.T) {
	t.Parallel()
	var vector struct{ Key, Nonce, Ciphertext string }
	data, err := os.ReadFile("../test/fixtures/reference/credential-vector.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &vector); err != nil {
		t.Fatal(err)
	}
	v, err := NewCredentialVault(vector.Key)
	if err != nil {
		t.Fatal(err)
	}
	v.random = bytes.NewReader(append(bytes.Repeat([]byte{1}, 12), bytes.Repeat([]byte{2}, 12)...))
	binding := CredentialBinding{APIKeyHash: strings.Repeat("a", 64), CredentialID: "credential-1", Origin: "https://api.example:443"}
	payload := CredentialPayload{APIKey: "fake-provider-secret", Headers: map[string]string{"X-Secret": "fake-header-secret"}}
	first, err := v.Seal(payload, binding)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("same binding opens original secret and uses fresh nonces", func(t *testing.T) {
		second, err := v.Seal(payload, binding)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := v.Open(first, binding)
		if err != nil || opened.APIKey != payload.APIKey || opened.Headers["X-Secret"] != payload.Headers["X-Secret"] || first.KeyID != "primary" || first.Algorithm != "AES-256-GCM" || first.Nonce == second.Nonce {
			t.Fatal("encryption roundtrip, framing or nonce invariant failed", err)
		}
	})
	t.Run("each authenticated binding and ciphertext mismatch fails", func(t *testing.T) {
		for _, name := range []string{"api_key_hash", "credential_id", "origin", "key_id", "ciphertext"} {
			t.Run(name, func(t *testing.T) {
				changedBinding := binding
				changed := first
				switch name {
				case "api_key_hash":
					changedBinding.APIKeyHash = strings.Repeat("b", 64)
				case "credential_id":
					changedBinding.CredentialID = "other"
				case "origin":
					changedBinding.Origin = "https://other.example"
				case "key_id":
					changed.KeyID = "other"
				case "ciphertext":
					raw, err := base64.RawURLEncoding.DecodeString(changed.Ciphertext)
					if err != nil {
						t.Fatal(err)
					}
					raw[0] ^= 1
					changed.Ciphertext = base64.RawURLEncoding.EncodeToString(raw)
				}
				if _, err := v.Open(changed, changedBinding); err == nil {
					t.Fatal("mismatched associated data accepted")
				}
			})
		}
		wrongKey, err := NewCredentialVault(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = wrongKey.Open(first, binding); err == nil {
			t.Fatal("different encryption key accepted")
		}
	})
	t.Run("key A credential cannot open under key B", func(t *testing.T) {
		other := binding
		other.APIKeyHash = strings.Repeat("b", 64)
		if _, err := v.Open(first, other); err == nil {
			t.Fatal("cross-key encrypted credential accepted")
		}
	})
	t.Run("AAD framing is not concatenation", func(t *testing.T) {
		a, err := credentialAAD(CredentialBinding{APIKeyHash: "ab", CredentialID: "c", Origin: "https://api.example"}, "primary")
		if err != nil {
			t.Fatal(err)
		}
		b, err := credentialAAD(CredentialBinding{APIKeyHash: "a", CredentialID: "bc", Origin: "https://api.example"}, "primary")
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(a, b) {
			t.Fatal("boundary-shifted bindings have identical AAD")
		}
	})
	t.Run("independent fixed ciphertext and production randomness", func(t *testing.T) {
		if first.Nonce != vector.Nonce || first.Ciphertext != vector.Ciphertext {
			t.Fatalf("encryption differs from independent AESGCM vector: nonce=%q ciphertext=%q", first.Nonce, first.Ciphertext)
		}
		production, err := NewCredentialVault(vector.Key)
		if err != nil || production.random != rand.Reader {
			t.Fatal("production constructor must use crypto/rand", err)
		}
	})
	t.Run("safe reads and encrypted bundle entries exclude plaintext", func(t *testing.T) {
		record := CredentialRecord{Binding: binding, Encrypted: first, Revision: 1, CreatedAt: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
		public, _ := json.Marshal(PublicCredentialState(record))
		entry, _ := json.Marshal(record)
		for _, sentinel := range []string{payload.APIKey, payload.Headers["X-Secret"]} {
			if bytes.Contains(public, []byte(sentinel)) || bytes.Contains(entry, []byte(sentinel)) {
				t.Fatal("plaintext in read or encrypted bundle")
			}
		}
		for _, field := range []string{"ciphertext", "nonce", first.Ciphertext, first.Nonce} {
			if bytes.Contains(public, []byte(field)) {
				t.Fatal("credential read exposes encryption internals")
			}
		}
		status := PublicCredentialState(record)
		if !status.Configured || status.ID != binding.CredentialID || status.Revision != 1 {
			t.Fatal("credential status lost configuration")
		}
	})
	t.Run("missing entered credential is explicit without substitution", func(t *testing.T) {
		_, err := v.openRecord(nil, binding)
		var fieldError *ValidationError
		if !errors.As(err, &fieldError) || fieldError.Code != "credential_invalid" {
			t.Fatalf("missing credential must be credential_invalid: %v", err)
		}
	})
	t.Run("constructor accepts only standard base64 of 32 bytes", func(t *testing.T) {
		for _, key := range []string{"", "not-base64", base64.StdEncoding.EncodeToString(make([]byte, 31)), base64.StdEncoding.EncodeToString(make([]byte, 33)), base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{255}, 32))} {
			if _, err := NewCredentialVault(key); err == nil {
				t.Errorf("invalid encryption material accepted; length=%d", len(key))
			}
		}
	})
	t.Run("retained payload and HTTPS origin validation", func(t *testing.T) {
		for _, origin := range []string{"http://api.example", "https://user:pass@api.example", "https://api.example/v1", "https://api.example?key=secret", "https://api.example#fragment", "https://api.example:99999"} {
			invalid := binding
			invalid.Origin = origin
			if _, err := v.Seal(payload, invalid); err == nil {
				t.Fatal("non-origin accepted")
			}
		}
		for _, p := range []CredentialPayload{{APIKey: " leading-space"}, {APIKey: "key\r\ninjected"}, {APIKey: "key", Headers: map[string]string{"Bad Header": "value"}}, {APIKey: "key", Headers: map[string]string{"X-Test": "value", "x-test": "other"}}, {APIKey: "key", Headers: map[string]string{"X-Test": "value\r\ninjected"}}} {
			if _, err := v.Seal(p, binding); err == nil {
				t.Fatal("invalid credential payload accepted")
			}
		}
	})
}
