package stateful

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/accounting"
	"time"
)

type Statistics struct {
	Scope       string               `json:"scope"`
	Window      HistoryWindow        `json:"window"`
	TotalCount  int64                `json:"totalCount"`
	Succeeded   int64                `json:"succeeded"`
	Failed      int64                `json:"failed"`
	Timeout     int64                `json:"timeout"`
	CacheServed int64                `json:"cacheServed"`
	Accounting  hardenllm.Accounting `json:"accounting"`
}
type ClearResult struct {
	Deleted     int64     `json:"deleted"`
	Cutoff      time.Time `json:"cutoff"`
	Complete    bool      `json:"complete"`
	FailedRunID string    `json:"failedRunId,omitempty"`
	Error       *RunError `json:"error,omitempty"`
}

func (s *Service) Statistics(ctx context.Context, hash string, q HistoryQuery) (Statistics, error) {
	if err := s.historyReadReady(hash); err != nil {
		return Statistics{}, err
	}
	q, err := s.normalizeHistoryQuery(q)
	if err != nil {
		return Statistics{}, err
	}
	stats := Statistics{Scope: "available_records", Window: HistoryWindow{From: q.From, Before: q.Before}}
	resultLedger, providerLedger := accounting.EmptyLedger(), accounting.EmptyLedger()
	filter, args := historyFilter(hash, q)
	err = withStorageTx(ctx, s.config.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT summary FROM history WHERE "+filter, args...)
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
			stats.TotalCount++
			switch summary.Status {
			case "succeeded":
				stats.Succeeded++
			case "failed":
				stats.Failed++
			case "timeout":
				stats.Timeout++
			default:
				return historyRecordInvalid()
			}
			if summary.CacheServed {
				stats.CacheServed++
			}
			resultLedger, err = accounting.AddLedger(resultLedger, summary.Accounting.Result)
			if err != nil {
				return historyRecordInvalid()
			}
			providerLedger, err = accounting.AddLedger(providerLedger, summary.Accounting.Provider)
			if err != nil {
				return historyRecordInvalid()
			}
		}
		return rows.Err()
	})
	if err != nil {
		var op *OperationError
		if errors.As(err, &op) {
			return Statistics{}, err
		}
		return Statistics{}, &OperationError{Code: "history_read_failed", Message: "History statistics are unavailable; inspect storage diagnostics.", cause: err}
	}
	stats.Accounting = hardenllm.Accounting{Result: hardenllm.ProjectLedger(resultLedger), Provider: hardenllm.ProjectLedger(providerLedger)}
	return stats, nil
}

func (s *Service) DeleteHistory(ctx context.Context, hash, id string) error {
	if _, err := s.GetHistory(ctx, hash, id); err != nil {
		return err
	}
	return s.deleteHistoryRun(ctx, hash, id)
}
func (s *Service) deleteHistoryRun(ctx context.Context, hash, id string) error {
	if s.config.Artifacts != nil {
		if err := s.config.Artifacts.deleteRunPrefix(ctx, hash, id, artifactDeleteOptions{beforeDelete: s.beforeArtifactDelete}); err != nil {
			return err
		}
	}
	err := withStorageTx(ctx, s.config.Pool, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "DELETE FROM history WHERE api_key_hash=$1 AND run_id=$2", hash, id)
		return err
	})
	return storageFailure(err)
}
func (s *Service) ClearHistory(ctx context.Context, hash string, q HistoryQuery) (ClearResult, error) {
	if err := s.historyReadReady(hash); err != nil {
		return ClearResult{}, err
	}
	q, err := s.normalizeHistoryQuery(q)
	if err != nil {
		return ClearResult{}, err
	}
	result := ClearResult{Cutoff: q.Before, Complete: false}
	filter, args := historyFilter(hash, q)
	for {
		var ids []string
		err = withStorageTx(ctx, s.config.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, "SELECT run_id FROM history WHERE "+filter+" ORDER BY started_at,run_id LIMIT 100", args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					return err
				}
				ids = append(ids, id)
			}
			return rows.Err()
		})
		if err != nil {
			return result, &OperationError{Code: "history_read_failed", Message: "History clear could not select its next batch; inspect storage diagnostics.", cause: err}
		}
		if len(ids) == 0 {
			result.Complete = true
			return result, nil
		}
		if s.onClearBatch != nil {
			s.onClearBatch(len(ids))
		}
		for _, id := range ids {
			if err = s.deleteHistoryRun(ctx, hash, id); err != nil {
				result.FailedRunID = id
				result.Error = ProjectRunError(err)
				return result, err
			}
			result.Deleted++
		}
	}
}
