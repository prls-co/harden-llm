//go:build compose

package smoke

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-433

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/stateful"
)

func assertReferenceCompose(t *testing.T, runner composeRunner, client *http.Client, identity *frontendControlPlaneFixture) {
	t.Helper()
	const origin = "https://app.smoke.localhost"
	a, b := runner.environment["HARDEN_LLM_TOKEN"], runner.environment["HARDEN_LLM_TEST_SECOND_TOKEN"]
	sql := func(query string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := runner.output(ctx, "exec", "-T", "harden-postgres", "psql", "-U", "postgres", "-d", "harden_llm", "-At", "-v", "ON_ERROR_STOP=1", "-c", query)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if sql("SELECT version FROM hllm_schema_version") != "1" {
		t.Fatal("fresh schema command did not install version 1")
	}
	if sql("SELECT count(*) FROM history") != "0" {
		t.Fatal("ordinary OpenAI request wrote history")
	}
	requestJSON(t, client, "GET", origin+"/api/v1/status", nil, "", 401)
	requestJSON(t, client, "GET", origin+"/api/v1/status", nil, a, 200)
	requestJSON(t, client, "GET", origin+"/v1/models", nil, b, 200)
	requestJSON(t, client, "POST", origin+"/api/v1/run", map[string]any{"apiKeyHash": "forged"}, a, 400)

	catalog, err := stateful.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	preset, exists := catalog["OpenAI GPT-5.6 Luna"]
	if !exists {
		t.Fatal("composed fixture requires the checked-in OpenAI Responses preset")
	}
	var definition map[string]any
	if err := json.Unmarshal(preset.Definition, &definition); err != nil {
		t.Fatal(err)
	}
	definition["baseUrl"], definition["modelId"] = "https://fake-provider:8443/v1", "smoke-model"
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	var inputs map[string]any
	if err := json.Unmarshal(preset.Inputs, &inputs); err != nil {
		t.Fatalf("decode preset inputs: %v", err)
	}
	inputs["modelId"], inputs["callType"] = "smoke-model", "text"
	preset.Inputs, err = json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := stateful.SaveProfileInput{Name: "Composed reference", Definition: encoded, Inputs: preset.Inputs,
		Binding: stateful.ProfileCredentialBinding{Source: "deployment", DeploymentUpstreamID: "smoke"}}
	profiles := make(map[string]string)
	for _, key := range []string{a, b} {
		saved := requestJSON(t, client, "POST", origin+"/api/v1/profiles", snapshot, key, 201)
		profiles[key] = text(t, object(t, saved["profile"], "saved profile")["id"], "profile ID")
	}
	requestJSON(t, client, "GET", origin+"/api/v1/profiles/"+profiles[a], nil, a, 200)
	requestJSON(t, client, "GET", origin+"/api/v1/profiles/"+profiles[a], nil, b, 404)
	requestJSON(t, client, "DELETE", origin+"/api/v1/profiles/"+profiles[a], nil, b, 404)
	draft := map[string]any{"draftKey": "workspace:reference-user", "revision": 0, "payload": map[string]any{"userPrompt": "composed draft"}}
	requestJSON(t, client, "POST", origin+"/api/v1/state", draft, a, 200)
	other := requestJSON(t, client, "GET", origin+"/api/v1/state?draftKey=workspace:reference-user", nil, b, 200)
	if integer(t, other["revision"], "foreign draft revision") != 0 {
		t.Fatal("foreign key inherited a draft")
	}

	policy := map[string]any{"maxAttempts": 1, "retryOn": []string{}, "backoff": map[string]any{"baseDelayMs": 0, "maxDelayMs": 0}, "jsonRepair": nil, "rerun": nil}
	run := func(key, prompt, mode string) map[string]any {
		t.Helper()
		response := requestJSON(t, client, "POST", origin+"/api/v1/run", map[string]any{
			"profileId": profiles[key], "userPrompt": prompt, "callType": "text", "cacheMode": mode, "recoveryPolicy": policy,
		}, key, 200)
		out := object(t, response["result"], "reference output")
		if out["output"] != "smoke-ok" {
			t.Fatalf("reference output = %#v", out)
		}
		return out
	}
	first := run(a, "reference cache isolation", "cache")
	if object(t, first["history"], "history outcome")["status"] != "saved" || first["providerInvoked"] != true {
		t.Fatal("first reference request not recorded/invoked")
	}
	id := text(t, first["runId"], "run ID")
	cached := run(a, "reference cache isolation", "cache")
	if cached["providerInvoked"] != false {
		t.Fatal("same key did not reuse cache")
	}
	foreign := run(b, "reference cache isolation", "cache")
	if foreign["providerInvoked"] != true {
		t.Fatal("foreign key reused cache")
	}
	requestJSON(t, client, "GET", origin+"/api/v1/history/"+id, nil, a, 200)
	requestJSON(t, client, "GET", origin+"/api/v1/history/"+id, nil, b, 404)
	requestJSON(t, client, "DELETE", origin+"/api/v1/history/"+id, nil, b, 404)
	page := requestJSON(t, client, "GET", origin+"/api/v1/history", nil, a, 200)
	if integer(t, object(t, page["pagination"], "pagination")["totalCount"], "history count") != 2 {
		t.Fatal("same-key history lost records")
	}
	requestJSON(t, client, "GET", origin+"/api/v1/stats", nil, a, 200)
	for _, suffix := range []string{"", "/artifacts/support-bundle.json"} {
		path := origin + "/api/v1/traces/" + id + suffix
		requestJSON(t, client, "GET", path, nil, a, 200)
		requestJSON(t, client, "GET", path, nil, b, 404)
	}

	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	getPage := func(path string, cookie bool) (int, http.Header, []byte) {
		t.Helper()
		req, _ := http.NewRequest("GET", origin+path, nil)
		if cookie {
			req.AddCookie(&http.Cookie{Name: "prls.session_token", Value: frontendSmokeSessionCookie})
		}
		res, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, res.Header, body
	}
	code, headers, _ := getPage("/", false)
	if code != 302 || !strings.HasPrefix(headers.Get("Location"), runner.environment["PRLS_PORTAL_URL"]+"/login") {
		t.Fatal("anonymous frontend did not use Portal admission", code)
	}
	// Exercise the shared authority's sign-in contract; no browser or HLLM login exists.
	signBody, _ := json.Marshal(map[string]string{"email": "reference@example.test", "password": "synthetic-reference-password"})
	signURL := strings.Replace(runner.environment["HARDEN_LLM_CONTROL_PLANE_URL"], "host.docker.internal", "127.0.0.1", 1) + "/api/auth/sign-in/email"
	signReq, _ := http.NewRequest("POST", signURL, bytes.NewReader(signBody))
	signReq.Header.Set("Authorization", "Bearer "+runner.environment["HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN"])
	signReq.Header.Set("Origin", runner.environment["HARDEN_LLM_WEB_PUBLIC_URL"])
	signReq.Header.Set("Content-Type", "application/json")
	signRes, err := (&http.Client{Timeout: 5 * time.Second}).Do(signReq)
	if err != nil {
		t.Fatal(err)
	}
	signRes.Body.Close()
	if signRes.StatusCode != 200 {
		t.Fatal("shared fixture sign-in failed")
	}
	for _, path := range []string{"/", "/profiles", "/embed/llm", "/traces/" + id} {
		code, _, body := getPage(path, true)
		if code != 200 {
			t.Fatalf("authenticated frontend %s = %d: %.200s", path, code, body)
		}
	}
	code, _, bundle := getPage("/profiles/bundle", true)
	if code != 200 || !bytes.Contains(bundle, []byte(profiles[a])) || bytes.Contains(bundle, []byte(profiles[b])) {
		t.Fatal("Phoenix key did not select the same private profiles")
	}
	identity.mu.Lock()
	identity.unavailable = true
	identity.mu.Unlock()
	code, _, _ = getPage("/", true)
	if code != 503 {
		t.Fatalf("identity outage status=%d, want 503", code)
	}
	requestJSON(t, client, "GET", origin+"/v1/models", nil, a, 200)
	identity.mu.Lock()
	identity.unavailable = false
	identity.mu.Unlock()

	sql("REVOKE INSERT ON history FROM harden_llm")
	notSaved := run(a, "recording fails once", "off")
	sql("GRANT INSERT ON history TO harden_llm")
	if object(t, notSaved["history"], "history failure")["status"] != "not_saved" {
		t.Fatal("definite rejected recording did not preserve outcome")
	}
	requestJSON(t, client, "DELETE", origin+"/api/v1/history/"+id, nil, a, 204)
	requestJSON(t, client, "GET", origin+"/api/v1/traces/"+id, nil, a, 404)
	cleared := requestJSON(t, client, "DELETE", origin+"/api/v1/history", nil, a, 200)
	if cleared["complete"] != true {
		t.Fatal("history clear incomplete")
	}
	requestJSON(t, client, "GET", origin+"/api/v1/history/"+text(t, foreign["runId"], "foreign run"), nil, b, 200)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runner.run(ctx, nil, "stop", "harden-postgres"); err != nil {
		t.Fatal(err)
	}
	requestJSON(t, client, "GET", origin+"/api/v1/history", nil, a, 503)
	ordinary := requestJSON(t, client, "POST", origin+"/v1/responses", map[string]any{"model": "smoke-model", "input": "storage outage", "harden": map[string]any{"cache": "off"}}, a, 200)
	if ordinary["output_text"] != "smoke-ok" {
		t.Fatal("storage outage coupled ordinary inference")
	}
	t.Log("TEST-433: two keys, shared frontend key, SQL/history/cache/artifact isolation, admission/storage outages and schema-before-start passed")
}
