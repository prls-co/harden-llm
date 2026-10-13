defmodule HardenLlmUI.HistoryComponents do
  @moduledoc "Presentation of summary rows and explicitly loaded full records."
  use Phoenix.Component
  import HardenLlmUI.ResponseComponents
  import HardenLlmUI.TraceComponents
  alias HardenLlmUI.{CostFormatter, TraceProjection}

  attr :id, :string, required: true
  attr :item, :map, required: true
  attr :states, :map, required: true
  attr :trace_assigns, :map, required: true
  attr :pending?, :boolean, default: false
  attr :restore_event, :string, required: true
  attr :delete_event, :string, required: true

  def history_row(assigns) do
    ~H"""
    <.llm_result
      id={@id}
      input={input(@states, @item["runId"])}
      output={output(@states, @item["runId"])}
      search={search(@states, @item["runId"])}
    >
      <:stats>
        <p
          :if={get_in(@states, [@item["runId"], :record_loading?])}
          role="status"
        >
          Loading the full history record…
        </p>
        <p
          :if={get_in(@states, [@item["runId"], :record_error])}
          role="alert"
        >
          {get_in(@states, [@item["runId"], :record_error])}
        </p>
        <.llm_trace {@trace_assigns} />
      </:stats>
      <:actions>
        <button
          type="button"
          phx-click={@restore_event}
          phx-value-run-id={@item["runId"]}
          disabled={@pending?}
          class="llm-result-action"
          aria-label="Restore prompt"
          title="Restore prompt"
        >📝</button>
        <button
          type="button"
          phx-click={@delete_event}
          phx-value-run-id={@item["runId"]}
          disabled={@pending?}
          class="llm-result-action"
          aria-label="Delete result"
          title="Delete result"
        >🗑️</button>
      </:actions>
    </.llm_result>
    """
  end

  def record(states, id), do: get_in(states, [id, :record])
  def input(states, id), do: get_in(states, [id, :record, "request", "input", "userPrompt"])
  def output(states, id), do: get_in(states, [id, :record, "result", "output"])
  def search(states, id), do: get_in(states, [id, :record, "result", "search"])

  def summary(item, nil) do
    %{
      "status_icon" => if(item["status"] == "succeeded", do: "✅", else: "❌"),
      "trace_id" => item["runId"],
      "model_id" => item["modelId"],
      "metrics" => [
        %{"key" => "attempts", "value" => "🔁 —", "title" => "Open details for full attempts"},
        %{
          "key" => "duration",
          "value" => "⏱️ #{item["durationMs"] / 1000}s",
          "title" => "Duration"
        },
        %{
          "key" => "input-tokens",
          "value" => "📥 #{item["resultTokens"]}",
          "title" => "Recorded result tokens"
        },
        %{
          "key" => "cost",
          "value" =>
            if(item["unknownCostObservations"] > 0, do: "≥", else: "") <>
              CostFormatter.format(item["knownCostUsd"]),
          "title" => "Recorded known cost subtotal"
        }
      ]
    }
  end

  def summary(_item, record), do: TraceProjection.summary(record["result"])
end
