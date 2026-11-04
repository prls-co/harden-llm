package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
)

const trackingHeader = "X-HLLM-Tracking-ID"

type trackingStore interface {
	ReserveTracking(context.Context, string, stateful.TrackingRecord) error
	UpdateTracking(context.Context, string, stateful.TrackingRecord) error
	LookupTracking(context.Context, string, string) (stateful.TrackingRecord, error)
}
type trackingProgressKey struct{}

type trackedCall struct {
	api              *API
	scope            string
	session          *stateful.TrackingSession
	progress         chan hardenllm.ProgressEvent
	stop             chan struct{}
	done             chan struct{}
	cancelFlush      context.CancelFunc
	closeOnce        sync.Once
	shutdownDeadline time.Time
	ticks            <-chan time.Time
	callID           string
	traceID          string
	retryWaitMS      int64
}

func (api *API) requestStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("trackingID")
	if !stateful.ValidTrackingID(id) {
		writeError(w, 400, "invalid_request_error", "invalid_tracking_id", "A canonical nonnil UUID is required.", "trackingID")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "invalid_request_error", "unsupported_query", "Query parameters are not supported.", nil)
		return
	}
	record, err := api.tracking.LookupTracking(r.Context(), scopeFromContext(r.Context()).hash, id)
	if errors.Is(err, stateful.ErrTrackingNotFound) {
		writeError(w, 404, "invalid_request_error", "request_not_found", "The request was not found.", nil)
		return
	}
	if err != nil {
		writeError(w, 503, "server_error", "request_status_unavailable", "Request status is unavailable.", nil)
		return
	}
	view := record.Observe(time.Now())
	if view.Status == "running" || view.Status == "waiting" {
		w.Header().Set("Retry-After", "2")
	}
	writeJSON(w, 200, view, maximumResponseBytes)
}

func (api *API) executionTimeout(timeoutMS int64) time.Duration {
	if timeoutMS == 0 {
		return api.maxRunDuration
	}
	return min(time.Duration(timeoutMS)*time.Millisecond, api.maxRunDuration)
}
func (api *API) beginTrackedCall(w http.ResponseWriter, r *http.Request, timeoutMS int64) (context.Context, *trackedCall, context.CancelFunc, bool) {
	var values []string
	present := false
	for name, entries := range r.Header {
		if strings.EqualFold(name, trackingHeader) {
			present = true
			values = append(values, entries...)
		}
	}
	if !present {
		return r.Context(), nil, func() {}, true
	}
	if len(values) != 1 || !stateful.ValidTrackingID(values[0]) {
		writeError(w, 400, "invalid_request_error", "invalid_tracking_id", "Supply one canonical nonnil UUID.", trackingHeader)
		return r.Context(), nil, func() {}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.executionTimeout(timeoutMS))
	deadline, _ := ctx.Deadline()
	record, err := stateful.NewTrackingRecord(values[0], r.URL.Path, time.Now(), deadline)
	if err == nil {
		err = api.tracking.ReserveTracking(ctx, scopeFromContext(ctx).hash, record)
	}
	if err != nil {
		cancel()
		if errors.Is(err, stateful.ErrTrackingConflict) {
			w.Header().Set("x-should-retry", "false")
			writeError(w, 409, "invalid_request_error", "tracking_id_conflict", "The tracking ID is already retained. Use a fresh ID for a new request.", trackingHeader)
		} else {
			writeError(w, 503, "server_error", "request_status_unavailable", "Request tracking could not be registered.", nil)
		}
		return r.Context(), nil, func() {}, false
	}
	session := stateful.NewTrackingSession(record, time.Now)
	flushCtx, cancelFlush := context.WithCancel(context.WithoutCancel(ctx))
	call := &trackedCall{api: api, scope: scopeFromContext(ctx).hash, session: session, progress: make(chan hardenllm.ProgressEvent, 64), stop: make(chan struct{}), done: make(chan struct{}), cancelFlush: cancelFlush}
	ctx = stateful.WithTrackingSession(ctx, session)
	ctx = context.WithValue(ctx, trackingProgressKey{}, (chan<- hardenllm.ProgressEvent)(call.progress))
	api.logger.InfoContext(ctx, "request tracking registered", "endpoint", record.Endpoint)
	go call.collect(flushCtx, deadline)
	return ctx, call, cancel, true
}
func (c *trackedCall) collect(ctx context.Context, deadline time.Time) {
	defer close(c.done)
	active := c.api.trackingActiveCollectors.Add(1)
	defer c.api.trackingActiveCollectors.Add(-1)
	for old := c.api.trackingPeakCollectors.Load(); active > old && !c.api.trackingPeakCollectors.CompareAndSwap(old, active); old = c.api.trackingPeakCollectors.Load() {
	}
	ticks := c.ticks
	if ticks == nil && c.api.trackingTicks != nil {
		var stop func()
		ticks, stop = c.api.trackingTicks()
		defer stop()
	}
	if ticks == nil {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		ticks = ticker.C
	}
	watchdog := time.NewTimer(max(time.Until(deadline.Add(time.Second)), 0))
	defer watchdog.Stop()
	drain := func() {
		for {
			select {
			case p := <-c.progress:
				c.session.Progress(p)
			default:
				return
			}
		}
	}
	for {
		select {
		case p := <-c.progress:
			c.session.Progress(p)
		case <-ticks:
			if err := c.api.tracking.UpdateTracking(ctx, c.scope, c.session.Snapshot(true)); err != nil {
				c.api.logger.Warn("request tracking snapshot unavailable", "endpoint", c.session.Snapshot(false).Endpoint)
			}
		case <-c.stop:
			drain()
			finalCtx, cancel := context.WithDeadline(context.Background(), c.shutdownDeadline)
			err := c.api.tracking.UpdateTracking(finalCtx, c.scope, c.session.Snapshot(false))
			cancel()
			record := c.session.Snapshot(false)
			c.api.logger.Info("request tracking finished", "endpoint", record.Endpoint, "status", record.Status, "elapsed_ms", record.Observe(time.Now()).ElapsedMS, "metadata_saved", err == nil, "call_id", c.callID, "trace_id", c.traceID, "retry_wait_ms", c.retryWaitMS)
			return
		case <-watchdog.C:
			// Missing owner completion stays stale/unknown. Observation never extends
			// execution, declares transport closure or starts a replacement call.
			return
		}
	}
}
func (c *trackedCall) close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		c.shutdownDeadline = time.Now().Add(600 * time.Millisecond)
		if c.session.Snapshot(false).FinishedAt == nil {
			c.session.Finish("unconfirmed", "execution_unobserved", false)
		}
		c.cancelFlush()
		close(c.stop)
		<-c.done
	})
}
func (c *trackedCall) observeResult(result hardenllm.Result) {
	if c == nil {
		return
	}
	c.session.Attempt(max(len(result.Attempts), result.Diagnostics.AttemptsUsed))
	c.callID = result.CallID
	c.traceID = result.TraceID
	c.retryWaitMS = result.Diagnostics.TotalActualWaitMs
}
func (c *trackedCall) fail(err error, result hardenllm.Result) {
	if c == nil {
		return
	}
	c.session.Attempt(len(result.Attempts))
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code := "request_canceled"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "request_timeout"
		}
		if c.session.ProviderEntered() {
			c.session.Finish("unconfirmed", code, false)
		} else {
			c.session.Finish("cancelled", code, true)
		}
		return
	}
	var p *hardenllm.ProviderError
	var uncertain interface{ InferenceUnconfirmed() bool }
	if c.session.ProviderEntered() && ((errors.As(err, &uncertain) && uncertain.InferenceUnconfirmed()) || (errors.As(err, &p) && (p.Status == 0 || p.Status >= 500 || p.Code == "execution_unconfirmed"))) {
		c.session.Finish("unconfirmed", "upstream_error", false)
		return
	}
	c.session.Finish("failed", "execution_failed", true)
}
func (c *trackedCall) project(response any) {
	if c == nil {
		return
	}
	c.session.Phase("response_projection")
	data, err := json.Marshal(response)
	if err != nil || len(data) > maximumResponseBytes {
		c.session.Finish("failed", "response_projection_error", true)
		return
	}
	c.session.Finish("completed", "", true)
	c.close()
}
func trackingLookupRoute(path string) bool { return strings.HasPrefix(path, "/v1/requests/") }
