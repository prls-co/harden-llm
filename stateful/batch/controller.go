// Package batch owns durable, key-scoped batch execution around the stateless core.
package batch

import (
	"fmt"
	"math"
	"time"
)

type Policy struct {
	Concurrency    int
	Burst          int
	StartRate      int
	MaximumRate    int
	RefillEstimate time.Duration
	WindowRequests int
	WindowDuration time.Duration
}

func DefaultPolicy() Policy {
	return Policy{Concurrency: 128, Burst: 200, StartRate: 100, MaximumRate: 200, RefillEstimate: 61 * time.Second, WindowRequests: 200, WindowDuration: 61 * time.Second}
}
func (p Policy) Validate() error {
	if p.Concurrency < 1 || p.Concurrency > 1000 || p.Burst < 1 || p.Burst > 1000 || p.StartRate < 1 || p.MaximumRate < p.StartRate || p.MaximumRate > 1000 || p.RefillEstimate < time.Second || p.RefillEstimate > time.Hour {
		return fmt.Errorf("batch: invalid admission policy")
	}
	if p.WindowRequests < 0 || p.WindowRequests > 1000 || (p.WindowRequests > 0 && (p.WindowDuration < time.Second || p.WindowDuration > time.Hour)) {
		return fmt.Errorf("batch: invalid admission window")
	}
	return nil
}

// Controller contains only deterministic values; workflows supply their clock.
type Controller struct {
	Policy            Policy
	Rate              int
	BurstLeft         int
	Successes         int
	Epoch             time.Time
	NextStart         time.Time
	NextEligible      time.Time
	Cooling           bool
	Probing           bool
	ProbeDelay        time.Duration
	Paused            bool
	WindowStart       time.Time
	WindowStarts      int
	RefillWindowBurst bool
	PacedRecovery     bool
	AccumulatePace    bool
}

func NewController(p Policy) Controller {
	return Controller{Policy: p, Rate: p.StartRate, BurstLeft: p.Burst, ProbeDelay: 2 * time.Second, RefillWindowBurst: true, PacedRecovery: true, AccumulatePace: true}
}
func (c *Controller) Configure(p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	c.Policy = p
	c.Rate = p.StartRate
	c.Successes = 0
	return nil
}
func (c *Controller) CanStart(now time.Time, active int) bool {
	if c.Paused || active >= c.Policy.Concurrency || c.Probing {
		return false
	}
	if c.Cooling {
		return !now.Before(c.NextEligible)
	}
	if !c.WindowWait(now).IsZero() {
		return false
	}
	return c.BurstLeft > 0 || !now.Before(c.NextStart)
}
func (c *Controller) WindowWait(now time.Time) time.Time {
	if c.Policy.WindowRequests > 0 && c.WindowStarts >= c.Policy.WindowRequests && now.Before(c.WindowStart.Add(c.Policy.WindowDuration)) {
		return c.WindowStart.Add(c.Policy.WindowDuration)
	}
	return time.Time{}
}
func (c *Controller) NextAdmission(now time.Time) time.Time {
	if c.Cooling {
		return c.NextEligible
	}
	if next := c.WindowWait(now); !next.IsZero() {
		return next
	}
	if c.BurstLeft > 0 {
		return now
	}
	return c.NextStart
}
func (c *Controller) Start(now time.Time) {
	if c.Policy.WindowRequests > 0 {
		if c.WindowStart.IsZero() || !now.Before(c.WindowStart.Add(c.Policy.WindowDuration)) {
			if !c.WindowStart.IsZero() && c.RefillWindowBurst {
				c.BurstLeft = c.Policy.Burst
			}
			c.WindowStart = now
			c.WindowStarts = 0
		}
		c.WindowStarts++
	}
	if c.Epoch.IsZero() {
		c.Epoch = now
	}
	if c.Cooling {
		c.Probing = true
		return
	}
	if c.BurstLeft > 0 {
		c.BurstLeft--
		c.NextStart = now.Add(time.Second / time.Duration(c.Rate))
		return
	}
	interval := time.Second / time.Duration(c.Rate)
	if c.AccumulatePace {
		// Temporal decisions are coarser than a 5-10 ms launch interval. Carry
		// elapsed credits forward, bounded by the configured burst, rather
		// than limiting throughput to one call per workflow task.
		oldest := now.Add(-time.Duration(c.Policy.Burst-1) * interval)
		if c.NextStart.Before(oldest) {
			c.NextStart = oldest
		}
		c.NextStart = c.NextStart.Add(interval)
	} else {
		c.NextStart = now.Add(interval)
	}
}
func (c *Controller) Success(now time.Time) {
	if c.Probing {
		c.Cooling = false
		c.Probing = false
		c.Epoch = now
		c.NextEligible = time.Time{}
		c.BurstLeft = c.Policy.Burst - 1
		// A probe establishes capacity for one request, not another entire
		// initial burst. Reopen at the adapted pace; an independent configured
		// admission window may still replenish its budget in a later window.
		if c.PacedRecovery {
			c.BurstLeft = 0
		}
		c.NextStart = now
		c.ProbeDelay = 2 * time.Second
		c.Successes = 0
	}
	c.Successes++
	if c.Successes >= 100 {
		c.Rate = min(c.Policy.MaximumRate, c.Rate*2)
		c.Successes = 0
	}
}
func (c *Controller) Throttle(now time.Time, delay time.Duration) {
	if !c.Cooling {
		c.Rate = max(1, c.Rate/2)
		c.Successes = 0
	}
	if c.Probing {
		c.Probing = false
		c.ProbeDelay = min(time.Minute, c.ProbeDelay*2)
	}
	c.Cooling = true
	next := now.Add(delay)
	if delay <= 0 {
		next = now.Add(c.ProbeDelay)
		if estimate := c.Epoch.Add(c.Policy.RefillEstimate); estimate.After(next) {
			next = estimate
		}
	}
	if next.After(c.NextEligible) {
		c.NextEligible = next
	}
}

// Complete distinguishes the recovery probe from requests admitted before the
// cooldown. A straggling success cannot reopen admission on the probe's behalf.
func (c *Controller) Complete(now, dispatched, reserved time.Time, probe bool, kind string, delay time.Duration) {
	switch kind {
	case "rejected":
		if c.Policy.WindowRequests > 0 && !reserved.Before(c.WindowStart) && c.WindowStarts > 0 {
			c.WindowStarts--
		}
		if !dispatched.Before(c.Epoch) {
			probing := c.Probing
			if !probe {
				c.Probing = false
			}
			c.Throttle(now, delay)
			if !probe {
				c.Probing = probing
			}
		}
	case "completed":
		if !c.Cooling || probe {
			c.Success(now)
			if probe {
				c.Epoch = dispatched
			}
		}
	case "policy":
		c.Paused = true
	}
	if probe && c.Probing && kind != "completed" && kind != "rejected" {
		c.Throttle(now, 0)
	}
}

// retryDelay saturates before multiplying, preserving very large provider waits.
func retryDelay(ms int64) time.Duration {
	if ms > math.MaxInt64/int64(time.Millisecond) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(max(0, ms)) * time.Millisecond
}
