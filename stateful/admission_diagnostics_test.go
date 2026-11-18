package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-542 / TEST-543 / TEST-549.

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/diagnostics"
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
			store.failureErr = errors.Join(batch.ErrUnavailable, &pgconn.PgError{Code: "57P01", Message: "driver-secret-sentinel", Detail: "private prompt"})
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

func TestCPAAdmissionEarlyFailureLogsSafeClass(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"pool_generation", "enqueue", "wake"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			service, store, calls, _ := referenceUncertaintyService(t, nil, "completed")
			store.fail = stage
			store.failureErr = errors.Join(batch.ErrUnavailable, &pgconn.PgError{Code: "57P01", Message: "driver-secret-sentinel", Detail: "private prompt"})
			want := "postgres_57P01"
			if stage == "wake" {
				want = "deadline"
				service.config.Admission.Signals = &admissionPoolSignals{next: admissionSignalStub{wake: func(context.Context, string) error {
					return context.DeadlineExceeded
				}}}
			}
			var logs bytes.Buffer
			service.config.Admission.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			deadline, _ := ctx.Deadline()
			out, _, err := service.Run(ctx, strings.Repeat("a", 64), uncertaintyRunInput())
			if err == nil || out.ProviderInvoked || calls.Load() != 0 || store.claims != 0 || store.reads != 0 || len(store.observations) != 0 {
				t.Fatal("pre-entry failure crossed the provider boundary", err)
			}
			if after, _ := ctx.Deadline(); after != deadline || ctx.Err() != nil {
				t.Fatal("metadata failure changed or canceled the caller deadline")
			}
			records := diagnosticLogRecords(t, logs.Bytes())
			if len(records) != 1 || records[0]["msg"] != "ordinary CPA admission not confirmed" || records[0]["metadata_stage"] != stage || records[0]["metadata_error_class"] != want || records[0]["native_entered"] != false {
				t.Fatalf("safe cause lost at %s: want=%s records=%v", stage, want, records)
			}
		})
	}
}

func TestReferenceRunCapturesSharedOperationDiagnosticsAndRedactedBundleLogs(t *testing.T) {
	t.Parallel()
	var s3Mu sync.Mutex
	s3Objects := map[string][]byte{}
	failPut := atomic.Bool{}
	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			if failPut.Load() && strings.HasSuffix(r.URL.Path, "/support-bundle.json") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			data, _ := io.ReadAll(r.Body)
			s3Mu.Lock()
			s3Objects[r.URL.Path] = data
			s3Mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s3Server.Close()

	var echoID atomic.Bool
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if echoID.Load() {
			w.Header().Set("X-Request-ID", "synthetic-private-prefix")
		} else {
			var payload struct{ Model string }
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
				return
			}
			w.Header().Set("X-Request-ID", "req-"+payload.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "chat/completions") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"gen-or-1","provider":"Groq","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg-1","stop_reason":"end_turn","content":[{"type":"text","text":"OK"}],"usage":{"input_tokens":5,"output_tokens":2}}`))
	}))
	defer provider.Close()

	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	artifacts, err := NewArtifactStore(ArtifactOptions{
		Endpoint: s3Server.URL, Bucket: "bucket", Region: "us-east-1",
		AccessKeyID: "garage-key", SecretAccessKey: "garage-secret", HTTPClient: s3Server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog := fixtureCatalog(t)
	orPreset := catalog["CPA Responses"]
	var def Profile
	_ = json.Unmarshal(orPreset.Definition, &def)
	def.Provider = "openrouter"
	def.APIInferenceType = "chat-completions"
	def.BaseURL = provider.URL + "/v1"
	def.DefaultOptions = map[string]any{"max_tokens": float64(128)}
	orPreset.Definition, _ = json.Marshal(def)
	orPreset.Binding = ProfileCredentialBinding{Source: "deployment", DeploymentUpstreamID: "openrouter"}

	var logBuf bytes.Buffer
	service, err := NewService(Config{
		Presets:   Catalog{"OpenRouter Test": orPreset},
		Artifacts: artifacts,
		Client: ClientOptions{
			Connections: []hardenllm.Connection{{
				ID: "openrouter", Provider: "openrouter", Protocol: "chat-completions",
				BaseURL: def.BaseURL, APIKey: "synthetic-provider-key",
				Headers: map[string]string{"X-Custom-Auth": "synthetic-private-prefix"},
			}},
			DefaultConnection: "openrouter",
			WebSearch:         WebSearchOptions{JinaAPIKey: "synthetic-jina-secret"},
			Logger:            slog.New(slog.NewJSONHandler(&logBuf, nil)),
			EndpointPolicy:    hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := RunInput{
		ProfileID: "OpenRouter Test", UserPrompt: "private prompt", CallType: hardenllm.CallTypeText,
		CacheMode: hardenllm.CacheModeOff, TimeoutMS: 5000,
		RecoveryPolicy: json.RawMessage(`{"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`),
	}
	scopeA := strings.Repeat("a", 64)
	scopeB := strings.Repeat("b", 64)

	input.ModelID = "scope-a"
	inputB := input
	inputB.ModelID = "scope-b"

	var wg sync.WaitGroup
	var outA, outB RunOutput
	var errA, errB error
	wg.Add(2)
	go func() {
		defer wg.Done()
		outA, _, errA = service.Run(t.Context(), scopeA, input)
	}()
	go func() {
		defer wg.Done()
		outB, _, errB = service.Run(t.Context(), scopeB, inputB)
	}()
	wg.Wait()
	if errA != nil || errB != nil || outA.Output != "OK" || outB.Output != "OK" {
		t.Fatalf("runs failed: errA=%v errB=%v", errA, errB)
	}
	if len(outA.OperationDiagnostics) != 1 || len(outB.OperationDiagnostics) != 1 || outA.DiagnosticsClipped || outB.DiagnosticsClipped {
		t.Fatalf("expected 1 isolated operation diagnostic per run: A=%v B=%v", outA.OperationDiagnostics, outB.OperationDiagnostics)
	}
	if outA.OperationDiagnostics[0]["upstream_provider"] != "Groq" || outA.OperationDiagnostics[0]["provider_request_id"] != "req-scope-a" || outB.OperationDiagnostics[0]["provider_request_id"] != "req-scope-b" || outA.OperationDiagnostics[0]["finish_reason"] != "stop" || outA.OperationDiagnostics[0]["final_text_present"] != true {
		t.Fatalf("non-CPA operation diagnostic missing response-shape facts: %#v", outA.OperationDiagnostics[0])
	}

	s3Mu.Lock()
	bundleBytes := s3Objects["/bucket/artifacts/"+scopeA+"/"+outA.RunID+"/support-bundle.json"]
	s3Mu.Unlock()
	if len(bundleBytes) == 0 {
		t.Fatal("support-bundle.json was not written")
	}
	var bundle diagnostics.Bundle
	if err := json.Unmarshal(bundleBytes, &bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Logs) != 1 || !strings.Contains(bundle.Logs[0], `"provider operation observed"`) || !strings.Contains(bundle.Logs[0], `"Groq"`) || !strings.Contains(bundle.Logs[0], `"req-scope-a"`) {
		t.Fatalf("support-bundle.json missing operation logs: %v", bundle.Logs)
	}
	for _, secret := range []string{"synthetic-provider-key", "synthetic-private-prefix", "synthetic-jina-secret", "private prompt"} {
		if bytes.Contains(bundleBytes, []byte(secret)) {
			t.Fatalf("support bundle leaked secret %q", secret)
		}
	}

	// A bounded upstream identifier can still equal a configured credential.
	echoID.Store(true)
	redacted, _, err := service.Run(t.Context(), scopeA, input)
	if err != nil || len(redacted.OperationDiagnostics) != 1 || redacted.OperationDiagnostics[0]["provider_request_id"] != "[REDACTED]" || bytes.Contains(logBuf.Bytes(), []byte("synthetic-private-prefix")) {
		t.Fatal("operation identifier was not scrubbed in both log sinks")
	}
	// Writer failure must surface ArtifactDiagnostic while preserving accepted inference.
	failPut.Store(true)
	outFail, _, errFail := service.Run(t.Context(), scopeA, input)
	if errFail != nil || outFail.Status != "succeeded" || outFail.Output != "OK" || outFail.ArtifactDiagnostic == nil || outFail.ArtifactDiagnostic.Code != "artifact_write_failed" {
		t.Fatalf("artifact writer failure did not preserve accepted inference or report diagnostic: out=%+v err=%v", outFail, errFail)
	}
}
