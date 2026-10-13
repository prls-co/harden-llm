package stateful

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	hardenllm "github.com/prls-co/harden-llm"
	"net/url"
	"time"
)

func (s *Service) ListProfiles(ctx context.Context, hash string) (views []ProfileView, err error) {
	if err = checkScope(hash); err != nil {
		return nil, err
	}
	err = withStorageTx(ctx, s.config.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		views, err = s.listProfilesTx(ctx, tx, hash)
		return err
	})
	return views, storageFailure(err)
}
func (s *Service) listProfilesTx(ctx context.Context, tx pgx.Tx, hash string) ([]ProfileView, error) {
	hidden := map[string]bool{}
	rows, err := tx.Query(ctx, "SELECT preset_id FROM hidden_presets WHERE api_key_hash=$1", hash)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		hidden[id] = true
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	views := presetViews(s.config.Presets, hidden)
	for i := range views {
		views[i].CredentialStatus = s.bindingStatus(s.config.Presets[views[i].ID].Binding, 0)
	}
	rows, err = tx.Query(ctx, `SELECT p.id,p.name,COALESCE(p.preset_id,''),p.definition,p.credential_source,COALESCE(p.deployment_upstream_id,''),COALESCE(p.entered_credential_id,''),p.created_at,COALESCE(c.revision,0)
   FROM profiles p LEFT JOIN credentials c ON c.api_key_hash=p.api_key_hash AND c.id=p.entered_credential_id WHERE p.api_key_hash=$1 ORDER BY p.created_at DESC,p.id DESC`, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var record ProfileRecord
		var revision int64
		if err = rows.Scan(&record.ID, &record.Name, &record.PresetID, &record.Definition, &record.Binding.Source, &record.Binding.DeploymentUpstreamID, &record.Binding.EnteredCredentialID, &record.CreatedAt, &revision); err != nil {
			return nil, err
		}
		views = append(views, profileView(record, s.bindingStatus(record.Binding, revision)))
	}
	return views, rows.Err()
}

func (s *Service) bindingStatus(binding ProfileCredentialBinding, revision int64) CredentialStatus {
	status := CredentialStatus{Source: binding.Source, Revision: revision}
	if binding.Source == "deployment" {
		status.ID = binding.DeploymentUpstreamID
		c, ok := s.effects.Connections[status.ID]
		status.Configured = ok && c.APIKey != ""
	} else {
		status.ID = binding.EnteredCredentialID
		status.Configured = revision > 0 && s.config.Vault != nil
	}
	if !status.Configured {
		status.Message = "Select or supply an explicitly configured provider credential."
	}
	return status
}
func (s *Service) GetProfile(ctx context.Context, hash, id string) (draft ProfileDraft, err error) {
	if err = checkScope(hash); err != nil {
		return draft, err
	}
	err = withStorageTx(ctx, s.config.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}, func(ctx context.Context, tx pgx.Tx) error {
		var readErr error
		draft, readErr = s.profileDraftTx(ctx, tx, hash, id)
		return readErr
	})
	return draft, storageFailure(err)
}
func (s *Service) profileDraftTx(ctx context.Context, tx pgx.Tx, hash, id string) (ProfileDraft, error) {
	if p, ok := s.config.Presets[id]; ok {
		var hidden bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM hidden_presets WHERE api_key_hash=$1 AND preset_id=$2)", hash, id).Scan(&hidden); err != nil {
			return ProfileDraft{}, err
		}
		if hidden {
			return ProfileDraft{}, &OperationError{Code: "preset_not_found", Message: "Starting template is not available for this access key."}
		}
		draft := presetDraft(id, p)
		draft.CredentialStatus = s.bindingStatus(p.Binding, 0)
		return draft, nil
	}
	if _, err := uuid.Parse(id); err != nil {
		return ProfileDraft{}, &OperationError{Code: "profile_not_found", Message: "Profile was not found for this access key."}
	}
	var r ProfileRecord
	var revision int64
	err := tx.QueryRow(ctx, `SELECT p.id,p.name,COALESCE(p.preset_id,''),p.definition,p.inputs,p.credential_source,COALESCE(p.deployment_upstream_id,''),COALESCE(p.entered_credential_id,''),p.created_at,COALESCE(c.revision,0)
  FROM profiles p LEFT JOIN credentials c ON c.api_key_hash=p.api_key_hash AND c.id=p.entered_credential_id WHERE p.api_key_hash=$1 AND p.id=$2`, hash, id).Scan(&r.ID, &r.Name, &r.PresetID, &r.Definition, &r.Inputs, &r.Binding.Source, &r.Binding.DeploymentUpstreamID, &r.Binding.EnteredCredentialID, &r.CreatedAt, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProfileDraft{}, &OperationError{Code: "profile_not_found", Message: "Profile was not found for this access key."}
	}
	if err != nil {
		return ProfileDraft{}, err
	}
	return recordDraft(r, s.bindingStatus(r.Binding, revision)), nil
}
func (s *Service) ResolveProfile(ctx context.Context, hash, id string) (resolved ResolvedProfile, err error) {
	if err = checkScope(hash); err != nil {
		return resolved, err
	}
	err = withStorageTx(ctx, s.config.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}, func(ctx context.Context, tx pgx.Tx) error {
		draft, err := s.profileDraftTx(ctx, tx, hash, id)
		if err != nil {
			return err
		}
		resolved, err = s.resolveDraftTx(ctx, tx, hash, draft, nil)
		return err
	})
	return resolved, storageFailure(err)
}
func originForURL(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	return normalizeOrigin(parsed.Scheme + "://" + parsed.Host)
}
func credentialInvalid() error {
	return &ValidationError{Code: "credential_invalid", FieldErrors: []FieldError{{Field: "credentialBinding", Message: "Select or provide a credential authorized for this access key and provider origin."}}}
}
func (s *Service) resolveDraftTx(ctx context.Context, tx pgx.Tx, hash string, draft ProfileDraft, candidate *CredentialRecord) (ResolvedProfile, error) {
	definition, err := parseDefinition(draft.Definition)
	if err != nil {
		return ResolvedProfile{}, err
	}
	origin, err := originForURL(definition.BaseURL)
	if err != nil {
		return ResolvedProfile{}, credentialInvalid()
	}
	c := hardenllm.Connection{ID: "generation", Provider: definition.Provider, Protocol: definition.APIInferenceType, BaseURL: definition.BaseURL, SupportsWebSearch: NativeWebSearchSupported(definition)}
	if definition.Pricing != nil {
		p := definition.Pricing
		c.Pricing = &hardenllm.Pricing{Input: p.Input, CacheRead: p.CacheRead, CacheCreation: p.CacheCreation, Output: p.Output, Reasoning: p.Reasoning}
	}
	var revision int64
	switch draft.Binding.Source {
	case "deployment":
		loaded, ok := s.effects.Connections[draft.Binding.DeploymentUpstreamID]
		if !ok || loaded.APIKey == "" {
			return ResolvedProfile{}, credentialInvalid()
		}
		loadedOrigin, err := originForURL(loaded.BaseURL)
		if err != nil || origin != loadedOrigin {
			return ResolvedProfile{}, credentialInvalid()
		}
		c.APIKey = loaded.APIKey
		c.Headers = cloneHeaderMap(loaded.Headers)
		c.CacheDomain = loaded.CacheDomain
	case "entered":
		binding := CredentialBinding{APIKeyHash: hash, CredentialID: draft.Binding.EnteredCredentialID, Origin: origin}
		if candidate == nil {
			candidate, err = readCredentialTx(ctx, tx, hash, binding.CredentialID)
			if err != nil {
				return ResolvedProfile{}, err
			}
		}
		if candidate == nil || candidate.Binding != binding {
			return ResolvedProfile{}, credentialInvalid()
		}
		payload, err := s.config.Vault.openRecord(candidate, binding)
		if err != nil {
			return ResolvedProfile{}, err
		}
		c.APIKey = payload.APIKey
		c.Headers = payload.Headers
		revision = candidate.Revision
	default:
		return ResolvedProfile{}, credentialInvalid()
	}
	draft.CredentialStatus = s.bindingStatus(draft.Binding, revision)
	return ResolvedProfile{ProfileDraft: draft, Connection: c.Clone(), CredentialRevision: revision}, nil
}
func readCredentialTx(ctx context.Context, tx pgx.Tx, hash, id string) (*CredentialRecord, error) {
	var r CredentialRecord
	var nonce, ciphertext []byte
	err := tx.QueryRow(ctx, "SELECT origin,key_id,nonce,ciphertext,revision,created_at,updated_at FROM credentials WHERE api_key_hash=$1 AND id=$2", hash, id).Scan(&r.Binding.Origin, &r.Encrypted.KeyID, &nonce, &ciphertext, &r.Revision, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Binding.APIKeyHash = hash
	r.Binding.CredentialID = id
	r.Encrypted.SchemaVersion = 1
	r.Encrypted.Algorithm = "AES-256-GCM"
	r.Encrypted.Nonce = base64.RawURLEncoding.EncodeToString(nonce)
	r.Encrypted.Ciphertext = base64.RawURLEncoding.EncodeToString(ciphertext)
	return &r, nil
}
func (s *Service) SaveProfile(ctx context.Context, hash string, input SaveProfileInput) (ProfileView, error) {
	if err := checkScope(hash); err != nil {
		return ProfileView{}, err
	}
	input, err := validateSnapshot(input)
	if err != nil {
		return ProfileView{}, err
	}
	var candidate *CredentialRecord
	if input.Credential != nil {
		if s.config.Vault == nil {
			return ProfileView{}, &OperationError{Code: "reference_configuration_error", Message: "Entered credential encryption is not configured."}
		}
		origin, err := normalizeOrigin(input.Credential.Origin)
		if err != nil {
			return ProfileView{}, credentialInvalid()
		}
		binding := CredentialBinding{APIKeyHash: hash, CredentialID: input.Binding.EnteredCredentialID, Origin: origin}
		encrypted, err := s.config.Vault.Seal(input.Credential.Payload, binding)
		if err != nil {
			return ProfileView{}, credentialInvalid()
		}
		candidate = &CredentialRecord{Binding: binding, Encrypted: encrypted, Revision: input.Credential.Revision + 1}
	}
	var resolved ResolvedProfile
	err = withStorageTx(ctx, s.config.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}, func(ctx context.Context, tx pgx.Tx) error {
		if input.Credential != nil {
			old, err := readCredentialTx(ctx, tx, hash, input.Binding.EnteredCredentialID)
			if err != nil {
				return err
			}
			revision := int64(0)
			if old != nil {
				revision = old.Revision
			}
			if revision != input.Credential.Revision {
				return revisionConflict(revision)
			}
		}
		var err error
		resolved, err = s.resolveDraftTx(ctx, tx, hash, ProfileDraft{Definition: input.Definition, Inputs: input.Inputs, Binding: input.Binding}, candidate)
		return err
	})
	if err != nil {
		return ProfileView{}, storageFailure(err)
	}
	if err = s.probeProfile(ctx, resolved); err != nil {
		return ProfileView{}, err
	}
	id, err := s.newID()
	if err != nil {
		return ProfileView{}, &OperationError{Code: "profile_invalid", Message: "Cannot allocate saved profile identity."}
	}
	now := s.now().UTC()
	record, err := snapshotRecord(hash, input, now, id)
	if err != nil {
		return ProfileView{}, err
	}
	err = withStorageTx(ctx, s.config.Pool, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if candidate != nil {
			if err := saveCredentialTx(ctx, tx, *candidate, input.Credential.Revision, now); err != nil {
				return err
			}
			if s.afterCredentialInsert != nil {
				if err := s.afterCredentialInsert(); err != nil {
					return err
				}
			}
		}
		return insertProfileTx(ctx, tx, record)
	})
	if err != nil {
		return ProfileView{}, storageFailure(err)
	}
	return profileView(record, resolved.CredentialStatus), nil
}
func revisionConflict(revision int64) error {
	return &OperationError{Code: "revision_conflict", Message: "The credential or draft revision changed; reload before saving.", CurrentRevision: &revision}
}
func saveCredentialTx(ctx context.Context, tx pgx.Tx, record CredentialRecord, expected int64, now time.Time) error {
	if expected == 0 {
		return insertCredentialTx(ctx, tx, record, now)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(record.Encrypted.Nonce)
	if err != nil {
		return credentialInvalid()
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(record.Encrypted.Ciphertext)
	if err != nil {
		return credentialInvalid()
	}
	var revision int64
	err = tx.QueryRow(ctx, `UPDATE credentials SET origin=$3,key_id=$4,nonce=$5,ciphertext=$6,revision=revision+1,updated_at=$8 WHERE api_key_hash=$1 AND id=$2 AND revision=$7 RETURNING revision`, record.Binding.APIKeyHash, record.Binding.CredentialID, record.Binding.Origin, record.Encrypted.KeyID, nonce, ciphertext, expected, now).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return revisionConflictTx(ctx, tx, "SELECT revision FROM credentials WHERE api_key_hash=$1 AND id=$2", record.Binding.APIKeyHash, record.Binding.CredentialID)
	}
	return err
}

func (s *Service) probeProfile(ctx context.Context, resolved ResolvedProfile) error {
	var definition Profile
	if err := json.Unmarshal(resolved.Definition, &definition); err != nil {
		return err
	}
	options := s.effects
	options.Connections = hardenllm.ConnectionCatalog{"generation": resolved.Connection}
	options.DefaultConnection = "generation"
	options.Cache = nil
	client, err := hardenllm.New(options)
	if err != nil {
		return credentialInvalid()
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = client.Call(bounded, hardenllm.Request{ModelID: definition.ModelID, CallType: hardenllm.CallTypeText, Messages: []hardenllm.Message{{Role: "user", Content: json.RawMessage(`"Reply with OK."`)}}, CacheMode: hardenllm.CacheModeOff, RecoveryPolicy: hardenllm.RecoveryPolicy{MaxAttempts: 1, RetryOn: []hardenllm.RecoveryCategory{}, Backoff: hardenllm.RecoveryBackoff{}}})
	if err != nil {
		return &OperationError{Code: "profile_invalid", Message: "The candidate provider probe failed; no profile was saved.", cause: err}
	}
	return nil
}
func (s *Service) DeleteProfile(ctx context.Context, hash, id string) error {
	if err := checkScope(hash); err != nil {
		return err
	}
	err := withStorageTx(ctx, s.config.Pool, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, ok := s.config.Presets[id]; ok {
			_, err := tx.Exec(ctx, "INSERT INTO hidden_presets(api_key_hash,preset_id,created_at) VALUES($1,$2,$3) ON CONFLICT (api_key_hash,preset_id) DO NOTHING", hash, id, s.now().UTC())
			return err
		}
		if _, err := uuid.Parse(id); err != nil {
			return &OperationError{Code: "profile_not_found", Message: "Profile was not found for this access key."}
		}
		tag, err := tx.Exec(ctx, "DELETE FROM profiles WHERE api_key_hash=$1 AND id=$2", hash, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return &OperationError{Code: "profile_not_found", Message: "Profile was not found for this access key."}
		}
		return nil
	})
	return storageFailure(err)
}

func insertProfileTx(ctx context.Context, tx pgx.Tx, record ProfileRecord) error {
	_, err := tx.Exec(ctx, `INSERT INTO profiles(api_key_hash,id,name,preset_id,definition,inputs,credential_source,deployment_upstream_id,entered_credential_id,created_at) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,NULLIF($8,''),NULLIF($9,''),$10)`, record.APIKeyHash, record.ID, record.Name, record.PresetID, record.Definition, record.Inputs, record.Binding.Source, record.Binding.DeploymentUpstreamID, record.Binding.EnteredCredentialID, record.CreatedAt)
	return err
}
