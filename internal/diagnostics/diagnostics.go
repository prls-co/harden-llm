// Package diagnostics builds bounded, redacted, storage-neutral support bundles.
package diagnostics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/prls-co/harden-llm/internal/redaction"
	"github.com/prls-co/harden-llm/internal/traces"
)

const (
	bundleSchemaVersion      = "harden-llm.diagnostics.v1"
	maxPersistenceMessageLen = 240
	maxBundleBytes           = 4 << 20
)

type RuntimeIdentity struct {
	Version    string    `json:"version"`
	CommitSHA  string    `json:"commitSha"`
	GoVersion  string    `json:"goVersion"`
	OS         string    `json:"os"`
	Arch       string    `json:"arch"`
	InstanceID string    `json:"instanceId,omitempty"`
	CapturedAt time.Time `json:"capturedAt,omitempty"`
}

type ArtifactIdentity struct {
	Kind        string    `json:"kind"`
	Key         string    `json:"key"`
	SHA256      string    `json:"sha256"`
	SizeBytes   int64     `json:"sizeBytes"`
	ContentType string    `json:"contentType"`
	CreatedAt   time.Time `json:"createdAt,omitempty"`
}

type Input struct {
	RuntimeIdentity RuntimeIdentity
	Trace           traces.Trace
	EndpointURL     string
	Environment     map[string]string
	Prompts         map[string]string
	Headers         map[string]string
	Config          map[string]any
	Logs            []string
	Error           string
	Artifacts       []ArtifactIdentity
	Secrets         []string
}

type Bundle struct {
	SchemaVersion          string             `json:"schemaVersion"`
	RuntimeIdentity        RuntimeIdentity    `json:"runtimeIdentity"`
	Trace                  traces.Trace       `json:"trace"`
	EndpointHost           string             `json:"endpointHost"`
	EnvironmentFingerprint string             `json:"environmentFingerprint"`
	Prompts                map[string]string  `json:"prompts,omitempty"`
	Headers                map[string]string  `json:"headers,omitempty"`
	Config                 map[string]any     `json:"config,omitempty"`
	Logs                   []string           `json:"logs,omitempty"`
	Error                  string             `json:"error,omitempty"`
	Artifacts              []ArtifactIdentity `json:"artifacts,omitempty"`
}

type Attachment struct {
	Key         string
	Content     []byte
	ContentType string
}

type ArtifactStore interface {
	Put(context.Context, string, []byte, string) (ArtifactIdentity, error)
}

type PersistenceObservation struct {
	Kind    string `json:"kind"`
	Outcome string `json:"outcome"`
	Message string `json:"message,omitempty"`
}

func Build(input Input) (Bundle, error) {
	parsed, err := url.Parse(strings.TrimSpace(input.EndpointURL))
	if err != nil || parsed.Hostname() == "" {
		return Bundle{}, errors.New("diagnostics: endpoint URL is invalid")
	}
	redactor := redaction.New(input.Secrets...)
	runtimeIdentity := input.RuntimeIdentity
	if err := redactInto(redactor, input.RuntimeIdentity, &runtimeIdentity); err != nil {
		return Bundle{}, err
	}
	trace := input.Trace
	if err := redactInto(redactor, input.Trace, &trace); err != nil {
		return Bundle{}, err
	}
	prompts := map[string]string(nil)
	if input.Prompts != nil {
		prompts = make(map[string]string)
		if err := redactInto(redactor, input.Prompts, &prompts); err != nil {
			return Bundle{}, err
		}
	}
	headers := map[string]string(nil)
	if input.Headers != nil {
		headers = make(map[string]string)
		if err := redactInto(redactor, input.Headers, &headers); err != nil {
			return Bundle{}, err
		}
	}
	config := map[string]any(nil)
	if input.Config != nil {
		config = make(map[string]any)
		if err := redactInto(redactor, input.Config, &config); err != nil {
			return Bundle{}, err
		}
	}
	logs := []string(nil)
	if input.Logs != nil {
		logs = make([]string, 0, len(input.Logs))
		if err := redactInto(redactor, input.Logs, &logs); err != nil {
			return Bundle{}, err
		}
	}
	artifacts := make([]ArtifactIdentity, len(input.Artifacts))
	if err := redactInto(redactor, input.Artifacts, &artifacts); err != nil {
		return Bundle{}, err
	}
	for _, artifact := range artifacts {
		if err := validateArtifactIdentity(artifact); err != nil {
			return Bundle{}, err
		}
	}
	fingerprint, err := environmentFingerprint(redactor, input.Environment)
	if err != nil {
		return Bundle{}, err
	}
	bundle := Bundle{
		SchemaVersion: bundleSchemaVersion, RuntimeIdentity: runtimeIdentity, Trace: trace,
		EndpointHost: strings.ToLower(strings.TrimSuffix(parsed.Hostname(), ".")), EnvironmentFingerprint: fingerprint,
		Prompts: prompts, Headers: headers, Config: config, Logs: logs,
		Error: bounded(redactor.Text(input.Error), maxPersistenceMessageLen), Artifacts: artifacts,
	}
	encodedBundle, err := json.Marshal(bundle)
	if err != nil {
		return Bundle{}, errors.New("diagnostics: bundle could not be encoded")
	}
	if len(encodedBundle) > maxBundleBytes {
		return Bundle{}, errors.New("diagnostics: bundle exceeds the size limit")
	}
	return bundle, nil
}

func PersistAttachment(ctx context.Context, store ArtifactStore, attachment Attachment, secrets []string) (ArtifactIdentity, *PersistenceObservation) {
	redactor := redaction.New(secrets...)
	if store == nil {
		return ArtifactIdentity{}, persistenceFailure("artifact store is not configured", redactor)
	}
	if ctx == nil {
		return ArtifactIdentity{}, persistenceFailure("artifact persistence context is missing", redactor)
	}
	if err := validateAttachment(attachment); err != nil {
		return ArtifactIdentity{}, persistenceFailure(err.Error(), redactor)
	}
	reference, err := store.Put(ctx, attachment.Key, append([]byte(nil), attachment.Content...), attachment.ContentType)
	if err != nil {
		return ArtifactIdentity{}, persistenceFailure(err.Error(), redactor)
	}
	if err := validateArtifactIdentity(reference); err != nil {
		return ArtifactIdentity{}, persistenceFailure(err.Error(), redactor)
	}
	return reference, nil
}

func persistenceFailure(message string, redactor *redaction.Redactor) *PersistenceObservation {
	return &PersistenceObservation{
		Kind: "artifact.persistence", Outcome: "failure",
		Message: bounded(redactor.Text(message), maxPersistenceMessageLen),
	}
}

func validateAttachment(attachment Attachment) error {
	if attachment.Key == "" || path.IsAbs(attachment.Key) || strings.Contains(attachment.Key, "\\") ||
		strings.Contains("/"+attachment.Key+"/", "/../") || strings.Contains(attachment.Key, "//") {
		return errors.New("diagnostics: attachment key is unsafe")
	}
	if len(attachment.Content) == 0 {
		return errors.New("diagnostics: attachment content is empty")
	}
	if attachment.ContentType != "application/json" {
		return errors.New("diagnostics: attachment content type must be application/json")
	}
	return nil
}

func validateArtifactIdentity(artifact ArtifactIdentity) error {
	if artifact.Key == "" || path.IsAbs(artifact.Key) || strings.Contains(artifact.Key, "\\") ||
		strings.Contains("/"+artifact.Key+"/", "/../") || strings.Contains(artifact.Key, "//") {
		return errors.New("diagnostics: artifact identity has an unsafe key")
	}
	digest := strings.TrimPrefix(artifact.SHA256, "sha256:")
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size || artifact.SizeBytes <= 0 || artifact.ContentType != "application/json" {
		return errors.New("diagnostics: artifact identity is incomplete")
	}
	return nil
}

func environmentFingerprint(redactor *redaction.Redactor, environment map[string]string) (string, error) {
	redacted, err := redactor.JSON(mustJSON(environment))
	if err != nil {
		return "", fmt.Errorf("diagnostics: redact environment: %w", err)
	}
	digest := sha256.Sum256(redacted)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func redactInto(redactor *redaction.Redactor, input, output any) error {
	encoded, err := json.Marshal(input)
	if err != nil {
		return errors.New("diagnostics: input contains unsupported values")
	}
	redacted, err := redactor.JSON(encoded)
	if err != nil {
		return fmt.Errorf("diagnostics: redact input: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(redacted))
	if err := decoder.Decode(output); err != nil {
		return errors.New("diagnostics: redacted input could not be projected")
	}
	return nil
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func bounded(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maximum {
		return value
	}
	end := maximum
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + "..."
}
