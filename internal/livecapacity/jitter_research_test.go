package livecapacity

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-483
// Research-only scheduling wrapper; production controller has no jitter field.

import (
	"fmt"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/stateful/batch"
)

func TestRecoveryJitterTradeoffUnderSharedAndIndependentAdmission(t *testing.T) {
	for _, model := range []string{"fixed_window", "token_bucket"} {
		for _, shared := range []bool{false, true} {
			for _, jitter := range []bool{false, true} {
				for seed := 1; seed <= 8; seed++ {
					t.Run(fmt.Sprintf("%s/shared=%t/jitter=%t/seed=%d", model, shared, jitter, seed), func(t *testing.T) {
						at := time.Unix(1000, 0)
						p := batch.DefaultPolicy()
						actors, items := StrategyWorkload(p, 1000, true, shared)
						e, err := NewStrategyEngine(at, actors, items, 10000000)
						if err != nil {
							t.Fatal(err)
						}
						var pending []NativeMeasurement
						epoch, now, last := at, at, at
						used := 0
						tokens := 200.0
						// Stable per-actor/per-probe choices make comparisons reproducible.
						probeSeq := make([]int, len(e.Controllers))
						jittered := 0
						for !e.Done() && now.Before(at.Add(30*time.Minute)) {
							rest := pending[:0]
							for _, m := range pending {
								if m.Finished.After(now) {
									rest = append(rest, m)
									continue
								}
								item := e.Items[m.ID]
								ci := e.control[item.Actor]
								before := e.Controllers[ci].NextEligible
								e.Finish(m)
								c := &e.Controllers[ci]
								// Jitter one scheduled recovery, never every refusal in a burst.
								if jitter && c.Cooling && c.NextEligible.After(before) {
									probeSeq[ci]++
									padding := time.Duration((seed*7919+ci*104729+probeSeq[ci]*15485863)%1001) * time.Millisecond
									floor := c.NextEligible
									c.NextEligible = c.NextEligible.Add(padding)
									if c.NextEligible.Before(floor) {
										t.Fatal("jitter shortened provider wait")
									}
									jittered++
								}
							}
							pending = rest
							for {
								v, ok := e.Take(now)
								if !ok {
									break
								}
								if !now.Before(epoch.Add(61 * time.Second)) {
									epoch = now
									used = 0
								}
								tokens = min(200, tokens+now.Sub(last).Seconds()*200/61)
								last = now
								accept := used < 200
								wait := epoch.Add(61 * time.Second).Sub(now)
								if model == "token_bucket" {
									accept = tokens >= 1
									wait = time.Second
								}
								m := NativeMeasurement{ID: v.ID, Attempt: v.Attempt, Started: now, Finished: now.Add(2 * time.Second), Kind: "completed", InputTokens: 97, OutputTokens: 38}
								if accept {
									used++
									tokens--
								} else {
									m.Kind = "rejected"
									m.InputTokens = 0
									m.OutputTokens = 0
									m.RetryAfterMS = max(1, wait.Milliseconds())
									m.Finished = now.Add(time.Millisecond)
								}
								pending = append(pending, m)
							}
							now = now.Add(10 * time.Millisecond)
						}
						s := e.Snapshot(now)
						if !e.Done() || s.StopReason != "" || s.Actors[0].Completed != 1000 || s.Actors[1].Completed != 200 {
							t.Fatalf("jitter lost work: %+v", s)
						}
						t.Logf("research model=%s shared=%t jitter=%t seed=%d foreground_ms=%d all_ms=%d refusals=%d jittered=%d", model, shared, jitter, seed, s.Actors[0].ReadyMs, s.ElapsedMs, s.Actors[0].Rejected+s.Actors[1].Rejected, jittered)
					})
				}
			}
		}
	}
}
