//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-426

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	hardenllm "github.com/prls-co/harden-llm"
)

const oneAttemptPolicy = `{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`

func referenceRunInput() RunInput {
	return RunInput{ProfileID: "CPA Responses", UserPrompt: "Reply with OK.", CallType: hardenllm.CallTypeText, RecoveryPolicy: json.RawMessage(oneAttemptPolicy)}
}

func TestReferenceHistory(t *testing.T) {
	t.Parallel()
	t.Run("healthy run records one complete scoped snapshot after terminal output", func(t *testing.T) {
		pool, _ := referencePool(t, true)
		var calls atomic.Int32
		s, _ := serviceFixture(t, pool, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			if pool.Stat().AcquiredConns() != 0 {
				t.Error("SQL transaction held across provider I/O")
			}
			calls.Add(1)
			writeFixtureOK(w)
		})
		s.config.History.Enabled = true
		input := referenceRunInput()
		inserts, reads, terminals := 0, 0, 0
		input.historyWrite = historyWriteOptions{onInsert: func() { inserts++ }, onReadback: func() { reads++ }}
		input.Completed = func(out RunOutput, state RunState, err error) {
			terminals++
			if err != nil || out.Output != "OK" || state.LastRunID != out.RunID || countRows(t, pool, "history", keyHashA) != 0 {
				t.Error("terminal output waits on storage or lost inference")
			}
		}
		out, state, err := s.Run(context.Background(), keyHashA, input)
		if err != nil || out.Output != "OK" || out.Status != "succeeded" || out.History.Status != "saved" || calls.Load() != 1 || inserts != 1 || reads != 0 || terminals != 1 || state.LastRunID != out.RunID || state.LastTraceID != out.TraceID {
			t.Fatal("healthy run did not preserve execution/recording", err, out.History, calls.Load(), inserts, reads, terminals)
		}
		record, err := s.GetHistory(context.Background(), keyHashA, out.RunID)
		if err != nil || countRows(t, pool, "history", keyHashA) != 1 {
			t.Fatal("complete record absent", err)
		}
		var saved struct {
			Input   RunInput
			Profile ProfileDraft
		}
		if json.Unmarshal(record.Request, &saved) != nil || saved.Input.UserPrompt != input.UserPrompt || !sameJSON(saved.Profile.Definition, s.config.Presets[input.ProfileID].Definition) || bytes.Contains(record.Result, []byte(`"history"`)) || bytes.Contains(record.Request, []byte("deployment-fixture-secret")) {
			t.Fatal("safe complete snapshot or recording exclusion broken")
		}
		if record.Summary.ProfileID != input.ProfileID || record.Summary.ModelID != out.SelectedTarget.ModelID || record.Summary.ResultSource != "provider" || !out.ProviderInvoked {
			t.Fatal("summary/target does not describe original core result")
		}
		_, err = s.GetHistory(context.Background(), keyHashB, out.RunID)
		requireCode(t, err, "history_not_found")
		err = s.DeleteHistory(context.Background(), keyHashB, out.RunID)
		requireCode(t, err, "history_not_found")
		page, err := s.ListHistory(context.Background(), keyHashB, HistoryQuery{})
		if err != nil || page.Pagination.TotalCount != 0 || countRows(t, pool, "history", keyHashA) != 1 {
			t.Fatal("foreign read/delete/count changed scope", err)
		}
	})
	t.Run("ordinary core call never writes history", func(t *testing.T) {
		pool, _ := referencePool(t, true)
		var calls atomic.Int32
		s, _ := serviceFixture(t, pool, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); writeFixtureOK(w) })
		s.config.History.Enabled = true
		client, err := s.ClientForKey(keyHashA)
		if err != nil {
			t.Fatal(err)
		}
		var policy hardenllm.RecoveryPolicy
		if err := json.Unmarshal([]byte(oneAttemptPolicy), &policy); err != nil {
			t.Fatal(err)
		}
		result, err := client.Call(context.Background(), hardenllm.Request{ModelID: "fixture-model", Messages: []hardenllm.Message{{Role: "user", Content: json.RawMessage(`"Reply with OK."`)}}, CallType: hardenllm.CallTypeText, CacheMode: hardenllm.CacheModeOff, RecoveryPolicy: policy})
		if err != nil || result.Output != "OK" || calls.Load() != 1 || countRows(t, pool, "history", keyHashA) != 0 {
			t.Fatal("ordinary execution entered reference history", err)
		}
	})
	for _, mode := range []string{"disabled", "nil-pool", "closed-pool", "oversize"} {
		t.Run("recording failure preserves inference/"+mode, func(t *testing.T) {
			pool, _ := referencePool(t, true)
			var calls atomic.Int32
			s, _ := serviceFixture(t, pool, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); writeFixtureOK(w) })
			s.config.History.Enabled = mode != "disabled"
			input := referenceRunInput()
			inserts, reads := 0, 0
			input.historyWrite = historyWriteOptions{onInsert: func() { inserts++ }, onReadback: func() { reads++ }}
			want := "not_saved"
			acquires := pool.Stat().AcquireCount()
			switch mode {
			case "disabled":
				want = "disabled"
			case "nil-pool":
				s.config.Pool = nil
				want = "configuration_error"
			case "closed-pool":
				pool.Close()
			case "oversize":
				// Inject an oversized encoded record at the production insert boundary;
				// keep the real inference result and real PostgreSQL implementation.
				input.historyWrite.beforeRecord = func(record *HistoryRecord) {
					var payload map[string]any
					if json.Unmarshal(record.Result, &payload) != nil {
						t.Fatal("record projection invalid")
					}
					payload["output"] = strings.Repeat("x", maxHistoryBytes)
					record.Result, _ = json.Marshal(payload)
				}
			}
			out, _, err := s.Run(context.Background(), keyHashA, input)
			if err != nil || out.Output != "OK" || out.History.Status != want || calls.Load() != 1 || inserts != 0 || reads != 0 {
				t.Fatal("recording error replayed or replaced inference", err, out.History, calls.Load(), inserts, reads)
			}
			if mode == "disabled" && (pool.Stat().AcquireCount() != acquires || countRows(t, pool, "history", keyHashA) != 0) {
				t.Fatal("disabled recording acquired PostgreSQL")
			}
			if mode == "closed-pool" && (out.History.Diagnostic == nil || out.History.Diagnostic.Cause != "connection") {
				t.Fatal("closed-pool failure not diagnosable", out.History)
			}
			if mode == "oversize" && countRows(t, pool, "history", keyHashA) != 0 {
				t.Fatal("oversize record truncated or inserted")
			}
		})
	}
	for _, outcome := range []string{"confirmed", "unknown-found", "unknown-absent", "unknown-read-failed", "rejected"} {
		t.Run("single insert/single readback/"+outcome, func(t *testing.T) {
			pool, _ := referencePool(t, true)
			var calls atomic.Int32
			s, _ := serviceFixture(t, pool, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); writeFixtureOK(w) })
			s.config.History.Enabled = true
			input := referenceRunInput()
			inserts, reads := 0, 0
			input.historyWrite = historyWriteOptions{onInsert: func() { inserts++ }, onReadback: func() { reads++ }}
			want, expectedReads := "saved", 0
			if outcome != "confirmed" {
				input.historyWrite.commit = func(ctx context.Context, tx pgx.Tx) (string, error) {
					var err error
					if outcome == "unknown-found" {
						err = tx.Commit(ctx)
					} else {
						err = tx.Rollback(ctx)
					}
					if err != nil {
						return "rejected", err
					}
					if outcome == "rejected" {
						return "rejected", errors.New("driver-secret-sentinel")
					}
					return "unknown", errors.New("driver-secret-sentinel")
				}
				want, expectedReads = "unconfirmed", 1
				if outcome == "unknown-found" {
					want = "saved"
				}
				if outcome == "rejected" {
					want, expectedReads = "not_saved", 0
				}
				if outcome == "unknown-read-failed" {
					input.historyWrite.readbackError = errors.New("driver-secret-sentinel")
				}
			}
			out, _, err := s.Run(context.Background(), keyHashA, input)
			if err != nil || out.Output != "OK" || out.History.Status != want || calls.Load() != 1 || inserts != 1 || reads != expectedReads {
				t.Fatal("unknown commit replayed work or changed classification", err, out.History, calls.Load(), inserts, reads)
			}
			encoded, _ := json.Marshal(out.History)
			if bytes.Contains(encoded, []byte("driver-secret-sentinel")) {
				t.Fatal("driver error leaked a secret")
			}
		})
	}
	t.Run("cache hit makes no provider call and records cache served", func(t *testing.T) {
		pool, _ := referencePool(t, true)
		var calls atomic.Int32
		s, _ := serviceFixture(t, pool, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); writeFixtureOK(w) })
		s.config.History.Enabled = true
		input := referenceRunInput()
		input.CacheMode = hardenllm.CacheModeCache
		first, _, err := s.Run(context.Background(), keyHashA, input)
		if err != nil || first.History.Status != "saved" || !first.Cache.Written || calls.Load() != 1 {
			t.Fatal("cache seed failed", err, first.History, first.Cache)
		}
		second, _, err := s.Run(context.Background(), keyHashA, input)
		if err != nil || second.History.Status != "saved" || !second.Cache.Served || second.ProviderInvoked || calls.Load() != 1 || countRows(t, pool, "history", keyHashA) != 2 {
			t.Fatal("cache hit replayed provider or lost history", err, second.History, second.Cache)
		}
		r, err := s.GetHistory(context.Background(), keyHashA, second.RunID)
		if err != nil || !r.Summary.CacheServed || r.Summary.ResultSource != "cache" {
			t.Fatal("cache summary fabricated", err)
		}
	})
	for _, invalid := range []string{"request", "profile", "credential", "recovery-profile"} {
		t.Run("pre-dispatch validation still records failure/"+invalid, func(t *testing.T) {
			pool, _ := referencePool(t, true)
			var calls atomic.Int32
			s, _ := serviceFixture(t, pool, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); writeFixtureOK(w) })
			s.config.History.Enabled = true
			input := referenceRunInput()
			switch invalid {
			case "request":
				input.UserPrompt = ""
			case "profile":
				input.ProfileID = "missing-profile"
			case "credential":
				preset := s.config.Presets[input.ProfileID]
				preset.Binding.DeploymentUpstreamID = "missing-credential"
				s.config.Presets[input.ProfileID] = preset
			case "recovery-profile":
				input.CallType = hardenllm.CallTypeStructured
				input.Schema = json.RawMessage(`{"type":"object"}`)
				input.RecoveryPolicy = json.RawMessage(`{"maxAttempts":2,"retryOn":[],"jsonRepair":{"initial":{"source":"profile","profileId":"missing-profile"},"escalation":null},"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`)
			}
			out, _, err := s.Run(context.Background(), keyHashA, input)
			if err == nil || out.Status != "failed" || out.History.Status != "saved" || calls.Load() != 0 || countRows(t, pool, "history", keyHashA) != 1 {
				t.Fatal("validation did not record failed execution without dispatch", err, out.History, calls.Load())
			}
			record, readErr := s.GetHistory(context.Background(), keyHashA, out.RunID)
			if readErr != nil || record.Error == nil || record.Error.Code == "" {
				t.Fatal("failed history lost typed application error", readErr)
			}
		})
	}
	t.Run("caller cancellation still inserts one detached terminal row", func(t *testing.T) {
		pool, _ := referencePool(t, true)
		started := make(chan struct{}, 1)
		var calls atomic.Int32
		s, _ := serviceFixture(t, pool, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			calls.Add(1)
			started <- struct{}{}
			<-r.Context().Done()
		})
		s.config.History.Enabled = true
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		type completion struct {
			output RunOutput
			err    error
		}
		done := make(chan completion, 1)
		go func() { out, _, err := s.Run(ctx, keyHashA, referenceRunInput()); done <- completion{out, err} }()
		select {
		case <-started:
			cancel()
		case <-time.After(5 * time.Second):
			t.Fatal("provider never dispatched")
		}
		select {
		case result := <-done:
			if result.err == nil || result.output.History.Status != "saved" || calls.Load() != 1 || countRows(t, pool, "history", keyHashA) != 1 {
				t.Fatal("canceled caller discarded or replayed terminal recording", result.err, result.output.History)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("detached recording did not terminate")
		}
	})
}
