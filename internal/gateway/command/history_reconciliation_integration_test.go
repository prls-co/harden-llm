//go:build integration

package command

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-061

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/internal/postgres"
)

func TestRetainedHistoryReconciliation(t *testing.T) {
	_, dsn := integrationtest.PostgresLease(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.MigrateForHistoryReconciliation(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := store.CreateUser(ctx, postgres.User{
		ID: "legacy-owner", Email: "legacy@example.test", PasswordHash: "$argon2id$v=19$fixture",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	trace := postgres.TraceRecord{
		OwnerID: "legacy-owner", TraceID: "legacy-trace",
		Record:    json.RawMessage(`{"schemaVersion":1,"runId":"legacy-run","traceId":"legacy-trace","callId":"legacy-call","status":"succeeded","attempts":[],"cache":{},"usage":{},"cost":{}}`),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SeedLegacyRunlessTraceForTest(ctx, trace, []postgres.ObservationRecord{{
		OwnerID: trace.OwnerID, TraceID: trace.TraceID, Sequence: 0, Type: "result",
		Data: json.RawMessage(`{"outcome":"success"}`), CreatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	objects := &reconciliationObjectStore{objects: make(map[string]hardenllm.ArtifactRef)}
	objectKey := "llm-traces/legacy-owner/legacy-run/legacy-trace/legacy-artifact.json"
	reference, err := objects.Put(ctx, objectKey, []byte(`{"safe":true}`), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SeedArtifactMetadataForTest(ctx, postgres.ArtifactRecord{
		OwnerID: trace.OwnerID, TraceID: trace.TraceID, ID: "legacy-artifact", Kind: "trace",
		ObjectKey: objectKey, ContentType: reference.ContentType, SHA256: reference.SHA256,
		SizeBytes: reference.SizeBytes, State: "available", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	siblingTrace := postgres.TraceRecord{
		OwnerID: trace.OwnerID, TraceID: "legacy-trace-sibling",
		Record:    json.RawMessage(`{"schemaVersion":1,"runId":"legacy-run-sibling","traceId":"legacy-trace-sibling","callId":"legacy-call-sibling","status":"succeeded","attempts":[],"cache":{},"usage":{},"cost":{}}`),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SeedLegacyRunlessTraceForTest(ctx, siblingTrace, nil); err != nil {
		t.Fatal(err)
	}
	siblingObjectKey := "llm-traces/legacy-owner/legacy-run-sibling/legacy-trace-sibling/legacy-artifact-sibling.json"
	siblingReference, err := objects.Put(ctx, siblingObjectKey, []byte(`{"safe":"sibling"}`), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SeedArtifactMetadataForTest(ctx, postgres.ArtifactRecord{
		OwnerID: siblingTrace.OwnerID, TraceID: siblingTrace.TraceID, ID: "legacy-artifact-sibling", Kind: "trace",
		ObjectKey: siblingObjectKey, ContentType: siblingReference.ContentType, SHA256: siblingReference.SHA256,
		SizeBytes: siblingReference.SizeBytes, State: "available", CreatedAt: siblingTrace.CreatedAt, UpdatedAt: siblingTrace.UpdatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err == nil {
		t.Fatal("execution ownership migration accepted retained runless traces")
	}
	if versions, err := store.AppliedMigrations(ctx); err != nil || !reflect.DeepEqual(versions, []int64{1, 2, 3, 4}) {
		t.Fatalf("failed ownership migration changed migration state: %v, %v", versions, err)
	}
	nextID := 0
	coordinator, err := gateway.NewArtifactCoordinator(gateway.ArtifactCoordinatorConfig{
		Store: store, Clock: func() time.Time { return now },
		NewID: func() (string, error) { nextID++; return fmt.Sprintf("history-batch-%d", nextID), nil },
		Scope: func(ownerID string) (gateway.ArtifactObjectAccess, error) {
			if ownerID != trace.OwnerID {
				return nil, errors.New("wrong owner")
			}
			return objects, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := HistoryReconciliationConfig{Store: store, Artifacts: coordinator, OwnerID: trace.OwnerID}
	first, err := ReconcileHistory(ctx, config)
	if err != nil || first.ClassifiedTraces != 2 || first.UnclassifiedTraces != 0 || first.IntegrityArtifacts != 2 {
		t.Fatalf("dry run = %#v, %v", first, err)
	}
	second, err := ReconcileHistory(ctx, config)
	if err != nil || second.PlanDigest != first.PlanDigest {
		t.Fatalf("second dry run = %#v, %v", second, err)
	}
	wrong := config
	wrong.Apply, wrong.PlanDigest = true, string(make([]byte, 64))
	if _, err := ReconcileHistory(ctx, wrong); err == nil {
		t.Fatal("changed plan digest was accepted")
	}
	if _, _, err := store.Trace(ctx, trace.OwnerID, trace.TraceID); err != nil {
		t.Fatalf("digest mismatch mutated trace: %v", err)
	}
	apply := config
	apply.Apply, apply.PlanDigest = true, first.PlanDigest
	applied, err := ReconcileHistory(ctx, apply)
	if err != nil || applied.AppliedTraces != 2 {
		t.Fatalf("apply = %#v, %v", applied, err)
	}
	if _, _, err := store.Trace(ctx, trace.OwnerID, trace.TraceID); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("reconciled trace remained: %v", err)
	}
	if objects.exists(objectKey) {
		t.Fatal("reconciled artifact object remained")
	}
	if _, _, err := store.Trace(ctx, siblingTrace.OwnerID, siblingTrace.TraceID); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("reconciled sibling trace remained: %v", err)
	}
	if objects.exists(siblingObjectKey) {
		t.Fatal("reconciled sibling artifact object remained")
	}
	noOp, err := ReconcileHistory(ctx, apply)
	if err != nil || noOp.AppliedTraces != 0 || noOp.CandidateTraces != 0 {
		t.Fatalf("second apply = %#v, %v", noOp, err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("ownership migration after reconciliation: %v", err)
	}
	if versions, err := store.AppliedMigrations(ctx); err != nil || !reflect.DeepEqual(versions, []int64{1, 2, 3, 4, 5}) {
		t.Fatalf("post-reconciliation migrations = %v, %v", versions, err)
	}
	if err := store.Ready(ctx); err != nil {
		t.Fatalf("post-reconciliation store is not ready: %v", err)
	}
}

type reconciliationObjectStore struct {
	mu      sync.Mutex
	objects map[string]hardenllm.ArtifactRef
}

func (store *reconciliationObjectStore) Put(_ context.Context, key string, content []byte, contentType string) (hardenllm.ArtifactRef, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	digest := sha256.Sum256(content)
	reference := hardenllm.ArtifactRef{Key: key, SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(content)), ContentType: contentType}
	store.objects[key] = reference
	return reference, nil
}

func (store *reconciliationObjectStore) Inspect(_ context.Context, key string) (hardenllm.ArtifactRef, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	reference, ok := store.objects[key]
	return reference, ok, nil
}

func (*reconciliationObjectStore) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (store *reconciliationObjectStore) DeleteMany(_ context.Context, keys []string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, key := range keys {
		delete(store.objects, key)
	}
	return nil
}

func (store *reconciliationObjectStore) exists(key string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, ok := store.objects[key]
	return ok
}
