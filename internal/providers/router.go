// Package providers implements all built-in provider protocols through one
// endpoint-validated HTTP transport.
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
)

const (
	defaultMaxResponseBytes = 16 << 20
	defaultAnthropicVersion = "2023-06-01"
	defaultTemperature      = 0.3
	rawEnvelopeVersion      = "utility-llm.raw-provider-envelope.v1"
)

var (
	responsesRetryDirectivePattern = regexp.MustCompile(`^An error occurred while processing your request\.\s+You can retry your request\b`)
	responsesRequestIDPattern      = regexp.MustCompile(`\brequest ID\s+([A-Za-z0-9][A-Za-z0-9_-]{7,127})\b`)
)

// Config configures a provider router.
type Config struct {
	EndpointPolicy   EndpointPolicy
	Logger           *slog.Logger
	MaxResponseBytes int64
}

// Router prepares and executes every supported provider protocol.
type Router struct {
	client           *http.Client
	guard            *endpointGuard
	logger           *slog.Logger
	maxResponseBytes int64
}

type preparedRequest struct {
	url       *url.URL
	headers   http.Header
	body      []byte
	provider  string
	protocol  string
	callType  string
	pricing   runtime.Pricing
	timeout   time.Duration
	operation cachekey.Operation
}

// NewRouter creates a router with one hardened, reusable egress client.
func NewRouter(config Config) (*Router, error) {
	client, err := newSafeHTTPClient(config.EndpointPolicy)
	if err != nil {
		return nil, err
	}
	guard, err := newEndpointGuard(config.EndpointPolicy)
	if err != nil {
		return nil, err
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	maximum := config.MaxResponseBytes
	if maximum <= 0 {
		maximum = defaultMaxResponseBytes
	}
	return &Router{client: client, guard: guard, logger: logger, maxResponseBytes: maximum}, nil
}

// Prepare converts a provider-neutral call into one canonical operation and an
// origin-bound request. No credential is placed in the operation descriptor.
func (router *Router) Prepare(ctx context.Context, profile runtime.Profile, credential runtime.Credential, call runtime.Call) (runtime.PreparedOperation, error) {
	if router == nil || router.client == nil || router.guard == nil {
		return runtime.PreparedOperation{}, errors.New("providers: router is not initialized")
	}
	if err := validateProfile(profile, call); err != nil {
		return runtime.PreparedOperation{}, err
	}
	if call.Repair != nil && call.Repair.ModelID != "" {
		profile.ModelID = call.Repair.ModelID
	}
	provider, protocol, path, payload, semanticHeaders, err := buildPayload(profile, call)
	if err != nil {
		return runtime.PreparedOperation{}, err
	}
	identity, requestURL, err := endpointURL(profile.BaseURL, profile.APIInferenceType, path)
	if err != nil {
		return runtime.PreparedOperation{}, err
	}
	if _, err = router.guard.resolve(ctx, requestURL.String()); err != nil {
		return runtime.PreparedOperation{}, err
	}
	headers, err := providerHeaders(profile.APIInferenceType, credential)
	if err != nil {
		return runtime.PreparedOperation{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return runtime.PreparedOperation{}, fmt.Errorf("providers: encode request payload: %w", err)
	}
	timeout, err := requestTimeout(call.ProviderOptions)
	if err != nil {
		return runtime.PreparedOperation{}, err
	}
	operation := cachekey.Operation{
		SchemaVersion: cachekey.OperationSchemaVersion,
		Protocol:      protocol,
		Endpoint: cachekey.Endpoint{
			Identity: identity, Method: http.MethodPost, Path: path,
		},
		Model: profile.ModelID, Payload: payload, SemanticHeaders: semanticHeaders,
		ResponseProjection: cachekey.ResponseProjection{
			Provider: provider, Kind: responseKind(call.CallType), Version: "v1",
		},
	}
	request := preparedRequest{
		url: requestURL, headers: headers, body: body, provider: provider, protocol: protocol,
		callType: call.CallType, pricing: profile.Pricing, timeout: timeout, operation: operation,
	}
	return runtime.PreparedOperation{Operation: operation, Opaque: request}, nil
}

// Execute performs a prepared request and normalizes its result.
func (router *Router) Execute(ctx context.Context, operation runtime.PreparedOperation) (runtime.ProviderResult, error) {
	if router == nil || router.client == nil {
		return runtime.ProviderResult{}, errors.New("providers: router is not initialized")
	}
	prepared, ok := operation.Opaque.(preparedRequest)
	if !ok {
		return runtime.ProviderResult{}, errors.New("providers: invalid prepared operation")
	}
	if prepared.operation.Protocol != operation.Operation.Protocol || prepared.operation.Endpoint.Identity != operation.Operation.Endpoint.Identity {
		return runtime.ProviderResult{}, errors.New("providers: prepared operation identity mismatch")
	}
	requestContext := ctx
	cancel := func() {}
	if prepared.timeout > 0 {
		requestContext, cancel = context.WithTimeout(ctx, prepared.timeout)
	}
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, prepared.url.String(), bytes.NewReader(prepared.body))
	if err != nil {
		return runtime.ProviderResult{}, fmt.Errorf("providers: build request: %w", err)
	}
	request.Header = prepared.headers.Clone()
	response, err := router.client.Do(request)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return runtime.ProviderResult{}, contextErr
		}
		if contextErr := requestContext.Err(); contextErr != nil {
			return runtime.ProviderResult{}, &retry.ProviderError{Err: errors.New("provider request timed out"), Code: "ETIMEDOUT", Timeout: true}
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return runtime.ProviderResult{}, &retry.ProviderError{Err: errors.New("provider network request timed out"), Code: "ETIMEDOUT", Timeout: true}
		}
		return runtime.ProviderResult{}, &retry.ProviderError{Err: errors.New("provider network request failed"), Code: "NETWORK_ERROR"}
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, router.maxResponseBytes)
	if err != nil {
		if prepared.protocol == "openai.responses" {
			if streamErr := normalizeResponsesStreamReadError(err); streamErr != nil {
				return runtime.ProviderResult{}, streamErr
			}
		}
		return runtime.ProviderResult{}, &retry.ProviderError{Err: err, Code: "PROVIDER_RESPONSE_READ"}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return runtime.ProviderResult{}, providerHTTPError(response, body)
	}
	if prepared.protocol == "openai.responses" && isEventStream(response.Header.Get("Content-Type"), body) {
		body, err = collectResponsesEventStream(body)
		if err != nil {
			return runtime.ProviderResult{}, err
		}
	}
	result, err := normalizeResponse(prepared, body)
	if err != nil {
		return result, err
	}
	return result, nil
}

func requestTimeout(options map[string]any) (time.Duration, error) {
	value, ok := options["timeout"]
	if !ok || value == nil {
		return 0, nil
	}
	var milliseconds float64
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, nil
		}
		milliseconds = parsed
	default:
		parsed, numeric := numericValue(typed)
		if !numeric {
			return 0, nil
		}
		milliseconds, _ = parsed.(float64)
	}
	if math.IsNaN(milliseconds) || math.IsInf(milliseconds, 0) || milliseconds <= 0 {
		return 0, nil
	}
	if milliseconds > float64(math.MaxInt64)/float64(time.Millisecond) {
		return 0, errors.New("providers: request timeout exceeds the supported duration")
	}
	return time.Duration(milliseconds * float64(time.Millisecond)), nil
}

func isEventStream(contentType string, body []byte) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream") ||
		bytes.HasPrefix(bytes.TrimSpace(body), []byte("data:"))
}

func collectResponsesEventStream(body []byte) ([]byte, error) {
	var delta strings.Builder
	done := make([]string, 0)
	final := make(map[string]any)
	for _, block := range splitSSEBlocks(string(body)) {
		dataLines := make([]string, 0)
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSuffix(line, "\r")
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		data := strings.Join(dataLines, "\n")
		if data == "" || data == "[DONE]" {
			continue
		}
		event, decodeErr := decodeJSONObject([]byte(data))
		if decodeErr != nil {
			return nil, &retry.ProviderError{Err: errors.New("provider returned a malformed event stream"), Code: "MALFORMED_STREAM", Parse: true}
		}
		switch stringValue(event["type"]) {
		case "response.output_text.delta":
			delta.WriteString(stringValue(event["delta"]))
		case "response.output_text.done":
			done = append(done, stringValue(event["text"]))
		case "response.completed":
			if response := objectValue(event["response"]); len(response) > 0 {
				final = response
			}
		case "response.failed", "error":
			return nil, normalizeResponsesStreamError(event)
		}
	}
	text := strings.Join(done, "")
	if text == "" {
		text = delta.String()
	}
	if text == "" {
		text = stringValue(final["output_text"])
	}
	if text == "" && len(final) == 0 {
		return nil, &retry.ProviderError{
			Err: errors.New("provider returned an empty or null response"), Code: "empty_response",
			RawResponse: string(body), Empty: true,
		}
	}
	if text != "" {
		final["output_text"] = text
	}
	encoded, encodeErr := json.Marshal(final)
	if encodeErr != nil {
		return nil, &retry.ProviderError{Err: errors.New("provider response stream normalization failed"), Code: "STREAM_NORMALIZATION", Parse: true}
	}
	return encoded, nil
}

func normalizeResponsesStreamError(event map[string]any) error {
	source := objectValue(event["error"])
	if len(source) == 0 {
		if response := objectValue(event["response"]); len(response) > 0 {
			source = objectValue(response["error"])
		}
	}
	if len(source) == 0 {
		source = event
	}
	message := strings.TrimSpace(firstString(source, event, "message"))
	if message == "" {
		message = strings.TrimSpace(stringValue(source["code"]))
	}
	if message == "" {
		message = strings.TrimSpace(stringValue(source["type"]))
	}
	if message == "" {
		message = "OpenAI Responses API stream failed."
	}
	status := firstStatus(source, event)
	code := boundedCode(stringValue(source["code"]))
	typeName := boundedCode(stringValue(source["type"]))
	requestID := ""
	requestIDMatch := responsesRequestIDPattern.FindStringSubmatch(message)
	hasStructuredEvidence := status != 0 || code != "" || typeName != ""
	if !hasStructuredEvidence && responsesRetryDirectivePattern.MatchString(message) && len(requestIDMatch) == 2 {
		code = "provider_retry"
		requestID = requestIDMatch[1]
	}
	return &retry.ProviderError{
		Err: errors.New(message), Code: code, Type: typeName, ProviderRequestID: requestID, Status: status,
	}
}

func normalizeResponsesStreamReadError(err error) error {
	for current := err; current != nil; current = errors.Unwrap(current) {
		message := strings.TrimSpace(current.Error())
		if responsesRetryDirectivePattern.MatchString(message) && responsesRequestIDPattern.MatchString(message) {
			return normalizeResponsesStreamError(map[string]any{"message": message})
		}
	}
	return nil
}

func firstString(primary, fallback map[string]any, key string) string {
	if value := stringValue(primary[key]); value != "" {
		return value
	}
	return stringValue(fallback[key])
}

func firstStatus(primary, fallback map[string]any) int {
	for _, object := range []map[string]any{primary, fallback} {
		value := object["status"]
		switch typed := value.(type) {
		case json.Number:
			if parsed, err := strconv.Atoi(typed.String()); err == nil && parsed > 0 {
				return parsed
			}
		case float64:
			if typed > 0 && typed == math.Trunc(typed) {
				return int(typed)
			}
		case int:
			if typed > 0 {
				return typed
			}
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return 0
}

func splitSSEBlocks(value string) []string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Split(value, "\n\n")
}

func validateProfile(profile runtime.Profile, call runtime.Call) error {
	if strings.TrimSpace(profile.ID) == "" || strings.TrimSpace(profile.ModelID) == "" {
		return errors.New("providers: profile ID and model ID are required")
	}
	if strings.TrimSpace(profile.BaseURL) == "" {
		return errors.New("providers: profile base URL is required")
	}
	switch profile.APIInferenceType {
	case "responses", "chat-completions", "gemini-generate-content", "anthropic-messages":
	default:
		return fmt.Errorf("providers: unsupported API inference type %q", profile.APIInferenceType)
	}
	if call.CallType != "text" && call.CallType != "structured" {
		return fmt.Errorf("providers: unsupported call type %q", call.CallType)
	}
	if call.CallType == "structured" {
		if !profile.SupportsStructuredOutput {
			return errors.New("providers: profile does not support contracted structured output")
		}
		if len(call.Schema) == 0 {
			return errors.New("providers: structured call schema is required")
		}
	}
	return nil
}

func endpointURL(baseURL, inferenceType, operationPath string) (string, *url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", nil, fmt.Errorf("providers: parse base URL: %w", err)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", nil, errors.New("providers: base URL cannot contain userinfo, query, or fragment")
	}
	if strings.ToLower(parsed.Scheme) != "https" || parsed.Opaque != "" {
		return "", nil, errors.New("providers: base URL must use HTTPS")
	}
	host, err := normalizeHost(parsed.Hostname())
	if err != nil {
		return "", nil, fmt.Errorf("providers: invalid base URL host: %w", err)
	}
	port := parsed.Port()
	if port == "443" {
		port = ""
	}
	if address, addressErr := netip.ParseAddr(host); addressErr == nil && address.Is6() {
		host = "[" + host + "]"
	}
	parsed.Scheme = "https"
	parsed.Host = host
	if port != "" {
		parsed.Host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	if inferenceType == "gemini-generate-content" && strings.HasSuffix(basePath, "/v1beta") {
		basePath = strings.TrimSuffix(basePath, "/v1beta")
	}
	parsed.RawPath = ""
	parsed.Path = basePath
	identity := strings.TrimRight(parsed.String(), "/")
	parsed.Path = strings.TrimRight(basePath, "/") + operationPath
	return identity, parsed, nil
}

func providerHeaders(inferenceType string, credential runtime.Credential) (http.Header, error) {
	headers := make(http.Header, len(credential.Headers)+4)
	for name, value := range credential.Headers {
		headers.Set(name, value)
	}
	headers = sanitizeHeaders(headers)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	key := strings.TrimSpace(credential.APIKey)
	if key == "" {
		return nil, errors.New("providers: API credential is empty")
	}
	switch inferenceType {
	case "gemini-generate-content":
		headers.Set("X-Goog-Api-Key", key)
	case "anthropic-messages":
		headers.Set("X-Api-Key", key)
		headers.Set("Anthropic-Version", defaultAnthropicVersion)
	default:
		headers.Set("Authorization", "Bearer "+key)
	}
	return headers, nil
}

func responseKind(callType string) string {
	if callType == "structured" {
		return "structured-output"
	}
	return "text"
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	limited := io.LimitReader(reader, maximum+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return body, providerResponseReadError{err: err}
	}
	if int64(len(body)) > maximum {
		return nil, errors.New("provider response exceeded the size limit")
	}
	return body, nil
}

type providerResponseReadError struct {
	err error
}

func (err providerResponseReadError) Error() string {
	return "provider response could not be read"
}

func (err providerResponseReadError) Unwrap() error {
	return err.err
}

func providerHTTPError(response *http.Response, body []byte) error {
	code, typeName := providerErrorFields(body)
	message := fmt.Sprintf("provider returned HTTP %d", response.StatusCode)
	if code != "" {
		message += " (" + code + ")"
	} else if typeName != "" {
		message += " (" + typeName + ")"
	}
	return &retry.ProviderError{
		Err: errors.New(message), Code: code, Type: typeName, Status: response.StatusCode,
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now()),
	}
}

func providerErrorFields(body []byte) (string, string) {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return "", ""
	}
	if nested, ok := payload["error"].(map[string]any); ok {
		code := boundedCode(stringValue(nested["code"]))
		typeName := boundedCode(stringValue(nested["type"]))
		if code == "" {
			code = boundedCode(stringValue(nested["status"]))
		}
		return code, typeName
	}
	code := boundedCode(stringValue(payload["code"]))
	typeName := boundedCode(stringValue(payload["type"]))
	if code == "" {
		code = boundedCode(stringValue(payload["status"]))
	}
	return code, typeName
}

func boundedCode(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 80 {
		value = value[:80]
	}
	for _, character := range value {
		if !(character == '_' || character == '-' || character == '.' || character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z') {
			return "provider_error"
		}
	}
	return value
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if timestamp, err := http.ParseTime(value); err == nil && timestamp.After(now) {
		return timestamp.Sub(now)
	}
	return 0
}
