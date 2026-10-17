//go:build integration && temporalintegration && live

package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-481
// Explicit opt-in only. Ordinary verification cannot compile this entrypoint.
import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/httpclient"
	"github.com/prls-co/harden-llm/internal/livecapacity"
	"github.com/prls-co/harden-llm/stateful"
	"github.com/prls-co/harden-llm/stateful/batch"
)

func TestCPATemporalThousandBenchmark(t *testing.T) {
	if os.Getenv("HARDEN_LLM_RUN_CPA_BATCH_LIVE") != "1" || os.Getenv("CPA_API_KEY") == "" {
		t.Fatal("explicit live CPA opt-in and protected key are required")
	}
	caseName := os.Getenv("HARDEN_LLM_CPA_BATCH_BENCH_CASE")
	policy := batch.DefaultPolicy()
	// Baseline pacing belongs to the producer; rolling cases deliberately probe.
	policy.WindowRequests = 0
	baseline := false
	mixed := false
	boundedText := false
	sustained := false
	totalLogical := 1000
	switch caseName {
	case "baseline":
		baseline = true
		// The producer releases 200 every 61 seconds. Keep all 1,000 starts
		// eligible for those bursts so a second timer-based pacer cannot distort
		// the fixed-cadence comparator. Active concurrency remains bounded.
		policy.Burst = 1000
	case "rolling-128-50":
		policy.StartRate, policy.Burst = 50, 100
	case "rolling-128-100":
		policy.StartRate = 100
	case "rolling-200-100":
		policy.Concurrency = 200
		policy.StartRate = 100
	case "rolling-256-100":
		policy.Concurrency = 256
		policy.StartRate = 100
	case "mixed-128-50":
		mixed = true
	case "sustained-windowed-3000", "sustained-continuous-3000", "sustained-mixed-windowed-3000", "sustained-mixed-continuous-3000":
		sustained, totalLogical = true, 3000
		if !strings.Contains(caseName, "continuous") {
			policy.WindowRequests, policy.WindowDuration = 200, 61*time.Second
		}
		mixed = strings.Contains(caseName, "mixed")
		boundedText = mixed
	case "windowed-128-100", "windowed-200-100", "mixed-windowed-128-100", "mixed-bounded-text-128-100":
		policy.StartRate = 100
		policy.Burst = 200
		policy.WindowRequests = 200
		policy.WindowDuration = 61 * time.Second
		if caseName == "windowed-200-100" {
			policy.Concurrency = 200
		}
		mixed = caseName == "mixed-windowed-128-100" || caseName == "mixed-bounded-text-128-100"
		boundedText = caseName == "mixed-bounded-text-128-100"
	default:
		t.Fatal("unknown bounded benchmark case")
	}
	requestBody := func(nonce string, long bool) json.RawMessage {
		if boundedText && long {
			return livecapacity.BoundedTextBatchRequestBody(nonce)
		}
		return livecapacity.BatchRequestBody(nonce, long)
	}
	validateOutput := func(nonce, text string, long bool) error {
		if boundedText && long {
			return livecapacity.ValidateBoundedTextBatchOutput(nonce, text)
		}
		return livecapacity.ValidateBatchOutput(nonce, text, long)
	}
	dir := os.Getenv("HARDEN_LLM_CPA_BATCH_BENCH_OUTPUT")
	if dir == "" || !filepath.IsAbs(dir) {
		t.Fatal("fresh absolute private output directory required")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.OpenFile(filepath.Join(dir, "attempts.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	summary := map[string]any{"case": caseName, "policy": policy, "logicalRequests": totalLogical, "twoBearerClientsSharePool": sustained, "model": "gpt-6-luna", "reasoning": "low", "sharedCPAConfigurationChanged": false, "officialAPIEnabled": false, "startedAt": time.Now().UTC(), "status": "running"}
	sha, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	summary["sourceSHA"] = strings.TrimSpace(string(sha))
	patch, _ := exec.Command("git", "diff", "--binary", "HEAD").Output()
	patchHash := sha256.Sum256(patch)
	summary["sourceDirty"] = len(patch) > 0
	summary["sourceDiffSHA256"] = hex.EncodeToString(patchHash[:])
	binary, _ := os.Executable()
	data, _ := os.ReadFile(binary)
	binaryHash := sha256.Sum256(data)
	summary["binarySHA256"] = hex.EncodeToString(binaryHash[:])
	defer func() {
		summary["finishedAt"] = time.Now().UTC()
		if t.Failed() {
			summary["status"] = "failed"
		}
		raw, _ := json.MarshalIndent(summary, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "summary.json"), raw, 0600); err != nil {
			t.Error(err)
		}
	}()
	f := newTemporalBatchFixture(t, false)
	budget := 15 * time.Minute
	if sustained {
		budget = 35 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	f.ctx = ctx
	f.jobs.JobTimeout = 12 * time.Minute
	if sustained {
		f.jobs.JobTimeout = 30 * time.Minute
	}
	f.worker.Stop()
	f.worker = nil
	core, err := stateful.NewClient(stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: "https://cpa.prls.co/v1", APIKey: os.Getenv("CPA_API_KEY")}}, DefaultConnection: "cpa", EndpointPolicy: hardenllm.EndpointPolicy{AllowedHosts: []string{"cpa.prls.co"}, MaxConnsPerHost: policy.Concurrency, ResponseHeaderTimeout: 180 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	f.activities.Policy = policy
	f.activities.Logger = slog.New(slog.NewJSONHandler(logFile, nil))
	f.activities.Execute = func(ctx context.Context, _ string, r hardenllm.Request) (hardenllm.Result, error) {
		return core.Call(ctx, r)
	}
	startFixtureWorker(t, f)
	c, err := httpclient.New(httpclient.Config{BaseURL: f.server.URL, APIKey: f.key, HTTPClient: f.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	clients := []*httpclient.Client{c}
	if sustained {
		other, err := httpclient.New(httpclient.Config{BaseURL: f.server.URL, APIKey: strings.Repeat("other-synthetic-key-", 3), HTTPClient: f.server.Client()})
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, other)
	}
	// Qualify two requests before the load run. A separate, serially used
	// private dispatcher keeps calibration outside the measured initial state.
	pilotPrefix := "pilot-" + uuid.NewString()
	pilotItems := []httpclient.JobItem{{CustomID: pilotPrefix + "-0", Request: requestBody(pilotPrefix+"-0", false)}, {CustomID: pilotPrefix + "-1", Request: requestBody(pilotPrefix+"-1", mixed)}}
	pilot, err := c.SubmitJob(ctx, pilotPrefix, pilotItems)
	if err != nil {
		summary["pilotSubmissionErrorType"] = fmt.Sprintf("%T", err)
		if rejected, ok := err.(*httpclient.RejectedError); ok {
			summary["pilotSubmissionCode"] = rejected.Problem.Code
			summary["pilotSubmissionMessage"] = rejected.Problem.Message
		}
		t.Fatal("pilot submission failed", err)
	}
	pilot, err = c.WaitJob(ctx, pilot.JobID, func(v httpclient.JobStatus) { t.Logf("benchmark alive: pilot %+v", v.Counts) })
	summary["pilot"] = pilot
	if err != nil || pilot.Counts.Succeeded != 2 {
		t.Fatal("pilot did not qualify both requests", err)
	}
	pilotPath := filepath.Join(dir, "pilot-results.ndjson")
	pilotFile, err := os.OpenFile(pilotPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.DownloadJobResult(ctx, pilot.JobID, pilotFile); err != nil {
		pilotFile.Close()
		t.Fatal(err)
	}
	pilotFile.Seek(0, 0)
	pilotScan := bufio.NewScanner(pilotFile)
	pilotScan.Buffer(make([]byte, 4096), 16<<20)
	for pilotScan.Scan() {
		var row batch.Result
		json.Unmarshal(pilotScan.Bytes(), &row)
		var response struct {
			OutputText string `json:"output_text"`
		}
		json.Unmarshal(row.Response, &response)
		if err = validateOutput(row.CustomID, response.OutputText, mixed && strings.HasSuffix(row.CustomID, "-1")); err != nil {
			pilotFile.Close()
			t.Fatal("pilot content qualification failed", err)
		}
	}
	err = pilotScan.Err()
	pilotFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	summary["pilotRequests"] = 2
	f.worker.Stop()
	f.worker = nil
	f.activities.PoolID = uuid.NewString()
	startFixtureWorker(t, f)
	// A quiet interval is part of the experiment, not an assertion that every
	// account-wide quota window has reset. Background account traffic is external.
	for i := 0; i < 18; i++ {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Second):
			t.Logf("benchmark alive: quiet-before-case %s %d/90 seconds", caseName, (i+1)*5)
		}
	}
	prefix := "bench-" + uuid.NewString()
	start := time.Now().UTC()
	summary["firstSubmissionAt"] = start
	var jobs []httpclient.JobStatus
	var jobClients []*httpclient.Client
	waves := 1
	perWave := 1000
	if baseline {
		waves = 5
		perWave = 200
	}
	if sustained {
		waves, perWave = 3, 1000
	}
	for wave := 0; wave < waves; wave++ {
		if baseline && wave > 0 {
			release := start.Add(time.Duration(wave) * 61 * time.Second)
			for time.Now().Before(release) {
				d := min(5*time.Second, time.Until(release))
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(d):
					t.Logf("benchmark alive: baseline release %d waits until %s", wave, release)
				}
			}
		}
		items := make([]httpclient.JobItem, perWave)
		for i := range items {
			index := wave*perWave + i
			nonce := fmt.Sprintf("%s-%04d", prefix, index)
			items[i] = httpclient.JobItem{CustomID: nonce, Request: requestBody(nonce, mixed && index%10 == 0)}
		}
		owner := clients[wave%len(clients)]
		job, err := owner.SubmitJob(ctx, fmt.Sprintf("%s-%d", prefix, wave), items)
		if err != nil {
			summary["submissionErrorType"] = fmt.Sprintf("%T", err)
			if rejected, ok := err.(*httpclient.RejectedError); ok {
				summary["submissionHTTPStatus"] = rejected.StatusCode
				summary["submissionCode"] = rejected.Problem.Code
				summary["submissionMessage"] = rejected.Problem.Message
			}
			t.Fatal("batch submission failed", err)
		}
		jobs = append(jobs, job)
		jobClients = append(jobClients, owner)
	}
	var final []httpclient.JobStatus
	for i, job := range jobs {
		status, err := jobClients[i].WaitJob(ctx, job.JobID, func(v httpclient.JobStatus) {
			raw, _ := json.Marshal(v)
			t.Logf("benchmark alive: %s", raw)
			if v.WaitingReason == "account_restricted" {
				cancel()
			}
		})
		final = append(final, status)
		summary["jobs"] = final
		if err != nil {
			t.Fatal("batch waiting failed", err)
		}
	}
	readyAt := start
	totalSuccess, totalFailed := 0, 0
	for _, j := range final {
		totalSuccess += j.Counts.Succeeded
		totalFailed += j.Counts.Failed
		if j.LastProgressAt != nil && j.LastProgressAt.After(readyAt) {
			readyAt = *j.LastProgressAt
		}
	}
	summary["validatedByCore"] = totalSuccess
	summary["failedItems"] = totalFailed
	summary["resultReadyAt"] = readyAt
	summary["resultReadyMs"] = readyAt.Sub(start).Milliseconds()
	summary["completionObservedMs"] = time.Since(start).Milliseconds()
	seen := map[string]bool{}
	downloaded := map[string]bool{}
	qualificationFailures := []map[string]string{}
	callerAttempts := 0
	var inputTokens, outputTokens int64
	for i, j := range final {
		path := filepath.Join(dir, fmt.Sprintf("results-%d.ndjson", i))
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		err = jobClients[i].DownloadJobResult(ctx, j.JobID, file)
		file.Close()
		if err != nil {
			t.Fatal("result download failed", err)
		}
		reader, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scan := bufio.NewScanner(reader)
		scan.Buffer(make([]byte, 4096), 16<<20)
		for scan.Scan() {
			var record batch.Result
			if err = json.Unmarshal(scan.Bytes(), &record); err != nil {
				reader.Close()
				t.Fatal(err)
			}
			callerAttempts += record.Diagnostics.Attempt
			if downloaded[record.CustomID] {
				reader.Close()
				t.Fatal("duplicate downloaded item")
			}
			downloaded[record.CustomID] = true
			if record.Diagnostics.InputTokens != nil {
				inputTokens += *record.Diagnostics.InputTokens
			}
			if record.Diagnostics.OutputTokens != nil {
				outputTokens += *record.Diagnostics.OutputTokens
			}
			if record.Error != nil {
				continue
			}
			var native struct {
				OutputText string `json:"output_text"`
				Model      string `json:"model"`
				Status     string `json:"status"`
			}
			if json.Unmarshal(record.Response, &native) != nil {
				reader.Close()
				t.Fatal("invalid Responses result")
			}
			index, parseErr := strconv.Atoi(record.CustomID[strings.LastIndex(record.CustomID, "-")+1:])
			if parseErr != nil || validateOutput(record.CustomID, native.OutputText, mixed && index%10 == 0) != nil || seen[record.CustomID] || native.Model != "gpt-6-luna" || native.Status != "completed" {
				qualificationFailures = append(qualificationFailures, map[string]string{"customId": record.CustomID, "code": "independent_result_qualification_failed"})
				continue
			}

			seen[record.CustomID] = true
		}
		err = scan.Err()
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	summary["distinctValidatedResults"] = len(seen)
	summary["distinctDownloadedResults"] = len(downloaded)
	summary["independentQualificationFailures"] = qualificationFailures
	summary["callerAttemptsFromItemCounts"] = callerAttempts
	summary["inputTokens"] = inputTokens
	summary["outputTokensIncludingReasoning"] = outputTokens
	summary["downloadFinishedMs"] = time.Since(start).Milliseconds()
	if len(seen) != totalLogical || totalSuccess != totalLogical || totalFailed != 0 {
		t.Fatalf("case incomplete: validated=%d successful=%d failed=%d", len(seen), totalSuccess, totalFailed)
	}
	summary["status"] = "passed"
}
