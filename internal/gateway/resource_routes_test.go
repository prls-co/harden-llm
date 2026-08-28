//go:build integration

package gateway_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-024 TEST-053
// PLAN-HLLM-WIDGET-PARITY-001 TEST-108

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/artifacts"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/gateway/auth"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
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
	identity, err := auth.NewService(auth.Config{Store: store, SessionTTL: time.Hour, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []struct{ id, email, password string }{
		{"owner-a", "a@example.test", "correct horse battery staple A"},
		{"owner-b", "b@example.test", "correct horse battery staple B"},
	} {
		if _, err := identity.BootstrapUser(ctx, user.id, user.email, user.password); err != nil {
			t.Fatal(err)
		}
	}
	loginA, err := identity.Login(ctx, "a@example.test", "correct horse battery staple A")
	if err != nil {
		t.Fatal(err)
	}
	loginB, err := identity.Login(ctx, "b@example.test", "correct horse battery staple B")
	if err != nil {
		t.Fatal(err)
	}
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
	resourceService, err := gateway.NewResourceService(gateway.ResourceServiceConfig{
		Store: store, Profiles: profileService, ModelRefresher: modelRefresher, Clock: clock,
		NewID: func() (string, error) { return "bundle-1", nil },
		ArtifactScope: func(ownerID string) (gateway.ArtifactAccess, error) {
			return garageStore.Scoped(garageFixture.Scope("llm-traces/" + ownerID + "/"))
		},
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
	authA := map[string][]string{"Authorization": {"Bearer " + loginA.AccessToken}}
	authB := map[string][]string{"Authorization": {"Bearer " + loginB.AccessToken}}

	stateBody := []byte(`{"schemaVersion":1,"selectedProfileId":"Backup","modelId":"gpt-backup","userPrompt":"draft","callType":"text","structuredRepair":false,"cacheMode":"off"}`)
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
	beforeFailure, _ := store.Profile(ctx, "owner-a", "Backup")
	modelRefresher.err = errors.New("provider unavailable")
	response = apiRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/profiles/Backup/models:refresh", nil, authA)
	assertEnvelope(t, response, http.StatusServiceUnavailable, true)
	afterFailure, _ := store.Profile(ctx, "owner-a", "Backup")
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
	invalidProfile.BackupProfiles = []string{"Missing"}
	invalidBundle.Profiles["Backup"] = invalidProfile
	invalidBytes, _ := json.Marshal(invalidBundle)
	response = apiRequest(t, server.Client(), http.MethodPut, server.URL+"/api/v1/profiles/bundle", invalidBytes, authA)
	assertEnvelope(t, response, http.StatusUnprocessableEntity, true)
	unchanged, _ := store.Profile(ctx, "owner-a", "Backup")
	if !bytes.Equal(unchanged.Document, beforeFailure.Document) {
		t.Fatal("invalid bundle partially replaced prior profiles")
	}
	validBytes, _ := json.Marshal(bundle)
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
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/stats", nil, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	stats := response.JSON["result"].(map[string]any)
	if stats["totalCount"] != float64(3) || stats["successCount"] != float64(3) || stats["totalCallDurationMs"] != float64(0) {
		t.Fatalf("owner stats = %#v", stats)
	}

	ownerStore, err := garageStore.Scoped(garageFixture.Scope("llm-traces/owner-a/"))
	if err != nil {
		t.Fatal(err)
	}
	objectKey := garageFixture.Key("llm-traces/owner-a/run-a/trace-a/artifact-a-trace.json")
	reference, err := ownerStore.Put(ctx, objectKey, []byte(`{"safe":true}`), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveArtifact(ctx, postgres.ArtifactRecord{
		OwnerID: "owner-a", TraceID: "trace-a", ID: "artifact-a", Kind: "trace", ObjectKey: objectKey,
		ContentType: reference.ContentType, SHA256: reference.SHA256, SizeBytes: reference.SizeBytes,
		Available: true, CreatedAt: now, UpdatedAt: now,
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
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/traces/trace-orphan", nil, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	orphanResources := response.JSON["result"].(map[string]any)["resources"].(map[string]any)
	if orphanResources["request"].(map[string]any)["available"] != false ||
		orphanResources["response"].(map[string]any)["available"] != false {
		t.Fatalf("orphan trace resource availability = %#v", orphanResources)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/traces/trace-a", nil, authB)
	assertEnvelope(t, response, http.StatusNotFound, true)

	noRedirect := *server.Client()
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	redirectRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/traces/trace-a/artifacts/artifact-a", nil)
	redirectRequest.Header.Set("Authorization", "Bearer "+loginA.AccessToken)
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
	redirectRequest.Header.Set("Authorization", "Bearer "+loginB.AccessToken)
	redirect, err = noRedirect.Do(redirectRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer redirect.Body.Close()
	if redirect.StatusCode != http.StatusNotFound || redirect.Header.Get("Location") != "" {
		t.Fatalf("cross-owner artifact redirect = %d %q", redirect.StatusCode, redirect.Header.Get("Location"))
	}

	if err := store.SaveTrace(ctx, postgres.TraceRecord{
		OwnerID: "owner-a", TraceID: "trace-delete-failure", Record: json.RawMessage(`{"status":"failed"}`), CreatedAt: now, UpdatedAt: now,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRun(ctx, postgres.RunRecord{
		OwnerID: "owner-a", ID: "run-delete-failure", ProfileID: "Backup", TraceID: "trace-delete-failure", Status: "failed",
		Request: json.RawMessage(`{"profileId":"Backup"}`), Result: json.RawMessage(`{"output":null}`), StartedAt: now, CompletedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	failureObjectKey := garageFixture.Key("llm-traces/owner-a/run-delete-failure/trace-delete-failure/artifact-delete-failure-trace.json")
	if err := store.SaveArtifact(ctx, postgres.ArtifactRecord{
		OwnerID: "owner-a", TraceID: "trace-delete-failure", ID: "artifact-delete-failure", Kind: "trace", ObjectKey: failureObjectKey,
		ContentType: "application/json", SHA256: strings.Repeat("b", 64), SizeBytes: 1,
		Available: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	failingService, err := gateway.NewResourceService(gateway.ResourceServiceConfig{
		Store: store, Profiles: profileService,
		ArtifactScope: func(string) (gateway.ArtifactAccess, error) { return failingArtifactAccess{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := failingService.DeleteHistory(ctx, "owner-a", "run-delete-failure"); err == nil {
		t.Fatal("artifact deletion failure did not fail closed")
	}
	if _, err := store.Run(ctx, "owner-a", "run-delete-failure"); err != nil {
		t.Fatalf("artifact deletion failure removed run metadata: %v", err)
	}
	if _, _, err := store.Trace(ctx, "owner-a", "trace-delete-failure"); err != nil {
		t.Fatalf("artifact deletion failure removed trace metadata: %v", err)
	}

	response = apiRequest(t, server.Client(), http.MethodDelete, server.URL+"/api/v1/history/run-a", nil, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	if _, err := store.Run(ctx, "owner-a", "run-a"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("history record was not deleted: %v", err)
	}
	if _, _, err := store.Trace(ctx, "owner-a", "trace-a"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("trace metadata was not deleted: %v", err)
	}
	if _, err := store.Artifact(ctx, "owner-a", "trace-a", "artifact-a"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("artifact metadata was not deleted: %v", err)
	}
	if _, _, err := ownerStore.Get(ctx, objectKey); !artifacts.IsKind(err, artifacts.KindNotFound) {
		t.Fatalf("artifact object body was not deleted: %v", err)
	}
	response = apiRequest(t, server.Client(), http.MethodDelete, server.URL+"/api/v1/history", nil, authA)
	assertEnvelope(t, response, http.StatusOK, false)
	if _, _, err := store.Trace(ctx, "owner-a", "trace-orphan"); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("orphan trace was not cleared: %v", err)
	}
	response = apiRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/stats", nil, authA)
	if response.JSON["result"].(map[string]any)["totalCount"] != float64(0) {
		t.Fatalf("cleared owner stats = %#v", response.JSON)
	}
	response = apiRequest(t, server.Client(), http.MethodDelete, server.URL+"/api/v1/profiles/Backup", nil, authA)
	assertEnvelope(t, response, http.StatusOK, false)
}

type testProfileProber struct{ err error }

func (prober *testProfileProber) Probe(context.Context, profiles.Profile, profiles.CredentialPayload) error {
	return prober.err
}

type testModelRefresher struct {
	models []profiles.Model
	err    error
}

type failingArtifactAccess struct{}

func (failingArtifactAccess) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", errors.New("presign unavailable")
}

func (failingArtifactAccess) DeleteMany(context.Context, []string) error {
	return errors.New("delete unavailable")
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
	contents, err := os.ReadFile("../../fixtures/parity/generated/profile-catalog.json")
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
	if err := store.SaveTrace(ctx, postgres.TraceRecord{
		OwnerID: "owner-a", TraceID: "trace-a", Record: json.RawMessage(`{"status":"success"}`), CreatedAt: now, UpdatedAt: now,
	}, []postgres.ObservationRecord{{OwnerID: "owner-a", TraceID: "trace-a", Sequence: 0, Type: "provider.attempt", Data: json.RawMessage(`{"number":1}`), CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"run-a", "run-b", "run-c"} {
		if err := store.SaveRun(ctx, postgres.RunRecord{
			OwnerID: "owner-a", ID: id, ProfileID: "Backup", TraceID: strings.Replace(id, "run", "trace", 1), Status: "succeeded",
			Request: json.RawMessage(`{"profileId":"Backup"}`), Result: json.RawMessage(`{"output":"ok"}`), StartedAt: now, CompletedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveTrace(ctx, postgres.TraceRecord{
		OwnerID: "owner-a", TraceID: "trace-orphan", Record: json.RawMessage(`{"status":"succeeded"}`), CreatedAt: now, UpdatedAt: now,
	}, nil); err != nil {
		t.Fatal(err)
	}
}
