package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-474
import (
	"math"
	"testing"
	"time"
)

func TestAdmissionBurstPaceAndSingleRecoveryProbe(t *testing.T) {
	now := time.Unix(1000, 0)
	p := DefaultPolicy()
	p.Burst, p.StartRate = 100, 50
	c := NewController(p)
	for i := 0; i < 100; i++ {
		if !c.CanStart(now, 0) {
			t.Fatalf("burst %d blocked", i)
		}
		c.Start(now)
	}
	if c.CanStart(now, 0) {
		t.Fatal("paced admission bypassed")
	}
	if !c.CanStart(now.Add(20*time.Millisecond), 0) || c.CanStart(now.Add(time.Second), 128) {
		t.Fatal("pace or concurrency incorrect")
	}
	c.Throttle(now.Add(time.Second), 0)
	if c.CanStart(now.Add(60*time.Second), 0) {
		t.Fatal("early recovery")
	}
	probe := now.Add(61 * time.Second)
	if !c.CanStart(probe, 0) {
		t.Fatal("missing recovery probe")
	}
	c.Start(probe)
	if c.CanStart(probe.Add(time.Second), 0) {
		t.Fatal("more than one recovery probe")
	}
	c.Success(probe.Add(time.Second))
	if !c.CanStart(probe.Add(time.Second), 0) {
		t.Fatal("successful probe did not reopen admission")
	}
}
func TestAdmissionHonorsLongestDelayAndOnePaceDecrease(t *testing.T) {
	now := time.Unix(1000, 0)
	c := NewController(DefaultPolicy())
	c.Start(now)
	c.Throttle(now, 2*time.Minute)
	rate := c.Rate
	c.Throttle(now.Add(time.Second), time.Minute)
	if c.Rate != rate || c.NextEligible.Before(now.Add(2*time.Minute)) {
		t.Fatal("repeated throttles shortened cooldown or compounded decrease")
	}
}
func TestAdmissionGrowthDoesNotConfuseRateWithConcurrency(t *testing.T) {
	now := time.Unix(1000, 0)
	c := NewController(DefaultPolicy())
	c.Start(now)
	for i := 0; i < 100; i++ {
		c.Success(now)
	}
	if c.Rate != 200 || c.Policy.Concurrency != 128 {
		t.Fatal("independent controls lost")
	}
}
func TestStragglersCannotReleaseProbeOrReapplyExpiredThrottle(t *testing.T) {
	now := time.Unix(1000, 0)
	c := NewController(DefaultPolicy())
	c.Start(now)
	c.Throttle(now.Add(time.Second), 0)
	probe := now.Add(61 * time.Second)
	c.Start(probe)
	c.Complete(probe.Add(time.Second), now, now, false, "completed", 0)
	if !c.Probing || c.CanStart(probe.Add(time.Second), 0) {
		t.Fatal("straggling success reopened admission")
	}
	c.Complete(probe.Add(2*time.Second), now, now, false, "rejected", time.Second)
	if !c.Probing {
		t.Fatal("old rejection released the probe latch")
	}
	c.Complete(probe.Add(3*time.Second), probe, probe, true, "completed", 0)
	if !c.Epoch.Equal(probe) {
		t.Fatal("provider latency was added to the next quota episode")
	}
	c.Complete(probe.Add(4*time.Second), now, now, false, "rejected", time.Second)
	if c.Cooling || c.Probing {
		t.Fatal("old rejection invalidated successful recovery")
	}
}
func TestPolicyUpdatePreservesCooldownAndDoesNotGrantAnotherBurst(t *testing.T) {
	now := time.Unix(1000, 0)
	c := NewController(DefaultPolicy())
	c.Start(now)
	c.Throttle(now, 2*time.Minute)
	left := c.BurstLeft
	p := DefaultPolicy()
	p.Concurrency = 64
	p.StartRate = 100
	if err := c.Configure(p); err != nil {
		t.Fatal(err)
	}
	if !c.Cooling || c.Rate != 100 || c.BurstLeft != left || c.CanStart(now.Add(time.Second), 0) || c.CanStart(now.Add(3*time.Minute), 64) {
		t.Fatal("policy update bypassed admission constraints")
	}
	p.Concurrency = 0
	if err := c.Configure(p); err == nil || c.Policy.Concurrency != 64 {
		t.Fatal("invalid update changed active policy")
	}
}

func TestAdmissionWindowDoesNotRefillOnFastCompletions(t *testing.T) {
	now := time.Unix(1000, 0)
	p := DefaultPolicy()
	p.Burst = 200
	p.WindowRequests = 200
	p.WindowDuration = 61 * time.Second
	c := NewController(p)
	for i := range 200 {
		at := now.Add(time.Duration(i) * time.Millisecond)
		if !c.CanStart(at, 0) {
			t.Fatalf("window admission %d blocked", i)
		}
		c.Start(at)
		c.Complete(at.Add(time.Millisecond), at, at, false, "completed", 0)
	}
	if c.CanStart(now.Add(60*time.Second), 0) || !c.NextAdmission(now.Add(time.Second)).Equal(now.Add(61*time.Second)) {
		t.Fatal("fast completions bypassed the admission window")
	}
	if !c.CanStart(now.Add(61*time.Second), 0) {
		t.Fatal("next admission window did not open")
	}
	c.Start(now.Add(61 * time.Second))
	if c.WindowStarts != 1 {
		t.Fatal("next window did not reset reservations")
	}
	// A delayed refusal can refund only the reservation's original window.
	c.Complete(now.Add(62*time.Second), now, now, false, "rejected", 0)
	if c.WindowStarts != 1 {
		t.Fatal("old refusal refunded the new window")
	}
}

func TestAdmissionWindowRefundsOnlyExplicitSameWindowRefusals(t *testing.T) {
	now := time.Unix(1000, 0)
	p := DefaultPolicy()
	p.WindowRequests = 2
	p.WindowDuration = time.Minute
	c := NewController(p)
	c.Start(now)
	c.Complete(now.Add(time.Second), now, now, false, "failed", 0)
	if c.WindowStarts != 1 {
		t.Fatal("ordinary error refunded an uncertain reservation")
	}
	c.Start(now.Add(time.Second))
	c.Complete(now.Add(2*time.Second), now.Add(time.Second), now.Add(time.Second), false, "rejected", time.Second)
	if c.WindowStarts != 1 || !c.Cooling || !c.CanStart(now.Add(3*time.Second), 0) {
		t.Fatal("explicit refusal was not refunded or recovery probe was blocked")
	}
}

func TestLargeRetryHintCannotOverflowIntoImmediateAdmission(t *testing.T) {
	if got := retryDelay(math.MaxInt64); got != time.Duration(math.MaxInt64) {
		t.Fatal("large retry hint overflowed", got)
	}
	c := NewController(DefaultPolicy())
	at := time.Unix(1000, 0)
	c.Start(at)
	c.Throttle(at, retryDelay(math.MaxInt64))
	if c.CanStart(at.Add(time.Hour), 0) {
		t.Fatal("large provider wait became an early probe")
	}
}
