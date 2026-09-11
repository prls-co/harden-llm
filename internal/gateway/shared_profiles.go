package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

// SharedProfiles is trusted operator configuration, never a public API input.
// Keys arrive on the administrative command's stdin, not arguments or logs.
type SharedProfiles struct {
	Profiles    profiles.Catalog                      `json:"profiles"`
	Credentials map[string]profiles.CredentialPayload `json:"credentials"`
}

type SharedProfileSyncResult struct {
	Profiles   int  `json:"profiles"`
	Configured int  `json:"configured"`
	Changed    bool `json:"changed"`
}

// ApplySharedProfiles validates and encrypts before one atomic, non-destructive
// upsert per owner. Unlike interactive Save, provisioning never probes a provider.
func ApplySharedProfiles(ctx context.Context, store *postgres.Store, vault *profiles.CredentialVault, ownerID string, config SharedProfiles) error {
	_, err := ApplySharedProfilesWithResult(ctx, store, vault, ownerID, config)
	return err
}

func ApplySharedProfilesWithResult(ctx context.Context, store *postgres.Store, vault *profiles.CredentialVault, ownerID string, config SharedProfiles) (SharedProfileSyncResult, error) {
	result := SharedProfileSyncResult{Profiles: len(config.Profiles), Configured: len(config.Credentials)}
	if strings.TrimSpace(ownerID) == "" || store == nil || vault == nil {
		return result, errors.New("shared profiles: owner, store and vault are required")
	}
	if err := validateSharedProfileConfig(config); err != nil {
		return result, err
	}
	existing, err := store.Profiles(ctx, ownerID)
	if err != nil {
		return result, err
	}
	if matched, err := sharedProfilesMatch(ctx, store, vault, ownerID, config, existing); err != nil {
		return result, err
	} else if matched {
		return result, nil
	}
	rows, credentials, err := sharedProfileRecords(config, ownerID, vault, time.Now().UTC())
	if err != nil {
		return result, err
	}
	// Validate against retained custom profiles as well (including backup links).
	catalog := maps.Clone(config.Profiles)
	sharedBindings := make(map[runtimeCredentialKey]string)
	for _, row := range rows {
		if row.CredentialID == "" {
			continue
		}
		profile := config.Profiles[row.ID]
		origin, _ := profileOrigin(profile.BaseURL)
		sharedBindings[runtimeCredentialKey{Origin: origin, Scope: profile.EndpointCredentialScope, APIInferenceType: profile.APIInferenceType}] = row.CredentialID
	}
	for _, row := range existing {
		if _, managed := catalog[row.ID]; managed {
			continue
		}
		profile, err := decodeProfileDocument(row.Document)
		if err != nil {
			return result, err
		}
		catalog[row.ID] = profile
		// A locally customized model on a shared endpoint must rotate with that
		// endpoint's key too, or the runtime resolver would see conflicting keys.
		origin, err := profileOrigin(profile.BaseURL)
		if err != nil {
			return result, err
		}
		if binding := sharedBindings[runtimeCredentialKey{Origin: origin, Scope: profile.EndpointCredentialScope, APIInferenceType: profile.APIInferenceType}]; binding != "" {
			row.CredentialID = binding
			rows = append(rows, row)
		}
	}
	if err := profiles.ValidateCatalog(catalog); err != nil {
		return result, err
	}
	if err := store.UpsertProfileBundle(ctx, ownerID, rows, credentials); err != nil {
		return result, err
	}
	result.Changed = true
	return result, nil
}

func validateSharedProfileConfig(config SharedProfiles) error {
	if len(config.Profiles) == 0 {
		return errors.New("shared profiles: profile catalog is required")
	}
	if err := profiles.ValidateCatalog(config.Profiles); err != nil {
		return err
	}
	for name, payload := range config.Credentials {
		if _, ok := config.Profiles[name]; !ok || strings.TrimSpace(payload.APIKey) == "" {
			return errors.New("shared profiles: invalid credential mapping")
		}
	}
	bindings := make(map[runtimeCredentialKey]profiles.CredentialPayload)
	for _, name := range slices.Sorted(maps.Keys(config.Profiles)) {
		payload, configured := config.Credentials[name]
		if !configured {
			continue
		}
		profile := config.Profiles[name]
		origin, err := profileOrigin(profile.BaseURL)
		if err != nil {
			return err
		}
		key := runtimeCredentialKey{Origin: origin, Scope: profile.EndpointCredentialScope, APIInferenceType: profile.APIInferenceType}
		if previous, exists := bindings[key]; exists && (previous.APIKey != payload.APIKey || !maps.Equal(previous.Headers, payload.Headers)) {
			return errors.New("shared profiles: ambiguous endpoint credentials")
		}
		bindings[key] = payload
	}
	return nil
}

func sharedProfilesMatch(ctx context.Context, store *postgres.Store, vault *profiles.CredentialVault, ownerID string, config SharedProfiles, existing []postgres.ProfileRecord) (bool, error) {
	if store == nil || vault == nil || strings.TrimSpace(ownerID) == "" {
		return false, errors.New("shared profiles: owner, store and vault are required")
	}
	credentialRecords, err := store.Credentials(ctx, ownerID)
	if err != nil {
		return false, err
	}
	byID := make(map[string]postgres.CredentialRecord, len(credentialRecords))
	for _, record := range credentialRecords {
		byID[record.ID] = record
	}
	profilesByID := make(map[string]postgres.ProfileRecord, len(existing))
	for _, record := range existing {
		profilesByID[record.ID] = record
	}
	sharedBindingByKey := make(map[runtimeCredentialKey]string)
	for _, name := range slices.Sorted(maps.Keys(config.Profiles)) {
		profile := config.Profiles[name]
		credentialID := ""
		if _, configured := config.Credentials[name]; configured {
			credentialID = sharedCredentialID(name)
			origin, originErr := profileOrigin(profile.BaseURL)
			if originErr != nil {
				return false, originErr
			}
			sharedBindingByKey[runtimeCredentialKey{Origin: origin, Scope: profile.EndpointCredentialScope, APIInferenceType: profile.APIInferenceType}] = credentialID
		}
		record, ok := profilesByID[name]
		if !ok || record.CredentialID != credentialID {
			return false, nil
		}
		stored, decodeErr := decodeProfileDocument(record.Document)
		if decodeErr != nil || !sameProfileDocument(stored, profile) {
			return false, nil
		}
		if credentialID != "" {
			if !credentialRecordMatches(byID[credentialID], vault, profile, config.Credentials[name]) {
				return false, nil
			}
		}
	}
	catalog := maps.Clone(config.Profiles)
	for _, record := range existing {
		if _, managed := config.Profiles[record.ID]; managed {
			continue
		}
		profile, decodeErr := decodeProfileDocument(record.Document)
		if decodeErr != nil {
			return false, nil
		}
		origin, originErr := profileOrigin(profile.BaseURL)
		if originErr != nil {
			return false, originErr
		}
		catalog[record.ID] = profile
		expectedID := sharedBindingByKey[runtimeCredentialKey{Origin: origin, Scope: profile.EndpointCredentialScope, APIInferenceType: profile.APIInferenceType}]
		if expectedID != "" && record.CredentialID != expectedID {
			return false, nil
		}
	}
	if err := profiles.ValidateCatalog(catalog); err != nil {
		return false, err
	}
	return true, nil
}

func credentialRecordMatches(record postgres.CredentialRecord, vault *profiles.CredentialVault, profile profiles.Profile, expected profiles.CredentialPayload) bool {
	if record.ID == "" {
		return false
	}
	public, err := publicCredentialRecord(record)
	if err != nil {
		return false
	}
	metadata, err := decodeCredentialMetadata(record.Metadata)
	if err != nil || metadata.Scope != profile.EndpointCredentialScope || !containsString(metadata.APIInferenceTypes, profile.APIInferenceType) {
		return false
	}
	origin, err := profileOrigin(profile.BaseURL)
	if err != nil || record.Origin != origin {
		return false
	}
	payload, err := vault.Open(public.Encrypted, public.Binding)
	return err == nil && payload.APIKey == expected.APIKey && maps.Equal(payload.Headers, expected.Headers)
}

func sameProfileDocument(a, b profiles.Profile) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && string(left) == string(right)
}

func sharedProfileRecords(config SharedProfiles, ownerID string, vault *profiles.CredentialVault, now time.Time) ([]postgres.ProfileRecord, []postgres.CredentialRecord, error) {
	if strings.TrimSpace(ownerID) == "" || vault == nil {
		return nil, nil, errors.New("shared profiles: owner, catalog and vault are required")
	}
	if err := validateSharedProfileConfig(config); err != nil {
		return nil, nil, err
	}
	rows := make([]postgres.ProfileRecord, 0, len(config.Profiles))
	credentials := make([]postgres.CredentialRecord, 0, len(config.Credentials))
	for _, name := range slices.Sorted(maps.Keys(config.Profiles)) {
		profile := config.Profiles[name]
		document, err := json.Marshal(profile)
		if err != nil {
			return nil, nil, errors.New("shared profiles: invalid profile document")
		}
		row := postgres.ProfileRecord{OwnerID: ownerID, ID: name, Document: document, CreatedAt: now, UpdatedAt: now}
		if payload, ok := config.Credentials[name]; ok {
			origin, err := profileOrigin(profile.BaseURL)
			if err != nil {
				return nil, nil, err
			}
			row.CredentialID = sharedCredentialID(name)
			binding := profiles.CredentialBinding{OwnerID: ownerID, CredentialID: row.CredentialID, Origin: origin}
			encrypted, err := vault.Seal(payload, binding)
			if err != nil {
				return nil, nil, err
			}
			record, err := postgresCredentialRecord(profiles.CredentialRecord{SchemaVersion: 1, Binding: binding, Scope: profile.EndpointCredentialScope, APIInferenceTypes: []string{profile.APIInferenceType}, Encrypted: encrypted, CreatedAt: now, UpdatedAt: now})
			if err != nil {
				return nil, nil, err
			}
			credentials = append(credentials, record)
		}
		rows = append(rows, row)
	}
	return rows, credentials, nil
}

func sharedCredentialID(profileName string) string {
	sum := sha256.Sum256([]byte(profileName))
	return "shared-" + hex.EncodeToString(sum[:16])
}
