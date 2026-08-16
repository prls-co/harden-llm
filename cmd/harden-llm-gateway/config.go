package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	listenAddressEnvironment         = "HARDEN_LLM_LISTEN_ADDRESS"
	encryptionKeysEnvironment        = "HARDEN_LLM_ENCRYPTION_KEYS"
	activeEncryptionKeyEnvironment   = "HARDEN_LLM_ACTIVE_ENCRYPTION_KEY_ID"
	artifactEndpointEnvironment      = "HARDEN_LLM_ARTIFACT_ENDPOINT"
	artifactExternalEnvironment      = "HARDEN_LLM_ARTIFACT_EXTERNAL_ENDPOINT"
	artifactBucketEnvironment        = "HARDEN_LLM_ARTIFACT_BUCKET"
	artifactAccessKeyEnvironment     = "HARDEN_LLM_ARTIFACT_ACCESS_KEY_ID"
	artifactSecretKeyEnvironment     = "HARDEN_LLM_ARTIFACT_SECRET_ACCESS_KEY"
	artifactPresignTTLEnvironment    = "HARDEN_LLM_ARTIFACT_PRESIGN_TTL"
	sessionTTLEnvironment            = "HARDEN_LLM_SESSION_TTL"
	staticTokenEnvironment           = "HARDEN_LLM_STATIC_TOKEN"
	staticTokenOwnerEnvironment      = "HARDEN_LLM_STATIC_TOKEN_OWNER_ID"
	maxRunDurationEnvironment        = "HARDEN_LLM_MAX_RUN_DURATION_MS"
	allowedHostsEnvironment          = "HARDEN_LLM_PROVIDER_ALLOWED_HOSTS"
	privateAllowlistEnvironment      = "HARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST"
	environmentEnvironment           = "HARDEN_LLM_ENVIRONMENT"
	releaseEnvironment               = "HARDEN_LLM_RELEASE"
	otelEndpointEnvironment          = "HARDEN_LLM_OTEL_EXPORTER_OTLP_ENDPOINT"
	serviceNameEnvironment           = "HARDEN_LLM_SERVICE_NAME"
	defaultListenAddress             = ":8080"
	defaultServiceName               = "harden-llm-gateway"
	defaultSessionTTL                = 24 * time.Hour
	defaultArtifactPresignTTL        = time.Minute
	defaultMaximumRunDuration        = 60 * time.Second
	maximumConfigurationDocumentSize = 64 << 10
)

type serverConfig struct {
	listenAddress       string
	databaseURL         string
	encryptionKeys      map[string][]byte
	activeEncryptionKey string
	artifactEndpoint    string
	artifactExternal    string
	artifactBucket      string
	artifactAccessKey   string
	artifactSecretKey   string
	artifactPresignTTL  time.Duration
	sessionTTL          time.Duration
	staticToken         string
	staticTokenOwnerID  string
	maxRunDuration      time.Duration
	allowedHosts        []string
	privateAllowedHosts []string
	privateAllowlist    []netip.Prefix
	environment         string
	release             string
	otelEndpoint        string
	serviceName         string
}

func loadServerConfig(getenv func(string) string) (serverConfig, error) {
	if getenv == nil {
		return serverConfig{}, errors.New("configuration: environment reader is required")
	}
	config := serverConfig{
		listenAddress:       strings.TrimSpace(getenv(listenAddressEnvironment)),
		databaseURL:         requiredEnvironment(getenv, databaseURLEnvironment),
		activeEncryptionKey: requiredEnvironment(getenv, activeEncryptionKeyEnvironment),
		artifactEndpoint:    requiredEnvironment(getenv, artifactEndpointEnvironment),
		artifactExternal:    requiredEnvironment(getenv, artifactExternalEnvironment),
		artifactBucket:      requiredEnvironment(getenv, artifactBucketEnvironment),
		artifactAccessKey:   requiredEnvironment(getenv, artifactAccessKeyEnvironment),
		artifactSecretKey:   requiredEnvironment(getenv, artifactSecretKeyEnvironment),
		environment:         requiredEnvironment(getenv, environmentEnvironment),
		release:             strings.TrimSpace(getenv(releaseEnvironment)),
		otelEndpoint:        strings.TrimSpace(getenv(otelEndpointEnvironment)),
		serviceName:         strings.TrimSpace(getenv(serviceNameEnvironment)),
		staticToken:         strings.TrimSpace(getenv(staticTokenEnvironment)),
		staticTokenOwnerID:  strings.TrimSpace(getenv(staticTokenOwnerEnvironment)),
	}
	if config.listenAddress == "" {
		config.listenAddress = defaultListenAddress
	}
	if config.serviceName == "" {
		config.serviceName = defaultServiceName
	}
	if err := validateListenAddress(config.listenAddress); err != nil {
		return serverConfig{}, err
	}
	for _, item := range []struct{ name, value string }{
		{databaseURLEnvironment, config.databaseURL}, {activeEncryptionKeyEnvironment, config.activeEncryptionKey},
		{artifactEndpointEnvironment, config.artifactEndpoint}, {artifactExternalEnvironment, config.artifactExternal},
		{artifactBucketEnvironment, config.artifactBucket}, {artifactAccessKeyEnvironment, config.artifactAccessKey},
		{artifactSecretKeyEnvironment, config.artifactSecretKey}, {environmentEnvironment, config.environment},
	} {
		if item.value == "" {
			return serverConfig{}, fmt.Errorf("configuration: %s is required", item.name)
		}
	}
	keys, err := parseEncryptionKeys(getenv(encryptionKeysEnvironment))
	if err != nil {
		return serverConfig{}, err
	}
	config.encryptionKeys = keys
	if _, ok := keys[config.activeEncryptionKey]; !ok {
		return serverConfig{}, fmt.Errorf("configuration: %s is not present in %s", activeEncryptionKeyEnvironment, encryptionKeysEnvironment)
	}
	config.sessionTTL, err = parseDurationEnvironment(getenv(sessionTTLEnvironment), sessionTTLEnvironment, defaultSessionTTL)
	if err != nil {
		return serverConfig{}, err
	}
	if config.sessionTTL < time.Minute || config.sessionTTL > 30*24*time.Hour {
		return serverConfig{}, fmt.Errorf("configuration: %s is outside the supported range", sessionTTLEnvironment)
	}
	if (config.staticToken == "") != (config.staticTokenOwnerID == "") {
		return serverConfig{}, fmt.Errorf("configuration: %s and %s are required together", staticTokenEnvironment, staticTokenOwnerEnvironment)
	}
	if config.staticToken != "" && !validStaticTokenConfiguration(config.staticToken) {
		return serverConfig{}, fmt.Errorf("configuration: %s must contain 32 to 512 printable non-whitespace bytes", staticTokenEnvironment)
	}
	if config.staticTokenOwnerID != "" && !validOwnerIDConfiguration(config.staticTokenOwnerID) {
		return serverConfig{}, fmt.Errorf("configuration: %s is invalid", staticTokenOwnerEnvironment)
	}
	config.artifactPresignTTL, err = parseDurationEnvironment(getenv(artifactPresignTTLEnvironment), artifactPresignTTLEnvironment, defaultArtifactPresignTTL)
	if err != nil {
		return serverConfig{}, err
	}
	if config.artifactPresignTTL < time.Second || config.artifactPresignTTL > 5*time.Minute {
		return serverConfig{}, fmt.Errorf("configuration: %s is outside the supported range", artifactPresignTTLEnvironment)
	}
	config.maxRunDuration, err = parseRunDuration(getenv(maxRunDurationEnvironment))
	if err != nil {
		return serverConfig{}, err
	}
	config.allowedHosts, err = parseCSVValues(getenv(allowedHostsEnvironment), allowedHostsEnvironment)
	if err != nil {
		return serverConfig{}, err
	}
	config.privateAllowedHosts, config.privateAllowlist, err = parsePrivateAllowlist(getenv(privateAllowlistEnvironment))
	if err != nil {
		return serverConfig{}, err
	}
	if err := validateRuntimeIdentity(config); err != nil {
		return serverConfig{}, err
	}
	if config.otelEndpoint != "" {
		parsed, parseErr := url.Parse(config.otelEndpoint)
		if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return serverConfig{}, fmt.Errorf("configuration: %s must be an HTTP or HTTPS origin", otelEndpointEnvironment)
		}
	}
	if isProduction(config.environment) {
		if config.otelEndpoint == "" {
			return serverConfig{}, fmt.Errorf("configuration: %s is required outside development and test", otelEndpointEnvironment)
		}
		if err := validateProductionSecrets(config); err != nil {
			return serverConfig{}, err
		}
	}
	return config, nil
}

func requiredEnvironment(getenv func(string) string, name string) string {
	return strings.TrimSpace(getenv(name))
}

func validateListenAddress(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("configuration: %s is invalid", listenAddressEnvironment)
	}
	if host != "" && !validListenHost(host) {
		return fmt.Errorf("configuration: %s is invalid", listenAddressEnvironment)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("configuration: %s is invalid", listenAddressEnvironment)
	}
	return nil
}

func validListenHost(host string) bool {
	if address, err := netip.ParseAddr(host); err == nil {
		return address.IsValid()
	}
	if len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func parseEncryptionKeys(value string) (map[string][]byte, error) {
	if len(value) == 0 || len(value) > maximumConfigurationDocumentSize {
		return nil, fmt.Errorf("configuration: %s is required and must be bounded JSON", encryptionKeysEnvironment)
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	var encoded map[string]string
	if err := decoder.Decode(&encoded); err != nil || encoded == nil || len(encoded) == 0 || len(encoded) > 32 {
		return nil, fmt.Errorf("configuration: %s must be a non-empty JSON object", encryptionKeysEnvironment)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("configuration: %s must contain one JSON object", encryptionKeysEnvironment)
	}
	keys := make(map[string][]byte, len(encoded))
	for keyID, material := range encoded {
		if !validRuntimeLabel(keyID, 128) || strings.TrimSpace(keyID) != keyID {
			return nil, fmt.Errorf("configuration: %s contains an invalid key ID", encryptionKeysEnvironment)
		}
		decoded, err := base64.RawURLEncoding.Strict().DecodeString(material)
		if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != material {
			return nil, fmt.Errorf("configuration: %s contains an invalid key", encryptionKeysEnvironment)
		}
		keys[keyID] = decoded
	}
	return keys, nil
}

func parseDurationEnvironment(value, name string, fallback time.Duration) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("configuration: %s is invalid", name)
	}
	return duration, nil
}

func parseRunDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultMaximumRunDuration, nil
	}
	milliseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || milliseconds < 1 || milliseconds > 60000 {
		return 0, fmt.Errorf("configuration: %s must be from 1 through 60000", maxRunDurationEnvironment)
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func parseCSVValues(value, name string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" || len(part) > 253 {
			return nil, fmt.Errorf("configuration: %s contains an invalid value", name)
		}
		lower := strings.ToLower(part)
		if _, duplicate := seen[lower]; duplicate {
			continue
		}
		seen[lower] = struct{}{}
		result = append(result, part)
	}
	return result, nil
}

func parsePrivateAllowlist(value string) ([]string, []netip.Prefix, error) {
	values, err := parseCSVValues(value, privateAllowlistEnvironment)
	if err != nil {
		return nil, nil, err
	}
	var hosts []string
	var prefixes []netip.Prefix
	for _, item := range values {
		if strings.Contains(item, "/") {
			prefix, parseErr := netip.ParsePrefix(item)
			if parseErr != nil {
				return nil, nil, fmt.Errorf("configuration: %s contains an invalid CIDR", privateAllowlistEnvironment)
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		hosts = append(hosts, item)
	}
	return hosts, prefixes, nil
}

func isProduction(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "development", "dev", "test", "local":
		return false
	default:
		return true
	}
}

func validateRuntimeIdentity(config serverConfig) error {
	if !validRuntimeLabel(config.environment, 64) {
		return fmt.Errorf("configuration: %s is invalid", environmentEnvironment)
	}
	if config.release != "" && !validRuntimeLabel(config.release, 128) {
		return fmt.Errorf("configuration: %s is invalid", releaseEnvironment)
	}
	if !validRuntimeLabel(config.serviceName, 128) {
		return fmt.Errorf("configuration: %s is invalid", serviceNameEnvironment)
	}
	if isProduction(config.environment) && config.release == "" {
		return fmt.Errorf("configuration: %s is required outside development and test", releaseEnvironment)
	}
	return nil
}

func validRuntimeLabel(value string, maximumBytes int) bool {
	return value != "" && len(value) <= maximumBytes && utf8.ValidString(value) &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func validateProductionSecrets(config serverConfig) error {
	if insecureSecret(config.artifactAccessKey) || insecureSecret(config.artifactSecretKey) || allZero(config.artifactAccessKey) || allZero(config.artifactSecretKey) {
		return errors.New("configuration: documented default artifact credentials are forbidden in production")
	}
	if config.staticToken != "" && insecureSecret(config.staticToken) {
		return errors.New("configuration: documented default static token is forbidden in production")
	}
	for _, key := range config.encryptionKeys {
		if allZeroBytes(key) {
			return errors.New("configuration: zero encryption keys are forbidden in production")
		}
	}
	parsedDatabase, err := url.Parse(config.databaseURL)
	if err != nil || parsedDatabase.Scheme == "" || parsedDatabase.Host == "" {
		return fmt.Errorf("configuration: %s is invalid", databaseURLEnvironment)
	}
	if password, ok := parsedDatabase.User.Password(); ok && insecureSecret(password) {
		return errors.New("configuration: documented default database credentials are forbidden in production")
	}
	parsedExternal, err := url.Parse(config.artifactExternal)
	if err != nil || parsedExternal.Scheme != "https" || parsedExternal.Host == "" {
		return fmt.Errorf("configuration: %s must use HTTPS in production", artifactExternalEnvironment)
	}
	return nil
}

func insecureSecret(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, forbidden := range []string{"change-me", "changeme", "example", "example-secret", "insecure", "password", "secret", "test"} {
		if value == forbidden {
			return true
		}
	}
	return value == ""
}

func allZero(value string) bool {
	return value != "" && strings.Trim(value, "0") == ""
}

func allZeroBytes(value []byte) bool {
	return len(value) > 0 && bytes.Equal(value, make([]byte, len(value)))
}

func validStaticTokenConfiguration(value string) bool {
	if len(value) < 32 || len(value) > 512 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character == 0x7f || character > 0x7e {
			return false
		}
	}
	return true
}

func validOwnerIDConfiguration(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' || (index > 0 && character == '.') {
			continue
		}
		return false
	}
	return true
}
