package livecapacity

import (
	"context"
	"fmt"
	"time"
)

// RunConcurrency releases fresh CPA-only waves without start pacing. Each wave
// must finish before another starts; any failed/unknown result stops escalation.
// CPA may retry internally, so this measures effective CPA HTTP capacity, not
// unobservable native Codex execution concurrency.
func (c *Campaign) RunConcurrency(ctx context.Context, maximum, waves int) error {
	if maximum < 1 || maximum > 128 || waves < 1 || waves > 5 {
		return fmt.Errorf("invalid concurrency experiment bounds")
	}
	for level := 1; ; level = min(maximum, level*2) {
		for wave := 1; wave <= waves; wave++ {
			s, err := c.RunPhase(ctx, fmt.Sprintf("concurrency-%d-wave-%d", level, wave), level, RouteConfig{Concurrency: level}, false, true, false)
			if err != nil {
				return err
			}
			if s.Snapshot(time.Now()).Completed != level {
				return fmt.Errorf("concurrency %d wave %d incomplete; escalation stopped; see status.json", level, wave)
			}
			if level > 1 && s.PeakCPA != level {
				return fmt.Errorf("target concurrency not attained")
			}
			if level == maximum && wave == waves {
				return nil
			}
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
}
