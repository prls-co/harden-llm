package profiles

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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	credentialEncryptionVersion = 1
	credentialBundleVersion     = 1
	credentialNonceBytes        = 12
)

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type CredentialBinding struct {
	OwnerID      string `json:"ownerId"`
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
	SchemaVersion     int                 `json:"schemaVersion"`
	Binding           CredentialBinding   `json:"binding"`
	Scope             string              `json:"scope"`
	APIInferenceTypes []string            `json:"apiInferenceTypes"`
	Encrypted         EncryptedCredential `json:"encrypted"`
	CreatedAt         time.Time           `json:"createdAt"`
	UpdatedAt         time.Time           `json:"updatedAt,omitempty"`
}

type CredentialState struct {
	SchemaVersion     int       `json:"schemaVersion"`
	CredentialID      string    `json:"credentialId"`
	Scope             string    `json:"scope"`
	Origin            string    `json:"origin"`
	APIInferenceTypes []string  `json:"apiInferenceTypes"`
	Configured        bool      `json:"configured"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt,omitempty"`
}

type credentialBundle struct {
	SchemaVersion int                `json:"schemaVersion"`
	BundleID      string             `json:"bundleId"`
	CreatedAt     time.Time          `json:"createdAt"`
	Credentials   []CredentialRecord `json:"credentials"`
}

type CredentialVault struct {
	activeKeyID string
	keys        map[string][]byte
	random      io.Reader
}

func NewCredentialVault(activeKeyID string, keys map[string][]byte, randomSource io.Reader) (*CredentialVault, error) {
	if !keyIDPattern.MatchString(activeKeyID) {
		return nil, errors.New("profiles: active credential key ID is invalid")
	}
	if len(keys) == 0 {
		return nil, errors.New("profiles: at least one credential encryption key is required")
	}
	cloned := make(map[string][]byte, len(keys))
	for keyID, material := range keys {
		if !keyIDPattern.MatchString(keyID) {
			return nil, fmt.Errorf("profiles: credential key ID %q is invalid", keyID)
		}
		if len(material) != 32 {
			return nil, fmt.Errorf("profiles: credential key %q must contain exactly 32 bytes", keyID)
		}
		cloned[keyID] = append([]byte(nil), material...)
	}
	if _, ok := cloned[activeKeyID]; !ok {
		return nil, errors.New("profiles: active credential key is not in the keyring")
	}
	if randomSource == nil {
		randomSource = rand.Reader
	}
	return &CredentialVault{activeKeyID: activeKeyID, keys: cloned, random: randomSource}, nil
}

func (vault *CredentialVault) Seal(payload CredentialPayload, binding CredentialBinding) (EncryptedCredential, error) {
	if vault == nil {
		return EncryptedCredential{}, errors.New("profiles: credential vault is required")
	}
	normalizedBinding, err := normalizeCredentialBinding(binding)
	if err != nil {
		return EncryptedCredential{}, err
	}
	if strings.TrimSpace(payload.APIKey) == "" {
		return EncryptedCredential{}, errors.New("profiles: credential API key is required")
	}
	plaintext, err := json.Marshal(CredentialPayload{APIKey: payload.APIKey, Headers: cloneHeaderMap(payload.Headers)})
	if err != nil {
		return EncryptedCredential{}, errors.New("profiles: credential payload is not JSON-safe")
	}
	block, err := aes.NewCipher(vault.keys[vault.activeKeyID])
	if err != nil {
		return EncryptedCredential{}, fmt.Errorf("profiles: initialize credential cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return EncryptedCredential{}, fmt.Errorf("profiles: initialize credential GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(vault.random, nonce); err != nil {
		return EncryptedCredential{}, fmt.Errorf("profiles: generate credential nonce: %w", err)
	}
	aad, err := credentialAAD(normalizedBinding, vault.activeKeyID)
	if err != nil {
		return EncryptedCredential{}, err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, aad)
	return EncryptedCredential{
		SchemaVersion: credentialEncryptionVersion, Algorithm: "AES-256-GCM", KeyID: vault.activeKeyID,
		Nonce: base64.RawURLEncoding.EncodeToString(nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(sealed),
	}, nil
}

func (vault *CredentialVault) Open(encrypted EncryptedCredential, binding CredentialBinding) (CredentialPayload, error) {
	if vault == nil {
		return CredentialPayload{}, errors.New("profiles: credential vault is required")
	}
	if encrypted.SchemaVersion != credentialEncryptionVersion || encrypted.Algorithm != "AES-256-GCM" {
		return CredentialPayload{}, errors.New("profiles: unsupported encrypted credential")
	}
	key, ok := vault.keys[encrypted.KeyID]
	if !ok {
		return CredentialPayload{}, fmt.Errorf("profiles: credential key %q is unavailable", encrypted.KeyID)
	}
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
		return CredentialPayload{}, errors.New("profiles: initialize credential cipher")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return CredentialPayload{}, errors.New("profiles: initialize credential GCM")
	}
	if len(nonce) != gcm.NonceSize() || len(ciphertext) < gcm.Overhead() {
		return CredentialPayload{}, errors.New("profiles: encrypted credential is malformed")
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
		return CredentialPayload{}, errors.New("profiles: encrypted credential authentication failed")
	}
	decoder := json.NewDecoder(strings.NewReader(string(plaintext)))
	decoder.DisallowUnknownFields()
	var payload CredentialPayload
	if err = decoder.Decode(&payload); err != nil || strings.TrimSpace(payload.APIKey) == "" {
		return CredentialPayload{}, errors.New("profiles: decrypted credential payload is invalid")
	}
	if err = requireJSONEOF(decoder); err != nil {
		return CredentialPayload{}, errors.New("profiles: decrypted credential payload is invalid")
	}
	payload.Headers = cloneHeaderMap(payload.Headers)
	return payload, nil
}

func (vault *CredentialVault) ExportBundle(records []CredentialRecord, bundleID string, createdAt time.Time) ([]byte, error) {
	if vault == nil {
		return nil, errors.New("profiles: credential vault is required")
	}
	if strings.TrimSpace(bundleID) == "" || createdAt.IsZero() {
		return nil, errors.New("profiles: bundle ID and creation time are required")
	}
	normalized := append([]CredentialRecord(nil), records...)
	for index := range normalized {
		record, err := vault.validateRecord(normalized[index])
		if err != nil {
			return nil, fmt.Errorf("profiles: bundle credential %d: %w", index, err)
		}
		normalized[index] = record
	}
	slices.SortFunc(normalized, func(left, right CredentialRecord) int {
		return strings.Compare(left.Binding.CredentialID, right.Binding.CredentialID)
	})
	for index := 1; index < len(normalized); index++ {
		if normalized[index-1].Binding.CredentialID == normalized[index].Binding.CredentialID {
			return nil, errors.New("profiles: credential bundle contains duplicate credential IDs")
		}
	}
	return json.Marshal(credentialBundle{
		SchemaVersion: credentialBundleVersion, BundleID: strings.TrimSpace(bundleID),
		CreatedAt: createdAt.UTC(), Credentials: normalized,
	})
}

func (vault *CredentialVault) ImportBundle(input []byte, expectedOwnerID string) ([]CredentialRecord, error) {
	if vault == nil {
		return nil, errors.New("profiles: credential vault is required")
	}
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.DisallowUnknownFields()
	var bundle credentialBundle
	if err := decoder.Decode(&bundle); err != nil {
		return nil, errors.New("profiles: credential bundle is invalid")
	}
	if err := requireJSONEOF(decoder); err != nil || bundle.SchemaVersion != credentialBundleVersion || bundle.BundleID == "" || bundle.CreatedAt.IsZero() {
		return nil, errors.New("profiles: credential bundle is invalid")
	}
	ownerID := strings.TrimSpace(expectedOwnerID)
	if ownerID == "" {
		return nil, errors.New("profiles: expected bundle owner is required")
	}
	result := make([]CredentialRecord, len(bundle.Credentials))
	seen := make(map[string]struct{}, len(result))
	for index, record := range bundle.Credentials {
		if record.Binding.OwnerID != ownerID {
			return nil, errors.New("profiles: credential bundle owner mismatch")
		}
		normalized, err := vault.validateRecord(record)
		if err != nil {
			return nil, fmt.Errorf("profiles: credential bundle record %d is invalid", index)
		}
		if _, duplicate := seen[normalized.Binding.CredentialID]; duplicate {
			return nil, errors.New("profiles: credential bundle contains duplicate credential IDs")
		}
		seen[normalized.Binding.CredentialID] = struct{}{}
		result[index] = normalized
	}
	return result, nil
}

func (vault *CredentialVault) validateRecord(record CredentialRecord) (CredentialRecord, error) {
	if record.SchemaVersion != 1 || record.CreatedAt.IsZero() {
		return CredentialRecord{}, errors.New("credential record version and creation time are required")
	}
	binding, err := normalizeCredentialBinding(record.Binding)
	if err != nil {
		return CredentialRecord{}, err
	}
	if record.Scope != "global" && record.Scope != "user" {
		return CredentialRecord{}, errors.New("credential scope must be global or user")
	}
	types, err := normalizeInferenceTypes(record.APIInferenceTypes)
	if err != nil {
		return CredentialRecord{}, err
	}
	if _, err = vault.Open(record.Encrypted, binding); err != nil {
		return CredentialRecord{}, err
	}
	record.Binding = binding
	record.APIInferenceTypes = types
	record.CreatedAt = record.CreatedAt.UTC()
	if !record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.UpdatedAt.UTC()
	}
	return record, nil
}

func PublicCredentialState(record CredentialRecord) CredentialState {
	binding, _ := normalizeCredentialBinding(record.Binding)
	types, _ := normalizeInferenceTypes(record.APIInferenceTypes)
	return CredentialState{
		SchemaVersion: 1, CredentialID: binding.CredentialID, Scope: record.Scope, Origin: binding.Origin,
		APIInferenceTypes: types, Configured: record.Encrypted.Ciphertext != "", CreatedAt: record.CreatedAt.UTC(),
		UpdatedAt: record.UpdatedAt.UTC(),
	}
}

func normalizeCredentialBinding(binding CredentialBinding) (CredentialBinding, error) {
	binding.OwnerID = strings.TrimSpace(binding.OwnerID)
	binding.CredentialID = strings.TrimSpace(binding.CredentialID)
	if binding.OwnerID == "" || binding.CredentialID == "" {
		return CredentialBinding{}, errors.New("profiles: credential owner and ID are required")
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
		return "", errors.New("profiles: credential origin must be an absolute HTTPS origin")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	port := parsed.Port()
	if port != "" {
		number, portErr := strconv.Atoi(port)
		if portErr != nil || number < 1 || number > 65535 {
			return "", errors.New("profiles: credential origin port is invalid")
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
		OwnerID       string `json:"ownerId"`
		CredentialID  string `json:"credentialId"`
		Origin        string `json:"origin"`
	}{credentialEncryptionVersion, "AES-256-GCM", keyID, binding.OwnerID, binding.CredentialID, binding.Origin})
}

func decodeBase64URL(value, label string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("profiles: encrypted credential %s is empty", label)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("profiles: encrypted credential %s is malformed", label)
	}
	return decoded, nil
}

func normalizeInferenceTypes(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := apiInferenceTypes[value]; !ok {
			return nil, fmt.Errorf("unsupported API inference type %q", value)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, errors.New("at least one API inference type is required")
	}
	slices.Sort(result)
	return result, nil
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
