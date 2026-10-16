//go:build integration && temporalintegration

package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-480
import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
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
	"github.com/prls-co/harden-llm/stateful/batch"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/encoding/protojson"
)

type temporalBatchFixture struct {
	server              *httptest.Server
	key, scope          string
	jobs                *batch.Service
	activities          *batch.Activities
	temporal            client.Client
	queue               string
	worker              worker.Worker
	calls, active, peak atomic.Int32
	ctx                 context.Context
}

func newTemporalBatchFixture(t *testing.T, throttle bool) *temporalBatchFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	f := &temporalBatchFixture{key: "batch-test-" + uuid.NewString(), queue: "batch-" + uuid.NewString(), ctx: ctx}
	sum := sha256.Sum256([]byte(f.key))
	f.scope = hex.EncodeToString(sum[:])
	_, dsn := integrationtest.PostgresLease(t)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = batch.ApplySchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	_, garage := integrationtest.GarageArtifactLease(t, f.scope)
	artifacts, err := stateful.NewArtifactStore(stateful.ArtifactOptions{Endpoint: garage.Endpoint, Bucket: garage.Bucket, Region: garage.Region, AccessKeyID: garage.AccessKeyID, SecretAccessKey: garage.SecretAccessKey})
	if err != nil {
		t.Fatal(err)
	}
	noncePattern := regexp.MustCompile(`nonce-[0-9]{4}`)
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := f.active.Add(1)
		defer f.active.Add(-1)
		for {
			p := f.peak.Load()
			if n <= p || f.peak.CompareAndSwap(p, n) {
				break
			}
		}
		call := f.calls.Add(1)
		if throttle && call == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"code":"rate_limit_exceeded"}}`)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("bad provider JSON")
			w.WriteHeader(400)
			return
		}
		raw, _ := json.Marshal(body)
		nonce := noncePattern.FindString(string(raw))
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer synthetic-cpa-key" || body["stream"] != true || body["model"] != "gpt-6-luna" || nonce == "" {
			t.Error("native one-generation request contract failed")
			w.WriteHeader(400)
			return
		}
		writePresetNativeResponse(w, r, body, fmt.Sprintf(`{"nonce":%q}`, nonce))
	}))
	t.Cleanup(provider.Close)
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	service, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: provider.URL + "/v1", APIKey: "synthetic-cpa-key"}}, DefaultConnection: "cpa", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots}, MaxConnsPerHost: 128}}})
	if err != nil {
		t.Fatal(err)
	}
	f.temporal, _ = integrationtest.TemporalLease(t)
	store := &batch.PostgresStore{Pool: pool}
	policy := batch.DefaultPolicy()
	if throttle {
		policy.Concurrency = 2
		policy.Burst = 2
	}
	f.jobs = &batch.Service{Store: store, Starter: batch.TemporalStarter{Client: f.temporal, TaskQueue: f.queue}, Artifacts: artifacts, Upstream: "cpa", JobTimeout: 2 * time.Minute}
	f.activities = &batch.Activities{Store: store, Artifacts: artifacts, Temporal: f.temporal, TaskQueue: f.queue, PoolID: uuid.NewString(), Policy: policy, Encode: EncodeBatchResponse, Execute: func(ctx context.Context, scope string, r hardenllm.Request) (hardenllm.Result, error) {
		core, err := service.ClientForKey(scope)
		if err != nil {
			return hardenllm.Result{}, err
		}
		return core.Call(ctx, r)
	}}
	startFixtureWorker(t, f)
	t.Cleanup(func() {
		if f.worker != nil {
			f.worker.Stop()
		}
	})
	api, err := New(Config{APIKeys: []string{f.key, strings.Repeat("other-synthetic-key-", 3)}, Service: service, Jobs: f.jobs})
	if err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewServer(api.Handler())
	t.Cleanup(f.server.Close)
	return f
}
func startFixtureWorker(t *testing.T, f *temporalBatchFixture) {
	t.Helper()
	w, err := batch.NewWorker(f.temporal, f.queue, f.activities)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Start(); err != nil {
		t.Fatal(err)
	}
	f.worker = w
}
func (f *temporalBatchFixture) submit(t *testing.T, n int) (batch.Status, []byte) {
	t.Helper()
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{"custom_id": fmt.Sprintf("nonce-%04d", i), "request": map[string]any{"model": "gpt-6-luna", "input": fmt.Sprintf("Return nonce-%04d as a JSON nonce object.", i), "reasoning": map[string]any{"effort": "low"}, "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "nonce", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{"nonce": map[string]any{"type": "string"}}, "required": []string{"nonce"}, "additionalProperties": false}}}}}
	}
	raw, _ := json.Marshal(map[string]any{"items": items})
	r, _ := http.NewRequestWithContext(f.ctx, "POST", f.server.URL+"/v1/jobs", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+f.key)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "fixture-job")
	response, err := f.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != 202 {
		t.Fatalf("submit: %d %s", response.StatusCode, data)
	}
	if err = contract.Validate("BatchJobStatus", data); err != nil {
		t.Fatal("status violates OpenAPI", err)
	}
	var status batch.Status
	json.Unmarshal(data, &status)
	return status, raw
}
func (f *temporalBatchFixture) wait(t *testing.T, id string, predicate func(batch.Status) bool) batch.Status {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	log := time.NewTicker(5 * time.Second)
	defer log.Stop()
	var last batch.Status
	for {
		select {
		case <-f.ctx.Done():
			t.Fatalf("job timeout: %+v", last)
		case <-log.C:
			t.Logf("batch still running: %+v", last.Counts)
		case <-ticker.C:
			v, err := f.jobs.Status(f.ctx, f.scope, id)
			if err != nil {
				t.Fatal(err)
			}
			last = v
			if predicate(v) {
				return v
			}
		}
	}
}
func (f *temporalBatchFixture) assertResults(t *testing.T, id string, n int) {
	t.Helper()
	r, _ := http.NewRequestWithContext(f.ctx, "GET", f.server.URL+"/v1/jobs/"+id+"/result", nil)
	r.Header.Set("Authorization", "Bearer "+f.key)
	response, err := f.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	scan := bufio.NewScanner(response.Body)
	scan.Buffer(make([]byte, 4096), 16<<20)
	seen := map[string]bool{}
	for scan.Scan() {
		if err := contract.Validate("BatchItemResult", scan.Bytes()); err != nil {
			t.Fatal("result violates OpenAPI", err)
		}
		var result batch.Result
		if err := json.Unmarshal(scan.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		var native struct {
			OutputText string `json:"output_text"`
		}
		json.Unmarshal(result.Response, &native)
		var value struct {
			Nonce string `json:"nonce"`
		}
		json.Unmarshal([]byte(native.OutputText), &value)
		if result.Error != nil || result.CustomID != value.Nonce || seen[value.Nonce] || result.Diagnostics.HeadersMS == nil || result.Diagnostics.OutputTokens == nil {
			t.Fatalf("unqualified or repeated result: %+v", result)
		}
		seen[value.Nonce] = true
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != n {
		t.Fatalf("received %d of %d results", len(seen), n)
	}
}
func TestTemporalBatchThousandSeparateRequests(t *testing.T) {
	t.Parallel()
	f := newTemporalBatchFixture(t, false)
	job, raw := f.submit(t, 1000)
	status := f.wait(t, job.JobID, func(v batch.Status) bool { return v.Counts.Done() })
	if status.Counts.Succeeded != 1000 || f.calls.Load() != 1000 || f.peak.Load() > 128 {
		t.Fatalf("not 1000 separate successful requests: %+v calls=%d peak=%d", status.Counts, f.calls.Load(), f.peak.Load())
	}
	f.assertResults(t, job.JobID, 1000)
	r, _ := http.NewRequestWithContext(f.ctx, "POST", f.server.URL+"/v1/jobs", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+f.key)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "fixture-job")
	res, err := f.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 202 || f.calls.Load() != 1000 {
		t.Fatal("submission replay regenerated inference")
	}
	r, _ = http.NewRequestWithContext(f.ctx, "GET", f.server.URL+job.StatusURL, nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("other-synthetic-key-", 3))
	res, err = f.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("job visible to another bearer")
	}
}
func TestTemporalBatchWorkerRestartDuringCooldown(t *testing.T) {
	t.Parallel()
	f := newTemporalBatchFixture(t, true)
	job, _ := f.submit(t, 24)
	f.wait(t, job.JobID, func(v batch.Status) bool { return v.WaitingReason == "provider_throttled" && v.Counts.Active == 0 })
	before := f.calls.Load()
	f.worker.Stop()
	f.worker = nil
	status, err := f.jobs.Status(f.ctx, f.scope, job.JobID)
	if err != nil || status.NextAttemptAt == nil {
		t.Fatal("cooldown not durable", err)
	}
	startFixtureWorker(t, f)
	status = f.wait(t, job.JobID, func(v batch.Status) bool { return v.Counts.Done() })
	if status.Counts.Succeeded != 24 || f.calls.Load() != 25 || before < 1 {
		t.Fatalf("restart lost or duplicated work: %+v calls=%d", status.Counts, f.calls.Load())
	}
	f.assertResults(t, job.JobID, 24)
	f.worker.Stop()
	f.worker = nil
	history := &historypb.History{}
	iter := f.temporal.GetWorkflowHistory(f.ctx, "hllm-cpa-pool-v1/"+f.activities.PoolID, "", false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			t.Fatal(err)
		}
		history.Events = append(history.Events, event)
	}
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(batch.DispatcherWorkflow, workflow.RegisterOptions{Name: batch.DispatcherWorkflowName})
	if err := replayer.ReplayWorkflowHistory(nil, history); err != nil {
		t.Fatal("real cooldown/restart history cannot replay", err)
	}
	// Opt-in fixture export contains synthetic local-provider metadata only.
	// Default verification never rewrites the retained offline replay fixture.
	if path := os.Getenv("HARDEN_LLM_TEST_BATCH_HISTORY_OUTPUT"); path != "" {
		if !filepath.IsAbs(path) {
			t.Fatal("history output must be an absolute fresh file")
		}
		raw, err := protojson.Marshal(history)
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(raw)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal("write replay fixture", writeErr, closeErr)
		}
	}
}
