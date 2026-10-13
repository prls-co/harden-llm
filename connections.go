package hardenllm

import (
	"errors"
	"fmt"
	"math"
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
	Pricing           *Pricing          `json:"pricing,omitempty"`
}

// ConnectionCatalog is the immutable startup set indexed by connection ID.
// Clone returns detached connection metadata and credentials for a call composition.
func (connection Connection) Clone() Connection {
	connection.Headers = cloneStringMap(connection.Headers)
	connection.Pricing = clonePricing(connection.Pricing)
	return connection
}

type ConnectionCatalog map[string]Connection

func normalizeConnections(input ConnectionCatalog, defaultID string) (ConnectionCatalog, string, error) {
	if len(input) == 0 {
		return nil, "", errors.New("hardenllm: at least one upstream connection is required")
	}
	connections := make(ConnectionCatalog, len(input))
	for id, connection := range input {
		if id != strings.TrimSpace(connection.ID) {
			return nil, "", errors.New("hardenllm: catalog key must equal its connection ID")
		}
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
		connection = connection.Clone()
		if connection.Pricing != nil {
			for _, rate := range []*float64{connection.Pricing.Input, connection.Pricing.CacheRead, connection.Pricing.CacheCreation, connection.Pricing.Output, connection.Pricing.Reasoning} {
				if rate != nil && (math.IsNaN(*rate) || math.IsInf(*rate, 0) || *rate < 0) {
					return nil, "", fmt.Errorf("hardenllm: connection %q pricing is invalid", connection.ID)
				}
			}
		}
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

func clonePricing(input *Pricing) *Pricing {
	if input == nil {
		return nil
	}
	copy := *input
	for _, rate := range []**float64{&copy.Input, &copy.CacheRead, &copy.CacheCreation, &copy.Output, &copy.Reasoning} {
		if *rate != nil {
			value := **rate
			*rate = &value
		}
	}
	return &copy
}
