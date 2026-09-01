//go:build integration

package gateway_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-060

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/internal/postgres"
)

func TestArtifactCoordinatorCrashConvergence(t *testing.T) {
	_, dsn := integrationtest.PostgresLease(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := store.CreateUser(ctx, postgres.User{
		ID: "artifact-owner", Email: "artifact@example.test", PasswordHash: "$argon2id$v=19$fixture",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	clock := now
	objects := &journalObjectStore{objects: make(map[string]objectFixture)}
	nextBatch := 0
	coordinator, err := gateway.NewArtifactCoordinator(gateway.ArtifactCoordinatorConfig{
		Store: store, Clock: func() time.Time { return clock },
		NewID: func() (string, error) { nextBatch++; return fmt.Sprintf("artifact-batch-%d", nextBatch), nil },
		Scope: func(ownerID string) (gateway.ArtifactObjectAccess, error) {
			if ownerID != "artifact-owner" {
				return nil, errors.New("wrong owner")
			}
			return objects, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("object without execution metadata is removed after restart", func(t *testing.T) {
		publication := artifactPublication("orphan-run", "orphan-trace", "orphan-artifact")
		reference, err := coordinator.PublishArtifact(ctx, publication)
		if err != nil || reference.ArtifactID != publication.ArtifactID || !objects.exists(publication.ObjectKey) {
			t.Fatalf("publication = %#v, %v", reference, err)
		}
		clock = clock.Add(16 * time.Minute)
		summary, err := coordinator.Reconcile(ctx)
		if err != nil || summary.Completed != 1 || objects.exists(publication.ObjectKey) {
			t.Fatalf("orphan reconciliation = %#v, %v", summary, err)
		}
		second, err := coordinator.Reconcile(ctx)
		if err != nil || second != (gateway.ArtifactReconcileSummary{}) {
			t.Fatalf("second reconciliation = %#v, %v", second, err)
		}
	})

	t.Run("execution save consumes publication and interrupted delete resumes", func(t *testing.T) {
		publication := artifactPublication("saved-run", "saved-trace", "saved-artifact")
		reference, err := coordinator.PublishArtifact(ctx, publication)
		if err != nil {
			t.Fatal(err)
		}
		artifact := postgres.ArtifactRecord{
			OwnerID: publication.OwnerID, RunID: publication.RunID, TraceID: publication.TraceID,
			ID: publication.ArtifactID, Kind: publication.Kind, ObjectKey: publication.ObjectKey,
			ContentType: reference.ContentType, SHA256: reference.SHA256, SizeBytes: reference.SizeBytes,
			State: "available", CreatedAt: clock, UpdatedAt: clock,
		}
		if err := store.SaveExecution(ctx, postgres.RunRecord{
			OwnerID: publication.OwnerID, ID: publication.RunID, ProfileID: "Profile",
			TraceID: publication.TraceID, Status: "succeeded",
			Request: json.RawMessage(`{"profileId":"Profile"}`), Result: json.RawMessage(`{"output":"ok"}`),
			StartedAt: clock, CompletedAt: clock,
		}, postgres.TraceRecord{
			OwnerID: publication.OwnerID, RunID: publication.RunID, TraceID: publication.TraceID,
			Record: json.RawMessage(`{"status":"success"}`), CreatedAt: clock, UpdatedAt: clock,
		}, nil, []postgres.ArtifactRecord{artifact}); err != nil {
			t.Fatal(err)
		}
		operation, err := store.ArtifactOperation(ctx, postgres.ArtifactOperationID("publish", artifact))
		if err != nil || operation.State != "completed" {
			t.Fatalf("consumed publication = %#v, %v", operation, err)
		}

		objects.failDelete = true
		if err := coordinator.DeleteExecution(ctx, publication.OwnerID, publication.RunID, publication.TraceID); err == nil {
			t.Fatal("interrupted deletion unexpectedly succeeded")
		}
		if _, err := store.Run(ctx, publication.OwnerID, publication.RunID); err != nil {
			t.Fatalf("failed object delete removed execution metadata: %v", err)
		}
		if _, err := store.Artifact(ctx, publication.OwnerID, publication.TraceID, publication.ArtifactID); !errors.Is(err, postgres.ErrNotFound) {
			t.Fatalf("deleting artifact remained actionable: %v", err)
		}

		objects.failDelete = false
		clock = clock.Add(time.Minute)
		summary, err := coordinator.Reconcile(ctx)
		if err != nil || summary.Applied != 1 || summary.Completed != 1 {
			t.Fatalf("delete reconciliation = %#v, %v", summary, err)
		}
		if _, err := store.Run(ctx, publication.OwnerID, publication.RunID); !errors.Is(err, postgres.ErrNotFound) {
			t.Fatalf("reconciled execution remained: %v", err)
		}
		if objects.exists(publication.ObjectKey) {
			t.Fatal("reconciled object remained")
		}
		second, err := coordinator.Reconcile(ctx)
		if err != nil || second != (gateway.ArtifactReconcileSummary{}) {
			t.Fatalf("second delete reconciliation = %#v, %v", second, err)
		}
	})

	t.Run("available metadata is demoted when its object disappears", func(t *testing.T) {
		publication := artifactPublication("missing-run", "missing-trace", "missing-artifact")
		reference, err := coordinator.PublishArtifact(ctx, publication)
		if err != nil {
			t.Fatal(err)
		}
		artifact := postgres.ArtifactRecord{
			OwnerID: publication.OwnerID, RunID: publication.RunID, TraceID: publication.TraceID,
			ID: publication.ArtifactID, Kind: publication.Kind, ObjectKey: publication.ObjectKey,
			ContentType: reference.ContentType, SHA256: reference.SHA256, SizeBytes: reference.SizeBytes,
			State: "available", CreatedAt: clock, UpdatedAt: clock,
		}
		if err := store.SaveExecution(ctx, postgres.RunRecord{
			OwnerID: publication.OwnerID, ID: publication.RunID, ProfileID: "Profile",
			TraceID: publication.TraceID, Status: "succeeded",
			Request: json.RawMessage(`{"profileId":"Profile"}`), Result: json.RawMessage(`{"output":"ok"}`),
			StartedAt: clock, CompletedAt: clock,
		}, postgres.TraceRecord{
			OwnerID: publication.OwnerID, RunID: publication.RunID, TraceID: publication.TraceID,
			Record: json.RawMessage(`{"status":"success"}`), CreatedAt: clock, UpdatedAt: clock,
		}, nil, []postgres.ArtifactRecord{artifact}); err != nil {
			t.Fatal(err)
		}
		if err := objects.DeleteMany(ctx, []string{publication.ObjectKey}); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(16 * time.Minute)
		summary, err := coordinator.Reconcile(ctx)
		if err != nil || summary.Audited != 1 || summary.Unavailable != 1 {
			t.Fatalf("missing object audit = %#v, %v", summary, err)
		}
		if _, err := store.Artifact(ctx, publication.OwnerID, publication.TraceID, publication.ArtifactID); !errors.Is(err, postgres.ErrNotFound) {
			t.Fatalf("unavailable artifact remained actionable: %v", err)
		}
		second, err := coordinator.Reconcile(ctx)
		if err != nil || second != (gateway.ArtifactReconcileSummary{}) {
			t.Fatalf("second integrity audit = %#v, %v", second, err)
		}
	})
}

func artifactPublication(runID, traceID, artifactID string) hardenllm.ArtifactPublication {
	return hardenllm.ArtifactPublication{
		OwnerID: "artifact-owner", RunID: runID, TraceID: traceID,
		ArtifactID: artifactID, Kind: "trace",
		ObjectKey: "llm-traces/artifact-owner/" + runID + "/" + traceID + "/" + artifactID + ".json",
		Content:   []byte(`{"safe":true}`), ContentType: "application/json",
	}
}

type objectFixture struct {
	contentType string
	digest      string
	size        int64
}

type journalObjectStore struct {
	mu         sync.Mutex
	objects    map[string]objectFixture
	failDelete bool
}

func (store *journalObjectStore) Put(_ context.Context, key string, content []byte, contentType string) (hardenllm.ArtifactRef, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	digest := sha256.Sum256(content)
	fixture := objectFixture{contentType: contentType, digest: hex.EncodeToString(digest[:]), size: int64(len(content))}
	if existing, ok := store.objects[key]; ok && existing != fixture {
		return hardenllm.ArtifactRef{}, errors.New("conflict")
	}
	store.objects[key] = fixture
	return hardenllm.ArtifactRef{Key: key, SHA256: fixture.digest, SizeBytes: fixture.size, ContentType: contentType}, nil
}

func (store *journalObjectStore) Inspect(_ context.Context, key string) (hardenllm.ArtifactRef, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	fixture, ok := store.objects[key]
	if !ok {
		return hardenllm.ArtifactRef{}, false, nil
	}
	return hardenllm.ArtifactRef{Key: key, SHA256: fixture.digest, SizeBytes: fixture.size, ContentType: fixture.contentType}, true, nil
}

func (*journalObjectStore) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "https://artifact.example.test/signed", nil
}

func (store *journalObjectStore) DeleteMany(_ context.Context, keys []string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failDelete {
		return errors.New("delete unavailable")
	}
	for _, key := range keys {
		delete(store.objects, key)
	}
	return nil
}

func (store *journalObjectStore) exists(key string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, ok := store.objects[key]
	return ok
}
