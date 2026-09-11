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

// ApplySharedProfiles validates and encrypts before one atomic, non-destructive
// upsert per owner. Unlike interactive Save, provisioning never probes a provider.
func ApplySharedProfiles(ctx context.Context, store *postgres.Store, vault *profiles.CredentialVault, ownerID string, config SharedProfiles) error {
	rows, credentials, err := sharedProfileRecords(config, ownerID, vault, time.Now().UTC())
	if err != nil {
		return err
	}
	// Validate against retained custom profiles as well (including backup links).
	existing, err := store.Profiles(ctx, ownerID)
	if err != nil {
		return err
	}
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
			return err
		}
		catalog[row.ID] = profile
		// A locally customized model on a shared endpoint must rotate with that
		// endpoint's key too, or the runtime resolver would see conflicting keys.
		origin, err := profileOrigin(profile.BaseURL)
		if err != nil {
			return err
		}
		if binding := sharedBindings[runtimeCredentialKey{Origin: origin, Scope: profile.EndpointCredentialScope, APIInferenceType: profile.APIInferenceType}]; binding != "" {
			row.CredentialID = binding
			rows = append(rows, row)
		}
	}
	if err := profiles.ValidateCatalog(catalog); err != nil {
		return err
	}
	return store.UpsertProfileBundle(ctx, ownerID, rows, credentials)
}

func sharedProfileRecords(config SharedProfiles, ownerID string, vault *profiles.CredentialVault, now time.Time) ([]postgres.ProfileRecord, []postgres.CredentialRecord, error) {
	if strings.TrimSpace(ownerID) == "" || len(config.Profiles) == 0 || vault == nil {
		return nil, nil, errors.New("shared profiles: owner, catalog and vault are required")
	}
	if err := profiles.ValidateCatalog(config.Profiles); err != nil {
		return nil, nil, err
	}
	for name, payload := range config.Credentials {
		if _, ok := config.Profiles[name]; !ok || strings.TrimSpace(payload.APIKey) == "" {
			return nil, nil, errors.New("shared profiles: invalid credential mapping")
		}
	}
	rows := make([]postgres.ProfileRecord, 0, len(config.Profiles))
	credentials := make([]postgres.CredentialRecord, 0, len(config.Credentials))
	bindings := make(map[runtimeCredentialKey]profiles.CredentialPayload)
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
			key := runtimeCredentialKey{Origin: origin, Scope: profile.EndpointCredentialScope, APIInferenceType: profile.APIInferenceType}
			if previous, exists := bindings[key]; exists && (previous.APIKey != payload.APIKey || !maps.Equal(previous.Headers, payload.Headers)) {
				return nil, nil, errors.New("shared profiles: ambiguous endpoint credentials")
			}
			bindings[key] = payload
			sum := sha256.Sum256([]byte(name))
			row.CredentialID = "shared-" + hex.EncodeToString(sum[:16])
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
