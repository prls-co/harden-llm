defmodule HardenLlmUI.StatsComponents do
  @moduledoc "Captured execution identity and metrics, with host-owned expansion."
  use Phoenix.Component
  attr :id, :string, required: true
  attr :summary, :map, required: true

  def llm_stats(assigns) do
    ~H"""
    <span class="llm-trace-identity">
      <span class="status-icon">{@summary["status_icon"] || "ℹ️"}</span>
      <strong
        class="llm-trace-id"
        title={"ID: #{@summary["trace_id"] || "—"}"}
      >ID: {@summary["trace_id"] || "—"}</strong>
      <span
        :if={@summary["model_id"] not in [nil, ""]}
        class="llm-trace-model ullm-mono"
        title={"Model: #{@summary["model_id"]}"}
      >Model: {@summary["model_id"]}</span>
      <span
        :if={@summary["error_category"] not in [nil, ""]}
        class="error-category"
      >({@summary["error_category"]})</span>
    </span>
    <span class="llm-trace-metrics">
      <span
        :for={metric <- @summary["metrics"] || []}
        id={if(metric["key"] not in [nil, ""], do: "#{@id}-#{metric["key"]}")}
        class={metric["class"]}
        title={metric["title"] || metric["label"]}
        role={metric["role"]}
        aria-label={metric["aria_label"]}
        data-cache-status={metric["data_cache_status"]}
      >{metric["value"]}</span>
    </span>
    """
  end
end
