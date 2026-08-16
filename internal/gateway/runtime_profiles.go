package gateway

import (
	"context"
	"errors"
	"maps"
	"strings"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

type runtimeCredentialKey struct {
	Origin           string
	Scope            string
	APIInferenceType string
}

type runtimeCredentialResolver struct {
	ownerID     string
	credentials map[runtimeCredentialKey]profiles.CredentialPayload
}

func (service *ProfileService) RuntimeProfiles(ctx context.Context, ownerID string) (hardenllm.ProfileCatalog, hardenllm.CredentialResolver, error) {
	ownerID = strings.TrimSpace(ownerID)
	records, err := service.store.Profiles(ctx, ownerID)
	if err != nil {
		return nil, nil, err
	}
	credentialRecords, err := service.store.Credentials(ctx, ownerID)
	if err != nil {
		return nil, nil, err
	}
	credentialsByID := make(map[string]postgres.CredentialRecord, len(credentialRecords))
	for _, record := range credentialRecords {
		credentialsByID[record.ID] = record
	}
	decryptedByID := make(map[string]profiles.CredentialPayload, len(credentialRecords))
	catalog := make(profiles.Catalog, len(records))
	rootCatalog := make(hardenllm.ProfileCatalog, len(records))
	resolver := &runtimeCredentialResolver{ownerID: ownerID, credentials: make(map[runtimeCredentialKey]profiles.CredentialPayload, len(records))}
	for _, record := range records {
		profile, err := decodeProfileDocument(record.Document)
		if err != nil || profile.LLMProfile != record.ID || record.CredentialID == "" {
			return nil, nil, errors.New("gateway: stored runtime profile is invalid")
		}
		catalog[record.ID] = profile
		rootCatalog[record.ID] = rootProfile(profile)
		credentialRecord, ok := credentialsByID[record.CredentialID]
		if !ok {
			return nil, nil, errors.New("gateway: stored runtime credential is missing")
		}
		metadata, err := decodeCredentialMetadata(credentialRecord.Metadata)
		if err != nil || metadata.Scope != profile.EndpointCredentialScope || !containsString(metadata.APIInferenceTypes, profile.APIInferenceType) {
			return nil, nil, errors.New("gateway: stored runtime credential binding is invalid")
		}
		origin, err := profileOrigin(profile.BaseURL)
		if err != nil || origin != credentialRecord.Origin {
			return nil, nil, errors.New("gateway: stored runtime credential origin is invalid")
		}
		payload, ok := decryptedByID[record.CredentialID]
		if !ok {
			payload, err = service.openCredentialRecord(credentialRecord)
			if err != nil {
				return nil, nil, err
			}
			decryptedByID[record.CredentialID] = payload
		}
		key := runtimeCredentialKey{Origin: origin, Scope: profile.EndpointCredentialScope, APIInferenceType: profile.APIInferenceType}
		if existing, ok := resolver.credentials[key]; ok && (existing.APIKey != payload.APIKey || !maps.Equal(existing.Headers, payload.Headers)) {
			return nil, nil, errors.New("gateway: endpoint credential binding is ambiguous")
		}
		resolver.credentials[key] = profiles.CredentialPayload{APIKey: payload.APIKey, Headers: cloneStringMap(payload.Headers)}
	}
	if err := profiles.ValidateCatalog(catalog); err != nil {
		return nil, nil, errors.New("gateway: stored runtime profile catalog is invalid")
	}
	return rootCatalog, resolver, nil
}

func (resolver *runtimeCredentialResolver) ResolveCredential(_ context.Context, request hardenllm.CredentialRequest) (hardenllm.Credential, error) {
	if resolver == nil || request.OwnerID != resolver.ownerID {
		return hardenllm.Credential{}, errors.New("gateway: credential owner mismatch")
	}
	origin, err := profileOrigin(request.BaseURL)
	if err != nil {
		return hardenllm.Credential{}, errors.New("gateway: credential endpoint is invalid")
	}
	payload, ok := resolver.credentials[runtimeCredentialKey{Origin: origin, Scope: request.Scope, APIInferenceType: request.APIInferenceType}]
	if !ok {
		return hardenllm.Credential{}, errors.New("gateway: endpoint credential was not found")
	}
	return hardenllm.Credential{APIKey: payload.APIKey, Headers: cloneStringMap(payload.Headers)}, nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
