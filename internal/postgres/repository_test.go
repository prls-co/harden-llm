//go:build integration

package postgres

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-020 TEST-053 TEST-059 TEST-061

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/internal/profiles"
)

func TestRepositoryContract(t *testing.T) {
	_, dsn := integrationtest.PostgresLease(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const runners = 8
	stores := make([]*Store, runners)
	for index := range stores {
		var err error
		stores[index], err = Open(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer stores[index].Close()
	}
	var wait sync.WaitGroup
	errorsByRunner := make(chan error, runners)
	for _, store := range stores {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsByRunner <- store.Migrate(ctx)
		}()
	}
	wait.Wait()
	close(errorsByRunner)
	for err := range errorsByRunner {
		if err != nil {
			t.Fatalf("concurrent migration: %v", err)
		}
	}
	store := stores[0]
	versions, err := store.AppliedMigrations(ctx)
	if err != nil || !reflect.DeepEqual(versions, []int64{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("migration versions = %v, %v", versions, err)
	}
	if err := store.Ready(ctx); err != nil {
		t.Fatalf("migrated store is not ready: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES (999)`); err != nil {
		t.Fatal(err)
	}
	if err := store.Ready(ctx); err == nil {
		t.Fatal("store with an unknown migration reported ready")
	}
	if err := store.Migrate(ctx); err == nil {
		t.Fatal("migration runner accepted an unknown applied migration")
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version = 999`); err != nil {
		t.Fatal(err)
	}
	assertSchema(t, ctx, store)

	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	users := []User{
		{ID: "owner-a", Email: "a@example.test", PasswordHash: "$argon2id$v=19$fixture-a", CreatedAt: now, UpdatedAt: now},
		{ID: "owner-b", Email: "b@example.test", PasswordHash: "$argon2id$v=19$fixture-b", CreatedAt: now, UpdatedAt: now},
	}
	for _, user := range users {
		if err := store.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	if user, err := store.UserByEmail(ctx, "A@EXAMPLE.TEST"); err != nil || user.ID != "owner-a" || user.Email != "a@example.test" {
		t.Fatalf("user round trip = %#v, %v", user, err)
	}

	credential := CredentialRecord{
		OwnerID: "owner-a", ID: "credential-a", KeyID: "key-2026", Nonce: []byte("0123456789ab"),
		Ciphertext: []byte("ciphertext-and-auth-tag"), Origin: "https://provider.example", Metadata: json.RawMessage(`{"schemaVersion":1}`),
		CreatedAt: now, UpdatedAt: now,
	}
	profile := ProfileRecord{
		OwnerID: "owner-a", ID: "profile-a", CredentialID: credential.ID,
		Document: json.RawMessage(`{"llmProfile":"Profile A","model":"model-a"}`), CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveProfile(ctx, profile, &credential); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Profile(ctx, "owner-a", "profile-a"); err != nil || got.CredentialID != credential.ID || !jsonEqual(got.Document, profile.Document) {
		t.Fatalf("profile round trip = %#v, %v", got, err)
	}
	if got, err := store.Credential(ctx, "owner-a", credential.ID); err != nil || !reflect.DeepEqual(got.Ciphertext, credential.Ciphertext) || got.Origin != credential.Origin {
		t.Fatalf("credential round trip = %#v, %v", got, err)
	}
	if _, err := store.Profile(ctx, "owner-b", "profile-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner profile read = %v", err)
	}
	if _, err := store.Credential(ctx, "owner-b", credential.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner credential read = %v", err)
	}
	boundaryProfile := profile
	boundaryProfile.ID = strings.Repeat("界", 500)
	boundaryProfile.Document = json.RawMessage(`{"llmProfile":"boundary"}`)
	if err := store.SaveProfile(ctx, boundaryProfile, nil); err != nil {
		t.Fatalf("1,500-byte profile ID: %v", err)
	}
	if got, err := store.Profile(ctx, "owner-a", boundaryProfile.ID); err != nil || got.ID != boundaryProfile.ID {
		t.Fatalf("boundary profile round trip = %#v, %v", got, err)
	}
	boundaryProfile.ID += "a"
	if err := store.SaveProfile(ctx, boundaryProfile, nil); err == nil {
		t.Fatal("profile ID beyond the source 1,500-byte limit was accepted")
	}

	state := ClientState{OwnerID: "owner-a", Document: json.RawMessage(`{"draft":"hello","theme":"system"}`), UpdatedAt: now}
	if err := store.SaveClientState(ctx, state); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ClientState(ctx, "owner-a"); err != nil || !jsonEqual(got.Document, state.Document) {
		t.Fatalf("state round trip = %#v, %v", got, err)
	}
	if _, err := store.ClientState(ctx, "owner-b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner state read = %v", err)
	}

	atomicRun := RunRecord{
		OwnerID: "owner-a", ID: "run-atomic", ProfileID: "profile-a", TraceID: "trace-atomic",
		Status: "succeeded", Request: json.RawMessage(`{"prompt":"redacted"}`),
		Result:    json.RawMessage(`{"schemaVersion":4,"output":"ok"}`),
		Execution: providerExecutionFields(1, 0, 0, 1, 0, "exact", 0.001, 1, 0, 0, 0),
		StartedAt: now, CompletedAt: now,
	}
	atomicTrace := TraceRecord{
		OwnerID: "owner-a", TraceID: "trace-atomic", RunID: "run-atomic", Record: json.RawMessage(`{"status":"succeeded"}`),
		CreatedAt: now, UpdatedAt: now,
	}
	duplicateArtifact := ArtifactRecord{
		OwnerID: "owner-a", RunID: "run-atomic", TraceID: "trace-atomic", ID: "artifact-atomic", Kind: "trace",
		ObjectKey:   "llm-traces/owner-a/run-atomic/trace-atomic/artifact-atomic.json",
		ContentType: "application/json", SHA256: strings.Repeat("a", 64), SizeBytes: 1,
		State: "available", CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecution(ctx, atomicRun, atomicTrace, nil, []ArtifactRecord{duplicateArtifact, duplicateArtifact}); err == nil {
		t.Fatal("duplicate execution artifact did not reject the atomic save")
	}
	if _, err := store.Run(ctx, "owner-a", "run-atomic"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed atomic execution left a run: %v", err)
	}
	if _, _, err := store.Trace(ctx, "owner-a", "trace-atomic"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed atomic execution left a trace: %v", err)
	}

	run := RunRecord{
		OwnerID: "owner-a", ID: "run-a", ProfileID: "profile-a", TraceID: "trace-a", Status: "succeeded",
		Request:   json.RawMessage(`{"prompt":"redacted"}`),
		Result:    json.RawMessage(`{"schemaVersion":4,"output":"ok"}`),
		Execution: cachedExecutionFields(10, 2, 3, 4, 5, 0.125, 1000, 42),
		StartedAt: now, CompletedAt: now.Add(time.Second),
	}
	trace := TraceRecord{OwnerID: "owner-a", TraceID: "trace-a", RunID: "run-a", Record: json.RawMessage(`{"schemaVersion":3,"runId":"run-a","traceId":"trace-a"}`), CreatedAt: now, UpdatedAt: now}
	observations := []ObservationRecord{{OwnerID: "owner-a", TraceID: "trace-a", Sequence: 0, Type: "attempt", Data: json.RawMessage(`{"number":1}`), CreatedAt: now}}
	if err := store.SaveExecution(ctx, run, trace, observations, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Run(ctx, "owner-a", "run-a"); err != nil || got.TraceID != run.TraceID || !jsonEqual(got.Result, run.Result) {
		t.Fatalf("run round trip = %#v, %v", got, err)
	}
	if _, err := store.Run(ctx, "owner-b", "run-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner run read = %v", err)
	}

	if got, gotObservations, err := store.Trace(ctx, "owner-a", "trace-a"); err != nil || got.RunID != "run-a" || !jsonEqual(got.Record, trace.Record) || !reflect.DeepEqual(observationTypes(gotObservations), []string{"attempt"}) {
		t.Fatalf("trace round trip = %#v %#v, %v", got, gotObservations, err)
	}
	if _, _, err := store.Trace(ctx, "owner-b", "trace-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner trace read = %v", err)
	}

	artifact := ArtifactRecord{OwnerID: "owner-a", RunID: "run-a", TraceID: "trace-a", ID: "artifact-a", Kind: "trace", ObjectKey: "owners/owner-a/traces/trace-a/trace/artifact-a.json", ContentType: "application/json", SHA256: strings.Repeat("a", 64), SizeBytes: 17, State: "available", CreatedAt: now, UpdatedAt: now}
	if err := store.SeedArtifactMetadataForTest(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Artifact(ctx, "owner-a", "trace-a", "artifact-a"); err != nil || got.ObjectKey != artifact.ObjectKey || got.SHA256 != artifact.SHA256 {
		t.Fatalf("artifact round trip = %#v, %v", got, err)
	}
	if references, truncated, err := store.ArtifactInventoryReferences(ctx, 10); err != nil || truncated || len(references) != 1 || references[0].ObjectKey != artifact.ObjectKey || references[0].Source != "metadata" {
		t.Fatalf("artifact inventory references = %#v, %v, %v", references, truncated, err)
	}
	if _, err := store.Artifact(ctx, "owner-b", "trace-a", "artifact-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner artifact read = %v", err)
	}

	failedRun := RunRecord{
		OwnerID: "owner-a", ID: "run-b", ProfileID: "profile-a", TraceID: "trace-b", Status: "failed",
		Request: json.RawMessage(`{"prompt":"redacted"}`), Result: json.RawMessage(`{"schemaVersion":4,"output":null}`),
		Execution: providerExecutionFields(1, 0, 0, 0, 0, "unknown", 0, 0, 1, 3000, 0),
		StartedAt: now, CompletedAt: now.Add(3 * time.Second),
	}
	failedTrace := TraceRecord{OwnerID: "owner-a", TraceID: "trace-b", RunID: "run-b", Record: json.RawMessage(`{"schemaVersion":3,"runId":"run-b","traceId":"trace-b"}`), CreatedAt: now, UpdatedAt: now}
	if err := store.SaveExecution(ctx, failedRun, failedTrace, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := store.RunStats(ctx, "owner-a"); err != nil ||
		got.TotalCount != 2 || got.SuccessCount != 1 || got.FailureCount != 1 || got.TimeoutCount != 0 ||
		got.ResultPromptTokens != 16 || got.ResultCacheReadTokens != 2 || got.ResultCacheCreationTokens != 3 ||
		got.ResultOutputTokens != 4 || got.ResultReasoningTokens != 5 || got.ResultTotalTokens != 25 ||
		got.ProviderPromptTokens != 1 || got.ProviderOutputTokens != 0 || got.ProviderReasoningTokens != 0 || got.ProviderTotalTokens != 1 ||
		got.ResultKnownCostSubtotalUSD != 0.125 || got.ProviderKnownCostSubtotalUSD != 0 ||
		got.CachedKnownCostSubtotalUSD != 0.125 || got.CachedCount != 1 ||
		got.ResultExactCostCount != 1 || got.ResultUnknownCostCount != 1 ||
		got.ProviderUnavailableCostCount != 1 || got.ProviderUnknownCostCount != 1 ||
		got.TotalCallDurationMS != 4000 || got.MaxCallDurationMS != 3000 || got.OverBudgetCount != 1 || got.MaxOverBudgetMS != 42 {
		t.Fatalf("authoritative stats = %#v, %v", got, err)
	}
	if got, err := store.RunStats(ctx, "owner-b"); err != nil || got.TotalCount != 0 || got.TotalCallDurationMS != 0 || got.MaxCallDurationMS != 0 {
		t.Fatalf("empty owner stats = %#v, %v", got, err)
	}
	if artifacts, err := store.ArtifactsForOwner(ctx, "owner-a"); err != nil || len(artifacts) != 1 || artifacts[0].ObjectKey != artifact.ObjectKey {
		t.Fatalf("owner artifacts = %#v, %v", artifacts, err)
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM llm_runs WHERE owner_id=$1 AND run_id=$2`, "owner-a", "run-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Run(ctx, "owner-a", "run-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted execution run remained: %v", err)
	}
	if _, _, err := store.Trace(ctx, "owner-a", "trace-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted execution trace remained: %v", err)
	}
	if artifacts, err := store.ArtifactsForOwner(ctx, "owner-a"); err != nil || len(artifacts) != 0 {
		t.Fatalf("deleted execution artifact metadata remained: %#v, %v", artifacts, err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO llm_traces (owner_id, trace_id, run_id, record, created_at, updated_at)
		VALUES ($1,$2,NULL,$3,$4,$4)`, "owner-a", "trace-orphan", json.RawMessage(`{"status":"failed"}`), now); err == nil {
		t.Fatal("runless trace bypassed structural execution ownership")
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO llm_traces (owner_id, trace_id, run_id, record, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$5)`, "owner-a", "trace-mismatch", "run-b", json.RawMessage(`{"status":"failed"}`), now); err == nil {
		t.Fatal("mismatched trace and run binding bypassed structural execution ownership")
	}
	if result, err := store.pool.Exec(ctx, `DELETE FROM llm_runs WHERE owner_id=$1`, "owner-a"); err != nil || result.RowsAffected() != 1 {
		t.Fatalf("aggregate root clear = %d, %v", result.RowsAffected(), err)
	}
	if _, _, err := store.Trace(ctx, "owner-a", "trace-b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("aggregate root clear left a trace: %v", err)
	}

	session := Session{ID: "session-a", OwnerID: "owner-a", TokenDigest: []byte(strings.Repeat("d", 32)), ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if got, err := store.SessionByDigest(ctx, session.TokenDigest); err != nil || got.ID != session.ID || got.OwnerID != session.OwnerID {
		t.Fatalf("session round trip = %#v, %v", got, err)
	}
	if err := store.RevokeSession(ctx, "owner-a", session.TokenDigest, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, err := store.SessionByDigest(ctx, session.TokenDigest); err != nil || got.RevokedAt == nil {
		t.Fatalf("session revocation = %#v, %v", got, err)
	}
}

func assertSchema(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	rows, err := store.pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, required := range []string{"users", "user_sessions", "llm_profiles", "llm_endpoint_credentials", "llm_client_state", "llm_runs", "llm_traces", "llm_trace_observations", "llm_artifacts", "llm_artifact_operations", "llm_artifact_delete_batches", "llm_operation_cache", "schema_migrations"} {
		if !contains(tables, required) {
			t.Errorf("required table %s missing from %v", required, tables)
		}
	}
	if contains(tables, "llm_stats_totals") {
		t.Fatal("unused mutable stats projection table remains in the application schema")
	}
	if strings.Contains(strings.ToLower(string(migrationSource())), "langfuse") {
		t.Fatal("application migration names an external diagnostics database")
	}
}

func observationTypes(records []ObservationRecord) []string {
	result := make([]string, len(records))
	for index, record := range records {
		result[index] = record.Type
	}
	return result
}

func jsonEqual(left, right []byte) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil && reflect.DeepEqual(leftValue, rightValue)
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func providerExecutionFields(input, cacheRead, cacheCreation, output, reasoning int64, costStatus string, subtotal float64, known, unknown, durationMS, overBudgetMS int64) *ExecutionFields {
	usage := UsageFields{
		Status: "complete", InputTokens: input, CacheReadTokens: cacheRead,
		CacheCreationTokens: cacheCreation, OutputTokens: output, ReasoningTokens: reasoning,
	}
	cost := CostFields{
		Status: costStatus, KnownSubtotalUSD: subtotal,
		KnownObservations: known, UnknownObservations: unknown,
	}
	return &ExecutionFields{
		SchemaVersion:    4,
		SelectedProvider: "openai", SelectedProtocol: "responses",
		SelectedEndpoint: "https://provider.example", SelectedModelID: "model-a",
		ResultSource: "provider", ProducerProfileID: "profile-a", ProducerProvider: "openai",
		ProducerProtocol: "openai.responses", ProducerEndpoint: "https://provider.example", ProducerModelID: "model-a",
		ProviderInvoked: true, ResultUsage: usage, ProviderUsage: usage, ResultCost: cost, ProviderCost: cost,
		TotalCallDurationMS: durationMS, OverBudgetMS: overBudgetMS,
	}
}

func cachedExecutionFields(input, cacheRead, cacheCreation, output, reasoning int64, subtotal float64, durationMS, overBudgetMS int64) *ExecutionFields {
	usage := UsageFields{
		Status: "complete", InputTokens: input, CacheReadTokens: cacheRead,
		CacheCreationTokens: cacheCreation, OutputTokens: output, ReasoningTokens: reasoning,
	}
	return &ExecutionFields{
		SchemaVersion:    4,
		SelectedProvider: "openai", SelectedProtocol: "responses",
		SelectedEndpoint: "https://provider.example", SelectedModelID: "model-a",
		ResultSource: "cache", ProducerProfileID: "profile-a", ProducerProvider: "openai",
		ProducerProtocol: "openai.responses", ProducerEndpoint: "https://provider.example", ProducerModelID: "model-a",
		ProviderInvoked: false, ResultUsage: usage, ProviderUsage: UsageFields{Status: "unavailable"},
		ResultCost:   CostFields{Status: "exact", KnownSubtotalUSD: subtotal, KnownObservations: 1},
		ProviderCost: CostFields{Status: "unavailable"}, CacheServed: true,
		TotalCallDurationMS: durationMS, OverBudgetMS: overBudgetMS,
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-208 TEST-251
func TestRecoveryMigration(t *testing.T) {
	store, ctx := recoveryMigrationStore(t)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	defaults := `{"maxAttempts":4,"retryOn":["network","rate_limit","server_error","empty_response","provider_retry"],"repairInvalidOutput":true,"backoff":{"baseDelayMs":500,"maxDelayMs":8000}}`
	noRepair := strings.Replace(defaults, `"repairInvalidOutput":true`, `"repairInvalidOutput":false`, 1)
	zero := `{"maxAttempts":1,"retryOn":["empty_response","provider_retry"],"repairInvalidOutput":false,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`
	cases := []struct{ id, options, policy string }{
		{"absent", `{}`, defaults},
		{"disabled", `{"structuredRepairRetry":false}`, noRepair},
		{"nested-disabled", `{"structuredRepairRetry":{"enabled":false}}`, noRepair},
		{"nested-zero", `{"structuredRepairRetry":{"enabled":false,"maxAttempts":1,"baseDelayMs":0,"maxDelayMs":0,"enableRetryOn429":false,"enableRetryOn5xx":false,"enableRetryOnNetworkError":false}}`, zero},
		{"flat-wins", `{"maxAttempts":1,"baseDelayMs":0,"maxDelayMs":0,"enableRetryOn429":false,"enableRetryOn5xx":false,"enableRetryOnNetworkError":false,"enableRetryOnParseError":true,"structuredRepairRetry":{"enabled":false,"maxAttempts":9,"baseDelayMs":1000,"maxDelayMs":9000,"enableRetryOn429":true,"enableRetryOn5xx":true,"enableRetryOnNetworkError":true,"escalation":{"llmProfile":"retired"}}}`, zero},
	}
	wantedProfiles := map[string]json.RawMessage{}
	wantedStates := map[string]json.RawMessage{}
	wantedResults := map[string]json.RawMessage{}
	contract, err := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for index, owner := range []string{"owner-a", "owner-b"} {
		if err := store.CreateUser(ctx, User{ID: owner, Email: owner + "@example.test", PasswordHash: "$argon2id$fixture", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		credential := CredentialRecord{OwnerID: owner, ID: "credential", KeyID: "key", Nonce: []byte("0123456789ab"), Ciphertext: []byte("synthetic-ciphertext-and-auth-tag"), Origin: "https://api.openai.com", Metadata: json.RawMessage(`{"schemaVersion":1,"scope":"global","apiInferenceTypes":["responses"],"custom":"retained"}`), CreatedAt: now, UpdatedAt: now}
		for _, test := range cases {
			document := recoveryMigrationProfile(t, test.id, test.options)
			if test.id == "flat-wins" {
				profile := rawObject(t, document)
				profile["reasoningEffortMap"] = json.RawMessage(`{"lowest":{"maxAttempts":7,"temperature":0.4}}`)
				document = marshalRecovery(t, profile)
			}
			if err := store.SaveProfile(ctx, ProfileRecord{OwnerID: owner, ID: test.id, CredentialID: credential.ID, Document: document, CreatedAt: now, UpdatedAt: now}, &credential); err != nil {
				t.Fatal(err)
			}
			wanted := rawObject(t, recoveryMigrationProfile(t, test.id, `{}`))
			wanted["schemaVersion"] = json.RawMessage(`3`)
			wanted["recoveryPolicy"] = recoveryMigrationPolicy(t, test.policy)
			delete(wanted, "backupProfiles")
			if test.id == "flat-wins" {
				wanted["reasoningEffortMap"] = json.RawMessage(`{"lowest":{"temperature":0.4}}`)
			}
			wantedProfiles[owner+"/"+test.id] = marshalRecovery(t, wanted)
		}
		state := rawObject(t, []byte(`{"schemaVersion":1,"selectedProfileId":"absent","userPrompt":"retain task 0012","callType":"structured","structuredRepair":false,"schema":{"type":"object"},"ui":{"historyOpen":true},"cacheMode":"cache","webSearch":true,"reasoningByProfile":{"absent":"highest"}}`))
		policy := noRepair
		state["providerOptions"] = json.RawMessage(`{"max_tokens":32,"structuredRepairRetry":false}`)
		if index == 1 {
			for key, value := range rawObject(t, []byte(`{"maxAttempts":10,"initialBackoffMs":0,"maximumBackoffMs":0,"retryNetwork":false,"retryRateLimit":false,"retryServerError":false,"retryEmpty":false,"retryParse":true,"repairEscalation":{"attempt":3}}`)) {
				state[key] = value
			}
			policy = `{"maxAttempts":10,"retryOn":["provider_retry"],"repairInvalidOutput":false,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`
		}
		if err := store.SaveClientState(ctx, ClientState{OwnerID: owner, Document: marshalRecovery(t, state), UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"maxAttempts", "initialBackoffMs", "maximumBackoffMs", "retryNetwork", "retryRateLimit", "retryServerError", "retryEmpty", "retryParse", "repairEscalation", "structuredRepair"} {
			delete(state, key)
		}
		state["schemaVersion"], state["recoveryPolicy"] = json.RawMessage(`3`), recoveryMigrationPolicy(t, policy)
		state["providerOptions"] = json.RawMessage(`{"max_tokens":32}`)
		wantedStates[owner] = marshalRecovery(t, state)

		// Use the public current example, then seed the two historical attempt
		// fields. A large exact output value detects numeric corruption.
		example := contract.Components.Responses["RunSuccess"].Value.Content["application/json"].Examples["text"].Value.Value.(map[string]any)["result"]
		result := rawObject(t, marshalRecovery(t, example))
		result["output"] = json.RawMessage(`{"exact":9007199254740993,"numericString":"0012","whitespace":"  keep  "}`)
		// Historical v3 runs did not measure elapsed retry waits. The v4
		// migration records that absence as JSON null rather than inventing zero.
		result["totalActualWaitMs"] = json.RawMessage(`null`)
		wantedResults[owner] = marshalRecovery(t, result)
		result["schemaVersion"] = json.RawMessage(`2`)
		var attempts []map[string]json.RawMessage
		if err := json.Unmarshal(result["attempts"], &attempts); err != nil {
			t.Fatal(err)
		}
		for _, attempt := range attempts {
			attempt["retryLocalNumber"], attempt["backupIndex"] = json.RawMessage(`1`), json.RawMessage(`0`)
		}
		result["attempts"] = marshalRecovery(t, attempts)
		if index == 1 {
			result["attempts"] = json.RawMessage(`null`)
			wanted := rawObject(t, wantedResults[owner])
			wanted["attempts"] = json.RawMessage(`[]`)
			wantedResults[owner] = marshalRecovery(t, wanted)
		}
		run := RunRecord{OwnerID: owner, ID: "run-example", ProfileID: "Primary", TraceID: "trace-example", Status: "succeeded", Request: json.RawMessage(`{"profileId":"Primary","maxAttempts":0,"structuredRepair":false,"userPrompt":"original evidence"}`), Result: marshalRecovery(t, result), Execution: providerExecutionFields(8, 0, 0, 1, 0, "exact", 0.00001, 1, 0, 120, 0), StartedAt: now, CompletedAt: now}
		run.Execution.SchemaVersion = 2
		// Fixture creation targets schema 5 directly, before the current writer
		// exists. The migration itself always runs through Store.Migrate.
		tx, err := store.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := insertExecutionRun(ctx, tx, run); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO llm_traces(owner_id,trace_id,run_id,record,created_at,updated_at) VALUES($1,'trace-example','run-example','{"schemaVersion":2,"runId":"run-example","traceId":"trace-example"}',$2,$2)`, owner, now); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO llm_trace_observations VALUES($1,'trace-example',0,'attempt','{"number":1,"retryLocalNumber":1,"backupIndex":0}',$2)`, owner, now); err != nil {
			t.Fatal(err)
		}
		artifact := ArtifactRecord{OwnerID: owner, RunID: "run-example", TraceID: "trace-example", ID: "artifact", Kind: "trace", ObjectKey: "owners/" + owner + "/immutable.json", ContentType: "application/json", SHA256: strings.Repeat("a", 64), SizeBytes: 17, State: "available", CreatedAt: now, UpdatedAt: now}
		if err := store.SeedArtifactMetadataForTest(ctx, artifact); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO llm_operation_cache
				(owner_id, cache_version, operation_hash, operation, result, usage, cost, provider_envelope, created_at, updated_at)
			VALUES ($1,'operation-v2','retained','{"responseProjectionVersion":"v1"}',
				'{"output":{"exact":9007199254740993,"numericString":"0012"},"accounting":{"usage":{"status":"complete","inputTokens":0,"cacheReadTokens":0,"cacheCreationTokens":0,"outputTokens":0,"reasoningTokens":0},"cost":{"status":"unavailable"}},"producer":{"profileId":"profile-a","provider":"fixture","protocol":"fixture","endpoint":"https://example.test","modelId":"model-a"}}',
				'{"status":"complete"}','{"status":"unavailable"}','{"schemaVersion":"raw.v1"}', $2, $2)`, owner, now); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateSession(ctx, Session{ID: "session-" + owner, OwnerID: owner, TokenDigest: []byte(strings.Repeat(string(rune('a'+index)), 32)), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	before := recoverySnapshot(t, ctx, store, true)
	errorsByRunner := make(chan error, 4)
	for range 4 {
		go func() { errorsByRunner <- store.Migrate(ctx) }()
	}
	for range 4 {
		if err := <-errorsByRunner; err != nil {
			t.Fatal(err)
		}
	}
	versions, err := store.AppliedMigrations(ctx)
	if err != nil || !reflect.DeepEqual(versions, []int64{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("migration versions = %v, %v", versions, err)
	}
	if after := recoverySnapshot(t, ctx, store, true); !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed independent rows, credentials, requests, execution values or ownership")
	}
	for identity, wanted := range wantedProfiles {
		owner, id, _ := strings.Cut(identity, "/")
		got, err := store.Profile(ctx, owner, id)
		if err != nil {
			t.Fatal(err)
		}
		assertRecoveryJSON(t, ctx, store, got.Document, wanted)
		catalog := marshalRecovery(t, map[string]json.RawMessage{id: got.Document})
		if _, err := profiles.ParseCatalog(catalog); err != nil {
			t.Fatalf("migrated profile violates current contract: %v", err)
		}
	}
	for owner, wanted := range wantedStates {
		got, err := store.ClientState(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		assertRecoveryJSON(t, ctx, store, got.Document, wanted)
		var value any
		if err := json.Unmarshal(got.Document, &value); err != nil {
			t.Fatal(err)
		}
		if err := contract.Components.Schemas["ClientState"].Value.VisitJSON(value); err != nil {
			t.Fatalf("migrated state contract: %v", err)
		}
	}
	for owner, wanted := range wantedResults {
		got, err := store.Run(ctx, owner, "run-example")
		if err != nil {
			t.Fatal(err)
		}
		assertRecoveryJSON(t, ctx, store, got.Result, wanted)
		var version int
		if err := store.pool.QueryRow(ctx, `SELECT result_schema_version FROM llm_runs WHERE owner_id=$1`, owner).Scan(&version); err != nil || version != 4 {
			t.Fatalf("result projection version=%d %v", version, err)
		}
		var value any
		if err := json.Unmarshal(got.Result, &value); err != nil {
			t.Fatal(err)
		}
		if err := contract.Components.Schemas["RunResult"].Value.VisitJSON(value); err != nil {
			t.Fatalf("migrated result contract: %v", err)
		}
	}
	all := recoverySnapshot(t, ctx, store, false)
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(all, recoverySnapshot(t, ctx, store, false)) {
		t.Fatal("repeated migration changed data")
	}
}

func TestRecoveryMigrationRejectsInvalidDocumentsAtomically(t *testing.T) {
	for _, test := range []struct{ kind, fields, field string }{
		{"profile", `{"maxAttempts":0}`, "maxAttempts"},
		{"profile", `{"maxAttempts":11}`, "maxAttempts"},
		{"profile", `{"maxAttempts":"4"}`, "maxAttempts"},
		{"profile", `{"maxAttempts":1.5}`, "maxAttempts"},
		{"profile", `{"baseDelayMs":-1}`, "baseDelayMs"},
		{"profile", `{"baseDelayMs":60001}`, "baseDelayMs"},
		{"profile", `{"maxDelayMs":600001}`, "maxDelayMs"},
		{"profile", `{"baseDelayMs":10,"maxDelayMs":0}`, "maxDelayMs"},
		{"profile", `{"enableRetryOn429":null}`, "enableRetryOn429"},
		{"profile", `{"structuredRepairRetry":null}`, "structuredRepairRetry"},
		{"profile", `{"structuredRepairRetry":{"enabled":"false"}}`, "enabled"},
		{"profile", `{"maxAttempts":4,"structuredRepairRetry":{"maxAttempts":0}}`, "maxAttempts"},
		{"state", `{}`, "structuredRepair"},
		{"state", `{"structuredRepair":null}`, "structuredRepair"},
		{"state", `{"structuredRepair":false,"retryEmpty":"false"}`, "retryEmpty"},
		{"state", `{"structuredRepair":true,"maxAttempts":0}`, "maxAttempts"},
		{"state", `{"structuredRepair":true,"initialBackoffMs":20,"maximumBackoffMs":0}`, "maximumBackoffMs"},
		{"run", `{"schemaVersion":2,"attempts":{}}`, "attempts"},
		{"run", `{"schemaVersion":2,"attempts":[null]}`, "attempts"},
		{"run", `{"schemaVersion":1,"attempts":[]}`, "schemaVersion"},
	} {
		t.Run(test.kind+"/"+test.fields, func(t *testing.T) {
			store, ctx := recoveryMigrationStore(t)
			now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
			if err := store.CreateUser(ctx, User{ID: "owner", Email: "owner@example.test", PasswordHash: "$argon2id$fixture", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			// A valid row is eligible before the invalid one. Any failed mapping
			// must roll back its changes as well as the migration version.
			for _, id := range []string{"a-valid", "z-invalid"} {
				options := `{}`
				if test.kind == "profile" && id == "z-invalid" {
					options = test.fields
				}
				if err := store.SaveProfile(ctx, ProfileRecord{OwnerID: "owner", ID: id, Document: recoveryMigrationProfile(t, id, options), CreatedAt: now, UpdatedAt: now}, nil); err != nil {
					t.Fatal(err)
				}
			}
			if test.kind == "state" {
				state := rawObject(t, []byte(test.fields))
				state["schemaVersion"] = json.RawMessage(`1`)
				if err := store.SaveClientState(ctx, ClientState{OwnerID: "owner", Document: marshalRecovery(t, state), UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
			}
			if test.kind == "run" {
				err := store.SaveExecution(ctx,
					RunRecord{OwnerID: "owner", ID: "invalid-run", ProfileID: "a-valid", TraceID: "invalid-trace", Status: "failed", Request: json.RawMessage(`{}`), Result: json.RawMessage(test.fields), StartedAt: now, CompletedAt: now},
					TraceRecord{OwnerID: "owner", TraceID: "invalid-trace", RunID: "invalid-run", Record: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			before := recoverySnapshot(t, ctx, store, false)
			err := store.Migrate(ctx)
			if err == nil || !strings.Contains(err.Error(), "owner") || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("expected document identity and field %s, got %v", test.field, err)
			}
			if !reflect.DeepEqual(before, recoverySnapshot(t, ctx, store, false)) {
				t.Fatal("failed migration partially changed stored data or version")
			}
			if err := store.Ready(ctx); err == nil {
				t.Fatal("unmigrated database reported ready")
			}
		})
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-251
func TestRecoveryStagesMigrationRejectsMixedPolicyAtomically(t *testing.T) {
	store, ctx := recoveryMigrationStore(t, 7)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	if err := store.CreateUser(ctx, User{ID: "owner", Email: "owner@example.test", PasswordHash: "$argon2id$fixture", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile("../../fixtures/contracts/profile-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	document := rawObject(t, catalog.Input["Primary"])
	document["schemaVersion"] = json.RawMessage(`2`)
	document["recoveryPolicy"] = json.RawMessage(`{"maxAttempts":4,"retryOn":["network"],"repairInvalidOutput":true,"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":500,"maxDelayMs":8000}}`)
	if err := store.SaveProfile(ctx, ProfileRecord{
		OwnerID: "owner", ID: "mixed", Document: marshalRecovery(t, document), CreatedAt: now, UpdatedAt: now,
	}, nil); err != nil {
		t.Fatal(err)
	}

	before := recoverySnapshot(t, ctx, store, false)
	err = store.Migrate(ctx)
	if err == nil || !strings.Contains(err.Error(), "recoveryPolicy mixes") || !strings.Contains(err.Error(), "profile owner/mixed") {
		t.Fatalf("expected mixed-policy migration rejection, got %v", err)
	}
	if !reflect.DeepEqual(before, recoverySnapshot(t, ctx, store, false)) {
		t.Fatal("mixed-policy migration partially changed stored data or version")
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-227
func TestRecoveryIntegrityStorage(t *testing.T) {
	store, ctx := recoveryMigrationStore(t, 6)
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	if err := store.CreateUser(ctx, User{ID: "cache-owner", Email: "cache-owner@example.test", PasswordHash: "$argon2id$fixture", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(`{"output":{"exact":9007199254740993,"fraction":0.12345678901234567890123456789},"accounting":{"usage":{"inputTokens":0,"cacheReadTokens":0,"cacheCreationTokens":0,"outputTokens":0,"reasoningTokens":0,"status":"complete"},"cost":{"knownSubtotalUsd":0,"status":"unavailable","source":"","knownObservations":0,"unknownObservations":0}},"producer":{"profileId":"historical-profile","provider":"fixture","protocol":"fixture","endpoint":"https://example.test","modelId":"historical-model"}}`)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO llm_operation_cache
			(owner_id, cache_version, operation_hash, operation, result, usage, cost, provider_envelope, created_at, updated_at)
		VALUES ($1,'operation-v2','historical-hash','{"model":"historical"}',$2,'{"status":"complete"}','{"status":"unavailable"}','{"schemaVersion":"raw.v1"}',$3,$3)`, "cache-owner", result, now); err != nil {
		t.Fatal(err)
	}
	type retained struct {
		OwnerID, Version, Hash string
		Result                 []byte
		CreatedAt, UpdatedAt   time.Time
	}
	readRetained := func() retained {
		var value retained
		if err := store.pool.QueryRow(ctx, `SELECT owner_id, cache_version, operation_hash, result, created_at, updated_at FROM llm_operation_cache WHERE owner_id=$1 AND cache_version=$2 AND operation_hash=$3`, "cache-owner", "operation-v2", "historical-hash").Scan(&value.OwnerID, &value.Version, &value.Hash, &value.Result, &value.CreatedAt, &value.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := readRetained()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	after := readRetained()
	if before.OwnerID != after.OwnerID || before.Version != after.Version || before.Hash != after.Hash || before.CreatedAt != after.CreatedAt || before.UpdatedAt != after.UpdatedAt {
		t.Fatalf("retained cache identity/timestamps changed: before=%#v after=%#v", before, after)
	}
	var equal bool
	if err := store.pool.QueryRow(ctx, `SELECT $1::jsonb = $2::jsonb`, before.Result, after.Result).Scan(&equal); err != nil || !equal {
		t.Fatalf("retained cache result changed: before=%s after=%s error=%v", before.Result, after.Result, err)
	}
	rows, err := store.pool.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name='llm_operation_cache' ORDER BY ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := make([]string, 0)
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantedColumns := []string{"owner_id", "cache_version", "operation_hash", "result", "created_at", "updated_at"}
	if !reflect.DeepEqual(columns, wantedColumns) {
		t.Fatalf("cache columns = %v, want %v", columns, wantedColumns)
	}
	versions, err := store.AppliedMigrations(ctx)
	if err != nil || !reflect.DeepEqual(versions, []int64{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("migration versions = %v, %v", versions, err)
	}
	if err := store.Ready(ctx); err != nil {
		t.Fatalf("migrated store is not ready: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if repeated := readRetained(); !reflect.DeepEqual(after, repeated) {
		t.Fatalf("repeated migration changed retained cache: before=%#v after=%#v", after, repeated)
	}
}

func recoveryMigrationStore(t *testing.T, through ...int) (*Store, context.Context) {
	t.Helper()
	_, dsn := integrationtest.PostgresLease(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `CREATE TABLE schema_migrations(version bigint PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationEntries()
	if err != nil {
		t.Fatal(err)
	}
	limit := int64(5)
	if len(through) > 0 {
		limit = int64(through[0])
	}
	for _, entry := range entries {
		if entry.version <= limit {
			if _, err := tx.Exec(ctx, entry.sql); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, entry.version); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return store, ctx
}

func recoveryMigrationProfile(t *testing.T, id, extra string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("../../fixtures/contracts/profile-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	profile := rawObject(t, fixture.Input["Backup"])
	profile["schemaVersion"], profile["llmProfile"] = json.RawMessage(`1`), marshalRecovery(t, id)
	delete(profile, "recoveryPolicy")
	profile["backupProfiles"] = json.RawMessage(`["retired"]`)
	options := rawObject(t, []byte(`{"temperature":0.25,"max_tokens":512}`))
	for key, value := range rawObject(t, []byte(extra)) {
		options[key] = value
	}
	profile["defaultOptions"] = marshalRecovery(t, options)
	return marshalRecovery(t, profile)
}

func rawObject(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func marshalRecovery(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func recoveryMigrationPolicy(t *testing.T, legacy string) json.RawMessage {
	t.Helper()
	policy := rawObject(t, []byte(legacy))
	var repairEnabled bool
	if err := json.Unmarshal(policy["repairInvalidOutput"], &repairEnabled); err != nil {
		t.Fatal(err)
	}
	delete(policy, "repairInvalidOutput")
	if repairEnabled {
		policy["jsonRepair"] = json.RawMessage(`{"initial":{"source":"generation"},"escalation":{"source":"generation"}}`)
	} else {
		policy["jsonRepair"] = json.RawMessage(`null`)
	}
	policy["rerun"] = json.RawMessage(`null`)
	return marshalRecovery(t, policy)
}

func assertRecoveryJSON(t *testing.T, ctx context.Context, store *Store, got, want []byte) {
	t.Helper()
	var equal bool
	if err := store.pool.QueryRow(ctx, `SELECT $1::jsonb = $2::jsonb`, got, want).Scan(&equal); err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatalf("migration document mismatch\ngot: %s\nwant: %s", got, want)
	}
}

func recoverySnapshot(t *testing.T, ctx context.Context, store *Store, independentOnly bool) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, table := range []string{"users", "user_sessions", "llm_endpoint_credentials", "llm_profiles", "llm_client_state", "llm_runs", "llm_traces", "llm_trace_observations", "llm_artifacts", "llm_artifact_operations", "llm_artifact_delete_batches", "llm_operation_cache", "schema_migrations"} {
		expression := "to_jsonb(row)"
		if independentOnly {
			switch table {
			case "schema_migrations":
				continue
			case "llm_profiles", "llm_client_state":
				expression += " - 'document'"
			case "llm_runs":
				expression += " - 'result' - 'result_schema_version'"
			case "llm_operation_cache":
				expression += " - 'operation' - 'provider_envelope' - 'usage' - 'cost'"
			}
		}
		var snapshot string
		query := fmt.Sprintf(`SELECT COALESCE(jsonb_agg(value ORDER BY value::text),'[]'::jsonb)::text FROM (SELECT %s AS value FROM %s AS row) AS snapshots`, expression, table)
		if err := store.pool.QueryRow(ctx, query).Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		result[table] = snapshot
	}
	return result
}
