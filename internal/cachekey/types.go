// Package cachekey owns canonical provider-operation cache identity.
package cachekey

const OperationSchemaVersion = "utility-llm.operation.v1"

// Operation is the provider-prepared semantic request used for cache identity.
type Operation struct {
	SchemaVersion      string             `json:"schemaVersion"`
	Protocol           string             `json:"protocol"`
	Endpoint           Endpoint           `json:"endpoint"`
	Model              string             `json:"model"`
	Payload            any                `json:"payload"`
	SemanticHeaders    map[string]any     `json:"semanticHeaders"`
	ResponseProjection ResponseProjection `json:"responseProjection"`
}

type Endpoint struct {
	Identity string `json:"identity"`
	Method   string `json:"method"`
	Path     string `json:"path"`
}

type ResponseProjection struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Version  string `json:"version"`
}
