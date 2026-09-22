// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-276

package capacity

import (
	"context"
	"sync"
	"testing"
	"time"
)

type immediateClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *immediateClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *immediateClock) Sleep(_ context.Context, duration time.Duration) error {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
	return nil
}

type scriptedTransport struct {
	mu        sync.Mutex
	responses []Response
	next      int
}

func (transport *scriptedTransport) Send(_ context.Context, _ Request) (Response, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	response := transport.responses[transport.next]
	transport.next++
	return response, nil
}

func TestCapacityDriverBuildsOpenLoopArrivalSchedule(t *testing.T) {
	scenario := Scenario{OfferedRPS: 4, Measurement: 1250 * time.Millisecond, MaxRequests: 10, MaxInflight: 4}
	got, err := BuildArrivalSchedule(scenario)
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{0, 250 * time.Millisecond, 500 * time.Millisecond, 750 * time.Millisecond, time.Second}
	if len(got) != len(want) {
		t.Fatalf("arrival count = %d, want %d (%v)", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("arrival %d = %s, want %s", index, got[index], want[index])
		}
	}
}

func TestCapacityDriverReconcilesTerminalOutcomesAndProviderStages(t *testing.T) {
	transport := &scriptedTransport{responses: []Response{
		{
			StatusCode:     200,
			StreamTerminal: "run.completed",
			JSONValid:      true,
			ProviderCalls:  []ProviderCall{{Stage: "generation", Model: "model-a", InputTokens: 20, OutputTokens: 10}},
		},
		{
			StatusCode:     200,
			StreamTerminal: "",
			JSONValid:      true,
			ProviderCalls:  []ProviderCall{{Stage: "generation", Model: "model-a"}, {Stage: "json_repair", Model: "model-b"}},
		},
		{
			StatusCode: 429,
		},
		{
			StatusCode:     200,
			StreamTerminal: "run.completed",
			CacheHit:       true,
			JSONValid:      true,
		},
	}}
	clock := &immediateClock{now: time.Unix(1_000, 0)}
	scenario := Scenario{
		OfferedRPS:  4,
		Measurement: time.Second,
		Drain:       time.Second,
		MaxRequests: 4,
		MaxInflight: 4,
		RequireJSON: true,
	}
	got, err := Run(context.Background(), scenario, transport, clock)
	if err != nil {
		t.Fatal(err)
	}
	if got.Offered != 4 || got.Launched != 4 || got.Unsent != 0 {
		t.Fatalf("offered/launched/unsent = %d/%d/%d, want 4/4/0", got.Offered, got.Launched, got.Unsent)
	}
	if got.Succeeded != 2 || got.Failed != 1 || got.Rejected != 1 || got.Canceled != 0 || got.Unfinished != 0 {
		t.Fatalf("terminal outcomes = success:%d failed:%d rejected:%d canceled:%d unfinished:%d", got.Succeeded, got.Failed, got.Rejected, got.Canceled, got.Unfinished)
	}
	if got.ProviderAttempts != 3 {
		t.Fatalf("provider attempts = %d, want 3 (cache hit is not a provider call)", got.ProviderAttempts)
	}
	if got.StageDispatches["generation"] != 2 || got.StageDispatches["json_repair"] != 1 {
		t.Fatalf("stage dispatches = %#v", got.StageDispatches)
	}
}

func TestCapacityDriverRejectsExternalProviderEndpoints(t *testing.T) {
	if err := ValidateEndpoint("https://api.openai.com/v1", nil); err == nil {
		t.Fatal("production provider endpoint must be rejected before dialing")
	}
	if err := ValidateEndpoint("https://api.openai.com.attacker.test/v1", nil); err == nil {
		t.Fatal("a production-provider hostname suffix must not pass as local")
	}
	if err := ValidateEndpoint("https://user:secret@127.0.0.1:8443", nil); err == nil {
		t.Fatal("credentials in the endpoint must be rejected")
	}
	if err := ValidateEndpoint("https://127.0.0.1:8443", nil); err != nil {
		t.Fatalf("loopback endpoint rejected: %v", err)
	}
	if err := ValidateEndpoint("https://fake-provider:8443", []string{"fake-provider"}); err != nil {
		t.Fatalf("explicit local scripted provider rejected: %v", err)
	}
}

func TestCapacityDriverBoundsScenarioAndReconcilesInflightCeiling(t *testing.T) {
	scenario := Scenario{OfferedRPS: 4, Measurement: time.Second, Drain: time.Second, MaxRequests: 4, MaxInflight: 1}
	schedule, err := BuildArrivalSchedule(scenario)
	if err != nil || len(schedule) != 4 {
		t.Fatalf("bounded schedule = %v, error = %v", schedule, err)
	}
	scenario.MaxRequests = 3
	if _, err := BuildArrivalSchedule(scenario); err == nil {
		t.Fatal("schedule above maxRequests must fail before allocating/sending")
	}

	gate := make(chan struct{})
	transport := &gatedTransport{release: gate}
	scenario.MaxRequests = 4
	scenario.Drain = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := Run(ctx, scenario, transport, &immediateClock{now: time.Unix(2_000, 0)})
	if err != nil {
		t.Fatal(err)
	}
	close(gate)
	if got.Offered != got.Launched+got.Unsent {
		t.Fatalf("offered accounting does not reconcile: %#v", got)
	}
	if got.Unfinished != 1 || got.Succeeded+got.Failed+got.Rejected+got.Canceled+got.Unfinished != got.Launched {
		t.Fatalf("drain deadline did not retain the outstanding request as unfinished: %#v", got)
	}
}

func TestCapacityDriverKeepsWarmupSeparateAndStopsMeasurementAfterBadWarmup(t *testing.T) {
	transport := &scriptedTransport{responses: []Response{
		{StatusCode: 200, StreamTerminal: "run.completed", JSONValid: true},
		{StatusCode: 200, StreamTerminal: "run.completed", JSONValid: true},
		{StatusCode: 200, StreamTerminal: "run.completed", JSONValid: true},
		{StatusCode: 200, StreamTerminal: "run.completed", JSONValid: true},
	}}
	got, err := Run(context.Background(), Scenario{
		OfferedRPS: 2, Warmup: time.Second, Measurement: time.Second, Drain: time.Second,
		MaxRequests: 4, MaxInflight: 2,
	}, transport, &immediateClock{now: time.Unix(3_000, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Warmup.Offered != 2 || got.Warmup.Succeeded != 2 || got.Offered != 2 || got.Succeeded != 2 {
		t.Fatalf("warmup and measured counters were mixed: warmup=%#v measured=%#v", got.Warmup, got.PopulationResult)
	}
	if got.Warmup.Requests[0].Population != "warmup" || got.Requests[0].Population != "measurement" || got.Warmup.Requests[1].ID >= got.Requests[0].ID {
		t.Fatalf("warmup and measurement request origins are not separated: warmup=%#v measured=%#v", got.Warmup.Requests, got.Requests)
	}

	failedWarmup := &scriptedTransport{responses: []Response{{StatusCode: 429}}}
	stopped, err := Run(context.Background(), Scenario{
		OfferedRPS: 1, Warmup: time.Second, Measurement: time.Second, Drain: time.Second,
		MaxRequests: 2, MaxInflight: 1,
	}, failedWarmup, &immediateClock{now: time.Unix(4_000, 0)})
	if err == nil {
		t.Fatal("failed warmup must prevent a misleading measurement phase")
	}
	if stopped.Warmup.Rejected != 1 || stopped.Launched != 0 || stopped.Unsent != stopped.Offered {
		t.Fatalf("failed warmup accounting = warmup:%#v measured:%#v", stopped.Warmup, stopped.PopulationResult)
	}
}

func TestCapacityDriverExplorationStopsOnlyAfterDeclaredFailureOrLagThresholds(t *testing.T) {
	population := PopulationResult{Launched: 100, Failed: 5}
	if reason := ExplorationStopReason("at-threshold", population, LatencyDistribution{SampleCount: 100, P95NS: 100 * time.Millisecond}); reason != "" {
		t.Fatalf("exact stop thresholds should allow the next workload: %q", reason)
	}
	population.Failed = 6
	if reason := ExplorationStopReason("high-failure", population, LatencyDistribution{SampleCount: 100, P95NS: 100 * time.Millisecond}); reason == "" {
		t.Fatal("more than 5 percent unexpected outcomes should stop escalation")
	}
	population = PopulationResult{Launched: 99, Failed: 99}
	if reason := ExplorationStopReason("too-few-samples", population, LatencyDistribution{}); reason != "" {
		t.Fatalf("failure ratio below 100 launches must not trigger the configured statistical stop: %q", reason)
	}
	if reason := ExplorationStopReason("scheduler-lag", PopulationResult{}, LatencyDistribution{SampleCount: 20, P95NS: 101 * time.Millisecond}); reason == "" {
		t.Fatal("p95 scheduler lag over 100 ms should stop the next workload")
	}
}

type gatedTransport struct{ release <-chan struct{} }

func (transport *gatedTransport) Send(ctx context.Context, _ Request) (Response, error) {
	select {
	case <-transport.release:
		return Response{StatusCode: 200, StreamTerminal: "run.completed", JSONValid: true}, nil
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
}
