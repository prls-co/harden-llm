//go:build live

package smoke

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-038

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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const liveGatewayConfigEnvironment = "HARDEN_LLM_LIVE_GATEWAY_CONFIG"

type liveGatewayConfig struct {
	GatewayURL           string          `json:"gatewayUrl"`
	Email                string          `json:"email"`
	PasswordEnv          string          `json:"passwordEnv"`
	ProviderAPIKeyEnv    string          `json:"providerApiKeyEnv"`
	Profile              json.RawMessage `json:"profile"`
	ArtifactAllowedHosts []string        `json:"artifactAllowedHosts"`
	GrafanaURL           string          `json:"grafanaUrl"`
	GrafanaUserEnv       string          `json:"grafanaUserEnv"`
	GrafanaPasswordEnv   string          `json:"grafanaPasswordEnv"`
	LangfuseURL          string          `json:"langfuseUrl"`
	LangfusePublicKeyEnv string          `json:"langfusePublicKeyEnv"`
	LangfuseSecretKeyEnv string          `json:"langfuseSecretKeyEnv"`
}

type liveSecrets struct {
	password        string
	providerAPIKey  string
	grafanaUser     string
	grafanaPassword string
	langfusePublic  string
	langfuseSecret  string
}

type liveResponse struct {
	status int
	body   []byte
	value  map[string]any
	header http.Header
}

func TestLiveGatewayLifecycle(t *testing.T) {
	configPath := strings.TrimSpace(os.Getenv(liveGatewayConfigEnvironment))
	if configPath == "" {
		t.Skip("not run: credentials absent (HARDEN_LLM_LIVE_GATEWAY_CONFIG is unset)")
	}
	config, secrets := loadLiveGatewayConfig(t, configPath)
	client := &http.Client{
		Timeout:       70 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	login := liveRequest(t, client, http.MethodPost, config.GatewayURL+"/api/v1/auth/login", map[string]any{
		"email": config.Email, "password": secrets.password,
	}, "", http.StatusOK)
	token := liveText(t, liveObject(t, login.value["result"], "login result")["accessToken"], "access token")
	if token == "" {
		t.Fatal("login returned an empty access token")
	}

	unique := fmt.Sprintf("live-%d", time.Now().UTC().UnixNano())
	profile := liveProfile(t, config.Profile, unique)
	profileID := liveText(t, profile["llmProfile"], "profile ID")
	credentialID := "credential-" + unique
	var runID, traceID string
	t.Cleanup(func() {
		cleanupClient := &http.Client{Timeout: 30 * time.Second, CheckRedirect: client.CheckRedirect}
		if runID != "" {
			liveCleanupRequest(t, cleanupClient, http.MethodDelete, config.GatewayURL+"/api/v1/history/"+url.PathEscape(runID), token)
		}
		liveCleanupRequest(t, cleanupClient, http.MethodDelete, config.GatewayURL+"/api/v1/profiles/"+url.PathEscape(profileID), token)
		liveCleanupRequest(t, cleanupClient, http.MethodPost, config.GatewayURL+"/api/v1/auth/logout", token)
	})

	liveRequest(t, client, http.MethodPut, config.GatewayURL+"/api/v1/profiles/"+url.PathEscape(profileID), map[string]any{
		"profile": profile, "credentialId": credentialID,
		"credential": map[string]any{"apiKey": secrets.providerAPIKey},
	}, token, http.StatusOK)
	refresh := liveRequest(t, client, http.MethodPost, config.GatewayURL+"/api/v1/profiles/"+url.PathEscape(profileID)+"/models:refresh", nil, token, http.StatusOK)
	refreshedProfile := liveObject(t, liveObject(t, refresh.value["result"], "refresh result")["profile"], "refreshed profile")
	if models, ok := refreshedProfile["models"].([]any); !ok || len(models) == 0 {
		t.Fatal("live model refresh returned no models")
	}

	prompt := "Reply with exactly LIVE-CERTIFIED."
	run := liveRequest(t, client, http.MethodPost, config.GatewayURL+"/api/v1/run", map[string]any{
		"profileId": profileID, "userPrompt": prompt, "callType": "text", "cacheMode": "off", "maxAttempts": 1,
	}, token, http.StatusOK)
	runResult := liveObject(t, run.value["result"], "run result")
	runID = liveText(t, runResult["runId"], "run ID")
	traceID = liveText(t, runResult["traceId"], "trace ID")
	if strings.TrimSpace(fmt.Sprint(runResult["output"])) == "" || runID == "" || traceID == "" {
		t.Fatal("live run returned incomplete output or correlation identity")
	}

	trace := liveRequest(t, client, http.MethodGet, config.GatewayURL+"/api/v1/traces/"+url.PathEscape(traceID), nil, token, http.StatusOK)
	if liveContainsSecret(trace.body, secrets) {
		t.Fatal("trace response contains credential material")
	}
	traceResult := liveObject(t, trace.value["result"], "trace result")
	artifacts, ok := traceResult["artifacts"].([]any)
	if !ok || len(artifacts) == 0 {
		t.Fatal("live trace returned no artifacts")
	}
	artifact := liveObject(t, artifacts[0], "trace artifact")
	artifactID := liveText(t, artifact["artifactId"], "artifact ID")
	wantDigest := liveText(t, artifact["sha256"], "artifact SHA-256")
	wantSize := int64(liveNumber(t, artifact["sizeBytes"], "artifact size"))
	redirect := liveRequest(t, client, http.MethodGet,
		config.GatewayURL+"/api/v1/traces/"+url.PathEscape(traceID)+"/artifacts/"+url.PathEscape(artifactID), nil, token, http.StatusSeeOther)
	location := validateLiveArtifactLocation(t, redirect.header.Get("Location"), config.ArtifactAllowedHosts)
	artifactBytes := liveArtifact(t, location)
	digest := sha256.Sum256(artifactBytes)
	if int64(len(artifactBytes)) != wantSize || hex.EncodeToString(digest[:]) != wantDigest || !json.Valid(artifactBytes) || liveContainsSecret(artifactBytes, secrets) || bytes.Contains(artifactBytes, []byte(prompt)) {
		t.Fatal("live artifact failed integrity or redaction checks")
	}

	bundle := liveRequest(t, client, http.MethodGet, config.GatewayURL+"/api/v1/profiles/bundle", nil, token, http.StatusOK)
	if liveContainsSecret(bundle.body, secrets) {
		t.Fatal("profile bundle contains plaintext credential material")
	}
	bundleResult := liveObject(t, bundle.value["result"], "bundle result")
	profiles := liveObject(t, bundleResult["profiles"], "bundle profiles")
	if _, ok := profiles[profileID]; !ok {
		t.Fatal("profile bundle does not include the live certification profile")
	}

	assertLiveDiagnostics(t, config, secrets, runID, traceID)

	liveRequest(t, client, http.MethodDelete, config.GatewayURL+"/api/v1/history/"+url.PathEscape(runID), nil, token, http.StatusOK)
	runID = ""
	liveRequest(t, client, http.MethodDelete, config.GatewayURL+"/api/v1/profiles/"+url.PathEscape(profileID), nil, token, http.StatusOK)
	profileID = ""
	liveRequest(t, client, http.MethodPost, config.GatewayURL+"/api/v1/auth/logout", nil, token, http.StatusOK)
	token = ""
}

func loadLiveGatewayConfig(t *testing.T, path string) (liveGatewayConfig, liveSecrets) {
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
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatal("live gateway config must contain exactly one JSON value")
	}
	config.GatewayURL = validateLiveOrigin(t, config.GatewayURL, "gateway")
	config.GrafanaURL = validateLiveOrigin(t, config.GrafanaURL, "Grafana")
	config.LangfuseURL = validateLiveOrigin(t, config.LangfuseURL, "Langfuse")
	if strings.TrimSpace(config.Email) == "" || len(config.Profile) == 0 || len(config.ArtifactAllowedHosts) == 0 {
		t.Fatal("live gateway email, profile, and artifact host allowlist are required")
	}
	secret := func(name, purpose string) string {
		name = strings.TrimSpace(name)
		value := strings.TrimSpace(os.Getenv(name))
		if name == "" || value == "" {
			t.Fatalf("live gateway %s requires its named credential environment variable", purpose)
		}
		return value
	}
	return config, liveSecrets{
		password:        secret(config.PasswordEnv, "user password"),
		providerAPIKey:  secret(config.ProviderAPIKeyEnv, "provider API key"),
		grafanaUser:     secret(config.GrafanaUserEnv, "Grafana user"),
		grafanaPassword: secret(config.GrafanaPasswordEnv, "Grafana password"),
		langfusePublic:  secret(config.LangfusePublicKeyEnv, "Langfuse public key"),
		langfuseSecret:  secret(config.LangfuseSecretKeyEnv, "Langfuse secret key"),
	}
}

func validateLiveOrigin(t *testing.T, value, name string) string {
	t.Helper()
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		t.Fatalf("live %s URL must be an HTTPS origin", name)
	}
	return strings.TrimRight(parsed.String(), "/")
}

func liveProfile(t *testing.T, raw json.RawMessage, profileID string) map[string]any {
	t.Helper()
	var profile map[string]any
	if err := json.Unmarshal(raw, &profile); err != nil || profile == nil {
		t.Fatalf("decode live profile: %v", err)
	}
	if backups, exists := profile["backupProfiles"]; exists {
		values, ok := backups.([]any)
		if !ok || len(values) != 0 {
			t.Fatal("live certification profile must have an empty backupProfiles array")
		}
	}
	profile["llmProfile"] = profileID
	profile["endpointCredentialScope"] = "user"
	profile["backupProfiles"] = []any{}
	delete(profile, "models")
	delete(profile, "lastModelRefreshAt")
	return profile
}

func liveRequest(t *testing.T, client *http.Client, method, target string, document any, token string, want int) liveResponse {
	t.Helper()
	var body io.Reader
	if document != nil {
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/json")
	if document != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s live gateway request failed: %v", method, err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, 20<<20))
	if err != nil {
		t.Fatalf("read live gateway response: %v", err)
	}
	if response.StatusCode != want {
		t.Fatalf("%s live gateway response status = %d, want %d", method, response.StatusCode, want)
	}
	result := liveResponse{status: response.StatusCode, body: contents, header: response.Header.Clone()}
	if response.StatusCode != http.StatusSeeOther {
		if err := json.Unmarshal(contents, &result.value); err != nil {
			t.Fatalf("decode live gateway response: %v", err)
		}
	}
	return result
}

func liveCleanupRequest(t *testing.T, client *http.Client, method, target, token string) {
	t.Helper()
	if token == "" || strings.HasSuffix(target, "/profiles/") {
		return
	}
	request, err := http.NewRequestWithContext(context.Background(), method, target, nil)
	if err != nil {
		t.Errorf("build live cleanup request: %v", err)
		return
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		t.Errorf("live cleanup request failed: %v", err)
		return
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusUnauthorized {
		t.Errorf("live cleanup status = %d", response.StatusCode)
	}
}

func validateLiveArtifactLocation(t *testing.T, location string, allowed []string) string {
	t.Helper()
	parsed, err := url.Parse(location)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		t.Fatal("artifact redirect is not a valid HTTPS URL")
	}
	for _, host := range allowed {
		if strings.EqualFold(strings.TrimSpace(host), parsed.Hostname()) {
			return parsed.String()
		}
	}
	t.Fatal("artifact redirect host is not allowlisted")
	return ""
}

func liveArtifact(t *testing.T, location string) []byte {
	t.Helper()
	response, err := (&http.Client{Timeout: 30 * time.Second}).Get(location)
	if err != nil {
		t.Fatalf("fetch live artifact: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("live artifact status = %d", response.StatusCode)
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, 20<<20))
	if err != nil {
		t.Fatalf("read live artifact: %v", err)
	}
	return contents
}

func assertLiveDiagnostics(t *testing.T, config liveGatewayConfig, secrets liveSecrets, runID, domainTraceID string) {
	t.Helper()
	deadline := time.Now().Add(180 * time.Second)
	var tempoID string
	complete := map[string]bool{}
	for time.Now().Before(deadline) {
		if !complete["tempo"] {
			query := `{ span.harden_llm.trace.id = "` + domainTraceID + `" }`
			body, ok := liveBasicGET(config.GrafanaURL+"/api/datasources/proxy/uid/harden-tempo/api/search?q="+url.QueryEscape(query), secrets.grafanaUser, secrets.grafanaPassword)
			if ok && !liveContainsSecret(body, secrets) {
				tempoID = liveTempoTraceID(body)
				complete["tempo"] = tempoID != ""
			}
		}
		if !complete["prometheus"] {
			body, ok := liveBasicGET(config.GrafanaURL+"/api/datasources/proxy/uid/harden-prometheus/api/v1/query?query="+url.QueryEscape("harden_llm_calls"), secrets.grafanaUser, secrets.grafanaPassword)
			complete["prometheus"] = ok && livePrometheusSample(body) && !liveContainsSecret(body, secrets)
		}
		if !complete["loki"] {
			query := `{service_name="harden-llm-gateway"} |= "run completed"`
			body, ok := liveBasicGET(config.GrafanaURL+"/api/datasources/proxy/uid/harden-loki/loki/api/v1/query_range?limit=100&direction=backward&query="+url.QueryEscape(query), secrets.grafanaUser, secrets.grafanaPassword)
			complete["loki"] = ok && bytes.Contains(body, []byte(runID)) && !liveContainsSecret(body, secrets)
		}
		if !complete["langfuse"] && tempoID != "" {
			body, ok := liveBasicGET(config.LangfuseURL+"/api/public/traces/"+url.PathEscape(tempoID), secrets.langfusePublic, secrets.langfuseSecret)
			complete["langfuse"] = ok && bytes.Contains(body, []byte(domainTraceID)) && !liveContainsSecret(body, secrets)
		}
		if len(complete) == 4 && complete["tempo"] && complete["prometheus"] && complete["loki"] && complete["langfuse"] {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("live diagnostics correlation incomplete: tempo=%t prometheus=%t loki=%t langfuse=%t", complete["tempo"], complete["prometheus"], complete["loki"], complete["langfuse"])
}

func liveBasicGET(target, username, password string) ([]byte, bool) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		return nil, false
	}
	request.SetBasicAuth(username, password)
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil, false
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	return body, err == nil && response.StatusCode >= 200 && response.StatusCode < 300
}

func liveTempoTraceID(body []byte) string {
	var response struct {
		Traces []struct {
			TraceID string `json:"traceID"`
		} `json:"traces"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Traces) == 0 {
		return ""
	}
	value := strings.ToLower(strings.TrimSpace(response.Traces[0].TraceID))
	if len(value) != 32 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func livePrometheusSample(body []byte) bool {
	var response struct {
		Status string `json:"status"`
		Data   struct {
			Result []json.RawMessage `json:"result"`
		} `json:"data"`
	}
	return json.Unmarshal(body, &response) == nil && response.Status == "success" && len(response.Data.Result) > 0
}

func liveContainsSecret(body []byte, secrets liveSecrets) bool {
	for _, secret := range []string{secrets.password, secrets.providerAPIKey, secrets.grafanaPassword, secrets.langfuseSecret} {
		if secret != "" && bytes.Contains(body, []byte(secret)) {
			return true
		}
	}
	return false
}

func liveObject(t *testing.T, value any, name string) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object", name)
	}
	return object
}

func liveText(t *testing.T, value any, name string) string {
	t.Helper()
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		t.Fatalf("%s is not non-empty text", name)
	}
	return text
}

func liveNumber(t *testing.T, value any, name string) float64 {
	t.Helper()
	number, ok := value.(float64)
	if !ok || number <= 0 {
		t.Fatalf("%s is not a positive number", name)
	}
	return number
}
