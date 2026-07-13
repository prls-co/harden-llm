// Package postgres owns the dedicated Harden-LLM application database.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const migrationAdvisoryLock int64 = 0x484c4c4d

//go:embed migrations/*.sql
var migrations embed.FS

// ErrNotFound is returned for an absent or non-owned record.
var ErrNotFound = errors.New("postgres: record not found")

// Store is the sole owner of application SQL and transactions.
type Store struct {
	pool *pgxpool.Pool
}

type openConfig struct {
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
}

type OpenOption func(*openConfig) error

// WithTelemetry records bounded query spans and persistence metrics through
// caller-owned providers without initializing global exporters.
func WithTelemetry(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) OpenOption {
	return func(config *openConfig) error {
		config.tracerProvider = tracerProvider
		config.meterProvider = meterProvider
		return nil
	}
}

// Open validates a connection pool and returns an application Store.
func Open(ctx context.Context, databaseURL string, options ...OpenOption) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("postgres: database URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse database URL: %w", err)
	}
	var openOptions openConfig
	for _, option := range options {
		if option == nil {
			return nil, errors.New("postgres: open option is nil")
		}
		if err := option(&openOptions); err != nil {
			return nil, err
		}
	}
	queryTelemetry, err := NewQueryTelemetry(openOptions.tracerProvider, openOptions.meterProvider)
	if err != nil {
		return nil, fmt.Errorf("postgres: initialize telemetry: %w", err)
	}
	config.ConnConfig.Tracer = queryTelemetry
	config.ConnConfig.RuntimeParams["application_name"] = "harden-llm-gateway"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("postgres: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the application connection pool.
func (store *Store) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}

// Ping verifies database reachability.
func (store *Store) Ping(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return errors.New("postgres: store is not initialized")
	}
	return store.pool.Ping(ctx)
}

// Ready verifies reachability and an exact match with this binary's embedded
// migration set. A database that is behind or ahead is not safe to serve.
func (store *Store) Ready(ctx context.Context) error {
	if err := store.Ping(ctx); err != nil {
		return err
	}
	expected, err := migrationEntries()
	if err != nil {
		return err
	}
	applied, err := store.AppliedMigrations(ctx)
	if err != nil {
		return err
	}
	if len(applied) != len(expected) {
		return errors.New("postgres: migration state is not current")
	}
	for index := range expected {
		if applied[index] != expected[index].version {
			return errors.New("postgres: migration state is not current")
		}
	}
	return nil
}

// Migrate applies embedded migrations once under a session-scoped advisory lock.
func (store *Store) Migrate(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return errors.New("postgres: store is not initialized")
	}
	connection, err := store.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: acquire migration connection: %w", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationAdvisoryLock); err != nil {
		return fmt.Errorf("postgres: acquire migration lock: %w", err)
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = connection.Exec(unlockContext, `SELECT pg_advisory_unlock($1)`, migrationAdvisoryLock)
	}()
	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version bigint PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("postgres: initialize migration table: %w", err)
	}

	entries, err := migrationEntries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		var applied bool
		if err := connection.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, entry.version).Scan(&applied); err != nil {
			return fmt.Errorf("postgres: inspect migration %d: %w", entry.version, err)
		}
		if applied {
			continue
		}
		transaction, err := connection.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return fmt.Errorf("postgres: begin migration %d: %w", entry.version, err)
		}
		if _, err := transaction.Exec(ctx, entry.sql); err != nil {
			_ = transaction.Rollback(ctx)
			return fmt.Errorf("postgres: apply migration %d: %w", entry.version, err)
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, entry.version); err != nil {
			_ = transaction.Rollback(ctx)
			return fmt.Errorf("postgres: record migration %d: %w", entry.version, err)
		}
		if err := transaction.Commit(ctx); err != nil {
			return fmt.Errorf("postgres: commit migration %d: %w", entry.version, err)
		}
	}
	return nil
}

// AppliedMigrations returns the ordered committed migration versions.
func (store *Store) AppliedMigrations(ctx context.Context) ([]int64, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("postgres: store is not initialized")
	}
	rows, err := store.pool.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list migrations: %w", err)
	}
	defer rows.Close()
	var versions []int64
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("postgres: scan migration: %w", err)
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

type migration struct {
	version int64
	sql     string
}

func migrationEntries() ([]migration, error) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("postgres: read migrations: %w", err)
	}
	result := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("postgres: migration %q has no numeric prefix", entry.Name())
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("postgres: migration %q has invalid version", entry.Name())
		}
		contents, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("postgres: read migration %q: %w", entry.Name(), err)
		}
		result = append(result, migration{version: version, sql: string(contents)})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].version < result[right].version })
	for index := 1; index < len(result); index++ {
		if result[index-1].version == result[index].version {
			return nil, fmt.Errorf("postgres: duplicate migration version %d", result[index].version)
		}
	}
	return result, nil
}

func migrationSource() []byte {
	entries, err := migrationEntries()
	if err != nil {
		return nil
	}
	var result strings.Builder
	for _, entry := range entries {
		result.WriteString(entry.sql)
	}
	return []byte(result.String())
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
