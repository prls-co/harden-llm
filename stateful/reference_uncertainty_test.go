package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-540.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	hardenllm "github.com/prls-co/harden-llm"
)

// Keep the actual TLS router, core and ordinary admission boundary. Only the
// admission metadata store is process-local; PostgreSQL history is tested below
// the integration build tag with the same service and provider fixture.
func referenceUncertaintyService(t *testing.T, pool *pgxpool.Pool, mode string) (*Service, *admissionMemoryStore, *atomic.Int32, <-chan struct{}) {
	t.Helper()
	calls := &atomic.Int32{}
	entered := make(chan struct{})
	release := make(chan struct{})
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var input struct {
			Stream bool `json:"stream"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || !input.Stream {
			t.Error("CPA native request must stream")
			w.WriteHeader(400)
			return
		}
		if mode == "no_headers" {
			<-r.Context().Done()
			<-release
			return
		}
		w.Header().Set("X-Cpa-Trace-Id", "synthetic-private-prefix-0123abcd")
		if mode == "rejected" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"code":"invalid_request","message":"private rejection"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"private partial\"}\n\n")
		w.(http.Flusher).Flush()
		close(entered)
		switch mode {
		case "canceled", "deadline":
			<-r.Context().Done()
			// Keep this fixture open until teardown so the client's own context
			// is the observed stop, rather than racing a server-generated EOF.
			<-release
		case "partial":
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			connection.Close()
		case "completed":
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"fixture\",\"status\":\"completed\",\"output_text\":\"OK\",\"usage\":{\"input_tokens\":4,\"output_tokens\":3,\"total_tokens\":7}}}\n\n")
		}
	}))
	t.Cleanup(func() { close(release); provider.Close() })
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	catalog := fixtureCatalog(t)
	preset := catalog["CPA Responses"]
	var definition Profile
	if json.Unmarshal(preset.Definition, &definition) != nil {
		t.Fatal("invalid preset")
	}
	definition.BaseURL = provider.URL + "/v1"
	preset.Definition, _ = json.Marshal(definition)
	preset.Binding = ProfileCredentialBinding{Source: "deployment", DeploymentUpstreamID: "cpa"}
	store := newAdmissionMemoryStore()
	headerTimeout := time.Duration(0)
	if mode == "no_headers" {
		headerTimeout = 100 * time.Millisecond
	}
	service, err := NewService(Config{Pool: pool, History: HistoryConfig{Enabled: pool != nil},
		Presets:   Catalog{"CPA Responses": preset},
		Admission: &CPAAdmission{Store: store, Signals: admissionSignals{}, PoolID: "pool", Endpoint: definition.BaseURL},
		Client: ClientOptions{
			Connections: []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: definition.BaseURL, APIKey: "synthetic-provider-key"}}, DefaultConnection: "cpa",
			EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: headerTimeout},
		}})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, calls, entered
}

func uncertaintyRunInput() RunInput {
	return RunInput{ProfileID: "CPA Responses", UserPrompt: "private prompt", CallType: hardenllm.CallTypeText, CacheMode: hardenllm.CacheModeOff, TimeoutMS: 1000,
		RecoveryPolicy: json.RawMessage(`{"maxAttempts":3,"retryOn":["network","server_error"],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}`)}
}

func TestReferenceProviderUncertainty(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"partial", "canceled", "rejected", "completed"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			service, store, calls, entered := referenceUncertaintyService(t, nil, mode)
			input := uncertaintyRunInput()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if mode == "canceled" {
				go func() {
					select {
					case <-entered:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			out, _, err := service.Run(ctx, strings.Repeat("a", 64), input)
			if calls.Load() != 1 || len(store.queued) != 1 || len(store.observations) != 1 || len(out.Attempts) != 1 || !out.ProviderInvoked {
				t.Fatalf("dispatch/observation changed: calls=%d queued=%d observations=%d attempts=%d error=%v problem=%+v", calls.Load(), len(store.queued), len(store.observations), len(out.Attempts), err, ProjectRunError(err))
			}
			observation := store.observations[0]
			if !observation.NativeEntered || !observation.LocalStopped || out.Cache.Written {
				t.Fatal("lost positive stop or cached a failed result")
			}
			problem := ProjectRunError(err)
			switch mode {
			case "partial", "canceled":
				if err == nil || problem.Code != "execution_unconfirmed" || observation.Outcome.Kind != "unconfirmed" || out.Output != nil || out.Attempts[0].Retryable {
					t.Fatalf("unknown provider outcome became definitive: error=%v problem=%+v observation=%s status=%s", err, problem, observation.Outcome.Kind, out.Status)
				}
				if observation.Outcome.InputTokens != nil || observation.Outcome.OutputTokens != nil || out.Accounting.Provider.Usage.Status == "complete" {
					t.Fatal("unknown provider usage became complete")
				}
				if mode == "partial" {
					stream := out.Attempts[0].Stream
					if stream == nil || stream.OutputBytes != int64(len("private partial")) || stream.FirstOutputMs == nil || stream.TerminalState != "missing" {
						t.Fatal("fixture did not reproduce output followed by a missing terminal", stream)
					}
				}
			case "rejected":
				if err == nil || problem.Code == "execution_unconfirmed" || observation.Outcome.Kind == "unconfirmed" {
					t.Fatal("known rejection became unknown", err, problem)
				}
			case "completed":
				if err != nil || problem != nil || out.Output != "OK" || observation.Outcome.Kind != "completed" || out.Accounting.Provider.Usage.TotalTokens != 7 {
					t.Fatal("known completion changed", err, problem, out.Accounting)
				}
			}
		})
	}
}
