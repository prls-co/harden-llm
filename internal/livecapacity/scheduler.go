// Package livecapacity implements an explicit live experiment driver. It is not
// a production job queue and is never used by the stateless execution core.
package livecapacity

import (
	"math"
	"time"
)

// Standard global GPT-6 Luna prices verified 2026-10-15 against OpenAI's
// changelog/model docs. Reserve 8192 input tokens at the higher cache-write
// rate and all 512 requested output tokens (including reasoning). The fixed
// synthetic request is under 2048 UTF-8 bytes; no tools or prior context.
const AttemptReservation = 8192*0.125/1e6 + 512*0.50/1e6

type RouteConfig struct {
	Concurrency int           `json:"concurrency"`
	Interval    time.Duration `json:"intervalNs"`
	Active      int           `json:"active"`
	Next        time.Time     `json:"nextStartAt"`
	PauseUntil  time.Time     `json:"pauseUntil"`
	Failures    int           `json:"consecutiveRejections"`
	Disabled    bool          `json:"disabled"`
}
type Item struct {
	ID        int           `json:"id"`
	Status    string        `json:"status"`
	Route     string        `json:"route,omitempty"`
	Attempts  int           `json:"attempts"`
	Enqueued  time.Time     `json:"enqueuedAt"`
	Started   time.Time     `json:"startedAt,omitempty"`
	QueueWait time.Duration `json:"queueWaitNs"`
	RetryWait time.Duration `json:"retryWaitNs"`
	RetryAt   time.Time     `json:"retryAt,omitempty"`
}
type Outcome struct {
	Kind       string        `json:"kind"` // completed, rejected (known refusal), failed, unconfirmed, policy
	RetryAfter time.Duration `json:"retryAfterNs"`
	UsageKnown bool          `json:"usageKnown"`
	Cost       float64       `json:"costUSD"`
}
type Scheduler struct {
	PeakCPA        int         `json:"peakCPA"`
	PeakAPI        int         `json:"peakOpenAI"`
	Items          []Item      `json:"items"`
	CPA            RouteConfig `json:"cpa"`
	API            RouteConfig `json:"openai"`
	Budget         float64     `json:"budgetUSD"`
	Spent          float64     `json:"settledUSD"`
	Reserved       float64     `json:"reservedUSD"`
	StopReason     string      `json:"stopReason,omitempty"`
	LastCompletion time.Time   `json:"lastCompletionAt"`
}

func NewScheduler(n int, now time.Time, cpa RouteConfig, budget float64) *Scheduler {
	s := &Scheduler{CPA: cpa, API: RouteConfig{Concurrency: 4, Interval: time.Second}, Budget: budget}
	for i := 0; i < n; i++ {
		s.Items = append(s.Items, Item{ID: i, Status: "queued", Enqueued: now})
	}
	return s
}
func (s *Scheduler) route(name string) *RouteConfig {
	if name == "openai" {
		return &s.API
	}
	return &s.CPA
}
func ready(r *RouteConfig, now time.Time) bool {
	return !r.Disabled && r.Active < r.Concurrency && !now.Before(r.Next) && !now.Before(r.PauseUntil)
}
func (s *Scheduler) Admit(now time.Time, overflow bool) (Item, bool) {
	if s.StopReason != "" {
		return Item{}, false
	}
	for i := range s.Items {
		item := &s.Items[i]
		if item.Status != "queued" || now.Before(item.RetryAt) {
			continue
		}
		name := item.Route
		if name == "" {
			if ready(&s.CPA, now) {
				name = "cpa"
			} else if overflow && (now.Sub(item.Enqueued) >= 30*time.Second || s.CPA.PauseUntil.Sub(now) > 30*time.Second) {
				name = "openai"
			} else {
				continue
			}
		}
		r := s.route(name)
		if !ready(r, now) {
			continue
		}
		if name == "openai" {
			if s.Spent+s.Reserved+AttemptReservation > s.Budget+1e-12 {
				continue
			}
			s.Reserved += AttemptReservation
		}
		if !item.RetryAt.IsZero() {
			item.RetryWait += now.Sub(item.Started)
			item.RetryAt = time.Time{}
		}
		if item.Attempts == 0 {
			item.QueueWait = now.Sub(item.Enqueued)
		}
		item.Status = "active"
		item.Route = name
		item.Attempts++
		item.Started = now
		r.Active++
		s.PeakCPA = max(s.PeakCPA, s.CPA.Active)
		s.PeakAPI = max(s.PeakAPI, s.API.Active)
		r.Next = now.Add(r.Interval)
		return *item, true
	}
	return Item{}, false
}
func (s *Scheduler) Finish(id int, outcome Outcome, now time.Time) {
	item := &s.Items[id]
	if item.Status != "active" {
		return
	}
	r := s.route(item.Route)
	r.Active--
	if item.Route == "openai" && outcome.UsageKnown {
		s.Reserved = math.Max(0, s.Reserved-AttemptReservation)
		s.Spent += outcome.Cost
		if outcome.Cost > AttemptReservation {
			s.Stop("reservation_exceeded")
		}
	}
	item.Status = outcome.Kind
	if outcome.Kind == "completed" {
		r.Failures = 0
		s.LastCompletion = now
		return
	}
	if outcome.Kind == "policy" {
		s.Stop("account_or_policy_rejection")
		return
	}
	if outcome.Kind == "unconfirmed" {
		r.Failures++
		r.PauseUntil = maxTime(r.PauseUntil, now.Add(60*time.Second))
		if r.Concurrency == 1 {
			r.Disabled = true
		} else {
			r.Concurrency = max(1, r.Concurrency/2)
		}
		return
	}
	if outcome.Kind != "rejected" {
		return
	}
	r.Failures++
	delay := outcome.RetryAfter
	if delay <= 0 {
		delay = time.Second * time.Duration(1<<min(r.Failures, 6))
	}
	// Deterministic per-item jitter, bounded to 2 seconds, never shortens hints.
	delay += time.Duration((id*7919+item.Attempts*997)%2000) * time.Millisecond
	until := now.Add(delay)
	if until.After(r.PauseUntil) {
		r.PauseUntil = until
	}
	if r.Concurrency == 1 {
		r.Disabled = true
	} else {
		r.Concurrency = max(1, r.Concurrency/2)
	}
	r.Interval = min(30*time.Second, max(time.Second, r.Interval*2))
	if item.Attempts >= 3 {
		item.Status = "failed"
	} else {
		item.Status = "queued"
		item.RetryAt = until
		item.Started = now
	}
}
func (s *Scheduler) Stop(reason string) {
	if s.StopReason == "" {
		s.StopReason = reason
	}
}
func (s *Scheduler) Done() bool {
	for _, i := range s.Items {
		if i.Status == "queued" || i.Status == "active" {
			return false
		}
	}
	return true
}
func (s *Scheduler) Stalled(now time.Time) string {
	for _, i := range s.Items {
		if i.Status == "active" && now.Sub(i.Started) > 61*time.Second {
			return "overdue_request"
		}
	}
	for _, i := range s.Items {
		if i.Status == "queued" && !i.RetryAt.IsZero() && now.Sub(i.RetryAt) > 5*time.Second && ready(s.route(i.Route), now) {
			return "retry_eligible_without_dispatch"
		}
	}
	return ""
}

type Progress struct {
	PeakCPA        int         `json:"peakCPA"`
	PeakAPI        int         `json:"peakOpenAI"`
	Accepted       int         `json:"accepted"`
	Completed      int         `json:"completed"`
	Failed         int         `json:"failed"`
	Unconfirmed    int         `json:"unconfirmed"`
	QueuedCPA      int         `json:"queuedCPA"`
	QueuedAPI      int         `json:"queuedOpenAI"`
	ActiveCPA      int         `json:"activeCPA"`
	ActiveAPI      int         `json:"activeOpenAI"`
	Percent        float64     `json:"percentCompleted"`
	WaitingReason  string      `json:"waitingReason,omitempty"`
	CPA            RouteConfig `json:"cpa"`
	API            RouteConfig `json:"openai"`
	LastCompletion time.Time   `json:"lastCompletionAt"`
	Spent          float64     `json:"settledUSD"`
	Reserved       float64     `json:"reservedUSD"`
	Budget         float64     `json:"budgetUSD"`
	StopReason     string      `json:"stopReason,omitempty"`
}

func (s *Scheduler) Snapshot(now time.Time) Progress {
	p := Progress{PeakCPA: s.PeakCPA, PeakAPI: s.PeakAPI, Accepted: len(s.Items), ActiveCPA: s.CPA.Active, ActiveAPI: s.API.Active, CPA: s.CPA, API: s.API, LastCompletion: s.LastCompletion, Spent: s.Spent, Reserved: s.Reserved, Budget: s.Budget, StopReason: s.StopReason}
	for _, i := range s.Items {
		switch i.Status {
		case "completed":
			p.Completed++
		case "failed", "policy":
			p.Failed++
		case "unconfirmed":
			p.Unconfirmed++
		case "queued":
			if i.Route == "openai" {
				p.QueuedAPI++
			} else {
				p.QueuedCPA++
			}
		}
	}
	if p.Accepted > 0 {
		p.Percent = 100 * float64(p.Completed) / float64(p.Accepted)
	}
	switch {
	case s.StopReason != "":
		p.WaitingReason = s.StopReason
	case s.CPA.Disabled:
		p.WaitingReason = "cpa_baseline_paused"
	case now.Before(s.CPA.PauseUntil):
		p.WaitingReason = "provider_cooldown"
	case p.ActiveCPA+p.ActiveAPI > 0:
		p.WaitingReason = "in_flight_or_pacing"
	case s.Spent+s.Reserved+AttemptReservation > s.Budget:
		p.WaitingReason = "api_budget_exhausted"
	default:
		p.WaitingReason = "admission_pacing"
	}
	return p
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
