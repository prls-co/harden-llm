package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-542 / TEST-543.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful/batch"
)

func diagnosticLogRecords(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	for _, secret := range []string{"private prompt", "private partial", "synthetic-provider-key", "synthetic-private-prefix", "driver-secret-sentinel"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("diagnostic log retained unsafe text: %s", secret)
		}
	}
	return records
}

func TestCPAAdmissionTransportDiagnostics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ mode, class, phase string }{
		{"no_headers", "timeout", "response_headers"},
		{"partial", "unexpected_eof", "response_stream"},
		{"canceled", "canceled", "response_stream"},
		{"deadline", "deadline", "response_stream"},
		{"completed", "", ""},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			service, store, calls, _ := referenceUncertaintyService(t, nil, tc.mode)
			var logs bytes.Buffer
			service.config.Admission.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			input := uncertaintyRunInput()
			if tc.mode != "deadline" {
				input.TimeoutMS = 5000
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if tc.mode == "canceled" {
				progress := make(chan hardenllm.ProgressEvent, 64)
				input.Progress = progress
				go func() {
					for {
						select {
						case event := <-progress:
							if event.OutputBytes > 0 {
								cancel()
								return
							}
						case <-ctx.Done():
							return
						}
					}
				}()
			}
			out, _, err := service.Run(ctx, strings.Repeat("a", 64), input)
			if calls.Load() != 1 || len(store.queued) != 1 || len(store.observations) != 1 || len(out.Attempts) != 1 {
				t.Fatalf("dispatch ownership changed: calls=%d queued=%d observations=%d attempts=%d err=%v", calls.Load(), len(store.queued), len(store.observations), len(out.Attempts), err)
			}
			observation := store.observations[0]
			if !observation.LocalStopped || !observation.NativeEntered {
				t.Fatal("positive local-stop evidence lost")
			}
			if tc.mode != "completed" && (err == nil || observation.Outcome.Kind != "unconfirmed" || out.Output != nil || out.Attempts[0].Retryable || observation.Outcome.InputTokens != nil || out.Cache.Written) {
				t.Fatal("transport diagnostics changed uncertainty, output, usage or replay", err)
			}
			if tc.mode == "completed" && (err != nil || out.Output != "OK" || observation.Outcome.Kind != "completed") {
				t.Fatal("success changed", err)
			}
			records := diagnosticLogRecords(t, logs.Bytes())
			if len(records) != 1 || records[0]["msg"] != "ordinary CPA dispatch observed" {
				t.Fatal("diagnostics must extend one existing event", records)
			}
			r := records[0]
			if tc.class == "" {
				if _, exists := r["error_class"]; exists {
					t.Fatal("success fabricated a transport failure")
				}
			} else if r["error_class"] != tc.class || r["error_phase"] != tc.phase || r["error_code"] == nil {
				t.Fatalf("safe transport facts missing: want=%s/%s record=%v", tc.class, tc.phase, r)
			}
			if r["generation_budget_ms"] != float64(observation.Outcome.GenerationBudgetMS) || r["generation_deadline"] != observation.Outcome.GenerationDeadline.Format(time.RFC3339Nano) || observation.Outcome.GenerationBudgetMS > int64(input.TimeoutMS) {
				t.Fatal("event reset or lost the accepted deadline", r)
			}
			if tc.mode == "no_headers" {
				if r["headers_ms"] != nil || r["first_output_ms"] != nil || r["stream_correlation_id"] != nil || out.Attempts[0].Stream != nil {
					t.Fatal("unobserved provider facts fabricated", r)
				}
			} else if r["stream_correlation_id"] != "0123abcd" {
				t.Fatal("safe stream correlation missing", r)
			}
			if tc.mode == "partial" && (r["terminal_state"] != "missing" || r["output_bytes"] != float64(len("private partial")) || r["last_event_at"] == nil || r["last_output_at"] == nil) {
				t.Fatal("partial-output evidence lost", r)
			}
			wire, marshalErr := json.Marshal(out)
			if marshalErr != nil || bytes.Contains(wire, []byte("errorClass")) || bytes.Contains(wire, []byte("errorPhase")) || bytes.Contains(wire, []byte("transportError")) {
				t.Fatal("log-only diagnostics changed the strict wire contract", marshalErr)
			}
		})
	}
}

func TestCPAAdmissionWaiterReadLogsSafeClass(t *testing.T) {
	t.Parallel()
	for _, failures := range []int{1, 99} {
		t.Run(strconv.Itoa(failures), func(t *testing.T) {
			service, store, calls, _ := referenceUncertaintyService(t, nil, "completed")
			store.readFailures = failures
			store.readFailureErr = errors.Join(batch.ErrUnavailable, &pgconn.PgError{Code: "57P01", Message: "driver-secret-sentinel", Detail: "private prompt"})
			var logs bytes.Buffer
			service.config.Admission.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			input := uncertaintyRunInput()
			input.TimeoutMS = 5000
			out, _, err := service.Run(t.Context(), strings.Repeat("a", 64), input)
			if len(store.queued) != 1 || store.reads != min(failures+1, 3) {
				t.Fatal("metadata classification changed recovery bounds or re-enqueued")
			}
			if failures == 1 && (err != nil || calls.Load() != 1 || out.Output != "OK") || failures == 99 && (err == nil || calls.Load() != 0 || store.claims != 0) {
				t.Fatal("metadata classification changed provider entry", err)
			}
			seen := 0
			for _, r := range diagnosticLogRecords(t, logs.Bytes()) {
				if r["metadata_stage"] != "waiter_read" {
					continue
				}
				seen++
				if r["metadata_error_class"] != "postgres_57P01" || r["native_entered"] != false {
					t.Fatal("safe SQL cause lost at recovery/failure event", r)
				}
			}
			if seen != min(failures, 3) {
				t.Fatal("existing read events missing", seen)
			}
		})
	}
}
