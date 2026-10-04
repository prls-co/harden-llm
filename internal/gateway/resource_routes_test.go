//go:build integration

package gateway_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-011 TEST-022 TEST-024 TEST-053 TEST-230 TEST-231
// PLAN-HLLM-WIDGET-PARITY-001 TEST-108

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/artifacts"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
	"github.com/prls-co/prls-control-plane/go/access"
)

const (
	userAID    = "User_Alpha"
	userBID    = "User_Beta"
	serviceKey = "harden-llm-test-service-token-0123456789"
)

func TestResourceRoutes(t *testing.T) {
	_, dsn := integrationtest.PostgresLease(t)
	_, garageFixture := integrationtest.GarageLease(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 13, 14, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	identity := loginIdentityFixture(t)
	vault, err := profiles.NewCredentialVault("key-2026", map[string][]byte{"key-2026": bytes.Repeat([]byte{0x55}, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	probe := &testProfileProber{}
	profileService, err := gateway.NewProfileService(gateway.ProfileServiceConfig{Store: store, Vault: vault, Prober: probe, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	modelRefresher := &testModelRefresher{models: []profiles.Model{{ID: "model-b"}, {ID: "model-a", Label: "Model A"}}}
	garageStore, err := artifacts.NewGarage(artifacts.Config{
		Endpoint: garageFixture.Endpoint, ExternalEndpoint: garageFixture.Endpoint,
		Bucket: garageFixture.Bucket, Region: garageFixture.Region,
		AccessKeyID: garageFixture.AccessKeyID, SecretAccessKey: garageFixture.SecretAccessKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactCoordinator, err := gateway.NewArtifactCoordinator(gateway.ArtifactCoordinatorConfig{
		Store: store, Clock: clock,
		Scope: func(ownerID string) (gateway.ArtifactObjectAccess, error) {
			return garageStore.Scoped(garageFixture.Scope("llm-traces/" + ownerID + "/"))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resourceService, err := gateway.NewResourceService(gateway.ResourceServiceConfig{
		Store: store, Profiles: profileService, ModelRefresher: modelRefresher, Clock: clock,
		NewID:     func() (string, error) { return "bundle-1", nil },
		Artifacts: artifactCoordinator,
	})
	if err != nil {
		t.Fatal(err)
	}
	api, err := httpapi.New(httpapi.Config{Auth: identity, Resources: resourceService})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	authA := map[string][]string{"Authorization": {"Bearer " + serviceKey}, "X-PRLS-Session-Reference": {"session-a"}}
	authB := map[string][]string{"Authorization": {"Bearer " + serviceKey}, "X-PRLS-Session-Reference": {"session-b"}}

	secondSessionA := map[string][]string{"Authorization": {"Bearer " + serviceKey}, "X-PRLS-Session-Reference": {"session-a2"}}
	machine := map[string][]string{"Authorization": {"Bearer " + serviceKey}}

	stateBody := []byte(`{"schemaVersion":2,"selectedProfileId":"Backup","modelId":"gpt-backup","userPrompt":"draft","callType":"text","cacheMode":"off","recoveryPolicy":{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}}`)
	response := apiRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/state", stateBody, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	if response.JSON["state"].(map[string]any)["userPrompt"] != "draft" {
		t.Fatalf("saved state = %#v", response.JSON)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/state", nil, authA)
	if response.JSON["state"].(map[string]any)["selectedProfileId"] != "Backup" {
		t.Fatalf("loaded state = %#v", response.JSON)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/state", nil, authB)
	if response.JSON["state"].(map[string]any)["userPrompt"] != nil {
		t.Fatalf("cross-owner state leaked: %#v", response.JSON)
	}

	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/state", nil, secondSessionA)
	if response.JSON["state"].(map[string]any)["userPrompt"] != "draft" {
		t.Fatal("second session lost same-user state")
	}
	stateB := bytes.Replace(stateBody, []byte("draft"), []byte("private-b"), 1)
	assertEnvelope(t, apiRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/state", stateB, machine), http.StatusOK, false)
	for _, headers := range []map[string][]string{authB, machine} {
		response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/state", nil, headers)
		if response.JSON["state"].(map[string]any)["userPrompt"] != "private-b" {
			t.Fatal("token and verification login have different state")
		}
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/state", nil, authA)
	if response.JSON["state"].(map[string]any)["userPrompt"] != "draft" {
		t.Fatal("token changed another user's state")
	}

	profile := loadGatewayFixtureProfile(t, "Backup")
	profileRequest, _ := json.Marshal(map[string]any{
		"profile": profile, "credentialId": "credential-a",
		"credential": map[string]any{"apiKey": "resource-route-provider-secret"},
	})
	response = apiRequest(t, server.Client(), http.MethodPut, server.URL+"/api/v1/profiles/Backup", profileRequest, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	if bytes.Contains(response.Body, []byte("resource-route-provider-secret")) || bytes.Contains(response.Body, []byte("ciphertext")) {
		t.Fatalf("profile response exposed credential: %s", response.Body)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/profiles", nil, authA)
	profilesResult := response.JSON["result"].(map[string]any)["profiles"].([]any)
	// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-208
	defaults := response.JSON["result"].(map[string]any)["defaults"].(map[string]any)
	wantDefaults, _ := json.Marshal(hardenllm.DefaultStructuredRecoveryPolicy())
	gotDefaults, _ := json.Marshal(defaults["recoveryPolicy"])
	var gotPolicy, wantPolicy any
	_ = json.Unmarshal(gotDefaults, &gotPolicy)
	_ = json.Unmarshal(wantDefaults, &wantPolicy)
	if !reflect.DeepEqual(gotPolicy, wantPolicy) {
		t.Fatalf("profiles defaults = %s, want %s", gotDefaults, wantDefaults)
	}
	if len(profilesResult) != 1 || bytes.Contains(response.Body, []byte("ciphertext")) {
		t.Fatalf("profile list = %s", response.Body)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/profiles", nil, authB)
	if len(response.JSON["result"].(map[string]any)["profiles"].([]any)) != 0 {
		t.Fatalf("cross-owner profiles leaked: %s", response.Body)
	}

	response = apiRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/profiles/Backup/models:refresh", nil, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	models := response.JSON["result"].(map[string]any)["profile"].(map[string]any)["models"].([]any)
	if len(models) != 2 || models[0].(map[string]any)["id"] != "model-a" {
		t.Fatalf("normalized models = %#v", models)
	}
	beforeFailure, _ := store.Profile(ctx, userAID, "Backup")
	modelRefresher.err = errors.New("provider unavailable")
	response = apiRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/profiles/Backup/models:refresh", nil, authA)
	assertEnvelope(t, response, http.StatusServiceUnavailable, true)
	afterFailure, _ := store.Profile(ctx, userAID, "Backup")
	if !bytes.Equal(beforeFailure.Document, afterFailure.Document) {
		t.Fatal("failed model refresh replaced prior model list")
	}
	modelRefresher.err = nil

	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/profiles/bundle", nil, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	if bytes.Contains(response.Body, []byte("resource-route-provider-secret")) || response.JSON["result"].(map[string]any)["bundleId"] != "bundle-1" {
		t.Fatalf("bundle export = %s", response.Body)
	}
	bundleBytes, _ := json.Marshal(response.JSON["result"])
	var bundle gateway.ProfileBundle
	if err := json.Unmarshal(bundleBytes, &bundle); err != nil {
		t.Fatal(err)
	}
	invalidBundle := bundle
	invalidBundle.Profiles = cloneProfileCatalog(bundle.Profiles)
	invalidProfile := invalidBundle.Profiles["Backup"]
	invalidProfile.RecoveryPolicy.MaxAttempts = 0
	invalidBundle.Profiles["Backup"] = invalidProfile
	invalidBytes, _ := json.Marshal(invalidBundle)
	response = apiRequest(t, server.Client(), http.MethodPut, server.URL+"/api/v1/profiles/bundle", invalidBytes, authA)
	assertEnvelope(t, response, http.StatusUnprocessableEntity, true)
	unchanged, _ := store.Profile(ctx, userAID, "Backup")
	if !bytes.Equal(unchanged.Document, beforeFailure.Document) {
		t.Fatal("invalid bundle partially replaced prior profiles")
	}
	validBytes, _ := json.Marshal(bundle)
	probesBefore := probe.calls.Load()
	response = apiRequest(t, server.Client(), http.MethodPut, server.URL+"/api/v1/profiles/bundle", validBytes, authB)
	assertEnvelope(t, response, http.StatusUnprocessableEntity, true)
	if probe.calls.Load() != probesBefore {
		t.Fatal("foreign credential bundle reached provider probe")
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/profiles", nil, authB)
	if len(response.JSON["result"].(map[string]any)["profiles"].([]any)) != 0 {
		t.Fatal("foreign bundle wrote another user's profiles")
	}
	profileB := profile
	profileB.ModelID = "private-b-model"
	requestB, _ := json.Marshal(map[string]any{"profile": profileB, "credentialId": "credential-a", "credential": map[string]any{"apiKey": "private-b-provider-secret"}})
	assertEnvelope(t, apiRequest(t, server.Client(), http.MethodPut, server.URL+"/api/v1/profiles/Backup", requestB, authB), http.StatusOK, false)

	oldBundle := bundle
	oldBundle.SchemaVersion = 1
	oldBytes, _ := json.Marshal(oldBundle)
	response = apiRequest(t, server.Client(), http.MethodPut, server.URL+"/api/v1/profiles/bundle", oldBytes, authA)
	assertEnvelope(t, response, http.StatusUnprocessableEntity, true)
	if !bytes.Contains(response.Body, []byte(`"schemaVersion"`)) {
		t.Fatalf("old bundle format error lacks field identity: %s", response.Body)
	}
	response = apiRequest(t, server.Client(), http.MethodPut, server.URL+"/api/v1/profiles/bundle", validBytes, authA)
	assertEnvelope(t, response, http.StatusOK, false)

	seedResourceHistory(t, ctx, store, now)
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/history?limit=2", nil, authA)
	page := response.JSON["result"].(map[string]any)
	items := page["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["runId"] != "run-c" || page["nextCursor"] == "" {
		t.Fatalf("first history page = %#v", page)
	}
	cursor := url.QueryEscape(page["nextCursor"].(string))
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/history?limit=2&cursor="+cursor, nil, authA)
	items = response.JSON["result"].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["runId"] != "run-a" {
		t.Fatalf("second history page = %#v", response.JSON)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/history?page=2&limit=1", nil, authA)
	numbered := response.JSON["result"].(map[string]any)
	numberedItems := numbered["items"].([]any)
	pagination := numbered["pagination"].(map[string]any)
	if len(numberedItems) != 1 || numberedItems[0].(map[string]any)["runId"] != "run-b" ||
		pagination["page"] != float64(2) || pagination["pageSize"] != float64(1) || pagination["totalCount"] != float64(3) {
		t.Fatalf("numbered middle history page = %#v", response.JSON)
	}
	if _, exists := numbered["nextCursor"]; exists {
		t.Fatalf("numbered history response leaked cursor metadata: %#v", numbered)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/history?page=999&limit=1", nil, authA)
	pagination = response.JSON["result"].(map[string]any)["pagination"].(map[string]any)
	items = response.JSON["result"].(map[string]any)["items"].([]any)
	if pagination["page"] != float64(3) || len(items) != 1 || items[0].(map[string]any)["runId"] != "run-a" {
		t.Fatalf("numbered clamped history page = %#v", response.JSON)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/history?page=1&limit=10", nil, authB)
	numbered = response.JSON["result"].(map[string]any)
	pagination = numbered["pagination"].(map[string]any)
	if len(numbered["items"].([]any)) != 0 || pagination["page"] != float64(1) || pagination["totalCount"] != float64(0) {
		t.Fatalf("cross-owner numbered history = %#v", response.JSON)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/stats", nil, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	stats := response.JSON["result"].(map[string]any)
	if stats["totalCount"] != float64(3) || stats["successCount"] != float64(3) || stats["totalCallDurationMs"] != float64(0) {
		t.Fatalf("owner stats = %#v", stats)
	}

	ownerStore, err := garageStore.Scoped(garageFixture.Scope("llm-traces/" + userAID + "/"))
	if err != nil {
		t.Fatal(err)
	}
	objectKey := garageFixture.Key("llm-traces/" + userAID + "/run-a/trace-a/artifact-a-trace.json")
	reference, err := ownerStore.Put(ctx, objectKey, []byte(`{"safe":true}`), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SeedArtifactMetadataForTest(ctx, postgres.ArtifactRecord{
		OwnerID: userAID, RunID: "run-a", TraceID: "trace-a", ID: "artifact-a", Kind: "trace", ObjectKey: objectKey,
		ContentType: reference.ContentType, SHA256: reference.SHA256, SizeBytes: reference.SizeBytes,
		State: "available", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/traces/trace-a", nil, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	if bytes.Contains(response.Body, []byte(objectKey)) || len(response.JSON["result"].(map[string]any)["artifacts"].([]any)) != 1 {
		t.Fatalf("trace response exposed storage key: %s", response.Body)
	}
	traceResult := response.JSON["result"].(map[string]any)
	traceResources := traceResult["resources"].(map[string]any)
	requestResource := traceResources["request"].(map[string]any)
	responseResource := traceResources["response"].(map[string]any)
	if requestResource["available"] != true || requestResource["payload"].(map[string]any)["profileId"] != "Backup" {
		t.Fatalf("trace request resource = %#v", requestResource)
	}
	if responseResource["available"] != true || responseResource["payload"].(map[string]any)["output"] != "ok" {
		t.Fatalf("trace response resource = %#v", responseResource)
	}
	if bytes.Contains(response.Body, []byte("run-route-provider-secret")) {
		t.Fatalf("trace response exposed provider secret: %s", response.Body)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/traces/trace-a", nil, authB)
	assertEnvelope(t, response, http.StatusNotFound, true)

	noRedirect := *server.Client()
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	redirectRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/traces/trace-a/artifacts/artifact-a", nil)
	redirectRequest.Header.Set("Authorization", "Bearer "+serviceKey)
	redirectRequest.Header.Set("X-PRLS-Session-Reference", "session-a")
	redirect, err := noRedirect.Do(redirectRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = redirect.Body.Close()
	location := redirect.Header.Get("Location")
	parsedLocation, parseErr := url.Parse(location)
	if redirect.StatusCode != http.StatusSeeOther || parseErr != nil || parsedLocation.Host == "" || parsedLocation.Query().Get("X-Amz-Expires") == "" {
		t.Fatalf("artifact redirect = %d %q", redirect.StatusCode, location)
	}
	redirectRequest, _ = http.NewRequest(http.MethodGet, server.URL+"/api/v1/traces/trace-a/artifacts/artifact-a", nil)
	redirectRequest.Header.Set("Authorization", "Bearer "+serviceKey)
	redirectRequest.Header.Set("X-PRLS-Session-Reference", "session-b")
	redirect, err = noRedirect.Do(redirectRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer redirect.Body.Close()
	if redirect.StatusCode != http.StatusNotFound || redirect.Header.Get("Location") != "" {
		t.Fatalf("cross-owner artifact redirect = %d %q", redirect.StatusCode, redirect.Header.Get("Location"))
	}

	if err := store.SaveExecution(ctx, postgres.RunRecord{
		OwnerID: userBID, ID: "run-a", ProfileID: "Backup", TraceID: "trace-a", Status: "succeeded",
		Request: json.RawMessage(`{"profileId":"Backup"}`), Result: json.RawMessage(`{"output":"private-b"}`), StartedAt: now, CompletedAt: now,
	}, postgres.TraceRecord{OwnerID: userBID, TraceID: "trace-a", RunID: "run-a", Record: json.RawMessage(`{"status":"succeeded"}`), CreatedAt: now, UpdatedAt: now}, nil, nil); err != nil {
		t.Fatal(err)
	}
	bObjects, err := garageStore.Scoped(garageFixture.Scope("llm-traces/" + userBID + "/"))
	if err != nil {
		t.Fatal(err)
	}
	bKey := garageFixture.Key("llm-traces/" + userBID + "/run-a/trace-a/artifact-a-trace.json")
	bRef, err := bObjects.Put(ctx, bKey, []byte(`{"private":"b"}`), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SeedArtifactMetadataForTest(ctx, postgres.ArtifactRecord{
		OwnerID: userBID, RunID: "run-a", TraceID: "trace-a", ID: "artifact-a", Kind: "trace", ObjectKey: bKey,
		ContentType: bRef.ContentType, SHA256: bRef.SHA256, SizeBytes: bRef.SizeBytes, State: "available", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.SaveExecution(ctx, postgres.RunRecord{
		OwnerID: userAID, ID: "run-delete-failure", ProfileID: "Backup", TraceID: "trace-delete-failure", Status: "failed",
		Request: json.RawMessage(`{"profileId":"Backup"}`), Result: json.RawMessage(`{"output":null}`), StartedAt: now, CompletedAt: now,
	}, postgres.TraceRecord{
		OwnerID: userAID, TraceID: "trace-delete-failure", RunID: "run-delete-failure", Record: json.RawMessage(`{"status":"failed"}`), CreatedAt: now, UpdatedAt: now,
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	failureObjectKey := garageFixture.Key("llm-traces/" + userAID + "/run-delete-failure/trace-delete-failure/artifact-delete-failure-trace.json")
	if err := store.SeedArtifactMetadataForTest(ctx, postgres.ArtifactRecord{
		OwnerID: userAID, RunID: "run-delete-failure", TraceID: "trace-delete-failure", ID: "artifact-delete-failure", Kind: "trace", ObjectKey: failureObjectKey,
		ContentType: "application/json", SHA256: strings.Repeat("b", 64), SizeBytes: 1,
		State: "available", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	failingService, err := gateway.NewResourceService(gateway.ResourceServiceConfig{
		Store: store, Profiles: profileService,
		Artifacts: failingArtifactLifecycle{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := failingService.DeleteHistory(ctx, userAID, "run-delete-failure"); err == nil {
		t.Fatal("artifact deletion failure did not fail closed")
	}
	if _, err := store.Run(ctx, userAID, "run-delete-failure"); err != nil {
		t.Fatalf("artifact deletion failure removed run metadata: %v", err)
	}
	if _, _, err := store.Trace(ctx, userAID, "trace-delete-failure"); err != nil {
		t.Fatalf("artifact deletion failure removed trace metadata: %v", err)
	}

	response = apiRequest(t, server.Client(), http.MethodDelete, server.URL+"/api/v1/history/run-a", nil, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	if _, err := store.Run(ctx, userAID, "run-a"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("history record was not deleted: %v", err)
	}
	if _, _, err := store.Trace(ctx, userAID, "trace-a"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("trace metadata was not deleted: %v", err)
	}
	if _, err := store.Artifact(ctx, userAID, "trace-a", "artifact-a"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("artifact metadata was not deleted: %v", err)
	}
	if _, _, err := ownerStore.Get(ctx, objectKey); !artifacts.IsKind(err, artifacts.KindNotFound) {
		t.Fatalf("artifact object body was not deleted: %v", err)
	}
	response = apiRequest(t, server.Client(), http.MethodDelete, server.URL+"/api/v1/history", nil, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	if _, _, err := store.Trace(ctx, userAID, "trace-orphan"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("orphan trace was not cleared: %v", err)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/stats", nil, authA)
	if response.JSON["result"].(map[string]any)["totalCount"] != float64(0) {
		t.Fatalf("cleared owner stats = %#v", response.JSON)
	}
	response = apiRequest(t, server.Client(), http.MethodDelete, server.URL+"/api/v1/profiles/Backup", nil, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/profiles", nil, authB)
	remaining := response.JSON["result"].(map[string]any)["profiles"].([]any)
	if len(remaining) != 1 || remaining[0].(map[string]any)["profile"].(map[string]any)["modelId"] != "private-b-model" {
		t.Fatal("profile deletion changed another user's same-ID profile")
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/stats", nil, authB)
	if response.JSON["result"].(map[string]any)["totalCount"] != float64(1) {
		t.Fatal("history deletion changed another user's statistics")
	}
	if _, _, err := store.Trace(ctx, userBID, "trace-a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bObjects.Get(ctx, bKey); err != nil {
		t.Fatalf("deletion removed another user's artifact: %v", err)
	}
}

type testProfileProber struct {
	err   error
	calls atomic.Int32
}

func (prober *testProfileProber) Probe(context.Context, profiles.Profile, profiles.CredentialPayload) error {
	prober.calls.Add(1)
	return prober.err
}

type testModelRefresher struct {
	models []profiles.Model
	err    error
}

type failingArtifactLifecycle struct{}

func (failingArtifactLifecycle) PresignGet(context.Context, string, string, time.Duration) (string, error) {
	return "", errors.New("presign unavailable")
}

func (failingArtifactLifecycle) DeleteExecution(context.Context, string, string, string) error {
	return errors.New("delete unavailable")
}

func (failingArtifactLifecycle) ClearOwner(context.Context, string) (int64, error) {
	return 0, errors.New("delete unavailable")
}

func (refresher *testModelRefresher) RefreshModels(context.Context, profiles.Profile, profiles.CredentialPayload) ([]profiles.Model, error) {
	return append([]profiles.Model(nil), refresher.models...), refresher.err
}

func apiRequest(t *testing.T, client *http.Client, method, target string, body []byte, headers map[string][]string) recordedResponse {
	t.Helper()
	cloned := make(map[string][]string, len(headers)+1)
	for name, values := range headers {
		cloned[name] = append([]string(nil), values...)
	}
	if body != nil {
		cloned["Content-Type"] = []string{"application/json"}
	}
	return request(t, client, method, target, body, cloned)
}

func loadGatewayFixtureProfile(t *testing.T, name string) profiles.Profile {
	t.Helper()
	contents, err := os.ReadFile("../../fixtures/contracts/profile-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(contents, &fixture); err != nil {
		t.Fatal(err)
	}
	catalog, err := profiles.ParseCatalog(fixture.Input)
	if err != nil {
		t.Fatal(err)
	}
	return catalog[name]
}

func cloneProfileCatalog(input profiles.Catalog) profiles.Catalog {
	encoded, _ := json.Marshal(input)
	var result profiles.Catalog
	_ = json.Unmarshal(encoded, &result)
	return result
}

func seedResourceHistory(t *testing.T, ctx context.Context, store *postgres.Store, now time.Time) {
	t.Helper()
	for _, id := range []string{"run-a", "run-b", "run-c"} {
		traceID := strings.Replace(id, "run", "trace", 1)
		var observations []postgres.ObservationRecord
		if id == "run-a" {
			observations = []postgres.ObservationRecord{{OwnerID: userAID, TraceID: traceID, Sequence: 0, Type: "provider.attempt", Data: json.RawMessage(`{"number":1}`), CreatedAt: now}}
		}
		if err := store.SaveExecution(ctx, postgres.RunRecord{
			OwnerID: userAID, ID: id, ProfileID: "Backup", TraceID: traceID, Status: "succeeded",
			Request: json.RawMessage(`{"profileId":"Backup"}`), Result: json.RawMessage(`{"output":"ok"}`), StartedAt: now, CompletedAt: now,
		}, postgres.TraceRecord{
			OwnerID: userAID, TraceID: traceID, RunID: id, Record: json.RawMessage(`{"status":"success"}`), CreatedAt: now, UpdatedAt: now,
		}, observations, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// Real authentication is shared by the resource and runtime storage boundaries.
func loginIdentityFixture(t *testing.T) *auth.Service {
	t.Helper()
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/internal/v1/access-context" || r.Header.Get("Authorization") != "Bearer control-plane-internal-token" {
			http.Error(w, "unexpected identity request", 400)
			return
		}
		userID := map[string]string{"session-a": userAID, "session-a2": userAID, "session-b": userBID}[r.Header.Get("X-PRLS-Session-Reference")]
		if userID == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"user_id": userID, "email": "user@example.test", "name": "User", "role": "member", "session_ref": r.Header.Get("X-PRLS-Session-Reference"), "account": map[string]any{"account_id": "11111111-1111-4111-8111-111111111111", "name": "Same Company", "products": []string{"knowledge"}}})
	}))
	t.Cleanup(controlPlane.Close)
	client, err := access.New(controlPlane.URL, "control-plane-internal-token")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewService(auth.Config{ControlPlane: client, ServiceToken: serviceKey, StaticUserID: userBID})
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-011 TEST-022 TEST-024
func TestLoginOwnedRuntimeCacheAndHistory(t *testing.T) {
	_, dsn := integrationtest.PostgresLease(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	calls := atomic.Int32{}
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer local-provider-secret" {
			http.Error(w, "unexpected provider request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output_text":"local-provider-ok","usage":{"input_tokens":2,"output_tokens":3}}`))
	}))
	defer provider.Close()
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	vault, err := profiles.NewCredentialVault("test", map[string][]byte{"test": bytes.Repeat([]byte{0x44}, 32)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	profileService, err := gateway.NewProfileService(gateway.ProfileServiceConfig{Store: store, Vault: vault, Prober: &testProfileProber{}})
	if err != nil {
		t.Fatal(err)
	}
	profile := loadGatewayFixtureProfile(t, "Backup")
	profile.LLMProfile = "Local"
	profile.BaseURL = provider.URL + "/v1"
	for _, user := range []string{userAID, userBID} {
		if _, err := profileService.Save(ctx, gateway.SaveProfileRequest{OwnerID: user, ProfileID: "Local", Profile: profile, CredentialID: "same-id", Credential: &profiles.CredentialPayload{APIKey: "local-provider-secret"}}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := gateway.NewRunService(gateway.RunServiceConfig{Store: store, Profiles: profileService, CallerFactory: func(c gateway.RuntimeClientConfig) (gateway.RuntimeCaller, error) {
		return hardenllm.New(hardenllm.Options{Credentials: c.Credentials, Cache: c.Cache, EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}})
	}})
	if err != nil {
		t.Fatal(err)
	}
	resources, err := gateway.NewResourceService(gateway.ResourceServiceConfig{Store: store, Profiles: profileService})
	if err != nil {
		t.Fatal(err)
	}
	api, err := httpapi.New(httpapi.Config{Auth: loginIdentityFixture(t), Runs: runs, Resources: resources})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	body := []byte(`{"profileId":"Local","userPrompt":"same request","callType":"text","cacheMode":"cache","recoveryPolicy":{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}}`)
	for i, reference := range []string{"session-a", "session-b", "session-a2", ""} {
		headers := map[string][]string{"Authorization": {"Bearer " + serviceKey}}
		if reference != "" {
			headers["X-PRLS-Session-Reference"] = []string{reference}
		}
		response := apiRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/run", body, headers)
		assertEnvelope(t, response, http.StatusOK, false)
		result := response.JSON["result"].(map[string]any)
		if result["cache"].(map[string]any)["served"] != (i >= 2) || result["output"] != "local-provider-ok" {
			t.Fatalf("run %d cache/output = %#v", i, result)
		}
		if calls.Load() != int32(min(i+1, 2)) {
			t.Fatalf("run %d provider calls = %d", i, calls.Load())
		}
		other := userBID
		if reference == "session-b" || reference == "" {
			other = userAID
		}
		if _, err := store.Run(ctx, other, result["runId"].(string)); !errors.Is(err, postgres.ErrNotFound) {
			t.Fatalf("run crosses users: %v", err)
		}
		if _, _, err := store.Trace(ctx, other, result["traceId"].(string)); !errors.Is(err, postgres.ErrNotFound) {
			t.Fatalf("trace crosses users: %v", err)
		}
	}
	for _, user := range []string{userAID, userBID} {
		reference := "session-a"
		if user == userBID {
			reference = "session-b"
		}
		r := apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/history?page=1&limit=10", nil, map[string][]string{"Authorization": {"Bearer " + serviceKey}, "X-PRLS-Session-Reference": {reference}})
		assertEnvelope(t, r, http.StatusOK, false)
		if len(r.JSON["result"].(map[string]any)["items"].([]any)) != 2 {
			t.Fatal("history includes another user's runs")
		}
	}
}
