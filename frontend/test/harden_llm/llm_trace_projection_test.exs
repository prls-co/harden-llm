defmodule HardenLlm.LlmTraceProjectionTest do
  use ExUnit.Case, async: true

  alias HardenLlm.LlmTraceProjection
  alias HardenLlmWeb.APIFixtures

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-036

  test "projects immutable trace identity and zero-token failures" do
    result =
      APIFixtures.run_result()
      |> Map.put("status", "failed")
      |> Map.put("category", "rate_limit")
      |> Map.put("statusCode", 429)
      |> put_in(["usage", "totalTokens"], 0)

    assert LlmTraceProjection.trace_available?(result)
    assert LlmTraceProjection.meta(result) == "responses · https://provider.example.test/v1"
    assert LlmTraceProjection.summary(result)["model_id"] == "model-test"

    assert %{
             "profile_id" => "Primary",
             "provider" => "openai",
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
             cache_creation_tokens: 2,
             reasoning_tokens: 4,
             known_cost: "$0.0004",
             cached_cost: "$0.0001",
             cached_count: 1,
             known_cost_count: 2,
             unknown_cost_count: 1,
             total_duration: 2_580,
             average_duration: 860,
             max_duration: 1_200,
             over_budget_count: 1,
             max_over_budget: 50
           } = LlmTraceProjection.stats(APIFixtures.stats())
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
end
