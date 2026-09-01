package hardenllm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/retry"
	coreruntime "github.com/prls-co/harden-llm/internal/runtime"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-019

func TestClientArtifactPersistenceIsRedactedAndNonFatal(t *testing.T) {
	t.Parallel()
	resultFixture := coreruntime.ProviderResult{
		Output:              "ok",
		Accounting:          testLedger(1, 0, 0, 1, 0, accounting.UnknownCost("missing_rate")),
		RawProviderEnvelope: json.RawMessage(`{"authorization":"Bearer fixture-only-key","output_text":"fixture prompt echoed"}`),
	}

	t.Run("success", func(t *testing.T) {
		store := &recordingArtifactStore{}
		client, err := New(Options{Credentials: fixedCredentialResolver{}, Artifacts: store})
		if err != nil {
			t.Fatal(err)
		}
		client.executor = &fixedExecutor{result: resultFixture}
		ids := []string{"call-artifact", "trace-artifact"}
		client.newID = func() (string, error) { id := ids[0]; ids = ids[1:]; return id, nil }
		result, err := client.Call(context.Background(), Request{
			ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "fixture prompt", CallType: CallTypeText,
			Context:     ObservabilityContext{OrganizationID: "org-1", TaskID: "task-1"},
			RetryPolicy: RetryPolicy{MaxAttempts: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Artifacts) != 1 || len(store.contents) != 1 {
			t.Fatalf("artifact result mismatch: %#v writes=%d", result.Artifacts, len(store.contents))
		}
		for _, content := range store.contents {
			if strings.Contains(string(content), "fixture-only-key") || strings.Contains(string(content), "fixture prompt") || !json.Valid(content) {
				t.Fatalf("unsafe artifact content: %s", content)
			}
		}
	})

	t.Run("failure remains non-fatal", func(t *testing.T) {
		client, err := New(Options{Credentials: fixedCredentialResolver{}, Artifacts: failingArtifactStore{}})
		if err != nil {
			t.Fatal(err)
		}
		client.executor = &fixedExecutor{result: resultFixture}
		client.newID = func() (string, error) { return "fixed", nil }
		result, err := client.Call(context.Background(), Request{
			ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "fixture", CallType: CallTypeText,
			RetryPolicy: RetryPolicy{MaxAttempts: 1},
		})
		if err != nil || result.Output != "ok" || len(result.Artifacts) != 0 {
			t.Fatalf("artifact failure changed provider result: %#v %v", result, err)
		}
	})

	t.Run("parse failure preserves exact redacted response", func(t *testing.T) {
		store := &recordingArtifactStore{}
		client, err := New(Options{Credentials: fixedCredentialResolver{}, Artifacts: store})
		if err != nil {
			t.Fatal(err)
		}
		client.executor = &fixedExecutor{
			result: coreruntime.ProviderResult{
				Accounting: testLedger(4, 0, 0, 2, 0, accounting.ExactCost(0.000006, "profile")),
			},
			err: &retry.ProviderError{
				Err: errors.New("structured parse failed"), Parse: true,
				RawResponse: `{"answer":"fixture-only-key","unfinished":`,
			},
		}
		ids := []string{"call-parse", "trace-parse"}
		client.newID = func() (string, error) { id := ids[0]; ids = ids[1:]; return id, nil }
		result, err := client.Call(context.Background(), Request{
			ProfileID: "primary", Profiles: testProfiles(), UserPrompt: "fixture", CallType: CallTypeText,
			Context:     ObservabilityContext{OrganizationID: "org-1", TaskID: "task-1"},
			RetryPolicy: RetryPolicy{MaxAttempts: 1},
		})
		if err == nil || len(store.contents) != 2 || len(result.Artifacts) != 2 {
			t.Fatalf("parse failure artifacts = %d/%d, error = %v", len(store.contents), len(result.Artifacts), err)
		}
		if result.CallID != "call-parse" || result.TraceID != "trace-parse" || len(result.Attempts) != 1 ||
			result.Accounting.Provider.Usage.TotalTokens != 6 || result.Accounting.Provider.Cost.Status != "exact" {
			t.Fatalf("parse failure result lost diagnostic context: %#v", result)
		}
		for key, content := range store.contents {
			if !json.Valid(content) || strings.Contains(string(content), "fixture-only-key") {
				t.Fatalf("unsafe parse failure artifact %q: %s", key, content)
			}
		}
	})
}

type recordingArtifactStore struct {
	contents map[string][]byte
}

func (store *recordingArtifactStore) Put(_ context.Context, key string, content []byte, contentType string) (ArtifactRef, error) {
	if store.contents == nil {
		store.contents = make(map[string][]byte)
	}
	store.contents[key] = append([]byte(nil), content...)
	digest := sha256.Sum256(content)
	return ArtifactRef{Key: key, SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(content)), ContentType: contentType}, nil
}

func (*recordingArtifactStore) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "https://example.invalid/presigned", nil
}

type failingArtifactStore struct{}

func (failingArtifactStore) Put(context.Context, string, []byte, string) (ArtifactRef, error) {
	return ArtifactRef{}, errors.New("store failed with Bearer fixture-only-key")
}

func (failingArtifactStore) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", errors.New("store failed")
}
