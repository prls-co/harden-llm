package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"slices"
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
	SeedCatalog  profiles.Catalog
	Clock        func() time.Time
	ProbeTimeout time.Duration
	Logger       *slog.Logger
}

type ProfileService struct {
	store        *postgres.Store
	vault        *profiles.CredentialVault
	prober       ProfileProber
	seedCatalog  profiles.Catalog
	clock        func() time.Time
	probeTimeout time.Duration
	logger       *slog.Logger
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
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	seedCatalog, err := cloneCatalog(config.SeedCatalog)
	if err != nil {
		return nil, fmt.Errorf("gateway: validate profile seed: %w", err)
	}
	return &ProfileService{
		store: config.Store, vault: config.Vault, prober: config.Prober, seedCatalog: seedCatalog,
		clock: config.Clock, probeTimeout: config.ProbeTimeout, logger: config.Logger,
	}, nil
}

func cloneCatalog(catalog profiles.Catalog) (profiles.Catalog, error) {
	if catalog == nil {
		return nil, nil
	}
	encoded, err := profiles.MarshalCatalog(catalog)
	if err != nil {
		return nil, err
	}
	return profiles.ParseCatalog(encoded)
}

func (service *ProfileService) ensureSeeded(ctx context.Context, ownerID string) error {
	if len(service.seedCatalog) == 0 {
		return nil
	}
	ownerID = strings.TrimSpace(ownerID)

	names := make([]string, 0, len(service.seedCatalog))
	for name := range service.seedCatalog {
		names = append(names, name)
	}
	slices.Sort(names)
	now := service.clock().UTC()
	records := make([]postgres.ProfileRecord, 0, len(names))
	for _, name := range names {
		document, err := json.Marshal(service.seedCatalog[name])
		if err != nil {
			return fmt.Errorf("gateway: encode profile seed %q: %w", name, err)
		}
		records = append(records, postgres.ProfileRecord{
			OwnerID: ownerID, ID: name, Document: document, CreatedAt: now, UpdatedAt: now,
		})
	}
	return service.store.SeedProfiles(ctx, ownerID, records)
}

func (service *ProfileService) Save(ctx context.Context, request SaveProfileRequest) (ProfileState, error) {
	if service == nil {
		return ProfileState{}, errors.New("gateway: profile service is not initialized")
	}
	request.OwnerID = strings.TrimSpace(request.OwnerID)
	request.ProfileID = strings.TrimSpace(request.ProfileID)
	request.CredentialID = strings.TrimSpace(request.CredentialID)
	if request.OwnerID == "" || request.ProfileID == "" || request.ProfileID != request.Profile.LLMProfile {
		return ProfileState{}, errors.New("gateway: owner and profile identity are required")
	}
	existingProfileRecord, existingProfileErr := service.store.Profile(ctx, request.OwnerID, request.ProfileID)
	if existingProfileErr != nil && !errors.Is(existingProfileErr, postgres.ErrNotFound) {
		return ProfileState{}, existingProfileErr
	}
	if existingProfileErr == nil && request.Profile.Models == nil {
		existingProfile, decodeErr := decodeProfileDocument(existingProfileRecord.Document)
		if decodeErr != nil {
			return ProfileState{}, decodeErr
		}
		request.Profile.Models = append([]profiles.Model(nil), existingProfile.Models...)
		request.Profile.LastModelRefreshAt = existingProfile.LastModelRefreshAt
		if request.CredentialID == "" {
			request.CredentialID = existingProfileRecord.CredentialID
		}
	}
	if request.CredentialID == "" && request.Credential != nil {
		request.CredentialID = credentialIDForProfile(request.Profile)
	}
	if request.CredentialID == "" {
		return ProfileState{}, errors.New("gateway: credential binding is required")
	}
	catalog, err := service.catalog(ctx, request.OwnerID)
	if err != nil {
		return ProfileState{}, err
	}
	catalog[request.ProfileID] = request.Profile
	if err := profiles.ValidateCatalog(catalog); err != nil {
		return ProfileState{}, err
	}
	origin, err := profileOrigin(request.Profile.BaseURL)
	if err != nil {
		return ProfileState{}, err
	}
	inferenceTypes := []string{request.Profile.APIInferenceType}
	profileRecords, err := service.store.Profiles(ctx, request.OwnerID)
	if err != nil {
		return ProfileState{}, err
	}
	for _, record := range profileRecords {
		if record.ID == request.ProfileID || record.CredentialID != request.CredentialID {
			continue
		}
		boundProfile := catalog[record.ID]
		boundOrigin, originErr := profileOrigin(boundProfile.BaseURL)
		if originErr != nil || boundOrigin != origin || boundProfile.EndpointCredentialScope != request.Profile.EndpointCredentialScope {
			return ProfileState{}, &profiles.ValidationError{Code: "invalid_profile", FieldErrors: []profiles.FieldError{{
				Field: "credentialId", Message: "A shared credential must retain one endpoint origin and scope.",
			}}}
		}
		if !slices.Contains(inferenceTypes, boundProfile.APIInferenceType) {
			inferenceTypes = append(inferenceTypes, boundProfile.APIInferenceType)
		}
	}
	slices.Sort(inferenceTypes)
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
	err = service.prober.Probe(probeContext, request.Profile, credentialCopy)
	cancel()
	if err != nil {
		service.logger.ErrorContext(ctx, "profile probe failed", "profile", request.ProfileID, "error", err)
		return ProfileState{}, errors.New("gateway: profile probe failed")
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
	if existingProfileErr == nil {
		profileCreatedAt = existingProfileRecord.CreatedAt
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
		Scope: request.Profile.EndpointCredentialScope, APIInferenceTypes: inferenceTypes,
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
	return service.openCredentialRecord(record)
}

func (service *ProfileService) openCredentialRecord(record postgres.CredentialRecord) (profiles.CredentialPayload, error) {
	metadata, err := decodeCredentialMetadata(record.Metadata)
	if err != nil {
		return profiles.CredentialPayload{}, err
	}
	encrypted := profiles.EncryptedCredential{
		SchemaVersion: metadata.SchemaVersion, Algorithm: metadata.Algorithm, KeyID: record.KeyID,
		Nonce: base64.RawURLEncoding.EncodeToString(record.Nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(record.Ciphertext),
	}
	return service.vault.Open(encrypted, profiles.CredentialBinding{OwnerID: record.OwnerID, CredentialID: record.ID, Origin: record.Origin})
}

func credentialIDForProfile(profile profiles.Profile) string {
	origin, err := profileOrigin(profile.BaseURL)
	if err != nil {
		return ""
	}
	// The ID is deterministic for the endpoint origin and credential scope; the
	// credential row remains owner-scoped in Postgres. It contains no credential
	// material and lets a seeded profile be configured without requiring the
	// operator to invent an opaque database identifier.
	digest := sha256.Sum256([]byte(profile.EndpointCredentialScope + "\x00" + origin))
	return "endpoint-" + hex.EncodeToString(digest[:])[:32]
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
	rootProfile := probeRootProfile(profile)
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

type probeCredentialContextKey struct{}

type sharedRootProfileProber struct{ client *hardenllm.Client }

// NewSharedRootProfileProber reuses one hardened provider transport pool for
// profile probes while binding each write-only credential to one call context.
func NewSharedRootProfileProber(options hardenllm.Options) (ProfileProber, error) {
	if options.Credentials != nil || options.Cache != nil || options.Artifacts != nil {
		return nil, errors.New("gateway: shared profile prober owns its credential adapter")
	}
	options.Credentials = probeCredentialResolver{}
	client, err := hardenllm.New(options)
	if err != nil {
		return nil, err
	}
	return &sharedRootProfileProber{client: client}, nil
}

func (prober *sharedRootProfileProber) Probe(ctx context.Context, profile profiles.Profile, credential profiles.CredentialPayload) error {
	if prober == nil || prober.client == nil || ctx == nil {
		return errors.New("gateway: shared profile prober is not initialized")
	}
	rootProfile := probeRootProfile(profile)
	_, err := prober.client.Call(context.WithValue(ctx, probeCredentialContextKey{}, profiles.CredentialPayload{
		APIKey: credential.APIKey, Headers: cloneStringMap(credential.Headers),
	}), hardenllm.Request{
		ProfileID: rootProfile.LLMProfile, Profiles: hardenllm.ProfileCatalog{rootProfile.LLMProfile: rootProfile},
		UserPrompt: "Reply with OK.", CallType: hardenllm.CallTypeText,
		RetryPolicy: hardenllm.RetryPolicy{MaxAttempts: 1},
	})
	return err
}

type probeCredentialResolver struct{}

func (probeCredentialResolver) ResolveCredential(ctx context.Context, _ hardenllm.CredentialRequest) (hardenllm.Credential, error) {
	payload, ok := ctx.Value(probeCredentialContextKey{}).(profiles.CredentialPayload)
	if !ok || payload.APIKey == "" {
		return hardenllm.Credential{}, errors.New("gateway: profile probe credential is unavailable")
	}
	return hardenllm.Credential{APIKey: payload.APIKey, Headers: cloneStringMap(payload.Headers)}, nil
}

func probeRootProfile(profile profiles.Profile) hardenllm.Profile {
	result := rootProfile(profile)
	result.BackupProfiles = nil
	return result
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
