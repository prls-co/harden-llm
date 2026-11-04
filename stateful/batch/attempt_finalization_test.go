package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-500

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

type finalizationStore struct {
	ExecutionStore
	attempt     Attempt
	settlements int
	settleErr   error
}

func (s *finalizationStore) ReadAttempts(context.Context, string, []string) ([]Attempt, error) {
	return []Attempt{s.attempt}, nil
}
func (s *finalizationStore) SettleAttempt(_ context.Context, _, _ string, o AttemptObservation) (Attempt, error) {
	s.settlements++
	if s.settleErr != nil {
		return Attempt{}, s.settleErr
	}
	s.attempt.State, s.attempt.Observation = "settled", &o
	s.attempt.SettlementRevision = 1
	if o.LocalStopped {
		s.attempt.StoppedAt = &o.ObservedAt
	}
	return s.attempt, nil
}

func TestIndependentFinalizationNotifiesOnlyAfterPositiveSettlement(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"known", "readback", "settlement-unconfirmed", "signal-unconfirmed", "not-settled"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				now := time.Now()
				observation := AttemptObservation{Outcome: Outcome{Kind: "completed", ArtifactID: "attempt.json"}, NativeEntered: true, LocalStopped: true, ObservedAt: now}
				store := &finalizationStore{attempt: Attempt{Reservation: Reservation{Kind: "job", PoolID: "pool", PermitID: "permit", Deadline: now.Add(time.Minute)}, ClaimID: "owner", State: "dispatching"}}
				known := KnownSettlement{Reference: AttemptReference{PoolID: "pool", PermitID: "permit"}, ClaimID: "owner", Observation: observation}
				if mode == "readback" {
					store.attempt.State, store.attempt.Observation, store.attempt.SettlementRevision = "settled", &observation, 1
					store.attempt.StoppedAt = &now
					known.ClaimID = ""
				}
				if mode == "not-settled" {
					known.ClaimID = ""
				}
				if mode == "settlement-unconfirmed" {
					store.settleErr = ErrClaimUnconfirmed
				}
				notices := 0
				a := &Activities{Store: store, PoolID: "pool", Temporal: starterTemporal{signal: func(ctx context.Context, id, run, name string, arg interface{}) error {
					notices++
					n := arg.(SettlementNotice)
					if store.attempt.State != "settled" || n.Revision != store.attempt.SettlementRevision || id != "hllm-cpa-pool-v1/pool" || run != "" || name != settlementSignal {
						t.Error("notification preceded positive settlement or changed identity", store.attempt, n)
					}
					if mode == "signal-unconfirmed" {
						return errors.New("synthetic signal acknowledgement lost")
					}
					return nil
				}}, Execute: func(context.Context, string, hardenllm.Request) (hardenllm.Result, error) {
					t.Error("metadata finalization entered provider execution")
					return hardenllm.Result{}, nil
				}}
				out, err := a.FinalizeAttempt(context.Background(), known)
				if mode == "settlement-unconfirmed" {
					if !errors.Is(err, ErrClaimUnconfirmed) || notices != 0 || store.settlements != 1 {
						t.Fatal("unknown settlement emitted a wake or retried", err, notices, store.settlements)
					}
				} else if mode == "not-settled" {
					if err != nil || out.State != "dispatching" || notices != 0 || store.settlements != 0 {
						t.Fatal("nonterminal readback invented a settlement", err, out, notices)
					}
				} else if err != nil || out.State != "settled" || notices != 1 || (mode != "readback" && store.settlements != 1) {
					t.Fatal("wake failure lost a positive settlement or retried metadata", err, out, notices, store.settlements)
				}
			})
		})
	}
}
