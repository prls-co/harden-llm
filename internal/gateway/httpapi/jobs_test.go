package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-475
import (
	"context"
	"encoding/json"
	"github.com/prls-co/harden-llm/stateful/batch"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type jobFixture struct {
	calls  int
	scope  string
	items  []batch.Item
	status batch.Status
}

func (f *jobFixture) UpstreamID() string { return "cpa" }
func (f *jobFixture) Submit(_ context.Context, scope, key string, items []batch.Item) (batch.Status, error) {
	f.calls++
	f.scope = scope
	f.items = items
	return f.status, nil
}
func (f *jobFixture) Status(_ context.Context, scope, id string) (batch.Status, error) {
	f.scope = scope
	return f.status, nil
}
func (f *jobFixture) WriteResults(_ context.Context, scope, id string, w io.Writer) error {
	_, e := io.WriteString(w, "{\"custom_id\":\"one\",\"response\":{\"output_text\":\"ok\"}}\n")
	return e
}
func TestJobAPIValidatesBeforeAdmissionAndUsesBearerScope(t *testing.T) {
	t.Parallel()
	f := &jobFixture{status: batch.Status{JobID: "job", Status: "queued", StatusURL: "/v1/jobs/job", ResultURL: "/v1/jobs/job/result", Counts: batch.Counts{Total: 1, Queued: 1}}}
	api := &API{jobs: f}
	good := `{"items":[{"custom_id":"one","request":{"model":"gpt-6-luna","input":"Return ok","reasoning":{"effort":"low"}}}]}`
	for _, tc := range []struct {
		body, key string
		want      int
	}{{good, "key", 202}, {good, "", 400}, {strings.Replace(good, "gpt-6-luna", "other", 1), "key", 400}, {strings.Replace(good, `"input":"Return ok"`, `"input":"Return ok","harden":{"recovery":{"maxAttempts":2,"retryOn":[],"backoff":{"baseDelayMs":0,"maxDelayMs":0}}}`, 1), "key", 400}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", tc.key)
		r = r.WithContext(context.WithValue(r.Context(), accessScopeKey{}, accessScope{hash: strings.Repeat("a", 64)}))
		before := f.calls
		api.createJob(w, r)
		if w.Code != tc.want {
			t.Fatalf("status %d expected %d: %s", w.Code, tc.want, w.Body.String())
		}
		if tc.want != 202 && f.calls != before {
			t.Fatal("invalid request was admitted")
		}
	}
	if f.scope != strings.Repeat("a", 64) || len(f.items) != 1 || f.items[0].Generation.ModelID != "gpt-6-luna" || f.items[0].Generation.Request().RecoveryPolicy.MaxAttempts != 1 {
		t.Fatal("scope or one-generation contract lost")
	}
	if f.items[0].Generation.ProviderOptions["stream"] != true {
		t.Fatal("native streaming not requested")
	}
}
func TestJobResultWaitsThenDeliversTerminalResults(t *testing.T) {
	f := &jobFixture{status: batch.Status{Counts: batch.Counts{Total: 1, Waiting: 1}}}
	api := &API{jobs: f}
	r := httptest.NewRequest(http.MethodGet, "/v1/jobs/job/result", nil)
	r.SetPathValue("jobID", "job")
	w := httptest.NewRecorder()
	api.jobResult(w, r)
	if w.Code != 202 || w.Header().Get("Retry-After") != "5" {
		t.Fatal("pending result not observable")
	}
	f.status.Counts = batch.Counts{Total: 1, Succeeded: 1}
	w = httptest.NewRecorder()
	api.jobResult(w, r)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/x-ndjson" || !json.Valid([]byte(strings.TrimSpace(w.Body.String()))) {
		t.Fatalf("terminal result missing: %d %s", w.Code, w.Body.String())
	}
}
