package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

const (
	defaultProbeTimeout = 10 * time.Second
	maximumProbeTimeout = 30 * time.Second
)

type ProfileProber interface {
	Probe(context.Context, profiles.Profile, profiles.CredentialPayload) error
}

type ProfileServiceConfig struct {
	Store        *postgres.Store
	Vault        *profiles.CredentialVault
	Prober       ProfileProber
	Clock        func() time.Time
	ProbeTimeout time.Duration
}

type ProfileService struct {
	store        *postgres.Store
	vault        *profiles.CredentialVault
	prober       ProfileProber
	clock        func() time.Time
	probeTimeout time.Duration
}

type SaveProfileRequest struct {
	OwnerID      string
	ProfileID    string
	Profile      profiles.Profile
	CredentialID string
	Credential   *profiles.CredentialPayload
}

type ProfileState struct {
	Profile    profiles.Profile         `json:"profile"`
	Credential profiles.CredentialState `json:"credential"`
}

func NewProfileService(config ProfileServiceConfig) (*ProfileService, error) {
	if config.Store == nil || config.Vault == nil || config.Prober == nil {
		return nil, errors.New("gateway: profile store, credential vault, and prober are required")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.ProbeTimeout == 0 {
		config.ProbeTimeout = defaultProbeTimeout
	}
	if config.ProbeTimeout < time.Millisecond || config.ProbeTimeout > maximumProbeTimeout {
		return nil, errors.New("gateway: profile probe timeout is outside the supported range")
	}
	return &ProfileService{store: config.Store, vault: config.Vault, prober: config.Prober, clock: config.Clock, probeTimeout: config.ProbeTimeout}, nil
}

func (service *ProfileService) Save(ctx context.Context, request SaveProfileRequest) (ProfileState, error) {
	if service == nil {
		return ProfileState{}, errors.New("gateway: profile service is not initialized")
	}
	request.OwnerID = strings.TrimSpace(request.OwnerID)
	request.ProfileID = strings.TrimSpace(request.ProfileID)
	request.CredentialID = strings.TrimSpace(request.CredentialID)
	if request.OwnerID == "" || request.ProfileID == "" || request.CredentialID == "" || request.ProfileID != request.Profile.LLMProfile {
		return ProfileState{}, errors.New("gateway: owner, profile, and credential binding are required")
	}
	if err := profiles.ValidateCatalog(profiles.Catalog{request.ProfileID: request.Profile}); err != nil {
		return ProfileState{}, err
	}
	credential := request.Credential
	if credential == nil {
		existing, err := service.Credential(ctx, request.OwnerID, request.CredentialID)
		if err != nil {
			if errors.Is(err, postgres.ErrNotFound) {
				return ProfileState{}, errors.New("gateway: write-only credential is required for a new profile")
			}
			return ProfileState{}, err
		}
		credential = &existing
	}
	credentialCopy := profiles.CredentialPayload{APIKey: credential.APIKey, Headers: cloneStringMap(credential.Headers)}
	probeContext, cancel := context.WithTimeout(ctx, service.probeTimeout)
	err := service.prober.Probe(probeContext, request.Profile, credentialCopy)
	cancel()
	if err != nil {
		return ProfileState{}, errors.New("gateway: profile probe failed")
	}

	origin, err := profileOrigin(request.Profile.BaseURL)
	if err != nil {
		return ProfileState{}, err
	}
	binding := profiles.CredentialBinding{OwnerID: request.OwnerID, CredentialID: request.CredentialID, Origin: origin}
	encrypted, err := service.vault.Seal(credentialCopy, binding)
	if err != nil {
		return ProfileState{}, err
	}
	nonce, err := base64.RawURLEncoding.DecodeString(encrypted.Nonce)
	if err != nil {
		return ProfileState{}, errors.New("gateway: credential nonce encoding failed")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(encrypted.Ciphertext)
	if err != nil {
		return ProfileState{}, errors.New("gateway: credential ciphertext encoding failed")
	}
	now := service.clock().UTC()
	profileCreatedAt, credentialCreatedAt := now, now
	if existing, existingErr := service.store.Profile(ctx, request.OwnerID, request.ProfileID); existingErr == nil {
		profileCreatedAt = existing.CreatedAt
	}
	if existing, existingErr := service.store.Credential(ctx, request.OwnerID, request.CredentialID); existingErr == nil {
		credentialCreatedAt = existing.CreatedAt
	}
	profileDocument, err := json.Marshal(request.Profile)
	if err != nil {
		return ProfileState{}, errors.New("gateway: profile encoding failed")
	}
	metadata := credentialMetadata{
		SchemaVersion: encrypted.SchemaVersion, Algorithm: encrypted.Algorithm,
		Scope: request.Profile.EndpointCredentialScope, APIInferenceTypes: []string{request.Profile.APIInferenceType},
	}
	metadataDocument, err := json.Marshal(metadata)
	if err != nil {
		return ProfileState{}, errors.New("gateway: credential metadata encoding failed")
	}
	credentialRecord := postgres.CredentialRecord{
		OwnerID: request.OwnerID, ID: request.CredentialID, KeyID: encrypted.KeyID, Nonce: nonce, Ciphertext: ciphertext,
		Origin: origin, Metadata: metadataDocument, CreatedAt: credentialCreatedAt, UpdatedAt: now,
	}
	profileRecord := postgres.ProfileRecord{
		OwnerID: request.OwnerID, ID: request.ProfileID, CredentialID: request.CredentialID,
		Document: profileDocument, CreatedAt: profileCreatedAt, UpdatedAt: now,
	}
	if err := service.store.SaveProfile(ctx, profileRecord, &credentialRecord); err != nil {
		return ProfileState{}, err
	}
	publicRecord := profiles.CredentialRecord{
		SchemaVersion: 1, Binding: binding, Scope: metadata.Scope, APIInferenceTypes: metadata.APIInferenceTypes,
		Encrypted: encrypted, CreatedAt: credentialCreatedAt, UpdatedAt: now,
	}
	return ProfileState{Profile: request.Profile, Credential: profiles.PublicCredentialState(publicRecord)}, nil
}

func (service *ProfileService) Credential(ctx context.Context, ownerID, credentialID string) (profiles.CredentialPayload, error) {
	if service == nil {
		return profiles.CredentialPayload{}, errors.New("gateway: profile service is not initialized")
	}
	record, err := service.store.Credential(ctx, strings.TrimSpace(ownerID), strings.TrimSpace(credentialID))
	if err != nil {
		return profiles.CredentialPayload{}, err
	}
	var metadata credentialMetadata
	decoder := json.NewDecoder(bytes.NewReader(record.Metadata))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil || metadata.SchemaVersion != 1 || metadata.Algorithm != "AES-256-GCM" {
		return profiles.CredentialPayload{}, errors.New("gateway: stored credential metadata is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return profiles.CredentialPayload{}, errors.New("gateway: stored credential metadata is invalid")
	}
	encrypted := profiles.EncryptedCredential{
		SchemaVersion: metadata.SchemaVersion, Algorithm: metadata.Algorithm, KeyID: record.KeyID,
		Nonce: base64.RawURLEncoding.EncodeToString(record.Nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(record.Ciphertext),
	}
	return service.vault.Open(encrypted, profiles.CredentialBinding{OwnerID: record.OwnerID, CredentialID: record.ID, Origin: record.Origin})
}

type credentialMetadata struct {
	SchemaVersion     int      `json:"schemaVersion"`
	Algorithm         string   `json:"algorithm"`
	Scope             string   `json:"scope"`
	APIInferenceTypes []string `json:"apiInferenceTypes"`
}

// RootProfileProber routes the probe through the same root Client and endpoint
// policy as a normal provider call.
type RootProfileProber struct {
	EndpointPolicy hardenllm.EndpointPolicy
}

func (prober RootProfileProber) Probe(ctx context.Context, profile profiles.Profile, credential profiles.CredentialPayload) error {
	rootProfile := rootProfile(profile)
	client, err := hardenllm.New(hardenllm.Options{
		Credentials:    staticCredentialResolver{credential: hardenllm.Credential{APIKey: credential.APIKey, Headers: cloneStringMap(credential.Headers)}},
		EndpointPolicy: prober.EndpointPolicy,
	})
	if err != nil {
		return err
	}
	_, err = client.Call(ctx, hardenllm.Request{
		ProfileID: rootProfile.LLMProfile, Profiles: hardenllm.ProfileCatalog{rootProfile.LLMProfile: rootProfile},
		UserPrompt: "Reply with OK.", CallType: hardenllm.CallTypeText,
		RetryPolicy: hardenllm.RetryPolicy{MaxAttempts: 1},
	})
	return err
}

type staticCredentialResolver struct{ credential hardenllm.Credential }

func (resolver staticCredentialResolver) ResolveCredential(context.Context, hardenllm.CredentialRequest) (hardenllm.Credential, error) {
	return hardenllm.Credential{APIKey: resolver.credential.APIKey, Headers: cloneStringMap(resolver.credential.Headers)}, nil
}

func rootProfile(profile profiles.Profile) hardenllm.Profile {
	result := hardenllm.Profile{
		SchemaVersion: profile.SchemaVersion, LLMProfile: profile.LLMProfile, Provider: profile.Provider,
		APIInferenceType: profile.APIInferenceType, EndpointCredentialScope: profile.EndpointCredentialScope,
		BaseURL: profile.BaseURL, ModelID: profile.ModelID,
		SupportsContractedStructuredOutput: profile.SupportsContractedStructuredOutput,
		DefaultOptions:                     cloneAnyMap(profile.DefaultOptions), ReasoningEffortMap: cloneNestedAnyMap(profile.ReasoningEffortMap),
		BackupProfiles: append([]string(nil), profile.BackupProfiles...),
	}
	if profile.SupportsTemperature != nil {
		result.SupportsTemperature = *profile.SupportsTemperature
	}
	if profile.TokensParam != nil {
		result.TokensParam = *profile.TokensParam
	}
	if profile.ResponsesTokensParam != nil {
		result.ResponsesTokensParam = *profile.ResponsesTokensParam
	}
	if profile.Pricing != nil {
		result.Pricing = &hardenllm.Pricing{
			Input: profile.Pricing.Input, CacheRead: profile.Pricing.CacheRead, CacheCreation: profile.Pricing.CacheCreation,
			Output: profile.Pricing.Output, Reasoning: profile.Pricing.Reasoning,
		}
	}
	for _, model := range profile.Models {
		result.Models = append(result.Models, hardenllm.Model{ID: model.ID, Label: model.Label})
	}
	return result
}

func profileOrigin(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme != "https" || parsed.Opaque != "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("gateway: profile endpoint is invalid")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return "", errors.New("gateway: profile endpoint is invalid")
	}
	originHost := host
	if address, parseErr := netip.ParseAddr(host); parseErr == nil {
		address = address.Unmap()
		originHost = address.String()
		if address.Is6() {
			originHost = "[" + originHost + "]"
		}
	} else if !validEndpointHostname(host) {
		return "", errors.New("gateway: profile endpoint is invalid")
	}
	port := parsed.Port()
	if port != "" {
		number, portErr := strconv.Atoi(port)
		if portErr != nil || number < 1 || number > 65535 {
			return "", errors.New("gateway: profile endpoint is invalid")
		}
		if number != 443 {
			originHost = net.JoinHostPort(strings.Trim(originHost, "[]"), strconv.Itoa(number))
		}
	}
	return "https://" + originHost, nil
}

func validEndpointHostname(host string) bool {
	for _, character := range host {
		if character > unicode.MaxASCII || !(unicode.IsLetter(character) || unicode.IsDigit(character) || character == '.' || character == '-') {
			return false
		}
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneNestedAnyMap(input map[string]map[string]any) map[string]map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]map[string]any, len(input))
	for key, value := range input {
		result[key] = cloneAnyMap(value)
	}
	return result
}
