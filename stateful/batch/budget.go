package batch

import (
	"context"
	"errors"
	"time"
)

const LegacyGenerationBudget = 180 * time.Second
const DefaultGenerationBudget = 300 * time.Second
const MaximumGenerationBudget = 900 * time.Second

// generationBudget reads an accepted snapshot without changing its policy.
// Only historical zero snapshots use the legacy interpretation.
func generationBudget(g Generation) time.Duration {
	if g.TimeoutMS == 0 {
		return LegacyGenerationBudget
	}
	if g.TimeoutMS < 1 || g.TimeoutMS > MaximumGenerationBudget.Milliseconds() {
		return 0
	}
	return time.Duration(g.TimeoutMS) * time.Millisecond
}
func resolveNewGenerationBudget(ms int64) (int64, error) {
	if ms < 0 || ms > MaximumGenerationBudget.Milliseconds() {
		return 0, errors.New("batch: invalid generation timeout")
	}
	if ms == 0 {
		return DefaultGenerationBudget.Milliseconds(), nil
	}
	return ms, nil
}

func generationContext(parent context.Context, g Generation, jobDeadline time.Time) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(generationBudget(g))
	if jobDeadline.Before(deadline) {
		deadline = jobDeadline
	}
	return context.WithDeadline(parent, deadline)
}
