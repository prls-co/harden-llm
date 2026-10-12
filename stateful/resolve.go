package stateful

import (
	"bytes"
	"encoding/json"
	hardenllm "github.com/prls-co/harden-llm"
	"strings"
)

// resolveRecovery translates wrapper profile references once. Core validation
// still owns budgets, categories, option restrictions and recovery semantics.
func resolveRecovery(policy json.RawMessage, generation ResolvedProfile, load func(string) (ResolvedProfile, error)) (hardenllm.RecoveryPolicy, hardenllm.ConnectionCatalog, error) {
	var document map[string]json.RawMessage
	if err := decodeStrict(policy, &document); err != nil || document == nil {
		return hardenllm.RecoveryPolicy{}, nil, validationFailure("recoveryPolicy", "Recovery policy must be an object.")
	}
	generation.Connection.ID = "generation"
	connections := hardenllm.ConnectionCatalog{"generation": generation.Connection}
	rewriteTarget := func(data json.RawMessage, id string) (json.RawMessage, error) {
		var target struct {
			Source          string         `json:"source"`
			ProfileID       string         `json:"profileId,omitempty"`
			ModelID         string         `json:"modelId,omitempty"`
			ReasoningEffort string         `json:"reasoningEffort,omitempty"`
			ProviderOptions map[string]any `json:"providerOptions,omitempty"`
			Upstream        string         `json:"upstream,omitempty"`
		}
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
	rewritePlan := func(data json.RawMessage, prefix string) (json.RawMessage, error) {
		if len(data) == 0 || strings.TrimSpace(string(data)) == "null" {
			return data, nil
		}
		var plan map[string]json.RawMessage
		if err := decodeStrict(data, &plan); err != nil || plan == nil {
			return nil, validationFailure("jsonRepair", "Repair plan must be an object.")
		}
		for _, leaf := range []string{"initial", "escalation"} {
			value, ok := plan[leaf]
			if !ok || strings.TrimSpace(string(value)) == "null" {
				continue
			}
			updated, err := rewriteTarget(value, prefix+leaf)
			if err != nil {
				return nil, err
			}
			plan[leaf] = updated
		}
		return json.Marshal(plan)
	}
	var err error
	if data, ok := document["jsonRepair"]; ok {
		document["jsonRepair"], err = rewritePlan(data, "repair-")
		if err != nil {
			return hardenllm.RecoveryPolicy{}, nil, err
		}
	}
	if data, ok := document["rerun"]; ok && strings.TrimSpace(string(data)) != "null" {
		var rerun map[string]json.RawMessage
		if err = decodeStrict(data, &rerun); err != nil || rerun == nil {
			return hardenllm.RecoveryPolicy{}, nil, validationFailure("rerun", "Rerun must be an object or null.")
		}
		if target, ok := rerun["target"]; ok {
			rerun["target"], err = rewriteTarget(target, "rerun")
			if err != nil {
				return hardenllm.RecoveryPolicy{}, nil, err
			}
		}
		if plan, ok := rerun["jsonRepair"]; ok {
			rerun["jsonRepair"], err = rewritePlan(plan, "rerun-repair-")
			if err != nil {
				return hardenllm.RecoveryPolicy{}, nil, err
			}
		}
		document["rerun"], err = json.Marshal(rerun)
		if err != nil {
			return hardenllm.RecoveryPolicy{}, nil, err
		}
	}
	encoded, err := json.Marshal(document)
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
