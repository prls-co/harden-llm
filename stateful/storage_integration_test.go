//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-424

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"strings"
	"testing"
	"time"
)

func referencePool(t *testing.T, apply bool) (*pgxpool.Pool, string) {
	t.Helper()
	setup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, dsn := integrationtest.PostgresLease(t)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 2
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(setup, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// NewWithConfig is lazy. Establish this fixture's own connection during
	// bounded resource setup, before the unchanged 600ms operation budget.
	if err := pool.Ping(setup); err != nil {
		t.Fatal("leased pool readiness:", err)
	}
	if apply {
		if err := ApplySchema(setup, pool); err != nil {
			t.Fatal("leased schema initialization:", err)
		}
	}
	return pool, dsn
}
func execSQL(t *testing.T, p *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := p.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}
func requireConstraint(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Fatalf("constraint boundary: expected %s, got %v", code, err)
	}
}
func TestReferenceStorage(t *testing.T) {
	t.Parallel()
	t.Run("fresh and idempotent schema has exact tables indexes and version", func(t *testing.T) {
		p, _ := referencePool(t, false)
		if err := ApplySchema(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		var version int
		if err := p.QueryRow(context.Background(), "SELECT version FROM hllm_schema_version WHERE singleton").Scan(&version); err != nil || version != 1 {
			t.Fatalf("fresh schema/version not created: %d %v", version, err)
		}
		for _, name := range []string{"credentials", "profiles", "hidden_presets", "drafts", "cache_entries", "history", "cache_entries_expiry", "drafts_expiry", "history_page"} {
			var exists bool
			if err := p.QueryRow(context.Background(), "SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists); err != nil || !exists {
				t.Errorf("missing schema object %s: %v", name, err)
			}
		}
		if err := ApplySchema(context.Background(), p); err != nil {
			t.Fatal("matching schema must be no-op", err)
		}
	})
	t.Run("version mismatch is typed and changes nothing", func(t *testing.T) {
		p, _ := referencePool(t, false)
		execSQL(t, p, "CREATE TABLE hllm_schema_version(singleton boolean PRIMARY KEY,version integer); INSERT INTO hllm_schema_version VALUES(true,2); CREATE TABLE sentinel(value text); INSERT INTO sentinel VALUES('preserve')")
		err := ApplySchema(context.Background(), p)
		var mismatch *SchemaVersionError
		if !errors.As(err, &mismatch) || mismatch.Actual != 2 {
			t.Fatalf("incompatible version not rejected: %v", err)
		}
		var value string
		if err := p.QueryRow(context.Background(), "SELECT value FROM sentinel").Scan(&value); err != nil || value != "preserve" {
			t.Fatal("mismatch modified data")
		}
		var absent bool
		if err := p.QueryRow(context.Background(), "SELECT to_regclass('profiles') IS NULL").Scan(&absent); err != nil || !absent {
			t.Fatal("mismatch created schema")
		}
	})
	t.Run("partial failure rolls back all newly created schema", func(t *testing.T) {
		p, _ := referencePool(t, false)
		execSQL(t, p, "CREATE TABLE drafts(conflict text)")
		if err := ApplySchema(context.Background(), p); err == nil {
			t.Fatal("conflicting table must reject fresh schema")
		}
		for _, name := range []string{"credentials", "profiles", "hllm_schema_version"} {
			var absent bool
			if err := p.QueryRow(context.Background(), "SELECT to_regclass($1) IS NULL", name).Scan(&absent); err != nil || !absent {
				t.Error("partial schema persisted", name)
			}
		}
	})
	t.Run("every scoped table rejects malformed hash", func(t *testing.T) {
		p, _ := referencePool(t, true)
		queries := map[string]string{
			"credentials":    "INSERT INTO credentials VALUES($1,'c','https://api.example','primary',decode(repeat('00',12),'hex'),'x',1,now(),now())",
			"profiles":       "INSERT INTO profiles VALUES($1,'00000000-0000-4000-8000-000000000001','name',NULL,'{}','{}','deployment','cpa',NULL,now())",
			"hidden_presets": "INSERT INTO hidden_presets VALUES($1,'preset',now())",
			"drafts":         "INSERT INTO drafts VALUES($1,'workspace:fixture',1,'{}',now(),now()+interval '1 day')",
			"cache_entries":  "INSERT INTO cache_entries VALUES($1,'namespace','operation',2,'{}',now()+interval '1 day')",
			"history":        "INSERT INTO history VALUES($1,'00000000-0000-4000-8000-000000000001',1,'succeeded',now(),now(),'{}','{}','{}')",
		}
		for table, q := range queries {
			t.Run(table, func(t *testing.T) {
				for _, hash := range []string{"bad", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63)} {
					_, err := p.Exec(context.Background(), q, hash)
					requireConstraint(t, err, "23514")
				}
			})
		}
	})
	t.Run("composite credential foreign key forbids other key binding", func(t *testing.T) {
		p, _ := referencePool(t, true)
		execSQL(t, p, "INSERT INTO credentials VALUES($1,'foreign','https://api.example','primary',decode(repeat('00',12),'hex'),'x',1,now(),now())", strings.Repeat("b", 64))
		_, err := p.Exec(context.Background(), "INSERT INTO profiles VALUES($1,'00000000-0000-4000-8000-000000000001','name',NULL,'{}','{}','entered',NULL,'foreign',now())", strings.Repeat("a", 64))
		requireConstraint(t, err, "23503")
	})
	t.Run("database enforces exactly one credential source", func(t *testing.T) {
		p, _ := referencePool(t, true)
		hash := strings.Repeat("a", 64)
		execSQL(t, p, "INSERT INTO credentials VALUES($1,'entered','https://api.example','primary',decode(repeat('00',12),'hex'),'x',1,now(),now())", hash)
		for _, values := range []string{"'deployment',NULL,NULL", "'entered',NULL,NULL", "'deployment','cpa','entered'", "'entered','cpa','entered'"} {
			_, err := p.Exec(context.Background(), "INSERT INTO profiles VALUES($1,'00000000-0000-4000-8000-000000000001','name',NULL,'{}','{}',"+values+",now())", hash)
			requireConstraint(t, err, "23514")
		}
	})
	t.Run("mutable revisions must be positive", func(t *testing.T) {
		p, _ := referencePool(t, true)
		hash := strings.Repeat("a", 64)
		for _, rev := range []int64{0, -1} {
			_, err := p.Exec(context.Background(), "INSERT INTO credentials VALUES($1,'entered','https://api.example','primary',decode(repeat('00',12),'hex'),'x',$2,now(),now())", hash, rev)
			requireConstraint(t, err, "23514")
			_, err = p.Exec(context.Background(), "INSERT INTO drafts VALUES($1,'workspace:fixture',$2,'{}',now(),now()+interval '1 day')", hash, rev)
			requireConstraint(t, err, "23514")
		}
	})
	t.Run("history status and terminal timestamp constraints", func(t *testing.T) {
		p, _ := referencePool(t, true)
		for _, status := range []string{"running", "unknown", "canceled", "success"} {
			_, err := p.Exec(context.Background(), "INSERT INTO history VALUES($1,'00000000-0000-4000-8000-000000000001',1,$2,now(),now(),'{}','{}','{}')", strings.Repeat("a", 64), status)
			requireConstraint(t, err, "23514")
		}
		_, err := p.Exec(context.Background(), "INSERT INTO history VALUES($1,'00000000-0000-4000-8000-000000000001',1,'succeeded',now(),now()-interval '1 second','{}','{}','{}')", strings.Repeat("a", 64))
		requireConstraint(t, err, "23514")
	})
	t.Run("no user session key registry tombstone or outbox table", func(t *testing.T) {
		p, _ := referencePool(t, true)
		for _, name := range []string{"users", "sessions", "incoming_keys", "outbox", "history_tombstones"} {
			var absent bool
			if err := p.QueryRow(context.Background(), "SELECT to_regclass($1) IS NULL", name).Scan(&absent); err != nil || !absent {
				t.Fatal("unexpected ownership table", name)
			}
		}
	})
	t.Run("canceled acquisition returns within cap", func(t *testing.T) {
		p, _ := referencePool(t, true)
		one, err := p.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer one.Release()
		two, err := p.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer two.Release()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		called := false
		start := time.Now()
		err = withStorageTx(ctx, p, pgx.TxOptions{}, func(context.Context, pgx.Tx) error { called = true; return nil })
		if !errors.Is(err, context.Canceled) || called || time.Since(start) > 600*time.Millisecond {
			t.Fatal("canceled checkout bypassed acquisition cap", err)
		}
	})
}
