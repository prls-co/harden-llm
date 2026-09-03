defmodule HardenLlm.LlmTraceProjectionTest do
  use ExUnit.Case, async: true

  alias HardenLlm.{LlmDiagnosticsWire, LlmStatsProjection, LlmTraceProjection}
  alias HardenLlmWeb.APIFixtures

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-036

  test "projects immutable trace identity and zero-token failures" do
    result =
      APIFixtures.run_result()
      |> Map.put("status", "failed")
      |> put_in(["attempts", Access.at(0), "category"], "rate_limit")
      |> put_in(["attempts", Access.at(0), "httpStatus"], 429)

    assert LlmTraceProjection.trace_available?(result)

    assert LlmTraceProjection.meta(result) ==
             "responses · https://provider.example.test/v1"

    assert LlmTraceProjection.summary(result)["model_id"] == "model-test"

    assert %{
             "profile_id" => "Primary",
             "provider" => "openai",
             "result_source" => "Provider attempt 1",
             "status" => "Rate Limit (429)"
           } = LlmTraceProjection.details(result)
  end

  test "builds an absolute credential-free and POSIX-safe replay command" do
    command =
      LlmTraceProjection.curl(
        %{"profileId" => "Primary", "userPrompt" => "it's safe", "callType" => "text"},
        "https://api.example.test"
      )

    assert command =~ "curl --fail-with-body --request POST 'https://api.example.test/api/v1/run'"
    assert command =~ ~s(--header "authorization: Bearer ${HARDEN_LLM_TOKEN}")
    assert command =~ ~s(it'"'"'s safe)
    refute command =~ APIFixtures.token()
  end

  test "projects all authoritative aggregate fields and cost completeness" do
    assert %{
             runs: 3,
             success: 2,
             failed: 1,
             result_cache_creation_tokens: 2,
             result_reasoning_tokens: 4,
             result_cost: %{
               text: "⚠️ $0.0004",
               state: :partial,
               detail:
                 "Known subtotal; not total cost. 1 exact · 1 partial · 1 unknown · 0 unavailable"
             },
             provider_cost: %{
               text: "⚠️ $0.0003",
               state: :partial,
               detail:
                 "Known subtotal; not total cost. 1 exact · 1 partial · 0 unknown · 1 unavailable"
             },
             cached_cost: %{
               text: "$0.0001",
               state: :exact,
               detail:
                 "Known cached subtotal is exact. 1 exact · 0 partial · 0 unknown · 0 unavailable"
             },
             cached_count: 1,
             total_duration: 2_580,
             average_duration: 860,
             max_duration: 1_200,
             over_budget_count: 1,
             max_over_budget: 50
           } = LlmStatsProjection.project(APIFixtures.stats())
  end

  test "keeps cost display concise while preserving certainty details" do
    cases = [
      {"exact zero", %{"knownSubtotalUsd" => 0.0, "coverage" => coverage(3, 0, 0, 0)},
       %{text: "$0.0000", state: :exact}},
      {"tiny exact", %{"knownSubtotalUsd" => 0.0000224, "coverage" => coverage(3, 0, 0, 0)},
       %{text: "$0.0000224", state: :exact}},
      {"unknown only", %{"knownSubtotalUsd" => 0.0, "coverage" => coverage(0, 0, 3, 0)},
       %{text: "❔", state: :unknown}},
      {"unavailable only", %{"knownSubtotalUsd" => 0.0, "coverage" => coverage(0, 0, 0, 3)},
       %{text: "—", state: :unavailable}},
      {"known and unknown", %{"knownSubtotalUsd" => 0.0011, "coverage" => coverage(1, 0, 2, 0)},
       %{text: "⚠️ $0.0011", state: :partial}}
    ]

    for {label, cost, expected} <- cases do
      actual = LlmStatsProjection.project(stats_with_cost(cost)).result_cost

      assert actual.text == expected.text, label
      assert actual.state == expected.state, label
      assert is_binary(actual.detail), label
      assert is_binary(actual.aria_label), label
    end
  end

  defp stats_with_cost(cost) do
    APIFixtures.stats()
    |> put_in(["resultAccounting", "cost"], cost)
    |> put_in(["resultAccounting", "usage", "coverage"], coverage(3, 0, 0, 0))
    |> put_in(["providerAccounting", "usage", "coverage"], coverage(3, 0, 0, 0))
    |> put_in(["providerAccounting", "cost"], %{
      "knownSubtotalUsd" => 0.001,
      "coverage" => coverage(3, 0, 0, 0)
    })
    |> put_in(["cached", "cost"], %{
      "knownSubtotalUsd" => 0.0,
      "coverage" => coverage(1, 0, 0, 0)
    })
  end

  defp coverage(exact, partial, unknown, unavailable) do
    %{"exact" => exact, "partial" => partial, "unknown" => unknown, "unavailable" => unavailable}
  end

  test "preserves tiny positive costs instead of displaying zero" do
    stats =
      APIFixtures.stats()
      |> put_in(["resultAccounting", "cost", "knownSubtotalUsd"], 0.0000224)
      |> put_in(["resultAccounting", "cost", "coverage"], coverage(3, 0, 0, 0))

    assert LlmStatsProjection.project(stats).result_cost.text == "$0.0000224"

    result =
      APIFixtures.run_result()
      |> put_in(["accounting", "result", "cost", "knownSubtotalUsd"], 0.00000002)

    assert LlmTraceProjection.cost(result) == "$0.00000002"
  end

  test "projects local and restored resources without owning host routes" do
    result = APIFixtures.run_result()
    request = %{"profileId" => "Primary", "userPrompt" => "hello", "callType" => "text"}
    artifact_url = fn trace_id, artifact_id -> "/artifacts/#{trace_id}/#{artifact_id}" end

    local =
      LlmTraceProjection.resources_from_run(
        result,
        request,
        "https://api.example.test",
        "/traces/trace-test",
        artifact_url
      )

    assert local["request"] == %{"available" => true, "payload" => request}
    assert local["response"] == %{"available" => true, "payload" => result}

    restored =
      LlmTraceProjection.resources_from_trace(
        APIFixtures.trace(),
        result,
        "https://api.example.test",
        "/traces/trace-test",
        artifact_url
      )

    assert restored["request"]["payload"]["userPrompt"] == "safe restored prompt"

    assert restored["artifacts"] == [
             %{
               "available" => true,
               "href" => "/artifacts/trace-test/artifact-test",
               "label" => "trace · 42 bytes"
             }
           ]

    unavailable_trace =
      APIFixtures.trace()
      |> put_in(["artifacts", Access.at(0), "state"], "unavailable")

    unavailable =
      LlmTraceProjection.resources_from_trace(
        unavailable_trace,
        result,
        "https://api.example.test",
        "/traces/trace-test",
        artifact_url
      )

    assert unavailable["artifacts"] == [
             %{
               "available" => false,
               "href" => nil,
               "label" => "trace · 42 bytes · unavailable"
             }
           ]
  end

  test "strict current run decoding rejects nonavailable artifact references" do
    artifact = APIFixtures.trace()["artifacts"] |> hd() |> Map.delete("createdAt")
    current = Map.put(APIFixtures.run_result(), "artifacts", [artifact])
    assert {:ok, ^current} = LlmDiagnosticsWire.decode("run", current)

    deleting = put_in(current, ["artifacts", Access.at(0), "state"], "deleting")
    assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("run", deleting)
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-063
  test "retained v1 trace exposes captured identity and explicit unavailable accounting" do
    legacy = %{
      "runId" => "run-legacy",
      "traceId" => "trace-legacy",
      "profileId" => "Legacy Profile",
      "modelId" => "legacy-model",
      "provider" => "openai",
      "apiInferenceType" => "responses",
      "providerBaseUrl" => "https://legacy.example.test/v1",
      "output" => "retained output",
      "attempts" => [],
      "cache" => %{
        "mode" => "off",
        "status" => "disabled",
        "served" => false,
        "written" => false
      },
      "artifacts" => [],
      "totalCallDurationMs" => 42,
      "totalWaitMs" => 0,
      "overBudgetMs" => 0,
      "usedRepair" => false,
      "status" => "succeeded"
    }

    trace =
      APIFixtures.trace()
      |> Map.put("traceId", "trace-legacy")
      |> Map.put("record", legacy)
      |> put_in(["resources", "response", "payload"], legacy)

    assert {:ok, ^trace} = LlmDiagnosticsWire.decode("getTrace", trace)
    assert LlmTraceProjection.summary(legacy)["model_id"] == "legacy-model"
    assert LlmTraceProjection.cost(legacy) == "$—"

    assert %{
             "schema_label" => "retained v1",
             "result_source" => "Not captured (retained v1)",
             "producer_provider" => nil,
             "result_usage_status" => nil,
             "result_cost_status" => nil
           } = LlmTraceProjection.details(legacy)
  end

  test "production-shaped retained v1 failures remain readable without relaxing v2" do
    legacy_failure = %{
      "runId" => "run-retained-failure",
      "callId" => "",
      "traceId" => "trace-retained-failure",
      "output" => nil,
      "usage" => %{
        "inputTokens" => 0,
        "cacheReadTokens" => 0,
        "cacheCreationTokens" => 0,
        "outputTokens" => 0,
        "reasoningTokens" => 0,
        "totalTokens" => 0
      },
      "cost" => %{"totalUsd" => 0, "known" => false, "source" => ""},
      "attempts" => nil,
      "cache" => %{"mode" => "", "status" => "", "served" => false, "written" => false},
      "artifacts" => []
    }

    history = %{
      "items" => [
        %{
          "runId" => "run-retained-failure",
          "profileId" => "Primary",
          "traceId" => "trace-retained-failure",
          "status" => "failed",
          "request" => %{"profileId" => "Primary"},
          "result" => legacy_failure,
          "startedAt" => "2026-07-13T12:00:00Z",
          "completedAt" => "2026-07-13T12:00:01Z"
        }
      ]
    }

    assert {:ok, ^history} = LlmDiagnosticsWire.decode("listHistory", history)
    assert LlmTraceProjection.cache_status_label(legacy_failure) == "Unknown"
    assert LlmTraceProjection.attempt_count(legacy_failure) == 0

    retained_trace =
      legacy_failure
      |> Map.put("schemaVersion", 1)
      |> Map.put("status", "failed")
      |> Map.put("profileId", "Primary")
      |> Map.put("providerInvoked", false)

    trace =
      APIFixtures.trace()
      |> Map.put("traceId", "trace-retained-failure")
      |> Map.put("record", retained_trace)

    assert {:ok, ^trace} = LlmDiagnosticsWire.decode("getTrace", trace)
    assert LlmTraceProjection.details(retained_trace)["provider_invoked"] == false

    malformed = put_in(history, ["items", Access.at(0), "result", "cache", "served"], true)
    assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("listHistory", malformed)
  end
end
