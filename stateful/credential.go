package stateful

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	credentialEncryptionVersion = 1
	credentialNonceBytes        = 12
)

type CredentialBinding struct {
	APIKeyHash   string `json:"apiKeyHash"`
	CredentialID string `json:"credentialId"`
	Origin       string `json:"origin"`
}
type CredentialPayload struct {
	APIKey  string            `json:"apiKey"`
	Headers map[string]string `json:"headers,omitempty"`
}
type EncryptedCredential struct {
	SchemaVersion int    `json:"schemaVersion"`
	Algorithm     string `json:"algorithm"`
	KeyID         string `json:"keyId"`
	Nonce         string `json:"nonce"`
	Ciphertext    string `json:"ciphertext"`
}
type CredentialRecord struct {
	Binding   CredentialBinding   `json:"binding"`
	Encrypted EncryptedCredential `json:"encrypted"`
	Revision  int64               `json:"revision"`
	CreatedAt time.Time           `json:"createdAt"`
	UpdatedAt time.Time           `json:"updatedAt"`
}
type CredentialVault struct {
	key    []byte
	random io.Reader
}

func NewCredentialVault(encoded string) (*CredentialVault, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 || base64.StdEncoding.EncodeToString(key) != encoded {
		return nil, errors.New("stateful: credential encryption key must be standard base64 of exactly 32 bytes")
	}
	return &CredentialVault{key: key, random: rand.Reader}, nil
}
func (vault *CredentialVault) Seal(payload CredentialPayload, binding CredentialBinding) (EncryptedCredential, error) {
	if vault == nil {
		return EncryptedCredential{}, errors.New("stateful: credential vault is required")
	}
	normalizedBinding, err := normalizeCredentialBinding(binding)
	if err != nil {
		return EncryptedCredential{}, err
	}
	if err := validateCredentialPayload(payload); err != nil {
		return EncryptedCredential{}, err
	}
	plaintext, err := json.Marshal(CredentialPayload{APIKey: payload.APIKey, Headers: cloneHeaderMap(payload.Headers)})
	if err != nil {
		return EncryptedCredential{}, errors.New("stateful: credential payload is not JSON-safe")
	}
	block, err := aes.NewCipher(vault.key)
	if err != nil {
		return EncryptedCredential{}, fmt.Errorf("stateful: initialize credential cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return EncryptedCredential{}, fmt.Errorf("stateful: initialize credential GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(vault.random, nonce); err != nil {
		return EncryptedCredential{}, fmt.Errorf("stateful: generate credential nonce: %w", err)
	}
	aad, err := credentialAAD(normalizedBinding, "primary")
	if err != nil {
		return EncryptedCredential{}, err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, aad)
	return EncryptedCredential{
		SchemaVersion: credentialEncryptionVersion, Algorithm: "AES-256-GCM", KeyID: "primary",
		Nonce: base64.RawURLEncoding.EncodeToString(nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(sealed),
	}, nil
}

func (vault *CredentialVault) Open(encrypted EncryptedCredential, binding CredentialBinding) (CredentialPayload, error) {
	if vault == nil {
		return CredentialPayload{}, errors.New("stateful: credential vault is required")
	}
	if encrypted.SchemaVersion != credentialEncryptionVersion || encrypted.Algorithm != "AES-256-GCM" {
		return CredentialPayload{}, errors.New("stateful: unsupported encrypted credential")
	}
	if encrypted.KeyID != "primary" {
		return CredentialPayload{}, errors.New("stateful: credential encryption key ID is invalid")
	}
	key := vault.key
	nonce, err := decodeBase64URL(encrypted.Nonce, "nonce")
	if err != nil {
		return CredentialPayload{}, err
	}
	ciphertext, err := decodeBase64URL(encrypted.Ciphertext, "ciphertext")
	if err != nil {
		return CredentialPayload{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return CredentialPayload{}, errors.New("stateful: initialize credential cipher")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return CredentialPayload{}, errors.New("stateful: initialize credential GCM")
	}
	if len(nonce) != gcm.NonceSize() || len(ciphertext) < gcm.Overhead() {
		return CredentialPayload{}, errors.New("stateful: encrypted credential is malformed")
	}
	normalizedBinding, err := normalizeCredentialBinding(binding)
	if err != nil {
		return CredentialPayload{}, err
	}
	aad, err := credentialAAD(normalizedBinding, encrypted.KeyID)
	if err != nil {
		return CredentialPayload{}, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return CredentialPayload{}, errors.New("stateful: encrypted credential authentication failed")
	}
	decoder := json.NewDecoder(strings.NewReader(string(plaintext)))
	decoder.DisallowUnknownFields()
	var payload CredentialPayload
	if err = decoder.Decode(&payload); err != nil || validateCredentialPayload(payload) != nil {
		return CredentialPayload{}, errors.New("stateful: decrypted credential payload is invalid")
	}
	if err = requireJSONEOF(decoder); err != nil {
		return CredentialPayload{}, errors.New("stateful: decrypted credential payload is invalid")
	}
	payload.Headers = cloneHeaderMap(payload.Headers)
	return payload, nil
}

func normalizeCredentialBinding(binding CredentialBinding) (CredentialBinding, error) {
	binding.APIKeyHash = strings.TrimSpace(binding.APIKeyHash)
	binding.CredentialID = strings.TrimSpace(binding.CredentialID)
	if !validKeyHash(binding.APIKeyHash) || binding.CredentialID == "" || len(binding.CredentialID) > 128 || !utf8.ValidString(binding.CredentialID) || strings.ContainsAny(binding.CredentialID, "\r\n\x00") {
		return CredentialBinding{}, errors.New("stateful: credential key hash and ID are required")
	}
	origin, err := normalizeOrigin(binding.Origin)
	if err != nil {
		return CredentialBinding{}, err
	}
	binding.Origin = origin
	return binding, nil
}

func normalizeOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || strings.ToLower(parsed.Scheme) != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("stateful: credential origin must be an absolute HTTPS origin")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	port := parsed.Port()
	if port != "" {
		number, portErr := strconv.Atoi(port)
		if portErr != nil || number < 1 || number > 65535 {
			return "", errors.New("stateful: credential origin port is invalid")
		}
	}
	if port == "443" {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	return "https://" + host, nil
}

func credentialAAD(binding CredentialBinding, keyID string) ([]byte, error) {
	return json.Marshal(struct {
		SchemaVersion int    `json:"schemaVersion"`
		Algorithm     string `json:"algorithm"`
		KeyID         string `json:"keyId"`
		APIKeyHash    string `json:"apiKeyHash"`
		CredentialID  string `json:"credentialId"`
		Origin        string `json:"origin"`
	}{credentialEncryptionVersion, "AES-256-GCM", keyID, binding.APIKeyHash, binding.CredentialID, binding.Origin})
}

func decodeBase64URL(value, label string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("stateful: encrypted credential %s is empty", label)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("stateful: encrypted credential %s is malformed", label)
	}
	return decoded, nil
}

func validateCredentialPayload(payload CredentialPayload) error {
	if strings.TrimSpace(payload.APIKey) == "" || strings.TrimSpace(payload.APIKey) != payload.APIKey || !utf8.ValidString(payload.APIKey) ||
		len(payload.APIKey) > 16<<10 || strings.ContainsAny(payload.APIKey, "\r\n\x00") {
		return errors.New("stateful: credential API key is invalid")
	}
	if len(payload.Headers) > 64 {
		return errors.New("stateful: credential has too many headers")
	}
	seen := make(map[string]struct{}, len(payload.Headers))
	for name, value := range payload.Headers {
		lower := strings.ToLower(name)
		if !validCredentialHeaderName(name) || !validCredentialHeaderValue(value) {
			return errors.New("stateful: credential header is invalid")
		}
		if _, duplicate := seen[lower]; duplicate {
			return errors.New("stateful: credential header names are ambiguous")
		}
		seen[lower] = struct{}{}
	}
	return nil
}

func validCredentialHeaderName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range []byte(value) {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func validCredentialHeaderValue(value string) bool {
	if len(value) > 8192 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == '\t' || character >= 0x20 && character != 0x7f {
			continue
		}
		return false
	}
	return true
}

func cloneHeaderMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func validKeyHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func PublicCredentialState(record CredentialRecord) CredentialStatus {
	return CredentialStatus{Configured: record.Encrypted.Ciphertext != "", Source: "entered", ID: record.Binding.CredentialID, Revision: record.Revision}
}
func (vault *CredentialVault) openRecord(record *CredentialRecord, binding CredentialBinding) (CredentialPayload, error) {
	if record == nil {
		return CredentialPayload{}, &ValidationError{Code: "credential_invalid", FieldErrors: []FieldError{{Field: "credential", Message: "Select or provide an entered credential for this access key."}}}
	}
	payload, err := vault.Open(record.Encrypted, binding)
	if err != nil {
		return CredentialPayload{}, &ValidationError{Code: "credential_invalid", FieldErrors: []FieldError{{Field: "credential", Message: "Entered credential cannot be authenticated for this binding."}}}
	}
	return payload, nil
}
