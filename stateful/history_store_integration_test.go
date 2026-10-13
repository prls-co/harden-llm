//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-442

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func setHistoryIdentity(t *testing.T, r HistoryRecord, id string, started time.Time) HistoryRecord {
	t.Helper()
	r.RunID = id
	r.StartedAt = started
	r.FinishedAt = started.Add(time.Second)
	r.Summary.RunID = id
	r.Summary.StartedAt = r.StartedAt
	r.Summary.FinishedAt = r.FinishedAt
	var result map[string]any
	if err := json.Unmarshal(r.Result, &result); err != nil {
		t.Fatal(err)
	}
	result["runId"] = id
	r.Result, _ = json.Marshal(result)
	return r
}
func TestHistoryStore(t *testing.T) {
	t.Parallel()
	t.Run("one insert uses server scope and detail denies foreign or unknown runs", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		s.config.History.Enabled = true
		record := historyFixtureRecord(t)
		inserts, reads := 0, 0
		outcome := s.insertHistory(context.Background(), keyHashA, record, historyWriteOptions{onInsert: func() { inserts++ }, onReadback: func() { reads++ }})
		if outcome.Status != "saved" || inserts != 1 || reads != 0 || countRows(t, p, "history", keyHashA) != 1 {
			t.Fatal("healthy insert not saved exactly once", outcome, inserts, reads)
		}
		got, err := s.GetHistory(context.Background(), keyHashA, record.RunID)
		if err != nil {
			t.Fatal(err)
		}
		wantBytes, _ := json.Marshal(record)
		gotBytes, _ := json.Marshal(got)
		if !sameJSON(wantBytes, gotBytes) {
			var expected, actual map[string]any
			_ = json.Unmarshal(wantBytes, &expected)
			_ = json.Unmarshal(gotBytes, &actual)
			for key, want := range expected {
				if !reflect.DeepEqual(want, actual[key]) {
					wantText, _ := json.Marshal(want)
					gotText, _ := json.Marshal(actual[key])
					if len(wantText) > 600 {
						wantText = wantText[:600]
					}
					if len(gotText) > 600 {
						gotText = gotText[:600]
					}
					t.Errorf("full record field %s differs: expected=%s actual=%s", key, wantText, gotText)
				}
			}
			t.FailNow()
		}
		for _, pair := range [][2]string{{keyHashB, record.RunID}, {keyHashA, "00000000-0000-4000-8000-000000000099"}} {
			_, err = s.GetHistory(context.Background(), pair[0], pair[1])
			requireCode(t, err, "history_not_found")
		}
		typ := reflect.TypeFor[HistoryRecord]()
		for _, name := range []string{"APIKeyHash", "OwnerID", "UserID", "APIKey"} {
			if _, ok := typ.FieldByName(name); ok {
				t.Fatal("caller can set storage authority", name)
			}
		}
		var stored string
		if err := p.QueryRow(context.Background(), "SELECT api_key_hash FROM history WHERE run_id=$1", record.RunID).Scan(&stored); err != nil || stored != keyHashA {
			t.Fatal("server scope not stored", err)
		}
	})
	t.Run("invalid stored schema or payload is not a successful restore", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		s.config.History.Enabled = true
		record := historyFixtureRecord(t)
		if o := s.insertHistory(context.Background(), keyHashA, record, historyWriteOptions{}); o.Status != "saved" {
			t.Fatal(o)
		}
		execSQL(t, p, "UPDATE history SET result='{}' WHERE api_key_hash=$1", keyHashA)
		_, err := s.GetHistory(context.Background(), keyHashA, record.RunID)
		requireCode(t, err, "history_record_invalid")
	})
	t.Run("numbered windows ordering counts and summaries stay scoped", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		s.config.History.Enabled = true
		now := time.Date(2026, 10, 10, 12, 5, 0, 0, time.UTC)
		s.now = func() time.Time { return now }
		base := historyFixtureRecord(t)
		for i := 1; i <= 30; i++ {
			r := setHistoryIdentity(t, base, fmt.Sprintf("00000000-0000-4000-8000-%012d", i), now.Add(-time.Minute))
			if o := s.insertHistory(context.Background(), keyHashA, r, historyWriteOptions{}); o.Status != "saved" {
				t.Fatal(o)
			}
		}
		foreign := setHistoryIdentity(t, base, "00000000-0000-4000-8000-000000000098", now.Add(-time.Minute))
		if o := s.insertHistory(context.Background(), keyHashB, foreign, historyWriteOptions{}); o.Status != "saved" {
			t.Fatal(o)
		}
		first, err := s.ListHistory(context.Background(), keyHashA, HistoryQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if first.Pagination.Page != 1 || first.Pagination.PageSize != 25 || first.Pagination.TotalCount != 30 || len(first.Items) != 25 || !first.Window.Before.Equal(now) || first.Items[0].RunID != "00000000-0000-4000-8000-000000000030" {
			t.Fatal("page window/count/order changed", first.Pagination)
		}
		late := setHistoryIdentity(t, base, "00000000-0000-4000-8000-000000000099", now.Add(time.Second))
		if o := s.insertHistory(context.Background(), keyHashA, late, historyWriteOptions{}); o.Status != "saved" {
			t.Fatal(o)
		}
		second, err := s.ListHistory(context.Background(), keyHashA, HistoryQuery{Before: first.Window.Before, Page: 2, PageSize: 25})
		if err != nil {
			t.Fatal(err)
		}
		if second.Pagination.TotalCount != 30 || len(second.Items) != 5 || second.Items[0].RunID != "00000000-0000-4000-8000-000000000005" {
			t.Fatal("next page admits later or foreign rows", second.Pagination)
		}
		from := now.Add(-30 * time.Second)
		filtered, err := s.ListHistory(context.Background(), keyHashA, HistoryQuery{From: &from, Before: now})
		if err != nil || filtered.Pagination.TotalCount != 0 || len(filtered.Items) != 0 {
			t.Fatal("from bound differs in count/list", err)
		}
		encoded, _ := json.Marshal(first.Items)
		if bytes.Contains(encoded, []byte(`"request"`)) || bytes.Contains(encoded, []byte(`"result"`)) || bytes.Contains(encoded, []byte("Return an object")) {
			t.Fatal("list exposes full payload")
		}
	})
	t.Run("invalid pages sizes and overflowing offsets fail", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		s.config.History.Enabled = true
		for _, q := range []HistoryQuery{{Page: -1}, {Page: 1, PageSize: 1}, {Page: 1, PageSize: 101}, {Page: math.MaxInt, PageSize: 100}} {
			if _, err := s.ListHistory(context.Background(), keyHashA, q); err == nil {
				t.Fatal("invalid pagination accepted", q)
			}
		}
	})
	t.Run("exact seventeen MiB payload survives and one extra byte rejects without truncation", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		s.config.History.Enabled = true
		r := historyFixtureRecord(t)
		var compactRequest bytes.Buffer
		if err := json.Compact(&compactRequest, r.Request); err != nil {
			t.Fatal(err)
		}
		r.Request = compactRequest.Bytes()
		var result map[string]any
		if err := json.Unmarshal(r.Result, &result); err != nil {
			t.Fatal(err)
		}
		result["output"] = ""
		encoded, _ := json.Marshal(result)
		padding := maxHistoryBytes - len(r.Request) - len(encoded)
		if padding < 0 {
			t.Fatal("fixture exceeds bound")
		}
		result["output"] = strings.Repeat("x", padding)
		r.Result, _ = json.Marshal(result)
		if len(r.Request)+len(r.Result) != maxHistoryBytes {
			t.Fatal("fixture not exactly at byte boundary")
		}
		start := time.Now()
		o := s.insertHistory(context.Background(), keyHashA, r, historyWriteOptions{})
		insertDuration := time.Since(start)
		if o.Status != "saved" || insertDuration >= historyInsertTimeout {
			t.Fatal("boundary record insert exceeded budget or failed", o, insertDuration)
		}
		start = time.Now()
		got, err := s.GetHistory(context.Background(), keyHashA, r.RunID)
		readDuration := time.Since(start)
		if err != nil || !sameJSON(got.Result, r.Result) || readDuration >= historyDetailTimeout {
			t.Fatal("boundary detail exceeded budget or changed data", err, readDuration)
		}
		if !bytes.Equal(got.Request, r.Request) || !bytes.Equal(got.Result, r.Result) {
			t.Fatal("opaque payload encoding changed in storage")
		}
		t.Logf("17 MiB insert=%s detail=%s", insertDuration, readDuration)
		r = setHistoryIdentity(t, r, "00000000-0000-4000-8000-000000000002", r.StartedAt)
		result["runId"] = r.RunID
		result["output"] = strings.Repeat("x", padding+1)
		r.Result, _ = json.Marshal(result)
		o = s.insertHistory(context.Background(), keyHashA, r, historyWriteOptions{})
		if o.Status != "not_saved" || countRows(t, p, "history", keyHashA) != 1 {
			t.Fatal("oversize record persisted or truncated", o)
		}
	})
	t.Run("commit outcomes have one insert and bounded single readback", func(t *testing.T) {
		for _, kind := range []string{"confirmed", "unknown-found", "unknown-absent", "unknown-read-failed", "rejected"} {
			t.Run(kind, func(t *testing.T) {
				p, _ := referencePool(t, true)
				s, _ := serviceFixture(t, p, nil)
				s.config.History.Enabled = true
				record := historyFixtureRecord(t)
				inserts, reads := 0, 0
				opts := historyWriteOptions{onInsert: func() { inserts++ }, onReadback: func() { reads++ }}
				want, readCount := "saved", 0
				if kind != "confirmed" {
					opts.commit = func(ctx context.Context, tx pgx.Tx) (string, error) {
						if kind == "unknown-found" {
							if err := tx.Commit(ctx); err != nil {
								return "rejected", err
							}
						} else {
							if err := tx.Rollback(ctx); err != nil {
								return "rejected", err
							}
						}
						if kind == "rejected" {
							return "rejected", errors.New("dsn-password-sentinel")
						}
						return "unknown", errors.New("dsn-password-sentinel")
					}
					want = "unconfirmed"
					readCount = 1
					if kind == "unknown-found" {
						want = "saved"
					}
					if kind == "rejected" {
						want = "not_saved"
						readCount = 0
					}
					if kind == "unknown-read-failed" {
						opts.readbackError = errors.New("dsn-password-sentinel")
					}
				}
				o := s.insertHistory(context.Background(), keyHashA, record, opts)
				if o.Status != want || inserts != 1 || reads != readCount {
					t.Fatal("commit classification/counts wrong", o, inserts, reads)
				}
				encoded, _ := json.Marshal(o)
				if bytes.Contains(encoded, []byte("dsn-password-sentinel")) {
					t.Fatal("diagnostic contains underlying password")
				}
			})
		}
	})
	t.Run("failed list is an explicit history error and usage remains scoped", func(t *testing.T) {
		p, _ := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		s.config.History.Enabled = true
		record := historyFixtureRecord(t)
		for _, hash := range []string{keyHashA, keyHashB} {
			if o := s.insertHistory(context.Background(), hash, record, historyWriteOptions{}); o.Status != "saved" {
				t.Fatal(o)
			}
		}
		usage, err := s.historyUsage(context.Background(), keyHashA)
		if err != nil || usage.Rows != 1 || usage.TableBytes <= 0 {
			t.Fatal("usage not scoped or unavailable disguised as zero", usage, err)
		}
		p.Close()
		_, err = s.ListHistory(context.Background(), keyHashA, HistoryQuery{})
		requireCode(t, err, "history_read_failed")
	})
	t.Run("delayed real commit can appear after the empty readback", func(t *testing.T) {
		p, dsn := referencePool(t, true)
		s, _ := serviceFixture(t, p, nil)
		s.config.History.Enabled = true
		record := historyFixtureRecord(t)
		gate, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer gate.Close(context.Background())
		// A deferred trigger blocks the real PostgreSQL COMMIT, while another
		// connection can execute the single readback against the uncommitted row.
		execSQL(t, p, `CREATE FUNCTION hold_history_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(442451); RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER hold_history_commit AFTER INSERT ON history DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION hold_history_commit()`)
		if _, err := gate.Exec(context.Background(), "SELECT pg_advisory_lock(442451)"); err != nil {
			t.Fatal(err)
		}
		released := false
		defer func() {
			if !released {
				_, _ = gate.Exec(context.Background(), "SELECT pg_advisory_unlock(442451)")
			}
		}()
		commitDone := make(chan error, 1)
		started := make(chan struct{})
		inserts, reads := 0, 0
		opts := historyWriteOptions{commitOwnsTx: true, onInsert: func() { inserts++ }, onReadback: func() { reads++ }, commit: func(ctx context.Context, tx pgx.Tx) (string, error) {
			commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), historyInsertTimeout)
			go func() { defer cancel(); close(started); commitDone <- tx.Commit(commitCtx) }()
			<-started
			return "unknown", errors.New("acknowledgement unavailable")
		}}
		o := s.insertHistory(context.Background(), keyHashA, record, opts)
		if o.Status != "unconfirmed" || inserts != 1 || reads != 1 {
			t.Fatal("delayed commit readback misclassified", o, inserts, reads)
		}
		_, err = s.GetHistory(context.Background(), keyHashA, record.RunID)
		requireCode(t, err, "history_not_found")
		if _, err := gate.Exec(context.Background(), "SELECT pg_advisory_unlock(442451)"); err != nil {
			t.Fatal(err)
		}
		released = true
		select {
		case err := <-commitDone:
			if err != nil {
				t.Fatal("real delayed commit did not finish", err)
			}
		case <-time.After(historyInsertTimeout):
			t.Fatal("delayed commit did not finish within bounded wait")
		}
		if _, err := s.GetHistory(context.Background(), keyHashA, record.RunID); err != nil {
			t.Fatal("committed row did not appear after unconfirmed", err)
		}
		if inserts != 1 || reads != 1 {
			t.Fatal("delayed commit replayed storage")
		}
	})
}
