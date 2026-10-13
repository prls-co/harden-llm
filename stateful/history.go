package stateful

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prls-co/harden-llm/api"
	"github.com/prls-co/harden-llm/internal/accounting"
	"math"
	"time"
)

const historyInsertTimeout = 2 * time.Second
const historyReadbackTimeout = time.Second
const historyDetailTimeout = 2 * time.Second
const maxHistoryBytes = 17 << 20

type Diagnostic struct {
	Code               string `json:"code"`
	Component          string `json:"component"`
	Operation          string `json:"operation"`
	RequestID          string `json:"requestId"`
	RunID              string `json:"runId,omitempty"`
	TraceID            string `json:"traceId,omitempty"`
	InferenceOutcome   string `json:"inferenceOutcome"`
	PersistenceOutcome string `json:"persistenceOutcome"`
	Cause              string `json:"cause"`
	DurationMS         int64  `json:"durationMs"`
	Message            string `json:"message"`
	Action             string `json:"action"`
}
type RecordingOutcome struct {
	Mode       string      `json:"mode"`
	Status     string      `json:"status"`
	RunID      string      `json:"runId"`
	Diagnostic *Diagnostic `json:"diagnostic,omitempty"`
}
type RunError struct {
	Code            string       `json:"code"`
	Message         string       `json:"message"`
	FieldErrors     []FieldError `json:"fieldErrors,omitempty"`
	CurrentRevision *int64       `json:"currentRevision,omitempty"`
	Diagnostic      *Diagnostic  `json:"diagnostic,omitempty"`
}
type HistorySummary struct {
	RunID                   string    `json:"runId"`
	ProfileID               string    `json:"profileId"`
	ModelID                 string    `json:"modelId"`
	Status                  string    `json:"status"`
	StartedAt               time.Time `json:"startedAt"`
	FinishedAt              time.Time `json:"finishedAt"`
	CacheServed             bool      `json:"cacheServed"`
	ResultSource            string    `json:"resultSource"`
	ProviderTokens          int64     `json:"providerTokens"`
	ResultTokens            int64     `json:"resultTokens"`
	KnownCostUSD            float64   `json:"knownCostUsd"`
	KnownCostObservations   int64     `json:"knownCostObservations"`
	UnknownCostObservations int64     `json:"unknownCostObservations"`
	DurationMS              int64     `json:"durationMs"`
}
type HistoryRecord struct {
	SchemaVersion int             `json:"schemaVersion"`
	RunID         string          `json:"runId"`
	StartedAt     time.Time       `json:"startedAt"`
	FinishedAt    time.Time       `json:"finishedAt"`
	Status        string          `json:"status"`
	Summary       HistorySummary  `json:"summary"`
	Request       json.RawMessage `json:"request"`
	Result        json.RawMessage `json:"result"`
	Error         *RunError       `json:"error"`
}
type HistoryQuery struct {
	From     *time.Time
	Before   time.Time
	Page     int
	PageSize int
}
type HistoryWindow struct {
	From   *time.Time `json:"from,omitempty"`
	Before time.Time  `json:"before"`
}
type HistoryPagination struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"pageSize"`
	TotalCount int64 `json:"totalCount"`
}
type HistoryPage struct {
	Items      []HistorySummary  `json:"items"`
	Pagination HistoryPagination `json:"pagination"`
	Window     HistoryWindow     `json:"window"`
	History    FeatureStatus     `json:"history"`
}
type HistoryUsage struct {
	Rows       int64 `json:"rows"`
	TableBytes int64 `json:"tableBytes"`
}

// Fault hooks are per call, test owned, and retain real SQL transactions.
type historyWriteOptions struct {
	beforeRecord  func(*HistoryRecord)
	commitOwnsTx  bool
	commit        func(context.Context, pgx.Tx) (string, error)
	onInsert      func()
	onReadback    func()
	readbackError error
}

type FeatureStatus struct {
	Mode       string      `json:"mode"`
	Status     string      `json:"status"`
	Diagnostic *Diagnostic `json:"diagnostic,omitempty"`
}
type storedHistorySummary struct {
	Accounting accounting.Accounting `json:"accounting"`
	HistorySummary
	Error *RunError `json:"error,omitempty"`
}

func historyStorageRecord(record HistoryRecord) HistoryRecord {
	record.StartedAt = record.StartedAt.UTC().Truncate(time.Microsecond)
	record.FinishedAt = record.FinishedAt.UTC().Truncate(time.Microsecond)
	record.Summary.StartedAt = record.StartedAt
	record.Summary.FinishedAt = record.FinishedAt
	return record
}

func recordingStatus(commit string, found bool, readErr error) string {
	switch commit {
	case "confirmed":
		return "saved"
	case "rejected":
		return "not_saved"
	default:
		if found && readErr == nil {
			return "saved"
		}
		return "unconfirmed"
	}
}
func commitClassification(err error) string {
	if err == nil {
		return "confirmed"
	}
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrTxCommitRollback) || errors.As(err, &pgErr) || pgconn.SafeToRetry(err) {
		return "rejected"
	}
	return "unknown"
}
func validateHistoryRecord(r HistoryRecord) error {
	_, err := validatedHistoryAccounting(r)
	return err
}

func validatedHistoryAccounting(r HistoryRecord) (accounting.Accounting, error) {
	invalid := func() (accounting.Accounting, error) { return accounting.Accounting{}, historyRecordInvalid() }
	if r.SchemaVersion != 1 || r.StartedAt.IsZero() || r.FinishedAt.Before(r.StartedAt) || (r.Status != "succeeded" && r.Status != "failed" && r.Status != "timeout") {
		return invalid()
	}
	if _, err := uuid.Parse(r.RunID); err != nil {
		return invalid()
	}
	if r.Summary.RunID != r.RunID || r.Summary.Status != r.Status || !r.Summary.StartedAt.Equal(r.StartedAt) || !r.Summary.FinishedAt.Equal(r.FinishedAt) {
		return invalid()
	}
	if len(r.Request)+len(r.Result) > maxHistoryBytes {
		return accounting.Accounting{}, &OperationError{Code: "history_write_rejected", Message: "History request and result exceed the 17 MiB bound; output was not truncated."}
	}
	if api.Validate("HistoryRequest", r.Request) != nil {
		return invalid()
	}
	value, err := api.Decode("RecordedRunOutput", r.Result)
	if err != nil {
		return invalid()
	}
	result, ok := value.(map[string]any)
	if !ok || result["runId"] != r.RunID || result["status"] != r.Status {
		return invalid()
	}
	data, err := json.Marshal(result["accounting"])
	if err != nil {
		return invalid()
	}
	var ledger accounting.Accounting
	if json.Unmarshal(data, &ledger) != nil {
		return invalid()
	}
	return ledger, nil
}
func historyRecordInvalid() error {
	return &OperationError{Code: "history_record_invalid", Message: "Stored history schema or complete payload is invalid."}
}
func safeCause(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || pgconn.Timeout(err) {
		return "timeout"
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return "rejected"
	}
	var op *OperationError
	if errors.As(err, &op) {
		switch op.Code {
		case "reference_configuration_error", "history_configuration_error":
			return "configuration"
		case "history_record_invalid":
			return "schema"
		case "history_write_rejected":
			return "rejected"
		}
	}
	return "connection"
}
func historyDiagnostic(r HistoryRecord, status string, err error, started time.Time) *Diagnostic {
	code, message, action := "history_write_rejected", "History was not saved; the inference output is preserved.", "Check reference storage status and correlated service diagnostics. Do not replay inference."
	if status == "unconfirmed" {
		code = "history_write_unconfirmed"
		message = "History recording is unconfirmed; the row may appear later."
		action = "Read history again. Do not replay inference."
	}
	if status == "configuration_error" {
		code = "history_configuration_error"
		message = "History is enabled but PostgreSQL is not configured."
		action = "Configure HARDEN_LLM_DATABASE_URL and check reference status."
	}
	var value struct {
		TraceID string `json:"traceId"`
	}
	_ = json.Unmarshal(r.Result, &value)
	return &Diagnostic{Code: code, Component: "history", Operation: "insert", RequestID: r.RunID, RunID: r.RunID, TraceID: value.TraceID, InferenceOutcome: r.Status, PersistenceOutcome: status, Cause: safeCause(err), DurationMS: time.Since(started).Milliseconds(), Message: message, Action: action}
}
func (s *Service) insertHistory(ctx context.Context, hash string, r HistoryRecord, options historyWriteOptions) RecordingOutcome {
	started := time.Now()
	outcome := RecordingOutcome{Mode: "enabled", Status: "not_saved", RunID: r.RunID}
	reject := func(err error) RecordingOutcome {
		outcome.Diagnostic = historyDiagnostic(r, outcome.Status, err, started)
		return outcome
	}
	if err := checkScope(hash); err != nil {
		return reject(err)
	}
	if s.config.Pool == nil {
		outcome.Status = "configuration_error"
		return reject(&OperationError{Code: "history_configuration_error", Message: "Reference PostgreSQL is not configured."})
	}
	ledger, err := validatedHistoryAccounting(r)
	if err != nil {
		return reject(err)
	}
	// PostgreSQL timestamptz stores microseconds. Canonicalize the JSON summary
	// to that same precision so a real clock cannot invalidate its own readback.
	r = historyStorageRecord(r)
	summary, err := json.Marshal(storedHistorySummary{HistorySummary: r.Summary, Error: r.Error, Accounting: ledger})
	if err != nil {
		return reject(historyRecordInvalid())
	}
	// Drop caller cancellation, retain correlation, and make only one insert.
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), historyInsertTimeout)
	defer cancel()
	tx, err := s.config.Pool.BeginTx(bounded, pgx.TxOptions{})
	if err != nil {
		return reject(err)
	}
	if !options.commitOwnsTx {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), storageStatementTimeout)
			defer cancel()
			_ = tx.Rollback(cleanup)
		}()
	}
	if _, err = tx.Exec(bounded, "SET LOCAL statement_timeout = '2000ms'"); err != nil {
		return reject(err)
	}
	if options.onInsert != nil {
		options.onInsert()
	}
	_, err = tx.Exec(bounded, `INSERT INTO history(api_key_hash,run_id,schema_version,status,started_at,finished_at,summary,request,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, hash, r.RunID, r.SchemaVersion, r.Status, r.StartedAt, r.FinishedAt, summary, r.Request, r.Result)
	if err != nil {
		return reject(err)
	}
	var classification string
	if options.commit != nil {
		classification, err = options.commit(bounded, tx)
	} else {
		err = tx.Commit(bounded)
		classification = commitClassification(err)
	}
	if classification == "unknown" {
		if options.onReadback != nil {
			options.onReadback()
		}
		readCtx, readCancel := context.WithTimeout(context.WithoutCancel(ctx), historyReadbackTimeout)
		defer readCancel()
		found := false
		readErr := options.readbackError
		if readErr == nil {
			readErr = s.config.Pool.QueryRow(readCtx, "SELECT EXISTS(SELECT 1 FROM history WHERE api_key_hash=$1 AND run_id=$2)", hash, r.RunID).Scan(&found)
		}
		outcome.Status = recordingStatus(classification, found, readErr)
		if outcome.Status != "saved" {
			if readErr != nil {
				err = readErr
			}
			return reject(err)
		}
	} else {
		outcome.Status = recordingStatus(classification, false, nil)
	}
	if outcome.Status != "saved" {
		return reject(err)
	}
	return outcome
}
func (s *Service) historyReadReady(hash string) error {
	if err := checkScope(hash); err != nil {
		return err
	}
	if !s.config.History.Enabled {
		return &OperationError{Code: "history_configuration_error", Message: "Reference history is disabled."}
	}
	if s.config.Pool == nil {
		return &OperationError{Code: "history_configuration_error", Message: "Reference history PostgreSQL is not configured."}
	}
	return nil
}
func (s *Service) GetHistory(ctx context.Context, hash, id string) (HistoryRecord, error) {
	if err := s.historyReadReady(hash); err != nil {
		return HistoryRecord{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return HistoryRecord{}, &OperationError{Code: "history_not_found", Message: "History entry was not found for this access key."}
	}
	bounded, cancel := context.WithTimeout(ctx, historyDetailTimeout)
	defer cancel()
	var r HistoryRecord
	var data, request, result []byte
	err := s.config.Pool.QueryRow(bounded, `SELECT schema_version,run_id,status,started_at,finished_at,summary,request,result FROM history WHERE api_key_hash=$1 AND run_id=$2`, hash, id).Scan(&r.SchemaVersion, &r.RunID, &r.Status, &r.StartedAt, &r.FinishedAt, &data, &request, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return HistoryRecord{}, &OperationError{Code: "history_not_found", Message: "History entry was not found for this access key."}
	}
	if err != nil {
		return HistoryRecord{}, &OperationError{Code: "history_read_failed", Message: "History detail could not be read; inspect storage diagnostics.", cause: err}
	}
	r.Request, r.Result = request, result
	r.StartedAt = r.StartedAt.UTC()
	r.FinishedAt = r.FinishedAt.UTC()
	var summary storedHistorySummary
	if decodeStrict(data, &summary) != nil {
		return HistoryRecord{}, historyRecordInvalid()
	}
	r.Summary = summary.HistorySummary
	r.Error = summary.Error
	if err = validateHistoryRecord(r); err != nil {
		return HistoryRecord{}, historyRecordInvalid()
	}
	return r, nil
}
func (s *Service) normalizeHistoryQuery(q HistoryQuery) (HistoryQuery, error) {
	if q.Page == 0 {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 25
	}
	if q.Before.IsZero() {
		q.Before = s.now().UTC()
	}
	q.Before = q.Before.UTC()
	if q.Page < 1 || (q.PageSize != 10 && q.PageSize != 25 && q.PageSize != 50 && q.PageSize != 100) || (q.Page-1) > math.MaxInt/q.PageSize {
		return q, &OperationError{Code: "history_query_invalid", Message: "Use a positive page and a page size of 10, 25, 50 or 100 without an overflowing offset."}
	}
	if q.From != nil {
		from := q.From.UTC()
		q.From = &from
		if !from.Before(q.Before) {
			return q, &OperationError{Code: "history_query_invalid", Message: "History window start must precede its cutoff."}
		}
	}
	return q, nil
}

// historyFilter owns the only key/window predicate used by list/count/stats/clear.
func historyFilter(hash string, q HistoryQuery) (string, []any) {
	return "api_key_hash=$1 AND started_at<$2 AND ($3::timestamptz IS NULL OR started_at>=$3)", []any{hash, q.Before, q.From}
}
func (s *Service) ListHistory(ctx context.Context, hash string, q HistoryQuery) (HistoryPage, error) {
	if err := s.historyReadReady(hash); err != nil {
		return HistoryPage{}, err
	}
	q, err := s.normalizeHistoryQuery(q)
	if err != nil {
		return HistoryPage{}, err
	}
	page := HistoryPage{Items: []HistorySummary{}, Pagination: HistoryPagination{Page: q.Page, PageSize: q.PageSize}, Window: HistoryWindow{From: q.From, Before: q.Before}, History: FeatureStatus{Mode: "enabled", Status: "configured"}}
	filter, args := historyFilter(hash, q)
	err = withStorageTx(ctx, s.config.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM history WHERE "+filter, args...).Scan(&page.Pagination.TotalCount); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT summary FROM history WHERE "+filter+" ORDER BY started_at DESC,run_id DESC LIMIT $4 OFFSET $5", append(args, q.PageSize, (q.Page-1)*q.PageSize)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var summary storedHistorySummary
			if decodeStrict(data, &summary) != nil {
				return historyRecordInvalid()
			}
			page.Items = append(page.Items, summary.HistorySummary)
		}
		return rows.Err()
	})
	if err != nil {
		var op *OperationError
		if errors.As(err, &op) {
			return HistoryPage{}, err
		}
		return HistoryPage{}, &OperationError{Code: "history_read_failed", Message: "History page could not be read; inspect storage diagnostics.", cause: err}
	}
	return page, nil
}
func (s *Service) historyUsage(ctx context.Context, hash string) (HistoryUsage, error) {
	if err := s.historyReadReady(hash); err != nil {
		return HistoryUsage{}, err
	}
	var usage HistoryUsage
	err := withStorageTx(ctx, s.config.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT (SELECT count(*) FROM history WHERE api_key_hash=$1),pg_total_relation_size('history')", hash).Scan(&usage.Rows, &usage.TableBytes)
	})
	if err != nil {
		return HistoryUsage{}, &OperationError{Code: "history_read_failed", Message: "History usage figures are unavailable; inspect storage diagnostics.", cause: err}
	}
	return usage, nil
}
