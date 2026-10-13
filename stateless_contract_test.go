package hardenllm_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-421

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

type contractExecutor struct {
	mu      sync.Mutex
	calls   int
	prepare func(hardenllm.ProviderConnection, hardenllm.ProviderCredential, hardenllm.ProviderCall)
	execute func(context.Context, hardenllm.PreparedOperation) (hardenllm.ProviderResult, error)
}

func (e *contractExecutor) Prepare(_ context.Context, connection hardenllm.ProviderConnection, credential hardenllm.ProviderCredential, call hardenllm.ProviderCall) (hardenllm.PreparedOperation, error) {
	if e.prepare != nil {
		e.prepare(connection, credential, call)
	}
	return hardenllm.PreparedOperation{Operation: hardenllm.CacheOperation{
		SchemaVersion: "utility-llm.operation.v1", Protocol: connection.APIInferenceType,
		Endpoint: hardenllm.CacheEndpoint{Identity: "https://provider.example:443", Method: "POST", Path: "/v1/responses"},
		Model:    call.ModelID, Payload: map[string]any{"messages": call.Messages, "options": call.ProviderOptions},
		ResponseProjection: hardenllm.ResponseProjection{Provider: "openai", Kind: "responses", Version: "v1"},
	}}, nil
}

func (e *contractExecutor) Execute(ctx context.Context, op hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	if e.execute != nil {
		return e.execute(ctx, op)
	}
	r := hardenllm.ProviderResult{Output: "ok", ProviderDispatched: true}
	r.Accounting.Usage.Status = "complete"
	r.Accounting.Usage.InputTokens = 2
	r.Accounting.Usage.OutputTokens = 1
	r.Accounting.Cost.Status = "unknown"
	r.Accounting.Cost.UnknownObservations = 1
	return r, nil
}

func coreContractOptions(executor hardenllm.Executor) hardenllm.Options {
	return hardenllm.Options{Connections: hardenllm.ConnectionCatalog{"primary": {ID: "primary", Provider: "openai", Protocol: "responses", BaseURL: "https://provider.example/v1", APIKey: "provider-secret-sentinel", Headers: map[string]string{"X-Fixture": "initial"}}}, DefaultConnection: "primary", Executor: executor}
}

func coreContractRequest() hardenllm.Request {
	return hardenllm.Request{ModelID: "fixture-model", CallType: hardenllm.CallTypeText,
		Messages:        []hardenllm.Message{{Role: "user", Content: json.RawMessage(`"input"`)}},
		CacheMode:       hardenllm.CacheModeOff,
		RecoveryPolicy:  hardenllm.RecoveryPolicy{MaxAttempts: 1, RetryOn: []hardenllm.RecoveryCategory{}, Backoff: hardenllm.RecoveryBackoff{}},
		ProviderOptions: map[string]any{"nested": map[string]any{"value": "initial"}},
	}
}

type contractObserver struct {
	mu               sync.Mutex
	starts, finishes int
	summary          hardenllm.CallSummary
}

func (o *contractObserver) StartCall(ctx context.Context, _ hardenllm.CallObservation) (context.Context, func(hardenllm.CallSummary, error)) {
	o.mu.Lock()
	o.starts++
	o.mu.Unlock()
	return ctx, func(s hardenllm.CallSummary, _ error) { o.mu.Lock(); defer o.mu.Unlock(); o.finishes++; o.summary = s }
}
func (*contractObserver) StartRuntime(ctx context.Context, _ hardenllm.CallObservation) (context.Context, func(error)) {
	return ctx, func(error) {}
}
func (*contractObserver) StartProvider(ctx context.Context, _ hardenllm.ObserverTarget, _ string) (context.Context, func(bool, error)) {
	return ctx, func(bool, error) {}
}
func (*contractObserver) StartAttempt(ctx context.Context, _ hardenllm.ObserverTarget, _ string, _ int) (context.Context, func(error)) {
	return ctx, func(error) {}
}
func (*contractObserver) StartRetry(ctx context.Context, _ hardenllm.ObserverTarget, _ string, _ hardenllm.RetryClassification, _ time.Duration) (context.Context, func(error)) {
	return ctx, func(error) {}
}
func (*contractObserver) StartValidation(ctx context.Context, _ hardenllm.ProviderConnection, _ bool) (context.Context, func(error)) {
	return ctx, func(error) {}
}
func (*contractObserver) StartCache(ctx context.Context, _ string) (context.Context, func(string, error)) {
	return ctx, func(string, error) {}
}

type contractCache struct {
	mu      sync.Mutex
	records map[string]hardenllm.CacheRecord
}

func (c *contractCache) Get(_ context.Context, key string) (hardenllm.CacheRecord, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.records[key]
	return r, ok, nil
}
func (c *contractCache) Set(_ context.Context, key string, r hardenllm.CacheRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records[key] = r
	return nil
}
func (c *contractCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.records, key)
	return nil
}

func TestStatelessContract(t *testing.T) {
	// This construction guard fails before any dispatch in the pre-injection
	// implementation; none of these tests can reach a public provider.
	if _, err := hardenllm.New(coreContractOptions(nil)); err == nil {
		t.Fatal("stateless core must reject a missing executor instead of constructing a concrete provider")
	}
	t.Run("core has only injected effects", func(t *testing.T) {
		want := map[string]bool{"Connections": true, "DefaultConnection": true, "Executor": true, "DiscoverModels": true, "Observer": true, "Cache": true}
		options := reflect.TypeFor[hardenllm.Options]()
		connectionField, ok := options.FieldByName("Connections")
		if !ok || connectionField.Type.Kind() != reflect.Map {
			t.Error("core connections must be an immutable indexed catalog")
		}
		if options.NumField() != len(want) {
			t.Error("core effect boundary fields are incomplete")
		}
		for i := range options.NumField() {
			if !want[options.Field(i).Name] {
				t.Errorf("core owns a concrete-effect setting: %s", options.Field(i).Name)
			}
		}
		for _, dir := range []string{".", "internal/runtime"} {
			err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					if path != dir {
						return filepath.SkipDir
					}
					return nil
				}
				if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
				if err != nil {
					return err
				}
				for _, imported := range file.Imports {
					value, _ := strconv.Unquote(imported.Path.Value)
					for _, forbidden := range []string{"/internal/providers", "/stateful", "pgx", "go.opentelemetry", "log/slog"} {
						if strings.Contains(value, forbidden) {
							t.Errorf("core imports concrete effect %s in %s", value, path)
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("external executor and model discovery", func(t *testing.T) {
		e := &contractExecutor{}
		options := coreContractOptions(e)
		discoveryCalls := 0
		options.DiscoverModels = func(_ context.Context, c hardenllm.Connection) ([]hardenllm.Model, error) {
			discoveryCalls++
			return []hardenllm.Model{{ID: "fixture-model", OwnedBy: c.Provider}}, nil
		}
		client, err := hardenllm.New(options)
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.Call(context.Background(), coreContractRequest())
		if err != nil || result.Output != "ok" || e.calls != 1 || discoveryCalls != 0 {
			t.Fatalf("injected call: output=%v err=%v calls=%d discovery=%d", result.Output, err, e.calls, discoveryCalls)
		}
		models, err := client.Models(context.Background(), "")
		if err != nil || len(models) != 1 || discoveryCalls != 1 {
			t.Fatalf("discovery: %v %v", models, err)
		}
	})
	t.Run("nil observer preserves output and accounting", func(t *testing.T) {
		plain, _ := hardenllm.New(coreContractOptions(&contractExecutor{}))
		observedOptions := coreContractOptions(&contractExecutor{})
		observer := &contractObserver{}
		observedOptions.Observer = observer
		observed, _ := hardenllm.New(observedOptions)
		a, errA := plain.Call(context.Background(), coreContractRequest())
		b, errB := observed.Call(context.Background(), coreContractRequest())
		if errA != nil || errB != nil || !reflect.DeepEqual(a.Output, b.Output) || !reflect.DeepEqual(a.Accounting, b.Accounting) || len(a.Attempts) != len(b.Attempts) || observer.starts != 1 || observer.finishes != 1 {
			t.Fatalf("observer changed execution: %v %v starts=%d finishes=%d", errA, errB, observer.starts, observer.finishes)
		}
		for _, forbidden := range []string{"Output", "Messages", "Credential", "PreparedOperation"} {
			if _, ok := reflect.TypeFor[hardenllm.CallSummary]().FieldByName(forbidden); ok {
				t.Errorf("observation exposes %s", forbidden)
			}
		}
	})
	t.Run("retry and cancellation remain core decisions", func(t *testing.T) {
		attempt := 0
		e := &contractExecutor{execute: func(_ context.Context, _ hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
			attempt++
			if attempt == 1 {
				return hardenllm.ProviderResult{ProviderDispatched: true}, &hardenllm.ProviderError{Status: 503}
			}
			return (&contractExecutor{}).Execute(context.Background(), hardenllm.PreparedOperation{})
		}}
		client, _ := hardenllm.New(coreContractOptions(e))
		request := coreContractRequest()
		request.RecoveryPolicy.MaxAttempts = 2
		request.RecoveryPolicy.RetryOn = []hardenllm.RecoveryCategory{"server_error"}
		r, err := client.Call(context.Background(), request)
		if err != nil || r.Output != "ok" || e.calls != 2 || len(r.Attempts) != 2 {
			t.Fatalf("retry invariant: %v attempts=%d calls=%d", err, len(r.Attempts), e.calls)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		before := e.calls
		_, err = client.Call(ctx, request)
		if !errors.Is(err, context.Canceled) || e.calls != before {
			t.Fatalf("cancellation dispatched provider: %v calls=%d", err, e.calls)
		}
	})
	t.Run("cache hit does not dispatch", func(t *testing.T) {
		e := &contractExecutor{}
		options := coreContractOptions(e)
		options.Cache = &contractCache{records: map[string]hardenllm.CacheRecord{}}
		client, _ := hardenllm.New(options)
		request := coreContractRequest()
		request.CacheMode = hardenllm.CacheModeCache
		first, err := client.Call(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		second, err := client.Call(context.Background(), request)
		if err != nil || e.calls != 1 || !second.Cache.Served || !reflect.DeepEqual(first.Output, second.Output) || second.Accounting.Provider.Usage.Status != "unavailable" {
			t.Fatalf("cache invariant: %v calls=%d served=%v", err, e.calls, second.Cache.Served)
		}
	})
	t.Run("input snapshots stay detached across concurrent calls", func(t *testing.T) {
		e := &contractExecutor{prepare: func(_ hardenllm.ProviderConnection, cred hardenllm.ProviderCredential, call hardenllm.ProviderCall) {
			if cred.Headers["X-Fixture"] != "initial" {
				t.Error("constructor retained caller headers")
			}
			call.ProviderOptions["nested"].(map[string]any)["value"] = "executor mutation"
			call.Messages[0].Content[1] = 'X'
		}}
		options := coreContractOptions(e)
		client, _ := hardenllm.New(options)
		options.Connections["primary"].Headers["X-Fixture"] = "caller mutation"
		var wg sync.WaitGroup
		for range 12 {
			wg.Go(func() {
				r := coreContractRequest()
				if _, err := client.Call(context.Background(), r); err != nil {
					t.Error(err)
				}
				if r.ProviderOptions["nested"].(map[string]any)["value"] != "initial" || string(r.Messages[0].Content) != `"input"` {
					t.Error("executor mutated caller input")
				}
			})
		}
		wg.Wait()
		if e.calls != 12 {
			t.Fatalf("concurrent calls=%d", e.calls)
		}
	})
	t.Run("terminal capture is detached bounded and redacted", func(t *testing.T) {
		e := &contractExecutor{execute: func(context.Context, hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
			return hardenllm.ProviderResult{ProviderDispatched: true}, &hardenllm.ProviderError{Category: "parse_error", RawResponse: `{"broken":"provider-secret-sentinel"`}
		}}
		client, _ := hardenllm.New(coreContractOptions(e))
		request := coreContractRequest()
		captures := 0
		request.CaptureDiagnostics = func(capture hardenllm.DiagnosticCapture) {
			captures++
			encoded, err := json.Marshal(capture)
			if err != nil {
				t.Error(err)
			}
			if strings.Contains(string(encoded), "provider-secret-sentinel") {
				t.Error("capture leaked resolved credential")
			}
			if len(capture.Artifacts) < 2 {
				t.Error("parse-failure evidence missing")
			}
		}
		_, err := client.Call(context.Background(), request)
		if err == nil || captures != 1 || e.calls != 1 {
			t.Fatalf("terminal capture: err=%v captures=%d calls=%d", err, captures, e.calls)
		}
	})
	t.Run("projection failure reports once and preserves inference", func(t *testing.T) {
		e := &contractExecutor{execute: func(context.Context, hardenllm.PreparedOperation) (hardenllm.ProviderResult, error) {
			r := hardenllm.ProviderResult{Output: "ok", ProviderDispatched: true}
			invalidTime := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
			r.Stream.LastEventAt = &invalidTime
			r.Stream.EventCount = 1
			return r, nil
		}}
		client, err := hardenllm.New(coreContractOptions(e))
		if err != nil {
			t.Fatal(err)
		}
		request := coreContractRequest()
		captures := 0
		request.CaptureDiagnostics = func(capture hardenllm.DiagnosticCapture) {
			captures++
			if capture.ErrorCode != "diagnostic_projection_failed" || len(capture.Artifacts) != 0 {
				t.Errorf("projection failure: errorCode=%q artifacts=%d", capture.ErrorCode, len(capture.Artifacts))
			}
		}
		result, err := client.Call(context.Background(), request)
		if err != nil || result.Output != "ok" || captures != 1 || e.calls != 1 {
			t.Fatalf("projection altered inference: err=%v captures=%d calls=%d", err, captures, e.calls)
		}
	})
	t.Run("progress and captured timestamps are detached from the final result", func(t *testing.T) {
		executor := &contractExecutor{}
		client, err := hardenllm.New(coreContractOptions(executor))
		if err != nil {
			t.Fatal(err)
		}
		request := coreContractRequest()
		progress := make(chan hardenllm.ProgressEvent, 64)
		request.Progress = progress
		var capture hardenllm.DiagnosticCapture
		request.CaptureDiagnostics = func(value hardenllm.DiagnosticCapture) { capture = value }
		result, err := client.Call(context.Background(), request)
		if err != nil || len(result.Attempts) != 1 || result.Attempts[0].StartedAt == nil || len(capture.Trace.Attempts) != 1 {
			t.Fatal("missing execution timestamps", err)
		}
		original := *result.Attempts[0].StartedAt
		changed := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		*capture.Trace.Attempts[0].StartedAt = changed
		close(progress)
		observed := false
		for event := range progress {
			for _, attempt := range event.Attempts {
				if attempt.StartedAt != nil {
					observed = true
					*attempt.StartedAt = changed
				}
			}
		}
		if !observed || !result.Attempts[0].StartedAt.Equal(original) {
			t.Fatal("public progress or capture mutated the final result")
		}
	})
}
