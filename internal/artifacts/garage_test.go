//go:build integration

package artifacts

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-040 TEST-053

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/integrationtest"
)

func TestGarageArtifactStore(t *testing.T) {
	_, fixture := integrationtest.GarageLease(t)
	store, err := NewGarage(Config{
		Endpoint: fixture.Endpoint, ExternalEndpoint: fixture.Endpoint,
		Bucket: fixture.Bucket, Region: fixture.Region,
		AccessKeyID: fixture.AccessKeyID, SecretAccessKey: fixture.SecretAccessKey,
		OperationTimeout: 3 * time.Second, MaxPresignTTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.Scoped(fixture.Scope("llm-traces/owner-a/"))
	if err != nil {
		t.Fatal(err)
	}

	artifacts := []struct {
		key     string
		content string
	}{
		{fixture.Key("llm-traces/owner-a/task-a/trace-a/artifact-1-trace.json"), `{"schemaVersion":"harden-llm.trace.v1","status":"success"}`},
		{fixture.Key("llm-traces/owner-a/task-a/trace-a/artifact-2-raw.json"), `{"schemaVersion":"harden-llm.parse-failure.v1","rawResponse":"[REDACTED]"}`},
		{fixture.Key("llm-traces/owner-a/task-a/trace-a/artifact-3-diagnostic.json"), `{"schemaVersion":"harden-llm.diagnostic.v1","outcome":"failure"}`},
	}
	for _, artifact := range artifacts {
		reference, err := owner.Put(context.Background(), artifact.key, []byte(artifact.content), "application/json")
		if err != nil {
			t.Fatalf("put %s: %v", artifact.key, err)
		}
		digest := sha256.Sum256([]byte(artifact.content))
		if reference.Key != artifact.key || reference.SHA256 != hex.EncodeToString(digest[:]) || reference.SizeBytes != int64(len(artifact.content)) || reference.ContentType != "application/json" {
			t.Fatalf("metadata mismatch for %s: %#v", artifact.key, reference)
		}
		content, loaded, err := owner.Get(context.Background(), artifact.key)
		if err != nil || string(content) != artifact.content || loaded != reference {
			t.Fatalf("get %s: content=%s metadata=%#v error=%v", artifact.key, content, loaded, err)
		}
	}
	if _, err := owner.Put(context.Background(), artifacts[0].key, []byte(`{"changed":true}`), "application/json"); !IsKind(err, KindConflict) {
		t.Fatalf("duplicate object key was not rejected: %v", err)
	}
	for _, key := range []string{"", "/absolute.json", fixture.Key("../escape.json"), fixture.Key("llm-traces/owner-a/../owner-b/object.json"), fixture.Key("llm-traces/owner-b/object.json"), fixture.Key("llm-traces/owner-a//object.json"), fixture.Key("llm-traces/owner-a/object.txt")} {
		if _, err := owner.Put(context.Background(), key, []byte(`{"safe":true}`), "application/json"); !IsKind(err, KindInvalid) {
			t.Errorf("unsafe key %q was accepted: %v", key, err)
		}
	}
	if _, err := owner.Put(context.Background(), fixture.Key("llm-traces/owner-a/not-json.json"), []byte(`not-json`), "application/json"); !IsKind(err, KindInvalid) {
		t.Fatalf("noncanonical content was accepted: %v", err)
	}
	if _, _, err := owner.Get(context.Background(), fixture.Key("llm-traces/owner-b/object.json")); !IsKind(err, KindInvalid) {
		t.Fatalf("cross-prefix read was accepted: %v", err)
	}

	presigned, err := owner.PresignGet(context.Background(), artifacts[0].key, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(presigned) //nolint:gosec // isolated test endpoint
	if err != nil {
		t.Fatal(err)
	}
	immediate, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(immediate) != artifacts[0].content {
		t.Fatalf("presigned GET = %d %s", response.StatusCode, immediate)
	}
	time.Sleep(2100 * time.Millisecond)
	expired, err := http.Get(presigned) //nolint:gosec // isolated test endpoint
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, expired.Body)
	_ = expired.Body.Close()
	if expired.StatusCode == http.StatusOK {
		t.Fatal("expired presigned URL remained valid")
	}
	if _, err := owner.PresignGet(context.Background(), artifacts[0].key, 5*time.Minute+time.Second); !IsKind(err, KindInvalid) {
		t.Fatalf("oversized presign TTL was accepted: %v", err)
	}

	wrongCredentialStore, err := NewGarage(Config{
		Endpoint: fixture.Endpoint, ExternalEndpoint: fixture.Endpoint,
		Bucket: fixture.Bucket, Region: fixture.Region,
		AccessKeyID: fixture.AccessKeyID, SecretAccessKey: strings.Repeat("f", 64),
		OperationTimeout: 700 * time.Millisecond, MaxPresignTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = wrongCredentialStore.Put(context.Background(), fixture.Key("llm-traces/owner-a/wrong-credential.json"), []byte(`{"safe":true}`), "application/json")
	if err == nil || strings.Contains(err.Error(), strings.Repeat("f", 16)) || strings.Contains(err.Error(), fixture.Endpoint) {
		t.Fatalf("credential failure was absent or leaked configuration: %v", err)
	}

	unavailable, err := NewGarage(Config{
		Endpoint: "http://127.0.0.1:1", ExternalEndpoint: "http://127.0.0.1:1",
		Bucket: fixture.Bucket, Region: fixture.Region,
		AccessKeyID: fixture.AccessKeyID, SecretAccessKey: fixture.SecretAccessKey,
		OperationTimeout: 400 * time.Millisecond, MaxPresignTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = unavailable.Put(context.Background(), fixture.Key("llm-traces/owner-a/unavailable.json"), []byte(`{"safe":true}`), "application/json")
	if err == nil || (!IsKind(err, KindUnavailable) && !IsKind(err, KindTimeout)) || time.Since(started) > time.Second || strings.Contains(err.Error(), fixture.SecretAccessKey) {
		t.Fatalf("unavailable store failure was not bounded and safe: elapsed=%s error=%v", time.Since(started), err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := owner.Put(canceled, fixture.Key("llm-traces/owner-a/canceled.json"), []byte(`{"safe":true}`), "application/json"); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was not preserved: %v", err)
	}
}
