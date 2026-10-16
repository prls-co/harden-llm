package httpclient

import (
	"encoding/json"
	"errors"
	"strings"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/api"
)

// ValidateEvidence checks a persisted compact projection without pretending it
// proves a remote operation exists. Product stage/member identities stay owned
// by their product. A cache-producing recovery branch can follow earlier work.
func ValidateEvidence(evidence RunEvidence, operationID string, mode hardenllm.CacheMode) error {
	if evidence.OperationID != operationID || strings.TrimSpace(operationID) == "" || strings.TrimSpace(evidence.TraceID) == "" ||
		evidence.Cache.Mode != mode {
		return errors.New("harden execution evidence is incomplete or inconsistent")
	}
	return validateExecutionFacts(evidence)
}

func validateExecutionFacts(evidence RunEvidence) error {
	if evidence.ProviderAttemptCount < 0 || evidence.ProviderAttemptCount > 10 || evidence.ProviderInvoked != (evidence.ProviderAttemptCount > 0) {
		return errors.New("harden dispatch evidence is contradictory")
	}
	data, err := json.Marshal(evidence.Cache)
	if err != nil || api.Validate("ReferenceCacheResult", data) != nil {
		return errors.New("harden cache evidence is invalid")
	}
	if (evidence.Cache.Status == "hit") != evidence.Cache.Served || (evidence.Cache.Served && evidence.Cache.Written) ||
		(evidence.Cache.Mode == hardenllm.CacheModeOff && (evidence.Cache.Served || evidence.Cache.Written)) || (evidence.Cache.Mode == hardenllm.CacheModeRefresh && evidence.Cache.Served) {
		return errors.New("harden cache evidence is contradictory")
	}
	return nil
}

func projectEvidence(result RecordedRunOutput, input RunInput) (*RunEvidence, error) {
	if result.Origin != input.Origin {
		return nil, &ProtocolError{}
	}
	if result.SelectedTarget == nil {
		// Validation may fail before a target is resolved. This is a known
		// failure, not an unknown inference or a fictitious product projection.
		if result.Status == "succeeded" || len(result.Attempts) != 0 || result.ProviderInvoked || result.ResultSource.Kind != hardenllm.ResultSourceNone {
			return nil, &ProtocolError{}
		}
	} else if result.SelectedTarget.ProfileID != input.ProfileID || (input.ModelID != "" && result.SelectedTarget.ModelID != input.ModelID) {
		return nil, &ProtocolError{}
	}
	count := 0
	seen := map[int]bool{}
	for _, attempt := range result.Attempts {
		if seen[attempt.Number] || attempt.ProfileID != attempt.Target.ProfileID {
			return nil, &ProtocolError{}
		}
		seen[attempt.Number] = true
		if attempt.ProviderUsed {
			count++
		}
	}
	if result.ResultSource.Kind == hardenllm.ResultSourceProvider {
		var produced *Attempt
		for i := range result.Attempts {
			if result.Attempts[i].Number == result.ResultSource.AttemptNumber {
				produced = &result.Attempts[i]
			}
		}
		if produced == nil || !produced.ProviderUsed || result.ResultSource.Producer == nil || produced.Target != *result.ResultSource.Producer {
			return nil, &ProtocolError{}
		}
	}
	if result.Status == "succeeded" && (result.ResultSource.Kind == hardenllm.ResultSourceNone || result.ResultSource.Producer == nil) {
		return nil, &ProtocolError{}
	}
	if (result.ResultSource.Kind == hardenllm.ResultSourceCache && !result.Cache.Served) || (result.ResultSource.Kind == hardenllm.ResultSourceProvider && result.Cache.Served) {
		return nil, &ProtocolError{}
	}
	mode := input.CacheMode
	if mode == "" {
		mode = hardenllm.CacheModeOff
	}
	evidence := &RunEvidence{OperationID: input.Origin.OperationID, TraceID: result.TraceID, Cache: result.Cache, ProviderInvoked: result.ProviderInvoked, ProviderAttemptCount: count}
	if result.Cache.Mode != mode || validateExecutionFacts(*evidence) != nil {
		return nil, &ProtocolError{}
	}
	if result.SelectedTarget == nil || strings.TrimSpace(input.Origin.OperationID) == "" {
		return nil, nil
	}
	if err := ValidateEvidence(*evidence, input.Origin.OperationID, mode); err != nil {
		return nil, &ProtocolError{}
	}
	return evidence, nil
}
