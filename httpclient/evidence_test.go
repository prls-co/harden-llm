package httpclient

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-472

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
)

func decodedResult(t *testing.T, result map[string]any) RecordedRunOutput {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var output RecordedRunOutput
	if err := decodeWire("RecordedRunOutput", data, &output); err != nil {
		t.Fatalf("fixture is not a canonical RecordedRunOutput: %v", err)
	}
	return output
}

func TestProjectEvidenceCountsObservedDispatches(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"provider", "predispatch_attempt", "cache_hit", "cached_recovery", "miss", "write_failed", "refresh", "failed_before_dispatch"} {
		t.Run(name, func(t *testing.T) {
			result := terminalResult(fixtureEvents(t))
			input := fixtureRequest(t)
			count := 2
			switch name {
			case "predispatch_attempt":
				first := result["attempts"].([]any)[0].(map[string]any)
				first["providerUsed"], first["dispatchObserved"] = false, false
				count = 1
			case "cache_hit", "cached_recovery":
				input.CacheMode = hardenllm.CacheModeCache
				result["cache"] = map[string]any{"mode": "cache", "status": "hit", "served": true, "written": false}
				result["resultSource"] = map[string]any{"kind": "cache", "producer": result["selectedTarget"]}
				if name == "cache_hit" {
					result["attempts"], result["providerInvoked"], count = []any{}, false, 0
				}
			case "miss", "write_failed", "refresh":
				mode := "cache"
				if name == "refresh" {
					mode = "refresh"
				}
				input.CacheMode = hardenllm.CacheMode(mode)
				status := "miss"
				if name == "write_failed" {
					status = name
				}
				result["cache"] = map[string]any{"mode": mode, "status": status, "served": false, "written": name != "write_failed"}
			case "failed_before_dispatch":
				result["status"], result["output"] = "failed", nil
				result["attempts"], result["providerInvoked"], count = []any{}, false, 0
				result["resultSource"] = map[string]any{"kind": "none"}
			}
			output := decodedResult(t, result)
			evidence, err := projectEvidence(output, input)
			if err != nil || evidence == nil || evidence.ProviderAttemptCount != count || evidence.ProviderInvoked != (count > 0) || evidence.Cache != output.Cache {
				t.Fatalf("projection lost observed facts for %s: evidence=%+v err=%v", name, evidence, err)
			}
			if err := ValidateEvidence(*evidence, input.Origin.OperationID, input.CacheMode); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProjectEvidenceRejectsContradictoryFacts(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"duplicate_attempt", "attempt_profile", "producer", "provider_source_not_dispatched", "source_cache_not_served", "invoked", "wrong_origin", "wrong_model", "refresh_hit", "off_write"} {
		t.Run(name, func(t *testing.T) {
			result := decodedResult(t, terminalResult(fixtureEvents(t)))
			input := fixtureRequest(t)
			switch name {
			case "duplicate_attempt":
				result.Attempts[1].Number = result.Attempts[0].Number
			case "attempt_profile":
				result.Attempts[0].ProfileID = "other-profile"
			case "producer":
				result.ResultSource.Producer.ModelID = "other-model"
			case "provider_source_not_dispatched":
				result.Attempts[1].ProviderUsed = false
			case "source_cache_not_served":
				result.ResultSource.Kind = hardenllm.ResultSourceCache
				result.ResultSource.AttemptNumber = 0
			case "invoked":
				result.ProviderInvoked = false
			case "wrong_origin":
				result.Origin.OperationID = "other-operation"
			case "wrong_model":
				input.ModelID = "explicit-other-model"
			case "refresh_hit":
				input.CacheMode = hardenllm.CacheModeRefresh
				result.Cache = hardenllm.CacheResult{Mode: input.CacheMode, Status: "hit", Served: true}
			case "off_write":
				result.Cache.Written = true
			}
			if evidence, err := projectEvidence(result, input); err == nil || evidence != nil {
				t.Fatalf("accepted %s: %+v", name, evidence)
			}
		})
	}
}

func TestRunPreservesFailureBeforeTargetResolution(t *testing.T) {
	t.Parallel()
	events := fixtureEvents(t)
	events[2]["type"] = "run.failed"
	result := terminalResult(events)
	result["status"], result["output"], result["selectedTarget"] = "failed", nil, nil
	delete(result, "generationTarget")
	result["attempts"], result["providerInvoked"] = []any{}, false
	result["resultSource"] = map[string]any{"kind": "none"}
	_ = decodedResult(t, result)
	events[2]["data"].(map[string]any)["error"] = map[string]any{"code": "profile_unavailable", "message": "fixture profile unavailable"}
	stream := eventBytes(t, events)
	client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(stream)
	})
	out, err := client.Run(context.Background(), fixtureRequest(t))
	var failure *RunFailure
	if !errors.As(err, &failure) || failure.Problem.Code != "profile_unavailable" || out.Result.Status != "failed" || out.Result.SelectedTarget != nil || out.Evidence != nil || calls.Load() != 1 {
		t.Fatalf("lost definitive pre-target failure: status=%s evidence=%+v err=%v calls=%d", out.Result.Status, out.Evidence, err, calls.Load())
	}
}

func TestRunAllowsUnboundReferenceOriginWithoutInventingProductEvidence(t *testing.T) {
	t.Parallel()
	events := fixtureEvents(t)
	delete(terminalResult(events), "origin")
	stream := eventBytes(t, events)
	client, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(stream)
	})
	input := fixtureRequest(t)
	input.Origin = hardenllm.Origin{}
	out, err := client.Run(context.Background(), input)
	if err != nil || out.Result.Status != "succeeded" || out.Evidence != nil || out.Result.Origin != input.Origin || calls.Load() != 1 {
		t.Fatalf("valid optional origin became unknown or invented evidence: status=%s evidence=%+v err=%v", out.Result.Status, out.Evidence, err)
	}
}
