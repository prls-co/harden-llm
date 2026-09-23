package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/cachekey"
	"github.com/prls-co/harden-llm/internal/retry"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-284
func TestProgressSnapshotContracts(t *testing.T) {
	t.Run("simple execution snapshots stream and completed attempt", func(t *testing.T) {
		profile := progressProfile("primary", "primary-model")
		base := time.Now().Truncate(time.Millisecond)
		deadline := base.Add(time.Hour)
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		stream := StreamDiagnostics{ReceivedBytes: 5, EventCount: 2, OutputBytes: 3, OutputCodePoints: 2}
		executor := &progressContractExecutor{outcomes: []progressContractOutcome{
			{result: progressResult("done", stream, 7)},
		}}
		var snapshots []ProgressSnapshot
		call := Call{
			CallType: "text", ReasoningEffort: "middle",
			Context:  ObservabilityContext{RunID: "simple-run"},
			Origin:   Origin{Client: "workspace", OperationID: "simple-operation"},
			Progress: func(snapshot ProgressSnapshot) { snapshots = append(snapshots, snapshot) },
		}
		config := retry.Config{
			Policy: retry.Policy{MaxAttempts: 3, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}},
			Now:    progressClock(base),
		}

		record, err := Execute(ctx, executor, progressCredentials, profile.ID,
			map[string]Profile{profile.ID: profile}, call, config, nil,
			cachekey.ModeOff, "v1", "simple-call", "simple-trace")
		if err != nil || len(record.Attempts) != 1 || executor.executes != 1 {
			t.Fatalf("record=%#v executes=%d error=%v", record, executor.executes, err)
		}

		assertProgressEvents(t, snapshots, "simple-run", "simple-call", "simple-trace", []progressEventExpectation{
			{eventType: "run.started", stage: stageOriginalGenerate, profileID: profile.ID, reasoning: "middle", remaining: 3},
			{eventType: "run.progress", stage: stageOriginalGenerate, profileID: profile.ID, reasoning: "middle", attempt: 1, remaining: 3},
			{eventType: "run.progress", stage: stageOriginalGenerate, profileID: profile.ID, reasoning: "middle", attempt: 1, used: 1, remaining: 2},
			{eventType: "run.terminal", stage: stageOriginalGenerate, profileID: profile.ID, reasoning: "middle", attempt: 1, used: 1, remaining: 2, terminal: true},
		})
		assertProgressCounters(t, snapshots, []progressCounters{
			{},
			{received: 5, events: 2, output: 3, codePoints: 2},
			{received: 5, events: 2, output: 3, codePoints: 2},
			{received: 5, events: 2, output: 3, codePoints: 2},
		})
		assertProgressClockSampling(t, snapshots, deadline)
		if snapshots[0].Attempts != nil || snapshots[0].Accounting != nil || snapshots[1].Accounting != nil {
			t.Fatalf("empty-attempt snapshots must omit accounting: %#v", snapshots[:2])
		}
		if snapshots[2].Accounting == nil || snapshots[2].Accounting.Provider.Usage.InputTokens != 7 ||
			!reflect.DeepEqual(snapshots[2].Origin, call.Origin) || snapshots[2].EffectiveTimeout == nil {
			t.Fatalf("completed snapshot omitted copied accounting or call facts: %#v", snapshots[2])
		}
		if snapshots[2].DeadlineRemainingMs == nil || *snapshots[2].EffectiveTimeout != *record.Diagnostics.EffectiveTimeout {
			t.Fatalf("deadline fields = %#v", snapshots[2])
		}

		recordProfile := record.Attempts[0].ProfileID
		recordInput := record.Accounting.Provider.Usage.InputTokens
		recordTimeout := *record.Diagnostics.EffectiveTimeout
		snapshots[2].Attempts[0].ProfileID = "mutated-snapshot"
		snapshots[2].Accounting.Provider.Usage.InputTokens = 999
		*snapshots[2].EffectiveTimeout = -1
		if record.Attempts[0].ProfileID != recordProfile ||
			record.Accounting.Provider.Usage.InputTokens != recordInput ||
			*record.Diagnostics.EffectiveTimeout != recordTimeout {
			t.Fatal("progress snapshot mutation changed the execution record")
		}
	})

	t.Run("explicit recovery snapshots follow active work identity", func(t *testing.T) {
		profiles := progressProfiles()
		base := time.Now().Truncate(time.Millisecond)
		deadline := base.Add(time.Hour)
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		firstStream := StreamDiagnostics{ReceivedBytes: 5, EventCount: 1, OutputBytes: 3, OutputCodePoints: 2}
		secondStream := StreamDiagnostics{ReceivedBytes: 7, EventCount: 2, OutputBytes: 4, OutputCodePoints: 3}
		executor := &progressContractExecutor{outcomes: []progressContractOutcome{
			{result: progressResult(map[string]any{"answer": false}, firstStream, 2)},
			{result: progressResult(map[string]any{"answer": true}, secondStream, 3)},
		}}
		var snapshots []ProgressSnapshot
		call := progressStructuredCall("explicit-run")
		call.Progress = func(snapshot ProgressSnapshot) { snapshots = append(snapshots, snapshot) }
		policy := progressExplicitPolicy()
		config := retry.Config{Policy: policy, Now: progressClock(base)}

		record, err := Execute(ctx, executor, progressCredentials, "original", profiles, call, config,
			nil, cachekey.ModeOff, "v1", "explicit-call", "explicit-trace")
		if err != nil || len(record.Attempts) != 2 || executor.executes != 2 {
			t.Fatalf("record=%#v executes=%d error=%v", record, executor.executes, err)
		}

		assertProgressEvents(t, snapshots, "explicit-run", "explicit-call", "explicit-trace", []progressEventExpectation{
			{eventType: "run.started", stage: stageOriginalGenerate, profileID: "original", reasoning: "highest", remaining: 2},
			{eventType: "run.progress", stage: stageOriginalGenerate, profileID: "original", reasoning: "highest", remaining: 2},
			{eventType: "run.progress", stage: stageOriginalGenerate, profileID: "original", reasoning: "highest", attempt: 1, remaining: 2},
			{eventType: "run.progress", stage: stageOriginalGenerate, profileID: "original", reasoning: "highest", attempt: 1, used: 1, remaining: 1},
			{eventType: "run.progress", stage: stageOriginalRepairInitial, profileID: "repair-l", reasoning: "lowest", attempt: 1, used: 1, remaining: 1},
			{eventType: "run.progress", stage: stageOriginalRepairInitial, profileID: "repair-l", reasoning: "lowest", attempt: 2, used: 1, remaining: 1},
			{eventType: "run.progress", stage: stageOriginalRepairInitial, profileID: "repair-l", reasoning: "lowest", attempt: 2, used: 2},
			{eventType: "run.terminal", stage: stageOriginalRepairInitial, profileID: "repair-l", reasoning: "lowest", attempt: 2, used: 2, terminal: true},
		})
		assertProgressCounters(t, snapshots, []progressCounters{
			{}, {},
			{received: 5, events: 1, output: 3, codePoints: 2},
			{received: 5, events: 1, output: 3, codePoints: 2},
			{received: 5, events: 1, output: 3, codePoints: 2},
			{received: 12, events: 3, output: 7, codePoints: 5},
			{received: 12, events: 3, output: 7, codePoints: 5},
			{received: 12, events: 3, output: 7, codePoints: 5},
		})
		assertProgressClockSampling(t, snapshots, deadline)
		wantAttemptCounts := []int{0, 0, 0, 1, 1, 1, 2, 2}
		for index, snapshot := range snapshots {
			if len(snapshot.Attempts) != wantAttemptCounts[index] {
				t.Errorf("snapshot %d attempts=%d, want %d", index, len(snapshot.Attempts), wantAttemptCounts[index])
			}
		}
		if snapshots[3].Accounting == nil || snapshots[3].Accounting.Provider.Usage.InputTokens != 2 ||
			snapshots[6].Accounting == nil || snapshots[6].Accounting.Provider.Usage.InputTokens != 5 ||
			snapshots[7].StopReason != "succeeded" {
			t.Fatalf("attempt or terminal accounting snapshots = %#v", snapshots[3:])
		}
		originalProfile := record.Attempts[0].ProfileID
		snapshots[3].Attempts[0].ProfileID = "mutated-snapshot"
		if record.Attempts[0].ProfileID != originalProfile {
			t.Fatal("snapshot attempt slice aliases the execution record")
		}
	})

	t.Run("preflight failures keep path-specific event boundaries", func(t *testing.T) {
		profile := progressProfile("primary", "primary-model")
		simpleCtx, cancelSimple := context.WithCancel(context.Background())
		cancelSimple()
		var simpleSnapshots []ProgressSnapshot
		simpleCall := Call{CallType: "text", Progress: func(snapshot ProgressSnapshot) {
			simpleSnapshots = append(simpleSnapshots, snapshot)
		}}
		simpleExecutor := &progressContractExecutor{}
		simpleRecord, simpleErr := Execute(simpleCtx, simpleExecutor, progressCredentials, profile.ID,
			map[string]Profile{profile.ID: profile}, simpleCall,
			retry.Config{Policy: retry.Policy{MaxAttempts: 1, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}}}, nil,
			cachekey.ModeOff, "v1", "simple-expired", "trace")
		if !errors.Is(simpleErr, context.Canceled) || simpleExecutor.prepares != 0 {
			t.Fatalf("simple expired call record=%#v prepares=%d error=%v", simpleRecord, simpleExecutor.prepares, simpleErr)
		}
		if got := progressEventTypes(simpleSnapshots); !reflect.DeepEqual(got, []string{"run.started", "run.terminal"}) {
			t.Fatalf("simple expired events=%v", got)
		}

		explicitCtx, cancelExplicit := context.WithCancel(context.Background())
		cancelExplicit()
		var explicitSnapshots []ProgressSnapshot
		explicitCall := progressStructuredCall("explicit-expired")
		explicitCall.Progress = func(snapshot ProgressSnapshot) {
			explicitSnapshots = append(explicitSnapshots, snapshot)
		}
		explicitExecutor := &progressContractExecutor{}
		explicitRecord, explicitErr := Execute(explicitCtx, explicitExecutor, progressCredentials,
			"original", progressProfiles(), explicitCall, retry.Config{Policy: progressExplicitPolicy()}, nil,
			cachekey.ModeOff, "v1", "explicit-expired", "trace")
		if !errors.Is(explicitErr, context.Canceled) || explicitExecutor.prepares != 0 || len(explicitSnapshots) != 0 {
			t.Fatalf("explicit expired call record=%#v prepares=%d snapshots=%#v error=%v", explicitRecord, explicitExecutor.prepares, explicitSnapshots, explicitErr)
		}

		var noWorkSnapshots []ProgressSnapshot
		explicitCall.Progress = func(snapshot ProgressSnapshot) {
			noWorkSnapshots = append(noWorkSnapshots, snapshot)
		}
		_, noWorkErr := Execute(context.Background(), &progressContractExecutor{}, func(context.Context, Profile) (Credential, error) {
			return Credential{}, errors.New("fixture credential unavailable")
		}, "original", progressProfiles(), explicitCall, retry.Config{Policy: progressExplicitPolicy()}, nil,
			cachekey.ModeOff, "v1", "explicit-no-work", "trace")
		if noWorkErr == nil || len(noWorkSnapshots) != 0 {
			t.Fatalf("explicit nil-work error=%v snapshots=%#v", noWorkErr, noWorkSnapshots)
		}
	})

	t.Run("terminal failure carries finalized stop reason", func(t *testing.T) {
		profile := progressProfile("primary", "primary-model")
		failure := errors.New("synthetic terminal failure")
		executor := &progressContractExecutor{outcomes: []progressContractOutcome{{err: failure}}}
		var snapshots []ProgressSnapshot
		call := Call{CallType: "text", Progress: func(snapshot ProgressSnapshot) {
			snapshots = append(snapshots, snapshot)
		}}
		record, err := Execute(context.Background(), executor, progressCredentials, profile.ID,
			map[string]Profile{profile.ID: profile}, call,
			retry.Config{Policy: retry.Policy{MaxAttempts: 1, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}}}, nil,
			cachekey.ModeOff, "v1", "failed-call", "failed-trace")
		if !errors.Is(err, failure) || record.StopReason == "" {
			t.Fatalf("record=%#v error=%v", record, err)
		}
		if len(snapshots) == 0 {
			t.Fatal("terminal failure emitted no progress snapshots")
		}
		last := snapshots[len(snapshots)-1]
		if last.Type != "run.terminal" || !last.Terminal || last.StopReason != record.StopReason || last.AttemptsUsed != 1 {
			t.Fatalf("terminal snapshot=%#v, record stop reason=%q", last, record.StopReason)
		}
	})

	t.Run("cache hits emit start and terminal without attempt events", func(t *testing.T) {
		t.Run("simple execution", func(t *testing.T) {
			profile := progressProfile("primary", "primary-model")
			cache := &progressContractCache{}
			call := Call{CallType: "text", UserPrompt: "cache fixture", Context: ObservabilityContext{RunID: "simple-cache"}}
			freshExecutor := &progressContractExecutor{outcomes: []progressContractOutcome{{
				result: progressResult("cached", StreamDiagnostics{}, 1),
			}}}
			config := retry.Config{Policy: retry.Policy{MaxAttempts: 2, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}}}
			_, err := Execute(context.Background(), freshExecutor, progressCredentials, profile.ID,
				map[string]Profile{profile.ID: profile}, call, config, cache,
				cachekey.ModeCache, "v1", "fresh-call", "trace")
			if err != nil {
				t.Fatal(err)
			}
			cache.found = true
			var snapshots []ProgressSnapshot
			call.Progress = func(snapshot ProgressSnapshot) { snapshots = append(snapshots, snapshot) }
			cacheExecutor := &progressContractExecutor{}
			record, err := Execute(context.Background(), cacheExecutor, progressCredentials, profile.ID,
				map[string]Profile{profile.ID: profile}, call, config, cache,
				cachekey.ModeCache, "v1", "cached-call", "trace")
			if err != nil || !record.Cache.Served || len(record.Attempts) != 0 || cacheExecutor.executes != 0 {
				t.Fatalf("cache record=%#v executes=%d error=%v", record, cacheExecutor.executes, err)
			}
			assertProgressEvents(t, snapshots, "simple-cache", "cached-call", "trace", []progressEventExpectation{
				{eventType: "run.started", stage: stageOriginalGenerate, profileID: profile.ID, remaining: 2},
				{eventType: "run.terminal", stage: stageOriginalGenerate, profileID: profile.ID, remaining: 2, terminal: true},
			})
		})

		t.Run("explicit recovery", func(t *testing.T) {
			profiles := progressProfiles()
			cache := &progressContractCache{}
			call := progressStructuredCall("explicit-cache")
			freshExecutor := &progressContractExecutor{outcomes: []progressContractOutcome{{
				result: progressResult(map[string]any{"answer": true}, StreamDiagnostics{}, 1),
			}}}
			config := retry.Config{Policy: progressExplicitPolicy()}
			_, err := Execute(context.Background(), freshExecutor, progressCredentials, "original", profiles,
				call, config, cache, cachekey.ModeCache, "v1", "fresh-call", "trace")
			if err != nil {
				t.Fatal(err)
			}
			cache.found = true
			var snapshots []ProgressSnapshot
			call.Progress = func(snapshot ProgressSnapshot) { snapshots = append(snapshots, snapshot) }
			cacheExecutor := &progressContractExecutor{}
			record, err := Execute(context.Background(), cacheExecutor, progressCredentials, "original", profiles,
				call, config, cache, cachekey.ModeCache, "v1", "cached-call", "trace")
			if err != nil || !record.Cache.Served || len(record.Attempts) != 0 || cacheExecutor.executes != 0 {
				t.Fatalf("cache record=%#v executes=%d error=%v", record, cacheExecutor.executes, err)
			}
			assertProgressEvents(t, snapshots, "explicit-cache", "cached-call", "trace", []progressEventExpectation{
				{eventType: "run.started", stage: stageOriginalGenerate, profileID: "original", reasoning: "highest", remaining: 2},
				{eventType: "run.terminal", stage: stageOriginalGenerate, profileID: "original", reasoning: "highest", remaining: 2, terminal: true},
			})
		})
	})
}

type progressEventExpectation struct {
	eventType string
	stage     string
	profileID string
	reasoning string
	attempt   int
	used      int
	remaining int
	terminal  bool
}

type progressCounters struct {
	received   int64
	events     int64
	output     int64
	codePoints int64
}

type progressContractOutcome struct {
	result ProviderResult
	err    error
}

type progressContractExecutor struct {
	outcomes []progressContractOutcome
	prepares int
	executes int
}

func (executor *progressContractExecutor) Prepare(_ context.Context, profile Profile, _ Credential, call Call) (PreparedOperation, error) {
	executor.prepares++
	return PreparedOperation{
		Operation: cachekey.Operation{
			SchemaVersion: cachekey.OperationSchemaVersion, Protocol: profile.APIInferenceType,
			Endpoint: cachekey.Endpoint{Identity: profile.BaseURL, Method: "POST", Path: "/run"},
			Model:    profile.ModelID, Payload: map[string]any{}, SemanticHeaders: map[string]any{},
			ResponseProjection: cachekey.ResponseProjection{Provider: profile.Provider, Kind: "fixture", Version: "v1"},
		},
		Opaque: call,
	}, nil
}

func (executor *progressContractExecutor) Execute(_ context.Context, operation PreparedOperation) (ProviderResult, error) {
	if executor.executes >= len(executor.outcomes) {
		return ProviderResult{}, errors.New("unexpected progress contract execution")
	}
	outcome := executor.outcomes[executor.executes]
	executor.executes++
	if operation.StreamProgress != nil {
		operation.StreamProgress(outcome.result.Stream)
	}
	return outcome.result, outcome.err
}

func progressProfile(id, model string) Profile {
	return Profile{
		ID: id, Provider: "fixture", APIInferenceType: "responses", BaseURL: "https://fixture.example",
		ModelID: model, SupportsStructuredOutput: true,
		ReasoningEffortMap: map[string]map[string]any{"lowest": {}, "highest": {}},
	}
}

func progressProfiles() map[string]Profile {
	original := progressProfile("original", "original-model")
	repair := progressProfile("repair-l", "repair-model")
	return map[string]Profile{original.ID: original, repair.ID: repair}
}

func progressCredentials(context.Context, Profile) (Credential, error) {
	return Credential{APIKey: "synthetic-progress-key"}, nil
}

func progressResult(output any, stream StreamDiagnostics, inputTokens int64) ProviderResult {
	return ProviderResult{
		ProviderDispatched: true,
		Output:             output,
		Stream:             stream,
		Accounting: Ledger{
			Usage: progressUsage(inputTokens),
			Cost:  accounting.UnavailableCost(),
		},
	}
}

func progressUsage(inputTokens int64) Usage {
	usage, err := accounting.CompleteUsage(inputTokens, 0, 0, 1, 0)
	if err != nil {
		panic(err)
	}
	return usage
}

type progressContractCache struct {
	record CachedResult
	found  bool
}

func (cache *progressContractCache) Get(context.Context, string, string) (CachedResult, bool, error) {
	return cache.record, cache.found, nil
}

func (cache *progressContractCache) Set(_ context.Context, _, _ string, result CachedResult) error {
	cache.record = result
	return nil
}

func progressClock(base time.Time) func() time.Time {
	calls := 0
	return func() time.Time {
		calls++
		return base.Add(time.Duration(calls) * time.Millisecond)
	}
}

func progressExplicitPolicy() retry.Policy {
	return retry.Policy{
		MaxAttempts: 2,
		RetryOn:     []retry.Category{},
		Backoff:     retry.Backoff{},
		JSONRepair: &retry.RepairPlan{
			Initial: retry.RecoveryTarget{Source: "profile", ProfileID: "repair-l", ReasoningEffort: "lowest"},
		},
	}
}

func progressStructuredCall(runID string) Call {
	return Call{
		CallType: "structured", Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"boolean"}},"required":["answer"],"additionalProperties":false}`),
		ReasoningEffort: "highest", Context: ObservabilityContext{RunID: runID},
		Origin: Origin{Client: "workspace", OperationID: "progress-operation"},
		ValidateStructured: func(value any) error {
			object, ok := value.(map[string]any)
			if !ok || object["answer"] != true {
				return errors.New("answer must be true")
			}
			return nil
		},
	}
}

func assertProgressEvents(t *testing.T, snapshots []ProgressSnapshot, runID, callID, traceID string, expected []progressEventExpectation) {
	t.Helper()
	if len(snapshots) != len(expected) {
		t.Fatalf("progress event count=%d, want %d: %#v", len(snapshots), len(expected), progressEventTypes(snapshots))
	}
	for index, want := range expected {
		got := snapshots[index]
		if got.Sequence != uint64(index+1) || got.RunID != runID || got.CallID != callID || got.TraceID != traceID ||
			got.Type != want.eventType || got.Stage != want.stage || got.Branch != "original" ||
			got.ProfileID != want.profileID || got.ReasoningEffort != want.reasoning || got.Attempt != want.attempt ||
			got.AttemptsUsed != want.used || got.AttemptsRemaining != want.remaining || got.Terminal != want.terminal {
			t.Errorf("progress snapshot %d = %#v, want %#v", index, got, want)
		}
	}
}

func assertProgressCounters(t *testing.T, snapshots []ProgressSnapshot, expected []progressCounters) {
	t.Helper()
	if len(snapshots) != len(expected) {
		t.Fatalf("counter expectations=%d snapshots=%d", len(expected), len(snapshots))
	}
	for index, want := range expected {
		got := progressCounters{
			received: snapshots[index].ReceivedBytes, events: snapshots[index].EventCount,
			output: snapshots[index].OutputBytes, codePoints: snapshots[index].OutputCodePoints,
		}
		if got != want {
			t.Errorf("snapshot %d counters=%#v, want %#v", index, got, want)
		}
	}
}

func assertProgressClockSampling(t *testing.T, snapshots []ProgressSnapshot, deadline time.Time) {
	t.Helper()
	var startedAt time.Time
	for index, snapshot := range snapshots {
		elapsedSample := snapshot.LastActivity.Add(-time.Millisecond)
		observedStart := elapsedSample.Add(-time.Duration(snapshot.ElapsedMs) * time.Millisecond)
		if index == 0 {
			startedAt = observedStart
		} else if !observedStart.Equal(startedAt) {
			t.Errorf("snapshot %d elapsed sample implies start %s, want %s", index, observedStart, startedAt)
		}
		if snapshot.DeadlineRemainingMs == nil {
			t.Fatalf("snapshot %d omitted deadline remaining", index)
		}
		wantRemaining := deadline.Sub(snapshot.LastActivity).Milliseconds() - 1
		if *snapshot.DeadlineRemainingMs != wantRemaining {
			t.Errorf("snapshot %d deadline remaining=%d, want %d", index, *snapshot.DeadlineRemainingMs, wantRemaining)
		}
	}
}

func progressEventTypes(snapshots []ProgressSnapshot) []string {
	eventTypes := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		eventTypes = append(eventTypes, snapshot.Type)
	}
	return eventTypes
}
