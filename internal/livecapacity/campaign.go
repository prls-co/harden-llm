package livecapacity

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/prls-co/harden-llm/internal/redaction"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Campaign struct {
	Redactor        *redaction.Redactor
	ID              string
	Directory       string
	Started         time.Time
	Transport       Transport
	Events          io.Writer
	Budget          float64
	Spent, Reserved float64
	Phase           string
	Last            *Scheduler
	Measurements    []Measurement
	StopReason      string
}

func (c *Campaign) event(kind string, data any) error {
	raw, err := json.Marshal(map[string]any{"campaign": c.ID, "phase": c.Phase, "type": kind, "at": time.Now().UTC(), "elapsedMs": time.Since(c.Started).Milliseconds(), "data": data})
	if err != nil {
		return err
	}
	scrub := c.Redactor
	if scrub == nil {
		scrub = redaction.New()
	}
	raw, err = scrub.JSON(raw)
	if err != nil {
		return err
	}
	_, err = c.Events.Write(append(raw, '\n'))
	return err
}
func (c *Campaign) save(s *Scheduler) error {
	report := struct {
		ID, Phase, StopReason string
		Started, Updated      time.Time
		Progress              Progress
		Items                 []Item
	}{c.ID, c.Phase, c.StopReason, c.Started, time.Now().UTC(), s.Snapshot(time.Now()), s.Items}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	temp := filepath.Join(c.Directory, "status.json.tmp")
	if err = os.WriteFile(temp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(temp, filepath.Join(c.Directory, "status.json"))
}

// RunPhase admits from a backlog; exactly one goroutine owns all scheduler
// state. Workers only return measurements. Cancellation drains every worker
// before checkpointing, retaining unknown outcomes instead of replaying them.
func (c *Campaign) RunPhase(ctx context.Context, name string, n int, config RouteConfig, overflow, stopOnRejection bool, onlyAPI bool) (*Scheduler, error) {
	c.Phase = name
	c.Transport.NoncePrefix = c.ID + "-" + name
	s := NewScheduler(n, time.Now(), config, c.Budget)
	if c.Last != nil {
		s.CPA.PauseUntil = c.Last.CPA.PauseUntil
	}
	s.Spent = c.Spent
	s.Reserved = c.Reserved
	if onlyAPI {
		for i := range s.Items {
			s.Items[i].Route = "openai"
		}
		s.API.Concurrency = 1
	}
	c.Last = s
	workers := make(chan Measurement, 32)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()
	if err := c.event("phase_started", s.Snapshot(time.Now())); err != nil {
		return s, err
	}
	if err := c.save(s); err != nil {
		return s, err
	}
	process := func(m Measurement) error {
		c.Measurements = append(c.Measurements, m)
		s.Finish(m.ID, m.Outcome, m.Finished)
		if err := c.event("call_finished", m); err != nil {
			return err
		}
		if stopOnRejection && m.Outcome.Kind != "completed" {
			s.Stop("ramp_failure")
		}
		return c.save(s)
	}
	var runErr error
	done := ctx.Done()
	for !s.Done() {
		if ctx.Err() != nil {
			s.Stop("deadline_or_canceled")
		}
		if s.StopReason == "" {
			if reason := s.Stalled(time.Now()); reason != "" {
				s.Stop(reason)
			}
			for {
				item, ok := s.Admit(time.Now(), overflow)
				if !ok {
					break
				}
				if err := c.event("call_started", item); err != nil {
					s.Finish(item.ID, Outcome{Kind: "failed"}, time.Now())
					s.Stop("artifact_error")
					runErr = err
					break
				}
				if err := c.save(s); err != nil {
					s.Finish(item.ID, Outcome{Kind: "failed"}, time.Now())
					s.Stop("artifact_error")
					runErr = err
					break
				}
				transport := c.Transport
				go func() { workers <- transport.Call(ctx, item) }()
			}
		}
		if s.StopReason != "" && s.CPA.Active+s.API.Active == 0 {
			break
		}
		if s.CPA.Active+s.API.Active == 0 {
			possible := false
			for _, item := range s.Items {
				if item.Status != "queued" {
					continue
				}
				if item.Route == "cpa" && !s.CPA.Disabled || item.Route == "openai" && !s.API.Disabled && s.Spent+s.Reserved+AttemptReservation <= s.Budget || item.Route == "" && (!s.CPA.Disabled || overflow && !s.API.Disabled && s.Spent+s.Reserved+AttemptReservation <= s.Budget) {
					possible = true
					break
				}
			}
			if !possible {
				s.Stop("routes_paused_or_budget_exhausted")
				continue
			}
		}
		// Do not wait until the campaign deadline for an impossible continuation.
		if s.CPA.Active+s.API.Active == 0 && (s.CPA.Disabled || onlyAPI) && s.API.Disabled {
			s.Stop("routes_paused")
			continue
		}
		select {
		case m := <-workers:
			if err := process(m); err != nil {
				runErr = err
				s.Stop("artifact_error")
			}
		case <-heartbeat.C:
			if err := c.event("heartbeat", s.Snapshot(time.Now())); err != nil {
				runErr = err
				s.Stop("artifact_error")
			}
			if err := c.save(s); err != nil {
				runErr = err
				s.Stop("artifact_error")
			}
		case <-tick.C:
		case <-done:
			s.Stop("deadline_or_canceled")
			done = nil
		}
	}
	c.Spent = s.Spent
	c.Reserved = s.Reserved
	if s.StopReason != "" {
		c.StopReason = s.StopReason
	}
	if err := c.event("phase_finished", s.Snapshot(time.Now())); err != nil {
		runErr = err
	}
	if err := c.save(s); err != nil {
		runErr = err
	}
	return s, runErr
}
func (c *Campaign) Run(ctx context.Context) error {
	clean := RouteConfig{Concurrency: 1, Interval: 4 * time.Second}
	for _, q := range []struct {
		name string
		n    int
		api  bool
	}{{"qualify-cpa", 3, false}, {"qualify-openai", 1, true}} {
		s, err := c.RunPhase(ctx, q.name, q.n, clean, false, true, q.api)
		if err != nil {
			return err
		}
		if s.Snapshot(time.Now()).Completed != q.n {
			return fmt.Errorf("%s failed; see status.json", q.name)
		}
	}
	for i, concurrency := range []int{1, 2, 4, 8, 16} {
		config := RouteConfig{Concurrency: concurrency, Interval: 4 * time.Second / time.Duration(1<<i)}
		s, err := c.RunPhase(ctx, fmt.Sprintf("ramp-%d", concurrency), 24, config, false, true, false)
		if err != nil {
			return err
		}
		if s.Snapshot(time.Now()).Completed != 24 {
			break
		}
		clean = config
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if c.StopReason == "account_or_policy_rejection" {
		return fmt.Errorf("account/policy rejection; campaign stopped")
	}
	c.StopReason = ""
	drain := RouteConfig{Concurrency: max(1, clean.Concurrency/2), Interval: clean.Interval * 2}
	s, err := c.RunPhase(ctx, "drain-1000", 1000, drain, true, false, false)
	if err != nil {
		return err
	}
	if s.Snapshot(time.Now()).Completed != 1000 {
		if c.StopReason == "" {
			c.StopReason = "drain_incomplete"
			s.Stop(c.StopReason)
			if err := c.save(s); err != nil {
				return err
			}
		}
		return fmt.Errorf("drain incomplete; see status.json")
	}
	return nil
}
func (c *Campaign) Summary() error {
	type distribution struct {
		N   int    `json:"n"`
		P50 int64  `json:"p50Ms"`
		P95 int64  `json:"p95Ms"`
		P99 *int64 `json:"p99Ms,omitempty"`
	}
	groups := map[string][]int64{}
	for _, m := range c.Measurements {
		if m.Outcome.Kind == "completed" {
			groups[m.Route] = append(groups[m.Route], m.DurationMS)
		}
	}
	stats := map[string]distribution{}
	for route, values := range groups {
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		pick := func(q float64) int64 { return values[min(len(values)-1, int(float64(len(values))*q))] }
		d := distribution{N: len(values), P50: pick(.5), P95: pick(.95)}
		if len(values) >= 1000 {
			n := pick(.99)
			d.P99 = &n
		}
		stats[route] = d
	}
	return c.event("summary", map[string]any{"completedHTTPDurations": stats, "settledUSD": c.Spent, "reservedUSD": c.Reserved, "stopReason": c.StopReason, "timingOrigin": "local gateway HTTP wall time; HLLM attempt duration is provider operation; stream timings, when present, start after upstream headers; CPA internal attempts unknown"})
}
