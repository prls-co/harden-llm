defmodule HardenLlmWeb.LlmResultComponents do
  @moduledoc """
  Transport-free result card: recorded input, output, and a host-supplied stats
  slot. Hosts own execution, resource loading, and collection management.
  Text expansion is local to each card and does not change stored data.
  """
  use Phoenix.Component
  alias Phoenix.LiveView.JS

  attr :id, :string, required: true
  attr :input, :any, default: nil
  attr :output, :any, default: nil
  attr :input_id, :string, default: nil
  attr :output_id, :string, default: nil
  attr :copy_output_id, :string, default: nil
  slot :stats
  slot :actions

  def llm_result(assigns) do
    assigns =
      assigns
      |> assign(:input_id, assigns.input_id || "#{assigns.id}-input")
      |> assign(:output_id, assigns.output_id || "#{assigns.id}-output")

    ~H"""
    <article id={@id} class="llm-result">
      <div class="llm-result-row">
        <span class="llm-result-label">Input</span>
        <pre id={@input_id} class="llm-result-text"><%= text(@input) || "Input unavailable." %></pre>
        <.copy_button id={"#{@id}-copy-input"} label="Copy input" value={text(@input)} />
        <button
          id={"#{@id}-expand"}
          type="button"
          class="llm-result-action"
          aria-label="Expand or collapse input and output"
          title="Expand or collapse input and output"
          aria-controls={"#{@input_id} #{@output_id}"}
          aria-expanded="false"
          phx-click={
            JS.toggle_class("is-expanded", to: "##{@id}")
            |> JS.toggle_attribute({"aria-expanded", "true", "false"})
          }
        >↕️</button>
        <span :if={@actions != []} class="llm-result-actions">{render_slot(@actions)}</span>
      </div>
      <div class="llm-result-row">
        <span class="llm-result-label">Output</span>
        <pre id={@output_id} class="llm-result-text"><%= text(@output) || "No output." %></pre>
        <.copy_button
          id={@copy_output_id || "#{@id}-copy-output"}
          label="Copy output"
          value={text(@output)}
        />
      </div>
      <div :if={@stats != []} class="llm-result-stats">{render_slot(@stats)}</div>
    </article>
    """
  end

  attr :id, :string, required: true
  attr :label, :string, required: true
  attr :value, :string, default: nil

  defp copy_button(assigns) do
    ~H"""
    <button
      id={@id}
      type="button"
      class="llm-result-action"
      phx-hook="Clipboard"
      data-copy-value={@value}
      data-copy-success="✅"
      data-copy-error="❌"
      aria-label={@label}
      title={@label}
      disabled={is_nil(@value)}
    >📋</button>
    """
  end

  defp text(nil), do: nil
  defp text(value) when is_binary(value), do: value
  defp text(value), do: Jason.encode!(value, pretty: true)
end
