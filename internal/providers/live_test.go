//go:build live

package providers_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-037

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

const liveProvidersEnvironment = "HARDEN_LLM_LIVE_PROVIDERS"

type liveProviderConfig struct {
	Name      string            `json:"name"`
	APIKeyEnv string            `json:"apiKeyEnv"`
	Profile   hardenllm.Profile `json:"profile"`
}

type liveCredential string

func (credential liveCredential) ResolveCredential(context.Context, hardenllm.CredentialRequest) (hardenllm.Credential, error) {
	return hardenllm.Credential{APIKey: string(credential)}, nil
}

func TestLiveProviders(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv(liveProvidersEnvironment))
	if raw == "" {
		t.Skip("not run: credentials absent (HARDEN_LLM_LIVE_PROVIDERS is unset)")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var configured []liveProviderConfig
	if err := decoder.Decode(&configured); err != nil || len(configured) == 0 {
		t.Fatalf("%s must be a non-empty JSON array of live provider configurations: %v", liveProvidersEnvironment, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("%s must contain exactly one JSON value", liveProvidersEnvironment)
	}

	for _, item := range configured {
		item := item
		name := strings.TrimSpace(item.Name)
		if name == "" {
			t.Fatal("live provider name is required")
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			credentialName := strings.TrimSpace(item.APIKeyEnv)
			credential := strings.TrimSpace(os.Getenv(credentialName))
			if credentialName == "" || credential == "" {
				t.Fatalf("configured provider %s requires its named API-key environment variable", name)
			}
			base, err := url.Parse(item.Profile.BaseURL)
			if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil {
				t.Fatalf("provider %s has an invalid HTTPS base URL", name)
			}
			client, err := hardenllm.New(hardenllm.Options{
				Credentials:    liveCredential(credential),
				EndpointPolicy: hardenllm.EndpointPolicy{AllowedHosts: []string{base.Hostname()}},
			})
			if err != nil {
				t.Fatalf("initialize provider %s: %v", name, err)
			}
			catalog := hardenllm.ProfileCatalog{item.Profile.LLMProfile: item.Profile}
			textContext, cancelText := context.WithTimeout(context.Background(), 90*time.Second)
			textResult, err := client.Call(textContext, hardenllm.Request{
				ProfileID:   item.Profile.LLMProfile,
				Profiles:    catalog,
				UserPrompt:  "Reply with exactly OK.",
				CallType:    hardenllm.CallTypeText,
				CacheMode:   hardenllm.CacheModeOff,
				RetryPolicy: hardenllm.RetryPolicy{MaxAttempts: 1},
			})
			cancelText()
			if err != nil {
				t.Fatalf("provider %s text call: %v", name, err)
			}
			if strings.TrimSpace(fmt.Sprint(textResult.Output)) == "" || len(textResult.Attempts) != 1 {
				t.Fatalf("provider %s returned an empty text result or unexpected attempts", name)
			}
			assertLiveAccounting(t, name, textResult)

			if !item.Profile.SupportsContractedStructuredOutput {
				return
			}
			structuredContext, cancelStructured := context.WithTimeout(context.Background(), 90*time.Second)
			structuredResult, err := client.Call(structuredContext, hardenllm.Request{
				ProfileID:   item.Profile.LLMProfile,
				Profiles:    catalog,
				UserPrompt:  "Return one JSON object whose ok field is true.",
				CallType:    hardenllm.CallTypeStructured,
				Schema:      json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`),
				CacheMode:   hardenllm.CacheModeOff,
				RetryPolicy: hardenllm.RetryPolicy{MaxAttempts: 1},
			})
			cancelStructured()
			if err != nil {
				t.Fatalf("provider %s structured call: %v", name, err)
			}
			object, ok := structuredResult.Output.(map[string]any)
			if !ok || object["ok"] != true {
				t.Fatalf("provider %s returned a non-conforming structured result", name)
			}
			assertLiveAccounting(t, name, structuredResult)
		})
	}
}

func assertLiveAccounting(t *testing.T, provider string, result hardenllm.Result) {
	t.Helper()
	usage := result.Usage
	if usage.InputTokens < 0 || usage.CacheReadTokens < 0 || usage.CacheCreationTokens < 0 || usage.OutputTokens < 0 || usage.ReasoningTokens < 0 || usage.TotalTokens < 0 {
		t.Fatalf("provider %s returned negative usage", provider)
	}
	if result.Cost.Known && (result.Cost.TotalUSD < 0 || strings.TrimSpace(result.Cost.Source) == "") {
		t.Fatalf("provider %s returned invalid known cost", provider)
	}
}
