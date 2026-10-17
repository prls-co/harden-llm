package livecapacity

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-483

import (
	"context"
	"fmt"
	"io"
	"sort"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/stateful/batch"
)

func TestStrategyControllerUnderChangingSharedQuotaModels(t *testing.T) {
	for _, model := range []string{"fixed_window", "sliding_window", "token_bucket", "concurrency"} {
		for _, shared := range []bool{false, true} {
			for _, window := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/shared=%t/window=%t", model, shared, window), func(t *testing.T) {
					at := time.Unix(1000, 0)
					p := batch.DefaultPolicy()
					if !window {
						p.WindowRequests = 0
					}
					actors, items := StrategyWorkload(p, 1000, true, shared)
					e, err := NewStrategyEngine(at, actors, items, 10000000)
					if err != nil {
						t.Fatal(err)
					}
					var pending []NativeMeasurement
					var starts []time.Time
					fixed := 0
					fixedEpoch := at
					tokens := 200.0
					last := at
					providerActive := 0
					peak := 0
					now := at
					for !e.Done() && now.Before(at.Add(30*time.Minute)) {
						var remaining []NativeMeasurement
						for _, m := range pending {
							if !m.Finished.After(now) {
								if m.Kind == "completed" {
									providerActive--
								}
								e.Finish(m)
							} else {
								remaining = append(remaining, m)
							}
						}
						pending = remaining
						for {
							v, ok := e.Take(now)
							if !ok {
								break
							}
							for len(starts) > 0 && !starts[0].Add(61*time.Second).After(now) {
								starts = starts[1:]
							}
							if !now.Before(fixedEpoch.Add(61 * time.Second)) {
								fixedEpoch = now
								fixed = 0
							}
							tokens = min(200, tokens+now.Sub(last).Seconds()*200/61)
							last = now
							accept := true
							wait := time.Second
							switch model {
							case "fixed_window":
								accept = fixed < 200
								wait = fixedEpoch.Add(61 * time.Second).Sub(now)
							case "sliding_window":
								accept = len(starts) < 200
								if !accept {
									wait = starts[0].Add(61 * time.Second).Sub(now)
								}
							case "token_bucket":
								accept = tokens >= 1
								wait = time.Second
							case "concurrency":
								accept = providerActive < 72
								wait = time.Second
							}
							m := NativeMeasurement{ID: v.ID, Attempt: v.Attempt, Started: now, Finished: now.Add(2 * time.Second), Kind: "completed", InputTokens: 97, OutputTokens: 38}
							if accept {
								fixed++
								starts = append(starts, now)
								tokens--
								providerActive++
								peak = max(peak, providerActive)
							} else {
								m.Kind = "rejected"
								m.InputTokens = 0
								m.OutputTokens = 0
								m.RetryAfterMS = max(1, wait.Milliseconds())
								m.Finished = now.Add(time.Millisecond)
							}
							pending = append(pending, m)
						}
						// Jump to the next observable completion/admission/offer;
						// virtual-clock resolution stays identical for every policy.
						now = now.Add(10 * time.Millisecond)
						if model == "concurrency" && peak > 72 {
							t.Fatal("provider fixture admitted above its own ceiling")
						}
					}
					s := e.Snapshot(now)
					if !e.Done() || s.StopReason != "" || s.Actors[0].Completed != 1000 || s.Actors[1].Completed != 200 {
						t.Fatalf("controller lost work: %+v", s)
					}
					for _, v := range e.Items {
						if v.Attempt > 16 {
							t.Fatal("attempt guard bypassed")
						}
					}
					// Retained output is simulation evidence only, never live capacity.
					t.Logf("foreground_ms=%d background_ms=%d refusals=%d", s.Actors[0].ReadyMs, s.Actors[1].ReadyMs, s.Actors[0].Rejected+s.Actors[1].Rejected)
				})
			}
		}
	}
}

func TestStrategyWorkloadOffersIdenticalCompetingDemand(t *testing.T) {
	p := batch.DefaultPolicy()
	a, x := StrategyWorkload(p, 1000, true, false)
	p.WindowRequests = 0
	b, y := StrategyWorkload(p, 1000, true, false)
	if len(a) != 2 || len(b) != 2 || len(x) != 1200 || len(y) != 1200 {
		t.Fatal("workload size changed")
	}
	for i := range x {
		if x[i] != y[i] {
			t.Fatal("policy changed offered demand")
		}
	}
	var times []time.Duration
	for _, v := range x {
		if v.Actor == 1 {
			times = append(times, v.ReadyAt)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	if times[99] != 0 || times[100] != 45*time.Second || times[199] != 144*time.Second || x[0].ReadyAt != 15*time.Second {
		t.Fatal("background phase contract changed")
	}
}

func TestStrategyEngineRetriesRefusalsButNeverUnknownOutcomes(t *testing.T) {
	at := time.Unix(1000, 0)
	p := batch.DefaultPolicy()
	p.Concurrency, p.Burst = 1, 1
	p.WindowRequests = 0
	e, err := NewStrategyEngine(at, []StrategyActor{{Name: "foreground", Policy: p}}, []StrategyItem{{Actor: 0}, {Actor: 0}}, 100000)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := e.Take(at)
	if !ok {
		t.Fatal("missing first dispatch")
	}
	e.Finish(NativeMeasurement{ID: first.ID, Attempt: first.Attempt, Started: at, Finished: at.Add(time.Second), Kind: "unconfirmed"})
	second, ok := e.Take(at.Add(time.Second))
	if !ok || second.ID == first.ID {
		t.Fatal("unknown was replayed or independent work blocked")
	}
	e.Finish(NativeMeasurement{ID: second.ID, Attempt: second.Attempt, Started: at.Add(time.Second), Finished: at.Add(2 * time.Second), Kind: "rejected", RetryAfterMS: 3000})
	if _, ok := e.Take(at.Add(4 * time.Second)); ok {
		t.Fatal("retry hint shortened")
	}
	probe, ok := e.Take(at.Add(5 * time.Second))
	if !ok || probe.ID != second.ID || probe.Attempt != 2 {
		t.Fatal("explicit refusal was not retried")
	}
	if _, ok := e.Take(at.Add(5 * time.Second)); ok {
		t.Fatal("multiple recovery probes")
	}
	e.Finish(NativeMeasurement{ID: probe.ID, Attempt: probe.Attempt, Started: at.Add(5 * time.Second), Finished: at.Add(6 * time.Second), Kind: "completed", InputTokens: 10, OutputTokens: 5})
	if !e.Done() || e.Snapshot(at.Add(6 * time.Second)).Actors[0].Completed != 1 || e.Snapshot(at.Add(6 * time.Second)).Actors[0].Unknown != 1 {
		t.Fatal("terminal counts wrong")
	}
}

func TestStrategySharedControllerEnforcesCombinedConcurrency(t *testing.T) {
	at := time.Unix(1000, 0)
	p := batch.DefaultPolicy()
	p.Concurrency = 2
	actors := []StrategyActor{{Name: "a", Policy: p}, {Name: "b", Policy: p, ShareWithFirst: true}}
	e, err := NewStrategyEngine(at, actors, []StrategyItem{{Actor: 0}, {Actor: 0}, {Actor: 1}, {Actor: 1}}, 100000)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := e.Take(at)
	b, _ := e.Take(at)
	if a.Actor == b.Actor {
		t.Fatal("ready clients were not served fairly")
	}
	if _, ok := e.Take(at); ok {
		t.Fatal("per-client concurrency multiplied shared account ceiling")
	}
}

func TestStrategyBudgetAndPolicyStopPreventNewDispatch(t *testing.T) {
	at := time.Unix(1000, 0)
	p := batch.DefaultPolicy()
	for _, reason := range []string{"budget", "policy"} {
		t.Run(reason, func(t *testing.T) {
			budget := int64(100000)
			if reason == "budget" {
				budget = strategyReservation - 1
			}
			e, err := NewStrategyEngine(at, []StrategyActor{{Name: "a", Policy: p}}, []StrategyItem{{Actor: 0}, {Actor: 0}}, budget)
			if err != nil {
				t.Fatal(err)
			}
			first, ok := e.Take(at)
			if reason == "policy" {
				if !ok {
					t.Fatal("expected initial dispatch")
				}
				e.Finish(NativeMeasurement{ID: first.ID, Attempt: first.Attempt, Started: at, Finished: at, Kind: "policy"})
			} else if ok {
				t.Fatal("budget reservation bypassed")
			}
			if _, ok := e.Take(at); ok {
				t.Fatal("stopped controller dispatched")
			}
			if !e.Done() || e.StopReason == "" {
				t.Fatal("stop not observable")
			}
		})
	}
}

func TestStrategyRunnerDrainsCallsOnCancellation(t *testing.T) {
	at := time.Now()
	p := batch.DefaultPolicy()
	p.Concurrency = 2
	e, err := NewStrategyEngine(at, []StrategyActor{{Name: "a", Policy: p}}, []StrategyItem{{Actor: 0}, {Actor: 0}, {Actor: 0}}, 100000)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := make(chan struct{}, 2)
	call := func(ctx context.Context, _ string, id, attempt int) NativeMeasurement {
		calls <- struct{}{}
		<-ctx.Done()
		return NativeMeasurement{ID: id, Attempt: attempt, Started: at, Finished: time.Now(), Kind: "unconfirmed"}
	}
	done := make(chan error, 1)
	go func() { done <- RunStrategy(ctx, e, "fixture", call, io.Discard, nil) }()
	<-calls
	<-calls
	cancel()
	select {
	case err := <-done:
		if err == nil || e.Active != 0 || e.Items[2].Attempt != 0 {
			t.Fatal("cancellation did not drain without dispatching queued work")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner leaked calls")
	}
}

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-483
func TestOfferedBackgroundRefusalsAreTerminalWithoutWeakeningForegroundRetries(t *testing.T) {
	at := time.Unix(1000, 0)
	p := batch.DefaultPolicy()
	p.Concurrency = 1
	p.Burst = 1
	p.WindowRequests = 0
	actors := []StrategyActor{{Name: "foreground", Policy: p}, {Name: "background", Policy: p, RefusalIsTerminal: true}}
	e, err := NewStrategyEngine(at, actors, []StrategyItem{{Actor: 0, ReadyAt: 15 * time.Second}, {Actor: 1}}, 100000)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := e.Take(at)
	if !ok || b.Actor != 1 {
		t.Fatal("background offer missing")
	}
	e.Finish(NativeMeasurement{ID: b.ID, Attempt: b.Attempt, Started: at, Finished: at.Add(time.Second), Kind: "rejected", RetryAfterMS: 1000})
	if e.stats[1].TerminalRefusals != 1 || e.Items[b.ID].state != "done" {
		t.Fatal("one-shot background refusal was requeued")
	}
	if _, ok := e.Take(at.Add(2 * time.Second)); ok {
		t.Fatal("background refusal was replayed")
	}
	f, ok := e.Take(at.Add(15 * time.Second))
	if !ok || f.Actor != 0 {
		t.Fatal("foreground offer missing")
	}
	e.Finish(NativeMeasurement{ID: f.ID, Attempt: f.Attempt, Started: at.Add(15 * time.Second), Finished: at.Add(16 * time.Second), Kind: "rejected", RetryAfterMS: 1000})
	retry, ok := e.Take(at.Add(17 * time.Second))
	if !ok || retry.ID != f.ID || retry.Attempt != 2 {
		t.Fatal("foreground result oracle weakened by background mode")
	}
	e.Finish(NativeMeasurement{ID: retry.ID, Attempt: retry.Attempt, Started: at.Add(17 * time.Second), Finished: at.Add(18 * time.Second), Kind: "completed", InputTokens: 10, OutputTokens: 5})
	if !e.Done() || e.stats[0].Completed != 1 || e.stats[0].TerminalRefusals != 0 || e.stats[1].Completed != 0 || e.stats[1].TerminalRefusals != 1 {
		t.Fatal("offered-load outcome totals wrong")
	}
	if e.Snapshot(at.Add(18*time.Second)).ForegroundAdmittedAfterBackgroundFinished != 1 {
		t.Fatal("background-stop coverage was not retained")
	}
	actors[0].RefusalIsTerminal = true
	if _, err := NewStrategyEngine(at, actors, []StrategyItem{{Actor: 0}}, 100000); err == nil {
		t.Fatal("foreground may not discard quota-refused work")
	}
}

func TestOfferedTrafficHasBoundedPhasesAndStopsNewArrivals(t *testing.T) {
	actors, items := StrategyOfferedTraffic(batch.DefaultPolicy(), 1000)
	if len(actors) != 2 || len(items) != 1175 || !actors[1].RefusalIsTerminal || actors[0].RefusalIsTerminal || actors[1].ShareWithFirst {
		t.Fatal("offered workload contract wrong")
	}
	counts := map[time.Duration]int{}
	for _, v := range items {
		if v.Actor == 1 {
			counts[v.ReadyAt]++
		} else if v.ReadyAt != 15*time.Second {
			t.Fatal("foreground start changed")
		}
	}
	if len(counts) != 4 || counts[0] != 100 || counts[67*time.Second] != 25 || counts[128*time.Second] != 25 || counts[189*time.Second] != 25 {
		t.Fatal("background phases changed", counts)
	}
	// The queued-work comparison must retain its original all-1,200-success contract.
	oldActors, oldItems := StrategyWorkload(batch.DefaultPolicy(), 1000, true, false)
	if len(oldItems) != 1200 || oldActors[1].RefusalIsTerminal {
		t.Fatal("existing comparison was weakened")
	}
}
