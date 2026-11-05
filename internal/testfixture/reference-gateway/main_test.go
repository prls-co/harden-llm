package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-485

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/testfixture/reference"
	"github.com/prls-co/harden-llm/stateful"
)

const testKey = "synthetic-reference-fixture-client-key-not-production"
const testSchema = "hllm_fixture_0123456789abcdef0123456789abcdef"
const testDSN = "postgres://fixture:private-marker@127.0.0.1:55432/ci_fixture?sslmode=disable&search_path=" + testSchema
const processScenario = `{"schemaVersion":"prls.reference-gateway-fixture.scenario.v1","operations":[{"operationPattern":"^fixture-process-probe$","steps":[{"match":{"path":"/v1/responses","modelId":"fixture-query-model","contains":"fixture egress probe"},"kind":"output","text":"fixture text"}]}]}`

type startupOutput struct {
	buffer bytes.Buffer
	line   chan []byte
	sent   bool
}

// exec owns the sole writer; the test reads Buffer only after Wait completes.
func (output *startupOutput) Write(data []byte) (int, error) {
	n, err := output.buffer.Write(data)
	if !output.sent {
		if end := bytes.IndexByte(output.buffer.Bytes(), '\n'); end >= 0 {
			output.line <- bytes.Clone(output.buffer.Bytes()[:end+1])
			output.sent = true
		}
	}
	return n, err
}

func (output *startupOutput) String() string { return output.buffer.String() }

func TestFixtureStartupOutputReceivesExecCopy(t *testing.T) {
	t.Parallel()
	output := &startupOutput{line: make(chan []byte, 1)}
	const ready = "{\"origin\":\"http://127.0.0.1:12345\"}\n"
	if _, err := io.Copy(output, io.LimitReader(strings.NewReader(ready), 1024)); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-output.line:
		if string(line) != ready {
			t.Fatal("exec copy changed readiness")
		}
	default:
		t.Fatal("exec copy bypassed the readiness writer")
	}
}

func fixtureEnvironment(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenario.json")
	if err := os.WriteFile(path, []byte(processScenario), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"PRLS_FIXTURE_CLIENT_KEY":    testKey,
		"PRLS_FIXTURE_DATABASE_URL":  testDSN,
		"PRLS_FIXTURE_SCENARIO_FILE": path,
	}
}

func TestFixtureConfigurationRequiresExplicitSyntheticInputs(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"PRLS_FIXTURE_CLIENT_KEY", "PRLS_FIXTURE_DATABASE_URL", "PRLS_FIXTURE_SCENARIO_FILE"} {
		t.Run("missing_"+name, func(t *testing.T) {
			env := fixtureEnvironment(t)
			delete(env, name)
			var output bytes.Buffer
			if err := run(t.Context(), func(key string) string { return env[key] }, &output); err == nil || strings.Contains(err.Error(), "private-marker") || output.Len() != 0 {
				t.Fatalf("missing input accepted or startup details published: %v", err)
			}
		})
	}
	for _, example := range []struct{ name, key, value string }{
		{"short_key", "PRLS_FIXTURE_CLIENT_KEY", "private-marker"},
		{"header_key", "PRLS_FIXTURE_CLIENT_KEY", testKey + "\nprivate-marker"},
		{"relative_scenario", "PRLS_FIXTURE_SCENARIO_FILE", "private-marker.json"},
		{"missing_scenario", "PRLS_FIXTURE_SCENARIO_FILE", filepath.Join(t.TempDir(), "private-marker.json")},
		{"no_schema", "PRLS_FIXTURE_DATABASE_URL", strings.Replace(testDSN, "&search_path="+testSchema, "", 1)},
		{"public_schema", "PRLS_FIXTURE_DATABASE_URL", strings.Replace(testDSN, testSchema, "public", 1)},
		{"multiple_schemas", "PRLS_FIXTURE_DATABASE_URL", testDSN + ",public"},
		{"no_host", "PRLS_FIXTURE_DATABASE_URL", "postgres:///ci_fixture?search_path=" + testSchema},
		{"no_port", "PRLS_FIXTURE_DATABASE_URL", strings.Replace(testDSN, ":55432", "", 1)},
		{"no_password", "PRLS_FIXTURE_DATABASE_URL", strings.Replace(testDSN, ":private-marker", "", 1)},
		{"no_database", "PRLS_FIXTURE_DATABASE_URL", strings.Replace(testDSN, "/ci_fixture", "/", 1)},
		{"remote_database", "PRLS_FIXTURE_DATABASE_URL", strings.Replace(testDSN, "127.0.0.1", "example.invalid", 1)},
		{"implicit_tls", "PRLS_FIXTURE_DATABASE_URL", strings.Replace(testDSN, "sslmode=disable&", "", 1)},
		{"extra_pg_options", "PRLS_FIXTURE_DATABASE_URL", testDSN + "&options=private-marker"},
	} {
		t.Run(example.name, func(t *testing.T) {
			env := fixtureEnvironment(t)
			env[example.key] = example.value
			if _, err := readConfig(func(key string) string { return env[key] }); err == nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("unsafe/implicit fixture configuration accepted or exposed: %v", err)
			}
		})
	}
	env := fixtureEnvironment(t)
	config, err := readConfig(func(key string) string { return env[key] })
	if err != nil || config.schema != testSchema || config.pool.ConnConfig.Host != "127.0.0.1" || config.pool.ConnConfig.Port != 55432 || config.pool.ConnConfig.Database != "ci_fixture" || config.pool.ConnConfig.User != "fixture" || config.pool.ConnConfig.Password != "private-marker" || len(config.pool.ConnConfig.Fallbacks) != 0 || config.pool.MaxConns > 4 {
		t.Fatal("explicit SQL scope was not retained without fallback connections")
	}
	if config.pool.ConnConfig.RuntimeParams["search_path"] != testSchema || config.pool.ConnConfig.RuntimeParams["application_name"] != testSchema {
		t.Fatal("fixture pool can resolve outside its single owned schema or lose cleanup identity")
	}
}

func TestFixtureProfilesPreserveCanonicalCapabilities(t *testing.T) {
	t.Parallel()
	profiles, err := fixtureProfiles("https://127.0.0.1:12345")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := stateful.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatal("fixture needs exactly query and utility profiles")
	}
	for id, model := range map[string]string{queryProfileID: "fixture-query-model", utilityProfileID: "fixture-utility-model"} {
		profile, ok := profiles[id]
		if !ok || profile.Binding != canonical["CPA gpt-6.1-sol"].Binding {
			t.Fatal("fixture invented a credential binding")
		}
		for _, example := range []struct{ actual, original json.RawMessage }{
			{profile.Definition, canonical["CPA gpt-6.1-sol"].Definition},
			{profile.Inputs, canonical["CPA gpt-6.1-sol"].Inputs},
		} {
			var actual, original map[string]any
			if json.Unmarshal(example.actual, &actual) != nil || json.Unmarshal(example.original, &original) != nil || actual["modelId"] != model {
				t.Fatal("invalid fixture model override")
			}
			if _, definition := original["baseUrl"]; definition {
				if actual["baseUrl"] != "https://127.0.0.1:12345/v1" {
					t.Fatal("fixture retained an external provider endpoint")
				}
				delete(actual, "baseUrl")
				delete(original, "baseUrl")
			}
			delete(actual, "modelId")
			delete(original, "modelId")
			if !reflect.DeepEqual(actual, original) {
				t.Fatal("fixture rewrote canonical structured/reasoning/recovery/search capabilities")
			}
		}
	}
}

func TestFixtureProviderCannotReachAnotherOriginOrPort(t *testing.T) {
	t.Parallel()
	scenario, err := reference.Decode(strings.NewReader(processScenario))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	provider := startProvider(ctx, scenario.Provider())
	defer provider.Close()
	ownedProfiles, err := fixtureProfiles(provider.URL)
	if err != nil {
		t.Fatal(err)
	}
	options, err := fixtureClient(provider, ownedProfiles[queryProfileID])
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"127.0.0.1:1", "example.invalid:443", "[::1]:443"} {
		connection, err := options.EndpointPolicy.DialContext(ctx, "tcp", address)
		if err == nil || connection != nil {
			t.Fatal("fixture egress dial accepted an unowned socket")
		}
	}
	for _, origin := range []string{provider.URL, "https://example.invalid", "https://127.0.0.1:1"} {
		profiles, err := fixtureProfiles(origin)
		if err != nil {
			t.Fatal(err)
		}
		service, err := stateful.NewService(stateful.Config{Presets: profiles, Client: options})
		if err != nil {
			t.Fatal(err)
		}
		finish, err := scenario.BeginRun("fixture-process-probe")
		if err != nil {
			t.Fatal(err)
		}
		result, _, err := service.Run(ctx, strings.Repeat("a", 64), stateful.RunInput{ProfileID: queryProfileID, UserPrompt: "fixture egress probe", CallType: hardenllm.CallTypeText, CacheMode: hardenllm.CacheModeOff, RecoveryPolicy: json.RawMessage(`{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`)})
		finish()
		if origin == provider.URL {
			if err != nil || result.Output != "fixture text" {
				t.Fatalf("owned provider control did not execute: %v", err)
			}
		} else if err == nil {
			t.Fatal("actual stateful/provider path accepted an unowned origin")
		}
	}
	if stats := scenario.Stats(); stats.ProviderRequestCount != 1 || stats.UnmatchedProviderCount != 0 {
		t.Fatalf("out-of-fixture origin caused HTTP effects: %#v", stats)
	}
}

func TestFixtureStartupDoesNotPublishReadinessOnDatabaseFailure(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.Accept()
		if err == nil {
			_ = connection.Close()
		}
	}()
	env := fixtureEnvironment(t)
	u, _ := url.Parse(testDSN)
	u.Host = listener.Addr().String()
	env["PRLS_FIXTURE_DATABASE_URL"] = u.String()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := run(ctx, func(key string) string { return env[key] }, &output); err == nil || output.Len() != 0 || strings.Contains(err.Error(), "private-marker") {
		t.Fatalf("failed SQL startup published readiness or a credential: %v", err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("owned SQL failure listener was not reached")
	}
}
