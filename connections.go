package hardenllm

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Connection is one operator-configured upstream endpoint. Model IDs belong to
// requests so a connection does not encode a model preset.
type Connection struct {
	ID                string            `json:"id"`
	Provider          string            `json:"provider"`
	Protocol          string            `json:"protocol"`
	BaseURL           string            `json:"baseUrl"`
	CacheDomain       string            `json:"cacheDomain,omitempty"`
	APIKey            string            `json:"-"`
	Headers           map[string]string `json:"-"`
	SupportsWebSearch bool              `json:"supportsWebSearch,omitempty"`
}

// ConnectionCatalog is the immutable startup set indexed by connection ID.
type ConnectionCatalog map[string]Connection

func normalizeConnections(input []Connection, defaultID string) (ConnectionCatalog, string, error) {
	if len(input) == 0 {
		return nil, "", errors.New("hardenllm: at least one upstream connection is required")
	}
	connections := make(ConnectionCatalog, len(input))
	for _, connection := range input {
		connection.ID = strings.TrimSpace(connection.ID)
		connection.Provider = strings.TrimSpace(connection.Provider)
		connection.Protocol = strings.TrimSpace(connection.Protocol)
		connection.BaseURL = strings.TrimRight(strings.TrimSpace(connection.BaseURL), "/")
		connection.CacheDomain = strings.TrimSpace(connection.CacheDomain)
		if connection.ID == "" || len(connection.ID) > 128 || !utf8.ValidString(connection.ID) {
			return nil, "", errors.New("hardenllm: connection ID is invalid")
		}
		if connection.Provider == "" || len(connection.Provider) > 128 || !utf8.ValidString(connection.Provider) {
			return nil, "", fmt.Errorf("hardenllm: connection %q provider is invalid", connection.ID)
		}
		if connection.CacheDomain == "" {
			connection.CacheDomain = connection.ID
		}
		if len(connection.CacheDomain) > 128 || !utf8.ValidString(connection.CacheDomain) {
			return nil, "", fmt.Errorf("hardenllm: connection %q cache domain is invalid", connection.ID)
		}
		switch connection.Protocol {
		case "responses", "chat-completions", "gemini-generate-content", "anthropic-messages":
		default:
			return nil, "", fmt.Errorf("hardenllm: connection %q protocol is unsupported", connection.ID)
		}
		parsed, err := url.Parse(connection.BaseURL)
		if err != nil || parsed == nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, "", fmt.Errorf("hardenllm: connection %q base URL is invalid", connection.ID)
		}
		if _, exists := connections[connection.ID]; exists {
			return nil, "", fmt.Errorf("hardenllm: connection %q is duplicated", connection.ID)
		}
		connection.Headers = cloneStringMap(connection.Headers)
		connections[connection.ID] = connection
	}
	defaultID = strings.TrimSpace(defaultID)
	if defaultID == "" {
		if len(connections) != 1 {
			return nil, "", errors.New("hardenllm: default connection is required when multiple connections are configured")
		}
		for id := range connections {
			defaultID = id
		}
	}
	if _, exists := connections[defaultID]; !exists {
		return nil, "", errors.New("hardenllm: configured default connection was not found")
	}
	return connections, defaultID, nil
}
