package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
)

const (
	apiTokenEnvironment       = "HARDEN_LLM_TOKEN"
	connectionFileEnvironment = "HARDEN_LLM_CONFIG_FILE"
	listenAddressEnvironment  = "HARDEN_LLM_LISTEN_ADDRESS"
	jinaAPIKeyEnvironment     = "JINA_API_KEY"
	allowedHostsEnvironment   = "HARDEN_LLM_PROVIDER_ALLOWED_HOSTS"
	privateHostsEnvironment   = "HARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST"
	environmentEnvironment    = "HARDEN_LLM_ENVIRONMENT"
	releaseEnvironment        = "HARDEN_LLM_RELEASE"
	otelEndpointEnvironment   = "HARDEN_LLM_OTEL_EXPORTER_OTLP_ENDPOINT"
	serviceNameEnvironment    = "HARDEN_LLM_SERVICE_NAME"
	defaultListenAddress      = ":8080"
	defaultServiceName        = "harden-llm-gateway"
	maximumConfigurationSize  = 64 << 10
)

type serverConfig struct {
	listenAddress       string
	token               string
	connections         []hardenllm.Connection
	defaultConnection   string
	jinaAPIKey          string
	maxRunDuration      time.Duration
	allowedHosts        []string
	privateAllowedHosts []string
	privateAllowlist    []netip.Prefix
	environment         string
	release             string
	otelEndpoint        string
	serviceName         string
}

type connectionFile struct {
	DefaultUpstream string               `json:"default_upstream"`
	Upstreams       []configuredUpstream `json:"upstreams"`
}

type configuredUpstream struct {
	ID                string `json:"id"`
	Provider          string `json:"provider"`
	Protocol          string `json:"protocol"`
	BaseURL           string `json:"base_url"`
	APIKeyEnv         string `json:"api_key_env"`
	CacheDomain       string `json:"cache_domain"`
	SupportsWebSearch bool   `json:"supports_web_search,omitempty"`
}

func loadServerConfig(getenv func(string) string) (serverConfig, error) {
	if getenv == nil {
		return serverConfig{}, errors.New("configuration: environment reader is required")
	}
	config := serverConfig{
		listenAddress:  strings.TrimSpace(getenv(listenAddressEnvironment)),
		token:          strings.TrimSpace(getenv(apiTokenEnvironment)),
		jinaAPIKey:     strings.TrimSpace(getenv(jinaAPIKeyEnvironment)),
		environment:    strings.TrimSpace(getenv(environmentEnvironment)),
		release:        strings.TrimSpace(getenv(releaseEnvironment)),
		otelEndpoint:   strings.TrimSpace(getenv(otelEndpointEnvironment)),
		serviceName:    strings.TrimSpace(getenv(serviceNameEnvironment)),
		maxRunDuration: httpapi.MaximumRunDuration,
	}
	if config.listenAddress == "" {
		config.listenAddress = defaultListenAddress
	}
	if config.environment == "" {
		config.environment = "development"
	}
	if config.serviceName == "" {
		config.serviceName = defaultServiceName
	}
	if !auth.ValidToken(config.token) {
		return serverConfig{}, fmt.Errorf("configuration: %s is required and must be a bounded printable token", apiTokenEnvironment)
	}
	if err := validateListenAddress(config.listenAddress); err != nil {
		return serverConfig{}, err
	}
	if !validConfigLabel(config.environment, 64) || !validConfigLabel(config.serviceName, 128) || (config.release != "" && !validConfigLabel(config.release, 128)) {
		return serverConfig{}, errors.New("configuration: runtime identity is invalid")
	}
	if config.environment != "development" && config.environment != "dev" && config.environment != "test" && config.release == "" {
		return serverConfig{}, fmt.Errorf("configuration: %s is required outside development and test", releaseEnvironment)
	}
	if config.otelEndpoint != "" {
		parsed, err := url.Parse(config.otelEndpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return serverConfig{}, fmt.Errorf("configuration: %s must be an HTTP or HTTPS origin", otelEndpointEnvironment)
		}
	}
	var err error
	config.allowedHosts, err = parseCSVValues(getenv(allowedHostsEnvironment), allowedHostsEnvironment)
	if err != nil {
		return serverConfig{}, err
	}
	config.privateAllowedHosts, config.privateAllowlist, err = parsePrivateAllowlist(getenv(privateHostsEnvironment))
	if err != nil {
		return serverConfig{}, err
	}
	config.maxRunDuration, err = parseRunDuration(getenv("HARDEN_LLM_MAX_RUN_DURATION_MS"))
	if err != nil {
		return serverConfig{}, err
	}
	path := strings.TrimSpace(getenv(connectionFileEnvironment))
	if path == "" {
		return serverConfig{}, fmt.Errorf("configuration: %s is required", connectionFileEnvironment)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return serverConfig{}, fmt.Errorf("configuration: read %s", connectionFileEnvironment)
	}
	if len(data) == 0 || len(data) > maximumConfigurationSize {
		return serverConfig{}, fmt.Errorf("configuration: %s must be a non-empty bounded JSON file", connectionFileEnvironment)
	}
	connections, defaultConnection, err := parseConnectionFile(data, getenv)
	if err != nil {
		return serverConfig{}, err
	}
	config.connections, config.defaultConnection = connections, defaultConnection
	return config, nil
}

func parseConnectionFile(data []byte, getenv func(string) string) ([]hardenllm.Connection, string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document connectionFile
	if err := decoder.Decode(&document); err != nil {
		return nil, "", errors.New("configuration: upstream file must contain the documented JSON shape")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, "", errors.New("configuration: upstream file must contain exactly one JSON value")
	}
	if len(document.Upstreams) == 0 || len(document.Upstreams) > 32 {
		return nil, "", errors.New("configuration: upstream file requires from 1 through 32 connections")
	}
	connections := make([]hardenllm.Connection, 0, len(document.Upstreams))
	seen := make(map[string]struct{}, len(document.Upstreams))
	for _, upstream := range document.Upstreams {
		if !validConfigLabel(upstream.APIKeyEnv, 128) || !validEnvironmentName(upstream.APIKeyEnv) {
			return nil, "", errors.New("configuration: upstream API key environment reference is invalid")
		}
		apiKey := strings.TrimSpace(getenv(upstream.APIKeyEnv))
		if apiKey == "" {
			return nil, "", fmt.Errorf("configuration: required upstream credential %s is empty", upstream.APIKeyEnv)
		}
		if _, duplicate := seen[upstream.ID]; duplicate {
			return nil, "", errors.New("configuration: upstream IDs must be unique")
		}
		seen[upstream.ID] = struct{}{}
		parsedBase, _ := url.Parse(upstream.BaseURL)
		if parsedBase != nil && parsedBase.Scheme != "https" {
			return nil, "", errors.New("configuration: upstream endpoints must use HTTPS")
		}
		connections = append(connections, hardenllm.Connection{
			ID: upstream.ID, Provider: upstream.Provider, Protocol: upstream.Protocol,
			BaseURL: upstream.BaseURL, CacheDomain: upstream.CacheDomain, APIKey: apiKey,
			SupportsWebSearch: upstream.SupportsWebSearch,
		})
	}
	if strings.TrimSpace(document.DefaultUpstream) == "" {
		return nil, "", errors.New("configuration: default_upstream is required")
	}
	return connections, document.DefaultUpstream, nil
}

func validEnvironmentName(value string) bool {
	if value == "" || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	for _, char := range value {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func validateListenAddress(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("configuration: %s is invalid", listenAddressEnvironment)
	}
	if host != "" {
		if address, err := netip.ParseAddr(host); err != nil || !address.IsValid() {
			return fmt.Errorf("configuration: %s must use an IP address or an empty host", listenAddressEnvironment)
		}
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("configuration: %s is invalid", listenAddressEnvironment)
	}
	return nil
}

func parseRunDuration(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return httpapi.MaximumRunDuration, nil
	}
	milliseconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || milliseconds < 1 || milliseconds > int64(httpapi.MaximumRunDuration/time.Millisecond) {
		return 0, errors.New("configuration: HARDEN_LLM_MAX_RUN_DURATION_MS must be from 1 through 60000")
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func parseCSVValues(value, name string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var result []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" || len(part) > 253 {
			return nil, fmt.Errorf("configuration: %s contains an invalid value", name)
		}
		key := strings.ToLower(part)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, part)
	}
	return result, nil
}

func parsePrivateAllowlist(value string) ([]string, []netip.Prefix, error) {
	values, err := parseCSVValues(value, privateHostsEnvironment)
	if err != nil {
		return nil, nil, err
	}
	var hosts []string
	var prefixes []netip.Prefix
	for _, item := range values {
		if strings.Contains(item, "/") {
			prefix, parseErr := netip.ParsePrefix(item)
			if parseErr != nil {
				return nil, nil, fmt.Errorf("configuration: %s contains an invalid CIDR", privateHostsEnvironment)
			}
			prefixes = append(prefixes, prefix.Masked())
		} else {
			hosts = append(hosts, item)
		}
	}
	return hosts, prefixes, nil
}

func validConfigLabel(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}
