//go:build integration

package postgres

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-021 TEST-053

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/integrationtest"
)

func TestCacheConcurrency(t *testing.T) {
	_, dsn := integrationtest.PostgresLease(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	for _, owner := range []string{"owner-a", "owner-b"} {
		if err := store.CreateUser(ctx, User{ID: owner, Email: owner + "@example.test", PasswordHash: "$argon2id$v=19$fixture", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	record := CacheRecord{OwnerID: "owner-a", Version: "operation-v2", OperationHash: "hash-a", Operation: json.RawMessage(`{"model":"fixture"}`), Result: json.RawMessage(`{"output":"ok"}`), Usage: json.RawMessage(`{"totalTokens":3}`), Cost: json.RawMessage(`{"known":true,"totalUsd":0.1}`), Envelope: json.RawMessage(`{"schemaVersion":"raw.v1"}`), CreatedAt: now, UpdatedAt: now}
	if err := store.PutCache(ctx, record); err != nil {
		t.Fatal(err)
	}

	const workers = 32
	var wait sync.WaitGroup
	failures := make(chan error, workers*2)
	for index := 0; index < workers; index++ {
		wait.Add(2)
		go func() {
			defer wait.Done()
			failures <- store.PutCache(ctx, record)
		}()
		go func(index int) {
			defer wait.Done()
			got, err := store.Cache(ctx, "owner-a", "operation-v2", "hash-a")
			if err == nil && (!json.Valid(got.Result) || !json.Valid(got.Operation)) {
				err = fmt.Errorf("worker %d observed malformed JSON", index)
			}
			failures <- err
		}(index)
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count, err := store.CountCache(ctx, "owner-a", "operation-v2", "hash-a"); err != nil || count != 1 {
		t.Fatalf("canonical cache rows = %d, %v", count, err)
	}

	isolated := record
	isolated.OwnerID = "owner-b"
	isolated.Version = "operation-v3"
	if err := store.PutCache(ctx, isolated); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Cache(ctx, "owner-a", "operation-v3", "hash-a"); err == nil {
		t.Fatal("cache version or owner boundary collapsed")
	}
	if got, err := store.Cache(ctx, "owner-b", "operation-v3", "hash-a"); err != nil || got.OwnerID != "owner-b" || got.Version != "operation-v3" {
		t.Fatalf("isolated cache round trip = %#v, %v", got, err)
	}
}
