//go:build integration

package gateway_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-025

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
)

func TestRunRoute(t *testing.T) {
	_, dsn := integrationtest.StartPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 13, 15, 0, 0, 0, time.UTC)
	if err := store.CreateUser(ctx, postgres.User{ID: "owner-a", Email: "a@example.test", PasswordHash: "$argon2id$v=19$fixture", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	vault, err := profiles.NewCredentialVault("key-2026", map[string][]byte{"key-2026": bytes.Repeat([]byte{0x66}, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	profileService, err := gateway.NewProfileService(gateway.ProfileServiceConfig{Store: store, Vault: vault, Prober: &testProfileProber{}, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	profile := loadGatewayFixtureProfile(t, "Backup")
	if _, err := profileService.Save(ctx, gateway.SaveProfileRequest{
		OwnerID: "owner-a", ProfileID: "Backup", Profile: profile, CredentialID: "credential-a",
		Credential: &profiles.CredentialPayload{APIKey: "run-route-provider-secret"},
	}); err != nil {
		t.Fatal(err)
	}

	identity := &fakeHTTPAuth{login: auth.LoginResult{Principal: auth.Principal{OwnerID: "owner-a", Email: "a@example.test", SessionID: "session-a", ExpiresAt: now.Add(time.Hour)}}}
	caller := &recordingRuntimeCaller{}
	nextID := 0
	runService, err := gateway.NewRunService(gateway.RunServiceConfig{
		Store: store, Profiles: profileService, Clock: func() time.Time { return now },
		NewID: func() (string, error) { nextID++; return fmt.Sprintf("run-%d", nextID), nil },
		CallerFactory: func(config gateway.RuntimeClientConfig) (gateway.RuntimeCaller, error) {
			if config.OwnerID != "owner-a" || config.Credentials == nil || config.Cache == nil {
				t.Fatalf("runtime client config = %#v", config)
			}
			return caller, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	api, err := httpapi.New(httpapi.Config{Auth: identity, Runs: runService})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	authorization := map[string][]string{"Authorization": {"Bearer valid-token"}}

	textBody := []byte(`{"profileId":"Backup","userPrompt":"say ok","callType":"text","cacheMode":"off","maxAttempts":1}`)
	response := apiRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/run", textBody, authorization)
	assertEnvelope(t, response, http.StatusOK, false)
	result := response.JSON["result"].(map[string]any)
	if result["output"] != "text-ok" || result["runId"] != "run-1" || result["traceId"] != "trace-1" || caller.calls != 1 {
		t.Fatalf("text run = %#v calls=%d", result, caller.calls)
	}
	if caller.last.Context.OrganizationID != "owner-a" || caller.last.Context.RunID != "run-1" || len(caller.last.Profiles) != 1 {
		t.Fatalf("root request = %#v", caller.last)
	}
	storedRun, err := store.Run(ctx, "owner-a", "run-1")
	if err != nil || storedRun.TraceID != "trace-1" || bytes.Contains(storedRun.Request, []byte("run-route-provider-secret")) {
		t.Fatalf("stored run = %#v, %v", storedRun, err)
	}
	trace, observations, err := store.Trace(ctx, "owner-a", "trace-1")
	if err != nil || trace.TraceID != "trace-1" || len(observations) != 1 {
		t.Fatalf("stored trace = %#v %#v, %v", trace, observations, err)
	}
	if _, err := store.Artifact(ctx, "owner-a", "trace-1", "call-1-trace"); err != nil {
		t.Fatalf("successful artifact reference was not indexed: %v", err)
	}
	if bytes.Contains(response.Body, []byte("llm-traces/")) {
		t.Fatalf("run response exposed artifact object key: %s", response.Body)
	}

	failureService, err := gateway.NewRunService(gateway.RunServiceConfig{
		Store: store, Profiles: profileService, Clock: func() time.Time { return now },
		NewID:         func() (string, error) { return "failure-run", nil },
		CallerFactory: func(gateway.RuntimeClientConfig) (gateway.RuntimeCaller, error) { return failureRuntimeCaller{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	failureOutput, failureState, callErr := failureService.Run(ctx, "owner-a", gateway.RunInput{
		ProfileID: "Backup", UserPrompt: "fail safely", CallType: hardenllm.CallTypeText, MaxAttempts: 1,
	})
	if callErr == nil || failureState.LastRunID != "failure-run" || failureState.LastTraceID != "trace-failure" {
		t.Fatalf("failure state lost runtime identity: %#v %v", failureState, callErr)
	}
	if failureOutput.CallID != "call-failure" || failureOutput.TraceID != "trace-failure" || len(failureOutput.Attempts) != 1 ||
		failureOutput.Usage.TotalTokens != 6 || len(failureOutput.Artifacts) != 2 {
		t.Fatalf("failure output lost runtime diagnostics: %#v", failureOutput)
	}
	failedRun, err := store.Run(ctx, "owner-a", "failure-run")
	if err != nil || failedRun.Status != "failed" || failedRun.TraceID != "trace-failure" {
		t.Fatalf("failed run was not persisted with runtime identity: %#v %v", failedRun, err)
	}
	var failedResult gateway.RunOutput
	if err := json.Unmarshal(failedRun.Result, &failedResult); err != nil || failedResult.CallID != "call-failure" ||
		failedResult.TraceID != "trace-failure" || len(failedResult.Attempts) != 1 || failedResult.Usage.TotalTokens != 6 ||
		len(failedResult.Artifacts) != 2 {
		t.Fatalf("failed run lost diagnostic result: %#v %v", failedResult, err)
	}
	for _, artifactID := range []string{"call-failure-trace", "call-failure-raw"} {
		if _, err := store.Artifact(ctx, "owner-a", "trace-failure", artifactID); err != nil {
			t.Fatalf("failed-run artifact %q was not indexed: %v", artifactID, err)
		}
	}

	structuredBody := []byte(`{"profileId":"Backup","userPrompt":"return JSON","callType":"structured","schema":{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}},"structuredRepair":true,"maxAttempts":2}`)
	response = apiRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/run", structuredBody, authorization)
	assertEnvelope(t, response, http.StatusOK, false)
	if response.JSON["result"].(map[string]any)["output"].(map[string]any)["ok"] != true || caller.calls != 2 {
		t.Fatalf("structured run = %#v calls=%d", response.JSON, caller.calls)
	}

	beforeInvalid := caller.calls
	response = apiRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/run", []byte(`{"profileId":"Backup","userPrompt":"","callType":"text"}`), authorization)
	assertEnvelope(t, response, http.StatusUnprocessableEntity, true)
	if caller.calls != beforeInvalid {
		t.Fatal("invalid run reached the root caller")
	}

	caller.invalidArtifact = true
	response = apiRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/run", textBody, authorization)
	assertEnvelope(t, response, http.StatusOK, false)
	if len(response.JSON["result"].(map[string]any)["artifacts"].([]any)) != 0 {
		t.Fatalf("failed artifact index changed provider success: %#v", response.JSON)
	}
	caller.invalidArtifact = false

	blocking := &blockingRuntimeCaller{canceled: make(chan struct{})}
	timeoutIDs := 0
	timeoutService, err := gateway.NewRunService(gateway.RunServiceConfig{
		Store: store, Profiles: profileService, Clock: time.Now,
		NewID:         func() (string, error) { timeoutIDs++; return fmt.Sprintf("timeout-%d", timeoutIDs), nil },
		CallerFactory: func(gateway.RuntimeClientConfig) (gateway.RuntimeCaller, error) { return blocking, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	timeoutAPI, err := httpapi.New(httpapi.Config{Auth: identity, Runs: timeoutService, MaxRunDuration: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	timeoutServer := httptest.NewServer(timeoutAPI.Handler())
	defer timeoutServer.Close()
	response = apiRequest(t, timeoutServer.Client(), http.MethodPost, timeoutServer.URL+"/api/v1/run", []byte(`{"profileId":"Backup","userPrompt":"wait","callType":"text","timeoutMs":10}`), authorization)
	assertEnvelope(t, response, http.StatusGatewayTimeout, true)
	if response.JSON["error"].(map[string]any)["code"] != "run_timeout" || blocking.calls != 1 {
		t.Fatalf("timeout response = %#v calls=%d", response.JSON, blocking.calls)
	}
	select {
	case <-blocking.canceled:
	default:
		t.Fatal("run timeout did not cancel root caller")
	}
	response = apiRequest(t, timeoutServer.Client(), http.MethodPost, timeoutServer.URL+"/api/v1/run", []byte(`{"profileId":"Backup","userPrompt":"wait","callType":"text","timeoutMs":51}`), authorization)
	assertEnvelope(t, response, http.StatusUnprocessableEntity, true)
	if blocking.calls != 1 {
		t.Fatal("timeout increase reached root caller")
	}
	if _, err := httpapi.New(httpapi.Config{Auth: identity, MaxRunDuration: 60*time.Second + time.Millisecond}); err == nil {
		t.Fatal("gateway accepted a run duration above the 60-second contract maximum")
	}

	privateProfile := profile
	privateProfile.LLMProfile = "Private"
	privateProfile.BaseURL = "https://127.0.0.1/v1"
	if _, err := profileService.Save(ctx, gateway.SaveProfileRequest{
		OwnerID: "owner-a", ProfileID: "Private", Profile: privateProfile, CredentialID: "credential-private",
		Credential: &profiles.CredentialPayload{APIKey: "private-provider-secret"},
	}); err != nil {
		t.Fatal(err)
	}
	dials := 0
	realRunService, err := gateway.NewRunService(gateway.RunServiceConfig{
		Store: store, Profiles: profileService,
		CallerFactory: func(config gateway.RuntimeClientConfig) (gateway.RuntimeCaller, error) {
			return hardenllm.New(hardenllm.Options{
				Credentials: config.Credentials, Cache: config.Cache, Artifacts: config.Artifacts,
				EndpointPolicy: hardenllm.EndpointPolicy{DialContext: func(context.Context, string, string) (net.Conn, error) {
					dials++
					return nil, errors.New("unexpected dial")
				}},
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	realAPI, err := httpapi.New(httpapi.Config{Auth: identity, Runs: realRunService, MaxRunDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	realServer := httptest.NewServer(realAPI.Handler())
	defer realServer.Close()
	response = apiRequest(t, realServer.Client(), http.MethodPost, realServer.URL+"/api/v1/run", []byte(`{"profileId":"Private","userPrompt":"must not dial","callType":"text","maxAttempts":1}`), authorization)
	assertEnvelope(t, response, http.StatusBadGateway, true)
	if dials != 0 {
		t.Fatalf("unsafe endpoint reached provider dial %d times", dials)
	}

	assertHandlerRuntimeBoundary(t)
}

type recordingRuntimeCaller struct {
	calls           int
	last            hardenllm.Request
	invalidArtifact bool
}

func (caller *recordingRuntimeCaller) Call(_ context.Context, request hardenllm.Request) (hardenllm.Result, error) {
	caller.calls++
	caller.last = request
	callID := fmt.Sprintf("call-%d", caller.calls)
	traceID := fmt.Sprintf("trace-%d", caller.calls)
	output := any("text-ok")
	if request.CallType == hardenllm.CallTypeStructured {
		output = map[string]any{"ok": true}
	}
	digest := strings.Repeat("a", 64)
	if caller.invalidArtifact {
		digest = "invalid"
	}
	return hardenllm.Result{
		Output: output, CallID: callID, TraceID: traceID,
		Usage:    hardenllm.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3},
		Cost:     hardenllm.Cost{Known: true, TotalUSD: 0.001, Source: "profile"},
		Attempts: []hardenllm.Attempt{{Number: 1, ProfileID: request.ProfileID, ProviderUsed: true}},
		Cache:    hardenllm.CacheResult{Mode: request.CacheMode, Status: "disabled"},
		Artifacts: []hardenllm.ArtifactRef{{
			Key:    fmt.Sprintf("llm-traces/%s/%s/%s/%s-trace.json", request.Context.OrganizationID, request.Context.TaskID, traceID, callID),
			SHA256: digest, SizeBytes: 15, ContentType: "application/json",
		}},
	}, nil
}

type blockingRuntimeCaller struct {
	calls    int
	canceled chan struct{}
}

type failureRuntimeCaller struct{}

func (failureRuntimeCaller) Call(_ context.Context, request hardenllm.Request) (hardenllm.Result, error) {
	digest := strings.Repeat("b", 64)
	prefix := fmt.Sprintf("llm-traces/%s/%s/trace-failure/call-failure", request.Context.OrganizationID, request.Context.TaskID)
	return hardenllm.Result{
		CallID: "call-failure", TraceID: "trace-failure",
		Usage:    hardenllm.Usage{InputTokens: 4, OutputTokens: 2, TotalTokens: 6},
		Cost:     hardenllm.Cost{TotalUSD: 0.000006, Known: true, Source: "profile"},
		Attempts: []hardenllm.Attempt{{Number: 1, ProfileID: request.ProfileID, Category: "parse", ProviderUsed: true}},
		Artifacts: []hardenllm.ArtifactRef{
			{Key: prefix + "-trace.json", SHA256: digest, SizeBytes: 64, ContentType: "application/json"},
			{Key: prefix + "-raw.json", SHA256: digest, SizeBytes: 32, ContentType: "application/json"},
		},
	}, errors.New("fixture provider failure")
}

func (caller *blockingRuntimeCaller) Call(ctx context.Context, _ hardenllm.Request) (hardenllm.Result, error) {
	caller.calls++
	<-ctx.Done()
	close(caller.canceled)
	return hardenllm.Result{}, ctx.Err()
}

func assertHandlerRuntimeBoundary(t *testing.T) {
	t.Helper()
	contents, err := os.ReadFile("httpapi/resources.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, forbidden := range []string{"/internal/providers", "/internal/retry", "/internal/schema", "/internal/cachekey", "chat/completions", "generateContent", "ratePerToken"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("HTTP handler contains runtime implementation detail %q", forbidden)
		}
	}
}
