package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

const profileBundleSchemaVersion = 1

var ErrProfileConflict = errors.New("gateway: profile conflict")

type ModelRefresher interface {
	RefreshModels(context.Context, profiles.Profile, profiles.CredentialPayload) ([]profiles.Model, error)
}

type ProfileBundle struct {
	SchemaVersion int                         `json:"schemaVersion"`
	BundleID      string                      `json:"bundleId"`
	CreatedAt     time.Time                   `json:"createdAt"`
	Profiles      profiles.Catalog            `json:"profiles"`
	CredentialIDs map[string]string           `json:"credentialIds"`
	Credentials   []profiles.CredentialRecord `json:"credentials"`
}

func (service *ProfileService) Profiles(ctx context.Context, ownerID string) ([]ProfileState, error) {
	ownerID = strings.TrimSpace(ownerID)
	if err := service.ensureSeeded(ctx, ownerID); err != nil {
		return nil, err
	}
	records, err := service.store.Profiles(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	credentialRecords, err := service.store.Credentials(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	credentials := make(map[string]profiles.CredentialRecord, len(credentialRecords))
	for _, record := range credentialRecords {
		public, err := publicCredentialRecord(record)
		if err != nil {
			return nil, err
		}
		credentials[record.ID] = public
	}
	catalog := make(profiles.Catalog, len(records))
	for _, record := range records {
		profile, err := decodeProfileDocument(record.Document)
		if err != nil || profile.LLMProfile != record.ID {
			return nil, errors.New("gateway: stored profile document is invalid")
		}
		profile.Models = profiles.NormalizeModels(profile.Models)
		catalog[record.ID] = profile
	}
	if err := profiles.ValidateCatalog(catalog); err != nil {
		return nil, errors.New("gateway: stored profile catalog is invalid")
	}
	result := make([]ProfileState, 0, len(records))
	for _, record := range records {
		state := ProfileState{Profile: catalog[record.ID]}
		if record.CredentialID != "" {
			credential, ok := credentials[record.CredentialID]
			if !ok {
				return nil, errors.New("gateway: stored profile credential is missing")
			}
			state.Credential = profiles.PublicCredentialState(credential)
		} else {
			origin, err := profileOrigin(state.Profile.BaseURL)
			if err != nil {
				return nil, errors.New("gateway: stored profile origin is invalid")
			}
			credentialID := credentialIDForProfile(state.Profile)
			state.Credential = profiles.CredentialState{
				SchemaVersion: 1, CredentialID: credentialID, Scope: state.Profile.EndpointCredentialScope,
				Origin: origin, APIInferenceTypes: []string{state.Profile.APIInferenceType}, Configured: false,
				CreatedAt: record.CreatedAt.UTC(), UpdatedAt: record.UpdatedAt.UTC(),
			}
		}
		result = append(result, state)
	}
	return result, nil
}

func (service *ProfileService) Profile(ctx context.Context, ownerID, profileID string) (ProfileState, error) {
	states, err := service.Profiles(ctx, ownerID)
	if err != nil {
		return ProfileState{}, err
	}
	for _, state := range states {
		if state.Profile.LLMProfile == profileID {
			return state, nil
		}
	}
	return ProfileState{}, postgres.ErrNotFound
}

func (service *ProfileService) Delete(ctx context.Context, ownerID, profileID string) error {
	catalog, err := service.catalog(ctx, ownerID)
	if err != nil {
		return err
	}
	if _, ok := catalog[profileID]; !ok {
		return postgres.ErrNotFound
	}
	delete(catalog, profileID)
	if err := profiles.ValidateCatalog(catalog); err != nil {
		return fmt.Errorf("%w: profile is referenced by the remaining catalog", ErrProfileConflict)
	}
	return service.store.DeleteProfile(ctx, ownerID, profileID)
}

func (service *ProfileService) RefreshModels(ctx context.Context, ownerID, profileID string, refresher ModelRefresher) (ProfileState, error) {
	if refresher == nil {
		return ProfileState{}, errors.New("gateway: model refresher is required")
	}
	if err := service.ensureSeeded(ctx, ownerID); err != nil {
		return ProfileState{}, err
	}
	record, err := service.store.Profile(ctx, ownerID, profileID)
	if err != nil {
		return ProfileState{}, err
	}
	profile, err := decodeProfileDocument(record.Document)
	if err != nil {
		return ProfileState{}, err
	}
	credential, err := service.Credential(ctx, ownerID, record.CredentialID)
	if err != nil {
		return ProfileState{}, err
	}
	refreshContext, cancel := context.WithTimeout(ctx, service.probeTimeout)
	models, err := refresher.RefreshModels(refreshContext, profile, credential)
	cancel()
	if err != nil {
		return ProfileState{}, errors.New("gateway: model refresh failed")
	}
	profile.Models = profiles.NormalizeModels(models)
	now := service.clock().UTC()
	profile.LastModelRefreshAt = &now
	catalog, err := service.catalog(ctx, ownerID)
	if err != nil {
		return ProfileState{}, err
	}
	catalog[profileID] = profile
	if err := profiles.ValidateCatalog(catalog); err != nil {
		return ProfileState{}, err
	}
	document, err := json.Marshal(profile)
	if err != nil {
		return ProfileState{}, errors.New("gateway: profile encoding failed")
	}
	if err := service.store.SaveProfile(ctx, postgres.ProfileRecord{
		OwnerID: ownerID, ID: profileID, CredentialID: record.CredentialID,
		Document: document, CreatedAt: record.CreatedAt, UpdatedAt: now,
	}, nil); err != nil {
		return ProfileState{}, err
	}
	return service.Profile(ctx, ownerID, profileID)
}

func (service *ProfileService) ExportBundle(ctx context.Context, ownerID, bundleID string) (ProfileBundle, error) {
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return ProfileBundle{}, errors.New("gateway: bundle ID is required")
	}
	if err := service.ensureSeeded(ctx, ownerID); err != nil {
		return ProfileBundle{}, err
	}
	profileRecords, err := service.store.Profiles(ctx, ownerID)
	if err != nil {
		return ProfileBundle{}, err
	}
	credentialRecords, err := service.store.Credentials(ctx, ownerID)
	if err != nil {
		return ProfileBundle{}, err
	}
	catalog := make(profiles.Catalog, len(profileRecords))
	bindings := make(map[string]string, len(profileRecords))
	for _, record := range profileRecords {
		profile, err := decodeProfileDocument(record.Document)
		if err != nil || profile.LLMProfile != record.ID {
			return ProfileBundle{}, errors.New("gateway: stored profile document is invalid")
		}
		catalog[record.ID] = profile
		bindings[record.ID] = record.CredentialID
	}
	if err := profiles.ValidateCatalog(catalog); err != nil {
		return ProfileBundle{}, errors.New("gateway: stored profile catalog is invalid")
	}
	credentials := make([]profiles.CredentialRecord, 0, len(credentialRecords))
	for _, record := range credentialRecords {
		credential, err := publicCredentialRecord(record)
		if err != nil {
			return ProfileBundle{}, err
		}
		credentials = append(credentials, credential)
	}
	return ProfileBundle{
		SchemaVersion: profileBundleSchemaVersion, BundleID: bundleID, CreatedAt: service.clock().UTC(),
		Profiles: catalog, CredentialIDs: bindings, Credentials: credentials,
	}, nil
}

func (service *ProfileService) ReplaceBundle(ctx context.Context, ownerID string, bundle ProfileBundle) ([]ProfileState, error) {
	ownerID = strings.TrimSpace(ownerID)
	if bundle.SchemaVersion != profileBundleSchemaVersion || strings.TrimSpace(bundle.BundleID) == "" || bundle.CreatedAt.IsZero() || bundle.Profiles == nil || bundle.CredentialIDs == nil {
		return nil, errors.New("gateway: profile bundle is invalid")
	}
	normalizedCatalog := make(profiles.Catalog, len(bundle.Profiles))
	for profileID, profile := range bundle.Profiles {
		profile.Models = profiles.NormalizeModels(profile.Models)
		if profile.LastModelRefreshAt != nil {
			refreshedAt := profile.LastModelRefreshAt.UTC()
			profile.LastModelRefreshAt = &refreshedAt
		}
		normalizedCatalog[profileID] = profile
	}
	bundle.Profiles = normalizedCatalog
	if err := profiles.ValidateCatalog(bundle.Profiles); err != nil {
		return nil, err
	}
	credentials, err := service.vault.ValidateRecords(bundle.Credentials, ownerID)
	if err != nil {
		return nil, err
	}
	credentialByID := make(map[string]profiles.CredentialRecord, len(credentials))
	for _, credential := range credentials {
		credentialByID[credential.Binding.CredentialID] = credential
	}
	if len(bundle.CredentialIDs) != len(bundle.Profiles) {
		return nil, errors.New("gateway: every bundled profile requires one credential binding")
	}
	profileNames := make([]string, 0, len(bundle.Profiles))
	for profileID := range bundle.Profiles {
		profileNames = append(profileNames, profileID)
	}
	slices.Sort(profileNames)
	usedCredentials := make(map[string]struct{}, len(credentials))
	for _, profileID := range profileNames {
		profile := bundle.Profiles[profileID]
		credentialID := strings.TrimSpace(bundle.CredentialIDs[profileID])
		credential, ok := credentialByID[credentialID]
		if !ok || credential.Scope != profile.EndpointCredentialScope || credential.Binding.Origin != mustProfileOrigin(profile.BaseURL) || !slices.Contains(credential.APIInferenceTypes, profile.APIInferenceType) {
			return nil, errors.New("gateway: bundled profile credential binding is invalid")
		}
		usedCredentials[credentialID] = struct{}{}
		payload, err := service.vault.Open(credential.Encrypted, credential.Binding)
		if err != nil {
			return nil, err
		}
		probeContext, cancel := context.WithTimeout(ctx, service.probeTimeout)
		err = service.prober.Probe(probeContext, profile, payload)
		cancel()
		if err != nil {
			return nil, errors.New("gateway: bundled profile probe failed")
		}
	}
	if len(usedCredentials) != len(credentials) {
		return nil, errors.New("gateway: profile bundle contains an unused credential")
	}

	now := service.clock().UTC()
	profileRecords := make([]postgres.ProfileRecord, 0, len(profileNames))
	for _, profileID := range profileNames {
		document, err := json.Marshal(bundle.Profiles[profileID])
		if err != nil {
			return nil, errors.New("gateway: bundled profile encoding failed")
		}
		profileRecords = append(profileRecords, postgres.ProfileRecord{
			OwnerID: ownerID, ID: profileID, CredentialID: bundle.CredentialIDs[profileID],
			Document: document, CreatedAt: now, UpdatedAt: now,
		})
	}
	postgresCredentials := make([]postgres.CredentialRecord, 0, len(credentials))
	for _, credential := range credentials {
		record, err := postgresCredentialRecord(credential)
		if err != nil {
			return nil, err
		}
		postgresCredentials = append(postgresCredentials, record)
	}
	if err := service.store.ReplaceProfileBundle(ctx, ownerID, profileRecords, postgresCredentials); err != nil {
		return nil, err
	}
	return service.Profiles(ctx, ownerID)
}

func (service *ProfileService) catalog(ctx context.Context, ownerID string) (profiles.Catalog, error) {
	if err := service.ensureSeeded(ctx, ownerID); err != nil {
		return nil, err
	}
	records, err := service.store.Profiles(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	catalog := make(profiles.Catalog, len(records))
	for _, record := range records {
		profile, err := decodeProfileDocument(record.Document)
		if err != nil || profile.LLMProfile != record.ID {
			return nil, errors.New("gateway: stored profile document is invalid")
		}
		catalog[record.ID] = profile
	}
	return catalog, nil
}

func decodeProfileDocument(document []byte) (profiles.Profile, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var profile profiles.Profile
	if err := decoder.Decode(&profile); err != nil {
		return profiles.Profile{}, errors.New("gateway: stored profile document is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return profiles.Profile{}, errors.New("gateway: stored profile document is invalid")
	}
	return profile, nil
}

func publicCredentialRecord(record postgres.CredentialRecord) (profiles.CredentialRecord, error) {
	metadata, err := decodeCredentialMetadata(record.Metadata)
	if err != nil {
		return profiles.CredentialRecord{}, err
	}
	return profiles.CredentialRecord{
		SchemaVersion: 1,
		Binding:       profiles.CredentialBinding{OwnerID: record.OwnerID, CredentialID: record.ID, Origin: record.Origin},
		Scope:         metadata.Scope, APIInferenceTypes: append([]string(nil), metadata.APIInferenceTypes...),
		Encrypted: profiles.EncryptedCredential{
			SchemaVersion: metadata.SchemaVersion, Algorithm: metadata.Algorithm, KeyID: record.KeyID,
			Nonce: base64.RawURLEncoding.EncodeToString(record.Nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(record.Ciphertext),
		},
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}, nil
}

func postgresCredentialRecord(record profiles.CredentialRecord) (postgres.CredentialRecord, error) {
	nonce, err := base64.RawURLEncoding.Strict().DecodeString(record.Encrypted.Nonce)
	if err != nil {
		return postgres.CredentialRecord{}, errors.New("gateway: bundled credential nonce is invalid")
	}
	ciphertext, err := base64.RawURLEncoding.Strict().DecodeString(record.Encrypted.Ciphertext)
	if err != nil {
		return postgres.CredentialRecord{}, errors.New("gateway: bundled credential ciphertext is invalid")
	}
	metadata, err := json.Marshal(credentialMetadata{
		SchemaVersion: record.Encrypted.SchemaVersion, Algorithm: record.Encrypted.Algorithm,
		Scope: record.Scope, APIInferenceTypes: append([]string(nil), record.APIInferenceTypes...),
	})
	if err != nil {
		return postgres.CredentialRecord{}, errors.New("gateway: bundled credential metadata is invalid")
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = record.CreatedAt
	}
	return postgres.CredentialRecord{
		OwnerID: record.Binding.OwnerID, ID: record.Binding.CredentialID, KeyID: record.Encrypted.KeyID,
		Nonce: nonce, Ciphertext: ciphertext, Origin: record.Binding.Origin, Metadata: metadata,
		CreatedAt: record.CreatedAt, UpdatedAt: updatedAt,
	}, nil
}

func decodeCredentialMetadata(document []byte) (credentialMetadata, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var metadata credentialMetadata
	if err := decoder.Decode(&metadata); err != nil || metadata.SchemaVersion != 1 || metadata.Algorithm != "AES-256-GCM" || (metadata.Scope != "global" && metadata.Scope != "user") || len(metadata.APIInferenceTypes) == 0 {
		return credentialMetadata{}, errors.New("gateway: stored credential metadata is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return credentialMetadata{}, errors.New("gateway: stored credential metadata is invalid")
	}
	return metadata, nil
}

func mustProfileOrigin(baseURL string) string {
	origin, _ := profileOrigin(baseURL)
	return origin
}
