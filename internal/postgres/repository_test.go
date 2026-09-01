//go:build integration

package postgres

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-020 TEST-053 TEST-059

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/integrationtest"
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
	if err != nil || !reflect.DeepEqual(versions, []int64{1, 2, 3}) {
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
		Result:    json.RawMessage(`{"schemaVersion":2,"output":"ok"}`),
		Execution: providerExecutionFields(1, 0, 0, 1, 0, "exact", 0.001, 1, 0, 0, 0),
		StartedAt: now, CompletedAt: now,
	}
	atomicTrace := TraceRecord{
		OwnerID: "owner-a", TraceID: "trace-atomic", Record: json.RawMessage(`{"status":"succeeded"}`),
		CreatedAt: now, UpdatedAt: now,
	}
	duplicateArtifact := ArtifactRecord{
		OwnerID: "owner-a", TraceID: "trace-atomic", ID: "artifact-atomic", Kind: "trace",
		ObjectKey:   "llm-traces/owner-a/run-atomic/trace-atomic/artifact-atomic.json",
		ContentType: "application/json", SHA256: strings.Repeat("a", 64), SizeBytes: 1,
		Available: true, CreatedAt: now, UpdatedAt: now,
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
		Result:    json.RawMessage(`{"schemaVersion":2,"output":"ok"}`),
		Execution: cachedExecutionFields(10, 2, 3, 4, 5, 0.125, 1000, 42),
		StartedAt: now, CompletedAt: now.Add(time.Second),
	}
	trace := TraceRecord{OwnerID: "owner-a", TraceID: "trace-a", RunID: "run-a", Record: json.RawMessage(`{"schemaVersion":2,"runId":"run-a","traceId":"trace-a"}`), CreatedAt: now, UpdatedAt: now}
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

	artifact := ArtifactRecord{OwnerID: "owner-a", TraceID: "trace-a", ID: "artifact-a", Kind: "trace", ObjectKey: "owners/owner-a/traces/trace-a/trace/artifact-a.json", ContentType: "application/json", SHA256: strings.Repeat("a", 64), SizeBytes: 17, Available: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveArtifact(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Artifact(ctx, "owner-a", "trace-a", "artifact-a"); err != nil || got.ObjectKey != artifact.ObjectKey || got.SHA256 != artifact.SHA256 {
		t.Fatalf("artifact round trip = %#v, %v", got, err)
	}
	if _, err := store.Artifact(ctx, "owner-b", "trace-a", "artifact-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner artifact read = %v", err)
	}

	failedRun := RunRecord{
		OwnerID: "owner-a", ID: "run-b", ProfileID: "profile-a", TraceID: "trace-b", Status: "failed",
		Request: json.RawMessage(`{"prompt":"redacted"}`), Result: json.RawMessage(`{"schemaVersion":2,"output":null}`),
		Execution: providerExecutionFields(1, 0, 0, 0, 0, "unknown", 0, 0, 1, 3000, 0),
		StartedAt: now, CompletedAt: now.Add(3 * time.Second),
	}
	failedTrace := TraceRecord{OwnerID: "owner-a", TraceID: "trace-b", RunID: "run-b", Record: json.RawMessage(`{"schemaVersion":2,"runId":"run-b","traceId":"trace-b"}`), CreatedAt: now, UpdatedAt: now}
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
	if err := store.DeleteExecution(ctx, "owner-a", "run-a", "trace-a"); err != nil {
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
	if err := store.SaveTrace(ctx, TraceRecord{OwnerID: "owner-a", TraceID: "trace-orphan", Record: json.RawMessage(`{"status":"failed"}`), CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	if count, err := store.ClearExecutions(ctx, "owner-a"); err != nil || count != 1 {
		t.Fatalf("clear executions = %d, %v", count, err)
	}
	if _, _, err := store.Trace(ctx, "owner-a", "trace-orphan"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan trace survived execution clear: %v", err)
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
	for _, required := range []string{"users", "user_sessions", "llm_profiles", "llm_endpoint_credentials", "llm_client_state", "llm_runs", "llm_traces", "llm_trace_observations", "llm_artifacts", "llm_operation_cache", "schema_migrations"} {
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
		SchemaVersion:    2,
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
		SchemaVersion:    2,
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
