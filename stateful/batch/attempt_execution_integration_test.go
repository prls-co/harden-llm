//go:build integration

package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-502

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

type droppedObservationStore struct{ ExecutionStore }

func (s droppedObservationStore) StageAttempt(context.Context, string, string, AttemptObservation) (Attempt, error) {
	return Attempt{}, ErrUnavailable
}

func TestIndependentInvocationRealSQLRecoversArtifactAfterLostReturn(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	reservation := permitFixture(ref, 1)
	ctx := context.Background()
	if _, err := s.ReserveAttempt(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	artifacts := &invocationArtifacts{}
	var calls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"answer":"one-independent-result"}`)
	}))
	t.Cleanup(native.Close)
	a := &Activities{Store: droppedObservationStore{s}, Artifacts: artifacts, Encode: func(Generation, hardenllm.Result, time.Time) (json.RawMessage, error) {
		return json.RawMessage(`{"answer":"one-independent-result"}`), nil
	}}
	a.Execute = func(ctx context.Context, _ string, _ hardenllm.Request) (hardenllm.Result, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, native.URL, bytes.NewBufferString(`{"request":"separate"}`))
		if err != nil {
			return hardenllm.Result{}, err
		}
		response, err := native.Client().Do(request)
		if err != nil {
			return hardenllm.Result{}, err
		}
		defer response.Body.Close()
		_, err = io.Copy(io.Discard, response.Body)
		return hardenllm.Result{}, err
	}
	first, err := a.InvokeAttempt(ctx, reservation.PermitID)
	if err != nil || first.Observation == nil || first.Observation.Outcome.Kind != "completed" {
		t.Fatal("original invocation did not produce durable artifact", err, first)
	}
	// Lose the entire activity result, including its nonce/observation. Only the
	// ledger and positively bound artifact survive this recovery boundary.
	first = InvocationResult{}
	loser, err := a.InvokeAttempt(ctx, reservation.PermitID)
	if err != nil || loser.Disposition != "claim_owned_elsewhere" {
		t.Fatal("activity redelivery obtained provider permission", err, loser)
	}
	a.Store = s
	recovered, err := a.ReconcileAttempt(ctx, AttemptReference{PoolID: reservation.PoolID, PermitID: reservation.PermitID})
	if err != nil || recovered.State != "settled" || recovered.Counts.Succeeded != 1 || recovered.StoppedAt == nil || calls.Load() != 1 {
		t.Fatal("metadata/artifact recovery lost or repeated provider execution", err, recovered, calls.Load())
	}
	var result bytes.Buffer
	service := &Service{Store: s, Artifacts: artifacts}
	if err := service.WriteResults(ctx, ref.Scope, ref.JobID, &result); err != nil {
		t.Fatal(err)
	}
	var item Result
	if err := json.Unmarshal(bytes.TrimSpace(result.Bytes()), &item); err != nil {
		t.Fatal(err)
	}
	if string(item.Response) != `{"answer":"one-independent-result"}` {
		t.Fatal("result endpoint exposed artifact envelope instead of OpenAI response", string(item.Response))
	}
}

func TestIndependentInvocationUnknownKeepsPermitUntilPositiveLocalStop(t *testing.T) {
	t.Parallel()
	s, ref := ledgerFixture(t)
	reservation := permitFixture(ref, 1)
	ctx := context.Background()
	if _, err := s.ReserveAttempt(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	nonce := "3a42c6f9-7572-49fc-ae6e-221ea32e52bb"
	if _, err := s.ClaimAttempt(ctx, reservation.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkDispatch(ctx, reservation.PermitID, nonce); err != nil {
		t.Fatal(err)
	}
	// Advance retained execution time, never a sleep or inference retry. This is
	// the no-finalizer evidence left by a lost worker after dispatch intent.
	if _, err := s.Pool.Exec(ctx, "UPDATE batch_attempts SET generation_deadline=now()-interval '31 seconds' WHERE permit_id=$1", reservation.PermitID); err != nil {
		t.Fatal(err)
	}
	a := &Activities{Store: s, Artifacts: &invocationArtifacts{}, Execute: func(context.Context, string, hardenllm.Request) (hardenllm.Result, error) {
		t.Error("reconciliation attempted inference")
		return hardenllm.Result{}, nil
	}}
	settled, err := a.ReconcileAttempt(ctx, AttemptReference{PoolID: reservation.PoolID, PermitID: reservation.PermitID})
	if err != nil || settled.State != "settled" || settled.Observation == nil || settled.Observation.Outcome.Kind != "unconfirmed" || settled.StoppedAt != nil || settled.Counts.Failed != 1 {
		t.Fatal("missing stop evidence invented completion or released capacity", err, settled)
	}
	if err := s.QuiescePool(ctx, reservation.PoolID, reservation.Generation); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePool(ctx, reservation.PoolID, 2, 3); !errors.Is(err, ErrGenerationFenced) {
		t.Fatal("unknown transport lifetime allowed new generation", err)
	}
	stopped, err := s.MarkLocalStop(ctx, reservation.PermitID, nonce, time.Now())
	if err != nil || stopped.SettlementRevision != 2 || stopped.Observation.Outcome.Kind != "unconfirmed" {
		t.Fatal("local stop replaced terminal outcome", err, stopped)
	}
	observed, err := a.ReconcileAttempt(ctx, AttemptReference{PoolID: reservation.PoolID, PermitID: reservation.PermitID})
	if err != nil || observed.Observation.Outcome.Kind != "unconfirmed" || observed.SettlementRevision != 2 {
		t.Fatal("later reconciliation changed immutable unknown result", err, observed)
	}
	if err := s.AdvancePool(ctx, reservation.PoolID, 2, 3); err != nil {
		t.Fatal("positive local stop did not unblock generation handoff", err)
	}
}
