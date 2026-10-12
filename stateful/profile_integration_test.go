//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-438

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	hardenllm "github.com/prls-co/harden-llm"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var keyHashA = strings.Repeat("a", 64)
var keyHashB = strings.Repeat("b", 64)

func serviceFixture(t *testing.T, pool *pgxpool.Pool, handler http.HandlerFunc) (*Service, SaveProfileInput) {
	t.Helper()
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/responses" {
				t.Errorf("unexpected local provider path %s", r.URL.Path)
				http.NotFound(w, r)
				return
			}
			writeFixtureOK(w)
		}
	}
	provider := httptest.NewTLSServer(handler)
	t.Cleanup(provider.Close)
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	catalog := fixtureCatalog(t)
	for id, p := range catalog {
		var def map[string]any
		if err := json.Unmarshal(p.Definition, &def); err != nil {
			t.Fatal(err)
		}
		def["baseUrl"] = provider.URL + "/v1"
		p.Definition, _ = json.Marshal(def)
		p.Binding = ProfileCredentialBinding{Source: "deployment", DeploymentUpstreamID: "cpa"}
		catalog[id] = p
	}
	vault, err := NewCredentialVault(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewService(Config{Client: ClientOptions{Connections: []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: provider.URL + "/v1", APIKey: "deployment-fixture-secret"}}, DefaultConnection: "cpa", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}, Pool: pool, Presets: catalog, Vault: vault})
	if err != nil {
		t.Fatal(err)
	}
	p := catalog["CPA Responses"]
	return s, SaveProfileInput{Name: p.Name, PresetID: "CPA Responses", Definition: p.Definition, Inputs: p.Inputs, Binding: p.Binding}
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var op *OperationError
	var val *ValidationError
	if errors.As(err, &op) && op.Code == code || errors.As(err, &val) && val.Code == code {
		return
	}
	t.Fatalf("expected code %s, got %v", code, err)
}
func countRows(t *testing.T, p *pgxpool.Pool, table, hash string) int {
	t.Helper()
	var count int
	// Table is a test-owned literal, never supplied by a product request.
	if err := p.QueryRow(context.Background(), "SELECT count(*) FROM "+table+" WHERE api_key_hash=$1", hash).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func TestProfilePersistence(t *testing.T) {
	t.Parallel()
	t.Run("save list and full detail are hash scoped", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		view, err := s.SaveProfile(context.Background(), keyHashA, input)
		if err != nil {
			t.Fatal(err)
		}
		a, err := s.ListProfiles(context.Background(), keyHashA)
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.ListProfiles(context.Background(), keyHashB)
		if err != nil {
			t.Fatal(err)
		}
		if len(a) != len(b)+1 || countRows(t, p, "profiles", keyHashA) != 1 || view.Kind != "saved" {
			t.Fatal("saved snapshot not scoped")
		}
		detail, err := s.GetProfile(context.Background(), keyHashA, view.ID)
		if err != nil || !sameJSON(detail.Definition, input.Definition) || !sameJSON(detail.Inputs, input.Inputs) {
			t.Fatal("saved full setup differs", err)
		}
		_, err = s.GetProfile(context.Background(), keyHashB, view.ID)
		requireCode(t, err, "profile_not_found")
	})
	t.Run("concurrent identical saves create different immutable UUIDs", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		results := make(chan ProfileView, 2)
		failures := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				view, err := s.SaveProfile(context.Background(), keyHashA, input)
				results <- view
				failures <- err
			})
		}
		wg.Wait()
		close(results)
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		one, two := <-results, <-results
		if one.ID == two.ID || one.ID == "" || countRows(t, p, "profiles", keyHashA) != 2 {
			t.Fatal("identical explicit saves deduplicated or overwrote")
		}
	})
	t.Run("editing setup creates another row and retains original bytes", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		one, err := s.SaveProfile(context.Background(), keyHashA, input)
		if err != nil {
			t.Fatal(err)
		}
		before, err := s.GetProfile(context.Background(), keyHashA, one.ID)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(before)
		input.Inputs = json.RawMessage(`{"userPrompt":"edited prompt","cacheMode":"off","timeoutMs":0}`)
		two, err := s.SaveProfile(context.Background(), keyHashA, input)
		if err != nil {
			t.Fatal(err)
		}
		after, err := s.GetProfile(context.Background(), keyHashA, one.ID)
		if err != nil {
			t.Fatal(err)
		}
		encodedAfter, _ := json.Marshal(after)
		if one.ID == two.ID || !bytes.Equal(encoded, encodedAfter) || countRows(t, p, "profiles", keyHashA) != 2 {
			t.Fatal("old snapshot was overwritten")
		}
	})
	t.Run("two services on same pool and access key share state", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		view, err := s.SaveProfile(context.Background(), keyHashA, input)
		if err != nil {
			t.Fatal(err)
		}
		other, err := NewService(s.config)
		if err != nil {
			t.Fatal(err)
		}
		draft, err := other.GetProfile(context.Background(), keyHashA, view.ID)
		if err != nil || !sameJSON(draft.Inputs, input.Inputs) {
			t.Fatal("same-key state not shared", err)
		}
	})
	t.Run("new entered credential and snapshot commit or roll back together", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		input = enteredSnapshot(t, input, "entered")
		s.afterCredentialInsert = func() error { return errors.New("transaction fixture failure") }
		if _, err := s.SaveProfile(context.Background(), keyHashA, input); err == nil {
			t.Fatal("injected SQL failure ignored")
		}
		if countRows(t, p, "credentials", keyHashA) != 0 || countRows(t, p, "profiles", keyHashA) != 0 {
			t.Fatal("partial atomic save persisted")
		}
		s.afterCredentialInsert = nil
		view, err := s.SaveProfile(context.Background(), keyHashA, input)
		if err != nil {
			t.Fatal(err)
		}
		if countRows(t, p, "credentials", keyHashA) != 1 || countRows(t, p, "profiles", keyHashA) != 1 || !view.CredentialStatus.Configured {
			t.Fatal("atomic new credential save missing")
		}
	})
	t.Run("stale credential replacement preserves ciphertext", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		input = enteredSnapshot(t, input, "entered")
		if _, err := s.SaveProfile(context.Background(), keyHashA, input); err != nil {
			t.Fatal(err)
		}
		var ciphertext []byte
		if err := p.QueryRow(context.Background(), "SELECT ciphertext FROM credentials WHERE api_key_hash=$1 AND id='entered'", keyHashA).Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		input.Credential.Payload.APIKey = "changed-secret"
		_, err := s.SaveProfile(context.Background(), keyHashA, input)
		requireCode(t, err, "revision_conflict")
		var after []byte
		if err := p.QueryRow(context.Background(), "SELECT ciphertext FROM credentials WHERE api_key_hash=$1 AND id='entered'", keyHashA).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ciphertext, after) || countRows(t, p, "profiles", keyHashA) != 1 {
			t.Fatal("stale save changed credential or snapshot")
		}
	})
	t.Run("delete saved and hide template are exact scope operations", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		view, err := s.SaveProfile(context.Background(), keyHashA, input)
		if err != nil {
			t.Fatal(err)
		}
		requireCode(t, s.DeleteProfile(context.Background(), keyHashB, view.ID), "profile_not_found")
		if err := s.DeleteProfile(context.Background(), keyHashA, view.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteProfile(context.Background(), keyHashA, "CPA Responses"); err != nil {
			t.Fatal(err)
		}
		_, err = s.GetProfile(context.Background(), keyHashA, "CPA Responses")
		requireCode(t, err, "preset_not_found")
		if _, err := s.GetProfile(context.Background(), keyHashB, "CPA Responses"); err != nil {
			t.Fatal(err)
		}
		if countRows(t, p, "profiles", keyHashA) != 0 || countRows(t, p, "hidden_presets", keyHashA) != 1 || countRows(t, p, "hidden_presets", keyHashB) != 0 {
			t.Fatal("delete crossed scope")
		}
	})
	t.Run("foreign entered credential fails before probe", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		foreign := enteredSnapshot(t, input, "entered")
		if _, err := s.SaveProfile(context.Background(), keyHashB, foreign); err != nil {
			t.Fatal(err)
		}
		foreign.Credential = nil
		_, err := s.SaveProfile(context.Background(), keyHashA, foreign)
		requireCode(t, err, "credential_invalid")
		if countRows(t, p, "profiles", keyHashA) != 0 {
			t.Fatal("foreign credential produced snapshot")
		}
	})
	t.Run("restart and template replacement preserve saved setup and binding", func(t *testing.T) {
		p, dsn := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		view, err := s.SaveProfile(context.Background(), keyHashA, input)
		if err != nil {
			t.Fatal(err)
		}
		before, err := s.GetProfile(context.Background(), keyHashA, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(before)
		p.Close()
		reopened, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(reopened.Close)
		config := s.config
		config.Pool = reopened
		config.Presets = Catalog{}
		other, err := NewService(config)
		if err != nil {
			t.Fatal(err)
		}
		after, err := other.GetProfile(context.Background(), keyHashA, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		encodedAfter, _ := json.Marshal(after)
		if !bytes.Equal(encoded, encodedAfter) {
			t.Fatal("restart or Git template removal changed saved snapshot")
		}
	})
	t.Run("failed probe holds no SQL connection and saves nothing", func(t *testing.T) {
		p, _ := referencePool(t, true)
		started := make(chan struct{})
		release := make(chan struct{})
		s, input := serviceFixture(t, p, func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-release
			http.Error(w, "fixture failure", 503)
		})
		done := make(chan error, 1)
		go func() { _, err := s.SaveProfile(context.Background(), keyHashA, input); done <- err }()
		select {
		case <-started:
		case err := <-done:
			t.Fatal("probe did not reach local provider", err)
		case <-time.After(5 * time.Second):
			t.Fatal("probe not started")
		}
		acquired := p.Stat().AcquiredConns()
		rows := countRows(t, p, "profiles", keyHashA)
		close(release)
		if err := <-done; err == nil || acquired != 0 || rows != 0 {
			t.Fatal("probe held connection or saved failed candidate", err, acquired, rows)
		}
	})
}
func enteredSnapshot(t *testing.T, input SaveProfileInput, id string) SaveProfileInput {
	t.Helper()
	var def struct {
		BaseURL string `json:"baseUrl"`
	}
	if err := json.Unmarshal(input.Definition, &def); err != nil {
		t.Fatal(err)
	}
	origin := strings.TrimSuffix(def.BaseURL, "/v1")
	input.Binding = ProfileCredentialBinding{Source: "entered", EnteredCredentialID: id}
	input.Credential = &EnteredCredentialInput{ID: id, Revision: 0, Origin: origin, Payload: CredentialPayload{APIKey: "entered-fixture-secret", Headers: map[string]string{"X-Fixture": "header-secret"}}}
	return input
}

func writeFixtureOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"fixture","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`))
}
