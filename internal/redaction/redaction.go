// Package redaction is the single secret-scrubbing boundary used before logs,
// telemetry, diagnostics, or artifacts leave the trusted call path.
package redaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const Replacement = "[REDACTED]"

var (
	secretKeyPattern   = regexp.MustCompile(`(?i)(api[-_]?key|authorization|proxy[-_]?authorization|cookie|set[-_]?cookie|password|passwd|secret|client[-_]?secret|access[-_]?token|refresh[-_]?token|id[-_]?token|credential|ciphertext|auth[-_]?tag|encryption[-_]?key|private[-_]?key)`)
	bearerPattern      = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)
	providerKeyPattern = regexp.MustCompile(`\b(?:sk|sk-ant|AIza|xai|gsk|nvapi)-?[A-Za-z0-9._~+/=-]{8,}`)
	pemPattern         = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	querySecretKey     = regexp.MustCompile(`(?i)^(key|api[-_]?key|token|access[-_]?token|auth|authorization|signature|sig|credential|password|secret)$`)
	embeddedURLPattern = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
)

type Redactor struct {
	literals []string
}

func New(secrets ...string) *Redactor {
	seen := make(map[string]struct{}, len(secrets))
	literals := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" || secret == Replacement {
			continue
		}
		if _, ok := seen[secret]; ok {
			continue
		}
		seen[secret] = struct{}{}
		literals = append(literals, secret)
	}
	slices.SortFunc(literals, func(left, right string) int { return len(right) - len(left) })
	return &Redactor{literals: literals}
}

func JSON(input []byte) ([]byte, error) {
	return New().JSON(input)
}

func (redactor *Redactor) JSON(input []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return json.Marshal(map[string]any{"rawText": redactor.Text(string(input))})
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return json.Marshal(map[string]any{"rawText": redactor.Text(string(input))})
	}
	return json.Marshal(redactor.Value(value))
}

func Value(value any) any {
	return New().Value(value)
}

func (redactor *Redactor) Value(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSecretKey(key, item) {
				result[key] = Replacement
			} else {
				result[key] = redactor.Value(item)
			}
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = redactor.Value(item)
		}
		return result
	case string:
		return redactor.Text(typed)
	default:
		return value
	}
}

func Text(value string) string {
	return New().Text(value)
}

func (redactor *Redactor) Text(value string) string {
	for _, literal := range redactor.literals {
		value = strings.ReplaceAll(value, literal, Replacement)
	}
	value = pemPattern.ReplaceAllString(value, Replacement)
	value = bearerPattern.ReplaceAllString(value, Replacement)
	value = providerKeyPattern.ReplaceAllString(value, Replacement)
	value = embeddedURLPattern.ReplaceAllStringFunc(value, redactURL)
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		value = redactParsedURL(parsed)
	}
	return value
}

func redactURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return value
	}
	return redactParsedURL(parsed)
}

func redactParsedURL(parsed *url.URL) string {
	if parsed.User != nil {
		parsed.User = url.User(Replacement)
	}
	query := parsed.Query()
	for key := range query {
		if querySecretKey.MatchString(key) {
			query.Set(key, Replacement)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func isSecretKey(key string, value any) bool {
	if !secretKeyPattern.MatchString(strings.TrimSpace(key)) {
		return false
	}
	// Canonical usage counters are operational measurements, not credentials.
	lower := strings.ToLower(strings.TrimSpace(key))
	if strings.HasSuffix(lower, "tokens") || lower == "tokenstotal" || lower == "ratepertoken" {
		switch value.(type) {
		case int, int32, int64, uint, uint32, uint64, float32, float64, json.Number:
			return false
		}
	}
	return true
}
