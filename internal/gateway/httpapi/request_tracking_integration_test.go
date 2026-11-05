//go:build integration

package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-495

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/stateful"
)

type trackingSQLTrace struct {
	queries atomic.Int64
	t       *testing.T
}

func (s *trackingSQLTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	s.queries.Add(1)
	return ctx
}
func (s *trackingSQLTrace) failure(operation string, err error) {
	if err == nil {
		return
	}
	code := "other"
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		code = pgErr.Code
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = "context_deadline_exceeded"
	} else if errors.Is(err, context.Canceled) {
		code = "context_canceled"
	}
	// Deliberately omit queries, parameters, DSNs and arbitrary error text.
	s.t.Logf("tracking SQL %s failure: type=%T code=%s", operation, err, code)
}
func (s *trackingSQLTrace) TraceQueryEnd(_ context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	s.failure("query", data.Err)
}
func (s *trackingSQLTrace) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	return ctx
}
func (s *trackingSQLTrace) TraceAcquireEnd(_ context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	s.failure("acquire", data.Err)
}
func trackingHTTPPool(t *testing.T) (*pgxpool.Pool, *trackingSQLTrace) {
	t.Helper()
	_, dsn := integrationtest.PostgresLease(t)
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	trace := &trackingSQLTrace{t: t}
	config.ConnConfig.Tracer = trace
	config.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := stateful.ApplySchema(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return pool, trace
}
func lookupTracked(t *testing.T, client *http.Client, origin, key, id string) (int, stateful.RequestStatus) {
	t.Helper()
	r, _ := http.NewRequest("GET", origin+"/v1/requests/"+id, nil)
	r.Header.Set("Authorization", "Bearer "+key)
	response, err := client.Do(r)
	if err != nil {
		t.Error(err)
		return 0, stateful.RequestStatus{}
	}
	defer response.Body.Close()
	var view stateful.RequestStatus
	if response.StatusCode == 200 {
		if err := json.NewDecoder(response.Body).Decode(&view); err != nil {
			t.Error(err)
		}
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Error("lookup cached")
	}
	return response.StatusCode, view
}
func TestRequestTrackingCrossReplica(t *testing.T) {
	t.Parallel()
	pool, _ := trackingHTTPPool(t)
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once

	var count atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if count.Add(1) == 1 {
			close(started)
			<-release
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"fixture","status":"completed","output_text":"fixture answer","output":[{"type":"message","content":[{"type":"output_text","text":"fixture answer"}]}]}`)
	}))
	a, b := newContractAPIWithPool(t, provider, pool), newContractAPIWithPool(t, provider, pool)
	secondKey := "synthetic-other-key-0123456789abcdef"
	a.keys = append(a.keys, secondKey)
	b.keys = append(b.keys, secondKey)
	first, second := httptest.NewServer(a.Handler()), httptest.NewServer(b.Handler())
	defer func() { releaseOnce.Do(func() { close(release) }); first.Close(); second.Close(); provider.Close() }()
	id := uuid.NewString()
	done := make(chan error, 1)
	go func() {
		r, _ := http.NewRequest("POST", first.URL+"/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"hello"}`))
		r.Header.Set("Authorization", "Bearer "+proxyTestToken)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(trackingHeader, id)
		resp, err := first.Client().Do(r)
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				err = fmt.Errorf("POST returned %d", resp.StatusCode)
			}
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not started")
	}
	if status, view := lookupTracked(t, second.Client(), second.URL, proxyTestToken, id); status != 200 || view.Status != "running" || view.FinishedAt != nil {
		t.Fatal("B did not observe A running", status, view)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if status, view := lookupTracked(t, second.Client(), second.URL, proxyTestToken, id); status != 200 || view.Status != "completed" || view.OutcomeConfirmed == nil || !*view.OutcomeConfirmed {
		t.Fatal("B did not observe A completion", status, view)
	}
	errorBody := func(key, id string) string {
		r, _ := http.NewRequest("GET", second.URL+"/v1/requests/"+id, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		response, err := second.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != 404 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("cross-key/absent lookup contract", response.StatusCode, err)
		}
		return string(data)
	}
	if errorBody(secondKey, id) != errorBody(proxyTestToken, uuid.NewString()) {
		t.Fatal("other-key existence leaked")
	}
	request, _ := http.NewRequest("POST", second.URL+"/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"hello"}`))
	request.Header.Set("Authorization", "Bearer "+secondKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(trackingHeader, id)
	response, err := second.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("same UUID under another key conflicted", response.StatusCode)
	}
	if status, view := lookupTracked(t, second.Client(), second.URL, secondKey, id); status != 200 || view.Status != "completed" {
		t.Fatal("other-key independent request unavailable", status, view.Status)
	}
	var retained int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM request_tracking WHERE tracking_id=$1", id).Scan(&retained); err != nil || retained != 2 {
		t.Fatal("same UUID scopes were not independently stored", retained, err)
	}
	if count.Load() != 2 {
		t.Fatal("lookup/replication introduced inference", count.Load())
	}
}
func TestRequestTrackingScale(t *testing.T) {
	t.Parallel()
	pool, trace := trackingHTTPPool(t)
	var count atomic.Int32
	provider := trackingProvider(t, &count)
	a, b := newContractAPIWithPool(t, provider, pool), newContractAPIWithPool(t, provider, pool)
	secondKey := "synthetic-other-key-0123456789abcdef"
	a.keys = append(a.keys, secondKey)
	b.keys = append(b.keys, secondKey)
	first, second := httptest.NewServer(a.Handler()), httptest.NewServer(b.Handler())
	defer first.Close()
	defer second.Close()
	const calls = 1000
	var peak, active atomic.Int32
	for _, tracked := range []bool{false, true} {
		start := time.Now()
		a.trackingPeakCollectors.Store(0)
		var usageBefore syscall.Rusage
		syscall.Getrusage(syscall.RUSAGE_SELF, &usageBefore)
		beforeQueries := trace.queries.Load()
		beforeCalls := count.Load()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		work := make(chan int)
		var wg sync.WaitGroup
		for worker := 0; worker < 16; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range work {
					current := active.Add(1)
					for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
					}
					key := proxyTestToken
					if i%2 != 0 {
						key = secondKey
					}
					id := uuid.NewString()
					body := fmt.Sprintf(`{"model":"fixture-model","input":"private-prompt-sentinel-%d","harden":{"cache":"off"}}`, i)
					r, _ := http.NewRequest("POST", first.URL+"/v1/responses", strings.NewReader(body))
					r.Header.Set("Authorization", "Bearer "+key)
					r.Header.Set("Content-Type", "application/json")
					if tracked {
						r.Header.Set(trackingHeader, id)
					}
					resp, err := first.Client().Do(r)
					if err != nil {
						t.Error(err)
					} else {
						data, readErr := io.ReadAll(resp.Body)
						resp.Body.Close()
						if readErr != nil || resp.StatusCode != 200 || !strings.Contains(string(data), "fixture answer") {
							t.Error("generation failed", resp.StatusCode, readErr)
							stats := pool.Stat()
							t.Logf("tracking pool: total=%d acquired=%d idle=%d cancelled_acquires=%d empty_acquires=%d", stats.TotalConns(), stats.AcquiredConns(), stats.IdleConns(), stats.CanceledAcquireCount(), stats.EmptyAcquireCount())
						}
					}
					if tracked {
						status, view := lookupTracked(t, second.Client(), second.URL, key, id)
						if status != 200 || view.Status != "completed" || view.ID != id {
							t.Error("missing independent completed status", status, view.ID, view.Status)
						}
					}
					active.Add(-1)
				}
			}()
		}
		for i := 0; i < calls; i++ {
			work <- i
		}
		close(work)
		wg.Wait()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		if got := count.Load() - beforeCalls; got != calls {
			t.Fatal("tracking changed provider dispatch count", got)
		}
		var usageAfter syscall.Rusage
		syscall.Getrusage(syscall.RUSAGE_SELF, &usageAfter)
		cpuUS := (usageAfter.Utime.Sec-usageBefore.Utime.Sec+usageAfter.Stime.Sec-usageBefore.Stime.Sec)*1000000 + usageAfter.Utime.Usec - usageBefore.Utime.Usec + usageAfter.Stime.Usec - usageBefore.Stime.Usec
		t.Logf("tracked=%v calls=%d generator_peak=%d collector_peak=%d collectors_after=%d sql_operations=%d elapsed_ms=%d process_cpu_us=%d process_peak_rss_kib=%d process_allocated_bytes=%d goroutines_after=%d", tracked, calls, peak.Load(), a.trackingPeakCollectors.Load(), a.trackingActiveCollectors.Load(), trace.queries.Load()-beforeQueries, time.Since(start).Milliseconds(), cpuUS, usageAfter.Maxrss, after.TotalAlloc-before.TotalAlloc, runtime.NumGoroutine())
		if a.trackingActiveCollectors.Load() != 0 || a.trackingPeakCollectors.Load() > 16 {
			t.Fatal("collector leak or multiplicity")
		}
	}
	var rows int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM request_tracking WHERE status='completed' AND finished_at IS NOT NULL").Scan(&rows); err != nil || rows != calls {
		t.Fatal("incorrect distinct terminal count", rows, err)
	}
	for _, table := range []string{"profiles", "history", "drafts", "credentials", "cache_entries"} {
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&rows); err != nil || rows != 0 {
			t.Fatal("ordinary tracked inference wrote reference data", table, rows, err)
		}
	}
	var leaked bool
	if err := pool.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM request_tracking r WHERE row_to_json(r)::text LIKE '%private-prompt-sentinel%' OR row_to_json(r)::text LIKE '%fixture answer%' OR row_to_json(r)::text LIKE '%synthetic-upstream-secret%')").Scan(&leaked); err != nil || leaked {
		t.Fatal("tracking persisted payload/secret", err)
	}
}

func TestRequestTrackingRealCancellation(t *testing.T) {
	t.Parallel()
	pool, _ := trackingHTTPPool(t)
	started, providerStopped, handlerStopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	var count atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		count.Add(1)
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
		close(providerStopped)
	}))
	a, b := newContractAPIWithPool(t, provider, pool), newContractAPIWithPool(t, provider, pool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.Handler().ServeHTTP(w, r)
		if r.Method == "POST" {
			close(handlerStopped)
		}
	}))
	second := httptest.NewServer(b.Handler())
	defer func() { releaseOnce.Do(func() { close(release) }); server.Close(); second.Close(); provider.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := uuid.NewString()
	r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"hello"}`))
	r.Header.Set("Authorization", "Bearer "+proxyTestToken)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(trackingHeader, id)
	reply := make(chan error, 1)
	go func() {
		resp, err := server.Client().Do(r)
		if resp != nil {
			resp.Body.Close()
		}
		reply <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider never started")
	}
	cancel()
	for _, ack := range []<-chan struct{}{providerStopped, handlerStopped} {
		select {
		case <-ack:
		case <-time.After(5 * time.Second):
			t.Fatal("cancellation did not terminate local transport/handler")
		}
	}
	if err := <-reply; err == nil {
		t.Fatal("client cancellation unexpectedly delivered a response")
	}
	status, view := lookupTracked(t, second.Client(), second.URL, proxyTestToken, id)
	if status != 200 || view.Status != "unconfirmed" || view.OutcomeConfirmed == nil || *view.OutcomeConfirmed || count.Load() != 1 {
		t.Fatal("cancelled dispatched execution was replayed or falsely confirmed", status, view.Status, count.Load())
	}
}

func TestRequestTrackingCacheIdentityAndLostObservation(t *testing.T) {
	t.Parallel()
	pool, _ := trackingHTTPPool(t)
	var count atomic.Int32
	api := newContractAPIWithPool(t, trackingProvider(t, &count), pool)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	for i := 0; i < 2; i++ {
		id := uuid.NewString()
		r, _ := http.NewRequest("POST", server.URL+"/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"hello","harden":{"cache":"cache"}}`))
		r.Header.Set("Authorization", "Bearer "+proxyTestToken)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(trackingHeader, id)
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("tracked cached request failed", response.StatusCode)
		}
		if status, view := lookupTracked(t, server.Client(), server.URL, proxyTestToken, id); status != 200 || view.Status != "completed" {
			t.Fatal("cache hit status not completed")
		}
	}
	if count.Load() != 1 {
		t.Fatal("tracking ID entered cache identity", count.Load())
	}
	now := time.Now()
	lost, _ := stateful.NewTrackingRecord(uuid.NewString(), "/v1/responses", now.Add(-3*time.Minute), now.Add(-time.Minute))
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(proxyTestToken)))
	if err := api.service.ReserveTracking(context.Background(), hash, lost); err != nil {
		t.Fatal(err)
	}
	if status, view := lookupTracked(t, server.Client(), server.URL, proxyTestToken, lost.ID); status != 200 || view.Status != "unconfirmed" || !view.ObservationStale || view.FinishedAt != nil {
		t.Fatal("lost execution falsely finalized", status, view.Status)
	}
	var status string
	if err := pool.QueryRow(context.Background(), "SELECT status FROM request_tracking WHERE api_key_hash=$1 AND tracking_id=$2", hash, lost.ID).Scan(&status); err != nil || status != "running" {
		t.Fatal("GET changed persisted execution", status, err)
	}
	if count.Load() != 1 {
		t.Fatal("GET recovered/replayed lost inference")
	}
}

func TestRequestTrackingActualSocketWriteFailure(t *testing.T) {
	t.Parallel()
	pool, _ := trackingHTTPPool(t)
	var count atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		text := strings.Repeat("x", 256<<10)
		json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "status": "completed", "output_text": text, "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": text}}}}})
	}))
	defer provider.Close()
	api := newContractAPIWithPool(t, provider, pool)
	ready, allowWrite, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	writeErrors := make(chan error, 1)
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			api.Handler().ServeHTTP(w, r)
			return
		}
		api.Handler().ServeHTTP(&trackingSocketWriter{ResponseWriter: w, ready: ready, allow: allowWrite, errors: writeErrors}, r)
		close(done)
	}))
	defer func() { once.Do(func() { close(allowWrite) }); server.Close() }()
	raw, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	id := uuid.NewString()
	body := `{"model":"fixture-model","input":"hello"}`
	if _, err := fmt.Fprintf(raw, "POST /v1/responses HTTP/1.1\r\nHost: fixture\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nX-HLLM-Tracking-ID: %s\r\nContent-Length: %d\r\n\r\n%s", proxyTestToken, id, len(body), body); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("completed projection never reached response headers")
	}
	// Completion must already be durable before the first response byte. Use a
	// fresh HTTP connection to inspect the real database, then reset the socket.
	if status, view := lookupTracked(t, server.Client(), server.URL, proxyTestToken, id); status != 200 || view.Status != "completed" {
		t.Fatal("response projection was not committed before delivery", status, view.Status)
	}
	if err := raw.(*net.TCPConn).SetLinger(0); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	once.Do(func() { close(allowWrite) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("response writer did not stop")
	}
	select {
	case err := <-writeErrors:
		if err == nil {
			t.Fatal("actual socket write failure was not exercised")
		}
	default:
		t.Fatal("no actual transport write result")
	}
	if status, view := lookupTracked(t, server.Client(), server.URL, proxyTestToken, id); status != 200 || view.Status != "completed" || count.Load() != 1 {
		t.Fatal("delivery ambiguity erased/replayed known completion", status, view.Status, count.Load())
	}
}

type trackingSocketWriter struct {
	http.ResponseWriter
	ready  chan struct{}
	allow  <-chan struct{}
	errors chan error
}

func (w *trackingSocketWriter) WriteHeader(status int) {
	close(w.ready)
	<-w.allow
	w.ResponseWriter.WriteHeader(status)
}
func (w *trackingSocketWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.errors <- err
	return n, err
}
