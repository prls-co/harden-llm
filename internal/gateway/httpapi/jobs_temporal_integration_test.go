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
	"sync"
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
	nativeStreamExpectations sync.Map
	clientOptions            stateful.ClientOptions
	server                   *httptest.Server
	key, scope               string
	jobs                     *batch.Service
	activities               *batch.Activities
	temporal                 client.Client
	queue                    string
	worker                   worker.Worker
	calls, active, peak      atomic.Int32
	ctx                      context.Context
}

func newTemporalBatchFixture(t *testing.T, throttle bool) *temporalBatchFixture {
	t.Helper()
	return newTemporalBatchFixtureWithGate(t, throttle, nil)
}
func newTemporalBatchFixtureWithGate(t *testing.T, throttle bool, gate func(context.Context, string)) *temporalBatchFixture {
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
		streamOK := body["stream"] == true
		if expected, ordinary := f.nativeStreamExpectations.Load(nonce); ordinary {
			streamOK = body["stream"] == expected || (expected == false && body["stream"] == nil)
		}
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer synthetic-cpa-key" || !streamOK || body["model"] != "gpt-6-luna" || nonce == "" {
			t.Error("native one-generation request contract failed")
			w.WriteHeader(400)
			return
		}
		if gate != nil {
			gate(r.Context(), nonce)
		}
		writePresetNativeResponse(w, r, body, fmt.Sprintf(`{"nonce":%q}`, nonce))
	}))
	t.Cleanup(provider.Close)
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	f.clientOptions = stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: provider.URL + "/v1", APIKey: "synthetic-cpa-key"}}, DefaultConnection: "cpa", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots}, MaxConnsPerHost: 128}}
	service, err := stateful.NewService(stateful.Config{Client: f.clientOptions})
	if err != nil {
		t.Fatal(err)
	}
	f.temporal, _ = integrationtest.TemporalLease(t)
	store := &batch.PostgresStore{Pool: pool}
	policy := batch.DefaultPolicy()
	// This local provider has no account quota; window timing is covered at T0.
	policy.WindowRequests = 0
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

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-507
// V2 enqueue uses the persisted generation; the initial empty authority is test setup.
func TestTemporalBatchV2SlowCallSurvivesRolloverAndOtherJobFinishes(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f := newTemporalBatchFixtureWithGate(t, false, func(ctx context.Context, nonce string) {
		if nonce == "nonce-0000" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	})
	store := f.activities.Store.(batch.QueueStore)
	if err := store.EnsurePool(f.ctx, f.activities.PoolID, 2); err != nil {
		t.Fatal(err)
	}
	policy := f.activities.Policy
	policy.Concurrency = 2
	policy.Burst = 2
	poolID := "hllm-cpa-pool-v1/" + f.activities.PoolID
	initial, err := f.temporal.ExecuteWorkflow(f.ctx, client.StartWorkflowOptions{ID: poolID, TaskQueue: f.queue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, batch.PoolWorkflowName, batch.NewPoolState(f.activities.PoolID, 2, policy))
	if err != nil {
		t.Fatal(err)
	}
	first, raw := f.submit(t, 2)
	select {
	case <-entered:
	case <-f.ctx.Done():
		t.Fatal("first native call never entered", f.ctx.Err())
	}
	if err := f.temporal.SignalWorkflow(f.ctx, poolID, "", "continue", true); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	changed := false
	for !changed {
		select {
		case <-f.ctx.Done():
			t.Fatal("pool failed to continue while native call remained active", f.ctx.Err())
		case <-ticker.C:
			described, err := f.temporal.DescribeWorkflowExecution(f.ctx, poolID, "")
			if err != nil {
				t.Fatal(err)
			}
			changed = described.WorkflowExecutionInfo.Execution.RunId != initial.GetRunID()
		}
	}
	secondRaw := bytes.ReplaceAll(bytes.ReplaceAll(raw, []byte("nonce-0000"), []byte("nonce-0002")), []byte("nonce-0001"), []byte("nonce-0003"))
	request, _ := http.NewRequestWithContext(f.ctx, http.MethodPost, f.server.URL+"/v1/jobs", bytes.NewReader(secondRaw))
	request.Header.Set("Authorization", "Bearer "+f.key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "after-rollover")
	response, err := f.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Fatal("second job not accepted", response.StatusCode, string(body))
	}
	var second batch.Status
	if err := json.Unmarshal(body, &second); err != nil {
		t.Fatal(err)
	}
	finished := f.wait(t, second.JobID, func(s batch.Status) bool { return s.Counts.Done() })
	if finished.Counts.Succeeded != 2 || f.active.Load() != 1 || f.calls.Load() != 4 || f.peak.Load() > 2 {
		t.Fatal("other job stalled, exceeded cap or replayed while slow call active", finished.Counts, f.active.Load(), f.calls.Load(), f.peak.Load())
	}
	stillActive, err := f.jobs.Status(f.ctx, f.scope, first.JobID)
	if err != nil || stillActive.Counts.Active != 1 {
		t.Fatal("rollover lost original execution", err, stillActive.Counts)
	}
	once.Do(func() { close(release) })
	completed := f.wait(t, first.JobID, func(s batch.Status) bool { return s.Counts.Done() })
	if completed.Counts.Succeeded != 2 || f.calls.Load() != 4 {
		t.Fatal("original call was lost or repeated", completed.Counts, f.calls.Load())
	}
	f.assertResults(t, first.JobID, 2)
	f.assertResults(t, second.JobID, 2)
	for {
		encoded, err := f.temporal.QueryWorkflow(f.ctx, poolID, "", "state")
		if err != nil {
			t.Fatal(err)
		}
		var state batch.PoolState
		if err := encoded.Get(&state); err != nil {
			t.Fatal(err)
		}
		if len(state.Outstanding) == 0 {
			if state.Sequence != 4 {
				t.Fatal("rollover reset sequence", state.Sequence)
			}
			break
		}
		select {
		case <-f.ctx.Done():
			t.Fatal("settled permits leaked", f.ctx.Err())
		case <-ticker.C:
		}
	}
	f.worker.Stop()
	f.worker = nil
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(batch.PoolWorkflow, workflow.RegisterOptions{Name: batch.PoolWorkflowName})
	for _, runID := range []string{initial.GetRunID(), ""} {
		history := &historypb.History{}
		events := f.temporal.GetWorkflowHistory(f.ctx, poolID, runID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		for events.HasNext() {
			event, err := events.Next()
			if err != nil {
				t.Fatal(err)
			}
			history.Events = append(history.Events, event)
		}
		if err := replayer.ReplayWorkflowHistory(nil, history); err != nil {
			t.Fatal("real V2 pool history cannot replay", runID, err)
		}
	}

}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-512
func TestTemporalBatchV1HandoffDrainsAndRoutesFutureJobsToV2(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f := newTemporalBatchFixtureWithGate(t, false, func(ctx context.Context, nonce string) {
		if nonce == "nonce-0000" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	})
	f.activities.Policy.Concurrency = 1
	f.activities.Policy.Burst = 2
	first, raw := f.submit(t, 2)
	select {
	case <-entered:
	case <-f.ctx.Done():
		t.Fatal("legacy provider did not start", f.ctx.Err())
	}
	poolID := "hllm-cpa-pool-v1/" + f.activities.PoolID
	upgraded := make(chan error, 1)
	go func() {
		_, err := batch.WaitPoolUpgrade(f.ctx, f.temporal, f.activities.PoolID, func(p batch.UpgradeProgress) {
			t.Logf("handoff still running: active=%d unresolved=%d quiescing=%v", p.Active, p.Unresolved, p.Quiescing)
		})
		upgraded <- err
	}()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		encoded, err := f.temporal.QueryWorkflow(f.ctx, poolID, "", "state")
		if err == nil {
			var state batch.DispatcherState
			if err := encoded.Get(&state); err != nil {
				t.Fatal(err)
			}
			if state.Quiescing {
				if state.Active != 1 || f.calls.Load() != 1 {
					t.Fatal("V1 did not stop grants while draining", state.Active, f.calls.Load())
				}
				break
			}
		}
		select {
		case <-f.ctx.Done():
			t.Fatal("quiesce was not observed", f.ctx.Err())
		case <-ticker.C:
		}
	}
	select {
	case err := <-upgraded:
		t.Fatal("handoff completed before local provider returned", err)
	default:
	}
	once.Do(func() { close(release) })
	select {
	case err := <-upgraded:
		if err != nil {
			t.Fatal(err)
		}
	case <-f.ctx.Done():
		t.Fatal("drained handoff did not finish", f.ctx.Err())
	}
	completed := f.wait(t, first.JobID, func(s batch.Status) bool { return s.Counts.Done() })
	if completed.Counts.Succeeded != 2 || f.calls.Load() != 2 || f.peak.Load() > 1 {
		t.Fatal("handoff lost or repeated accepted inference", completed.Counts, f.calls.Load(), f.peak.Load())
	}
	f.assertResults(t, first.JobID, 2)
	nextRaw := bytes.ReplaceAll(bytes.ReplaceAll(raw, []byte("nonce-0000"), []byte("nonce-0002")), []byte("nonce-0001"), []byte("nonce-0003"))
	request, _ := http.NewRequestWithContext(f.ctx, "POST", f.server.URL+"/v1/jobs", bytes.NewReader(nextRaw))
	request.Header.Set("Authorization", "Bearer "+f.key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "after-real-handoff")
	response, err := f.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 202 {
		t.Fatal("future job not accepted", response.StatusCode, string(body))
	}
	var next batch.Status
	if err := json.Unmarshal(body, &next); err != nil {
		t.Fatal(err)
	}
	done := f.wait(t, next.JobID, func(s batch.Status) bool { return s.Counts.Done() })
	if done.Counts.Succeeded != 2 || f.calls.Load() != 4 {
		t.Fatal("delayed/new enqueue restarted V1 or duplicated inference", done.Counts, f.calls.Load())
	}
	f.assertResults(t, next.JobID, 2)
	description, err := f.temporal.DescribeWorkflowExecution(f.ctx, poolID, "")
	if err != nil || description.WorkflowExecutionInfo.Type.Name != batch.PoolWorkflowName {
		t.Fatal("stable pool identity did not remain V2", err)
	}
	marker, err := f.activities.Store.(batch.ExecutionStore).PoolGeneration(f.ctx, f.activities.PoolID)
	if err != nil || marker.Generation != 2 {
		t.Fatal("generation handoff missing", marker, err)
	}
}
func TestTemporalBatchEmptyUpgradeStartsNoInference(t *testing.T) {
	t.Parallel()
	f := newTemporalBatchFixture(t, false)
	store := f.activities.Store.(*batch.PostgresStore)
	if err := store.CheckEmptyPoolBootstrap(f.ctx); err != nil {
		t.Fatal(err)
	}
	id := "hllm-cpa-pool-v1/" + f.activities.PoolID
	if _, err := f.temporal.SignalWithStartWorkflow(f.ctx, id, "enqueue", batch.JobRef{}, client.StartWorkflowOptions{TaskQueue: f.queue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, batch.DispatcherWorkflowName, batch.DispatcherState{Controller: batch.NewController(f.activities.Policy)}); err != nil {
		t.Fatal(err)
	}
	state, err := batch.WaitPoolUpgrade(f.ctx, f.temporal, f.activities.PoolID, nil)
	if err != nil || state.Generation != 2 || state.Sequence != 0 || f.calls.Load() != 0 || len(state.Outstanding) != 0 {
		t.Fatal("empty bootstrap performed inference or lost ownership", state, err, f.calls.Load())
	}
	if err := store.CheckEmptyPoolBootstrap(f.ctx); err == nil {
		t.Fatal("bootstrap allowed existing authority to be reset")
	}
}

func TestTemporalBatchV2ReportsCooldownAndSurvivesWorkerRestart(t *testing.T) {
	t.Parallel()
	f := newTemporalBatchFixture(t, true)
	store := f.activities.Store.(batch.ExecutionStore)
	if err := store.EnsurePool(f.ctx, f.activities.PoolID, 2); err != nil {
		t.Fatal(err)
	}
	id := "hllm-cpa-pool-v1/" + f.activities.PoolID
	if _, err := f.temporal.ExecuteWorkflow(f.ctx, client.StartWorkflowOptions{ID: id, TaskQueue: f.queue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, batch.PoolWorkflowName, batch.NewPoolState(f.activities.PoolID, 2, f.activities.Policy)); err != nil {
		t.Fatal(err)
	}
	job, _ := f.submit(t, 24)
	waiting := f.wait(t, job.JobID, func(s batch.Status) bool { return s.WaitingReason == "provider_throttled" && s.Counts.Active == 0 })
	if waiting.NextAttemptAt == nil || waiting.LastWorkerHeartbeat == nil {
		t.Fatal("cooldown lacks truthful waiting/heartbeat", waiting)
	}
	floor := *waiting.NextAttemptAt
	f.worker.Stop()
	f.worker = nil
	paused, err := f.jobs.Status(f.ctx, f.scope, job.JobID)
	if err != nil || paused.NextAttemptAt == nil || !paused.NextAttemptAt.Equal(floor) {
		t.Fatal("worker stop erased cooldown", paused, err)
	}
	startFixtureWorker(t, f)
	finished := f.wait(t, job.JobID, func(s batch.Status) bool { return s.Counts.Done() })
	if finished.Counts.Succeeded != 24 || f.calls.Load() != 25 || f.peak.Load() > 2 {
		t.Fatal("V2 restart replayed inference or lost jobs", finished.Counts, f.calls.Load(), f.peak.Load())
	}
	f.assertResults(t, job.JobID, 24)
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-519
func enableFixtureOrdinaryAdmission(t *testing.T, f *temporalBatchFixture) {
	t.Helper()
	store := f.activities.Store.(*batch.PostgresStore)
	if err := stateful.ApplySchema(f.ctx, store.Pool); err != nil {
		t.Fatal(err)
	}
	service, err := stateful.NewService(stateful.Config{Client: f.clientOptions, Pool: store.Pool, Admission: &stateful.CPAAdmission{Store: store, Signals: batch.TemporalRequestSignals{Client: f.temporal}, PoolID: f.activities.PoolID, Endpoint: f.clientOptions.Connections[0].BaseURL}})
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(Config{APIKeys: []string{f.key, strings.Repeat("other-synthetic-key-", 3)}, Service: service, Jobs: f.jobs})
	if err != nil {
		t.Fatal(err)
	}
	f.server.Close()
	f.server = httptest.NewServer(api.Handler())
	t.Cleanup(f.server.Close)
}
func startFixtureV2Pool(t *testing.T, f *temporalBatchFixture, cap int) {
	t.Helper()
	store := f.activities.Store.(batch.QueueStore)
	if err := store.EnsurePool(f.ctx, f.activities.PoolID, 2); err != nil {
		t.Fatal(err)
	}
	policy := f.activities.Policy
	policy.Concurrency = cap
	policy.Burst = cap
	_, err := f.temporal.ExecuteWorkflow(f.ctx, client.StartWorkflowOptions{ID: "hllm-cpa-pool-v1/" + f.activities.PoolID, TaskQueue: f.queue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, batch.PoolWorkflowName, batch.NewPoolState(f.activities.PoolID, 2, policy))
	if err != nil {
		t.Fatal(err)
	}
}

type ordinaryHTTPResult struct {
	status      int
	contentType string
	body        []byte
	err         error
}

func ordinaryFixtureCall(ctx context.Context, f *temporalBatchFixture, path, nonce, tracking, cache string, stream bool, timeout int64) ordinaryHTTPResult {
	return ordinaryFixtureCallAs(ctx, f, f.key, path, nonce, tracking, cache, stream, timeout)
}
func ordinaryFixtureCallAs(ctx context.Context, f *temporalBatchFixture, key, path, nonce, tracking, cache string, stream bool, timeout int64) ordinaryHTTPResult {
	// Ordinary frontend SSE projects the completed result; it does not opt the
	// native provider into streaming. Jobs separately require native streaming.
	f.nativeStreamExpectations.Store(nonce, false)
	harden := map[string]any{"cache": cache}
	if timeout > 0 {
		harden["timeout_ms"] = timeout
	}
	body := map[string]any{"model": "gpt-6-luna", "stream": stream, "harden": harden}
	if path == "/v1/responses" {
		body["input"] = "Return " + nonce + " as a JSON nonce object."
	} else {
		body["messages"] = []map[string]any{{"role": "user", "content": "Return " + nonce + " as a JSON nonce object."}}
	}
	raw, _ := json.Marshal(body)
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.server.URL+path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", "application/json")
	if tracking != "" {
		r.Header.Set(trackingHeader, tracking)
	}
	response, err := f.server.Client().Do(r)
	if err != nil {
		return ordinaryHTTPResult{err: err}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	return ordinaryHTTPResult{status: response.StatusCode, contentType: response.Header.Get("Content-Type"), body: payload, err: err}
}
func waitOrdinaryTracking(t *testing.T, f *temporalBatchFixture, id string, predicate func(stateful.RequestStatus) bool) stateful.RequestStatus {
	return waitOrdinaryTrackingAs(t, f, f.key, id, predicate)
}
func waitOrdinaryTrackingAs(t *testing.T, f *temporalBatchFixture, key, id string, predicate func(stateful.RequestStatus) bool) stateful.RequestStatus {
	t.Helper()
	bounded, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	var last stateful.RequestStatus
	for {
		r, _ := http.NewRequestWithContext(bounded, http.MethodGet, f.server.URL+"/v1/requests/"+id, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		response, err := f.server.Client().Do(r)
		if err == nil {
			raw, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode == 200 {
				if err := contract.Validate("RequestStatus", raw); err != nil {
					t.Fatal("admission lookup violates OpenAPI", err, string(raw))
				}
				if err := json.Unmarshal(raw, &last); err != nil {
					t.Fatal(err)
				}
				if predicate(last) {
					return last
				}
			} else if response.StatusCode != 404 {
				t.Fatal("tracking lookup failed", response.StatusCode, string(raw))
			}
		}
		select {
		case <-bounded.Done():
			t.Fatal("waiting for ordinary tracking", last, bounded.Err())
			return last
		case <-tick.C:
		}
	}
}

func TestTemporalBatchOrdinaryShareAdmissionAndKeepOpenAIWire(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f := newTemporalBatchFixtureWithGate(t, false, func(ctx context.Context, nonce string) {
		if nonce == "nonce-0000" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	})
	enableFixtureOrdinaryAdmission(t, f)
	startFixtureV2Pool(t, f, 1)
	job, _ := f.submit(t, 1)
	select {
	case <-entered:
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	tracking := uuid.NewString()
	pending := make(chan ordinaryHTTPResult, 1)
	go func() {
		pending <- ordinaryFixtureCall(f.ctx, f, "/v1/responses", "nonce-8000", tracking, "off", false, 0)
	}()
	waiting := waitOrdinaryTracking(t, f, tracking, func(v stateful.RequestStatus) bool {
		return v.Status == "waiting" && v.Stage != nil && *v.Stage == "admission_wait" && v.Reason != nil && *v.Reason == "pool_capacity"
	})
	if f.calls.Load() != 1 || waiting.NextAttemptAt != nil || waiting.RetryAfterMS != nil {
		t.Fatal("ordinary call bypassed job capacity or invented ETA", f.calls.Load(), waiting)
	}
	otherKey := strings.Repeat("other-synthetic-key-", 3)
	otherTracking := uuid.NewString()
	otherPending := make(chan ordinaryHTTPResult, 1)
	go func() {
		otherPending <- ordinaryFixtureCallAs(f.ctx, f, otherKey, "/v1/responses", "nonce-8300", otherTracking, "off", false, 0)
	}()
	waitOrdinaryTrackingAs(t, f, otherKey, otherTracking, func(v stateful.RequestStatus) bool {
		return v.Status == "waiting" && v.Reason != nil && *v.Reason == "pool_capacity"
	})
	otherRequest, _ := http.NewRequestWithContext(f.ctx, http.MethodGet, f.server.URL+"/v1/requests/"+otherTracking, nil)
	otherRequest.Header.Set("Authorization", "Bearer "+f.key)
	otherResponse, otherErr := f.server.Client().Do(otherRequest)
	if otherErr != nil {
		t.Fatal(otherErr)
	}
	otherResponse.Body.Close()
	if otherResponse.StatusCode != 404 || f.calls.Load() != 1 {
		t.Fatal("cross-key status leaked or key scope multiplied provider capacity", otherResponse.StatusCode, f.calls.Load())
	}
	cancelID := uuid.NewString()
	canceled := ordinaryFixtureCall(f.ctx, f, "/v1/responses", "nonce-8001", cancelID, "off", false, 150)
	if canceled.err != nil || canceled.status != 504 || f.calls.Load() != 1 {
		t.Fatal("expired queued call ran inference", canceled.status, string(canceled.body), canceled.err, f.calls.Load())
	}
	waitOrdinaryTracking(t, f, cancelID, func(v stateful.RequestStatus) bool {
		return v.Status == "cancelled" && v.OutcomeConfirmed != nil && *v.OutcomeConfirmed
	})
	once.Do(func() { close(release) })
	select {
	case result := <-pending:
		if result.err != nil || result.status != 200 || !strings.Contains(string(result.body), "nonce-8000") || !strings.Contains(result.contentType, "application/json") {
			t.Fatal("ordinary result contract changed", result.status, string(result.body), result.err)
		}
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	select {
	case result := <-otherPending:
		if result.err != nil || result.status != 200 || !strings.Contains(string(result.body), "nonce-8300") {
			t.Fatal("second scope failed shared admission", result.status, string(result.body), result.err)
		}
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	waitOrdinaryTracking(t, f, tracking, func(v stateful.RequestStatus) bool { return v.Status == "completed" })
	f.wait(t, job.JobID, func(v batch.Status) bool { return v.Counts.Done() })
	for i, path := range []string{"/v1/responses", "/v1/chat/completions"} {
		for j, stream := range []bool{false, true} {
			nonce := fmt.Sprintf("nonce-%04d", 8100+i*2+j)
			result := ordinaryFixtureCall(f.ctx, f, path, nonce, "", "off", stream, 0)
			if result.err != nil || result.status != 200 || !strings.Contains(string(result.body), nonce) {
				t.Fatal("ordinary OpenAI result changed", path, stream, result.status, string(result.body), result.err)
			}
			if stream {
				if !strings.Contains(result.contentType, "text/event-stream") {
					t.Fatal("stream content type changed", result.contentType)
				}
				if path == "/v1/responses" && !strings.Contains(string(result.body), "response.completed") {
					t.Fatal("completed Responses SSE missing")
				}
				if path == "/v1/chat/completions" && !strings.Contains(string(result.body), "[DONE]") {
					t.Fatal("chat completion terminator missing")
				}
			}
		}
	}
	cold := ordinaryFixtureCall(f.ctx, f, "/v1/responses", "nonce-8200", "", "cache", false, 0)
	before := f.calls.Load()
	var beforeWaiters int
	store := f.activities.Store.(*batch.PostgresStore)
	if err := store.Pool.QueryRow(f.ctx, "SELECT count(*) FROM batch_request_waiters").Scan(&beforeWaiters); err != nil {
		t.Fatal(err)
	}
	warm := ordinaryFixtureCall(f.ctx, f, "/v1/responses", "nonce-8200", "", "cache", false, 0)
	var afterWaiters int
	if err := store.Pool.QueryRow(f.ctx, "SELECT count(*) FROM batch_request_waiters").Scan(&afterWaiters); err != nil {
		t.Fatal(err)
	}
	if cold.err != nil || warm.err != nil || cold.status != 200 || warm.status != 200 || before != 8 || f.calls.Load() != before || beforeWaiters != afterWaiters {
		t.Fatal("cache hit consumed admission or changed response", cold.status, warm.status, before, f.calls.Load(), beforeWaiters, afterWaiters, cold.err, warm.err)
	}
	var jobs, items, history, ordinaryUnstopped int
	if err := store.Pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM batch_jobs),(SELECT count(*) FROM batch_items),(SELECT count(*) FROM history),(SELECT count(*) FROM batch_attempts WHERE kind='ordinary' AND (state<>'settled' OR local_stopped_at IS NULL))`).Scan(&jobs, &items, &history, &ordinaryUnstopped); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || items != 1 || history != 0 || ordinaryUnstopped != 0 || f.peak.Load() > 1 {
		t.Fatal("ordinary calls retained results/history or exceeded global overlap", jobs, items, history, ordinaryUnstopped, f.peak.Load())
	}
}

func TestTemporalBatchOrdinaryRefusalSharesCooldownAndRetryOwnership(t *testing.T) {
	t.Parallel()
	f := newTemporalBatchFixture(t, true)
	enableFixtureOrdinaryAdmission(t, f)
	startFixtureV2Pool(t, f, 2)
	tracking := uuid.NewString()
	ordinary := make(chan ordinaryHTTPResult, 1)
	go func() {
		ordinary <- ordinaryFixtureCall(f.ctx, f, "/v1/responses", "nonce-8400", tracking, "off", false, 0)
	}()
	waitOrdinaryTracking(t, f, tracking, func(v stateful.RequestStatus) bool {
		return v.Status == "waiting" && v.Reason != nil && *v.Reason == "provider_throttled" && v.NextAttemptAt != nil
	})
	job, _ := f.submit(t, 1)
	waiting := f.wait(t, job.JobID, func(v batch.Status) bool {
		return v.WaitingReason == "provider_throttled" && v.Counts.Active == 0 && v.NextAttemptAt != nil
	})
	if f.calls.Load() != 1 || waiting.NextAttemptAt == nil {
		t.Fatal("ordinary refusal did not cool the shared durable pool", f.calls.Load(), waiting)
	}
	select {
	case result := <-ordinary:
		if result.err != nil || result.status != 200 || !strings.Contains(string(result.body), "nonce-8400") {
			t.Fatal("ordinary retry failed after shared cooldown", result.status, string(result.body), result.err)
		}
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	f.wait(t, job.JobID, func(v batch.Status) bool { return v.Counts.Done() })
	store := f.activities.Store.(*batch.PostgresStore)
	var attempts, executions, minOrdinal, maxOrdinal int
	if err := store.Pool.QueryRow(f.ctx, `SELECT count(*),count(DISTINCT execution_id),min(dispatch_ordinal),max(dispatch_ordinal) FROM batch_request_waiters WHERE api_key_hash=$1`, f.scope).Scan(&attempts, &executions, &minOrdinal, &maxOrdinal); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || executions != 1 || minOrdinal != 1 || maxOrdinal != 2 || f.calls.Load() != 3 || f.peak.Load() > 2 {
		t.Fatal("shared retry reset identity or duplicated inference", attempts, executions, minOrdinal, maxOrdinal, f.calls.Load(), f.peak.Load())
	}
}
