//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-463

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConcurrentDraftCAS(t *testing.T) {
	t.Parallel()
	pool, dsn := referencePool(t, true)
	one, _ := serviceFixture(t, pool, nil)
	two, err := NewService(one.config)
	if err != nil {
		t.Fatal(err)
	}
	original := SaveDraftInput{Payload: json.RawMessage(`{"userPrompt":"original","nested":{"keep":false}}`)}
	saved, err := one.SaveDraft(context.Background(), keyHashA, "workspace:cas", original)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := one.SaveDraft(context.Background(), keyHashB, "workspace:cas", original)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// A third, test-owned connection holds this row so both pool writers really
	// overlap at PostgreSQL's lock boundary; it never touches another database.
	gate, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close(context.Background())
	tx, err := gate.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SELECT 1 FROM drafts WHERE api_key_hash=$1 AND draft_key=$2 FOR UPDATE", keyHashA, "workspace:cas"); err != nil {
		t.Fatal(err)
	}
	type reply struct {
		input SaveDraftInput
		value Draft
		err   error
	}
	done := make(chan reply, 2)
	for i, service := range []*Service{one, two} {
		input := SaveDraftInput{Revision: saved.Revision, Payload: json.RawMessage([]string{`{"userPrompt":"writer-a","nested":{"keep":false}}`, `{"userPrompt":"writer-b","nested":{"keep":0}}`}[i])}
		go func() {
			value, err := service.SaveDraft(ctx, keyHashA, "workspace:cas", input)
			done <- reply{input, value, err}
		}()
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		// PostgreSQL retains the first activity snapshot within this lock-owning
		// transaction. Refresh it before observing newly blocked writers.
		if _, err = tx.Exec(ctx, "SELECT pg_stat_clear_snapshot()"); err != nil {
			t.Fatal(err)
		}
		var waiting int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND pid<>pg_backend_pid()").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("two writers did not reach the owned SQL lock")
		case <-ticker.C:
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	successes, conflicts := 0, 0
	var winner reply
	for range 2 {
		select {
		case result := <-done:
			if result.err == nil {
				successes++
				winner = result
			} else {
				requireCode(t, result.err, "revision_conflict")
				conflicts++
			}
		case <-ctx.Done():
			t.Fatal("CAS writers did not terminate")
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS outcomes: success=%d conflicts=%d", successes, conflicts)
	}
	actual, err := one.GetDraft(ctx, keyHashA, "workspace:cas")
	if err != nil || actual.Revision != saved.Revision+1 || !sameJSON(actual.Payload, winner.input.Payload) {
		t.Fatalf("winning full state differs: revision=%d err=%v", actual.Revision, err)
	}
	untouched, err := two.GetDraft(ctx, keyHashB, "workspace:cas")
	if err != nil || !sameJSON(untouched.Payload, foreign.Payload) || untouched.Revision != foreign.Revision {
		t.Fatal("foreign scope changed", err)
	}
}

func TestCredentialCASAfterConcurrentProbes(t *testing.T) {
	t.Parallel()
	pool, _ := referencePool(t, true)
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	one, input := serviceFixture(t, pool, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Authorization"), "race-candidate-") {
			ready <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		writeFixtureOK(w)
	})
	two, err := NewService(one.config)
	if err != nil {
		t.Fatal(err)
	}
	input = enteredSnapshot(t, input, "entered")
	if _, err = one.SaveProfile(context.Background(), keyHashA, input); err != nil {
		t.Fatal(err)
	}
	if _, err = one.SaveProfile(context.Background(), keyHashB, input); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type reply struct {
		candidate string
		profile   ProfileView
		err       error
	}
	done := make(chan reply, 2)
	for i, service := range []*Service{one, two} {
		candidate := input
		credential := *input.Credential
		candidate.Credential = &credential
		candidate.Credential.Revision = 1
		candidate.Credential.Payload.APIKey = []string{"race-candidate-a", "race-candidate-b"}[i]
		go func() {
			profile, err := service.SaveProfile(ctx, keyHashA, candidate)
			done <- reply{candidate.Credential.Payload.APIKey, profile, err}
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("concurrent candidates did not both complete revision read before probe")
		}
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("provider probes hold SQL connections")
	}
	close(release)
	successes, conflicts := 0, 0
	var winner reply
	for range 2 {
		select {
		case result := <-done:
			if result.err == nil {
				successes++
				winner = result
			} else {
				requireCode(t, result.err, "revision_conflict")
				conflicts++
			}
		case <-ctx.Done():
			t.Fatal("credential writers did not terminate")
		}
	}
	if successes != 1 || conflicts != 1 || countRows(t, pool, "profiles", keyHashA) != 2 || countRows(t, pool, "credentials", keyHashA) != 1 {
		t.Fatalf("candidate transaction outcomes success=%d conflicts=%d", successes, conflicts)
	}
	err = withStorageTx(ctx, pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		for _, hash := range []string{keyHashA, keyHashB} {
			record, err := readCredentialTx(ctx, tx, hash, "entered")
			if err != nil {
				return err
			}
			if record == nil {
				t.Fatal("credential missing")
			}
			plain, err := one.config.Vault.Open(record.Encrypted, record.Binding)
			if err != nil {
				return err
			}
			want, revision := input.Credential.Payload.APIKey, int64(1)
			if hash == keyHashA {
				want = winner.candidate
				revision = 2
			}
			if plain.APIKey != want || record.Revision != revision {
				t.Fatal("winning ciphertext or foreign scope changed")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if winner.profile.CredentialStatus.Revision != 2 || countRows(t, pool, "profiles", keyHashB) != 1 {
		t.Fatal("winner revision or foreign snapshot changed")
	}
}
