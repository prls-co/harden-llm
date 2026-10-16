package batch

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-474
import (
	"testing"
	"time"
)

func TestAdmissionBurstPaceAndSingleRecoveryProbe(t *testing.T) {
	now := time.Unix(1000, 0)
	c := NewController(DefaultPolicy())
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
	if c.Rate != 100 || c.Policy.Concurrency != 128 {
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
	c.Complete(probe.Add(time.Second), now, false, "completed", 0)
	if !c.Probing || c.CanStart(probe.Add(time.Second), 0) {
		t.Fatal("straggling success reopened admission")
	}
	c.Complete(probe.Add(2*time.Second), now, false, "rejected", time.Second)
	if !c.Probing {
		t.Fatal("old rejection released the probe latch")
	}
	c.Complete(probe.Add(3*time.Second), probe, true, "completed", 0)
	if !c.Epoch.Equal(probe) {
		t.Fatal("provider latency was added to the next quota episode")
	}
	c.Complete(probe.Add(4*time.Second), now, false, "rejected", time.Second)
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
