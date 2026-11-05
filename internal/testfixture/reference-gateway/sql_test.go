//go:build referencefixtureintegration

package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-486

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/httpclient"
	"github.com/prls-co/harden-llm/internal/testfixture/reference"
)

type fixtureSQL struct {
	url    string
	schema string
	pool   *pgxpool.Pool
	admin  *pgxpool.Pool
}

// This explicit track borrows the existing approved CI database. It never
// starts a physical service, creates a role/database or resets public tables.
func ownedSQL(t *testing.T) fixtureSQL {
	t.Helper()
	raw := os.Getenv("PRLS_FIXTURE_TEST_DATABASE_URL")
	u, err := url.Parse(raw)
	if err != nil || raw == "" || u.Hostname() != "127.0.0.1" || !strings.HasPrefix(u.Path, "/ci_") || u.User == nil || u.Query().Get("sslmode") != "disable" {
		t.Fatal("PRLS_FIXTURE_TEST_DATABASE_URL must explicitly select the approved loopback CI database")
	}
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal("invalid approved CI database configuration")
	}
	config.MaxConns, config.MinConns = 2, 0
	admin, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal("cannot open approved CI database")
	}
	t.Cleanup(admin.Close)
	schema := "hllm_fixture_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal("cannot allocate the owned fixture schema in the approved CI database")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error("cannot clean this test's owned fixture schema")
		}
	})
	query := url.Values{"sslmode": {"disable"}, "search_path": {schema}}
	u.RawQuery = query.Encode()
	config, err = pgxpool.ParseConfig(u.String())
	if err != nil {
		t.Fatal("cannot configure the private fixture schema")
	}
	config.MaxConns, config.MinConns = 2, 0
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal("cannot open the private fixture schema")
	}
	t.Cleanup(pool.Close)
	return fixtureSQL{url: u.String(), schema: schema, pool: pool, admin: admin}
}

func fixtureBinary(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "reference-gateway")
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", binary, "./internal/testfixture/reference-gateway")
	command.Dir = root
	if err := command.Run(); err != nil {
		t.Fatal("cannot build the actual fixture command from this owner source")
	}
	return binary
}

func startFixture(t *testing.T, binary, dsn, scenario string, stopSignal os.Signal) (readiness, func()) {
	t.Helper()
	env := fixtureEnvironment(t)
	if os.WriteFile(env["PRLS_FIXTURE_SCENARIO_FILE"], []byte(scenario), 0o600) != nil {
		t.Fatal("cannot write the synthetic scenario")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, binary)
	command.Env = []string{"PRLS_FIXTURE_CLIENT_KEY=" + testKey, "PRLS_FIXTURE_DATABASE_URL=" + dsn, "PRLS_FIXTURE_SCENARIO_FILE=" + env["PRLS_FIXTURE_SCENARIO_FILE"]}
	output := &startupOutput{line: make(chan []byte, 1)}
	var diagnostic bytes.Buffer
	command.Stdout, command.Stderr = output, &diagnostic
	command.Cancel = func() error { return command.Process.Signal(stopSignal) }
	command.WaitDelay = 2 * time.Second
	if command.Start() != nil {
		t.Fatal("cannot start the actual owned fixture process")
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = command.Wait() }()
	stop := sync.OnceFunc(func() {
		cancel()
		<-done
		if command.ProcessState.ExitCode() != 0 {
			t.Error("fixture did not complete graceful owned shutdown")
		}
		secretURL, _ := url.Parse(dsn)
		password, _ := secretURL.User.Password()
		for _, secret := range []string{testKey, password, "fixture SQL object"} {
			if secret != "" && (strings.Contains(output.String(), secret) || strings.Contains(diagnostic.String(), secret)) {
				t.Error("fixture stdout/stderr disclosed a key, DSN password or prompt")
			}
		}
		if strings.Count(output.String(), "\n") != 1 {
			t.Error("fixture did not emit exactly one readiness JSON line")
		}
	})
	t.Cleanup(stop)
	var ready readiness
	select {
	case line := <-output.line:
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&ready) != nil || decoder.Decode(new(any)) != io.EOF || ready.QueryProfileID != queryProfileID || ready.UtilityProfileID != utilityProfileID {
			t.Fatal("fixture did not publish the exact readiness contract")
		}
	case <-done:
		t.Fatal("fixture exited before readiness; private diagnostics were not published")
	case <-ctx.Done():
		t.Fatal("fixture readiness deadline expired")
	}
	u, err := url.Parse(ready.Origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" {
		t.Fatal("fixture readiness did not select its own loopback origin")
	}
	return ready, stop
}

func readFixtureStats(t *testing.T, origin, key string, status int) reference.Stats {
	t.Helper()
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, origin+"/fixture/stats", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("cannot read the actual fixture stats endpoint")
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		t.Fatalf("fixture stats status=%d want=%d", response.StatusCode, status)
	}
	var stats reference.Stats
	if status == http.StatusOK && json.NewDecoder(response.Body).Decode(&stats) != nil {
		t.Fatal("invalid actual fixture stats JSON")
	}
	return stats
}

func assertNoFixtureConnections(t *testing.T, database fixtureSQL) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var count int
	if database.admin.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE application_name=$1", database.schema).Scan(&count) != nil || count != 0 {
		t.Fatal("owned fixture SQL connections survived process shutdown")
	}
}

func TestFixtureExecutableUsesRealSQLCacheHistoryAndOwnsCleanup(t *testing.T) {
	t.Parallel()
	binary := fixtureBinary(t)
	missing := exec.CommandContext(t.Context(), binary)
	missing.Env = []string{}
	var missingOutput bytes.Buffer
	missing.Stdout = &missingOutput
	if missing.Run() == nil || missing.ProcessState.ExitCode() != 1 || missingOutput.Len() != 0 {
		t.Fatal("actual fixture command accepted empty configuration or published readiness")
	}
	first, second := ownedSQL(t), ownedSQL(t)
	const scenario = `{"schemaVersion":"prls.reference-gateway-fixture.scenario.v1","operations":[{"operationPattern":"^fixture-sql-operation$","steps":[{"match":{"path":"/v1/responses","modelId":"fixture-query-model","contains":"fixture SQL object"},"kind":"output","text":"{\"ok\":true}","searchSources":[{"url":"https://example.invalid/company","title":"Independent fixture source"}]}]}]}`
	ready, stop := startFixture(t, binary, first.url, scenario, os.Interrupt)
	client, err := httpclient.New(httpclient.Config{BaseURL: ready.Origin, APIKey: testKey})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := client.ListProfiles(t.Context())
	if err != nil || len(profiles.Profiles) != 2 {
		t.Fatal("actual gateway cannot list its two canonical fixture profiles")
	}
	providerURL := profiles.Profiles[0].Definition.BaseURL
	input := httpclient.RunInput{ProfileID: ready.QueryProfileID, UserPrompt: "fixture SQL object", CallType: hardenllm.CallTypeStructured, Schema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`), CacheMode: hardenllm.CacheModeCache, Origin: hardenllm.Origin{Client: "query-analytics", OperationID: "fixture-sql-operation"}, RecoveryPolicy: json.RawMessage(`{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`)}
	input.WebSearch = true
	seed, err := client.Run(t.Context(), input)
	if err != nil || string(seed.Result.Output) != `{"ok":true}` || seed.Evidence == nil || seed.Evidence.ProviderAttemptCount != 1 || seed.Recording.Status != "saved" {
		t.Fatalf("actual SQL-backed seed failed: recording=%s err=%v", seed.Recording.Status, err)
	}
	replay, err := client.Run(t.Context(), input)
	if err != nil || string(replay.Result.Output) != `{"ok":true}` || replay.Evidence == nil || replay.Evidence.ProviderAttemptCount != 0 || replay.Result.Cache.Status != "hit" || replay.Recording.Status != "saved" || replay.Recording.RunID == seed.Recording.RunID {
		t.Fatal("actual SQL cache replay/history was not independent of inference")
	}
	expectedSources := []hardenllm.SearchSource{{URL: "https://example.invalid/company", Title: "Independent fixture source"}}
	for _, result := range []httpclient.RunOutcome{seed, replay} {
		if result.Result.Search == nil || !result.Result.Search.Executed || result.Result.Search.Mode != "native" || !reflect.DeepEqual(result.Result.Search.Sources, expectedSources) {
			t.Fatal("actual native search evidence was lost in execution or SQL cache replay")
		}
	}
	stats := readFixtureStats(t, ready.Origin, testKey, http.StatusOK)
	if stats.RunPostCount != 2 || stats.ProviderRequestCount != 1 || stats.UnmatchedRunCount != 0 || stats.UnmatchedProviderCount != 0 || stats.Operations["fixture-sql-operation"].ProviderDispatchCount != 1 {
		t.Fatalf("actual CLI Run/provider counts differ: %#v", stats)
	}
	readFixtureStats(t, ready.Origin, strings.Repeat("wrong-fixture-key", 3), http.StatusUnauthorized)
	wrong, _ := httpclient.New(httpclient.Config{BaseURL: ready.Origin, APIKey: strings.Repeat("wrong-fixture-key", 3)})
	_, err = wrong.Run(t.Context(), input)
	var rejected *httpclient.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatal("real owner accepted the wrong bearer")
	}
	if after := readFixtureStats(t, ready.Origin, testKey, http.StatusOK); after.RunPostCount != 2 || after.ProviderRequestCount != 1 {
		t.Fatal("rejected auth or read caused inference effects")
	}
	keyHash := sha256.Sum256([]byte(testKey))
	var historyCount, cacheCount int
	if first.pool.QueryRow(t.Context(), "SELECT count(*) FROM history WHERE api_key_hash=$1", hex.EncodeToString(keyHash[:])).Scan(&historyCount) != nil || historyCount != 2 || first.pool.QueryRow(t.Context(), "SELECT count(*) FROM cache_entries WHERE api_key_hash=$1", hex.EncodeToString(keyHash[:])).Scan(&cacheCount) != nil || cacheCount != 1 {
		t.Fatal("claimed history/cache were not persisted in the actual owned schema")
	}
	for _, id := range []string{seed.Recording.RunID, replay.Recording.RunID} {
		var recordedOutput bool
		if first.pool.QueryRow(t.Context(), "SELECT (result->'output'->>'ok')::boolean FROM history WHERE run_id=$1", id).Scan(&recordedOutput) != nil || !recordedOutput {
			t.Fatal("actual persisted history does not contain the independently expected result")
		}
		var recordedSearch bool
		if first.pool.QueryRow(t.Context(), "SELECT (result->'search'->>'executed')::boolean AND result->'search'->>'mode'='native' AND (result->'search'->'sources')::jsonb=$2::jsonb FROM history WHERE run_id=$1", id, `[{"url":"https://example.invalid/company","title":"Independent fixture source"}]`).Scan(&recordedSearch) != nil || !recordedSearch {
			t.Fatal("actual persisted history does not contain the independently expected native search evidence")
		}
	}
	otherReady, stopOther := startFixture(t, binary, second.url, strings.Replace(scenario, `true`, `false`, 1), syscall.SIGTERM)
	other, _ := httpclient.New(httpclient.Config{BaseURL: otherReady.Origin, APIKey: testKey})
	otherResult, err := other.Run(t.Context(), input)
	if err != nil || string(otherResult.Result.Output) != `{"ok":false}` || otherResult.Evidence == nil || otherResult.Evidence.ProviderAttemptCount != 1 {
		t.Fatal("second owned schema did not execute its own independent model scenario")
	}
	stop()
	assertNoFixtureConnections(t, first)
	for _, address := range []string{ready.Origin, providerURL} {
		u, _ := url.Parse(address)
		connection, err := net.DialTimeout("tcp", u.Host, time.Second)
		if err == nil {
			_ = connection.Close()
			t.Fatal("owned gateway/provider listener survived shutdown")
		}
	}
	if _, err := other.Run(t.Context(), input); err != nil || readFixtureStats(t, otherReady.Origin, testKey, http.StatusOK).ProviderRequestCount != 1 {
		t.Fatal("first fixture cleanup affected its sibling or repeated inference")
	}
	reopened, stopReopened := startFixture(t, binary, first.url, scenario, syscall.SIGTERM)
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, reopened.Origin+"/api/v1/history?pageSize=10", nil)
	request.Header.Set("Authorization", "Bearer "+testKey)
	historyClient := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second}
	defer historyClient.CloseIdleConnections()
	response, err := historyClient.Do(request)
	if err != nil {
		t.Fatal("cannot read reopened actual owner history")
	}
	data, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || !bytes.Contains(data, []byte(seed.Recording.RunID)) || !bytes.Contains(data, []byte(replay.Recording.RunID)) || readFixtureStats(t, reopened.Origin, testKey, http.StatusOK).ProviderRequestCount != 0 {
		t.Fatal("actual restart lost persisted history or history read dispatched inference")
	}
	// A fresh local provider port changes the real cache namespace. Do not
	// override that identity or pretend this restart certifies a cache hit.
	stopReopened()
	stopOther()
	assertNoFixtureConnections(t, first)
	assertNoFixtureConnections(t, second)
}

type failingReadyWriter struct{}

func (failingReadyWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestFixtureSQLStartupRefusesMissingOrIncompatibleScopeAndCleansFailure(t *testing.T) {
	t.Parallel()
	database := ownedSQL(t)
	env := fixtureEnvironment(t)
	env["PRLS_FIXTURE_DATABASE_URL"] = strings.Replace(database.url, database.schema, "hllm_fixture_"+strings.Repeat("f", 32), 1)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := run(ctx, func(key string) string { return env[key] }, &output); err == nil || output.Len() != 0 {
		t.Fatal("missing schema was created implicitly or published readiness")
	}
	env["PRLS_FIXTURE_DATABASE_URL"] = database.url
	if err := run(ctx, func(key string) string { return env[key] }, failingReadyWriter{}); err == nil {
		t.Fatal("readiness write failure was accepted")
	}
	assertNoFixtureConnections(t, database)
	// Alter only this test's already initialized schema; the owner must refuse
	// an incompatible version without resetting, migrating or erasing it.
	if _, err := database.pool.Exec(ctx, "ALTER TABLE hllm_schema_version DROP CONSTRAINT hllm_schema_version_version_check; UPDATE hllm_schema_version SET version=2"); err != nil {
		t.Fatal("cannot prepare the owned incompatible-version fixture")
	}
	if err := run(ctx, func(key string) string { return env[key] }, &output); err == nil || output.Len() != 0 {
		t.Fatal("incompatible owned schema was reset or published readiness")
	}
	var version int
	if database.pool.QueryRow(ctx, "SELECT version FROM hllm_schema_version").Scan(&version) != nil || version != 2 {
		t.Fatal("failed startup changed the incompatible schema")
	}
	assertNoFixtureConnections(t, database)
}
