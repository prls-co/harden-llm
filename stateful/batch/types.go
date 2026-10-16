package batch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

const MaximumItems = 1000
const BatchWorkflowName = "HLLMBatchV1"
const DispatcherWorkflowName = "HLLMCPADispatcherV1"
const enqueueSignal = "enqueue"
const completedSignal = "completed"
const stateQuery = "state"

var ErrNotFound = errors.New("batch: not found")
var ErrConflict = errors.New("batch: idempotency key reused with different input")
var ErrUnavailable = errors.New("batch: durable job service unavailable")

// Generation is a credential-free immutable request snapshot, never a profile.
type Generation struct {
	ConnectionID      string                    `json:"upstream"`
	ModelID           string                    `json:"model"`
	Messages          []hardenllm.Message       `json:"messages"`
	CallType          hardenllm.CallType        `json:"callType"`
	Schema            json.RawMessage           `json:"schema,omitempty"`
	Tools             []hardenllm.FunctionTool  `json:"tools,omitempty"`
	ToolChoice        hardenllm.ToolChoice      `json:"toolChoice,omitempty"`
	ReasoningEffort   hardenllm.ReasoningEffort `json:"reasoning_effort"`
	ProviderOptions   map[string]any            `json:"providerOptions,omitempty"`
	ParallelToolCalls bool                      `json:"parallel_tool_calls"`
	TimeoutMS         int64                     `json:"timeout_ms"`
}

func SingleAttemptPolicy() hardenllm.RecoveryPolicy {
	p := hardenllm.DefaultRecoveryPolicy()
	p.MaxAttempts = 1
	p.RetryOn = []hardenllm.RecoveryCategory{}
	p.JSONRepair = nil
	p.Rerun = nil
	return p
}
func (g Generation) Request() hardenllm.Request {
	return hardenllm.Request{ConnectionID: g.ConnectionID, ModelID: g.ModelID, Messages: g.Messages, CallType: g.CallType, Schema: g.Schema, Tools: g.Tools, ToolChoice: g.ToolChoice, ReasoningEffort: g.ReasoningEffort, ProviderOptions: g.ProviderOptions, CacheMode: hardenllm.CacheModeOff, RecoveryPolicy: SingleAttemptPolicy()}
}

type Item struct {
	CustomID   string     `json:"custom_id"`
	Generation Generation `json:"generation"`
}
type Ref struct {
	Scope string
	JobID string
	Index int
}
type JobRef struct {
	Scope      string
	JobID      string
	Count      int
	CreatedAt  time.Time
	Deadline   time.Time
	WorkflowID string
}
type Counts struct {
	Total     int `json:"total"`
	Queued    int `json:"queued"`
	Active    int `json:"active"`
	Waiting   int `json:"waiting"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

func (c Counts) Done() bool { return c.Total > 0 && c.Succeeded+c.Failed == c.Total }

type Status struct {
	JobID               string     `json:"job_id"`
	Status              string     `json:"status"`
	Counts              Counts     `json:"counts"`
	CreatedAt           time.Time  `json:"created_at"`
	Deadline            time.Time  `json:"deadline"`
	ObservedAt          time.Time  `json:"observed_at"`
	LastWorkerHeartbeat *time.Time `json:"last_worker_heartbeat_at"`
	LastProgressAt      *time.Time `json:"last_progress_at"`
	WaitingReason       string     `json:"waiting_reason,omitempty"`
	NextAttemptAt       *time.Time `json:"next_attempt_at"`
	StatusURL           string     `json:"status_url"`
	ResultURL           string     `json:"result_url"`
}
type ItemError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
}
type Outcome struct {
	Kind                string     `json:"kind"`
	Error               *ItemError `json:"error,omitempty"`
	RetryAfterMS        int64      `json:"retryAfterMs,omitempty"`
	ArtifactID          string     `json:"artifactId,omitempty"`
	Attempt             int        `json:"attempt"`
	QueueMS             int64      `json:"queueMs"`
	RetryWaitMS         int64      `json:"retryWaitMs"`
	StartedAt           *time.Time `json:"startedAt,omitempty"`
	FinishedAt          *time.Time `json:"finishedAt,omitempty"`
	DurationMS          int64      `json:"durationMs"`
	HeadersMS           *int64     `json:"headersMs,omitempty"`
	FirstOutputMS       *int64     `json:"firstOutputMs,omitempty"`
	InputTokens         *int64     `json:"inputTokens,omitempty"`
	OutputTokens        *int64     `json:"outputTokens,omitempty"`
	ReasoningTokens     *int64     `json:"reasoningTokens,omitempty"`
	CachedInputTokens   *int64     `json:"cachedInputTokens,omitempty"`
	ProviderRequestID   string     `json:"providerRequestId,omitempty"`
	CPAInternalAttempts *int       `json:"cpaInternalAttempts"`
}
type Result struct {
	CustomID    string          `json:"custom_id"`
	Response    json.RawMessage `json:"response,omitempty"`
	Error       *ItemError      `json:"error,omitempty"`
	Diagnostics Outcome         `json:"diagnostics"`
}
type StoredItem struct {
	Item       Item
	State      string
	Attempt    int
	CreatedAt  time.Time
	ReadyAt    time.Time
	WasWaiting bool
	Deadline   time.Time
	Outcome    Outcome
}

type Store interface {
	Create(context.Context, string, string, []Item, time.Duration) (JobRef, error)
	Status(context.Context, string, string) (Status, error)
	Load(context.Context, Ref) (StoredItem, error)
	Begin(context.Context, Ref) (StoredItem, error)
	Record(context.Context, Ref, Outcome) (Counts, error)
	Heartbeat(context.Context, Ref, bool) error
	Cooldown(context.Context, []JobRef, string, *time.Time) error
	Expire(context.Context, JobRef) error
}
type Starter interface {
	Start(context.Context, JobRef) error
}
type Artifacts interface {
	PutBatchResult(context.Context, string, string, string, []byte) error
	OpenBatchResult(context.Context, string, string, string) (io.ReadCloser, error)
}
type Executor func(context.Context, string, hardenllm.Request) (hardenllm.Result, error)
type Encoder func(Generation, hardenllm.Result, time.Time) (json.RawMessage, error)

type Service struct {
	Store      Store
	Starter    Starter
	Artifacts  Artifacts
	Upstream   string
	JobTimeout time.Duration
}

func (s *Service) Submit(ctx context.Context, scope, key string, items []Item) (Status, error) {
	if s == nil || s.Store == nil || s.Starter == nil || s.Artifacts == nil {
		return Status{}, ErrUnavailable
	}
	if !validScope(scope) || len(key) < 1 || len(key) > 128 || len(items) < 1 || len(items) > MaximumItems {
		return Status{}, errors.New("batch: invalid submission")
	}
	seen := map[string]bool{}
	for _, item := range items {
		if len(item.CustomID) < 1 || len(item.CustomID) > 128 || seen[item.CustomID] || item.Generation.ConnectionID != s.Upstream || item.Generation.ModelID != "gpt-6-luna" || item.Generation.ReasoningEffort != "low" {
			return Status{}, errors.New("batch: invalid item")
		}
		seen[item.CustomID] = true
	}
	d := s.JobTimeout
	if d == 0 {
		d = 2 * time.Hour
	}
	ref, err := s.Store.Create(ctx, scope, key, items, d)
	if err != nil {
		return Status{}, err
	}
	if err = s.Starter.Start(ctx, ref); err != nil {
		return Status{}, ErrUnavailable
	}
	return s.Status(ctx, scope, ref.JobID)
}
func (s *Service) Status(ctx context.Context, scope, id string) (Status, error) {
	if s == nil || s.Store == nil || !validScope(scope) {
		return Status{}, ErrUnavailable
	}
	v, err := s.Store.Status(ctx, scope, id)
	if err != nil {
		return v, err
	}
	v.ObservedAt = time.Now().UTC()
	v.StatusURL = "/v1/jobs/" + v.JobID
	v.ResultURL = v.StatusURL + "/result"
	return v, nil
}
func (s *Service) UpstreamID() string {
	if s == nil {
		return ""
	}
	return s.Upstream
}

// WriteResults streams one complete NDJSON record per item. Callers must verify
// the record count against status; an artifact-read failure terminates delivery.
func (s *Service) WriteResults(ctx context.Context, scope, id string, w io.Writer) error {
	status, err := s.Status(ctx, scope, id)
	if err != nil {
		return err
	}
	if !status.Counts.Done() {
		return errors.New("batch: result is not ready")
	}
	enc := json.NewEncoder(w)
	for i := 0; i < status.Counts.Total; i++ {
		v, err := s.Store.Load(ctx, Ref{Scope: scope, JobID: id, Index: i})
		if err != nil {
			return err
		}
		r := Result{CustomID: v.Item.CustomID, Error: v.Outcome.Error, Diagnostics: v.Outcome}
		if v.State == "succeeded" {
			if s.Artifacts == nil {
				return ErrUnavailable
			}
			reader, err := s.Artifacts.OpenBatchResult(ctx, scope, id, v.Outcome.ArtifactID)
			if err != nil {
				return err
			}
			raw, readErr := io.ReadAll(io.LimitReader(reader, (16<<20)+1))
			closeErr := reader.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			if len(raw) > 16<<20 || !json.Valid(raw) {
				return errors.New("batch: invalid result artifact")
			}
			r.Response = raw
		}
		if err = enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}
func validScope(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
