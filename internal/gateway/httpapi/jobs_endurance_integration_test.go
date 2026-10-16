//go:build integration && temporalintegration && endurance

package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-484
// Explicit local-stack scale qualification; no public provider or credentials.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/httpclient"
	"github.com/prls-co/harden-llm/internal/livecapacity"
	"github.com/prls-co/harden-llm/stateful/batch"
)

func TestTemporalBatchTenThousandAcrossClientsAndRunContinuations(t *testing.T) {
	t.Parallel()
	f := newTemporalBatchFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	f.ctx = ctx
	f.jobs.JobTimeout = 8 * time.Minute
	var clients []*httpclient.Client
	for _, key := range []string{f.key, strings.Repeat("other-synthetic-key-", 3)} {
		c, err := httpclient.New(httpclient.Config{BaseURL: f.server.URL, APIKey: key, HTTPClient: f.server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, c)
	}
	started := time.Now()
	var jobs []httpclient.JobStatus
	firstRun := ""
	for job := range 10 {
		items := make([]httpclient.JobItem, 1000)
		for i := range items {
			nonce := fmt.Sprintf("nonce-%04d", job*1000+i)
			items[i] = httpclient.JobItem{CustomID: nonce, Request: livecapacity.BatchRequestBody(nonce, false)}
		}
		status, err := clients[job%2].SubmitJob(ctx, fmt.Sprintf("endurance-%d", job), items)
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, status)
		if job == 0 {
			f.wait(t, status.JobID, func(s batch.Status) bool { return s.Counts.Active+s.Counts.Succeeded > 0 })
			info, err := f.temporal.DescribeWorkflowExecution(ctx, "hllm-cpa-pool-v1/"+f.activities.PoolID, "")
			if err != nil {
				t.Fatal(err)
			}
			firstRun = info.WorkflowExecutionInfo.Execution.RunId
		}
	}
	seen := map[string]bool{}
	for i, job := range jobs {
		owner := clients[i%2]
		status, err := owner.WaitJob(ctx, job.JobID, func(s httpclient.JobStatus) {
			t.Logf("ten-thousand qualification alive: job=%d elapsed=%s counts=%+v", i, time.Since(started).Round(time.Second), s.Counts)
		})
		if err != nil || status.Counts.Succeeded != 1000 || status.Counts.Failed != 0 {
			t.Fatalf("job lost work: %+v %v", status.Counts, err)
		}
		var raw bytes.Buffer
		if err := owner.DownloadJobResult(ctx, job.JobID, &raw); err != nil {
			t.Fatal(err)
		}
		scan := bufio.NewScanner(&raw)
		scan.Buffer(make([]byte, 4096), 16<<20)
		for scan.Scan() {
			var row batch.Result
			if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
				t.Fatal(err)
			}
			var response struct {
				OutputText string `json:"output_text"`
			}
			if err := json.Unmarshal(row.Response, &response); err != nil {
				t.Fatal(err)
			}
			if row.Error != nil || seen[row.CustomID] || livecapacity.ValidateBatchOutput(row.CustomID, response.OutputText, false) != nil || row.Diagnostics.Attempt != 1 || row.Diagnostics.HeadersMS == nil || row.Diagnostics.OutputTokens == nil {
				t.Fatalf("unqualified, repeated or replayed item: %s", row.CustomID)
			}
			seen[row.CustomID] = true
		}
		if err := scan.Err(); err != nil {
			t.Fatal(err)
		}
		_, err = clients[(i+1)%2].JobStatus(ctx, job.JobID)
		if rejected, ok := err.(*httpclient.RejectedError); !ok || rejected.StatusCode != 404 {
			t.Fatalf("cross-key status isolation failed: %T", err)
		}
	}
	info, err := f.temporal.DescribeWorkflowExecution(ctx, "hllm-cpa-pool-v1/"+f.activities.PoolID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 10000 || f.calls.Load() != 10000 || f.peak.Load() > 128 || f.peak.Load() < 2 || firstRun == info.WorkflowExecutionInfo.Execution.RunId {
		t.Fatalf("scale/run-continuation invariant failed: distinct=%d provider_calls=%d peak=%d continued=%t", len(seen), f.calls.Load(), f.peak.Load(), firstRun != info.WorkflowExecutionInfo.Execution.RunId)
	}
	t.Logf("qualified 10000 distinct results, exactly 10000 local provider calls, two isolated bearer scopes, shared peak=%d and natural Temporal continuation in %s", f.peak.Load(), time.Since(started).Round(time.Millisecond))
}
