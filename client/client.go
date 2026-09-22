// Package client is the provider-independent HTTP client for durable Harden
// structured operations. It contains transport and wire decoding only;
// provider selection, cache identity, repair and credentials remain owned by
// the Harden service.
package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	SchemaVersion = "harden-llm-operation.v1"
	maxBodyBytes  = 512 << 10
)

var (
	ErrConflict      = errors.New("harden client: operation identity conflict")
	ErrUnauthorized  = errors.New("harden client: unauthorized")
	ErrUnavailable   = errors.New("harden client: service unavailable")
	ErrOperationFail = errors.New("harden client: operation failed")
)

type Client struct {
	baseURL    string
	service    string
	serviceKey string
	http       *http.Client
	pollEvery  time.Duration
}

type Config struct {
	BaseURL     string
	ServiceName string
	ServiceKey  string
	HTTPClient  *http.Client
	PollEvery   time.Duration
}

func New(config Config) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("harden client: BaseURL must be an HTTP origin")
	}
	if strings.TrimSpace(config.ServiceName) == "" || strings.TrimSpace(config.ServiceKey) == "" {
		return nil, errors.New("harden client: service name and key are required")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if config.PollEvery == 0 {
		config.PollEvery = 250 * time.Millisecond
	}
	if config.PollEvery < 10*time.Millisecond || config.PollEvery > 30*time.Second {
		return nil, errors.New("harden client: PollEvery is outside the supported range")
	}
	return &Client{baseURL: parsed.String(), service: config.ServiceName, serviceKey: config.ServiceKey, http: config.HTTPClient, pollEvery: config.PollEvery}, nil
}

type StructuredRequest struct {
	OperationID     string            `json:"operationId"`
	AccountID       string            `json:"accountId"`
	InputDigest     string            `json:"inputDigest"`
	ProfileID       string            `json:"profileId"`
	ModelID         string            `json:"modelId,omitempty"`
	SystemPrompt    string            `json:"systemPrompt,omitempty"`
	UserPrompt      string            `json:"userPrompt"`
	Schema          json.RawMessage   `json:"schema"`
	ReasoningEffort string            `json:"reasoningEffort,omitempty"`
	WebSearch       bool              `json:"webSearch,omitempty"`
	ProviderOptions map[string]any    `json:"providerOptions,omitempty"`
	CacheMode       string            `json:"cacheMode,omitempty"`
	CacheVersion    string            `json:"cacheVersion,omitempty"`
	TimeoutMS       int               `json:"timeoutMs,omitempty"`
	RecoveryPolicy  json.RawMessage   `json:"recoveryPolicy,omitempty"`
	Origin          map[string]string `json:"origin,omitempty"`
}

func (request StructuredRequest) Digest() string {
	encoded, _ := json.Marshal(request)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type Operation struct {
	SchemaVersion string          `json:"schemaVersion"`
	OperationID   string          `json:"operationId"`
	AccountID     string          `json:"accountId"`
	InputDigest   string          `json:"inputDigest"`
	Status        string          `json:"status"`
	Output        json.RawMessage `json:"output,omitempty"`
	Error         *Failure        `json:"error,omitempty"`
	RunID         string          `json:"runId,omitempty"`
	TraceID       string          `json:"traceId,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (client *Client) SubmitStructured(ctx context.Context, request StructuredRequest) (Operation, error) {
	if err := validateRequest(request); err != nil {
		return Operation{}, err
	}
	return client.do(ctx, http.MethodPost, "/internal/v1/llm-operations", request, request.AccountID)
}

func (client *Client) Get(ctx context.Context, accountID, operationID string) (Operation, error) {
	if strings.TrimSpace(operationID) == "" || len(operationID) > 128 {
		return Operation{}, errors.New("harden client: operation ID is invalid")
	}
	return client.do(ctx, http.MethodGet, "/internal/v1/llm-operations/"+url.PathEscape(operationID), nil, accountID)
}

func (client *Client) Cancel(ctx context.Context, accountID, operationID string) (Operation, error) {
	if strings.TrimSpace(operationID) == "" || len(operationID) > 128 {
		return Operation{}, errors.New("harden client: operation ID is invalid")
	}
	return client.do(ctx, http.MethodPost, "/internal/v1/llm-operations/"+url.PathEscape(operationID)+":cancel", nil, accountID)
}

func (client *Client) Wait(ctx context.Context, accountID, operationID string) (Operation, error) {
	ticker := time.NewTicker(client.pollEvery)
	defer ticker.Stop()
	for {
		operation, err := client.Get(ctx, accountID, operationID)
		if err != nil {
			return Operation{}, err
		}
		switch operation.Status {
		case "succeeded":
			return operation, nil
		case "failed", "cancelled", "unresolved":
			return operation, fmt.Errorf("%w: %s", ErrOperationFail, operation.ErrorMessage())
		case "accepted", "running":
		default:
			return Operation{}, fmt.Errorf("%w: unknown operation status %q", ErrUnavailable, operation.Status)
		}
		select {
		case <-ctx.Done():
			return Operation{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (operation Operation) ErrorMessage() string {
	if operation.Error == nil {
		return operation.Status
	}
	return operation.Error.Code + ": " + operation.Error.Message
}

func (client *Client) do(ctx context.Context, method, path string, input any, accountID string) (Operation, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return Operation{}, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, body)
	if err != nil {
		return Operation{}, err
	}
	request.Header.Set("Authorization", "Bearer "+client.serviceKey)
	request.Header.Set("X-PRLS-Service", client.service)
	request.Header.Set("X-PRLS-Account-ID", accountID)
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return Operation{}, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return Operation{}, ErrUnauthorized
	}
	if response.StatusCode == http.StatusConflict {
		return Operation{}, ErrConflict
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Operation{}, ErrUnavailable
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	var operation Operation
	if err := decoder.Decode(&operation); err != nil {
		return Operation{}, ErrUnavailable
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Operation{}, ErrUnavailable
	}
	if operation.SchemaVersion != SchemaVersion || operation.OperationID == "" {
		return Operation{}, ErrUnavailable
	}
	return operation, nil
}

func validateRequest(request StructuredRequest) error {
	for name, value := range map[string]string{"operationId": request.OperationID, "accountId": request.AccountID, "profileId": request.ProfileID, "userPrompt": request.UserPrompt} {
		if strings.TrimSpace(value) == "" || len(value) > 64<<10 {
			return fmt.Errorf("harden client: %s is required and bounded", name)
		}
	}
	if len(request.OperationID) > 128 || len(request.AccountID) > 128 || len(request.Schema) == 0 || len(request.Schema) > 64<<10 {
		return errors.New("harden client: operation request is outside bounds")
	}
	if len(request.InputDigest) != sha256.Size*2 {
		return errors.New("harden client: inputDigest must be a 64-character lowercase SHA-256 digest")
	}
	if _, err := hex.DecodeString(request.InputDigest); err != nil || request.InputDigest != strings.ToLower(request.InputDigest) {
		return errors.New("harden client: inputDigest must be a 64-character lowercase SHA-256 digest")
	}
	return nil
}
