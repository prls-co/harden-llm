//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-439

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	hardenllm "github.com/prls-co/harden-llm"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func cacheFixtureRecord(key string, now time.Time) hardenllm.CacheRecord {
	return hardenllm.CacheRecord{SchemaVersion: 2, CacheVersion: "operation-v2", OperationHash: key, ProviderResult: json.RawMessage(`{"output":"cached-fixture","accounting":{"usage":{"status":"unavailable"},"cost":{"status":"unknown","unknownObservations":1}}}`), CreatedAt: now}
}
func fixtureCall(mode hardenllm.CacheMode) hardenllm.Request {
	return hardenllm.Request{ModelID: "gpt-5.6-luna", CallType: hardenllm.CallTypeText, Messages: []hardenllm.Message{{Role: "user", Content: json.RawMessage(`"input-fixture"`)}}, CacheMode: mode, RecoveryPolicy: hardenllm.RecoveryPolicy{MaxAttempts: 1, RetryOn: []hardenllm.RecoveryCategory{}, Backoff: hardenllm.RecoveryBackoff{}}}
}
func TestCacheDraftContract(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	t.Run("cache envelope is scoped by key and credential namespace", func(t *testing.T) {
		p, _ := referencePool(t, true)
		a := newPostgresCache(p, keyHashA, "one", time.Hour, clock)
		b := newPostgresCache(p, keyHashB, "one", time.Hour, clock)
		key := strings.Repeat("c", 64)
		record := cacheFixtureRecord(key, now)
		if err := a.Set(context.Background(), key, record); err != nil {
			t.Fatal(err)
		}
		stored, hit, err := a.Get(context.Background(), key)
		if err != nil || !hit || !sameJSON(stored.ProviderResult, record.ProviderResult) {
			t.Fatal("cache envelope not stored", err, hit)
		}
		_, hit, err = b.Get(context.Background(), key)
		if err != nil || hit {
			t.Fatal("foreign key received cache envelope", err)
		}
		otherNamespace := newPostgresCache(p, keyHashA, "two", time.Hour, clock)
		_, hit, err = otherNamespace.Get(context.Background(), key)
		if err != nil || hit {
			t.Fatal("foreign credential namespace received cache")
		}
	})
	t.Run("two ordinary clients with same key share cached response", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		a, err := s.ClientForKey(keyHashA)
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.ClientForKey(keyHashA)
		if err != nil {
			t.Fatal(err)
		}
		first, err := a.Call(context.Background(), fixtureCall(hardenllm.CacheModeCache))
		if err != nil {
			t.Fatal(err)
		}
		second, err := b.Call(context.Background(), fixtureCall(hardenllm.CacheModeCache))
		if err != nil {
			t.Fatal(err)
		}
		if first.Cache.Served || !second.Cache.Served || second.Output != first.Output || second.Accounting.Provider.Usage.Status != "unavailable" {
			t.Fatal("same-key clients did not share cache")
		}
	})
	t.Run("entered credential replacement invalidates previous namespace", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, input := serviceFixture(t, p, nil)
		input = enteredSnapshot(t, input, "entered")
		view, err := s.SaveProfile(context.Background(), keyHashA, input)
		if err != nil {
			t.Fatal(err)
		}
		before, err := s.ResolveProfile(context.Background(), keyHashA, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		n1 := credentialNamespace(hardenllm.ConnectionCatalog{"generation": before.Connection}, map[string]int64{"generation": before.CredentialRevision})
		old := newPostgresCache(p, keyHashA, n1, time.Hour, clock)
		key := strings.Repeat("c", 64)
		if err := old.Set(context.Background(), key, cacheFixtureRecord(key, now)); err != nil {
			t.Fatal(err)
		}
		input.Credential.Revision = 1
		input.Credential.Payload.APIKey = "replacement-secret"
		if _, err := s.SaveProfile(context.Background(), keyHashA, input); err != nil {
			t.Fatal(err)
		}
		after, err := s.ResolveProfile(context.Background(), keyHashA, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		n2 := credentialNamespace(hardenllm.ConnectionCatalog{"generation": after.Connection}, map[string]int64{"generation": after.CredentialRevision})
		_, hit, err := newPostgresCache(p, keyHashA, n2, time.Hour, clock).Get(context.Background(), key)
		if err != nil || hit || n1 == n2 {
			t.Fatal("credential replacement reused stale cache", err)
		}
		if strings.Contains(n2, "replacement-secret") {
			t.Fatal("namespace contains raw provider key")
		}
	})
	t.Run("changing a recovery credential changes namespace", func(t *testing.T) {
		c := hardenllm.ConnectionCatalog{"generation": {ID: "generation", BaseURL: "https://api.example/v1", APIKey: "generation-key"}, "repair-initial": {ID: "repair-initial", BaseURL: "https://repair.example/v1", APIKey: "repair-key"}}
		before := credentialNamespace(c, map[string]int64{"repair-initial": 1})
		v := c["repair-initial"]
		v.APIKey = "changed-key"
		c["repair-initial"] = v
		after := credentialNamespace(c, map[string]int64{"repair-initial": 2})
		if before == after || strings.Contains(after, "changed-key") {
			t.Fatal("repair credential not bound by a safe fingerprint")
		}
	})
	t.Run("expired cache is not served", func(t *testing.T) {
		p, _ := referencePool(t, true)
		a := newPostgresCache(p, keyHashA, "expiry", time.Hour, clock)
		key := strings.Repeat("c", 64)
		if err := a.Set(context.Background(), key, cacheFixtureRecord(key, now)); err != nil {
			t.Fatal(err)
		}
		expired := newPostgresCache(p, keyHashA, "expiry", time.Hour, func() time.Time { return now.Add(2 * time.Hour) })
		_, hit, err := expired.Get(context.Background(), key)
		if err != nil || hit {
			t.Fatal("expired cache served", err)
		}
	})
	t.Run("one cache write cleans at most one hundred expired same-key rows", func(t *testing.T) {
		p, _ := referencePool(t, true)
		execSQL(t, p, "INSERT INTO cache_entries SELECT $1,'expiry',n::text,2,'{}',$2::timestamptz-interval '1 hour' FROM generate_series(1,205)n", keyHashA, now)
		execSQL(t, p, "INSERT INTO cache_entries SELECT $1,'expiry',n::text,2,'{}',$2::timestamptz-interval '1 hour' FROM generate_series(1,15)n", keyHashB, now)
		execSQL(t, p, "INSERT INTO cache_entries VALUES($1,'expiry','live',2,'{}',$2::timestamptz+interval '1 hour')", keyHashA, now)
		a := newPostgresCache(p, keyHashA, "expiry", time.Hour, clock)
		key := strings.Repeat("c", 64)
		if err := a.Set(context.Background(), key, cacheFixtureRecord(key, now)); err != nil {
			t.Fatal(err)
		}
		var expired, live int
		if err := p.QueryRow(context.Background(), "SELECT count(*) FILTER(WHERE expires_at<=$2),count(*) FILTER(WHERE expires_at>$2) FROM cache_entries WHERE api_key_hash=$1", keyHashA, now).Scan(&expired, &live); err != nil {
			t.Fatal(err)
		}
		if expired != 105 || live != 2 || countRows(t, p, "cache_entries", keyHashB) != 15 {
			t.Fatal("cleanup exceeded bound or key scope", expired, live)
		}
	})
	t.Run("corrupt envelope is rejected by core rather than a cache hit", func(t *testing.T) {
		p, _ := referencePool(t, true)
		var providerCalls atomic.Int32
		s, _ := serviceFixture(t, p, func(w http.ResponseWriter, r *http.Request) { providerCalls.Add(1); writeFixtureOK(w) })
		client, err := s.ClientForKey(keyHashA)
		if err != nil {
			t.Fatal(err)
		}
		first, err := client.Call(context.Background(), fixtureCall(hardenllm.CacheModeCache))
		if err != nil {
			t.Fatal(err)
		}
		if !first.Cache.Written || countRows(t, p, "cache_entries", keyHashA) != 1 {
			t.Fatal("valid cache fixture was not persisted before corruption")
		}
		execSQL(t, p, "UPDATE cache_entries SET record=jsonb_set(record,'{schemaVersion}','-1') WHERE api_key_hash=$1", keyHashA)
		second, err := client.Call(context.Background(), fixtureCall(hardenllm.CacheModeCache))
		if err == nil || !strings.Contains(err.Error(), "CACHE_INTEGRITY") || second.Cache.Served || second.Cache.Written || second.Output != nil || second.ResultSource.Kind != hardenllm.ResultSourceNone || providerCalls.Load() != 1 {
			t.Fatal("corrupt cache treated as success", err)
		}
	})
	t.Run("missing and expired drafts are empty revision zero", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		s.now = clock
		missing, err := s.GetDraft(context.Background(), keyHashA, "workspace:user-1")
		if err != nil || missing.Revision != 0 || !sameJSON(missing.Payload, []byte(`{}`)) {
			t.Fatal("missing draft did not return revision zero", err)
		}
		execSQL(t, p, "INSERT INTO drafts VALUES($1,'workspace:user-1',1,'{}',$2,$2::timestamptz-interval '1 second')", keyHashA, now)
		expired, err := s.GetDraft(context.Background(), keyHashA, "workspace:user-1")
		if err != nil || expired.Revision != 0 || !sameJSON(expired.Payload, []byte(`{}`)) {
			t.Fatal("expired draft remains visible", err)
		}
	})
	t.Run("draft create then stale save returns conflict without overwrite", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		input := SaveDraftInput{Revision: 0, Payload: json.RawMessage(`{"input":"original"}`)}
		saved, err := s.SaveDraft(context.Background(), keyHashA, "workspace:user-1", input)
		if err != nil || saved.Revision != 1 {
			t.Fatal("create revision not one", err)
		}
		input.Payload = json.RawMessage(`{"input":"stale"}`)
		_, err = s.SaveDraft(context.Background(), keyHashA, "workspace:user-1", input)
		requireCode(t, err, "revision_conflict")
		kept, err := s.GetDraft(context.Background(), keyHashA, "workspace:user-1")
		if err != nil || kept.Revision != 1 || !sameJSON(kept.Payload, saved.Payload) {
			t.Fatal("stale draft overwrote stored payload", err)
		}
		input.Revision = 1
		updated, err := s.SaveDraft(context.Background(), keyHashA, "workspace:user-1", input)
		if err != nil || updated.Revision != 2 {
			t.Fatal("matching CAS did not advance revision", err)
		}
	})
	t.Run("opaque host draft keys and access keys are independent", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		for _, pair := range [][2]string{{keyHashA, "workspace:user-1"}, {keyHashA, "workspace:user-2"}, {keyHashB, "workspace:user-1"}} {
			payload, _ := json.Marshal(map[string]string{"input": pair[0] + pair[1]})
			saved, err := s.SaveDraft(context.Background(), pair[0], pair[1], SaveDraftInput{Payload: payload})
			if err != nil || saved.Revision != 1 {
				t.Fatal("draft scope collision", err)
			}
		}
		if countRows(t, p, "drafts", keyHashA) != 2 || countRows(t, p, "drafts", keyHashB) != 1 {
			t.Fatal("host/key draft scopes overlap")
		}
	})
	t.Run("draft rejects oversize payload and key", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		oversize, _ := json.Marshal(map[string]string{"input": strings.Repeat("x", MaxSnapshotBytes)})
		_, err := s.SaveDraft(context.Background(), keyHashA, "workspace:user-1", SaveDraftInput{Payload: oversize})
		requireCode(t, err, "draft_invalid")
		_, err = s.SaveDraft(context.Background(), keyHashA, strings.Repeat("x", 129), SaveDraftInput{Payload: json.RawMessage(`{}`)})
		requireCode(t, err, "draft_invalid")
	})
	t.Run("cache-on blocked provider holds no database connection", func(t *testing.T) {
		initial, dsn := referencePool(t, true)
		initial.Close()
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		cfg.MaxConns = 1
		cfg.MinConns = 0
		p, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		started := make(chan struct{})
		release := make(chan struct{})
		s, _ := serviceFixture(t, p, func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-release
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"fixture","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`))
		})
		client, err := s.ClientForKey(keyHashA)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := client.Call(context.Background(), fixtureCall(hardenllm.CacheModeCache))
			done <- err
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("provider did not start")
		}
		acquired := p.Stat().AcquiredConns()
		_, readErr := s.GetDraft(context.Background(), keyHashA, "workspace:user-1")
		close(release)
		callErr := <-done
		if acquired != 0 || readErr != nil || callErr != nil {
			t.Fatal("database connection held during provider", acquired, readErr, callErr)
		}
	})
	t.Run("cache-off ordinary inference acquires zero connections", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		before := p.Stat().AcquireCount()
		client, err := s.ClientForKey(keyHashA)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Call(context.Background(), fixtureCall(hardenllm.CacheModeOff)); err != nil {
			t.Fatal(err)
		}
		if p.Stat().AcquireCount() != before {
			t.Fatal("cache-off call used application storage")
		}
	})
	t.Run("closed storage is an error rather than missing draft", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		p.Close()
		_, err := s.GetDraft(context.Background(), keyHashA, "workspace:user-1")
		requireCode(t, err, "storage_unavailable")
	})
	t.Run("same-key cache Delete preserves foreign scope", func(t *testing.T) {
		p, _ := referencePool(t, true)
		a := newPostgresCache(p, keyHashA, "one", time.Hour, clock)
		b := newPostgresCache(p, keyHashB, "one", time.Hour, clock)
		key := strings.Repeat("c", 64)
		for _, c := range []*PostgresCache{a, b} {
			if err := c.Set(context.Background(), key, cacheFixtureRecord(key, now)); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.Delete(context.Background(), key); err != nil {
			t.Fatal(err)
		}
		_, hit, err := b.Get(context.Background(), key)
		if err != nil || !hit {
			t.Fatal("cache deletion crossed key", err)
		}
	})
}
