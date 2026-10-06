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
	"sync"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

const liveProvidersEnvironment = "HARDEN_LLM_LIVE_PROVIDERS"

type liveProviderConfig struct {
	Name                               string               `json:"name"`
	APIKeyEnv                          string               `json:"apiKeyEnv"`
	Connection                         hardenllm.Connection `json:"connection"`
	ModelID                            string               `json:"modelId"`
	SupportsContractedStructuredOutput bool                 `json:"supportsContractedStructuredOutput"`
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
	var pace func(context.Context) error
	if value := os.Getenv("HARDEN_LLM_LIVE_PROVIDER_INTERVAL"); value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval <= 0 {
			t.Fatal("HARDEN_LLM_LIVE_PROVIDER_INTERVAL must be a positive duration")
		}
		var mu sync.Mutex
		var next time.Time
		pace = func(ctx context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			if delay := time.Until(next); delay > 0 {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			next = time.Now().Add(interval)
			return nil
		}
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
			base, err := url.Parse(item.Connection.BaseURL)
			if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil {
				t.Fatalf("provider %s has an invalid HTTPS base URL", name)
			}
			item.Connection.APIKey = credential
			client, err := hardenllm.New(hardenllm.Options{
				Connections:    []hardenllm.Connection{item.Connection},
				EndpointPolicy: hardenllm.EndpointPolicy{AllowedHosts: []string{base.Hostname()}},
			})
			if err != nil {
				t.Fatalf("initialize provider %s: %v", name, err)
			}
			waitLiveProvider(t, pace, name)
			textContext, cancelText := context.WithTimeout(context.Background(), 90*time.Second)
			textResult, err := client.Call(textContext, hardenllm.Request{
				ConnectionID: item.Connection.ID, ModelID: item.ModelID,
				Messages: []hardenllm.Message{{Role: "user", Content: json.RawMessage(`"Reply with exactly OK."`)}},
				CallType: hardenllm.CallTypeText, CacheMode: hardenllm.CacheModeOff,
				RecoveryPolicy: hardenllm.RecoveryPolicy{MaxAttempts: 1, RetryOn: []hardenllm.RecoveryCategory{}, Backoff: hardenllm.RecoveryBackoff{}},
			})
			cancelText()
			if err != nil {
				t.Fatalf("provider %s text call: %v", name, err)
			}
			if strings.TrimSpace(fmt.Sprint(textResult.Output)) == "" || len(textResult.Attempts) != 1 {
				t.Fatalf("provider %s returned an empty text result or unexpected attempts", name)
			}
			assertLiveAccounting(t, name, textResult)

			if !item.SupportsContractedStructuredOutput {
				return
			}
			waitLiveProvider(t, pace, name)
			structuredContext, cancelStructured := context.WithTimeout(context.Background(), 90*time.Second)
			structuredResult, err := client.Call(structuredContext, hardenllm.Request{
				ConnectionID: item.Connection.ID, ModelID: item.ModelID,
				Messages:       []hardenllm.Message{{Role: "user", Content: json.RawMessage(`"Return one JSON object whose ok field is true."`)}},
				CallType:       hardenllm.CallTypeStructured,
				Schema:         json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`),
				CacheMode:      hardenllm.CacheModeOff,
				RecoveryPolicy: hardenllm.RecoveryPolicy{MaxAttempts: 1, RetryOn: []hardenllm.RecoveryCategory{}, Backoff: hardenllm.RecoveryBackoff{}},
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

func waitLiveProvider(t *testing.T, pace func(context.Context) error, provider string) {
	t.Helper()
	if pace == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := pace(ctx); err != nil {
		t.Fatalf("pace provider %s: %v", provider, err)
	}
}

func assertLiveAccounting(t *testing.T, provider string, result hardenllm.Result) {
	t.Helper()
	for name, ledger := range map[string]hardenllm.AccountingLedger{
		"result": result.Accounting.Result, "provider": result.Accounting.Provider,
	} {
		usage := ledger.Usage
		if usage.InputTokens < 0 || usage.CacheReadTokens < 0 || usage.CacheCreationTokens < 0 || usage.OutputTokens < 0 || usage.ReasoningTokens < 0 || usage.TotalTokens < 0 {
			t.Fatalf("provider %s returned negative %s usage", provider, name)
		}
		cost := ledger.Cost
		if cost.KnownSubtotalUSD < 0 || (cost.KnownObservations > 0 && strings.TrimSpace(cost.Source) == "") {
			t.Fatalf("provider %s returned invalid known %s cost", provider, name)
		}
	}
}
