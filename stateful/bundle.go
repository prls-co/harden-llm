package stateful

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	hardenllm "github.com/prls-co/harden-llm"
	"time"
)

const maxBundleBytes = 2 << 20

type ProfileBundle struct {
	SchemaVersion int                `json:"schemaVersion"`
	BundleID      string             `json:"bundleId"`
	CreatedAt     time.Time          `json:"createdAt"`
	APIKeyHash    string             `json:"apiKeyHash"`
	Profiles      []BundleProfile    `json:"profiles"`
	HiddenPresets []string           `json:"hiddenPresets"`
	Credentials   []BundleCredential `json:"credentials"`
}
type BundleProfile struct {
	Profile ProfileView              `json:"profile"`
	Inputs  json.RawMessage          `json:"inputs"`
	Binding ProfileCredentialBinding `json:"credentialBinding"`
}
type BundleCredential struct {
	ID         string `json:"id"`
	Origin     string `json:"origin"`
	KeyID      string `json:"keyId"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	Revision   int64  `json:"revision"`
}

func (s *Service) RefreshModels(ctx context.Context, hash, id string) (ProfileDraft, error) {
	resolved, err := s.ResolveProfile(ctx, hash, id)
	if err != nil {
		return ProfileDraft{}, err
	}
	options := s.effects
	options.Connections = hardenllm.ConnectionCatalog{"generation": resolved.Connection}
	options.DefaultConnection = "generation"
	options.Cache = nil
	client, err := hardenllm.New(options)
	if err != nil {
		return ProfileDraft{}, credentialInvalid()
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	models, err := client.Models(bounded, "generation")
	if err != nil {
		return ProfileDraft{}, &OperationError{Code: "profile_invalid", Message: "Provider model discovery failed; the saved profile was not changed.", cause: err}
	}
	definition, err := parseDefinition(resolved.Definition)
	if err != nil {
		return ProfileDraft{}, err
	}
	definitions := make([]Model, len(models))
	for i, m := range models {
		definitions[i] = Model{ID: m.ID, Label: m.Label}
	}
	definition.Models = NormalizeModels(definitions)
	now := s.now().UTC()
	definition.LastModelRefreshAt = &now
	if err = validateProfile(definition, "definition"); err != nil {
		return ProfileDraft{}, err
	}
	resolved.Definition, err = json.Marshal(definition)
	if err != nil {
		return ProfileDraft{}, err
	}
	return resolved.ProfileDraft, nil
}
func (s *Service) ExportBundle(ctx context.Context, hash string) ([]byte, error) {
	if err := checkScope(hash); err != nil {
		return nil, err
	}
	id, err := s.newID()
	if err != nil {
		return nil, &OperationError{Code: "profile_invalid", Message: "Cannot allocate bundle identity."}
	}
	bundle := ProfileBundle{SchemaVersion: 4, BundleID: id, CreatedAt: s.now().UTC(), APIKeyHash: hash, Profiles: []BundleProfile{}, HiddenPresets: []string{}, Credentials: []BundleCredential{}}
	err = withStorageTx(ctx, s.config.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}, func(ctx context.Context, tx pgx.Tx) error {
		views, err := s.listProfilesTx(ctx, tx, hash)
		if err != nil {
			return err
		}
		for _, view := range views {
			if view.Kind != "saved" {
				continue
			}
			draft, err := s.profileDraftTx(ctx, tx, hash, view.ID)
			if err != nil {
				return err
			}
			bundle.Profiles = append(bundle.Profiles, BundleProfile{Profile: view, Inputs: draft.Inputs, Binding: draft.Binding})
		}
		rows, err := tx.Query(ctx, "SELECT preset_id FROM hidden_presets WHERE api_key_hash=$1 ORDER BY preset_id", hash)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			bundle.HiddenPresets = append(bundle.HiddenPresets, id)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, "SELECT id,origin,key_id,nonce,ciphertext,revision FROM credentials WHERE api_key_hash=$1 ORDER BY id", hash)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c BundleCredential
			var nonce, ciphertext []byte
			if err = rows.Scan(&c.ID, &c.Origin, &c.KeyID, &nonce, &ciphertext, &c.Revision); err != nil {
				return err
			}
			c.Nonce = base64.StdEncoding.EncodeToString(nonce)
			c.Ciphertext = base64.StdEncoding.EncodeToString(ciphertext)
			bundle.Credentials = append(bundle.Credentials, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, storageFailure(err)
	}
	encoded, err := json.Marshal(bundle)
	if err != nil || len(encoded) > maxBundleBytes {
		return nil, bundleInvalid()
	}
	return encoded, nil
}
func bundleInvalid() error {
	return &ValidationError{Code: "profile_invalid", FieldErrors: []FieldError{{Field: "bundle", Message: "Use a current-format bundle for this access key and encryption key with complete valid bindings."}}}
}
func bundleCredentialRecord(hash string, c BundleCredential, now time.Time) (CredentialRecord, error) {
	nonce, err := base64.StdEncoding.DecodeString(c.Nonce)
	if err != nil || base64.StdEncoding.EncodeToString(nonce) != c.Nonce || len(nonce) != 12 {
		return CredentialRecord{}, bundleInvalid()
	}
	ciphertext, err := base64.StdEncoding.DecodeString(c.Ciphertext)
	if err != nil || base64.StdEncoding.EncodeToString(ciphertext) != c.Ciphertext || len(ciphertext) < 16 || c.KeyID != "primary" || c.Revision <= 0 {
		return CredentialRecord{}, bundleInvalid()
	}
	binding, err := normalizeCredentialBinding(CredentialBinding{APIKeyHash: hash, CredentialID: c.ID, Origin: c.Origin})
	if err != nil {
		return CredentialRecord{}, bundleInvalid()
	}
	return CredentialRecord{Binding: binding, Encrypted: EncryptedCredential{SchemaVersion: 1, Algorithm: "AES-256-GCM", KeyID: "primary", Nonce: base64.RawURLEncoding.EncodeToString(nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext)}, Revision: c.Revision, CreatedAt: now, UpdatedAt: now}, nil
}
func (s *Service) ReplaceBundle(ctx context.Context, hash string, data []byte) error {
	if err := checkScope(hash); err != nil {
		return err
	}
	var bundle ProfileBundle
	if len(data) > maxBundleBytes || decodeStrict(data, &bundle) != nil || bundle.SchemaVersion != 4 || bundle.APIKeyHash != hash || bundle.CreatedAt.IsZero() || bundle.Profiles == nil || bundle.HiddenPresets == nil || bundle.Credentials == nil {
		return bundleInvalid()
	}
	if _, err := uuid.Parse(bundle.BundleID); err != nil {
		return bundleInvalid()
	}
	now := s.now().UTC()
	credentials := make(map[string]CredentialRecord, len(bundle.Credentials))
	for _, c := range bundle.Credentials {
		r, err := bundleCredentialRecord(hash, c, now)
		if err != nil {
			return err
		}
		if _, ok := credentials[c.ID]; ok {
			return bundleInvalid()
		}
		if s.config.Vault == nil {
			return &OperationError{Code: "reference_configuration_error", Message: "Entered credential encryption is not configured."}
		}
		if _, err = s.config.Vault.Open(r.Encrypted, r.Binding); err != nil {
			return bundleInvalid()
		}
		credentials[c.ID] = r
	}
	records := make([]ProfileRecord, 0, len(bundle.Profiles))
	resolved := make([]ResolvedProfile, 0, len(bundle.Profiles))
	ids := map[string]bool{}
	for _, entry := range bundle.Profiles {
		if _, err := uuid.Parse(entry.Profile.ID); err != nil || ids[entry.Profile.ID] || entry.Profile.Kind != "saved" || entry.Profile.CreatedAt == nil || entry.Profile.CreatedAt.IsZero() {
			return bundleInvalid()
		}
		ids[entry.Profile.ID] = true
		input, err := validateSnapshot(SaveProfileInput{Name: entry.Profile.Name, PresetID: entry.Profile.PresetID, Definition: entry.Profile.Definition, Inputs: entry.Inputs, Binding: entry.Binding})
		if err != nil {
			return bundleInvalid()
		}
		var candidate *CredentialRecord
		if entry.Binding.Source == "entered" {
			r, ok := credentials[entry.Binding.EnteredCredentialID]
			if !ok {
				return bundleInvalid()
			}
			candidate = &r
		}
		// Every entered binding is supplied by this bundle. Deployment bindings use
		// the immutable startup snapshot; neither resolution path needs a SQL read.
		r, err := s.resolveDraftTx(ctx, nil, hash, ProfileDraft{Definition: input.Definition, Inputs: input.Inputs, Binding: input.Binding}, candidate)
		if err != nil {
			return bundleInvalid()
		}
		resolved = append(resolved, r)
		records = append(records, ProfileRecord{APIKeyHash: hash, ID: entry.Profile.ID, Name: entry.Profile.Name, PresetID: entry.Profile.PresetID, Definition: input.Definition, Inputs: input.Inputs, Binding: input.Binding, CreatedAt: entry.Profile.CreatedAt.UTC()})
	}
	hidden := map[string]bool{}
	for _, id := range bundle.HiddenPresets {
		if hidden[id] || validateProfileName(id) != nil {
			return bundleInvalid()
		}
		hidden[id] = true
	}
	// Bounded candidate probes finish before the replacement transaction begins.
	for _, r := range resolved {
		if err := s.probeProfile(ctx, r); err != nil {
			return err
		}
	}
	err := withStorageTx(ctx, s.config.Pool, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		for _, table := range []string{"profiles", "hidden_presets", "credentials"} {
			if _, err := tx.Exec(ctx, "DELETE FROM "+table+" WHERE api_key_hash=$1", hash); err != nil {
				return err
			}
		}
		if s.afterBundleDelete != nil {
			if err := s.afterBundleDelete(); err != nil {
				return err
			}
		}
		for _, r := range credentials {
			if err := insertCredentialTx(ctx, tx, r, now); err != nil {
				return err
			}
		}
		for _, r := range records {
			if err := insertProfileTx(ctx, tx, r); err != nil {
				return err
			}
		}
		for _, id := range bundle.HiddenPresets {
			if _, err := tx.Exec(ctx, "INSERT INTO hidden_presets(api_key_hash,preset_id,created_at) VALUES($1,$2,$3)", hash, id, now); err != nil {
				return err
			}
		}
		return nil
	})
	return storageFailure(err)
}
func insertCredentialTx(ctx context.Context, tx pgx.Tx, record CredentialRecord, now time.Time) error {
	nonce, err := base64.RawURLEncoding.DecodeString(record.Encrypted.Nonce)
	if err != nil {
		return credentialInvalid()
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(record.Encrypted.Ciphertext)
	if err != nil {
		return credentialInvalid()
	}
	var revision int64
	err = tx.QueryRow(ctx, `INSERT INTO credentials(api_key_hash,id,origin,key_id,nonce,ciphertext,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8) ON CONFLICT (api_key_hash,id) DO NOTHING RETURNING revision`, record.Binding.APIKeyHash, record.Binding.CredentialID, record.Binding.Origin, record.Encrypted.KeyID, nonce, ciphertext, record.Revision, now).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return revisionConflict()
	}
	return err
}
