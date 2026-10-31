package httpclient

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-478
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const jobFixtureID = "00000000-0000-4000-8000-000000000001"

func fixtureJobStatus(done bool) map[string]any {
	c := map[string]int{"total": 2, "queued": 0, "active": 0, "waiting": 2, "succeeded": 0, "failed": 0}
	state := "waiting"
	if done {
		state = "completed_with_errors"
		c["waiting"] = 0
		c["failed"] = 2
	}
	return map[string]any{"job_id": jobFixtureID, "status": state, "counts": c, "created_at": "2026-10-16T00:00:00Z", "deadline": "2026-10-16T02:00:00Z", "observed_at": "2026-10-16T00:01:00Z", "last_worker_heartbeat_at": "2026-10-16T00:01:00Z", "last_progress_at": "2026-10-16T00:00:05Z", "next_attempt_at": nil, "status_url": "/v1/jobs/" + jobFixtureID, "result_url": "/v1/jobs/" + jobFixtureID + "/result"}
}
func TestWaitJobReportsWaitingAndNeverRepostsInference(t *testing.T) {
	t.Parallel()
	gets, posts, progress := 0, 0, 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			posts++
			t.Error("unexpected mutation")
		}
		gets++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fixtureJobStatus(gets > 1))
	}))
	defer s.Close()
	c, err := New(Config{BaseURL: s.URL, APIKey: strings.Repeat("synthetic-key", 4), HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := c.waitJob(ctx, jobFixtureID, time.Millisecond, func(JobStatus) { progress++ })
	if err != nil || status.Counts.Failed != 2 || gets != 2 || posts != 0 || progress != 2 {
		t.Fatalf("wait contract: %+v %v gets=%d posts=%d progress=%d", status, err, gets, posts, progress)
	}
	if _, err = c.WaitJob(context.Background(), jobFixtureID, nil); err == nil {
		t.Fatal("unbounded waiter accepted")
	}
}
func TestJobDownloadRejectsDuplicateOrMissingItemRecords(t *testing.T) {
	t.Parallel()
	for _, lines := range []int{1, 2} {
		t.Run(string(rune('0'+lines)), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/result") {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(fixtureJobStatus(true))
					return
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				for i := 0; i < lines; i++ {
					io.WriteString(w, `{"custom_id":"same","error":{"code":"fixture","message":"failed"},"diagnostics":{"kind":"failed","attempt":1,"queueMs":0,"durationMs":0,"cpaInternalAttempts":null}}`+"\n")
				}
			}))
			defer s.Close()
			c, _ := New(Config{BaseURL: s.URL, APIKey: strings.Repeat("synthetic-key", 4), HTTPClient: s.Client()})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := c.DownloadJobResult(ctx, jobFixtureID, io.Discard); err == nil {
				t.Fatal("incomplete/duplicate download accepted")
			}
		})
	}
}

func TestJobRejectionUsesOrdinaryOpenAIErrorContract(t *testing.T) {
	t.Parallel()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"error":{"message":"The durable job operation could not be completed.","type":"server_error","param":null,"code":"idempotency_conflict"}}`)
	}))
	defer s.Close()
	c, _ := New(Config{BaseURL: s.URL, APIKey: strings.Repeat("synthetic-key", 4), HTTPClient: s.Client()})
	_, err := c.SubmitJob(context.Background(), "same-key", []JobItem{{CustomID: "one", Request: json.RawMessage(`{"model":"gpt-6-luna","input":"return ok"}`)}})
	var rejected *RejectedError
	if !errors.As(err, &rejected) || rejected.StatusCode != 409 || rejected.Problem.Code != "idempotency_conflict" {
		t.Fatalf("expected typed canonical rejection, got %T", err)
	}
}

func TestWaitJobStopsOnCancellationAfterReportingProgress(t *testing.T) {
	t.Parallel()
	gets := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets++
		if r.Method != http.MethodGet {
			t.Error("waiting submitted inference")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fixtureJobStatus(false))
	}))
	defer s.Close()
	c, _ := New(Config{BaseURL: s.URL, APIKey: strings.Repeat("synthetic-key", 4), HTTPClient: s.Client()})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := c.WaitJob(ctx, jobFixtureID, func(JobStatus) { cancel() })
	if !errors.Is(err, context.Canceled) || gets != 1 || status.Counts.Waiting != 2 {
		t.Fatalf("progress must not extend cancellation: gets=%d error=%v", gets, err)
	}
}
