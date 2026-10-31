//go:build integration && temporalintegration && endurance

package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-520
// Explicit local mixed V2 qualification. All provider calls terminate at the
// process-owned TLS fixture; real CPA credentials and public endpoints are absent.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/httpclient"
	"github.com/prls-co/harden-llm/internal/livecapacity"
	"github.com/prls-co/harden-llm/stateful/batch"
)

type mixedNativeStart struct {
	ID        string `json:"id"`
	ElapsedMS int64  `json:"elapsed_ms"`
}
type mixedLoadReport struct {
	LedgerSnapshotConfirmed                                 bool
	LedgerAttempts, NeverSentAttempts                       int
	ClaimP50MS, ClaimP95MS, ClaimMaxMS                      float64
	Accepted                                                bool
	Total, Durable, Ordinary, Replicas, Scopes, Concurrency int
	ElapsedMS                                               int64
	NativeRequests, NativePeak                              int32
	DistinctNative, DistinctResults, OrdinaryErrors         int
	FirstError                                              string `json:",omitempty"`
	PeakHeapBytes                                           uint64
	PeakGoroutines                                          int
	QueueP50MS, QueueP95MS, QueueMaxMS, NativeP95MS         float64
	DatabaseTransactions, DatabaseUpdatedRows               int64
	Workflows                                               map[string]int64
	Continued                                               bool
	SnapshotBytes                                           int
	Starts                                                  []mixedNativeStart
}
type mixedJob struct {
	owner  int
	status httpclient.JobStatus
	count  int
}
type mixedOrdinaryResult struct {
	id     string
	result ordinaryHTTPResult
}

func TestTemporalBatchMixedThousand(t *testing.T) { t.Parallel(); qualifyMixedCPALoad(t, 1000, 200) }
func TestTemporalBatchMixedTenThousand(t *testing.T) {
	t.Parallel()
	qualifyMixedCPALoad(t, 10000, 1000)
}

func qualifyMixedCPALoad(t *testing.T, total, ordinaryCount int) {
	t.Helper()
	started := time.Now()
	var nativeClock atomic.Pointer[time.Time]
	nativeClock.Store(&started)
	var nativeMu sync.Mutex
	nativeIDs := map[string]int{}
	starts := make([]mixedNativeStart, 0, total)
	f := newTemporalBatchFixtureWithGate(t, false, func(ctx context.Context, id string) {
		nativeMu.Lock()
		nativeIDs[id]++
		starts = append(starts, mixedNativeStart{ID: id, ElapsedMS: time.Since(*nativeClock.Load()).Milliseconds()})
		nativeMu.Unlock()
		timer := time.NewTimer(5 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	f.ctx = ctx
	f.jobs.JobTimeout = 8 * time.Minute
	var owned sync.WaitGroup
	defer func() { cancel(); owned.Wait() }()
	enableFixtureOrdinaryAdmission(t, f)
	replica := newMixedFixtureReplica(t, f)
	replicas := []*temporalBatchFixture{f, replica}
	startFixtureV2Pool(t, f, 128)
	keys := []string{f.key, strings.Repeat("other-synthetic-key-", 3)}
	clients := make([][]*httpclient.Client, 2)
	for replicaIndex, r := range replicas {
		for _, key := range keys {
			c, err := httpclient.New(httpclient.Config{BaseURL: r.server.URL, APIKey: key, HTTPClient: r.server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			clients[replicaIndex] = append(clients[replicaIndex], c)
		}
	}
	store := f.activities.Store.(*batch.PostgresStore)
	beforeTx, beforeUpdates := mixedDatabaseCounters(t, f)
	report := mixedLoadReport{Total: total, Durable: total - ordinaryCount, Ordinary: ordinaryCount, Replicas: 2, Scopes: 2, Concurrency: 128}
	defer func() {
		report.Accepted = !t.Failed()
		diagnosticCtx, stopDiagnostic := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		diagnosticErr := store.Pool.QueryRow(diagnosticCtx, `SELECT count(*),count(*) FILTER(WHERE observation->'outcome'->>'kind'='never_dispatched'),COALESCE(percentile_cont(0.5) WITHIN GROUP(ORDER BY extract(epoch FROM (claimed_at-granted_at))*1000),0),COALESCE(percentile_cont(0.95) WITHIN GROUP(ORDER BY extract(epoch FROM (claimed_at-granted_at))*1000),0),COALESCE(max(extract(epoch FROM (claimed_at-granted_at))*1000),0) FROM batch_attempts`).Scan(&report.LedgerAttempts, &report.NeverSentAttempts, &report.ClaimP50MS, &report.ClaimP95MS, &report.ClaimMaxMS)
		report.LedgerSnapshotConfirmed = diagnosticErr == nil
		stopDiagnostic()
		report.ElapsedMS = time.Since(started).Milliseconds()
		report.NativeRequests = f.calls.Load()
		report.NativePeak = f.peak.Load()
		nativeMu.Lock()
		report.DistinctNative = len(nativeIDs)
		report.Starts = append([]mixedNativeStart(nil), starts...)
		nativeMu.Unlock()
		sort.Slice(report.Starts, func(i, j int) bool { return report.Starts[i].ElapsedMS < report.Starts[j].ElapsedMS })
		path := filepath.Join("..", "..", "..", "tmp", fmt.Sprintf("cpa-mixed-%d-%d.json", total, os.Getpid()))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err == nil {
			raw, _ := json.MarshalIndent(report, "", "  ")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Error(err)
			}
		} else {
			t.Error(err)
		}
		t.Logf("mixed qualification report=%s total=%d completed=%d native=%d distinct=%d peak=%d elapsed=%s heap_peak_bytes=%d goroutines_peak=%d ordinary_errors=%d", path, total, report.DistinctResults, report.NativeRequests, report.DistinctNative, report.NativePeak, time.Since(started).Round(time.Millisecond), report.PeakHeapBytes, report.PeakGoroutines, report.OrdinaryErrors)
	}()
	started = time.Now()
	nativeClock.Store(&started)
	launch := make(chan struct{})
	jobResults := make(chan mixedJob, (total-ordinaryCount+999)/1000+32)
	submitErrors := make(chan error, 2)
	ordinaryResults := make(chan mixedOrdinaryResult, ordinaryCount)
	var ordinaryDone atomic.Int64
	jobSize := min(1000, (total-ordinaryCount)/2)
	for owner := range 2 {
		owned.Add(1)
		go func(owner int) {
			defer owned.Done()
			<-launch
			for base := owner * jobSize; base < total-ordinaryCount; base += 2 * jobSize {
				count := min(jobSize, total-ordinaryCount-base)
				items := make([]httpclient.JobItem, count)
				for i := range items {
					nonce := fmt.Sprintf("nonce-%04d", base+i)
					items[i] = httpclient.JobItem{CustomID: nonce, Request: livecapacity.BatchRequestBody(nonce, false)}
				}
				status, err := clients[owner][owner].SubmitJob(ctx, fmt.Sprintf("mixed-%d-%d", total, base), items)
				if err != nil {
					submitErrors <- err
					return
				}
				jobResults <- mixedJob{owner: owner, status: status, count: count}
			}
		}(owner)
	}
	for index := range ordinaryCount {
		owned.Add(1)
		go func(index int) {
			defer owned.Done()
			<-launch
			id := fmt.Sprintf("nonce-%04d", total-ordinaryCount+index)
			result := ordinaryFixtureCallAs(ctx, replicas[index%2], keys[(index/2)%2], "/v1/responses", id, "", "off", false, 0)
			ordinaryDone.Add(1)
			ordinaryResults <- mixedOrdinaryResult{id: id, result: result}
		}(index)
	}
	firstRun, err := mixedPoolRunID(f)
	if err != nil {
		t.Fatal(err)
	}
	close(launch)
	completedSubmissions := make(chan struct{})
	go func() { owned.Wait(); close(completedSubmissions) }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	lastLog := time.Time{}
	continued := false
	var jobs []mixedJob
	submittersDone := false
	// The ordinary 60-second deadline is preserved. Diagnostics never extend it.
	for !submittersDone {
		select {
		case <-completedSubmissions:
			submittersDone = true
		case job := <-jobResults:
			jobs = append(jobs, job)
		case err := <-submitErrors:
			report.FirstError = err.Error()
			t.Fatal("durable admission failed under mixed load", err)
		case <-ticker.C:
			mixedSampleProcess(&report)
			if !continued && f.calls.Load() >= 32 && f.active.Load() > 0 {
				if err := mixedContinuePool(f); err != nil {
					t.Fatal(err)
				}
				continued = true
			}
			if time.Since(lastLog) >= 5*time.Second {
				lastLog = time.Now()
				t.Logf("mixed qualification alive total=%d native=%d ordinary_finished=%d active=%d elapsed=%s", total, f.calls.Load(), ordinaryDone.Load(), f.active.Load(), time.Since(started).Round(time.Second))
			}
		case <-ctx.Done():
			report.FirstError = ctx.Err().Error()
			t.Fatal("mixed qualification deadline", ctx.Err())
		}
	}
	for {
		select {
		case job := <-jobResults:
			jobs = append(jobs, job)
		default:
			goto jobsDrained
		}
	}
jobsDrained:
	select {
	case err := <-submitErrors:
		report.FirstError = err.Error()
		t.Fatal(err)
	default:
	}
	seen := map[string]bool{}
	for range ordinaryCount {
		v := <-ordinaryResults
		if v.result.err != nil || v.result.status != http.StatusOK {
			report.OrdinaryErrors++
			if report.FirstError == "" {
				report.FirstError = fmt.Sprintf("ordinary %s status=%d error=%v body=%s", v.id, v.result.status, v.result.err, v.result.body)
			}
			continue
		}
		var response struct {
			OutputText string `json:"output_text"`
		}
		if json.Unmarshal(v.result.body, &response) != nil || livecapacity.ValidateBatchOutput(v.id, response.OutputText, false) != nil || seen[v.id] {
			t.Fatal("ordinary result identity was lost or repeated", v.id)
		}
		seen[v.id] = true
	}
	report.DistinctResults = len(seen)
	if report.OrdinaryErrors > 0 {
		t.Fatal("mixed ordinary calls failed", report.OrdinaryErrors, report.FirstError)
	}
	for _, job := range jobs {
		owner := clients[job.owner][job.owner]
		status, err := owner.WaitJob(ctx, job.status.JobID, func(s httpclient.JobStatus) {
			mixedSampleProcess(&report)
			t.Logf("mixed durable alive job=%s counts=%+v native=%d elapsed=%s", s.JobID, s.Counts, f.calls.Load(), time.Since(started).Round(time.Second))
		})
		if err != nil || status.Counts.Succeeded != job.count || status.Counts.Failed != 0 {
			t.Fatal("durable work lost under mixed load", status.Counts, err)
		}
		var raw bytes.Buffer
		if err := owner.DownloadJobResult(ctx, job.status.JobID, &raw); err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(&raw)
		scanner.Buffer(make([]byte, 4096), 16<<20)
		for scanner.Scan() {
			var row batch.Result
			if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
				t.Fatal(err)
			}
			var response struct {
				OutputText string `json:"output_text"`
			}
			if json.Unmarshal(row.Response, &response) != nil || row.Error != nil || row.Diagnostics.Attempt != 1 || seen[row.CustomID] || livecapacity.ValidateBatchOutput(row.CustomID, response.OutputText, false) != nil {
				t.Fatal("missing, replayed or mismatched durable result", row.CustomID)
			}
			seen[row.CustomID] = true
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		_, err = clients[(job.owner+1)%2][(job.owner+1)%2].JobStatus(ctx, job.status.JobID)
		if rejected, ok := err.(*httpclient.RejectedError); !ok || rejected.StatusCode != 404 {
			t.Fatal("cross-key durable status isolation failed", err)
		}
	}
	report.DistinctResults = len(seen)
	final, err := mixedWaitPoolIdle(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := final.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	report.SnapshotBytes = len(snapshot)
	runID, err := mixedPoolRunID(f)
	if err != nil {
		t.Fatal(err)
	}
	report.Continued = runID != firstRun
	var attempts, unknown, waiters, jobsCount, items, history, ordinaryArtifacts int
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM batch_attempts),(SELECT count(*) FROM batch_attempts WHERE state<>'settled' OR local_stopped_at IS NULL),(SELECT count(*) FROM batch_request_waiters),(SELECT count(*) FROM batch_jobs),(SELECT count(*) FROM batch_items),(SELECT count(*) FROM history),(SELECT count(*) FROM batch_attempts WHERE kind='ordinary' AND observation->'outcome'->>'artifactId' IS NOT NULL)`).Scan(&attempts, &unknown, &waiters, &jobsCount, &items, &history, &ordinaryArtifacts); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT COALESCE(percentile_cont(0.5) WITHIN GROUP(ORDER BY (observation->'outcome'->>'queueMs')::double precision),0),COALESCE(percentile_cont(0.95) WITHIN GROUP(ORDER BY (observation->'outcome'->>'queueMs')::double precision),0),COALESCE(max((observation->'outcome'->>'queueMs')::double precision),0),COALESCE(percentile_cont(0.95) WITHIN GROUP(ORDER BY (observation->'outcome'->>'durationMs')::double precision),0) FROM batch_attempts`).Scan(&report.QueueP50MS, &report.QueueP95MS, &report.QueueMaxMS, &report.NativeP95MS); err != nil {
		t.Fatal(err)
	}
	afterTx, afterUpdates := mixedDatabaseCounters(t, f)
	report.DatabaseTransactions = afterTx - beforeTx
	report.DatabaseUpdatedRows = afterUpdates - beforeUpdates
	report.Workflows, err = mixedWorkflowCounts(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	nativeMu.Lock()
	distinct := len(nativeIDs)
	duplicate := false
	for _, n := range nativeIDs {
		duplicate = duplicate || n != 1
	}
	nativeMu.Unlock()
	if len(seen) != total || distinct != total || duplicate || f.calls.Load() != int32(total) || f.peak.Load() > 128 || f.peak.Load() < 2 || attempts != total || unknown != 0 || waiters != ordinaryCount || items != total-ordinaryCount || jobsCount != len(jobs) || history != 0 || ordinaryArtifacts != 0 || !report.Continued || report.Workflows[batch.AttemptWorkflowName] != int64(total-ordinaryCount) {
		t.Fatal("mixed scale invariant failed", len(seen), distinct, duplicate, f.calls.Load(), f.peak.Load(), attempts, unknown, waiters, items, jobsCount, history, ordinaryArtifacts, report.Continued, report.Workflows)
	}
	if report.PeakHeapBytes > 768<<20 || report.PeakGoroutines > 12000 {
		t.Fatal("mixed client/worker process exceeded qualified resource bounds", report.PeakHeapBytes, report.PeakGoroutines)
	}
}

func mixedSampleProcess(report *mixedLoadReport) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	report.PeakHeapBytes = max(report.PeakHeapBytes, m.HeapAlloc)
	report.PeakGoroutines = max(report.PeakGoroutines, runtime.NumGoroutine())
}
func mixedDatabaseCounters(t *testing.T, f *temporalBatchFixture) (int64, int64) {
	t.Helper()
	var tx, updates int64
	pool := f.activities.Store.(*batch.PostgresStore).Pool
	if err := pool.QueryRow(f.ctx, `SELECT xact_commit+xact_rollback,tup_updated FROM pg_stat_database WHERE datname=current_database()`).Scan(&tx, &updates); err != nil {
		t.Fatal(err)
	}
	return tx, updates
}
