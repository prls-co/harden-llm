//go:build integration

package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-441

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	hardenllm "github.com/prls-co/harden-llm"
	contract "github.com/prls-co/harden-llm/api"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/stateful"
)

const referenceContractPolicy = `{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`

type referenceHTTPFixture struct {
	api        *API
	server     *httptest.Server
	pool       *pgxpool.Pool
	service    *stateful.Service
	keyA, keyB string
	calls      *atomic.Int32
	snapshot   stateful.SaveProfileInput
}

func referenceHTTP(t *testing.T, history, artifacts bool) referenceHTTPFixture {
	t.Helper()
	return referenceHTTPConfigured(t, history, artifacts, nil, nil)
}

func referenceHTTPConfigured(t *testing.T, history, artifacts bool, presets stateful.Catalog, native http.HandlerFunc) referenceHTTPFixture {
	t.Helper()
	_, dsn := integrationtest.PostgresLease(t)
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 2
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = stateful.ApplySchema(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int32{}
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if native != nil {
			if r.Method == http.MethodPost {
				calls.Add(1)
			}
			native(w, r)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"fixture-model","object":"model","owned_by":"fixture"}]}`)
			return
		}
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		_, _ = io.WriteString(w, `{"id":"fixture-response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}],"output_text":"OK","usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`)
	}))
	t.Cleanup(provider.Close)
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	if presets == nil {
		data, err := os.ReadFile("../../../test/fixtures/reference/presets.json")
		if err != nil {
			t.Fatal(err)
		}
		presets, err = stateful.ParseCatalog(data)
		if err != nil {
			t.Fatal(err)
		}
	}
	connections := []hardenllm.Connection{}
	seen := map[string]bool{}
	for id, preset := range presets {
		var definition map[string]any
		if json.Unmarshal(preset.Definition, &definition) != nil {
			t.Fatal("invalid shared preset")
		}
		base := provider.URL + "/v1"
		if definition["provider"] == "google" {
			base = provider.URL
		}
		definition["baseUrl"] = base
		preset.Definition, _ = json.Marshal(definition)
		binding := preset.Binding.DeploymentUpstreamID
		if !seen[binding] {
			connections = append(connections, hardenllm.Connection{ID: binding, Provider: definition["provider"].(string), Protocol: definition["apiInferenceType"].(string), BaseURL: base, APIKey: "fixture-provider-secret"})
			seen[binding] = true
		}
		presets[id] = preset
	}
	keyA, keyB := "synthetic-key-a-"+uuid.NewString(), "synthetic-key-b-"+uuid.NewString()
	var store *stateful.ArtifactStore
	if artifacts {
		digest := sha256.Sum256([]byte(keyA))
		_, garage := integrationtest.GarageArtifactLease(t, hex.EncodeToString(digest[:]))
		other := sha256.Sum256([]byte(keyB))
		_, _ = integrationtest.GarageArtifactLease(t, hex.EncodeToString(other[:]))
		store, err = stateful.NewArtifactStore(stateful.ArtifactOptions{Endpoint: garage.Endpoint, Bucket: garage.Bucket, Region: garage.Region, AccessKeyID: garage.AccessKeyID, SecretAccessKey: garage.SecretAccessKey})
		if err != nil {
			t.Fatal(err)
		}
	}
	vault, err := stateful.NewCredentialVault(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x35}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	service, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{Connections: connections, DefaultConnection: connections[0].ID, EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}, Pool: pool, Presets: presets, Vault: vault, History: stateful.HistoryConfig{Enabled: history}, Artifacts: store})
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(Config{APIKeys: []string{keyA, keyB}, Service: service})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	ids := make([]string, 0, len(presets))
	for id := range presets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	preset := presets[ids[0]]
	return referenceHTTPFixture{api: api, server: server, pool: pool, service: service, keyA: keyA, keyB: keyB, calls: calls, snapshot: stateful.SaveProfileInput{Name: preset.Name, PresetID: "CPA Responses", Definition: preset.Definition, Inputs: preset.Inputs, Binding: preset.Binding}}
}

func (f referenceHTTPFixture) request(t *testing.T, key, method, path string, body any, accept string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, f.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	response, err := f.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func referenceBody(t *testing.T, response *http.Response, status int, schema string, out any) []byte {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 18<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		if len(data) > 2000 {
			data = data[:2000]
		}
		t.Fatalf("status %d, expected %d: %s", response.StatusCode, status, data)
	}
	if schema != "" {
		if err = contract.Validate(schema, data); err != nil {
			t.Fatal("body violates canonical schema", schema, err)
		}
	}
	if out != nil {
		if err = json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
	}
	return data
}

func referenceRequest() stateful.RunInput {
	return stateful.RunInput{ProfileID: "CPA Responses", UserPrompt: "Reply with OK.", CallType: hardenllm.CallTypeText, RecoveryPolicy: json.RawMessage(referenceContractPolicy)}
}

func TestReferenceContract(t *testing.T) {
	t.Parallel()
	t.Run("every companion method requires one bearer and rejects caller scope", func(t *testing.T) {
		f := referenceHTTP(t, true, false)
		id := "00000000-0000-4000-8000-000000000001"
		for _, route := range []struct{ method, path string }{
			{"GET", "/api/v1/status"}, {"GET", "/api/v1/profiles"}, {"POST", "/api/v1/profiles"}, {"GET", "/api/v1/profiles/" + id}, {"DELETE", "/api/v1/profiles/" + id}, {"POST", "/api/v1/profiles/" + id + "/models:refresh"}, {"GET", "/api/v1/profiles/bundle"}, {"PUT", "/api/v1/profiles/bundle"}, {"GET", "/api/v1/state?draftKey=workspace:fixture"}, {"POST", "/api/v1/state"}, {"POST", "/api/v1/run"}, {"GET", "/api/v1/history"}, {"DELETE", "/api/v1/history"}, {"GET", "/api/v1/history/" + id}, {"DELETE", "/api/v1/history/" + id}, {"GET", "/api/v1/stats"}, {"GET", "/api/v1/traces/" + id}, {"GET", "/api/v1/traces/" + id + "/artifacts/trace.json"},
		} {
			t.Run(route.method+" "+route.path, func(t *testing.T) {
				var body map[string]any
				referenceBody(t, f.request(t, "", route.method, route.path, nil, ""), 401, "ReferenceErrorEnvelope", &body)
				if body["error"].(map[string]any)["code"] != "invalid_api_key" {
					t.Fatal("wrong bearer failure")
				}
			})
		}
		for _, field := range []string{"apiKeyHash", "ownerId", "userId", "sessionReference"} {
			var body map[string]any
			referenceBody(t, f.request(t, f.keyA, "POST", "/api/v1/run", map[string]any{field: "caller-scope"}, ""), 400, "ReferenceErrorEnvelope", &body)
			if body["error"].(map[string]any)["code"] != "scope_selector_rejected" {
				t.Fatal("scope field accepted", field)
			}
		}
		if f.calls.Load() != 0 {
			t.Fatal("denied access dispatched provider")
		}
	})
	t.Run("profiles snapshots refresh bundles drafts and ordinary client retain contract", func(t *testing.T) {
		f := referenceHTTP(t, true, false)
		var listed struct{ Profiles []stateful.ProfileView }
		referenceBody(t, f.request(t, f.keyA, "GET", "/api/v1/profiles", nil, ""), 200, "ProfilesEnvelope", &listed)
		if len(listed.Profiles) != 2 {
			t.Fatal("Git templates absent")
		}
		var saved struct{ Profile stateful.ProfileView }
		referenceBody(t, f.request(t, f.keyA, "POST", "/api/v1/profiles", f.snapshot, ""), 201, "ProfileEnvelope", &saved)
		if _, err := uuid.Parse(saved.Profile.ID); err != nil || saved.Profile.CreatedAt == nil || saved.Profile.Name == f.snapshot.Name {
			t.Fatal("explicit Save is not a new UUID/date/suffix snapshot")
		}
		var detail stateful.ProfileDraft
		path := "/api/v1/profiles/" + url.PathEscape(saved.Profile.ID)
		referenceBody(t, f.request(t, f.keyA, "GET", path, nil, ""), 200, "ProfileDraft", &detail)
		if !bytes.Equal(canonicalReferenceJSON(t, detail.Inputs), canonicalReferenceJSON(t, f.snapshot.Inputs)) {
			t.Fatal("saved full inputs changed")
		}
		referenceBody(t, f.request(t, f.keyB, "GET", path, nil, ""), 404, "ReferenceErrorEnvelope", nil)
		referenceBody(t, f.request(t, f.keyA, "GET", path, nil, ""), 200, "ProfileDraft", nil)
		var refreshed stateful.ProfileDraft
		referenceBody(t, f.request(t, f.keyA, "POST", path+"/models:refresh", nil, ""), 200, "ProfileDraft", &refreshed)
		if !bytes.Contains(refreshed.Definition, []byte("fixture-model")) {
			t.Fatal("model refresh absent")
		}
		referenceBody(t, f.request(t, f.keyA, "GET", path, nil, ""), 200, "ProfileDraft", &detail)
		if bytes.Contains(detail.Definition, []byte("fixture-model")) {
			t.Fatal("refresh mutated immutable saved definition")
		}
		var bundle stateful.ProfileBundle
		referenceBody(t, f.request(t, f.keyA, "GET", "/api/v1/profiles/bundle", nil, ""), 200, "ProfileBundle", &bundle)
		if len(bundle.Profiles) != 1 {
			t.Fatal("literal bundle route captured as profile")
		}
		referenceBody(t, f.request(t, f.keyA, "PUT", "/api/v1/profiles/bundle", bundle, ""), 200, "ProfilesEnvelope", nil)
		for _, hostKey := range []string{"workspace:one", "workspace:two"} {
			var draft stateful.Draft
			referenceBody(t, f.request(t, f.keyA, "POST", "/api/v1/state", map[string]any{"draftKey": hostKey, "revision": 0, "payload": map[string]any{"userPrompt": hostKey}}, ""), 200, "Draft", &draft)
			if draft.Revision != 1 {
				t.Fatal("draft create CAS changed")
			}
			referenceBody(t, f.request(t, f.keyA, "GET", "/api/v1/state?draftKey="+url.QueryEscape(hostKey), nil, ""), 200, "Draft", &draft)
			if !bytes.Contains(draft.Payload, []byte(hostKey)) {
				t.Fatal("same key/host draft not shared")
			}
			referenceBody(t, f.request(t, f.keyB, "GET", "/api/v1/state?draftKey="+url.QueryEscape(hostKey), nil, ""), 200, "Draft", &draft)
			if draft.Revision != 0 || string(draft.Payload) != "{}" {
				t.Fatal("foreign key inherited draft")
			}
		}
		var conflict map[string]any
		referenceBody(t, f.request(t, f.keyA, "POST", "/api/v1/state", map[string]any{"draftKey": "workspace:one", "revision": 0, "payload": map[string]any{}}, ""), 409, "ReferenceErrorEnvelope", &conflict)
		if conflict["error"].(map[string]any)["currentRevision"] != float64(1) {
			t.Fatal("draft conflict omitted actual revision")
		}
		referenceBody(t, f.request(t, f.keyA, "DELETE", path, nil, ""), 204, "", nil)
		referenceBody(t, f.request(t, f.keyA, "GET", path, nil, ""), 404, "ReferenceErrorEnvelope", nil)
		before := f.calls.Load()
		referenceBody(t, f.request(t, f.keyA, "POST", "/v1/responses", map[string]any{"model": "fixture-model", "input": "Reply with OK."}, ""), 200, "", nil)
		if f.calls.Load() != before+1 {
			t.Fatal("ordinary inference unavailable")
		}
		var count int
		if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM history").Scan(&count); err != nil || count != 0 {
			t.Fatal("ordinary /v1 wrote reference history", err)
		}
	})
	t.Run("JSON run full record stats artifacts isolation and prefix-first deletion", func(t *testing.T) {
		f := referenceHTTP(t, true, true)
		var envelope struct {
			Result stateful.RunOutput
			State  stateful.RunState
		}
		referenceBody(t, f.request(t, f.keyA, "POST", "/api/v1/run", referenceRequest(), ""), 200, "RunEnvelope", &envelope)
		out := envelope.Result
		if out.Output != "OK" || out.History.Status != "saved" || f.calls.Load() != 1 || envelope.State.LastRunID != out.RunID || len(out.Artifacts) != 2 {
			t.Fatal("JSON inference/recording/artifacts were not returned together", out.History, len(out.Artifacts))
		}
		var page stateful.HistoryPage
		referenceBody(t, f.request(t, f.keyA, "GET", "/api/v1/history", nil, ""), 200, "HistoryPage", &page)
		if len(page.Items) != 1 || page.Pagination.TotalCount != 1 {
			t.Fatal("saved run not shared by same key")
		}
		path := "/api/v1/history/" + out.RunID
		referenceBody(t, f.request(t, f.keyA, "GET", path, nil, ""), 200, "HistoryRecord", nil)
		referenceBody(t, f.request(t, f.keyB, "GET", path, nil, ""), 404, "ReferenceErrorEnvelope", nil)
		referenceBody(t, f.request(t, f.keyB, "DELETE", path, nil, ""), 404, "ReferenceErrorEnvelope", nil)
		var stats stateful.Statistics
		referenceBody(t, f.request(t, f.keyA, "GET", "/api/v1/stats?before="+url.QueryEscape(page.Window.Before.Format(time.RFC3339Nano)), nil, ""), 200, "Statistics", &stats)
		if stats.TotalCount != 1 || stats.Scope != "available_records" {
			t.Fatal("stats window differs from history")
		}
		referenceBody(t, f.request(t, f.keyB, "GET", "/api/v1/history", nil, ""), 200, "HistoryPage", &page)
		if page.Pagination.TotalCount != 0 {
			t.Fatal("foreign history counted")
		}
		for _, artifactPath := range []string{"/api/v1/traces/" + out.RunID, "/api/v1/traces/" + out.RunID + "/artifacts/support-bundle.json"} {
			response := f.request(t, f.keyA, "GET", artifactPath, nil, "")
			data := referenceBody(t, response, 200, "", nil)
			if !json.Valid(data) || response.Header.Get("Content-Type") != "application/json" || response.Header.Get("Content-Length") == "" {
				t.Fatal("artifact bytes/metadata absent")
			}
			referenceBody(t, f.request(t, f.keyB, "GET", artifactPath, nil, ""), 404, "ReferenceErrorEnvelope", nil)
		}
		referenceBody(t, f.request(t, f.keyA, "DELETE", path, nil, ""), 204, "", nil)
		referenceBody(t, f.request(t, f.keyA, "GET", "/api/v1/traces/"+out.RunID, nil, ""), 404, "ReferenceErrorEnvelope", nil)
		referenceBody(t, f.request(t, f.keyA, "POST", "/api/v1/run", referenceRequest(), ""), 200, "RunEnvelope", nil)
		var cleared stateful.ClearResult
		referenceBody(t, f.request(t, f.keyA, "DELETE", "/api/v1/history", nil, ""), 200, "ClearResult", &cleared)
		if !cleared.Complete || cleared.Deleted != 1 {
			t.Fatal("clear did not delete matching runs")
		}
	})
	t.Run("SSE has progress one terminal and later recording with monotonic IDs", func(t *testing.T) {
		f := referenceHTTP(t, true, false)
		response := f.request(t, f.keyA, "POST", "/api/v1/run", referenceRequest(), "text/event-stream")
		defer response.Body.Close()
		if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
			t.Fatal("reference SSE unavailable", response.StatusCode)
		}
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), 16<<20)
		var sequence uint64
		runID := ""
		progress, terminals, recordings := 0, 0, 0
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := []byte(strings.TrimPrefix(line, "data: "))
			if err := contract.Validate("ReferenceRunProgressEvent", data); err != nil {
				t.Fatal(err)
			}
			var event struct {
				Sequence    uint64
				RunID, Type string
				Data        json.RawMessage
			}
			if json.Unmarshal(data, &event) != nil {
				t.Fatal("invalid SSE event")
			}
			if event.Sequence <= sequence {
				t.Fatal("nonmonotonic event sequence")
			}
			sequence = event.Sequence
			if runID == "" {
				runID = event.RunID
			}
			if event.RunID != runID {
				t.Fatal("event changed execution identity")
			}
			switch event.Type {
			case "run.progress":
				if terminals != 0 {
					t.Fatal("progress after terminal")
				}
				progress++
			case "run.completed", "run.failed":
				if recordings != 0 {
					t.Fatal("terminal after recording")
				}
				terminals++
			case "history.recorded":
				if terminals != 1 {
					t.Fatal("recording before exactly one terminal")
				}
				recordings++
				var outcome stateful.RecordingOutcome
				if json.Unmarshal(event.Data, &outcome) != nil || outcome.Status != "saved" {
					t.Fatal("recording outcome missing")
				}
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		if progress == 0 || terminals != 1 || recordings != 1 || f.calls.Load() != 1 {
			t.Fatal("SSE duplicated or lost lifecycle", progress, terminals, recordings, f.calls.Load())
		}
	})
	t.Run("status distinguishes enabled disabled and unavailable usage", func(t *testing.T) {
		for _, mode := range []string{"enabled", "disabled", "closed"} {
			t.Run(mode, func(t *testing.T) {
				f := referenceHTTP(t, mode != "disabled", false)
				if mode == "closed" {
					f.pool.Close()
				}
				var status map[string]any
				referenceBody(t, f.request(t, f.keyA, "GET", "/api/v1/status", nil, ""), 200, "ReferenceStatus", &status)
				usage, present := status["history"]
				if mode == "disabled" {
					if present {
						t.Fatal("disabled history returned usage")
					}
					return
				}
				if !present {
					t.Fatal("enabled history omitted availability")
				}
				figures := usage.(map[string]any)
				if mode == "closed" {
					if figures["rows"] != nil || figures["tableBytes"] != nil || status["historyStatus"].(map[string]any)["status"] != "unavailable" {
						t.Fatal("unavailable usage was represented as zero")
					}
				} else if figures["rows"] != float64(0) || figures["tableBytes"].(float64) <= 0 {
					t.Fatal("available usage missing")
				}
			})
		}
	})
	t.Run("real malformed conflict oversize and storage errors keep typed envelope", func(t *testing.T) {
		f := referenceHTTP(t, true, false)
		referenceBody(t, f.request(t, f.keyA, "POST", "/api/v1/run", map[string]any{"unknown": "field"}, ""), 400, "ReferenceErrorEnvelope", nil)
		referenceBody(t, f.request(t, f.keyA, "GET", "/api/v1/history?page=0&pageSize=1", nil, ""), 400, "ReferenceErrorEnvelope", nil)
		referenceBody(t, f.request(t, f.keyA, "PUT", "/api/v1/profiles/bundle", map[string]any{"padding": strings.Repeat("x", (2<<20)+1)}, ""), 413, "ReferenceErrorEnvelope", nil)
		f.pool.Close()
		referenceBody(t, f.request(t, f.keyA, "GET", "/api/v1/profiles", nil, ""), 503, "ReferenceErrorEnvelope", nil)
		referenceBody(t, f.request(t, f.keyA, "GET", "/api/v1/history", nil, ""), 503, "ReferenceErrorEnvelope", nil)
	})
}

func canonicalReferenceJSON(t *testing.T, data []byte) []byte {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal("invalid fixture JSON", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
