defmodule HardenLlmWeb.LlmStatsComponents do
  @moduledoc """
  Transport-free aggregate LLM statistics presentation components.

  Hosts provide the `AsyncResult` and refresh metadata owned by `LiveStats`.
  This module only renders the already-projected values and certainty states.
  """

  use Phoenix.Component

  attr :id, :string, required: true
  attr :stats, :any, required: true
  attr :updated_at, :any, default: nil
  attr :refresh_event, :string, default: nil
  attr :refresh_target, :any, default: nil
  attr :title, :string, default: "LLM stats summary"
  attr :subtitle, :string, default: nil
  attr :navigate, :string, default: nil
  attr :link_label, :string, default: "Full view"
  attr :class, :any, default: nil
  attr :grid_class, :string, default: "mt-4 grid grid-cols-2 gap-3 text-xs sm:grid-cols-3"
  attr :fact_class, :string, default: "min-w-0 rounded-lg border border-slate-800 p-2"
  attr :value_class, :string, default: "mt-1 truncate font-mono text-slate-200"
  attr :labels, :map, default: %{}
  attr :aria_label, :string, default: "LLM stats"

  @doc "Renders reusable aggregate LLM statistics from a projected stats map."
  def llm_stats_summary(assigns) do
    ~H"""
    <section
      id={@id}
      aria-label={@aria_label}
      aria-busy={to_string(@stats.loading == true)}
      class={@class}
    >
      <div class="flex items-center justify-between gap-3">
        <h2 class="font-semibold text-slate-950">{@title}</h2>
        <div class="flex items-center gap-3">
          <span :if={present?(@subtitle)} class="text-xs text-slate-500">{@subtitle}</span>
          <span
            :if={not is_nil(@updated_at)}
            id={"#{@id}-updated"}
            class="text-xs text-slate-500"
          >Last updated {snapshot_time(@updated_at)}</span>
          <button
            :if={present?(@refresh_event)}
            id={"#{@id}-refresh"}
            type="button"
            phx-click={@refresh_event}
            phx-target={@refresh_target}
            disabled={@stats.loading}
            class="text-xs font-semibold text-teal-700 disabled:cursor-not-allowed disabled:opacity-50"
          >{refresh_label(@stats)}</button>
          <a :if={present?(@navigate)} href={@navigate} class="text-xs font-semibold text-teal-700">
            {@link_label}
          </a>
        </div>
      </div>
      <p :if={@stats.loading} id={"#{@id}-loading"} class="mt-3 text-xs text-slate-500" role="status">
        Loading aggregate diagnostics…
      </p>
      <p :if={@stats.failed} id={"#{@id}-error"} class="mt-3 text-xs text-rose-700" role="alert">
        {stats_error(@stats, @updated_at)}
      </p>
      <dl :if={@stats.ok?} class={@grid_class}>
        <div :for={{key, default_label} <- stats_fields()} class={@fact_class}>
          <% display = stats_display(@stats.result, key) %>
          <dt class="text-slate-500">{Map.get(@labels, Atom.to_string(key), default_label)}</dt>
          <%= if metric_details?(display) do %>
            <dd class="mt-1">
              <details id={"#{@id}-#{key}-details"} class="llm-stats-disclosure">
                <summary
                  class={[
                    "llm-stats-disclosure-summary",
                    "llm-stats-disclosure-summary-#{display.state}"
                  ]}
                  aria-label={display.aria_label}
                >
                  <span class="llm-stats-disclosure-value">{display.text}</span>
                </summary>
                <p class="llm-stats-disclosure-detail">{display.detail}</p>
              </details>
            </dd>
          <% else %>
            <dd class={@value_class} title={to_string(display)}>{display}</dd>
          <% end %>
        </div>
      </dl>
    </section>
    """
  end

  defp present?(value) when is_binary(value), do: String.trim(value) != ""
  defp present?(value), do: not is_nil(value)

  defp snapshot_time(%DateTime{} = value),
    do: Calendar.strftime(value, "%Y-%m-%d %H:%M:%S UTC")

  defp snapshot_time(value), do: to_string(value)

  defp refresh_label(%{failed: failed, ok?: false}) when not is_nil(failed), do: "Retry"
  defp refresh_label(_stats), do: "Refresh"

  defp stats_error(%{ok?: true}, nil),
    do: "Aggregate diagnostics are temporarily unavailable. Showing the last successful snapshot."

  defp stats_error(%{ok?: true}, updated_at) do
    "Aggregate diagnostics are temporarily unavailable. " <>
      "Showing the last successful snapshot from #{snapshot_time(updated_at)}."
  end

  defp stats_error(_stats, _updated_at),
    do: "Aggregate diagnostics are temporarily unavailable."

  defp stats_fields do
    [
      {:runs, "Runs"},
      {:success, "Success"},
      {:failed, "Failed"},
      {:timeout, "Timeout"},
      {:result_prompt_tokens, "Result prompt tokens"},
      {:result_cache_read_tokens, "Result cache read"},
      {:result_cache_creation_tokens, "Result cache creation"},
      {:result_output_tokens, "Result output tokens"},
      {:result_reasoning_tokens, "Result reasoning tokens"},
      {:result_total_tokens, "Result tokens"},
      {:result_usage_coverage, "Result usage coverage"},
      {:result_cost, "Result cost"},
      {:provider_prompt_tokens, "Provider prompt tokens"},
      {:provider_output_tokens, "Provider output tokens"},
      {:provider_reasoning_tokens, "Provider reasoning tokens"},
      {:provider_total_tokens, "Provider tokens"},
      {:provider_usage_coverage, "Provider usage coverage"},
      {:provider_cost, "Provider cost"},
      {:cached_cost, "Cached cost"},
      {:cached_count, "Cached runs"},
      {:total_duration, "Total duration ms"},
      {:average_duration, "Avg duration ms"},
      {:max_duration, "Max duration ms"},
      {:over_budget_count, "Over budget"},
      {:max_over_budget, "Max over budget ms"}
    ]
  end

  defp stats_display(stats, key) when is_map(stats) do
    case Map.get(stats, key) do
      nil -> "—"
      value -> value
    end
  end

  defp stats_display(_stats, _key), do: "—"

  defp metric_details?(value), do: is_map(value) and is_binary(value[:detail])
end
