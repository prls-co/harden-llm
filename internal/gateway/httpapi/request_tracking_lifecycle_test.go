package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-493

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
)

type trackingStoreStub struct {
	reserve func(context.Context, string, stateful.TrackingRecord) error
	update  func(context.Context, string, stateful.TrackingRecord) error
	lookup  func(context.Context, string, string) (stateful.TrackingRecord, error)
}

func (s trackingStoreStub) ReserveTracking(ctx context.Context, key string, r stateful.TrackingRecord) error {
	return s.reserve(ctx, key, r)
}
func (s trackingStoreStub) UpdateTracking(ctx context.Context, key string, r stateful.TrackingRecord) error {
	return s.update(ctx, key, r)
}
func (s trackingStoreStub) LookupTracking(ctx context.Context, key, id string) (stateful.TrackingRecord, error) {
	return s.lookup(ctx, key, id)
}

func trackingProvider(t *testing.T, count *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Header.Get(trackingHeader) != "" {
			t.Error("tracking header forwarded upstream")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"fixture","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture answer"}]}],"output_text":"fixture answer","usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}`)
	}))
	t.Cleanup(server.Close)
	return server
}
func trackedRequest(api *API, id string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"hello","harden":{"cache":"off"}}`))
	r.Header.Set("Authorization", "Bearer "+proxyTestToken)
	r.Header.Set("Content-Type", "application/json")
	if id != "absent" {
		r.Header.Set(trackingHeader, id)
	}
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)
	return w
}
func TestRequestTrackingRegistration(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	api := newContractAPI(t, trackingProvider(t, &count))
	var reserves atomic.Int32
	api.tracking = trackingStoreStub{reserve: func(context.Context, string, stateful.TrackingRecord) error {
		reserves.Add(1)
		return stateful.ErrTrackingConflict
	}}
	for _, id := range []string{"", "bad", "00000000-0000-0000-0000-000000000000", "A60F21DA-604F-4FE3-A039-23382EC6B313"} {
		if w := trackedRequest(api, id); w.Code != 400 {
			t.Errorf("invalid ID %q status=%d body=%s", id, w.Code, w.Body)
		}
	}
	if reserves.Load() != 0 || count.Load() != 0 {
		t.Fatal("invalid header dispatched registration/inference")
	}
	w := trackedRequest(api, "a60f21da-604f-4fe3-a039-23382ec6b313")
	if w.Code != 409 || w.Header().Get("x-should-retry") != "false" || count.Load() != 0 {
		t.Fatal("duplicate dispatched inference or allowed SDK retry")
	}
	if w := trackedRequest(api, "absent"); w.Code != 200 || reserves.Load() != 1 || count.Load() != 1 {
		t.Fatal("untracked call used tracking or changed inference", w.Code, w.Body)
	}
}
func TestRequestTrackingCompletionFailureIsolation(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	api := newContractAPI(t, trackingProvider(t, &count))
	var final stateful.TrackingRecord
	var reserved stateful.TrackingRecord
	var writes int
	api.tracking = trackingStoreStub{
		reserve: func(ctx context.Context, key string, r stateful.TrackingRecord) error {
			if count.Load() != 0 {
				t.Error("inference preceded registration")
			}
			reserved = r
			return nil
		},
		update: func(ctx context.Context, key string, r stateful.TrackingRecord) error {
			writes++
			final = r
			return errors.New("synthetic storage outage")
		},
	}
	w := trackedRequest(api, "a60f21da-604f-4fe3-a039-23382ec6b313")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "fixture answer") || count.Load() != 1 || writes != 1 {
		t.Fatal("metadata failure replaced/retried result", w.Code, count.Load(), writes)
	}
	if final.Status != "completed" || final.FinishedAt == nil || final.OwnerID != reserved.OwnerID || final.Revision <= reserved.Revision {
		t.Fatal("completion not finalized/fenced")
	}
	encoded, _ := json.Marshal(final.Observe(time.Now()))
	if strings.Contains(string(encoded), "fixture answer") || strings.Contains(string(encoded), "hello") || strings.Contains(string(encoded), reserved.OwnerID) {
		t.Fatal("tracking retained payload/owner")
	}
}
func TestRequestTrackingSDKDuplicateDoesNotRetry(t *testing.T) {
	t.Parallel()
	var count, submissions atomic.Int32
	api := newContractAPI(t, trackingProvider(t, &count))
	api.tracking = trackingStoreStub{reserve: func(context.Context, string, stateful.TrackingRecord) error {
		submissions.Add(1)
		return stateful.ErrTrackingConflict
	}}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	// Keep real SDK default retries: a terminal ID conflict must disable them.
	client := openai.NewClient(option.WithAPIKey(proxyTestToken), option.WithBaseURL(server.URL+"/v1"))
	_, err := client.Responses.New(context.Background(), responses.ResponseNewParams{Model: "fixture-model", Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("hello")}}, option.WithHeader(trackingHeader, "a60f21da-604f-4fe3-a039-23382ec6b313"))
	if err == nil || submissions.Load() != 1 || count.Load() != 0 {
		t.Fatal("SDK retried duplicate tracking ID", err, submissions.Load(), count.Load())
	}
}

func TestRequestTrackingLookupIsolation(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	api := newContractAPI(t, trackingProvider(t, &count))
	api.tracking = trackingStoreStub{lookup: func(context.Context, string, string) (stateful.TrackingRecord, error) {
		return stateful.TrackingRecord{}, stateful.ErrTrackingNotFound
	}}
	for _, path := range []string{"/v1/requests/a60f21da-604f-4fe3-a039-23382ec6b313", "/v1/requests/bad", "/v1/requests/a60f21da-604f-4fe3-a039-23382ec6b313?owner=x", "/v1/requests/a60f21da-604f-4fe3-a039-23382ec6b313?x=1"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+proxyTestToken)
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		want := 400
		if path == "/v1/requests/a60f21da-604f-4fe3-a039-23382ec6b313" {
			want = 404
		}
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("lookup auth/validation/cache changed", path, w.Code, w.Header())
		}
	}
	if count.Load() != 0 {
		t.Fatal("GET performed inference")
	}
}

func TestRequestTrackingFinalBeforeDelivery(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	api := newContractAPI(t, trackingProvider(t, &count))
	var mu sync.Mutex
	var final bool
	api.tracking = trackingStoreStub{reserve: func(context.Context, string, stateful.TrackingRecord) error { return nil }, update: func(_ context.Context, _ string, r stateful.TrackingRecord) error {
		mu.Lock()
		final = r.Status == "completed"
		mu.Unlock()
		return nil
	}}
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"hello"}`))
	r.Header.Set("Authorization", "Bearer "+proxyTestToken)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(trackingHeader, "a60f21da-604f-4fe3-a039-23382ec6b313")
	w := &deliveryFailureWriter{header: http.Header{}, before: func() {
		mu.Lock()
		defer mu.Unlock()
		if !final {
			t.Error("completion not persisted before delivery began")
		}
	}}
	api.Handler().ServeHTTP(w, r)
	if count.Load() != 1 {
		t.Fatal("delivery error replayed inference")
	}
}

type deliveryFailureWriter struct {
	header http.Header
	before func()
}

func (w *deliveryFailureWriter) Header() http.Header       { return w.header }
func (w *deliveryFailureWriter) WriteHeader(int)           {}
func (w *deliveryFailureWriter) Write([]byte) (int, error) { w.before(); return 0, io.ErrClosedPipe }

func TestRequestTrackingHeaderMultiplicity(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	api := newContractAPI(t, trackingProvider(t, &count))
	var registrations atomic.Int32
	api.tracking = trackingStoreStub{reserve: func(context.Context, string, stateful.TrackingRecord) error {
		registrations.Add(1)
		return stateful.ErrTrackingConflict
	}}
	id := "a60f21da-604f-4fe3-a039-23382ec6b313"
	for _, values := range [][]string{{id, id}, {id + "," + id}, {""}} {
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"hello"}`))
		r.Header.Set("Authorization", "Bearer "+proxyTestToken)
		r.Header.Set("Content-Type", "application/json")
		r.Header[trackingHeader] = values
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("duplicate/empty tracking header accepted", values, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"hello"}`))
	r.Header.Set("Authorization", "Bearer "+proxyTestToken)
	r.Header.Set("Content-Type", "application/json")
	r.Header["x-hllm-tracking-id"] = []string{id}
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)
	if w.Code != 409 || registrations.Load() != 1 || count.Load() != 0 {
		t.Fatal("header names are not case insensitive", w.Code, registrations.Load(), count.Load())
	}
}

func TestRequestTrackingCollectorCoalesces(t *testing.T) {
	t.Parallel()
	now := time.Now()
	record, _ := stateful.NewTrackingRecord("a60f21da-604f-4fe3-a039-23382ec6b313", "/v1/responses", now, now.Add(time.Minute))
	session := stateful.NewTrackingSession(record, time.Now)
	ticks := make(chan time.Time)
	saved := make(chan stateful.TrackingRecord, 4)
	var count atomic.Int32
	api := newContractAPI(t, trackingProvider(t, &count))
	api.tracking = trackingStoreStub{update: func(_ context.Context, _ string, r stateful.TrackingRecord) error { saved <- r; return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	call := &trackedCall{api: api, scope: "synthetic-scope", session: session, progress: make(chan hardenllm.ProgressEvent, 64), stop: make(chan struct{}), done: make(chan struct{}), cancelFlush: cancel, ticks: ticks}
	go call.collect(ctx, now.Add(time.Minute))
	for i := 0; i < 100; i++ {
		session.Phase("provider_attempt")
		session.Attempt(i + 1)
		session.Phase("validation")
	}
	select {
	case <-saved:
		t.Fatal("phase callbacks performed SQL without a flush tick")
	default:
	}
	ticks <- time.Now()
	select {
	case r := <-saved:
		if r.AttemptsUsed != 100 || r.LastProgressAt == nil {
			t.Fatal("collector did not coalesce latest snapshot")
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot not flushed")
	}
	session.Finish("completed", "", true)
	call.close()
	select {
	case r := <-saved:
		if r.Status != "completed" {
			t.Fatal("collector did not finalize")
		}
	default:
		t.Fatal("missing final flush")
	}
	select {
	case <-call.done:
	default:
		t.Fatal("collector leaked")
	}
	if count.Load() != 0 {
		t.Fatal("collector dispatched inference")
	}
}

func TestRequestTrackingOfficialSDKJSONAndStreams(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	api := newContractAPI(t, trackingProvider(t, &count))
	var recordsMu sync.Mutex
	var records []stateful.TrackingRecord
	api.tracking = trackingStoreStub{reserve: func(context.Context, string, stateful.TrackingRecord) error { return nil }, update: func(_ context.Context, _ string, r stateful.TrackingRecord) error {
		recordsMu.Lock()
		records = append(records, r)
		recordsMu.Unlock()
		return nil
	}}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	client := openai.NewClient(option.WithAPIKey(proxyTestToken), option.WithBaseURL(server.URL+"/v1"), option.WithMaxRetries(0))
	header := func() option.RequestOption { return option.WithHeader(trackingHeader, uuid.NewString()) }
	chatInput := openai.ChatCompletionNewParams{Model: "fixture-model", Messages: []openai.ChatCompletionMessageParamUnion{{OfUser: &openai.ChatCompletionUserMessageParam{Content: openai.ChatCompletionUserMessageParamContentUnion{OfString: openai.String("hello")}}}}}
	chat, err := client.Chat.Completions.New(context.Background(), chatInput, header())
	if err != nil || len(chat.Choices) != 1 || chat.Choices[0].Message.Content != "fixture answer" {
		t.Fatal("tracked Chat JSON changed", err)
	}
	responseInput := responses.ResponseNewParams{Model: "fixture-model", Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("hello")}}
	response, err := client.Responses.New(context.Background(), responseInput, header())
	if err != nil || response.OutputText() != "fixture answer" {
		t.Fatal("tracked Responses JSON changed", err)
	}
	chatStream := client.Chat.Completions.NewStreaming(context.Background(), chatInput, header())
	var chatText strings.Builder
	for chatStream.Next() {
		event := chatStream.Current()
		for _, choice := range event.Choices {
			chatText.WriteString(choice.Delta.Content)
		}
	}
	if err := chatStream.Err(); err != nil || chatText.String() != "fixture answer" {
		t.Fatal("tracked Chat SSE changed", err, chatText.String())
	}
	chatStream.Close()
	responseStream := client.Responses.NewStreaming(context.Background(), responseInput, header())
	var responseText strings.Builder
	completed := false
	for responseStream.Next() {
		event := responseStream.Current()
		if event.Type == "response.output_text.delta" {
			responseText.WriteString(event.AsResponseOutputTextDelta().Delta)
		}
		if event.Type == "response.completed" {
			completed = true
		}
	}
	if err := responseStream.Err(); err != nil || responseText.String() != "fixture answer" || !completed {
		t.Fatal("tracked Responses SSE changed", err, responseText.String(), completed)
	}
	responseStream.Close()
	recordsMu.Lock()
	defer recordsMu.Unlock()
	if len(records) != 4 || count.Load() != 4 {
		t.Fatal("tracking introduced retries/extra writes", len(records), count.Load())
	}
	for _, record := range records {
		if record.Status != "completed" {
			t.Fatal("SDK completion not tracked")
		}
	}
}

func TestRequestTrackingActualRetryWait(t *testing.T) {
	t.Parallel()
	for _, failure := range []int{429, 503} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			var count atomic.Int32
			provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				if count.Add(1) == 1 {
					w.Header().Set("Retry-After", "1")
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(failure)
					io.WriteString(w, `{"error":{"message":"synthetic temporary failure","type":"server_error","code":"rate_limit_exceeded"}}`)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"fixture","status":"completed","output_text":"fixture answer","output":[{"type":"message","content":[{"type":"output_text","text":"fixture answer"}]}]}`)
			}))
			defer provider.Close()
			api := newContractAPI(t, provider)
			ticks := make(chan time.Time)
			api.trackingTicks = func() (<-chan time.Time, func()) { return ticks, func() {} }
			saved := make(chan stateful.TrackingRecord, 1)
			api.tracking = trackingStoreStub{reserve: func(context.Context, string, stateful.TrackingRecord) error { return nil }, update: func(ctx context.Context, _ string, r stateful.TrackingRecord) error {
				select {
				case saved <- r:
				case <-ctx.Done():
					return ctx.Err()
				}
				return nil
			}}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- trackedRequest(api, "a60f21da-604f-4fe3-a039-23382ec6b313") }()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			waiting := false
			for !waiting {
				select {
				case ticks <- time.Now():
				case <-ctx.Done():
					t.Fatal("core did not schedule an observable retry")
				case w := <-done:
					t.Fatal("core completed without expected wait", w.Code, w.Body.String())
				}
				select {
				case r := <-saved:
					if r.Status == "waiting" {
						want := "retry_backoff"
						if failure == 429 {
							want = "provider_throttled"
						}
						if r.Reason == nil || *r.Reason != want || r.NextAttemptAt == nil || r.AttemptsUsed != 1 {
							t.Fatal("retry classification/timing incorrect", r)
						}
						waiting = true
					}
				case <-ctx.Done():
					t.Fatal("no observation")
				}
			}
			// Drain periodic/final metadata independently; actual retry remains owned by
			// the unmodified runtime and its Retry-After wait.
			finished := false
			for !finished {
				select {
				case w := <-done:
					if w.Code != 200 {
						t.Fatal("retry policy changed", w.Code, w.Body.String())
					}
					finished = true
				case <-saved:
				case <-ctx.Done():
					t.Fatal("normal retry did not complete")
				}
			}
			if count.Load() != 2 {
				t.Fatal("tracking introduced provider dispatches", count.Load())
			}
		})
	}
}

func TestRequestTrackingTerminalRefusalIsNotWaiting(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"synthetic refusal","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
	}))
	defer provider.Close()
	api := newContractAPI(t, provider)
	var final stateful.TrackingRecord
	api.tracking = trackingStoreStub{reserve: func(context.Context, string, stateful.TrackingRecord) error { return nil }, update: func(_ context.Context, _ string, r stateful.TrackingRecord) error { final = r; return nil }}
	policy := hardenllm.DefaultRecoveryPolicy()
	policy.MaxAttempts = 1
	body, _ := json.Marshal(map[string]any{"model": "fixture-model", "input": "hello", "harden": map[string]any{"recovery": policy}})
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+proxyTestToken)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(trackingHeader, "a60f21da-604f-4fe3-a039-23382ec6b313")
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, r)
	if w.Code != 429 || count.Load() != 1 || final.Status != "failed" || final.NextAttemptAt != nil || final.Reason != nil {
		t.Fatal("terminal refusal invented a retry wait", w.Code, count.Load(), final.Status, w.Body.String())
	}
}

func TestRequestTrackingBlockedStorageDoesNotBlockCallbacks(t *testing.T) {
	t.Parallel()
	now := time.Now()
	record, _ := stateful.NewTrackingRecord("a60f21da-604f-4fe3-a039-23382ec6b313", "/v1/responses", now, now.Add(time.Minute))
	session := stateful.NewTrackingSession(record, time.Now)
	ticks := make(chan time.Time)
	started := make(chan struct{})
	var writes atomic.Int32
	var count atomic.Int32
	api := newContractAPI(t, trackingProvider(t, &count))
	api.tracking = trackingStoreStub{update: func(ctx context.Context, _ string, r stateful.TrackingRecord) error {
		if writes.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 600*time.Millisecond {
			t.Error("metadata finalization has no shared bounded budget")
		}
		if r.Status != "completed" {
			t.Error("storage failure changed the known outcome")
		}
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	call := &trackedCall{api: api, scope: "synthetic-scope", session: session, progress: make(chan hardenllm.ProgressEvent, 64), stop: make(chan struct{}), done: make(chan struct{}), cancelFlush: cancel, ticks: ticks}
	go call.collect(ctx, now.Add(time.Minute))
	ticks <- time.Now()
	<-started
	callbacksDone := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			session.Phase("provider_attempt")
			session.Attempt(i + 1)
		}
		session.Finish("completed", "", true)
		close(callbacksDone)
	}()
	select {
	case <-callbacksDone:
	case <-time.After(time.Second):
		t.Fatal("provider callbacks blocked on storage")
	}
	// Match the runtime's nonblocking progress contract with a deliberately full
	// channel while SQL is held. No callback or token waits for a collector.
	for i := 0; i < 1000; i++ {
		select {
		case call.progress <- hardenllm.ProgressEvent{Sequence: uint64(i + 1)}:
		default:
		}
	}
	call.close()
	if writes.Load() != 2 || count.Load() != 0 {
		t.Fatal("collector repeated SQL/inference", writes.Load(), count.Load())
	}
}

func TestRequestTrackingInvalidRequestsDoNotReserve(t *testing.T) {
	t.Parallel()
	var count, reservations atomic.Int32
	api := newContractAPI(t, trackingProvider(t, &count))
	api.tracking = trackingStoreStub{reserve: func(context.Context, string, stateful.TrackingRecord) error { reservations.Add(1); return nil }}
	for _, body := range []string{`{}`, `{"model":"fixture-model"}`, `{"model":"fixture-model","input":"hello","background":true}`, `{"model":"fixture-model","input":"hello","harden":{"timeout_ms":-1}}`, `{"model":"fixture-model","input":"hello","owner":"forged"}`} {
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+proxyTestToken)
		r.Header.Set(trackingHeader, "a60f21da-604f-4fe3-a039-23382ec6b313")
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("invalid tracked input did not reject before registration", w.Code, w.Body.String())
		}
	}
	if count.Load() != 0 || reservations.Load() != 0 {
		t.Fatal("invalid tracked input dispatched registration/inference")
	}
}

func TestRequestTrackingStructuredAndToolResponses(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if string(body["tools"]) != "" && string(body["tools"]) != "null" && string(body["tools"]) != "[]" {
			io.WriteString(w, `{"id":"fixture","status":"completed","output":[{"type":"function_call","id":"fc_fixture","call_id":"call_fixture","name":"lookup","arguments":"{\"query\":\"hello\"}"}]}`)
		} else {
			io.WriteString(w, `{"id":"fixture","status":"completed","output_text":"{\"ok\":true}","output":[{"type":"message","content":[{"type":"output_text","text":"{\"ok\":true}"}]}]}`)
		}
	}))
	defer provider.Close()
	api := newContractAPI(t, provider)
	var final stateful.TrackingRecord
	api.tracking = trackingStoreStub{reserve: func(context.Context, string, stateful.TrackingRecord) error { return nil }, update: func(_ context.Context, _ string, r stateful.TrackingRecord) error { final = r; return nil }}
	for _, item := range []struct{ path, body, contains string }{
		{"/v1/responses", `{"model":"fixture-model","input":"hello","text":{"format":{"type":"json_schema","name":"answer","schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}}}`, `\"ok\":true`},
		{"/v1/chat/completions", `{"model":"fixture-model","messages":[{"role":"user","content":"hello"}],"response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}}}`, `\"ok\":true`},
		{"/v1/responses", `{"model":"fixture-model","input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}]}`, `"call_id":"call_fixture"`},
		{"/v1/chat/completions", `{"model":"fixture-model","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}}]}`, `"id":"call_fixture"`},
	} {
		r := httptest.NewRequest("POST", item.path, strings.NewReader(item.body))
		r.Header.Set("Authorization", "Bearer "+proxyTestToken)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(trackingHeader, uuid.NewString())
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), item.contains) || final.Status != "completed" {
			t.Fatal("tracked structured/tool projection changed", item.path, w.Code, w.Body.String(), final.Status)
		}
	}
	if count.Load() != 4 {
		t.Fatal("tracking introduced repair/replay", count.Load())
	}
}

func TestRequestTrackingLogsExcludeCallerHeaders(t *testing.T) {
	t.Parallel()
	var count atomic.Int32
	api := newContractAPI(t, trackingProvider(t, &count))
	var logs bytes.Buffer
	api.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	api.tracking = trackingStoreStub{reserve: func(context.Context, string, stateful.TrackingRecord) error { return nil }, update: func(context.Context, string, stateful.TrackingRecord) error {
		return errors.New("private-storage-error-sentinel")
	}}
	id := "a60f21da-604f-4fe3-a039-23382ec6b313"
	if w := trackedRequest(api, id); w.Code != 200 {
		t.Fatal("tracking failed", w.Code)
	}
	for _, secret := range []string{id, proxyTestToken, "synthetic-upstream-secret", "private-storage-error-sentinel", "hello", "fixture answer"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("tracking logs retained caller/provider data")
		}
	}
	if !strings.Contains(logs.String(), `"call_id"`) || !strings.Contains(logs.String(), `"elapsed_ms"`) {
		t.Fatal("server correlation/timing diagnostics missing")
	}
}
