package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/runtime"
	"github.com/prls-co/harden-llm/internal/traces"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-019

func TestDiagnosticsBundleRedactsAdversarialInputs(t *testing.T) {
	t.Parallel()
	secrets := []string{"fake-provider-secret", "fake-prompt-secret", "fake-cookie-secret", "fake-encryption-secret", "fake-ciphertext-secret"}
	trace := traces.Trace{
		SchemaVersion: "harden-llm.trace.v2", CallID: "call-1", TraceID: "trace-1", Status: traces.StatusFailure,
		TotalCallDurationMs: 250, TotalWaitMs: 50,
		Accounting: runtime.Accounting{
			Result: diagnosticsLedger(2, 1), Provider: diagnosticsLedger(2, 1),
		},
		Cache:   runtime.CacheFacts{Status: "miss"},
		Context: runtime.ObservabilityContext{Environment: "test", Metadata: map[string]string{"authorization": "Bearer fake-provider-secret"}},
	}
	bundle, err := Build(Input{
		RuntimeIdentity: RuntimeIdentity{Version: "0.1.0", CommitSHA: "abc123", GoVersion: "go1.26.0", OS: "linux", Arch: "amd64"},
		Trace:           trace, EndpointURL: "https://user:fake-provider-secret@api.example/v1?api_key=fake-provider-secret",
		Environment: map[string]string{"REGION": "local", "LLM_CREDENTIAL_ENCRYPTION_KEY": "fake-encryption-secret"},
		Prompts:     map[string]string{"system": "do not print fake-prompt-secret"},
		Headers:     map[string]string{"Authorization": "Bearer fake-provider-secret", "Cookie": "sid=fake-cookie-secret"},
		Config:      map[string]any{"ciphertext": "fake-ciphertext-secret"},
		Logs:        []string{"request failed Bearer fake-provider-secret"}, Error: "upstream rejected fake-provider-secret",
		Artifacts: []ArtifactIdentity{{Kind: "trace", Key: "llm-traces/trace-1.json", SHA256: strings.Repeat("a", 64), SizeBytes: 10, ContentType: "application/json"}},
		Secrets:   secrets,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("diagnostics leaked %q: %s", secret, encoded)
		}
	}
	if bundle.EndpointHost != "api.example" || bundle.EnvironmentFingerprint == "" || len(bundle.Artifacts) != 1 {
		t.Fatalf("diagnostic signal missing: %#v", bundle)
	}
	if bundle.Trace.Accounting.Result.Usage.TotalTokens() != 3 || bundle.Trace.TotalCallDurationMs != 250 {
		t.Fatalf("trace signal changed during redaction: %#v", bundle.Trace)
	}
}

func diagnosticsLedger(input, output int64) runtime.Ledger {
	usage, err := accounting.CompleteUsage(input, 0, 0, output, 0)
	if err != nil {
		panic(err)
	}
	return runtime.Ledger{Usage: usage, Cost: accounting.UnknownCost("missing_rate")}
}

func TestDiagnosticsBundleArtifactFailureIsBoundedAndNonFatal(t *testing.T) {
	t.Parallel()
	store := failingStore{err: errors.New("Garage rejected Bearer fake-provider-secret with a very long internal message " + strings.Repeat("x", 1000))}
	ref, observation := PersistAttachment(context.Background(), store, Attachment{
		Key: "diagnostics/call-1.json", Content: []byte(`{"safe":true}`), ContentType: "application/json",
	}, []string{"fake-provider-secret"})
	if ref != (ArtifactIdentity{}) {
		t.Fatalf("failed persistence returned a reference: %#v", ref)
	}
	if observation == nil || observation.Kind != "artifact.persistence" || observation.Outcome != "failure" {
		t.Fatalf("missing non-fatal persistence observation: %#v", observation)
	}
	encoded, _ := json.Marshal(observation)
	if strings.Contains(string(encoded), "fake-provider-secret") || len(encoded) > 512 {
		t.Fatalf("persistence observation is unsafe or unbounded: %s", encoded)
	}

	success := memoryStore{}
	ref, observation = PersistAttachment(context.Background(), success, Attachment{
		Key: "diagnostics/call-1.json", Content: []byte(`{"safe":true}`), ContentType: "application/json",
	}, nil)
	if observation != nil || ref.Key == "" || ref.SHA256 == "" {
		t.Fatalf("successful persistence mismatch: %#v %#v", ref, observation)
	}
}

func TestDiagnosticsBundleEnforcesOutputBound(t *testing.T) {
	t.Parallel()
	_, err := Build(Input{
		RuntimeIdentity: RuntimeIdentity{Version: "test"},
		Trace:           traces.Trace{SchemaVersion: "harden-llm.trace.v1"},
		EndpointURL:     "https://api.example",
		Logs:            []string{strings.Repeat("x", maxBundleBytes)},
	})
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized diagnostic bundle was accepted: %v", err)
	}
	message := bounded(strings.Repeat("é", 300), maxPersistenceMessageLen)
	if !utf8.ValidString(message) || len(message) > maxPersistenceMessageLen+3 {
		t.Fatalf("bounded diagnostic message is invalid or oversized: bytes=%d", len(message))
	}
}

type failingStore struct{ err error }

func (store failingStore) Put(context.Context, string, []byte, string) (ArtifactIdentity, error) {
	return ArtifactIdentity{}, store.err
}

type memoryStore struct{}

func (memoryStore) Put(_ context.Context, key string, content []byte, contentType string) (ArtifactIdentity, error) {
	return ArtifactIdentity{Kind: "diagnostic", Key: key, SHA256: strings.Repeat("b", 64), SizeBytes: int64(len(content)), ContentType: contentType, CreatedAt: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)}, nil
}
