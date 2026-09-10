defmodule HardenLlm.LlmTraceProjectionTest do
  use ExUnit.Case, async: true

  alias HardenLlm.{LlmDiagnosticsWire, LlmTraceProjection}
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

  test "preserves tiny positive costs instead of displaying zero" do
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
  test "retired v1 records are rejected instead of projected as current diagnostics" do
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

    assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("getTrace", trace)
    assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("run", legacy)
  end

  test "retired zero-value failure records are rejected on history and trace reads" do
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

    assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("listHistory", history)

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

    assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("getTrace", trace)

    malformed = put_in(history, ["items", Access.at(0), "result", "cache", "served"], true)
    assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("listHistory", malformed)
  end

  test "every execution read uses v2 and checks its enclosing identity" do
    for version <- [nil, 1, 3] do
      result = Map.put(APIFixtures.run_result(), "schemaVersion", version)
      history = %{"items" => [Map.put(APIFixtures.history_item(), "result", result)]}
      trace = Map.put(APIFixtures.trace(), "record", result)
      assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("run", result)
      assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("listHistory", history)
      assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("getTrace", trace)
    end

    for key <- ~w(runId traceId profileId status) do
      history = %{"items" => [Map.put(APIFixtures.history_item(), key, "mismatched")]}
      assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("listHistory", history)
    end

    history = %{"items" => [Map.put(APIFixtures.history_item(), "status", "failed")]}
    assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("listHistory", history)
  end
end
