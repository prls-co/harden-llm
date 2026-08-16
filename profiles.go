package hardenllm

import "context"

// Profile contains one redacted provider/model execution configuration.
type Profile struct {
	SchemaVersion                      int                       `json:"schemaVersion"`
	LLMProfile                         string                    `json:"llmProfile"`
	Provider                           string                    `json:"provider"`
	APIInferenceType                   string                    `json:"apiInferenceType"`
	EndpointCredentialScope            string                    `json:"endpointCredentialScope"`
	BaseURL                            string                    `json:"baseUrl"`
	ModelID                            string                    `json:"modelId"`
	Pricing                            *Pricing                  `json:"pricing"`
	SupportsTemperature                bool                      `json:"supportsTemperature"`
	SupportsContractedStructuredOutput bool                      `json:"supportsContractedStructuredOutput"`
	TokensParam                        string                    `json:"tokensParam"`
	ResponsesTokensParam               string                    `json:"responsesTokensParam"`
	DefaultOptions                     map[string]any            `json:"defaultOptions"`
	ReasoningEffortMap                 map[string]map[string]any `json:"reasoningEffortMap,omitempty"`
	BackupProfiles                     []string                  `json:"backupProfiles,omitempty"`
	Models                             []Model                   `json:"models,omitempty"`
}

// ProfileCatalog indexes profiles by their LLMProfile natural key.
type ProfileCatalog map[string]Profile

// Model is a provider-discovered model safe for client display.
type Model struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Pricing stores per-token rates captured with a profile.
type Pricing struct {
	Input         *float64 `json:"input,omitempty"`
	CacheRead     *float64 `json:"cacheRead,omitempty"`
	CacheCreation *float64 `json:"cacheCreation,omitempty"`
	Output        *float64 `json:"output,omitempty"`
	Reasoning     *float64 `json:"reasoning,omitempty"`
}

// CredentialRequest identifies an origin-bound endpoint credential.
type CredentialRequest struct {
	Scope            string
	OwnerID          string
	BaseURL          string
	APIInferenceType string
}

// Credential is returned only inside the trusted server process.
type Credential struct {
	APIKey  string
	Headers map[string]string
}

// CredentialResolver resolves credentials for one normalized endpoint origin.
type CredentialResolver interface {
	ResolveCredential(ctx context.Context, request CredentialRequest) (Credential, error)
}
