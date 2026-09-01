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
             result_known_cost: "$0.0004",
             result_cost_coverage: "1 exact · 1 partial · 1 unknown · 0 unavailable",
             provider_known_cost: "$0.0003",
             cached_cost: "$0.0001",
             cached_count: 1,
             total_duration: 2_580,
             average_duration: 860,
             max_duration: 1_200,
             over_budget_count: 1,
             max_over_budget: 50
           } = LlmStatsProjection.project(APIFixtures.stats())
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
             %{"href" => "/artifacts/trace-test/artifact-test", "label" => "trace · 42 bytes"}
           ]
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
end
