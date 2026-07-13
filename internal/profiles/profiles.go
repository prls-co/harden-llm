// Package profiles owns the strict, credential-free profile catalog contract.
package profiles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	SchemaVersion         = 1
	MaxBackupProfileDepth = 5
	MaxProfileNameBytes   = 1500
)

var (
	apiInferenceTypes = map[string]struct{}{
		"chat-completions": {}, "responses": {}, "gemini-generate-content": {}, "anthropic-messages": {},
	}
	credentialScopes    = map[string]struct{}{"global": {}, "user": {}}
	reasoningLevels     = map[string]struct{}{"lowest": {}, "middle": {}, "highest": {}}
	forbiddenDefaultKey = regexp.MustCompile(`(?i)^(api[-_]?key|authorization|credential|encrypted|ciphertext|authTag|secret|pricing|price|cost|metadata|model|models|lastModelRefreshAt|endpointCredentialStatus|ui|uiState)$`)
	reservedProfileName = regexp.MustCompile(`^__.*__$`)
)

type Catalog map[string]Profile

type Profile struct {
	SchemaVersion                      int                       `json:"schemaVersion"`
	LLMProfile                         string                    `json:"llmProfile"`
	Provider                           string                    `json:"provider"`
	APIInferenceType                   string                    `json:"apiInferenceType"`
	EndpointCredentialScope            string                    `json:"endpointCredentialScope"`
	BaseURL                            string                    `json:"baseUrl"`
	ModelID                            string                    `json:"modelId"`
	Pricing                            *Pricing                  `json:"pricing"`
	SupportsTemperature                *bool                     `json:"supportsTemperature"`
	SupportsContractedStructuredOutput bool                      `json:"supportsContractedStructuredOutput"`
	TokensParam                        *string                   `json:"tokensParam"`
	ResponsesTokensParam               *string                   `json:"responsesTokensParam"`
	DefaultOptions                     map[string]any            `json:"defaultOptions"`
	ReasoningEffortMap                 map[string]map[string]any `json:"reasoningEffortMap,omitempty"`
	BackupProfiles                     []string                  `json:"backupProfiles"`
	Models                             []Model                   `json:"-"`
}

type Pricing struct {
	Input         *float64 `json:"input_cost_per_token"`
	CacheRead     *float64 `json:"cache_read_input_token_cost"`
	CacheCreation *float64 `json:"cache_creation_input_token_cost"`
	Output        *float64 `json:"output_cost_per_token"`
	Reasoning     *float64 `json:"output_cost_per_reasoning_token"`
}

type Model struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationError struct {
	Code        string       `json:"code"`
	FieldErrors []FieldError `json:"fieldErrors"`
}

func (validationError *ValidationError) Error() string {
	if validationError == nil || len(validationError.FieldErrors) == 0 {
		return "profiles: invalid profile catalog"
	}
	first := validationError.FieldErrors[0]
	return fmt.Sprintf("profiles: invalid profile catalog: %s: %s", first.Field, first.Message)
}

func ParseCatalog(input []byte) (Catalog, error) {
	rawCatalog, err := decodeRawCatalog(input)
	if err != nil {
		return nil, validationFailure("catalog", err.Error())
	}
	catalog := make(Catalog, len(rawCatalog))
	for name, raw := range rawCatalog {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return nil, validationFailure(name, "Catalog entry must be an object.")
		}
		for _, required := range []string{
			"schemaVersion", "llmProfile", "provider", "apiInferenceType", "endpointCredentialScope",
			"baseUrl", "modelId", "pricing", "supportsTemperature", "supportsContractedStructuredOutput",
			"tokensParam", "responsesTokensParam", "defaultOptions",
		} {
			if _, ok := fields[required]; !ok {
				return nil, validationFailure(name+"."+required, "Required profile catalog field is missing.")
			}
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		decoder.UseNumber()
		var profile Profile
		if err := decoder.Decode(&profile); err != nil {
			return nil, validationFailure(name, err.Error())
		}
		if err := requireJSONEOF(decoder); err != nil {
			return nil, validationFailure(name, err.Error())
		}
		profile.LLMProfile = strings.TrimSpace(profile.LLMProfile)
		profile.Provider = strings.TrimSpace(profile.Provider)
		profile.APIInferenceType = strings.TrimSpace(profile.APIInferenceType)
		profile.EndpointCredentialScope = strings.TrimSpace(profile.EndpointCredentialScope)
		profile.BaseURL = strings.TrimRight(strings.TrimSpace(profile.BaseURL), "/")
		profile.ModelID = strings.TrimSpace(profile.ModelID)
		if profile.BackupProfiles == nil {
			profile.BackupProfiles = []string{}
		}
		catalog[name] = profile
	}
	if err := ValidateCatalog(catalog); err != nil {
		return nil, err
	}
	return catalog, nil
}

func MarshalCatalog(catalog Catalog) ([]byte, error) {
	if err := ValidateCatalog(catalog); err != nil {
		return nil, err
	}
	return json.Marshal(catalog)
}

func ValidateCatalog(catalog Catalog) error {
	if catalog == nil {
		return validationFailure("catalog", "Profile catalog must be a JSON object.")
	}
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, key := range names {
		profile := catalog[key]
		if key != profile.LLMProfile {
			return validationFailure(key+".llmProfile", "Catalog key must match llmProfile.")
		}
		if err := validateProfile(profile, key); err != nil {
			return err
		}
	}
	return validateBackupGraph(catalog, names)
}

func validateProfile(profile Profile, prefix string) error {
	if profile.SchemaVersion != SchemaVersion {
		return validationFailure(prefix+".schemaVersion", "schemaVersion must be 1.")
	}
	if err := validateProfileName(profile.LLMProfile); err != nil {
		return validationFailure("llmProfile", err.Error())
	}
	if strings.TrimSpace(profile.Provider) == "" {
		return validationFailure(prefix+".provider", "Provider is required.")
	}
	if _, ok := apiInferenceTypes[profile.APIInferenceType]; !ok {
		return validationFailure(prefix+".apiInferenceType", "Unsupported API inference type.")
	}
	if _, ok := credentialScopes[profile.EndpointCredentialScope]; !ok {
		return validationFailure(prefix+".endpointCredentialScope", "Endpoint credential scope must be global or user.")
	}
	if err := validateBaseURL(profile.BaseURL); err != nil {
		return validationFailure(prefix+".baseUrl", err.Error())
	}
	if strings.TrimSpace(profile.ModelID) == "" {
		return validationFailure(prefix+".modelId", "Model ID is required.")
	}
	if profile.Pricing != nil {
		if err := validatePricing(*profile.Pricing); err != nil {
			return validationFailure(prefix+".pricing", err.Error())
		}
	}
	if profile.DefaultOptions == nil {
		return validationFailure(prefix+".defaultOptions", "defaultOptions must be a JSON object.")
	}
	if _, err := json.Marshal(profile.DefaultOptions); err != nil {
		return validationFailure(prefix+".defaultOptions", "defaultOptions must contain JSON values only.")
	}
	if field := forbiddenOptionField(profile.DefaultOptions, prefix+".defaultOptions"); field != "" {
		return validationFailure(field, "defaultOptions may contain request defaults only.")
	}
	for level, options := range profile.ReasoningEffortMap {
		if _, ok := reasoningLevels[level]; !ok {
			return validationFailure(prefix+".reasoningEffortMap."+level, "reasoningEffortMap may contain only contracted reasoning levels.")
		}
		if options == nil {
			return validationFailure(prefix+".reasoningEffortMap."+level, "reasoning effort options must be a JSON object.")
		}
		if _, err := json.Marshal(options); err != nil {
			return validationFailure(prefix+".reasoningEffortMap."+level, "reasoning effort options must contain JSON values only.")
		}
	}
	return nil
}

func validateProfileName(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("LLM Profile is required")
	}
	if strings.Contains(value, "/") {
		return errors.New(`LLM Profile cannot contain "/"`)
	}
	if value == "." || value == ".." {
		return errors.New("LLM Profile cannot be . or ..")
	}
	if reservedProfileName.MatchString(value) {
		return errors.New("LLM Profile cannot match __.*__")
	}
	if !utf8.ValidString(value) || len([]byte(value)) > MaxProfileNameBytes {
		return errors.New("LLM Profile must be valid UTF-8 and 1,500 UTF-8 bytes or fewer")
	}
	return nil
}

func validateBaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("Base URL must be a valid absolute URL.")
	}
	if strings.ToLower(parsed.Scheme) != "https" {
		return errors.New("Base URL must use HTTPS.")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("Base URL cannot contain userinfo, query, or fragment.")
	}
	return nil
}

func validatePricing(pricing Pricing) error {
	for _, rate := range []*float64{pricing.Input, pricing.CacheRead, pricing.CacheCreation, pricing.Output, pricing.Reasoning} {
		if rate != nil && (math.IsNaN(*rate) || math.IsInf(*rate, 0) || *rate < 0) {
			return errors.New("Pricing rates must be finite and non-negative.")
		}
	}
	return nil
}

func forbiddenOptionField(value any, path string) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if forbiddenDefaultKey.MatchString(key) {
				return path + "." + key
			}
			if nested := forbiddenOptionField(typed[key], path+"."+key); nested != "" {
				return nested
			}
		}
	case []any:
		for index, item := range typed {
			if nested := forbiddenOptionField(item, fmt.Sprintf("%s.%d", path, index)); nested != "" {
				return nested
			}
		}
	}
	return ""
}

func validateBackupGraph(catalog Catalog, names []string) error {
	for _, name := range names {
		profile := catalog[name]
		seen := make(map[string]struct{}, len(profile.BackupProfiles))
		for index, backup := range profile.BackupProfiles {
			field := fmt.Sprintf("%s.backupProfiles[%d]", name, index)
			if strings.TrimSpace(backup) == "" {
				return validationFailure(field, "Backup profile references must not be empty.")
			}
			if backup == name {
				return validationFailure(field, "A profile cannot reference itself as a backup profile.")
			}
			if _, duplicate := seen[backup]; duplicate {
				return validationFailure(field, fmt.Sprintf("Duplicate backup profile %q.", backup))
			}
			seen[backup] = struct{}{}
			if _, found := catalog[backup]; !found {
				return validationFailure(field, fmt.Sprintf("Backup profile %q was not found.", backup))
			}
		}
	}
	for _, root := range names {
		if err := visitBackupGraph(catalog, root, root, 0, map[string]struct{}{}); err != nil {
			return err
		}
	}
	return nil
}

func visitBackupGraph(catalog Catalog, root, current string, depth int, stack map[string]struct{}) error {
	if depth > MaxBackupProfileDepth {
		return validationFailure(root+".backupProfiles", fmt.Sprintf("Backup profile depth cannot exceed %d.", MaxBackupProfileDepth))
	}
	nextStack := make(map[string]struct{}, len(stack)+1)
	for key := range stack {
		nextStack[key] = struct{}{}
	}
	nextStack[current] = struct{}{}
	for index, backup := range catalog[current].BackupProfiles {
		if _, cycle := nextStack[backup]; cycle {
			return validationFailure(fmt.Sprintf("%s.backupProfiles[%d]", current, index), fmt.Sprintf("Backup profile cycle includes %q.", backup))
		}
		if err := visitBackupGraph(catalog, root, backup, depth+1, nextStack); err != nil {
			return err
		}
	}
	return nil
}

func NormalizeModels(input []Model) []Model {
	byID := make(map[string]Model, len(input))
	for _, model := range input {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" {
			continue
		}
		model.Label = strings.TrimSpace(model.Label)
		if model.Label == "" {
			model.Label = model.ID
		}
		byID[model.ID] = model
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	result := make([]Model, 0, len(ids))
	for _, id := range ids {
		result = append(result, byID[id])
	}
	return result
}

func decodeRawCatalog(input []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, errors.New("Profile catalog must be a JSON object.")
	}
	result := make(map[string]json.RawMessage)
	for decoder.More() {
		nameToken, tokenErr := decoder.Token()
		if tokenErr != nil {
			return nil, tokenErr
		}
		name, ok := nameToken.(string)
		if !ok {
			return nil, errors.New("Profile catalog key must be a string.")
		}
		if _, duplicate := result[name]; duplicate {
			return nil, fmt.Errorf("duplicate profile catalog key %q", name)
		}
		var raw json.RawMessage
		if decodeErr := decoder.Decode(&raw); decodeErr != nil {
			return nil, decodeErr
		}
		result[name] = raw
	}
	if _, err = decoder.Token(); err != nil {
		return nil, err
	}
	if err = requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	return result, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("trailing JSON value")
}

func validationFailure(field, message string) *ValidationError {
	return &ValidationError{Code: "invalid_profile_catalog", FieldErrors: []FieldError{{Field: field, Message: message}}}
}
