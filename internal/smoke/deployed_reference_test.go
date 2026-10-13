//go:build live

package smoke

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-435

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/gateway/auth"
)

// TestDeployedReferenceHTTP is explicitly selected after deployment identity
// verification. It reads feature status, never records or executes inference.
func TestDeployedReferenceHTTP(t *testing.T) {
	origin := strings.TrimSuffix(os.Getenv("HARDEN_LLM_ACCEPTANCE_URL"), "/")
	release := os.Getenv("HLLM_RELEASE_SHA")
	key := os.Getenv("HARDEN_LLM_TOKEN")
	target, err := url.Parse(origin)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.Path != "" || target.RawQuery != "" || target.Fragment != "" {
		t.Fatal("HARDEN_LLM_ACCEPTANCE_URL must be an explicit HTTPS origin")
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(release) || !auth.ValidToken(key) {
		t.Fatal("HLLM_RELEASE_SHA and HARDEN_LLM_TOKEN must be supplied explicitly")
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	probe := func(method, path, bearer, body string, status int) (http.Header, []byte) {
		t.Helper()
		request, err := http.NewRequest(method, origin+path, strings.NewReader(body))
		if err != nil {
			t.Fatal("invalid acceptance request")
		}
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("%s %s transport failed: %v", method, path, err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		if err != nil || len(data) > 2<<20 {
			t.Fatalf("%s %s response unreadable or oversized", method, path)
		}
		if response.StatusCode != status {
			t.Fatalf("%s %s status = %d, want %d", method, path, response.StatusCode, status)
		}
		if bytes.Contains(data, []byte(key)) {
			t.Fatal("acceptance response disclosed the incoming bearer")
		}
		return response.Header, data
	}
	decode := func(data []byte) map[string]any {
		t.Helper()
		var value map[string]any
		if json.Unmarshal(data, &value) != nil || value == nil {
			t.Fatal("acceptance response must be a JSON object")
		}
		return value
	}
	for path, status := range map[string]string{"/healthz": "ok", "/readyz": "ready"} {
		_, body := probe("GET", path, "", "", 200)
		if decode(body)["status"] != status {
			t.Fatalf("%s has the wrong health contract", path)
		}
	}
	headers, _ := probe("GET", "/", "", "", 302)
	login, err := url.Parse(headers.Get("Location"))
	if err != nil || login.Scheme != "https" || login.Hostname() == "" || login.User != nil || login.Path != "/login" || login.Query().Get("return_to") != origin+"/" {
		t.Fatal("frontend entry must redirect to Portal login with the same-origin return destination")
	}
	probe("GET", "/metrics", "", "", 404)
	digest := sha256.Sum256([]byte(key))
	for _, bearer := range []string{"", "synthetic-invalid-acceptance-key", hex.EncodeToString(digest[:])} {
		for _, path := range []string{"/v1/models", "/api/v1/status"} {
			_, body := probe("GET", path, bearer, "", 401)
			problem, ok := decode(body)["error"].(map[string]any)
			if !ok || problem["code"] != "invalid_api_key" {
				t.Fatal("unauthorized response has the wrong bearer contract")
			}
		}
	}
	_, modelsBody := probe("GET", "/v1/models", key, "", 200)
	models := decode(modelsBody)
	data, ok := models["data"].([]any)
	if !ok || len(data) == 0 || models["object"] != "list" {
		t.Fatal("configured upstream model discovery returned no model list")
	}
	// These requests fail local validation before provider dispatch or mutation.
	probe("POST", "/v1/responses", key, `{}`, 400)
	for _, selector := range []string{"api_key_hash", "ownerId", "user_id", "sessionReference"} {
		_, body := probe("GET", "/api/v1/status?"+selector+"=rejected", key, "", 400)
		problem, ok := decode(body)["error"].(map[string]any)
		if !ok || problem["code"] != "scope_selector_rejected" {
			t.Fatal("reference query selector was not rejected at the bearer boundary")
		}
	}
	_, body := probe("POST", "/api/v1/run", key, `{"apiKeyHash":"rejected"}`, 400)
	problem, ok := decode(body)["error"].(map[string]any)
	if !ok || problem["code"] != "scope_selector_rejected" {
		t.Fatal("reference body selector was not rejected before execution")
	}
	_, statusBody := probe("GET", "/api/v1/status", key, "", 200)
	status := decode(statusBody)
	for _, name := range []string{"storage", "historyStatus", "artifacts", "credentials"} {
		feature, ok := status[name].(map[string]any)
		if !ok || feature["mode"] != "enabled" || feature["status"] != "configured" {
			t.Fatalf("reference feature %s is not configured and available", name)
		}
	}
	history, ok := status["history"].(map[string]any)
	if !ok {
		t.Fatal("status is missing history growth figures")
	}
	for _, name := range []string{"rows", "tableBytes"} {
		value, ok := history[name].(float64)
		if !ok || value < 0 || value != float64(int64(value)) {
			t.Fatalf("history.%s must be a nonnegative integer", name)
		}
	}
	t.Logf("TEST-435: origin=%s expected_release=%s models=%d history.rows=%.0f history.tableBytes=%.0f; HTTP only, no inference or stored-record reads", origin, release, len(data), history["rows"], history["tableBytes"])
}
