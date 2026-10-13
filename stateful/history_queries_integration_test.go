//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-427

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func storedHistoryFixture(t *testing.T, s *Service, hash, id string, started time.Time) HistoryRecord {
	t.Helper()
	r := setHistoryIdentity(t, historyFixtureRecord(t), id, started)
	o := s.insertHistory(context.Background(), hash, r, historyWriteOptions{})
	if o.Status != "saved" {
		t.Fatal("history setup did not persist", o)
	}
	return r
}
func TestHistoryQueries(t *testing.T) {
	t.Parallel()
	runID := "00000000-0000-4000-8000-000000000001"
	now := time.Date(2026, 10, 10, 12, 5, 0, 0, time.UTC)
	t.Run("statistics use the same key window with truthful known and unknown accounting", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		s.config.History.Enabled = true
		s.now = func() time.Time { return now }
		base := historyFixtureRecord(t)
		for i := 1; i <= 3; i++ {
			r := setHistoryIdentity(t, base, fmt.Sprintf("00000000-0000-4000-8000-%012d", i), now.Add(-time.Minute))
			var result map[string]any
			if err := json.Unmarshal(r.Result, &result); err != nil {
				t.Fatal(err)
			}
			accounting := result["accounting"].(map[string]any)
			provider := accounting["provider"].(map[string]any)
			cost := provider["cost"].(map[string]any)
			if i == 1 {
				cost["knownSubtotalUsd"] = 0.25
				cost["knownObservations"] = 1
				cost["unknownObservations"] = 0
				cost["status"] = "exact"
				cost["source"] = "reported"
				r.Summary.KnownCostUSD = 0.25
				r.Summary.KnownCostObservations = 1
				r.Summary.UnknownCostObservations = 0
			}
			if i == 2 {
				r.Summary.CacheServed = true
				cache := result["cache"].(map[string]any)
				cache["served"] = true
			}
			r.Result, _ = json.Marshal(result)
			if o := s.insertHistory(context.Background(), keyHashA, r, historyWriteOptions{}); o.Status != "saved" {
				t.Fatal(o)
			}
		}
		storedHistoryFixture(t, s, keyHashB, "00000000-0000-4000-8000-000000000098", now.Add(-time.Minute))
		from := now.Add(-2 * time.Minute)
		query := HistoryQuery{From: &from, Before: now}
		stats, err := s.Statistics(context.Background(), keyHashA, query)
		if err != nil {
			t.Fatal(err)
		}
		page, err := s.ListHistory(context.Background(), keyHashA, query)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Scope != "available_records" || stats.TotalCount != 3 || stats.Succeeded != 3 || stats.CacheServed != 1 || stats.TotalCount != page.Pagination.TotalCount || stats.Accounting.Provider.Cost.KnownSubtotalUSD != 0.25 || stats.Accounting.Provider.Cost.UnknownObservations != 2 || stats.Accounting.Provider.Cost.Status != "partial" {
			t.Fatal("statistics disguised incomplete cost or crossed scope", stats)
		}
		after := now.Add(-30 * time.Second)
		empty, err := s.Statistics(context.Background(), keyHashA, HistoryQuery{From: &after, Before: now})
		if err != nil || empty.TotalCount != 0 {
			t.Fatal("stats/list window diverged", err)
		}
	})
	t.Run("delete removes exact run prefix before row and denies foreign key", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, store, _, capture := artifactFixture(t)
		s.config.Pool = p
		s.config.History.Enabled = true
		storedHistoryFixture(t, s, keyHashA, runID, now.Add(-time.Minute))
		if refs, d := s.publishArtifacts(context.Background(), keyHashA, runID, capture); d != nil || len(refs) != 3 {
			t.Fatal(d)
		}
		requireCode(t, s.DeleteHistory(context.Background(), keyHashB, runID), "history_not_found")
		reader, err := s.OpenArtifact(context.Background(), keyHashA, runID, "trace.json")
		if err != nil {
			t.Fatal("foreign delete removed bytes", err)
		}
		_ = reader.Body.Close()
		checkedOrder := false
		s.beforeArtifactDelete = func(string) error {
			checkedOrder = true
			if countRows(t, p, "history", keyHashA) != 1 {
				t.Error("history row removed before objects")
			}
			return nil
		}
		if err := s.DeleteHistory(context.Background(), keyHashA, runID); err != nil {
			t.Fatal(err)
		}
		if !checkedOrder {
			t.Fatal("object cleanup not used")
		}
		_, err = s.GetHistory(context.Background(), keyHashA, runID)
		requireCode(t, err, "history_not_found")
		_, err = s.OpenArtifact(context.Background(), keyHashA, runID, "trace.json")
		requireCode(t, err, "artifact_not_found")
		if err := store.deleteRunPrefix(context.Background(), keyHashA, runID, artifactDeleteOptions{}); err != nil {
			t.Fatal("missing objects do not count as removed", err)
		}
	})
	t.Run("partial object cleanup preserves row and explicit retry completes", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _, _, capture := artifactFixture(t)
		s.config.Pool = p
		s.config.History.Enabled = true
		storedHistoryFixture(t, s, keyHashA, runID, now.Add(-time.Minute))
		if _, d := s.publishArtifacts(context.Background(), keyHashA, runID, capture); d != nil {
			t.Fatal(d)
		}
		calls := 0
		s.beforeArtifactDelete = func(string) error {
			calls++
			if calls == 2 {
				return errors.New("fixture object failure")
			}
			return nil
		}
		err := s.DeleteHistory(context.Background(), keyHashA, runID)
		requireCode(t, err, "artifact_cleanup_failed")
		if !strings.Contains(err.Error(), runID) || countRows(t, p, "history", keyHashA) != 1 {
			t.Fatal("failed cleanup removed row or lost run correlation", err)
		}
		s.beforeArtifactDelete = nil
		if err := s.DeleteHistory(context.Background(), keyHashA, runID); err != nil {
			t.Fatal(err)
		}
		if countRows(t, p, "history", keyHashA) != 0 {
			t.Fatal("retry did not complete row removal")
		}
	})
	t.Run("clear freezes cutoff and limits batches to one hundred", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		s.config.History.Enabled = true
		s.now = func() time.Time { return now }
		for i := 1; i <= 150; i++ {
			storedHistoryFixture(t, s, keyHashA, fmt.Sprintf("00000000-0000-4000-8000-%012d", i), now.Add(-time.Minute))
		}
		storedHistoryFixture(t, s, keyHashB, "00000000-0000-4000-8000-000000000998", now.Add(-time.Minute))
		storedHistoryFixture(t, s, keyHashA, "00000000-0000-4000-8000-000000000999", now.Add(time.Second))
		batches := []int{}
		s.onClearBatch = func(n int) { batches = append(batches, n) }
		result, err := s.ClearHistory(context.Background(), keyHashA, HistoryQuery{})
		if err != nil || result.Deleted != 150 || !result.Complete || !result.Cutoff.Equal(now) || len(batches) != 2 || batches[0] != 100 || batches[1] != 50 || countRows(t, p, "history", keyHashA) != 1 || countRows(t, p, "history", keyHashB) != 1 {
			t.Fatal("clear batch/cutoff/scope wrong", result, err, batches)
		}
	})
	t.Run("clear stops on first failed run and leaves later rows", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _, _, capture := artifactFixture(t)
		s.config.Pool = p
		s.config.History.Enabled = true
		s.now = func() time.Time { return now }
		for i := 1; i <= 3; i++ {
			id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
			storedHistoryFixture(t, s, keyHashA, id, now.Add(-time.Minute))
			if _, d := s.publishArtifacts(context.Background(), keyHashA, id, capture); d != nil {
				t.Fatal(d)
			}
		}
		s.beforeArtifactDelete = func(key string) error {
			if strings.Contains(key, "00000000-0000-4000-8000-000000000002") {
				return errors.New("fixture stop")
			}
			return nil
		}
		result, err := s.ClearHistory(context.Background(), keyHashA, HistoryQuery{})
		requireCode(t, err, "artifact_cleanup_failed")
		if result.Complete || result.Deleted != 1 || result.FailedRunID != "00000000-0000-4000-8000-000000000002" || countRows(t, p, "history", keyHashA) != 2 {
			t.Fatal("clear continued after first failure", result)
		}
	})
	t.Run("disabled artifacts allow row-only delete and clear", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		s.config.History.Enabled = true
		s.now = func() time.Time { return now }
		storedHistoryFixture(t, s, keyHashA, runID, now.Add(-time.Minute))
		if err := s.DeleteHistory(context.Background(), keyHashA, runID); err != nil {
			t.Fatal(err)
		}
		storedHistoryFixture(t, s, keyHashA, runID, now.Add(-time.Minute))
		result, err := s.ClearHistory(context.Background(), keyHashA, HistoryQuery{})
		if err != nil || !result.Complete || result.Deleted != 1 {
			t.Fatal("disabled-artifact row clear failed", err)
		}
	})
}
