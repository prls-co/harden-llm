package stateful

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	hardenllm "github.com/prls-co/harden-llm"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const SchemaVersion = 4
const MaxProfileNameBytes = 1500
const MaxSnapshotBytes = 256 << 10

type Catalog map[string]Preset
type Preset struct {
	Name       string                   `json:"name"`
	Definition json.RawMessage          `json:"definition"`
	Inputs     json.RawMessage          `json:"inputs"`
	Binding    ProfileCredentialBinding `json:"credentialBinding"`
}
type ProfileCredentialBinding struct {
	Source               string `json:"source"`
	DeploymentUpstreamID string `json:"deploymentUpstreamId,omitempty"`
	EnteredCredentialID  string `json:"enteredCredentialId,omitempty"`
}
type SaveProfileInput struct {
	Name       string                   `json:"name,omitempty"`
	PresetID   string                   `json:"presetId,omitempty"`
	Definition json.RawMessage          `json:"definition"`
	Inputs     json.RawMessage          `json:"inputs"`
	Binding    ProfileCredentialBinding `json:"credentialBinding"`
	Credential *EnteredCredentialInput  `json:"credential,omitempty"`
}
type EnteredCredentialInput struct {
	ID       string            `json:"id,omitempty"`
	Revision int64             `json:"revision"`
	Origin   string            `json:"origin"`
	Payload  CredentialPayload `json:"payload"`
}
type ProfileRecord struct {
	APIKeyHash string                   `json:"-"`
	ID         string                   `json:"id"`
	Name       string                   `json:"name"`
	PresetID   string                   `json:"presetId,omitempty"`
	Definition json.RawMessage          `json:"definition"`
	Inputs     json.RawMessage          `json:"inputs"`
	Binding    ProfileCredentialBinding `json:"credentialBinding"`
	CreatedAt  time.Time                `json:"createdAt"`
}
type CredentialStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
	ID         string `json:"id,omitempty"`
	Revision   int64  `json:"revision,omitempty"`
	Message    string `json:"message,omitempty"`
}
type ProfileView struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	Kind             string           `json:"kind"`
	PresetID         string           `json:"presetId,omitempty"`
	CreatedAt        *time.Time       `json:"createdAt,omitempty"`
	Definition       json.RawMessage  `json:"definition"`
	CredentialStatus CredentialStatus `json:"credentialStatus"`
}
type ProfileDraft struct {
	SourceProfileID  string                   `json:"sourceProfileId,omitempty"`
	PresetID         string                   `json:"presetId,omitempty"`
	Definition       json.RawMessage          `json:"definition"`
	Inputs           json.RawMessage          `json:"inputs"`
	Binding          ProfileCredentialBinding `json:"credentialBinding,omitempty"`
	CredentialStatus CredentialStatus         `json:"credentialStatus"`
}
type ResolvedProfile struct {
	ProfileDraft
	Connection         hardenllm.Connection
	CredentialRevision int64
}

func decodeStrict(input []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(input))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(out); err != nil {
		return err
	}
	return requireJSONEOF(d)
}
func validateSnapshot(input SaveProfileInput) (SaveProfileInput, error) {
	encoded, err := json.Marshal(input)
	if err != nil || len(encoded) > MaxSnapshotBytes {
		return SaveProfileInput{}, validationFailure("snapshot", "Snapshot must contain at most 256 KiB of JSON.")
	}
	if input.Name != "" {
		if err := validateProfileName(input.Name); err != nil {
			return SaveProfileInput{}, validationFailure("name", err.Error())
		}
	}
	if input.PresetID != "" {
		if err := validateProfileName(input.PresetID); err != nil {
			return SaveProfileInput{}, validationFailure("presetId", err.Error())
		}
	}
	definition, err := parseDefinition(input.Definition)
	if err != nil {
		return SaveProfileInput{}, err
	}
	if err := validateInputs(input.Inputs, definition); err != nil {
		return SaveProfileInput{}, err
	}
	if input.Credential != nil && input.Credential.Revision < 0 {
		return SaveProfileInput{}, validationFailure("credential.revision", "Credential revision must be nonnegative.")
	}
	binding := input.Binding
	validID := func(s string) bool {
		return s != "" && strings.TrimSpace(s) == s && utf8.ValidString(s) && len(s) <= 128 && !strings.ContainsAny(s, "\x00\r\n")
	}
	if !((binding.Source == "deployment" && validID(binding.DeploymentUpstreamID) && binding.EnteredCredentialID == "") || (binding.Source == "entered" && validID(binding.EnteredCredentialID) && binding.DeploymentUpstreamID == "")) {
		return SaveProfileInput{}, validationFailure("credentialBinding", "Choose exactly one deployment or entered credential binding.")
	}
	if input.Credential != nil && (binding.Source != "entered" || input.Credential.ID != "" && input.Credential.ID != binding.EnteredCredentialID) {
		return SaveProfileInput{}, validationFailure("credential", "Credential must match the entered binding.")
	}
	input.Definition, err = json.Marshal(definition)
	if err != nil {
		return SaveProfileInput{}, err
	}
	input.Inputs = bytes.Clone(input.Inputs)
	if input.Credential != nil {
		c := *input.Credential
		c.Payload.Headers = maps.Clone(c.Payload.Headers)
		input.Credential = &c
	}
	return input, nil
}
func snapshotRecord(hash string, input SaveProfileInput, now time.Time, id string) (ProfileRecord, error) {
	input, err := validateSnapshot(input)
	if err != nil {
		return ProfileRecord{}, err
	}
	parsed, err := uuid.Parse(id)
	if err != nil || now.IsZero() {
		return ProfileRecord{}, validationFailure("snapshot", "Server identity and creation time are required.")
	}
	base := input.Name
	if base == "" {
		base = input.PresetID
	}
	if base == "" {
		return ProfileRecord{}, validationFailure("name", "A name or starting template is required.")
	}
	name := fmt.Sprintf("%s · %s · %s", base, now.UTC().Format("2006-01-02 15:04Z"), strings.ReplaceAll(parsed.String(), "-", "")[:6])
	if err := validateProfileName(name); err != nil {
		return ProfileRecord{}, validationFailure("name", err.Error())
	}
	return ProfileRecord{APIKeyHash: hash, ID: parsed.String(), Name: name, PresetID: input.PresetID, Definition: input.Definition, Inputs: input.Inputs, Binding: input.Binding, CreatedAt: now.UTC()}, nil
}
func presetDraft(id string, p Preset) ProfileDraft {
	return ProfileDraft{SourceProfileID: id, PresetID: id, Definition: bytes.Clone(p.Definition), Inputs: bytes.Clone(p.Inputs), Binding: p.Binding, CredentialStatus: CredentialStatus{Source: p.Binding.Source}}
}
func profileView(r ProfileRecord, status CredentialStatus) ProfileView {
	return ProfileView{ID: r.ID, Name: r.Name, Kind: "saved", PresetID: r.PresetID, CreatedAt: &r.CreatedAt, Definition: bytes.Clone(r.Definition), CredentialStatus: status}
}
func recordDraft(r ProfileRecord, status CredentialStatus) ProfileDraft {
	return ProfileDraft{SourceProfileID: r.ID, PresetID: r.PresetID, Definition: bytes.Clone(r.Definition), Inputs: bytes.Clone(r.Inputs), Binding: r.Binding, CredentialStatus: status}
}
func presetViews(catalog Catalog, hidden map[string]bool) []ProfileView {
	ids := make([]string, 0, len(catalog))
	for id := range catalog {
		if !hidden[id] {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	result := make([]ProfileView, 0, len(ids))
	for _, id := range ids {
		p := catalog[id]
		result = append(result, ProfileView{ID: id, Name: p.Name, Kind: "preset", Definition: bytes.Clone(p.Definition), CredentialStatus: CredentialStatus{Source: p.Binding.Source}})
	}
	return result
}
func parseSaveProfile(input []byte) (SaveProfileInput, error) {
	var value SaveProfileInput
	if err := decodeStrict(input, &value); err != nil {
		return SaveProfileInput{}, validationFailure("snapshot", "Snapshot has an unknown field or invalid value.")
	}
	return validateSnapshot(value)
}

type profileInputs struct {
	ModelID         string           `json:"modelId,omitempty"`
	SystemPrompt    string           `json:"systemPrompt,omitempty"`
	UserPrompt      string           `json:"userPrompt,omitempty"`
	CallType        string           `json:"callType,omitempty"`
	Schema          json.RawMessage  `json:"schema,omitempty"`
	SchemaShorthand string           `json:"schemaShorthand,omitempty"`
	ReasoningEffort string           `json:"reasoningEffort,omitempty"`
	ProviderOptions map[string]any   `json:"providerOptions,omitempty"`
	CacheMode       string           `json:"cacheMode,omitempty"`
	WebSearch       bool             `json:"webSearch,omitempty"`
	CacheVersion    string           `json:"cacheVersion,omitempty"`
	TimeoutMS       int              `json:"timeoutMs,omitempty"`
	RecoveryPolicy  json.RawMessage  `json:"recoveryPolicy,omitempty"`
	Origin          hardenllm.Origin `json:"origin,omitempty"`
}

func validateInputs(input json.RawMessage, definition Profile) error {
	var value profileInputs
	if len(input) == 0 || string(input) == "null" || decodeStrict(input, &value) != nil {
		return validationFailure("inputs", "Inputs must be an object with supported fields.")
	}
	if value.CallType != "" && value.CallType != "text" && value.CallType != "structured" {
		return validationFailure("inputs.callType", "Call type must be text or structured.")
	}
	if value.TimeoutMS < 0 || value.TimeoutMS > 60000 || len(value.ModelID) > 512 || len(value.SystemPrompt) > 32768 || len(value.UserPrompt) > 65536 || len(value.SchemaShorthand) > 65536 || len(value.CacheVersion) > 64 {
		return validationFailure("inputs", "Input field exceeds its bound.")
	}
	if value.ReasoningEffort != "" && value.ReasoningEffort != "lowest" && value.ReasoningEffort != "middle" && value.ReasoningEffort != "highest" {
		return validationFailure("inputs.reasoningEffort", "Reasoning effort is unsupported.")
	}
	if value.CacheMode != "" && value.CacheMode != "off" && value.CacheMode != "cache" && value.CacheMode != "refresh" {
		return validationFailure("inputs.cacheMode", "Cache mode is unsupported.")
	}
	if len(value.Schema) > 0 {
		var object map[string]any
		if json.Unmarshal(value.Schema, &object) != nil || object == nil {
			return validationFailure("inputs.schema", "Schema must be an object.")
		}
	}
	if field := forbiddenOptionField(value.ProviderOptions, "inputs.providerOptions"); field != "" {
		return validationFailure(field, "Options cannot contain credentials or stored results.")
	}
	policy := definition.RecoveryPolicy
	if len(value.RecoveryPolicy) > 0 {
		policy = value.RecoveryPolicy
	}
	core, _, err := resolveRecovery(policy, ResolvedProfile{ProfileDraft: definitionForValidation(definition.ModelID)}, func(string) (ResolvedProfile, error) {
		return ResolvedProfile{ProfileDraft: definitionForValidation("validation-model")}, nil
	})
	if err != nil {
		return err
	}
	return core.ValidateProviderOptions(value.ProviderOptions)
}
