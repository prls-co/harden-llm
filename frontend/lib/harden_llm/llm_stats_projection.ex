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
      result_cost: cost_presentation(get_in(result, ["cost"]), "Result"),
      provider_prompt_tokens: get_in(provider, ["usage", "promptTokens"]),
      provider_output_tokens: get_in(provider, ["usage", "outputTokens"]),
      provider_reasoning_tokens: get_in(provider, ["usage", "reasoningTokens"]),
      provider_total_tokens: get_in(provider, ["usage", "totalTokens"]),
      provider_usage_coverage: usage_coverage(get_in(provider, ["usage", "coverage"])),
      provider_cost: cost_presentation(get_in(provider, ["cost"]), "Provider"),
      cached_cost: cost_presentation(get_in(cached, ["cost"]), "Cached"),
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

  defp cost_presentation(
         %{"knownSubtotalUsd" => amount, "coverage" => coverage},
         scope
       )
       when is_number(amount) and is_map(coverage) do
    exact = coverage["exact"]
    partial = coverage["partial"]
    unknown = coverage["unknown"]
    unavailable = coverage["unavailable"]
    known = exact + partial
    incomplete = partial + unknown + unavailable
    coverage_text = cost_coverage(coverage)

    {state, text, detail} =
      cond do
        known > 0 and incomplete == 0 ->
          {:exact, LlmCostFormatter.format(amount),
           "Known #{String.downcase(scope)} subtotal is exact."}

        known > 0 ->
          {:partial, "⚠️ " <> LlmCostFormatter.format(amount), "Known subtotal; not total cost."}

        unknown > 0 ->
          {:unknown, "❔", "No known #{String.downcase(scope)} cost."}

        true ->
          {:unavailable, "—", "No #{String.downcase(scope)} cost observation was available."}
      end

    %{
      text: text,
      state: state,
      detail: detail <> " " <> coverage_text,
      aria_label: aria_label(state, scope, text)
    }
  end

  defp cost_presentation(_cost, scope) do
    %{
      text: "—",
      state: :unavailable,
      detail: "No #{String.downcase(scope)} cost observation was available.",
      aria_label: aria_label(:unavailable, scope, "—")
    }
  end

  defp aria_label(:exact, scope, text),
    do: "Exact #{String.downcase(scope)} cost, #{text}; show details"

  defp aria_label(:partial, scope, text),
    do: "Partial #{String.downcase(scope)} cost, #{without_warning(text)} known; show details"

  defp aria_label(:unknown, scope, _text), do: "#{scope} cost unknown; show details"
  defp aria_label(:unavailable, scope, _text), do: "#{scope} cost unavailable; show details"

  defp without_warning("⚠️ " <> text), do: text
  defp without_warning(text), do: text

  defp cost_coverage(coverage) do
    "#{coverage["exact"]} exact · #{coverage["partial"]} partial · " <>
      "#{coverage["unknown"]} unknown · #{coverage["unavailable"]} unavailable"
  end
end
