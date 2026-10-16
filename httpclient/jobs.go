package httpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/prls-co/harden-llm/api"
)

type JobItem struct {
	CustomID string          `json:"custom_id"`
	Request  json.RawMessage `json:"request"`
}
type JobCounts struct {
	Total     int `json:"total"`
	Queued    int `json:"queued"`
	Active    int `json:"active"`
	Waiting   int `json:"waiting"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

func (c JobCounts) Done() bool { return c.Total > 0 && c.Succeeded+c.Failed == c.Total }

type JobStatus struct {
	JobID               string     `json:"job_id"`
	Status              string     `json:"status"`
	Counts              JobCounts  `json:"counts"`
	CreatedAt           time.Time  `json:"created_at"`
	Deadline            time.Time  `json:"deadline"`
	ObservedAt          time.Time  `json:"observed_at"`
	LastWorkerHeartbeat *time.Time `json:"last_worker_heartbeat_at"`
	LastProgressAt      *time.Time `json:"last_progress_at"`
	NextAttemptAt       *time.Time `json:"next_attempt_at"`
	WaitingReason       string     `json:"waiting_reason,omitempty"`
	StatusURL           string     `json:"status_url"`
	ResultURL           string     `json:"result_url"`
}

func decodeJobStatus(response *http.Response) (JobStatus, error) {
	var v JobStatus
	if mediaType(response) != "application/json" {
		return v, &ProtocolError{}
	}
	raw, err := readBounded(response.Body, maximumResponseBytes)
	if err != nil {
		return v, err
	}
	if err = decodeWire("BatchJobStatus", raw, &v); err != nil {
		return v, err
	}
	c := v.Counts
	if c.Queued+c.Active+c.Waiting+c.Succeeded+c.Failed != c.Total {
		return v, &ProtocolError{}
	}
	if v.StatusURL != "/v1/jobs/"+v.JobID || v.ResultURL != v.StatusURL+"/result" {
		return v, &ProtocolError{}
	}
	if (v.Status == "completed" || v.Status == "completed_with_errors") != c.Done() {
		return v, &ProtocolError{}
	}
	return v, nil
}
func (c *Client) SubmitJob(ctx context.Context, key string, items []JobItem) (JobStatus, error) {
	var v JobStatus
	if len(key) < 1 || len(key) > 128 {
		return v, errors.New("a bounded idempotency key is required")
	}
	for _, x := range key {
		if x < ' ' || x > 126 {
			return v, errors.New("idempotency key must contain printable ASCII")
		}
	}
	raw, err := json.Marshal(struct {
		Items []JobItem `json:"items"`
	}{items})
	if err != nil || len(raw) > 32<<20 {
		return v, &ProtocolError{}
	}
	if err = api.Validate("BatchJobRequest", raw); err != nil {
		return v, &ProtocolError{}
	}
	response, err := c.requestHeaders(ctx, "POST", "/v1/jobs", "application/json", raw, map[string]string{"Idempotency-Key": key})
	if err != nil {
		return v, errors.New("batch submission acknowledgement unavailable; retry only with the same idempotency key")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return v, rejection(response)
	}
	return decodeJobStatus(response)
}
func (c *Client) JobStatus(ctx context.Context, id string) (JobStatus, error) {
	var v JobStatus
	if _, err := uuid.Parse(id); err != nil || len(id) != 36 {
		return v, errors.New("a job UUID is required")
	}
	response, err := c.request(ctx, "GET", "/v1/jobs/"+id, "application/json", nil)
	if err != nil {
		return v, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return v, rejection(response)
	}
	v, err = decodeJobStatus(response)
	if err == nil && v.JobID != id {
		return v, &ProtocolError{}
	}
	return v, err
}

// WaitJob reports status every five seconds. The caller's deadline remains the
// hard bound; a worker heartbeat never extends it or counts as completed work.
func (c *Client) WaitJob(ctx context.Context, id string, progress func(JobStatus)) (JobStatus, error) {
	return c.waitJob(ctx, id, 5*time.Second, progress)
}
func (c *Client) waitJob(ctx context.Context, id string, interval time.Duration, progress func(JobStatus)) (JobStatus, error) {
	var last JobStatus
	if ctx == nil {
		return last, errors.New("context is required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return last, errors.New("batch waiter requires an overall context deadline")
	}
	for {
		readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		v, err := c.JobStatus(readCtx, id)
		cancel()
		if err != nil {
			return last, err
		}
		last = v
		if progress != nil {
			progress(v)
		}
		if v.Counts.Done() {
			return v, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

// DownloadJobResult verifies every record and the final distinct item count.
// Writers should publish the output only after this method returns successfully.
func (c *Client) DownloadJobResult(ctx context.Context, id string, w io.Writer) error {
	if w == nil {
		return errors.New("result writer is required")
	}
	status, err := c.JobStatus(ctx, id)
	if err != nil {
		return err
	}
	if !status.Counts.Done() {
		return errors.New("batch results are not ready")
	}
	response, err := c.request(ctx, "GET", "/v1/jobs/"+id+"/result", "application/x-ndjson", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return rejection(response)
	}
	if mediaType(response) != "application/x-ndjson" {
		return &ProtocolError{}
	}
	scan := bufio.NewScanner(response.Body)
	scan.Buffer(make([]byte, 4096), maximumResponseBytes)
	seen := map[string]bool{}
	success, failed := 0, 0
	for scan.Scan() {
		line := scan.Bytes()
		if len(seen) >= status.Counts.Total {
			return &ProtocolError{}
		}
		if err = api.Validate("BatchItemResult", line); err != nil {
			return &ProtocolError{}
		}
		var record struct {
			CustomID string          `json:"custom_id"`
			Response json.RawMessage `json:"response"`
			Error    json.RawMessage `json:"error"`
		}
		if json.Unmarshal(line, &record) != nil || record.CustomID == "" || seen[record.CustomID] {
			return &ProtocolError{}
		}
		seen[record.CustomID] = true
		if len(record.Response) > 0 && len(record.Error) == 0 {
			success++
		} else if len(record.Error) > 0 && len(record.Response) == 0 {
			failed++
		} else {
			return &ProtocolError{}
		}
		if _, err = w.Write(append(append([]byte(nil), line...), '\n')); err != nil {
			return err
		}
	}
	if err = scan.Err(); err != nil {
		return err
	}
	if len(seen) != status.Counts.Total || success != status.Counts.Succeeded || failed != status.Counts.Failed {
		return &ProtocolError{}
	}
	return nil
}
