//go:build integration

package postgres

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-230 TEST-232

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/integrationtest"
)

func TestNumberedHistoryMeasurement(t *testing.T) {
	_, dsn := integrationtest.PostgresLease(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	evidence := paginationMeasurementEvidence{
		GeneratedAt:     time.Now().UTC(),
		GoVersion:       runtime.Version(),
		GOOS:            runtime.GOOS,
		GOARCH:          runtime.GOARCH,
		CacheConditions: "first call after ANALYZE plus three warm calls; shared-buffer eviction/cold-cache measurement was not available through the pooled runner",
	}
	if hostname, err := os.Hostname(); err == nil {
		evidence.Hostname = hostname
	}
	if err := store.pool.QueryRow(ctx, `SELECT current_setting('server_version')`).Scan(&evidence.PostgresVersion); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
		evidence.GitSHA = strings.TrimSpace(string(output))
	}

	for _, cardinality := range []int{1_000, 10_000, 100_000} {
		ownerID := fmt.Sprintf("pagination-benchmark-%d", cardinality)
		if err := seedPaginationOwner(ctx, store, ownerID, cardinality); err != nil {
			t.Fatalf("seed %d rows: %v", cardinality, err)
		}
		if _, err := store.pool.Exec(ctx, `ANALYZE llm_runs`); err != nil {
			t.Fatalf("analyze %d rows: %v", cardinality, err)
		}

		measurement := paginationDatasetMeasurement{OwnerID: ownerID, Cardinality: cardinality}
		for _, pageSize := range []int{10, 25, 50, 100} {
			totalPages := (cardinality + pageSize - 1) / pageSize
			positions := []struct {
				name string
				page int64
			}{
				{name: "first", page: 1},
				{name: "middle", page: int64((totalPages + 1) / 2)},
				{name: "last", page: int64(totalPages)},
			}
			for _, position := range positions {
				caseMeasurement, err := measurePaginationCase(ctx, store, ownerID, cardinality, pageSize, position.name, position.page)
				if err != nil {
					t.Fatalf("measure rows=%d size=%d position=%s: %v", cardinality, pageSize, position.name, err)
				}
				measurement.Cases = append(measurement.Cases, caseMeasurement)
			}
		}
		evidence.Datasets = append(evidence.Datasets, measurement)
	}

	if err := verifyCancellationReleasesConnection(ctx, store, evidence.Datasets[0].OwnerID); err != nil {
		t.Fatal(err)
	}
	evidence.Snapshot = verifyConcurrentSnapshot(ctx, t, store)

	path := filepath.Join(repositoryRootForEvidence(t), "plans", "evidence", "harden-llm", "reusable-pagination-test-232.json")
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("pagination measurement evidence: %s", path)
}

type paginationMeasurementEvidence struct {
	GeneratedAt     time.Time                      `json:"generatedAt"`
	GitSHA          string                         `json:"gitSHA,omitempty"`
	Hostname        string                         `json:"hostname,omitempty"`
	GoVersion       string                         `json:"goVersion"`
	GOOS            string                         `json:"goos"`
	GOARCH          string                         `json:"goarch"`
	PostgresVersion string                         `json:"postgresVersion"`
	CacheConditions string                         `json:"cacheConditions"`
	Datasets        []paginationDatasetMeasurement `json:"datasets"`
	Snapshot        paginationSnapshotMeasurement  `json:"snapshot"`
}

type paginationDatasetMeasurement struct {
	OwnerID     string                  `json:"ownerId"`
	Cardinality int                     `json:"cardinality"`
	Cases       []paginationCaseMeasure `json:"cases"`
}

type paginationCaseMeasure struct {
	PageSize         int              `json:"pageSize"`
	Position         string           `json:"position"`
	RequestedPage    int64            `json:"requestedPage"`
	EffectivePage    int64            `json:"effectivePage"`
	TotalPages       int64            `json:"totalPages"`
	TotalCount       int64            `json:"totalCount"`
	ItemCount        int              `json:"itemCount"`
	ResponseBytes    int              `json:"responseBytes"`
	FirstCallMicros  int64            `json:"firstCallMicros"`
	WarmCallMicros   []int64          `json:"warmCallMicros"`
	CountPlan        json.RawMessage  `json:"countPlan"`
	PagePlan         json.RawMessage  `json:"pagePlan"`
	CountPlanSummary queryPlanSummary `json:"countPlanSummary"`
	PagePlanSummary  queryPlanSummary `json:"pagePlanSummary"`
}

type queryPlanSummary struct {
	ActualRows       float64 `json:"actualRows"`
	RowsRemoved      float64 `json:"rowsRemovedByFilter"`
	SharedHitBlocks  float64 `json:"sharedHitBlocks"`
	SharedReadBlocks float64 `json:"sharedReadBlocks"`
}

type paginationSnapshotMeasurement struct {
	Checks        int    `json:"checks"`
	MismatchCount int    `json:"mismatchCount"`
	WriterOps     int    `json:"writerOps"`
	Result        string `json:"result"`
}

func seedPaginationOwner(ctx context.Context, store *Store, ownerID string, cardinality int) error {
	now := time.Now().UTC()
	if err := store.CreateUser(ctx, User{
		ID: ownerID, Email: ownerID + "@example.test", PasswordHash: "$argon2id$benchmark",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return err
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO llm_runs
			(owner_id, run_id, profile_id, trace_id, status, request, result, started_at, completed_at)
		SELECT $1,
			'run-' || lpad(value::text, 8, '0'),
			'benchmark-profile',
			'trace-' || lpad(value::text, 8, '0'),
			'succeeded', '{}'::jsonb, '{}'::jsonb,
			$2::timestamptz - (value * interval '1 second'),
			$2::timestamptz - (value * interval '1 second')
		FROM generate_series(1, $3::int) AS values(value)`, ownerID, now, cardinality)
	return err
}

func measurePaginationCase(ctx context.Context, store *Store, ownerID string, cardinality, pageSize int, position string, page int64) (paginationCaseMeasure, error) {
	first, err := timedRunsPage(ctx, store, ownerID, page, pageSize)
	if err != nil {
		return paginationCaseMeasure{}, err
	}
	if first.Page != page || first.TotalCount != int64(cardinality) || len(first.Records) != pageSize {
		return paginationCaseMeasure{}, fmt.Errorf("unexpected first result: page=%d count=%d records=%d", first.Page, first.TotalCount, len(first.Records))
	}

	warm := make([]int64, 3)
	for index := range warm {
		result, callErr := timedRunsPage(ctx, store, ownerID, page, pageSize)
		if callErr != nil {
			return paginationCaseMeasure{}, callErr
		}
		if result.Page != first.Page || result.TotalCount != first.TotalCount || len(result.Records) != len(first.Records) {
			return paginationCaseMeasure{}, errorsForMeasurement(first, result)
		}
		warm[index] = result.Elapsed
	}

	countPlan, countSummary, err := explainPlan(ctx, store, `SELECT COUNT(*) FROM llm_runs WHERE owner_id = $1`, ownerID)
	if err != nil {
		return paginationCaseMeasure{}, err
	}
	offset := (page - 1) * int64(pageSize)
	pagePlan, pageSummary, err := explainPlan(ctx, store, `
		SELECT owner_id, run_id, profile_id, trace_id, status, request, result, started_at, completed_at
		FROM llm_runs WHERE owner_id = $1
		ORDER BY started_at DESC, run_id DESC LIMIT $2 OFFSET $3`, ownerID, pageSize, offset)
	if err != nil {
		return paginationCaseMeasure{}, err
	}

	responseBytes, err := json.Marshal(map[string]any{
		"items":      first.Records,
		"pagination": map[string]any{"page": first.Page, "pageSize": first.PageSize, "totalCount": first.TotalCount},
	})
	if err != nil {
		return paginationCaseMeasure{}, err
	}
	return paginationCaseMeasure{
		PageSize: pageSize, Position: position, RequestedPage: page, EffectivePage: first.Page,
		TotalPages: int64((cardinality + pageSize - 1) / pageSize), TotalCount: first.TotalCount,
		ItemCount: len(first.Records), ResponseBytes: len(responseBytes), FirstCallMicros: first.Elapsed,
		WarmCallMicros: warm, CountPlan: countPlan, PagePlan: pagePlan,
		CountPlanSummary: countSummary, PagePlanSummary: pageSummary,
	}, nil
}

type timedPageResult struct {
	NumberedRunsPage
	Elapsed int64
}

func timedRunsPage(ctx context.Context, store *Store, ownerID string, page int64, pageSize int) (timedPageResult, error) {
	started := time.Now()
	result, err := store.RunsPage(ctx, ownerID, page, pageSize)
	return timedPageResult{NumberedRunsPage: result, Elapsed: time.Since(started).Microseconds()}, err
}

func explainPlan(ctx context.Context, store *Store, query string, arguments ...any) (json.RawMessage, queryPlanSummary, error) {
	var encoded []byte
	if err := store.pool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+query, arguments...).Scan(&encoded); err != nil {
		return nil, queryPlanSummary{}, err
	}
	var plan any
	if err := json.Unmarshal(encoded, &plan); err != nil {
		return nil, queryPlanSummary{}, err
	}
	return json.RawMessage(encoded), summarizeQueryPlan(plan), nil
}

func summarizeQueryPlan(value any) queryPlanSummary {
	var summary queryPlanSummary
	var visit func(any)
	visit = func(node any) {
		object, ok := node.(map[string]any)
		if !ok {
			if values, ok := node.([]any); ok {
				for _, child := range values {
					visit(child)
				}
			}
			return
		}
		for key, target := range map[string]*float64{
			"Actual Rows":            &summary.ActualRows,
			"Rows Removed by Filter": &summary.RowsRemoved,
			"Shared Hit Blocks":      &summary.SharedHitBlocks,
			"Shared Read Blocks":     &summary.SharedReadBlocks,
		} {
			if number, ok := object[key].(float64); ok {
				*target += number
			}
		}
		for _, child := range object {
			visit(child)
		}
	}
	visit(value)
	return summary
}

func errorsForMeasurement(want, got timedPageResult) error {
	return fmt.Errorf("warm result changed: want page=%d count=%d records=%d, got page=%d count=%d records=%d", want.Page, want.TotalCount, len(want.Records), got.Page, got.TotalCount, len(got.Records))
}

func verifyCancellationReleasesConnection(ctx context.Context, store *Store, ownerID string) error {
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.RunsPage(canceled, ownerID, 1, 10); err == nil {
		return fmt.Errorf("canceled numbered history read succeeded")
	}
	stat := store.pool.Stat()
	if stat.AcquiredConns() != 0 {
		return fmt.Errorf("canceled numbered history read retained %d acquired connections", stat.AcquiredConns())
	}
	return nil
}

func verifyConcurrentSnapshot(ctx context.Context, t *testing.T, store *Store) paginationSnapshotMeasurement {
	t.Helper()
	ownerID := "pagination-snapshot-owner"
	if err := seedPaginationOwner(ctx, store, ownerID, 25); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `ANALYZE llm_runs`); err != nil {
		t.Fatal(err)
	}

	const operations = 200
	writerErrors := make(chan error, 1)
	go func() {
		for index := 0; index < operations; index++ {
			runID := fmt.Sprintf("snapshot-new-%03d", index)
			_, err := store.pool.Exec(ctx, `
				INSERT INTO llm_runs
					(owner_id, run_id, profile_id, trace_id, status, request, result, started_at, completed_at)
				VALUES ($1, $2, 'snapshot-profile', $3, 'succeeded', '{}'::jsonb, '{}'::jsonb, now(), now())`, ownerID, runID, runID)
			if err != nil {
				writerErrors <- err
				return
			}
			if _, err := store.pool.Exec(ctx, `DELETE FROM llm_runs WHERE owner_id = $1 AND run_id = $2`, ownerID, runID); err != nil {
				writerErrors <- err
				return
			}
		}
		close(writerErrors)
	}()

	measurement := paginationSnapshotMeasurement{Checks: operations, WriterOps: operations, Result: "pass"}
	for index := 0; index < operations; index++ {
		result, err := store.RunsPage(ctx, ownerID, 1, 26)
		if err != nil {
			t.Fatal(err)
		}
		want := result.TotalCount
		if want > 26 {
			want = 26
		}
		if int64(len(result.Records)) != want {
			measurement.MismatchCount++
		}
	}
	if err, ok := <-writerErrors; ok && err != nil {
		t.Fatal(err)
	}
	if measurement.MismatchCount != 0 {
		measurement.Result = "fail"
		t.Fatalf("numbered history snapshot mismatches: %d", measurement.MismatchCount)
	}
	return measurement
}

func repositoryRootForEvidence(t testing.TB) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(workingDirectory, "go.mod")); err == nil {
			return workingDirectory
		}
		parent := filepath.Dir(workingDirectory)
		if parent == workingDirectory {
			t.Fatal("repository root not found")
		}
		workingDirectory = parent
	}
}
