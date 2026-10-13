package stateful

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/providers"
	"slices"
	"strings"
)

type referenceRecoveryTarget struct {
	Source          string         `json:"source"`
	ProfileID       string         `json:"profileId,omitempty"`
	ModelID         string         `json:"modelId,omitempty"`
	ReasoningEffort string         `json:"reasoningEffort,omitempty"`
	ProviderOptions map[string]any `json:"providerOptions,omitempty"`
	Upstream        string         `json:"upstream,omitempty"`
}

// mapRecoveryTargets owns the single walk over the five explicit policy leaves.
func mapRecoveryTargets(policy json.RawMessage, apply func(json.RawMessage, string) (json.RawMessage, error)) ([]byte, error) {
	var document map[string]json.RawMessage
	if err := decodeStrict(policy, &document); err != nil || document == nil {
		return nil, validationFailure("recoveryPolicy", "Recovery policy must be an object.")
	}
	mapPlan := func(data json.RawMessage, prefix string) (json.RawMessage, error) {
		if len(data) == 0 || strings.TrimSpace(string(data)) == "null" {
			return data, nil
		}
		var plan map[string]json.RawMessage
		if err := decodeStrict(data, &plan); err != nil || plan == nil {
			return nil, validationFailure("jsonRepair", "Repair plan must be an object.")
		}
		for _, leaf := range []string{"initial", "escalation"} {
			if data, ok := plan[leaf]; ok && strings.TrimSpace(string(data)) != "null" {
				value, err := apply(data, prefix+leaf)
				if err != nil {
					return nil, err
				}
				plan[leaf] = value
			}
		}
		return json.Marshal(plan)
	}
	var err error
	if data, ok := document["jsonRepair"]; ok {
		document["jsonRepair"], err = mapPlan(data, "repair-")
		if err != nil {
			return nil, err
		}
	}
	if data, ok := document["rerun"]; ok && strings.TrimSpace(string(data)) != "null" {
		var rerun map[string]json.RawMessage
		if err = decodeStrict(data, &rerun); err != nil || rerun == nil {
			return nil, validationFailure("rerun", "Rerun must be an object or null.")
		}
		if data, ok := rerun["target"]; ok {
			rerun["target"], err = apply(data, "rerun")
			if err != nil {
				return nil, err
			}
		}
		if data, ok := rerun["jsonRepair"]; ok {
			rerun["jsonRepair"], err = mapPlan(data, "rerun-repair-")
			if err != nil {
				return nil, err
			}
		}
		document["rerun"], err = json.Marshal(rerun)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(document)
}

// resolveRecovery translates wrapper profile references once. Core validation
// still owns budgets, categories, option restrictions and recovery semantics.
func resolveRecovery(policy json.RawMessage, generation ResolvedProfile, load func(string) (ResolvedProfile, error)) (hardenllm.RecoveryPolicy, hardenllm.ConnectionCatalog, error) {
	generation.Connection.ID = "generation"
	connections := hardenllm.ConnectionCatalog{"generation": generation.Connection}
	rewriteTarget := func(data json.RawMessage, id string) (json.RawMessage, error) {
		var target referenceRecoveryTarget
		if err := decodeStrict(data, &target); err != nil {
			return nil, validationFailure("recoveryTarget", "Recovery target has an unknown field or invalid value.")
		}
		if target.Source != "profile" {
			if target.ProfileID != "" {
				return nil, validationFailure("recoveryTarget.profileId", "Only profile targets can name profiles.")
			}
			return bytes.Clone(data), nil
		}
		if target.Upstream != "" || validateProfileName(target.ProfileID) != nil {
			return nil, validationFailure("recoveryTarget.profileId", "A profile target requires an authorized profile ID.")
		}
		resolved, err := load(target.ProfileID)
		if err != nil {
			return nil, err
		}
		var definition struct {
			ModelID string `json:"modelId"`
		}
		if err = json.Unmarshal(resolved.Definition, &definition); err != nil {
			return nil, validationFailure("recoveryTarget.profileId", "Resolved profile definition is invalid.")
		}
		model := target.ModelID
		if model == "" {
			model = definition.ModelID
		}
		connection := resolved.Connection
		connection.ID = id
		connections[id] = connection
		return json.Marshal(hardenllm.RecoveryTarget{Source: "model", ConnectionID: id, ModelID: model, ReasoningEffort: target.ReasoningEffort, ProviderOptions: target.ProviderOptions})
	}
	encoded, err := mapRecoveryTargets(policy, rewriteTarget)
	if err != nil {
		return hardenllm.RecoveryPolicy{}, nil, err
	}
	var core hardenllm.RecoveryPolicy
	if err = decodeStrict(encoded, &core); err != nil {
		return hardenllm.RecoveryPolicy{}, nil, err
	}
	if err = core.Validate(); err != nil {
		return hardenllm.RecoveryPolicy{}, nil, err
	}
	return core, connections, nil
}

type resolvedRun struct {
	request        hardenllm.Request
	connections    hardenllm.ConnectionCatalog
	profiles       map[string]ResolvedProfile
	labels         map[string]string
	reasoning      map[string]string
	cacheNamespace string
}

func (s *Service) resolveRun(ctx context.Context, hash string, input RunInput) (resolvedRun, error) {
	resolved := resolvedRun{profiles: map[string]ResolvedProfile{}, labels: map[string]string{"generation": input.ProfileID}, reasoning: map[string]string{"generation": input.ReasoningEffort}}
	ids := map[string]bool{input.ProfileID: true}
	_, err := mapRecoveryTargets(input.RecoveryPolicy, func(data json.RawMessage, id string) (json.RawMessage, error) {
		var target referenceRecoveryTarget
		if err := decodeStrict(data, &target); err != nil {
			return nil, validationFailure("recoveryTarget", "Recovery target has an unknown field or invalid value.")
		}
		if target.Source == "profile" {
			ids[target.ProfileID] = true
			resolved.labels[id] = target.ProfileID
			resolved.reasoning[id] = target.ReasoningEffort
		}
		return data, nil
	})
	if err != nil {
		return resolved, err
	}
	needsSQL := false
	orderedIDs := []string{}
	for id := range ids {
		preset, ok := s.config.Presets[id]
		if !ok {
			if _, err := uuid.Parse(id); err != nil {
				return resolved, &OperationError{Code: "profile_not_found", Message: "Profile was not found for this access key."}
			}
		}
		if !ok || preset.Binding.Source != "deployment" {
			needsSQL = true
		}
		if id != input.ProfileID {
			orderedIDs = append(orderedIDs, id)
		}
	}
	slices.Sort(orderedIDs)
	orderedIDs = append([]string{input.ProfileID}, orderedIDs...)
	load := func(ctx context.Context, tx pgx.Tx) error {
		for _, id := range orderedIDs {
			var draft ProfileDraft
			var err error
			// Git templates are immutable deployment metadata. Hiding a template
			// changes picker visibility; it is not an execution permission.
			if preset, ok := s.config.Presets[id]; ok {
				draft = presetDraft(id, preset)
			} else {
				draft, err = s.profileDraftTx(ctx, tx, hash, id)
			}
			if err != nil {
				return err
			}
			resolved.profiles[id] = ResolvedProfile{ProfileDraft: draft}
			profile, err := s.resolveDraftTx(ctx, tx, hash, draft, nil)
			if err != nil {
				return err
			}
			resolved.profiles[id] = profile
		}
		return nil
	}
	if needsSQL {
		err = withStorageTx(ctx, s.config.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}, load)
	} else {
		err = load(ctx, nil)
	}
	if err != nil {
		return resolved, storageFailure(err)
	}
	generation := resolved.profiles[input.ProfileID]
	policy, connections, err := resolveRecovery(input.RecoveryPolicy, generation, func(id string) (ResolvedProfile, error) {
		p, ok := resolved.profiles[id]
		if !ok {
			return ResolvedProfile{}, &OperationError{Code: "profile_not_found", Message: "Recovery profile was not found for this access key."}
		}
		return p, nil
	})
	if err != nil {
		return resolved, err
	}
	definition, err := parseDefinition(generation.Definition)
	if err != nil {
		return resolved, err
	}
	options, err := profileOptions(definition, input.ReasoningEffort, input.ProviderOptions)
	if err != nil {
		return resolved, err
	}
	model := input.ModelID
	if model == "" {
		model = definition.ModelID
	}
	type leaf struct {
		target *hardenllm.RecoveryTarget
		path   string
	}
	var targets []leaf
	addPlan := func(plan *hardenllm.JSONRepairPlan, path string) {
		if plan != nil {
			targets = append(targets, leaf{&plan.Initial, path + ".initial"})
			if plan.Escalation != nil {
				targets = append(targets, leaf{plan.Escalation, path + ".escalation"})
			}
		}
	}
	addPlan(policy.JSONRepair, "recoveryPolicy.jsonRepair")
	if policy.Rerun != nil {
		targets = append(targets, leaf{&policy.Rerun.Target, "recoveryPolicy.rerun.target"})
		addPlan(policy.Rerun.JSONRepair, "recoveryPolicy.rerun.jsonRepair")
	}
	for _, item := range targets {
		target := item.target
		// Generation leaves inherit the already prepared request unchanged.
		// The core forbids adding overrides to that source.
		if target.Source == "generation" {
			continue
		}
		profile := generation
		effort := input.ReasoningEffort
		if target.Source == "model" {
			profile = resolved.profiles[resolved.labels[target.ConnectionID]]
			effort = ""
		}
		if target.ReasoningEffort != "" {
			effort = target.ReasoningEffort
		}
		definition, err := parseDefinition(profile.Definition)
		if err != nil {
			return resolved, err
		}
		target.ProviderOptions, err = profileOptions(definition, effort, target.ProviderOptions)
		if err != nil {
			var invalid *ValidationError
			if errors.As(err, &invalid) {
				qualified := &ValidationError{Code: invalid.Code}
				for _, field := range invalid.FieldErrors {
					qualified.FieldErrors = append(qualified.FieldErrors, FieldError{Field: item.path + "." + field.Field, Message: field.Message})
				}
				return resolved, qualified
			}
			return resolved, err
		}
		target.ReasoningEffort = ""
	}
	messages := []hardenllm.Message{}
	if input.SystemPrompt != "" {
		data, _ := json.Marshal(input.SystemPrompt)
		messages = append(messages, hardenllm.Message{Role: "system", Content: data})
	}
	data, _ := json.Marshal(input.UserPrompt)
	messages = append(messages, hardenllm.Message{Role: "user", Content: data})
	resolved.request = hardenllm.Request{ConnectionID: "generation", ModelID: model, Messages: messages, CallType: input.CallType, Schema: bytes.Clone(input.Schema), WebSearch: input.WebSearch, ProviderOptions: options, CacheMode: input.CacheMode, CacheVersion: input.CacheVersion, RecoveryPolicy: policy, Origin: input.Origin, Progress: input.Progress}
	if resolved.request.CacheMode == "" {
		resolved.request.CacheMode = hardenllm.CacheModeOff
	}
	if resolved.request.CacheVersion == "" {
		resolved.request.CacheVersion = "operation-v2"
	}
	resolved.connections = connections
	revisions := map[string]int64{}
	for id, label := range resolved.labels {
		revisions[id] = resolved.profiles[label].CredentialRevision
	}
	namespace, _ := json.Marshal(struct {
		Credentials string
		Profiles    map[string]string
	}{credentialNamespace(connections, revisions), resolved.labels})
	digest := sha256.Sum256(namespace)
	resolved.cacheNamespace = hex.EncodeToString(digest[:])
	return resolved, nil
}

// profileOptions expands stored portable settings into one native request map;
// provider payload validation and protocol normalization retain their one owner.
func profileOptions(profile Profile, effort string, overrides map[string]any) (map[string]any, error) {
	if effort != "" && providers.HasReasoningOption(overrides) {
		return nil, validationFailure("providerOptions", "Portable reasoning effort conflicts with native reasoning options.")
	}
	data, err := json.Marshal(profile.DefaultOptions)
	if err != nil {
		return nil, validationFailure("defaultOptions", "Request defaults must be JSON safe.")
	}
	options := map[string]any{}
	if string(data) != "null" {
		if err = json.Unmarshal(data, &options); err != nil {
			return nil, err
		}
	}
	merge := func(values map[string]any) {
		for key, value := range values {
			options = providers.MergeProviderOption(profile.APIInferenceType, options, key, value)
		}
	}
	merge(overrides)
	if effort != "" {
		mapped, ok := profile.ReasoningEffortMap[effort]
		if !ok {
			return nil, validationFailure("reasoningEffort", "The selected profile does not configure this portable reasoning level.")
		}
		merge(mapped)
	}
	if profile.SupportsTemperature == nil || !*profile.SupportsTemperature {
		delete(options, "temperature")
	}
	if profile.APIInferenceType == "chat-completions" && profile.TokensParam != nil && *profile.TokensParam != "" {
		if value, ok := options["max_tokens"]; ok && *profile.TokensParam != "max_tokens" {
			if _, present := options[*profile.TokensParam]; !present {
				options[*profile.TokensParam] = value
			}
			delete(options, "max_tokens")
		}
	}
	return options, nil
}
