defmodule HardenLlm.LlmStatsProjection do
  @moduledoc """
  Pure display projection for the validated aggregate diagnostics contract.

  It has no transport, LiveView, route, or persistence dependency, which keeps
  the stats presentation reusable by other LLM-facing applications.
  """

  alias HardenLlm.LlmCostFormatter

  def project(%{"schemaVersion" => 2} = stats) do
    result = stats["resultAccounting"]
    provider = stats["providerAccounting"]
    cached = stats["cached"]

    %{
      runs: stats["totalCount"],
      success: stats["successCount"],
      failed: stats["failureCount"],
      timeout: stats["timeoutCount"],
      result_prompt_tokens: get_in(result, ["usage", "promptTokens"]),
      result_cache_read_tokens: get_in(result, ["usage", "cacheReadTokens"]),
      result_cache_creation_tokens: get_in(result, ["usage", "cacheCreationTokens"]),
      result_output_tokens: get_in(result, ["usage", "outputTokens"]),
      result_reasoning_tokens: get_in(result, ["usage", "reasoningTokens"]),
      result_total_tokens: get_in(result, ["usage", "totalTokens"]),
      result_usage_coverage: usage_coverage(get_in(result, ["usage", "coverage"])),
      result_known_cost: LlmCostFormatter.format(get_in(result, ["cost", "knownSubtotalUsd"])),
      result_cost_coverage: cost_coverage(get_in(result, ["cost", "coverage"])),
      provider_prompt_tokens: get_in(provider, ["usage", "promptTokens"]),
      provider_output_tokens: get_in(provider, ["usage", "outputTokens"]),
      provider_reasoning_tokens: get_in(provider, ["usage", "reasoningTokens"]),
      provider_total_tokens: get_in(provider, ["usage", "totalTokens"]),
      provider_usage_coverage: usage_coverage(get_in(provider, ["usage", "coverage"])),
      provider_known_cost:
        LlmCostFormatter.format(get_in(provider, ["cost", "knownSubtotalUsd"])),
      provider_cost_coverage: cost_coverage(get_in(provider, ["cost", "coverage"])),
      cached_cost: LlmCostFormatter.format(get_in(cached, ["cost", "knownSubtotalUsd"])),
      cached_cost_coverage: cost_coverage(get_in(cached, ["cost", "coverage"])),
      cached_count: cached["count"],
      total_duration: stats["totalCallDurationMs"],
      average_duration: average(stats["totalCallDurationMs"], stats["totalCount"]),
      max_duration: stats["maxCallDurationMs"],
      over_budget_count: stats["overBudgetCount"],
      max_over_budget: stats["maxOverBudgetMs"]
    }
  end

  defp average(_duration, 0), do: nil
  defp average(duration, count), do: div(duration, count)

  defp usage_coverage(coverage) do
    "#{coverage["complete"]} complete · #{coverage["partial"]} partial · " <>
      "#{coverage["unavailable"]} unavailable · #{coverage["inconsistent"]} inconsistent"
  end

  defp cost_coverage(coverage) do
    "#{coverage["exact"]} exact · #{coverage["partial"]} partial · " <>
      "#{coverage["unknown"]} unknown · #{coverage["unavailable"]} unavailable"
  end
end
