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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	hardenllm "github.com/prls-co/harden-llm"
	contract "github.com/prls-co/harden-llm/api"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/internal/livecapacity"
	"github.com/prls-co/harden-llm/internal/testfixture/native"
	"github.com/prls-co/harden-llm/stateful"
	"github.com/prls-co/harden-llm/stateful/batch"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/encoding/protojson"
)

type temporalBatchFixture struct {
	clientOptions         stateful.ClientOptions
	server                *httptest.Server
	key, scope            string
	jobs                  *batch.Service
	activities            *batch.Activities
	temporal              client.Client
	queue                 string
	worker                worker.Worker
	calls, active, peak   atomic.Int32
	claimSignalCalls      atomic.Int64
	settlementSignalCalls atomic.Int64
	ctx                   context.Context
	processSettings       fixtureProcessWorkerSettings
}

// Count logical durable SignalWorkflow calls, not SDK transport retry attempts.
// The embedded real client retains every other behavior and payload unchanged.
type fixtureDurableSignalCounter struct {
	client.Client
	fixture *temporalBatchFixture
}

func (c fixtureDurableSignalCounter) SignalWorkflow(ctx context.Context, id, run, name string, arg interface{}) error {
	if notice, ok := arg.(batch.SettlementNotice); ok && name == "attempt-settled" {
		if notice.Revision == 0 {
			c.fixture.claimSignalCalls.Add(1)
		} else {
			c.fixture.settlementSignalCalls.Add(1)
		}
	}
	return c.Client.SignalWorkflow(ctx, id, run, name, arg)
}

func newTemporalBatchFixture(t *testing.T, throttle bool) *temporalBatchFixture {
	t.Helper()
	return newTemporalBatchFixtureWithGate(t, throttle, nil)
}
func newTemporalBatchFixtureWithGate(t *testing.T, throttle bool, gate func(context.Context, string)) *temporalBatchFixture {
	t.Helper()
	return newTemporalBatchFixtureWithTemporal(t, throttle, gate, nil)
}

func newTemporalBatchFixtureWithTemporal(t *testing.T, throttle bool, gate func(context.Context, string), wrap func(client.Client) client.Client) *temporalBatchFixture {
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
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("bad provider JSON")
			w.WriteHeader(400)
			return
		}
		raw, _ := json.Marshal(body)
		nonce := noncePattern.FindString(string(raw))
		// CPA streams natively for both public JSON and final-only SSE calls.
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer synthetic-cpa-key" || body["stream"] != true || body["model"] != "gpt-6-luna" || nonce == "" {
			t.Error("native one-generation request contract failed")
			w.WriteHeader(400)
			return
		}
		if gate != nil {
			gate(r.Context(), nonce)
		}
		if throttle && call == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"code":"rate_limit_exceeded"}}`)
			return
		}
		native.WriteResponse(w, r, body, fmt.Sprintf(`{"nonce":%q}`, nonce))
	}))
	t.Cleanup(provider.Close)
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	f.clientOptions = stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: provider.URL + "/v1", APIKey: "synthetic-cpa-key"}}, DefaultConnection: "cpa", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots}, MaxConnsPerHost: 128}}
	service, err := stateful.NewService(stateful.Config{Client: f.clientOptions})
	if err != nil {
		t.Fatal(err)
	}
	var namespace string
	f.temporal, namespace = integrationtest.TemporalLease(t)
	activityTemporal := f.temporal
	if wrap != nil {
		activityTemporal = wrap(activityTemporal)
	}
	store := &batch.PostgresStore{Pool: pool}
	policy := batch.DefaultPolicy()
	// This local provider has no account quota; window timing is covered at T0.
	policy.WindowRequests = 0
	if throttle {
		policy.Concurrency = 2
		policy.Burst = 2
	}
	f.jobs = &batch.Service{Store: store, Starter: batch.TemporalStarter{Client: f.temporal, TaskQueue: f.queue}, Artifacts: artifacts, Upstream: "cpa", JobTimeout: 2 * time.Minute}
	f.activities = &batch.Activities{Store: store, Artifacts: artifacts, Temporal: fixtureDurableSignalCounter{Client: activityTemporal, fixture: f}, TaskQueue: f.queue, PoolID: uuid.NewString(), Policy: policy, Encode: EncodeBatchResponse, Execute: func(ctx context.Context, scope string, r hardenllm.Request) (hardenllm.Result, error) {
		core, err := service.ClientForKey(scope)
		if err != nil {
			return hardenllm.Result{}, err
		}
		return core.Call(ctx, r)
	}}
	f.processSettings = fixtureProcessWorkerSettings{DSN: dsn, Artifacts: stateful.ArtifactOptions{Endpoint: garage.Endpoint, Bucket: garage.Bucket, Region: garage.Region, AccessKeyID: garage.AccessKeyID, SecretAccessKey: garage.SecretAccessKey}, Endpoint: os.Getenv("HARDEN_LLM_TEST_TEMPORAL_ENDPOINT"), Namespace: namespace, Queue: f.queue, PoolID: f.activities.PoolID, ProviderURL: provider.URL, Certificate: provider.Certificate().Raw}
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
	service, err := stateful.NewService(stateful.Config{Client: f.clientOptions, Pool: store.Pool, Admission: &stateful.CPAAdmission{Store: store, Signals: batch.TemporalRequestSignals{Client: f.temporal}, PoolID: f.activities.PoolID, Endpoint: f.clientOptions.Connections[0].BaseURL, Logger: mixedFixtureLogger(t)}})
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
	refusalEntered, releaseRefusal := make(chan struct{}), make(chan struct{})
	var refusalOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRefusal) }) }
	defer release()
	f := newTemporalBatchFixtureWithGate(t, true, func(ctx context.Context, nonce string) {
		if nonce != "nonce-8400" {
			return
		}
		refusalOnce.Do(func() {
			close(refusalEntered)
			select {
			case <-releaseRefusal:
			case <-ctx.Done():
			}
		})
	})
	enableFixtureOrdinaryAdmission(t, f)
	// Queue the job before starting the refusal's seven-second cooldown.
	startFixtureV2Pool(t, f, 1)
	tracking := uuid.NewString()
	ordinary := make(chan ordinaryHTTPResult, 1)
	go func() {
		ordinary <- ordinaryFixtureCall(f.ctx, f, "/v1/responses", "nonce-8400", tracking, "off", false, 0)
	}()
	select {
	case <-refusalEntered:
	case result := <-ordinary:
		t.Fatal("ordinary request bypassed the refusal gate", result.status, result.err)
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	if f.calls.Load() != 1 {
		t.Fatal("first refusal bypassed the provider gate", f.calls.Load())
	}
	job, _ := f.submit(t, 1)
	f.wait(t, job.JobID, func(v batch.Status) bool {
		return v.WaitingReason == "pool_capacity" && v.Counts.Queued == 1 && v.Counts.Active == 0
	})
	release()
	waitOrdinaryTracking(t, f, tracking, func(v stateful.RequestStatus) bool {
		return v.Status == "waiting" && v.Reason != nil && *v.Reason == "provider_throttled" && v.NextAttemptAt != nil
	})
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

// Mixed qualification helpers stay in this approved Temporal composition file.
func newMixedFixtureReplica(t *testing.T, f *temporalBatchFixture) *temporalBatchFixture {
	t.Helper()
	original := f.activities.Store.(*batch.PostgresStore)
	pool, err := pgxpool.New(f.ctx, original.Pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := &batch.PostgresStore{Pool: pool}
	service, err := stateful.NewService(stateful.Config{Client: f.clientOptions, Pool: pool, Admission: &stateful.CPAAdmission{Store: store, Signals: batch.TemporalRequestSignals{Client: f.temporal}, PoolID: f.activities.PoolID, Endpoint: f.clientOptions.Connections[0].BaseURL, Logger: mixedFixtureLogger(t)}})
	if err != nil {
		t.Fatal(err)
	}
	jobs := *f.jobs
	jobs.Store = store
	api, err := New(Config{APIKeys: []string{f.key, strings.Repeat("other-synthetic-key-", 3)}, Service: service, Jobs: &jobs})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	return &temporalBatchFixture{server: server, key: f.key, scope: f.scope, jobs: &jobs, ctx: f.ctx}
}
func mixedPoolRunID(f *temporalBatchFixture) (string, error) {
	info, err := f.temporal.DescribeWorkflowExecution(f.ctx, "hllm-cpa-pool-v1/"+f.activities.PoolID, "")
	if err != nil {
		return "", err
	}
	return info.WorkflowExecutionInfo.Execution.RunId, nil
}
func mixedContinuePool(f *temporalBatchFixture) error {
	return f.temporal.SignalWorkflow(f.ctx, "hllm-cpa-pool-v1/"+f.activities.PoolID, "", "continue", true)
}
func mixedWaitPoolIdle(ctx context.Context, f *temporalBatchFixture) (batch.PoolState, error) {
	bounded, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		value, err := f.temporal.QueryWorkflow(bounded, "hllm-cpa-pool-v1/"+f.activities.PoolID, "", "state")
		if err == nil {
			var state batch.PoolState
			if err := value.Get(&state); err != nil {
				return state, err
			}
			if len(state.Outstanding) == 0 && len(state.StartIntents) == 0 && len(state.RequestRetirements) == 0 {
				return state, nil
			}
		}
		select {
		case <-bounded.Done():
			return batch.PoolState{}, bounded.Err()
		case <-timer.C:
		}
	}
}
func mixedWorkflowCounts(ctx context.Context, f *temporalBatchFixture) (map[string]int64, error) {
	counts := map[string]int64{}
	for _, name := range []string{batch.AttemptWorkflowName, batch.BatchWorkflowName, batch.PoolWorkflowName} {
		response, err := f.temporal.CountWorkflow(ctx, &workflowservice.CountWorkflowExecutionsRequest{Query: "WorkflowType = '" + name + "'"})
		if err != nil {
			return nil, err
		}
		counts[name] = response.Count
	}
	return counts, nil
}

func mixedFixtureLogger(t *testing.T) *slog.Logger {
	t.Helper()
	path := filepath.Join("..", "..", "..", "tmp", fmt.Sprintf("cpa-metadata-%d.jsonl", os.Getpid()))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	return slog.New(slog.NewJSONHandler(file, nil))
}

type mixedTimingSummary struct {
	Count          int
	TotalMS, MaxMS float64
}
type mixedHistoryDiagnostics struct {
	Complete bool
	Events   int
	Timings  map[string]mixedTimingSummary
}

// Retain timing aggregates only. Inference payloads, workflow inputs, headers,
// scope keys and raw history never enter this report.
func mixedPoolHistoryTiming(ctx context.Context, f *temporalBatchFixture) mixedHistoryDiagnostics {
	out := mixedHistoryDiagnostics{Timings: map[string]mixedTimingSummary{}}
	type span struct {
		name string
		at   time.Time
	}
	scheduled, started := map[int64]span{}, map[int64]span{}
	add := func(name string, d time.Duration) {
		v := out.Timings[name]
		v.Count++
		ms := float64(d) / float64(time.Millisecond)
		v.TotalMS += ms
		v.MaxMS = max(v.MaxMS, ms)
		out.Timings[name] = v
	}
	events := f.temporal.GetWorkflowHistory(ctx, "hllm-cpa-pool-v1/"+f.activities.PoolID, "", false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for events.HasNext() && out.Events < 10000 {
		e, err := events.Next()
		if err != nil {
			return out
		}
		out.Events++
		at := e.GetEventTime().AsTime()
		if a := e.GetActivityTaskScheduledEventAttributes(); a != nil {
			scheduled[e.EventId] = span{a.ActivityType.Name, at}
		}
		if a := e.GetActivityTaskStartedEventAttributes(); a != nil {
			if s, ok := scheduled[a.ScheduledEventId]; ok {
				add(s.name+"/queue", at.Sub(s.at))
				started[a.ScheduledEventId] = span{s.name, at}
			}
		}
		if a := e.GetActivityTaskCompletedEventAttributes(); a != nil {
			if s, ok := started[a.ScheduledEventId]; ok {
				add(s.name+"/execute", at.Sub(s.at))
			}
		}
		if e.GetWorkflowTaskScheduledEventAttributes() != nil {
			scheduled[e.EventId] = span{"workflow", at}
		}
		if a := e.GetWorkflowTaskStartedEventAttributes(); a != nil {
			if s, ok := scheduled[a.ScheduledEventId]; ok {
				add("workflow/queue", at.Sub(s.at))
				started[a.ScheduledEventId] = span{"workflow", at}
			}
		}
		if a := e.GetWorkflowTaskCompletedEventAttributes(); a != nil {
			if s, ok := started[a.ScheduledEventId]; ok {
				add("workflow/execute", at.Sub(s.at))
			}
		}
		if a := e.GetTimerStartedEventAttributes(); a != nil {
			scheduled[e.EventId] = span{"timer/" + a.StartToFireTimeout.AsDuration().String(), at}
		}
		if a := e.GetTimerFiredEventAttributes(); a != nil {
			if s, ok := scheduled[a.StartedEventId]; ok {
				add(s.name, at.Sub(s.at))
			}
		}
	}
	out.Complete = !events.HasNext()
	return out
}

type mixedPoolObservation struct {
	ElapsedMS                                                                  int64
	Confirmed                                                                  bool
	Reason                                                                     string
	Outstanding, Unclaimed, StartIntents, PendingClaimLimit, Retirements, Rate int
	PersistenceWait, DispatchNeedsReady, Quiescing                             bool
	NextStart, NextEligible                                                    time.Time
}

func mixedObservePool(ctx context.Context, f *temporalBatchFixture) mixedPoolObservation {
	var out mixedPoolObservation
	v, err := f.temporal.QueryWorkflow(ctx, "hllm-cpa-pool-v1/"+f.activities.PoolID, "", "state")
	if err != nil {
		return out
	}
	var s batch.PoolState
	if err := v.Get(&s); err != nil {
		return out
	}
	out.Confirmed = true
	out.Reason, _ = s.Waiting(time.Now())
	out.Outstanding = len(s.Outstanding)
	out.StartIntents = len(s.StartIntents)
	out.PendingClaimLimit = s.PendingClaimLimit
	out.Retirements = len(s.RequestRetirements)
	out.Rate = s.Controller.Rate
	out.PersistenceWait, out.DispatchNeedsReady, out.Quiescing = s.PersistenceWait, s.DispatchNeedsReady, s.Quiescing
	out.NextStart, out.NextEligible = s.Controller.NextStart, s.Controller.NextEligible
	if s.PendingClaimLimit > 0 {
		for _, p := range s.Outstanding {
			if !p.Claimed {
				out.Unclaimed++
			}
		}
	}
	return out
}

// Drop only the caller acknowledgement after a real positive Temporal start.
// The independent execution and its original workflow identity remain intact.
type fixtureLostAttemptStart struct {
	client.Client
	mu          sync.Mutex
	reservation batch.Reservation
	identities  map[string]int
	lost        atomic.Bool
	ackLost     chan struct{}
}

func (c *fixtureLostAttemptStart) ExecuteWorkflow(ctx context.Context, opts client.StartWorkflowOptions, w interface{}, args ...interface{}) (client.WorkflowRun, error) {
	if w != batch.AttemptWorkflowName {
		return c.Client.ExecuteWorkflow(ctx, opts, w, args...)
	}
	c.mu.Lock()
	c.identities[opts.ID]++
	c.mu.Unlock()
	run, err := c.Client.ExecuteWorkflow(ctx, opts, w, args...)
	if err == nil && c.lost.CompareAndSwap(false, true) {
		c.mu.Lock()
		c.reservation = args[0].(batch.AttemptInput).Reservation
		c.mu.Unlock()
		close(c.ackLost)
		return nil, errors.New("synthetic lost acknowledgement after real positive start")
	}
	return run, err
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-526
func TestTemporalBatchLostStartAcknowledgementKeepsOneAttemptAndResult(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	lost := &fixtureLostAttemptStart{identities: map[string]int{}, ackLost: make(chan struct{})}
	f := newTemporalBatchFixtureWithTemporal(t, false, func(ctx context.Context, _ string) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}, func(c client.Client) client.Client {
		lost.Client = c
		return lost
	})
	startFixtureV2Pool(t, f, 1)
	job, _ := f.submit(t, 1)
	select {
	case <-entered:
	case <-f.ctx.Done():
		t.Fatal("started workflow never entered its sole native call", f.ctx.Err())
	}
	select {
	case <-lost.ackLost:
	case <-f.ctx.Done():
		t.Fatal("no real positive start acknowledgement was lost", f.ctx.Err())
	}
	lost.mu.Lock()
	r := lost.reservation
	lost.mu.Unlock()
	// Retry metadata using the exact reservation/Workflow ID while HTTP is active.
	out, err := f.activities.StartAttempts(f.ctx, []batch.Reservation{r})
	if err != nil || len(out) != 1 || !out[0].Started || out[0].PermitID != r.PermitID {
		t.Fatal("same-identity retry failed to resolve an already-started execution", err, out)
	}
	store := f.activities.Store.(*batch.PostgresStore)
	var rows, ordinal int
	if err := store.Pool.QueryRow(f.ctx, `SELECT count(*),max(ordinal) FROM batch_attempts WHERE permit_id=$1`, r.PermitID).Scan(&rows, &ordinal); err != nil || rows != 1 || ordinal != 1 || f.calls.Load() != 1 || f.active.Load() != 1 {
		t.Fatal("lost start acknowledgement duplicated reservation or native execution", err, rows, ordinal, f.calls.Load())
	}
	encoded, err := f.temporal.QueryWorkflow(f.ctx, "hllm-cpa-pool-v1/"+f.activities.PoolID, "", "state")
	var active batch.PoolState
	if err != nil || encoded.Get(&active) != nil || len(active.Outstanding) != 1 || active.Outstanding[0].ID != r.PermitID {
		t.Fatal("uncertain start acknowledgement released active capacity", err, active)
	}
	once.Do(func() { close(release) })
	finished := f.wait(t, job.JobID, func(s batch.Status) bool { return s.Counts.Done() })
	if finished.Counts.Succeeded != 1 || f.calls.Load() != 1 {
		t.Fatal("lost start acknowledgement lost or repeated the final result", finished.Counts, f.calls.Load())
	}
	f.assertResults(t, job.JobID, 1)
	if err := f.temporal.GetWorkflow(f.ctx, r.WorkflowID, "").Get(f.ctx, nil); err != nil {
		t.Fatal("independent workflow did not finish after metadata recovery", err)
	}
	lost.mu.Lock()
	identities, starts := len(lost.identities), lost.identities[r.WorkflowID]
	lost.mu.Unlock()
	if identities != 1 || starts < 2 {
		t.Fatal("retry selected a replacement workflow identity", identities, starts)
	}
	retained, err := store.ReadAttempts(f.ctx, r.PoolID, []string{r.PermitID})
	if err != nil || len(retained) != 1 || retained[0].State != "settled" || retained[0].StoppedAt == nil || retained[0].Observation == nil || retained[0].Observation.Outcome.Kind != "completed" {
		t.Fatal("same-identity execution did not retain a confirmed settlement", err, retained)
	}
	assertFixturePoolSettled(t, f, 1)
	// Replay the actual independent attempt history, including its new version
	// marker and combined metadata-finalization activity.
	history := &historypb.History{}
	events := f.temporal.GetWorkflowHistory(f.ctx, r.WorkflowID, "", false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for events.HasNext() {
		event, err := events.Next()
		if err != nil || len(history.Events) >= 1000 {
			t.Fatal("attempt history unavailable or unexpectedly unbounded", err)
		}
		history.Events = append(history.Events, event)
	}
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(batch.AttemptWorkflow, workflow.RegisterOptions{Name: batch.AttemptWorkflowName})
	if err := replayer.ReplayWorkflowHistory(nil, history); err != nil {
		t.Fatal("real independent attempt history cannot replay", err)
	}
}

func assertFixturePoolSettled(t *testing.T, f *temporalBatchFixture, sequence int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		encoded, err := f.temporal.QueryWorkflow(ctx, "hllm-cpa-pool-v1/"+f.activities.PoolID, "", "state")
		var state batch.PoolState
		if err != nil || encoded.Get(&state) != nil {
			t.Fatal("pool state unavailable after positive settlement", err)
		}
		if len(state.Outstanding) == 0 && len(state.StartIntents) == 0 && len(state.RequestRetirements) == 0 && !state.PersistenceWait {
			if state.Sequence != sequence || f.active.Load() != 0 {
				t.Fatal("settlement changed permit identities or left native transport active", state.Sequence, f.active.Load())
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("positive settlement leaked pool capacity or persistence gate", state, ctx.Err())
		case <-tick.C:
		}
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-527
func TestTemporalBatchPersistenceBlockRetriesMetadataWithoutInference(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f := newTemporalBatchFixtureWithGate(t, false, func(ctx context.Context, _ string) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
	})
	startFixtureV2Pool(t, f, 1)
	job, _ := f.submit(t, 1)
	select {
	case <-entered:
	case <-f.ctx.Done():
		t.Fatal("provider never entered before persistence blockage", f.ctx.Err())
	}
	store := f.activities.Store.(*batch.PostgresStore)
	var permit, claim, workflowID string
	if err := store.Pool.QueryRow(f.ctx, `SELECT permit_id::text,claim_id::text,workflow_id FROM batch_attempts WHERE api_key_hash=$1 AND job_id=$2`, f.scope, job.JobID).Scan(&permit, &claim, &workflowID); err != nil {
		t.Fatal(err)
	}
	held, err := store.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(context.Background())
	var locked string
	if err := held.QueryRow(f.ctx, `SELECT permit_id::text FROM batch_attempts WHERE permit_id=$1 FOR UPDATE`, permit).Scan(&locked); err != nil || locked != permit {
		t.Fatal("test failed to acquire its own invocation row", err)
	}
	once.Do(func() { close(release) })
	phase, stopPhase := context.WithTimeout(f.ctx, 12*time.Second)
	defer stopPhase()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	artifactRetained, metadataRetried := false, false
	for !artifactRetained || !metadataRetried {
		if !artifactRetained {
			r, err := f.activities.Artifacts.OpenBatchResult(phase, f.scope, job.JobID, "attempt-"+permit+".json")
			if err == nil {
				raw, readErr := io.ReadAll(io.LimitReader(r, 65536))
				closeErr := r.Close()
				var envelope struct {
					PermitID    string                   `json:"permit_id"`
					ClaimID     string                   `json:"claim_id"`
					Observation batch.AttemptObservation `json:"observation"`
				}
				if readErr != nil || closeErr != nil || json.Unmarshal(raw, &envelope) != nil || envelope.PermitID != permit || envelope.ClaimID != claim || !envelope.Observation.NativeEntered || !envelope.Observation.LocalStopped || envelope.Observation.Outcome.Kind != "completed" {
					t.Fatal("native result artifact lacked positive same-owner completion")
				}
				artifactRetained = true
			}
		}
		if !metadataRetried {
			described, err := f.temporal.DescribeWorkflowExecution(phase, workflowID, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, pending := range described.PendingActivities {
				if pending.ActivityType.GetName() == "FinalizeAttempt" && (pending.Attempt > 1 || pending.LastFailure != nil) {
					metadataRetried = true
				}
			}
		}
		if artifactRetained && metadataRetried {
			break
		}
		select {
		case <-phase.Done():
			t.Fatal("did not observe real artifact completion and metadata retry under held SQL row", artifactRetained, metadataRetried, phase.Err())
		case <-tick.C:
		}
	}
	if f.calls.Load() != 1 || f.active.Load() != 0 {
		t.Fatal("persistence blockage replayed inference or prevented native closure", f.calls.Load(), f.active.Load())
	}
	if err := held.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	finished := f.wait(t, job.JobID, func(s batch.Status) bool { return s.Counts.Done() })
	if finished.Counts.Succeeded != 1 || finished.Counts.Failed != 0 || f.calls.Load() != 1 {
		t.Fatal("same-observation metadata retry lost or repeated native output", finished.Counts, f.calls.Load())
	}
	f.assertResults(t, job.JobID, 1)
	if err := f.temporal.GetWorkflow(f.ctx, workflowID, "").Get(f.ctx, nil); err != nil {
		t.Fatal("independent workflow did not complete after persistence recovery", err)
	}
	retained, err := store.ReadAttempts(f.ctx, f.activities.PoolID, []string{permit})
	if err != nil || len(retained) != 1 || retained[0].ClaimID != claim || retained[0].Ordinal != 1 || retained[0].State != "settled" || retained[0].StoppedAt == nil || retained[0].Observation == nil || retained[0].Observation.Outcome.Kind != "completed" {
		t.Fatal("persistence recovery changed immutable ownership/result", err, retained)
	}
	assertFixturePoolSettled(t, f, 1)
}

// The parent owns every service lease and this private bootstrap file. The
// child inherits no user/provider environment and creates no additional leases.
type fixtureProcessWorkerSettings struct {
	DSN, Endpoint, Namespace, Queue, PoolID, ProviderURL, ReadyFile string
	Certificate                                                     []byte
	Artifacts                                                       stateful.ArtifactOptions
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-530
func TestTemporalBatchOwnedWorkerProcessFixture(t *testing.T) {
	path := os.Getenv("HLLM_OWNED_WORKER_BOOTSTRAP")
	if path == "" {
		return // Helper is launched only by the real process-death case below.
	}
	raw, err := os.ReadFile(path)
	var cfg fixtureProcessWorkerSettings
	if err != nil || json.Unmarshal(raw, &cfg) != nil {
		t.Fatal("invalid owned worker bootstrap")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		t.Fatal("owned worker database initialization failed")
	}
	defer pool.Close()
	c, err := client.DialContext(ctx, client.Options{HostPort: cfg.Endpoint, Namespace: cfg.Namespace})
	if err != nil {
		t.Fatal("owned worker Temporal initialization failed")
	}
	defer c.Close()
	artifacts, err := stateful.NewArtifactStore(cfg.Artifacts)
	if err != nil {
		t.Fatal("owned worker artifact initialization failed")
	}
	cert, err := x509.ParseCertificate(cfg.Certificate)
	if err != nil {
		t.Fatal("owned worker fixture certificate invalid")
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	core, err := stateful.NewClient(stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: cfg.ProviderURL + "/v1", APIKey: "synthetic-cpa-key"}}, DefaultConnection: "cpa", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots}}})
	if err != nil {
		t.Fatal("owned worker native client initialization failed")
	}
	policy := batch.DefaultPolicy()
	policy.Concurrency, policy.WindowRequests = 1, 0
	a := &batch.Activities{Store: &batch.PostgresStore{Pool: pool}, Artifacts: artifacts, Temporal: c, PoolID: cfg.PoolID, TaskQueue: cfg.Queue, Policy: policy, Encode: EncodeBatchResponse, Execute: func(ctx context.Context, _ string, request hardenllm.Request) (hardenllm.Result, error) {
		return core.Call(ctx, request)
	}}
	w, err := batch.NewWorker(c, cfg.Queue, a)
	if err != nil || w.Start() != nil {
		t.Fatal("owned worker startup failed")
	}
	defer w.Stop()
	if err := os.WriteFile(cfg.ReadyFile, []byte("ready\n"), 0600); err != nil {
		t.Fatal("owned worker readiness acknowledgement failed")
	}
	<-ctx.Done() // Emergency bound only; the parent must SIGKILL this process.
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-530
func TestTemporalBatchKilledWorkerRetainsUnknownUntilAuditedStop(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	var enteredOnce sync.Once
	f := newTemporalBatchFixtureWithGate(t, false, func(ctx context.Context, _ string) {
		enteredOnce.Do(func() { close(entered) })
		<-ctx.Done() // No response can finish while the child transport is alive.
	})
	f.worker.Stop()
	f.worker = nil
	cfg := f.processSettings
	cfg.ReadyFile = filepath.Join(t.TempDir(), "worker-ready")
	configPath := filepath.Join(t.TempDir(), "worker.json")
	raw, err := json.Marshal(cfg)
	if err != nil || os.WriteFile(configPath, raw, 0600) != nil {
		t.Fatal("cannot create private owned-worker bootstrap")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-test.run=^TestTemporalBatchOwnedWorkerProcessFixture$", "-test.timeout=100s")
	command.Env = []string{"HLLM_OWNED_WORKER_BOOTSTRAP=" + configPath}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		t.Fatal("cannot start test-owned worker process")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = command.Process.Kill()
			<-done
		}
	})
	ready, stopReady := context.WithTimeout(f.ctx, 15*time.Second)
	defer stopReady()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if data, err := os.ReadFile(cfg.ReadyFile); err == nil && string(data) == "ready\n" {
			break
		}
		select {
		case <-done:
			waited = true
			t.Fatal("test-owned worker exited before readiness")
		case <-ready.Done():
			t.Fatal("test-owned worker did not become ready", ready.Err())
		case <-tick.C:
		}
	}
	startFixtureV2Pool(t, f, 1)
	var nativeRequest map[string]any
	if err := json.Unmarshal(livecapacity.BatchRequestBody("nonce-0000", false), &nativeRequest); err != nil {
		t.Fatal(err)
	}
	nativeRequest["harden"] = map[string]any{"timeout_ms": 10000}
	manifest, _ := json.Marshal(map[string]any{"items": []map[string]any{{"custom_id": "nonce-0000", "request": nativeRequest}}})
	request, _ := http.NewRequestWithContext(f.ctx, http.MethodPost, f.server.URL+"/v1/jobs", bytes.NewReader(manifest))
	request.Header.Set("Authorization", "Bearer "+f.key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "owned-process-death")
	response, err := f.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	var job batch.Status
	if readErr != nil || response.StatusCode != http.StatusAccepted || json.Unmarshal(body, &job) != nil {
		t.Fatal("process-death job was not accepted", response.StatusCode, readErr)
	}
	select {
	case <-entered:
	case <-f.ctx.Done():
		t.Fatal("child worker never entered native HTTP", f.ctx.Err())
	}
	store := f.activities.Store.(*batch.PostgresStore)
	var permit, claim string
	if err := store.Pool.QueryRow(f.ctx, `SELECT permit_id::text,claim_id::text FROM batch_attempts WHERE job_id=$1`, job.JobID).Scan(&permit, &claim); err != nil || f.active.Load() != 1 {
		t.Fatal("native execution was not active before killing its owner", err, f.active.Load())
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal("cannot kill the test-owned worker", err)
	}
	waitErr := <-done
	waited = true
	status, ok := command.ProcessState.Sys().(syscall.WaitStatus)
	if waitErr == nil || !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("worker termination lacks actual SIGKILL/Wait proof")
	}
	// Resume compatible control/reconciliation in another process, but withhold
	// stop proof until the immutable unconfirmed inference outcome is retained.
	startFixtureWorker(t, f)
	finished := f.wait(t, job.JobID, func(s batch.Status) bool { return s.Counts.Done() })
	if finished.Counts.Succeeded != 0 || finished.Counts.Failed != 1 || f.calls.Load() != 1 {
		t.Fatal("process death replayed inference or invented a successful result", finished.Counts, f.calls.Load())
	}
	retained, err := store.ReadAttempts(f.ctx, f.activities.PoolID, []string{permit})
	if err != nil || len(retained) != 1 || retained[0].State != "settled" || retained[0].StoppedAt != nil || retained[0].Observation == nil || retained[0].Observation.Outcome.Kind != "unconfirmed" || retained[0].Observation.Outcome.ArtifactID != "" {
		t.Fatal("missing finalizer invented transport stop or a confirmed artifact", err, retained)
	}
	unknownCtx, stopUnknown := context.WithTimeout(f.ctx, 10*time.Second)
	defer stopUnknown()
	for {
		value, err := f.temporal.QueryWorkflow(unknownCtx, "hllm-cpa-pool-v1/"+f.activities.PoolID, "", "state")
		var blocked batch.PoolState
		if err != nil || value.Get(&blocked) != nil || len(blocked.Outstanding) != 1 || blocked.Outstanding[0].ID != permit || blocked.CanGrant(time.Now()) {
			t.Fatal("dead worker's unresolved transport released capacity without audited proof", err, blocked)
		}
		if blocked.Outstanding[0].AwaitingStop {
			break
		}
		select {
		case <-unknownCtx.Done():
			t.Fatal("pool never applied the retained unknown result", unknownCtx.Err())
		case <-tick.C:
		}
	}
	var before bytes.Buffer
	if err := f.jobs.WriteResults(f.ctx, f.scope, job.JobID, &before); err != nil {
		t.Fatal(err)
	}
	var result batch.Result
	if json.Unmarshal(bytes.TrimSpace(before.Bytes()), &result) != nil || result.Error == nil || len(result.Response) != 0 {
		t.Fatal("unknown process outcome exposed an invented native result")
	}
	// This is explicit test-owned operator resolution backed by Process.Wait;
	// production must never derive it from a heartbeat, timeout or lease alone.
	stopped, err := store.MarkLocalStop(f.ctx, permit, claim, time.Now().UTC())
	if err != nil || stopped.SettlementRevision != retained[0].SettlementRevision+1 || stopped.Observation.Outcome.Kind != "unconfirmed" {
		t.Fatal("audited stop changed immutable inference outcome", err, stopped)
	}
	if err := f.activities.NotifySettlement(f.ctx, stopped); err != nil {
		t.Fatal(err)
	}
	assertFixturePoolSettled(t, f, 1)
	var after bytes.Buffer
	if err := f.jobs.WriteResults(f.ctx, f.scope, job.JobID, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) || f.calls.Load() != 1 {
		t.Fatal("audited stop replayed inference or rewrote the public terminal result", err, f.calls.Load())
	}
}

type fixtureDelayedArtifact struct {
	batch.Artifacts
	entered, release, committed chan struct{}
}

func (a *fixtureDelayedArtifact) PutBatchResult(ctx context.Context, scope, jobID, id string, raw []byte) error {
	close(a.entered)
	select {
	case <-a.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := a.Artifacts.PutBatchResult(ctx, scope, jobID, id, raw); err != nil {
		return err
	}
	close(a.committed)
	return nil
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-531
func TestTemporalBatchLateArtifactCannotReplaceExpiredTerminalOutcome(t *testing.T) {
	t.Parallel()
	f := newTemporalBatchFixture(t, false)
	// Replace the effect only after its old worker has fully stopped, before
	// any pool/job starts. The real Garage client and prefix owner are retained.
	f.worker.Stop()
	f.worker = nil
	delayed := &fixtureDelayedArtifact{Artifacts: f.activities.Artifacts, entered: make(chan struct{}), release: make(chan struct{}), committed: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(delayed.release) })
	f.activities.Artifacts = delayed
	startFixtureWorker(t, f)
	startFixtureV2Pool(t, f, 1)
	job, _ := f.submit(t, 1)
	select {
	case <-delayed.entered:
	case <-f.ctx.Done():
		t.Fatal("validated native result never reached the delayed real artifact effect", f.ctx.Err())
	}
	store := f.activities.Store.(*batch.PostgresStore)
	var permit, claim, workflowID string
	if err := store.Pool.QueryRow(f.ctx, `SELECT permit_id::text,claim_id::text,workflow_id FROM batch_attempts WHERE job_id=$1`, job.JobID).Scan(&permit, &claim, &workflowID); err != nil {
		t.Fatal(err)
	}
	// Advance only this test-owned ledger's resolved deadline to the expired
	// finalization boundary. No live timeout or accepted request is extended.
	if _, err := store.Pool.Exec(f.ctx, `UPDATE batch_attempts SET generation_deadline=now()-interval '31 seconds' WHERE permit_id=$1`, permit); err != nil {
		t.Fatal(err)
	}
	// A concurrent pool read may win the metadata settlement. Either way, only
	// a positive authoritative terminal read below permits releasing storage.
	_, _ = f.activities.ReconcileAttempt(f.ctx, batch.AttemptReference{PoolID: f.activities.PoolID, PermitID: permit})
	terminal := f.wait(t, job.JobID, func(s batch.Status) bool { return s.Counts.Done() })
	before, err := store.ReadAttempts(f.ctx, f.activities.PoolID, []string{permit})
	if err != nil || terminal.Counts.Failed != 1 || len(before) != 1 || before[0].State != "settled" || before[0].StoppedAt != nil || before[0].Observation == nil || before[0].Observation.Outcome.Kind != "unconfirmed" {
		t.Fatal("expired result was not authoritative before the late artifact", err, terminal.Counts, before)
	}
	var publicBefore bytes.Buffer
	if err := f.jobs.WriteResults(f.ctx, f.scope, job.JobID, &publicBefore); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(delayed.release) })
	select {
	case <-delayed.committed:
	case <-f.ctx.Done():
		t.Fatal("late real Garage write never committed", f.ctx.Err())
	}
	if err := f.temporal.GetWorkflow(f.ctx, workflowID, "").Get(f.ctx, nil); err != nil {
		t.Fatal("attempt did not reconcile the immutable terminal result", err)
	}
	reader, err := delayed.OpenBatchResult(f.ctx, f.scope, job.JobID, "attempt-"+permit+".json")
	if err != nil {
		t.Fatal("late object did not exist in real Garage", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(reader, 1<<20))
	_ = reader.Close()
	var artifact struct {
		PermitID    string                   `json:"permit_id"`
		ClaimID     string                   `json:"claim_id"`
		Observation batch.AttemptObservation `json:"observation"`
		Response    json.RawMessage          `json:"response"`
	}
	if readErr != nil || json.Unmarshal(raw, &artifact) != nil || artifact.PermitID != permit || artifact.ClaimID != claim || artifact.Observation.Outcome.Kind != "completed" {
		t.Fatal("late artifact did not retain the genuine original result/owner")
	}
	var native struct {
		OutputText string `json:"output_text"`
	}
	if json.Unmarshal(artifact.Response, &native) != nil || livecapacity.ValidateBatchOutput("nonce-0000", native.OutputText, false) != nil {
		t.Fatal("late artifact did not retain the validated native output")
	}
	after, err := store.ReadAttempts(f.ctx, f.activities.PoolID, []string{permit})
	if err != nil || len(after) != 1 || after[0].Observation == nil || after[0].Observation.Outcome.Kind != "unconfirmed" || after[0].StoppedAt == nil || after[0].Ordinal != 1 || after[0].SettlementRevision != before[0].SettlementRevision+1 {
		t.Fatal("late artifact or stop proof rewrote the terminal result", err, after)
	}
	var publicAfter bytes.Buffer
	if err := f.jobs.WriteResults(f.ctx, f.scope, job.JobID, &publicAfter); err != nil || !bytes.Equal(publicBefore.Bytes(), publicAfter.Bytes()) || f.calls.Load() != 1 {
		t.Fatal("late artifact was promoted or inference replayed", err, f.calls.Load())
	}
	assertFixturePoolSettled(t, f, 1)
}
