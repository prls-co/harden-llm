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

type NativePoint struct {
	Name        string    `json:"name"`
	Concurrency int       `json:"concurrency"`
	Accepted    int       `json:"accepted"`
	Dispatched  int       `json:"dispatched"`
	Completed   int       `json:"completed"`
	Rejections  int       `json:"rejections"`
	Unknown     int       `json:"unknown"`
	Failed      int       `json:"failed"`
	Policy      int       `json:"policy"`
	Active      int       `json:"active"`
	Peak        int       `json:"peak"`
	Started     time.Time `json:"startedAt"`
	Finished    time.Time `json:"finishedAt"`
	PauseUntil  time.Time `json:"pauseUntil"`
	State       string    `json:"state"`
	DurationMS  int64     `json:"durationMs"`
	Goodput     float64   `json:"successfulCallsPerSecond"`
	P50MS       int64     `json:"p50SuccessfulMs"`
	P95MS       int64     `json:"p95SuccessfulMs"`
}

type NativeBench struct {
	WindowBurst    int
	WindowDuration time.Duration
	Redactor       *redaction.Redactor
	Directory      string
	Events         io.Writer
	Started        time.Time
	Call           func(context.Context, string, int, int) NativeMeasurement
	FallbackPause  time.Duration
	Points         []NativePoint
}

func (b *NativeBench) emit(kind string, data any) error {
	raw, err := json.Marshal(map[string]any{"type": kind, "at": time.Now().UTC(), "elapsedMs": time.Since(b.Started).Milliseconds(), "data": data})
	if err != nil {
		return err
	}
	if b.Redactor != nil {
		raw, err = b.Redactor.JSON(raw)
		if err != nil {
			return err
		}
	}
	_, err = b.Events.Write(append(raw, '\n'))
	return err
}
func (b *NativeBench) checkpoint(p NativePoint) error {
	raw, err := json.MarshalIndent(map[string]any{"updatedAt": time.Now().UTC(), "current": p, "points": b.Points}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(b.Directory, "status.json")
	if err = os.WriteFile(path+".tmp", raw, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
func (b *NativeBench) Wait(ctx context.Context, name string, d time.Duration) error {
	p := NativePoint{Name: name, State: "waiting_for_quota_reset", Started: time.Now(), PauseUntil: time.Now().Add(d)}
	if err := b.emit("waiting", p); err != nil {
		return err
	}
	if err := b.checkpoint(p); err != nil {
		return err
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-tick.C:
			if err := b.emit("heartbeat", p); err != nil {
				return err
			}
			if err := b.checkpoint(p); err != nil {
				return err
			}
		}
	}
}

// Point fills a fixed-size native pool. A timeout remains terminal and never
// stops other independent items. Only explicit rejections may retry. After a
// throttle, shared cooldown admits one recovery probe before reopening the pool.
func (b *NativeBench) Point(ctx context.Context, name string, concurrency, n int, interval time.Duration, retry bool) (p NativePoint, err error) {
	if concurrency < 1 || concurrency > 1000 || n < 1 || n > 1000 || interval < 0 {
		return p, fmt.Errorf("invalid native point bounds")
	}
	p = NativePoint{Name: name, Concurrency: concurrency, Accepted: n, Started: time.Now(), State: "running"}
	type item struct {
		state   string
		attempt int
	}
	items := make([]item, n)
	for i := range items {
		items[i].state = "queued"
	}
	results := make(chan NativeMeasurement, concurrency)
	tick := time.NewTicker(time.Millisecond * 10)
	defer tick.Stop()
	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()
	var next time.Time
	var windowStart time.Time
	windowUsed := 0
	windowEnabled := retry && b.WindowBurst > 0 && b.WindowDuration > 0
	probing := false
	probeActive := false
	probeID := -1
	stopped := false
	done := ctx.Done()
	fallback := b.FallbackPause
	if fallback <= 0 {
		fallback = 2 * time.Second
	}
	backoff := fallback
	durations := []int64{}
	if err = b.emit("point_started", p); err != nil {
		return
	}
	if err = b.checkpoint(p); err != nil {
		return
	}
	// On artifact failure/cancellation, cancel and drain all owned workers.
	workersCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() {
		if err != nil {
			cancel()
			for p.Active > 0 {
				<-results
				p.Active--
			}
		}
	}()
	for {
		if ctx.Err() != nil {
			stopped = true
			p.State = "canceled"
		}
		if windowEnabled {
			now := time.Now()
			if windowStart.IsZero() || !now.Before(windowStart.Add(b.WindowDuration)) {
				windowStart = now
				windowUsed = 0
			}
			if windowUsed >= b.WindowBurst {
				p.PauseUntil = maxTime(p.PauseUntil, windowStart.Add(b.WindowDuration))
			}
		}
		for !stopped && p.Active < concurrency && !time.Now().Before(next) && !time.Now().Before(p.PauseUntil) && (!probing || !probeActive) {
			id := -1
			for i := range items {
				if items[i].state == "queued" {
					id = i
					break
				}
			}
			if id < 0 {
				break
			}
			items[id].state = "active"
			items[id].attempt++
			attempt := items[id].attempt
			p.Dispatched++
			if windowEnabled {
				windowUsed++
				if windowUsed >= b.WindowBurst {
					p.PauseUntil = maxTime(p.PauseUntil, windowStart.Add(b.WindowDuration))
				}
			}
			p.Active++
			p.Peak = max(p.Peak, p.Active)
			next = time.Now().Add(interval)
			if probing {
				probeActive = true
				probeID = id
			}
			if err = b.emit("call_started", map[string]any{"point": name, "id": id, "attempt": attempt, "active": p.Active}); err != nil {
				p.Active--
				return
			}
			prefix := b.Started.UTC().Format("20060102T150405.000000000") + "-" + name
			go func(id, attempt int) { results <- b.Call(workersCtx, prefix, id, attempt) }(id, attempt)
		}
		pending := false
		for _, i := range items {
			if i.state == "queued" {
				pending = true
				break
			}
		}
		if p.Active == 0 && (stopped || !pending) {
			break
		}
		select {
		case m := <-results:
			p.Active--
			items[m.ID].state = "done"
			isProbe := m.ID == probeID
			if isProbe {
				probeActive = false
				probeID = -1
			}
			switch m.Kind {
			case "completed":
				p.Completed++
				durations = append(durations, m.DurationMS)
				if probing && isProbe {
					probing = false
					backoff = fallback
				}
			case "rejected":
				p.Rejections++
				if retry && items[m.ID].attempt < 12 {
					items[m.ID].state = "queued"
					firstThrottle := !probing
					probing = true
					wait := time.Duration(m.RetryAfterMS) * time.Millisecond
					if wait <= 0 {
						wait = backoff
						if isProbe || firstThrottle {
							backoff = min(30*time.Second, backoff*2)
						}
					}
					p.PauseUntil = maxTime(p.PauseUntil, time.Now().Add(wait))
				}
			case "policy":
				p.Policy++
				p.State = "policy_stop"
				stopped = true
			case "unconfirmed":
				p.Unknown++
			default:
				p.Failed++
			}
			if err = b.emit("call_finished", map[string]any{"point": name, "measurement": m}); err != nil {
				return
			}
		case <-heartbeat.C:
			if err = b.emit("heartbeat", p); err != nil {
				return
			}
			if err = b.checkpoint(p); err != nil {
				return
			}
		case <-tick.C:
		case <-done:
			done = nil
			stopped = true
			p.State = "canceled"
		}
	}
	p.Finished = time.Now()
	p.DurationMS = p.Finished.Sub(p.Started).Milliseconds()
	if p.DurationMS > 0 {
		p.Goodput = float64(p.Completed) / p.Finished.Sub(p.Started).Seconds()
	}
	if p.State == "running" {
		p.State = "finished"
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	if len(durations) > 0 {
		p.P50MS = durations[len(durations)/2]
		p.P95MS = durations[min(len(durations)-1, len(durations)*95/100)]
	}
	b.Points = append(b.Points, p)
	if err = b.emit("point_finished", p); err != nil {
		return
	}
	err = b.checkpoint(p)
	return
}

func (b *NativeBench) Search(ctx context.Context, maximum, granularity int, quiet time.Duration) (int, int, error) {
	if maximum < 1 || maximum > 1000 || granularity < 1 || quiet < 0 {
		return 0, 0, fmt.Errorf("invalid search bounds")
	}
	low, high := 0, 0
	test := func(c int) (bool, error) {
		if c >= 64 {
			if err := b.Wait(ctx, fmt.Sprintf("quiet-before-%d", c), quiet); err != nil {
				return false, err
			}
		}
		p, err := b.Point(ctx, fmt.Sprintf("search-%d-%d", len(b.Points), c), c, c, 0, false)
		if err != nil {
			return false, err
		}
		if p.Policy > 0 {
			return false, fmt.Errorf("account/policy stop")
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// Only explicit throttles define this bracket. A timeout is an outlier,
		// not evidence of a concurrency rejection. Wrong results invalidate a point.
		if p.Failed > 0 || p.Completed == 0 && p.Rejections == 0 {
			return false, fmt.Errorf("native point could not qualify workload; see status.json")
		}
		return p.Rejections == 0, nil
	}
	for c := 1; ; c = min(maximum, c*2) {
		pass, err := test(c)
		if err != nil {
			return low, high, err
		}
		if !pass {
			high = c
			break
		}
		low = c
		if c == maximum {
			return low, 0, nil
		}
	}
	for high-low > granularity {
		mid := (low + high) / 2
		pass, err := test(mid)
		if err != nil {
			return low, high, err
		}
		if pass {
			low = mid
		} else {
			high = mid
		}
	}
	return low, high, b.emit("search_bracket", map[string]any{"low": low, "high": high, "granularity": granularity, "criterion": "no explicit throttles; unknown outliers retained separately"})
}
