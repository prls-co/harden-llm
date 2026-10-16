package livecapacity

// This experiment uses the production admission controller without Temporal.
// Its results establish controller/native behavior, not Temporal throughput.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/prls-co/harden-llm/stateful/batch"
)

const strategyReservation int64 = 32768
const strategyUnknownReservation int64 = 32768

type StrategyActor struct {
	Name           string       `json:"name"`
	Policy         batch.Policy `json:"policy"`
	ShareWithFirst bool         `json:"shareWithFirst"`
}

// StrategyWorkload offers the same independent competing demand in every case:
// 100 items before the foreground starts, then one per second for 100 seconds.
// Offered times do not depend on either controller's success or refusal rate.
func StrategyWorkload(p batch.Policy, calls int, competing, shared bool) ([]StrategyActor, []StrategyItem) {
	actors := []StrategyActor{{Name: "foreground", Policy: p}}
	var items []StrategyItem
	foregroundAt := time.Duration(0)
	if competing {
		foregroundAt = 15 * time.Second
	}
	for range calls {
		items = append(items, StrategyItem{Actor: 0, ReadyAt: foregroundAt})
	}
	if competing {
		background := p
		if !shared {
			background.Concurrency = 64
			background.Burst = 100
			background.StartRate = 1
			background.MaximumRate = 1
			background.WindowRequests = 0
		}
		actors = append(actors, StrategyActor{Name: "background", Policy: background, ShareWithFirst: shared})
		for i := range 200 {
			at := time.Duration(0)
			if i >= 100 {
				at = 45*time.Second + time.Duration(i-100)*time.Second
			}
			items = append(items, StrategyItem{Actor: 1, ReadyAt: at})
		}
	}
	return actors, items
}

type StrategyItem struct {
	Actor       int
	ReadyAt     time.Duration
	ID, Attempt int
	state       string
	probe       bool
	reserved    time.Time
}
type StrategyActorStats struct {
	Name                                                                            string `json:"name"`
	Offered, Dispatched, Completed, Rejected, Unknown, Failed, Policy, Active, Peak int
	InputTokens, OutputTokens                                                       int64
	FirstStart, LastFinish                                                          time.Time
	ReadyMs                                                                         int64
}
type StrategySnapshot struct {
	At                                                                   time.Time `json:"at"`
	ElapsedMs                                                            int64     `json:"elapsedMs"`
	StopReason                                                           string    `json:"stopReason,omitempty"`
	Active, Peak                                                         int
	ReportedTokens, UnknownTokenReserve, ActiveTokenReserve, TokenBudget int64
	Actors                                                               []StrategyActorStats
	Controllers                                                          []batch.Controller
}
type StrategyEngine struct {
	Started                               time.Time
	Actors                                []StrategyActor
	Items                                 []StrategyItem
	Controllers                           []batch.Controller
	Active, Peak                          int
	StopReason                            string
	Budget, ReportedTokens, UnknownTokens int64
	stats                                 []StrategyActorStats
	control, controlActive                []int
	turn                                  int
}

func NewStrategyEngine(at time.Time, actors []StrategyActor, items []StrategyItem, budget int64) (*StrategyEngine, error) {
	if len(actors) < 1 || len(actors) > 16 || len(items) < 1 || len(items) > 20000 || budget < 1 || budget > 10000000 {
		return nil, fmt.Errorf("invalid bounded strategy experiment")
	}
	e := &StrategyEngine{Started: at, Actors: append([]StrategyActor(nil), actors...), Items: append([]StrategyItem(nil), items...), Budget: budget}
	for i, a := range actors {
		if a.Name == "" || a.Policy.Validate() != nil {
			return nil, fmt.Errorf("invalid strategy actor")
		}
		e.stats = append(e.stats, StrategyActorStats{Name: a.Name})
		if a.ShareWithFirst {
			if i == 0 || a.Policy != actors[0].Policy {
				return nil, fmt.Errorf("shared actors require the same policy")
			}
			e.control = append(e.control, 0)
		} else {
			e.control = append(e.control, len(e.Controllers))
			e.Controllers = append(e.Controllers, batch.NewController(a.Policy))
			e.controlActive = append(e.controlActive, 0)
		}
	}
	for i := range e.Items {
		v := &e.Items[i]
		if v.Actor < 0 || v.Actor >= len(actors) || v.ReadyAt < 0 || v.ReadyAt > time.Hour {
			return nil, fmt.Errorf("invalid scheduled item")
		}
		v.ID, v.Attempt, v.state = i, 0, "queued"
		e.stats[v.Actor].Offered++
	}
	return e, nil
}

func (e *StrategyEngine) Take(now time.Time) (StrategyItem, bool) {
	if e.StopReason != "" {
		return StrategyItem{}, false
	}
	for n := range e.Actors {
		a := (e.turn + n) % len(e.Actors)
		ci := e.control[a]
		c := &e.Controllers[ci]
		if !c.CanStart(now, e.controlActive[ci]) {
			continue
		}
		for i := range e.Items {
			v := &e.Items[i]
			if v.Actor != a || v.state != "queued" || now.Before(e.Started.Add(v.ReadyAt)) {
				continue
			}
			if e.ReportedTokens+e.UnknownTokens+int64(e.Active+1)*strategyReservation > e.Budget {
				// Let active calls settle before treating their reservation as a
				// budget exhaustion. Unknown completions retain a larger reserve.
				if e.Active == 0 {
					e.StopReason = "token_budget"
				}
				return StrategyItem{}, false
			}
			v.Attempt++
			v.state = "active"
			v.probe = c.Cooling
			v.reserved = now
			c.Start(now)
			e.Active++
			e.Peak = max(e.Peak, e.Active)
			e.controlActive[ci]++
			s := &e.stats[a]
			s.Active++
			s.Dispatched++
			s.Peak = max(s.Peak, s.Active)
			if s.FirstStart.IsZero() {
				s.FirstStart = now
			}
			e.turn = (a + 1) % len(e.Actors)
			return *v, true
		}
	}
	return StrategyItem{}, false
}

func (e *StrategyEngine) Finish(m NativeMeasurement) {
	if m.ID < 0 || m.ID >= len(e.Items) {
		e.StopReason = "invalid_measurement_identity"
		return
	}
	v := &e.Items[m.ID]
	if v.state != "active" || m.Attempt != v.Attempt {
		e.StopReason = "invalid_measurement_identity"
		return
	}
	e.Active--
	ci := e.control[v.Actor]
	e.controlActive[ci]--
	s := &e.stats[v.Actor]
	s.Active--
	v.state = "done"
	e.ReportedTokens += max(0, m.InputTokens) + max(0, m.OutputTokens)
	s.InputTokens += max(0, m.InputTokens)
	s.OutputTokens += max(0, m.OutputTokens)
	if m.Finished.After(s.LastFinish) {
		s.LastFinish = m.Finished
	}
	if m.InputTokens+m.OutputTokens > strategyReservation {
		e.StopReason = "attempt_token_reservation_exceeded"
	}
	switch m.Kind {
	case "completed":
		s.Completed++
	case "rejected":
		s.Rejected++
		if v.Attempt < 16 {
			v.state = "queued"
		} else {
			s.Failed++
			e.StopReason = "refusal_attempt_limit"
		}
	case "policy":
		s.Policy++
		e.StopReason = "account_restriction"
	case "failed":
		s.Failed++
	default:
		s.Unknown++
		e.UnknownTokens += strategyUnknownReservation
		unknown := 0
		for _, a := range e.stats {
			unknown += a.Unknown
		}
		if unknown >= 5 {
			e.StopReason = "substantial_unknown_outcomes"
		}
	}
	dispatched := m.Started
	if dispatched.IsZero() {
		dispatched = v.reserved
	}
	e.Controllers[ci].Complete(m.Finished, dispatched, v.reserved, v.probe, m.Kind, time.Duration(m.RetryAfterMS)*time.Millisecond)
}

func (e *StrategyEngine) Done() bool {
	if e.Active > 0 {
		return false
	}
	if e.StopReason != "" {
		return true
	}
	for _, v := range e.Items {
		if v.state == "queued" {
			return false
		}
	}
	return true
}
func (e *StrategyEngine) Snapshot(now time.Time) StrategySnapshot {
	s := StrategySnapshot{At: now, ElapsedMs: now.Sub(e.Started).Milliseconds(), StopReason: e.StopReason, Active: e.Active, Peak: e.Peak, ReportedTokens: e.ReportedTokens, UnknownTokenReserve: e.UnknownTokens, ActiveTokenReserve: int64(e.Active) * strategyReservation, TokenBudget: e.Budget, Actors: append([]StrategyActorStats(nil), e.stats...), Controllers: append([]batch.Controller(nil), e.Controllers...)}
	for a := range s.Actors {
		first := time.Duration(1<<63 - 1)
		for _, v := range e.Items {
			if v.Actor == a {
				first = min(first, v.ReadyAt)
			}
		}
		if !s.Actors[a].LastFinish.IsZero() {
			s.Actors[a].ReadyMs = max(0, s.Actors[a].LastFinish.Sub(e.Started.Add(first)).Milliseconds())
		}
	}
	return s
}

// RunStrategy owns all calls and drains them on cancellation or artifact errors.
// Events contain bounded measurements only; callers keep request-level files private.
func RunStrategy(ctx context.Context, e *StrategyEngine, prefix string, call func(context.Context, string, int, int) NativeMeasurement, events io.Writer, progress func(StrategySnapshot) error) (runErr error) {
	owned, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan NativeMeasurement, len(e.Items))
	write := func(kind string, v any) error {
		return json.NewEncoder(events).Encode(map[string]any{"type": kind, "at": time.Now().UTC(), "data": v})
	}
	defer func() {
		cancel()
		for e.Active > 0 {
			m := <-results
			e.Finish(m)
			_ = write("call_finished", m)
		}
		if progress != nil {
			if err := progress(e.Snapshot(time.Now())); runErr == nil {
				runErr = err
			}
		}
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()
	for !e.Done() {
		if ctx.Err() != nil {
			e.StopReason = "canceled"
			return ctx.Err()
		}
		for {
			v, ok := e.Take(time.Now())
			if !ok {
				break
			}
			if err := write("call_started", map[string]any{"id": v.ID, "actor": e.Actors[v.Actor].Name, "attempt": v.Attempt, "probe": v.probe}); err != nil {
				e.Active--
				e.controlActive[e.control[v.Actor]]--
				e.stats[v.Actor].Active--
				e.Items[v.ID].state = "not_dispatched"
				e.StopReason = "artifact_write_failed"
				return err
			}
			go func(v StrategyItem) {
				m := call(owned, prefix, v.ID, v.Attempt)
				if m.ID != v.ID || m.Attempt != v.Attempt {
					m = NativeMeasurement{ID: v.ID, Attempt: v.Attempt, Kind: "unconfirmed", Code: "invalid_measurement_identity", Started: v.reserved, Finished: time.Now()}
				}
				results <- m
			}(v)
		}
		select {
		case m := <-results:
			e.Finish(m)
			if err := write("call_finished", m); err != nil {
				e.StopReason = "artifact_write_failed"
				return err
			}
		case <-heartbeat.C:
			if progress != nil {
				if err := progress(e.Snapshot(time.Now())); err != nil {
					e.StopReason = "artifact_write_failed"
					return err
				}
			}
		case <-tick.C:
		case <-ctx.Done():
			e.StopReason = "canceled"
			return ctx.Err()
		}
	}
	if e.StopReason != "" {
		return fmt.Errorf("strategy experiment stopped: %s", e.StopReason)
	}
	return nil
}
