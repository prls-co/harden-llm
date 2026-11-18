package providers

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-542 TEST-549.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/retry"
	"github.com/prls-co/harden-llm/internal/runtime"
)

func TestTransportFailureLabelsDoNotRetainRawErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err   error
		class string
	}{
		{io.ErrUnexpectedEOF, "unexpected_eof"},
		{io.EOF, "eof"},
		{syscall.ECONNRESET, "connection_reset"},
		{syscall.ECONNREFUSED, "connection_refused"},
		{syscall.EPIPE, "broken_pipe"},
		{net.ErrClosed, "connection_closed"},
		{&net.DNSError{Name: "private-host", Err: "private driver text"}, "dns"},
		{context.DeadlineExceeded, "timeout"},
		{context.Canceled, "canceled"},
		{&endpointPolicyError{err: errors.New("private endpoint")}, "endpoint_policy"},
		{errors.New("private unknown error"), "network_error"},
	} {
		t.Run(tc.class, func(t *testing.T) {
			raw := &url.Error{Op: "Post", URL: "https://private-key@private-host", Err: fmt.Errorf("private SQL or provider output: %w", tc.err)}
			var d runtime.StreamDiagnostics
			captureTransportFailure(&d, t.Context(), raw, "response_headers")
			if d.TransportErrorClass != tc.class || d.TransportErrorPhase != "response_headers" {
				t.Fatalf("safe error classification lost: %+v", d)
			}
			encoded, err := json.Marshal(d)
			if err != nil || string(encoded) != "{}" {
				t.Fatal("internal labels changed serialized diagnostics", string(encoded), err)
			}
		})
	}
	parent, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
	defer cancel()
	var d runtime.StreamDiagnostics
	captureTransportFailure(&d, parent, context.DeadlineExceeded, "response_stream")
	if d.TransportErrorClass != "deadline" {
		t.Fatal("confirmed logical deadline mislabeled", d)
	}
	captureTransportFailure(&d, parent, syscall.ECONNRESET, "response_stream")
	if d.TransportErrorClass != "connection_reset" {
		t.Fatal("logical stop erased the original socket failure", d)
	}
	local, stop := context.WithDeadline(t.Context(), time.Unix(0, 0))
	defer stop()
	captureTransportFailure(&d, t.Context(), local.Err(), "response_stream")
	if d.TransportErrorClass != "timeout" {
		t.Fatal("narrow transport deadline mislabeled as logical deadline", d)
	}
}

func TestOperationDiagnosticsTimeoutAndPhaseOwnership(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, class, phase string
		dispatched         bool
		headersReceived    bool
		parentExpired      bool
	}{
		{"header_timeout", "timeout", "response_headers", true, false, false},
		{"parent_deadline", "deadline", "response_headers", true, false, true},
		{"canceled", "canceled", "response_stream", true, true, true},
		{"partial_disconnect", "unexpected_eof", "response_stream", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			release := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				switch tc.name {
				case "header_timeout", "parent_deadline":
					<-release
				case "canceled":
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"private partial\"}\n\n")
					w.(http.Flusher).Flush()
					<-release
				case "partial_disconnect":
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"private partial\"}\n\n")
					w.(http.Flusher).Flush()
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						conn.Close()
					}
				}
			}))
			t.Cleanup(func() { close(release); server.Close() })
			router := newTLSTestRouter(t, server, nil)
			if tc.name == "header_timeout" {
				router.client.Transport.(*safeRoundTripper).headerTimeout = 60 * time.Millisecond
			}
			var logBuf bytes.Buffer
			router.logger = slog.New(slog.NewJSONHandler(&logBuf, nil))
			conn := runtime.Connection{ID: "gemini", Provider: "google", APIInferenceType: "gemini-generate-content", BaseURL: server.URL}
			if tc.name == "canceled" || tc.name == "partial_disconnect" {
				conn = runtime.Connection{ID: "cpa", Provider: "cpa", APIInferenceType: "responses", BaseURL: server.URL + "/v1"}
			}
			call := runtime.Call{ModelID: "gemini-3.7-flash", CallType: "structured", Schema: json.RawMessage(`{"type":"object","properties":{"joke":{"type":"string"}},"required":["joke"],"additionalProperties":false}`), Messages: providerMessages("", "private prompt")}
			if conn.APIInferenceType == "responses" {
				call.ProviderOptions = map[string]any{"stream": true}
			}
			prepared, err := router.Prepare(t.Context(), conn, runtime.Credential{APIKey: "synthetic-secret"}, call)
			if err != nil {
				t.Fatal(err)
			}
			ctx, collector := WithOperationDiagnostics(t.Context())
			cancel := func() {}
			if tc.name == "parent_deadline" {
				// The observation starts on an established TLS connection. A cold
				// handshake can itself outlast a short deadline under the race detector.
				req, err := http.NewRequestWithContext(t.Context(), http.MethodHead, server.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := router.client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				ctx, cancel = context.WithTimeout(ctx, time.Second)
			} else if tc.name == "canceled" {
				ctx, cancel = context.WithCancel(ctx)
				prepared.StreamProgress = func(snapshot runtime.StreamDiagnostics) {
					if snapshot.OutputBytes > 0 {
						cancel()
					}
				}
			} else {
				ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
			}
			defer cancel()
			result, execErr := router.Execute(ctx, prepared)
			if execErr == nil || result.ProviderDispatched != tc.dispatched {
				t.Fatalf("expected failure with dispatched=%v, got result=%+v err=%v", tc.dispatched, result, execErr)
			}
			records, clipped := collector.Records()
			if clipped || len(records) != 1 {
				t.Fatalf("expected 1 operation record, got clipped=%v records=%v", clipped, records)
			}
			rec := records[0]
			if rec["msg"] != "provider operation observed" || rec["stage"] != "provider_model" || rec["dispatched"] != tc.dispatched || rec["headers_received"] != tc.headersReceived || rec["error_class"] != tc.class || rec["error_phase"] != tc.phase || rec["parent_context_expired"] != tc.parentExpired {
				t.Fatalf("unexpected operation diagnostic record: %#v", rec)
			}
			for _, secret := range []string{"private prompt", "private partial", "synthetic-secret"} {
				if bytes.Contains(logBuf.Bytes(), []byte(secret)) {
					t.Fatalf("logger leaked %s", secret)
				}
			}
		})
	}
}

func TestJinaSearchOperationDiagnosticsAndSnippetContract(t *testing.T) {
	t.Parallel()
	t.Run("oversize HTTP 200 retains status and byte count without invoking model", func(t *testing.T) {
		t.Parallel()
		var jinaHeaders http.Header
		jinaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jinaHeaders = r.Header.Clone()
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bytes.Repeat([]byte("x"), 1025))
		}))
		defer jinaServer.Close()
		modelCalled := atomic.Bool{}
		modelServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			modelCalled.Store(true)
			w.WriteHeader(http.StatusOK)
		}))
		defer modelServer.Close()
		router := newTLSTestRouter(t, modelServer, nil)
		jinaURL, _ := url.Parse(jinaServer.URL + "/")
		router.webSearcher = &jinaSearcher{
			client: jinaServer.Client(), logger: router.logger, apiKey: "synthetic-jina-key",
			baseURL: jinaURL, timeout: time.Second, maxResponseBytes: 1024, now: time.Now,
		}
		conn := runtime.Connection{ID: "openrouter", Provider: "openrouter", APIInferenceType: "chat-completions", BaseURL: modelServer.URL + "/v1", SupportsWebSearch: false}
		call := runtime.Call{ModelID: "deepseek/deepseek-v4", CallType: "text", Messages: providerMessages("", "Search the web for Go docs"), WebSearch: true}
		prepared, err := router.Prepare(t.Context(), conn, runtime.Credential{APIKey: "synthetic-model-key"}, call)
		if err != nil {
			t.Fatal(err)
		}
		ctx, collector := WithOperationDiagnostics(t.Context())
		result, execErr := router.Execute(ctx, prepared)
		if execErr == nil || result.ProviderDispatched || modelCalled.Load() {
			t.Fatalf("oversize Jina response must fail before model: dispatched=%v modelCalled=%v err=%v", result.ProviderDispatched, modelCalled.Load(), execErr)
		}
		if jinaHeaders.Get("X-Respond-With") != "no-content" || jinaHeaders.Get("Accept") != "text/plain" {
			t.Fatalf("Jina request missing snippet header: %#v", jinaHeaders)
		}
		records, clipped := collector.Records()
		if clipped || len(records) != 1 {
			t.Fatalf("expected 1 search diagnostic record, got %v", records)
		}
		rec := records[0]
		if rec["msg"] != "web search operation observed" || rec["stage"] != "web_search" || rec["provider"] != "jina" || rec["http_status"] != 200 || rec["received_bytes"] != int64(1025) || rec["max_response_bytes"] != int64(1024) || rec["error_code"] != "RESPONSE_TOO_LARGE" || rec["dispatched"] != true {
			t.Fatalf("Jina oversize facts lost: %#v", rec)
		}
	})

	t.Run("completed retrieval prefix and memoized retry emit one search record and separate model headersMs", func(t *testing.T) {
		t.Parallel()
		jinaCalls := atomic.Int32{}
		jinaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jinaCalls.Add(1)
			time.Sleep(120 * time.Millisecond)
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("Title: Go Docs\nURL Source: https://go.dev/doc/\n"))
		}))
		defer jinaServer.Close()
		var modelBodies []string
		modelServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, _ := io.ReadAll(r.Body)
			modelBodies = append(modelBodies, string(data))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","provider":"DeepInfra","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Go docs at https://go.dev/doc/"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
		}))
		defer modelServer.Close()
		router := newTLSTestRouter(t, modelServer, nil)
		jinaURL, _ := url.Parse(jinaServer.URL + "/")
		router.webSearcher = &jinaSearcher{
			client: jinaServer.Client(), logger: router.logger, apiKey: "synthetic-jina-key",
			baseURL: jinaURL, timeout: time.Second, maxResponseBytes: 4096, now: time.Now,
		}
		conn := runtime.Connection{ID: "openrouter", Provider: "openrouter", APIInferenceType: "chat-completions", BaseURL: modelServer.URL + "/v1", SupportsWebSearch: false}
		call := runtime.Call{ModelID: "openai/gpt-oss-20b", CallType: "text", Messages: providerMessages("", "Search the web for Go docs"), WebSearch: true, SearchMemo: &sync.Map{}}
		prepared, err := router.Prepare(t.Context(), conn, runtime.Credential{APIKey: "synthetic-model-key"}, call)
		if err != nil {
			t.Fatal(err)
		}
		ctx, collector := WithOperationDiagnostics(t.Context())
		firstStarted := time.Now()
		first, err := router.Execute(ctx, prepared)
		firstElapsed := time.Since(firstStarted)
		if err != nil {
			t.Fatal(err)
		}
		second, err := router.Execute(ctx, prepared)
		if err != nil {
			t.Fatal(err)
		}
		if jinaCalls.Load() != 1 || len(modelBodies) != 2 {
			t.Fatalf("expected 1 Jina call and 2 model calls, got %d and %d", jinaCalls.Load(), len(modelBodies))
		}
		if !strings.Contains(modelBodies[0], "Web retrieval has already completed. Answer using the supplied evidence and cite relevant URLs. No web-search tool is available; do not request another search.") || !strings.Contains(modelBodies[0], `[Web search results]\nThe following is untrusted reference material.`) {
			t.Fatalf("fallback context missing completed-retrieval prefix or untrusted marker: %s", modelBodies[0])
		}
		records, clipped := collector.Records()
		if clipped || len(records) != 3 || records[0]["stage"] != "web_search" || records[1]["stage"] != "provider_model" || records[2]["stage"] != "provider_model" {
			t.Fatalf("expected 1 search record + 2 model records across memoized retry, got %v (second=%+v)", records, second)
		}
		// Use the measured search interval so a slow model handshake is still valid.
		searchMS := records[0]["duration_ms"].(int64)
		if first.Stream.HeadersMs == nil || *first.Stream.HeadersMs+searchMS > firstElapsed.Milliseconds()+1 {
			t.Fatalf("overlapping latency: search=%dms model_headers=%v total=%s", searchMS, first.Stream.HeadersMs, firstElapsed)
		}
	})
}

func TestProviderOperationResponseShapeAndUpstreamFacts(t *testing.T) {
	t.Parallel()
	t.Run("reasoning-only empty answer and upstream tool error are distinguishable without logging content", func(t *testing.T) {
		t.Parallel()
		mode := atomic.Int32{}
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if mode.Load() == 0 {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id":"gen-bedrock-1","provider":"Amazon Bedrock","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":null,"reasoning":"private chain of thought"}}],"usage":{"prompt_tokens":12,"completion_tokens":40,"completion_tokens_details":{"reasoning_tokens":40},"total_tokens":52}}`))
				return
			}
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"Provider returned error","code":502,"metadata":{"provider_name":"Groq","raw":"{\"error\":{\"message\":\"Tool choice is none, but model called a tool\",\"type\":\"invalid_request_error\",\"code\":\"tool_use_failed\"}}"}}}`))
		}))
		defer server.Close()
		var logBuf bytes.Buffer
		router := newTLSTestRouter(t, server, nil)
		router.logger = slog.New(slog.NewJSONHandler(&logBuf, nil))
		conn := runtime.Connection{ID: "openrouter", Provider: "openrouter", APIInferenceType: "chat-completions", BaseURL: server.URL + "/v1"}
		call := runtime.Call{ModelID: "openai/gpt-oss-20b", CallType: "text", Messages: providerMessages("", "private prompt")}
		prepared, err := router.Prepare(t.Context(), conn, runtime.Credential{APIKey: "synthetic-key"}, call)
		if err != nil {
			t.Fatal(err)
		}
		ctx, collector := WithOperationDiagnostics(t.Context())
		_, err = router.Execute(ctx, prepared)
		if err == nil {
			t.Fatal("expected empty_response error on reasoning-only reply")
		}
		mode.Store(1)
		_, err = router.Execute(ctx, prepared)
		if err == nil {
			t.Fatal("expected HTTP 502 error on Groq tool failure")
		}
		records, _ := collector.Records()
		if len(records) != 2 {
			t.Fatalf("expected 2 model records, got %v", records)
		}
		bedrock := records[0]
		if bedrock["http_status"] != 200 || bedrock["error_code"] != "empty_response" || bedrock["upstream_provider"] != "Amazon Bedrock" || bedrock["provider_request_id"] != "gen-bedrock-1" || bedrock["finish_reason"] != "stop" || bedrock["reasoning_present"] != true || bedrock["final_text_present"] != false || bedrock["final_text_bytes"] != int64(0) || bedrock["tool_call_count"] != 0 {
			t.Fatalf("Bedrock reasoning-only facts missing: %#v", bedrock)
		}
		groq := records[1]
		if groq["http_status"] != 502 || groq["upstream_provider"] != "Groq" || groq["upstream_error_code"] != "model_called_tool_when_none_enabled" {
			t.Fatalf("Groq upstream tool-call facts missing: %#v", groq)
		}
		for _, forbidden := range []string{"private chain of thought", "private prompt", "Tool choice is none"} {
			if bytes.Contains(logBuf.Bytes(), []byte(forbidden)) {
				t.Fatalf("diagnostic log leaked %q", forbidden)
			}
		}
	})
}

// TEST-549. The same completed envelope selects one answer for output, citations and diagnostics.
func TestOperationDiagnosticsClipping(t *testing.T) {
	t.Parallel()
	ctx, capture := WithOperationDiagnostics(t.Context())
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	emitOperationDiagnostic(ctx, logger, slog.LevelInfo, "oversized", "model", strings.Repeat("x", maxOperationDiagnosticLineLen))
	for i := 0; i <= maxOperationDiagnosticRecords; i++ {
		emitOperationDiagnostic(ctx, logger, slog.LevelInfo, "bounded", "record", i)
	}
	records, lines, clipped := capture.Snapshot()
	if !clipped || len(records) != maxOperationDiagnosticRecords || len(lines) != len(records)+1 ||
		!strings.Contains(lines[len(records)], `"clipped":true`) || strings.Contains(buf.String(), "oversized") {
		t.Fatal("oversized or excess diagnostic records escaped capture bounds")
	}
	for i, rec := range records {
		if rec["record"] != i || !json.Valid([]byte(lines[i])) || len(lines[i]) > maxOperationDiagnosticLineLen {
			t.Fatalf("incomplete or out-of-order record %d", i)
		}
	}
}

func TestNativeResponsesFinalAnswer(t *testing.T) {
	t.Parallel()
	message := func(phase, text, url string) map[string]any {
		part := map[string]any{"type": "output_text", "text": text}
		if url != "" {
			part["annotations"] = []any{map[string]any{"type": "url_citation", "url": url, "start_index": 0, "end_index": 2}}
		}
		m := map[string]any{"type": "message", "role": "assistant", "content": []any{part}}
		if phase != "" {
			m["phase"] = phase
		}
		return m
	}
	search := map[string]any{"type": "web_search_call", "status": "completed"}
	const source = "https://example.test/final"
	ok := message("", "ok", source)
	pre := message("", "Searching...", "https://example.test/preamble")
	comment := message("commentary", "Searching...", "https://example.test/comment")
	final := message("final_answer", "ok", source)
	empty := map[string]any{"type": "message", "phase": "final_answer", "content": []any{}}
	for _, tc := range []struct {
		name     string
		items    []any
		answer   string
		searched bool
	}{
		{"unlabeled_before_search", []any{pre, search}, "", true},
		{"two_searches", []any{search, ok, search}, "", true},
		{"legacy_answer", []any{pre, search, ok}, "ok", true},
		{"explicit_empty", []any{pre, search, empty, comment}, "", true},
		{"commentary_only", []any{comment, search}, "", true},
		{"late_commentary", []any{search, final, comment}, "ok", true},
		{"optional_without_search", []any{ok}, "ok", false},
	} {
		for _, wire := range []string{"json", "sse"} {
			t.Run(tc.name+"/"+wire, func(t *testing.T) {
				response := map[string]any{"status": "completed", "output": tc.items}
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if wire == "sse" {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{"type": "response.completed", "response": response}))
						return
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(response)
				}))
				defer server.Close()
				router := newTLSTestRouter(t, server, nil)
				p, err := router.Prepare(t.Context(), runtime.Connection{ID: "fixture", Provider: "openai", APIInferenceType: "responses", BaseURL: server.URL, SupportsWebSearch: true}, runtime.Credential{APIKey: "fixture"}, runtime.Call{ModelID: "fixture", CallType: "text", WebSearch: true, Messages: providerMessages("", "go docs")})
				if err != nil {
					t.Fatal(err)
				}
				ctx, capture := WithOperationDiagnostics(t.Context())
				result, err := router.Execute(ctx, p)
				if tc.answer == "" {
					var failure *retry.ProviderError
					if !errors.As(err, &failure) || failure.Code != "empty_response" || result.Output != nil {
						t.Fatalf("expected empty_response, got %v, %v", result.Output, err)
					}
				} else if err != nil || result.Output != tc.answer {
					t.Fatalf("answer=%v, err=%v", result.Output, err)
				}
				if result.Search == nil || result.Search.Executed != tc.searched {
					t.Fatalf("search observation: %+v", result.Search)
				}
				if tc.answer == "" {
					if len(result.Search.Citations) != 0 {
						t.Fatal("attached citations to missing answer")
					}
				} else if len(result.Search.Citations) != 1 || result.Search.Citations[0].URL != source || result.Search.CitationTextSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(tc.answer))) {
					t.Fatalf("wrong final citations: %+v", result.Search)
				}
				records, clipped := capture.Records()
				if clipped || len(records) != 1 || records[0]["final_text_present"] != (tc.answer != "") || records[0]["final_text_bytes"] != int64(len(tc.answer)) {
					t.Fatalf("wrong diagnostic shape: %+v", records)
				}
			})
		}
	}
}
