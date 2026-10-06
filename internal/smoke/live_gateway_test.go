//go:build live

package smoke

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-038

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/gateway/auth"
)

const liveGatewayConfigEnvironment = "HARDEN_LLM_LIVE_GATEWAY_CONFIG"

type liveGatewayConfig struct {
	GatewayURL      string `json:"gatewayUrl"`
	ServiceTokenEnv string `json:"serviceTokenEnv"`
	Model           string `json:"model"`
}

func TestLiveGatewayResponses(t *testing.T) {
	configPath := strings.TrimSpace(os.Getenv(liveGatewayConfigEnvironment))
	if configPath == "" {
		t.Skip("not run: credentials absent (HARDEN_LLM_LIVE_GATEWAY_CONFIG is unset)")
	}
	config, token := loadLiveGatewayConfig(t, configPath)
	client := &http.Client{Timeout: 70 * time.Second}

	unauthorized, err := http.NewRequest(http.MethodPost, config.GatewayURL+"/v1/responses", bytes.NewReader([]byte(`{"model":"unused","input":"unused"}`)))
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Header.Set("Content-Type", "application/json")
	response, err := client.Do(unauthorized)
	if err != nil {
		t.Fatalf("unauthorized gateway request failed: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized gateway status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	body, err := json.Marshal(map[string]any{
		"model": config.Model,
		"input": "Reply with exactly LIVE-CERTIFIED.",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, config.GatewayURL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("authorized gateway request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authorized gateway status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var result struct {
		Status     string `json:"status"`
		OutputText string `json:"output_text"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<20))
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("decode Responses result: %v", err)
	}
	if result.Status != "completed" || !strings.Contains(result.OutputText, "LIVE-CERTIFIED") {
		t.Fatalf("Responses result status/output did not satisfy the live canary")
	}
}

func loadLiveGatewayConfig(t *testing.T, path string) (liveGatewayConfig, string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read live gateway config: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var config liveGatewayConfig
	if err := decoder.Decode(&config); err != nil {
		t.Fatalf("parse live gateway config: %v", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		t.Fatal("live gateway config must contain exactly one JSON value")
	}
	parsed, err := url.Parse(strings.TrimSpace(config.GatewayURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		t.Fatal("live gateway URL must be an HTTPS origin")
	}
	config.GatewayURL = strings.TrimRight(parsed.String(), "/")
	config.Model = strings.TrimSpace(config.Model)
	if config.Model == "" {
		t.Fatal("live gateway model is required")
	}
	tokenName := strings.TrimSpace(config.ServiceTokenEnv)
	token := strings.TrimSpace(os.Getenv(tokenName))
	if tokenName == "" || !auth.ValidToken(token) {
		t.Fatal("live gateway requires a valid named API token")
	}
	return config, token
}
