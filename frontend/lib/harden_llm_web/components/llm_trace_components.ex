defmodule HardenLlmWeb.LlmTraceComponents do
  @moduledoc """
  Backend-agnostic LLM trace and stats presentation components.

  `llm_trace/1` is deliberately a presentational boundary. The host supplies
  normalized summary/detail maps, persisted resource payloads, and LiveView
  event names. The component does not know how a trace was produced, where it
  is stored, or which API client loads it. This keeps the widget embeddable in
  other LLM-facing LiveViews and applications.

  The resource contract is a map with string keys:

      %{
        "trace_url" => "/traces/trace-1",
        "curl" => "curl ...",
        "request" => %{"available" => true, "payload" => %{}},
        "response" => %{"available" => false, "message" => "..."},
        "artifacts" => [%{"href" => "/traces/...", "label" => "trace · 42 bytes"}]
      }

  A resource without `available: true` is rendered as unavailable rather than
  as an empty or fabricated payload.
  """

  use Phoenix.Component

  attr :id, :string, required: true
  attr :summary, :map, required: true
  attr :details, :map, default: %{}
  attr :resources, :map, default: %{}
  attr :details_open, :boolean, default: true
  attr :request_open, :boolean, default: false
  attr :response_open, :boolean, default: false
  attr :resource_loading?, :boolean, default: false
  attr :resource_error, :string, default: nil
  attr :details_event, :string, default: nil
  attr :details_name, :string, default: "detailsOpen"
  attr :resource_event, :string, default: nil
  attr :target, :any, default: nil
  attr :details_disabled, :boolean, default: false
  attr :details_id, :string, default: nil
  attr :details_toggle_id, :string, default: nil
  attr :curl_id, :string, default: nil
  attr :request_toggle_id, :string, default: nil
  attr :response_toggle_id, :string, default: nil
  attr :request_id, :string, default: nil
  attr :response_id, :string, default: nil
  attr :request_content_id, :string, default: nil
  attr :response_content_id, :string, default: nil
  attr :class, :any, default: nil

  @doc "Renders one expandable LLM trace summary, detail panel, and resources row."
  def llm_trace(assigns) do
    assigns =
      assigns
      |> assign(:details_id, assigns.details_id || "#{assigns.id}-details")
      |> assign(:details_toggle_id, assigns.details_toggle_id || "#{assigns.id}-details-toggle")
      |> assign(:curl_id, assigns.curl_id || "#{assigns.id}-copy-curl")
      |> assign(:request_toggle_id, assigns.request_toggle_id || "#{assigns.id}-show-request")
      |> assign(:response_toggle_id, assigns.response_toggle_id || "#{assigns.id}-show-response")
      |> assign(:request_id, assigns.request_id || "#{assigns.id}-request")
      |> assign(:response_id, assigns.response_id || "#{assigns.id}-response")
      |> assign(
        :request_content_id,
        assigns.request_content_id || "#{assigns.id}-request-content"
      )
      |> assign(
        :response_content_id,
        assigns.response_content_id || "#{assigns.id}-response-content"
      )

    ~H"""
    <div id={@id} class={[@class, "llm-trace-item"]}>
      <div
        class="llm-trace-summary"
        phx-click={@details_event}
        phx-value-name={@details_name}
        phx-value-open={to_string(!@details_open)}
        phx-target={@target}
      >
        <div>
          <span class="status-icon">{value(@summary, "status_icon") || "ℹ️"}</span>
          <strong>ID: {value(@summary, "trace_id") || "—"}</strong>
          <span
            :if={present?(value(@summary, "model_id"))}
            class="llm-trace-model ullm-mono"
            title="Model"
          >Model: {value(@summary, "model_id")}</span>
          <span
            :if={present?(value(@summary, "error_category"))}
            class="error-category"
          >({value(@summary, "error_category")})</span>
        </div>
        <div>
          <span
            :for={metric <- list_value(@summary, "metrics")}
            id={value(metric, "id")}
            class={value(metric, "class")}
            title={value(metric, "title") || value(metric, "label")}
            role={value(metric, "role")}
            aria-label={value(metric, "aria_label")}
            data-cache-status={value(metric, "data_cache_status")}
          >{value(metric, "value")}</span>
        </div>
      </div>

      <div class="trace-resources trace-controls" aria-label="Trace resources">
        <button
          id={@details_toggle_id}
          type="button"
          phx-click={@details_event}
          phx-value-name={@details_name}
          phx-value-open={to_string(!@details_open)}
          phx-target={@target}
          aria-label={if @details_open, do: "Hide trace details", else: "Show trace details"}
          aria-expanded={to_string(@details_open)}
          disabled={@details_disabled}
        >{if @details_open, do: "Hide", else: "Details"}</button>

        <%= if present?(trace_url(@resources)) do %>
          <a
            href={trace_url(@resources)}
            target="_blank"
            rel="noopener noreferrer"
          >View JSON Trace</a>
        <% else %>
          <button type="button" class="trace-resource-disabled" disabled>View JSON Trace</button>
        <% end %>

        <button
          id={@curl_id}
          type="button"
          phx-hook="Clipboard"
          data-copy-value={curl(@resources)}
          disabled={not present?(curl(@resources))}
        >Copy cURL</button>

        <button
          id={@request_toggle_id}
          type="button"
          phx-click={@resource_event}
          phx-value-kind="request"
          phx-target={@target}
          aria-expanded={to_string(@request_open)}
          disabled={not resource_available?(@resources, "request") or @resource_loading?}
        >{if @request_open, do: "Hide Request", else: "Show Request"}</button>

        <button
          id={@response_toggle_id}
          type="button"
          phx-click={@resource_event}
          phx-value-kind="response"
          phx-target={@target}
          aria-expanded={to_string(@response_open)}
          disabled={not resource_available?(@resources, "response") or @resource_loading?}
        >{if @response_open, do: "Hide Response", else: "Show Response"}</button>

        <a
          :for={artifact <- artifact_links(@resources)}
          href={value(artifact, "href")}
        >{value(artifact, "label")}</a>
      </div>

      <div
        :if={@details_open}
        id={@details_id}
        class="llm-trace-details"
      >
        <p><strong>Trace ID:</strong> {value(@details, "trace_id") || "—"}</p>
        <p :if={present?(value(@details, "run_id"))}>
          <strong>Run ID:</strong> {value(@details, "run_id")}
        </p>
        <p :if={present?(value(@details, "status"))}>
          <strong>Status:</strong> {value(@details, "status")}
        </p>
        <p :if={present?(value(@details, "cache_status"))}>
          <strong>Harden-LLM cache:</strong> {value(@details, "cache_status")}
        </p>
        <p :if={not is_nil(value(@details, "used_repair"))}>
          <strong>Used Repair:</strong> {if value(@details, "used_repair"), do: "Yes", else: "No"}
        </p>
        <strong>Attempts:</strong>
        <ul>
          <li :for={attempt <- list_value(@details, "attempts")}>
            Attempt {value(attempt, "attempt")}: {value(attempt, "category")} ({format_status(
              value(attempt, "status_code")
            )})
            <span :if={value(attempt, "retryable")}>
              - Retried after {value(attempt, "delay_ms")}ms
            </span>
          </li>
        </ul>
      </div>

      <p
        :if={@resource_loading?}
        id={"#{@id}-resource-loading"}
        class="trace-resource-muted"
        role="status"
      >
        Loading trace JSON…
      </p>
      <p
        :if={present?(@resource_error)}
        id={"#{@id}-resource-error"}
        class="trace-resource-muted"
        role="alert"
      >
        {@resource_error}
      </p>

      <div
        :if={!@resource_loading? and is_nil(@resource_error) and (@request_open or @response_open)}
        class="trace-data-display"
      >
        <.trace_resource_block
          :if={@request_open}
          id={@request_id}
          title="Request"
          resource={resource(@resources, "request")}
          missing_message="Request payload is not available for this trace."
          content_id={@request_content_id}
        />
        <.trace_resource_block
          :if={@response_open}
          id={@response_id}
          title="Response"
          resource={resource(@resources, "response")}
          missing_message="Response payload is not available for this trace."
          content_id={@response_content_id}
        />
      </div>
    </div>
    """
  end

  attr :id, :string, required: true
  attr :stats, :map, required: true
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

  @doc "Renders reusable aggregate LLM statistics from a normalized stats map."
  def llm_stats_summary(assigns) do
    ~H"""
    <section id={@id} aria-label={@aria_label} class={@class}>
      <div class="flex items-center justify-between gap-3">
        <h2 class="font-semibold text-slate-950">{@title}</h2>
        <%= cond do %>
          <% present?(@navigate) -> %>
            <a href={@navigate} class="text-xs font-semibold text-teal-700">{@link_label}</a>
          <% present?(@subtitle) -> %>
            <span class="text-xs text-slate-500">{@subtitle}</span>
          <% true -> %>
            nil
        <% end %>
      </div>
      <dl class={@grid_class}>
        <div :for={{key, default_label} <- stats_fields()} class={@fact_class}>
          <dt class="text-slate-500">{Map.get(@labels, key, default_label)}</dt>
          <dd class={@value_class}>{stats_display(@stats, key)}</dd>
        </div>
      </dl>
    </section>
    """
  end

  attr :id, :string, required: true
  attr :title, :string, required: true
  attr :resource, :map, default: %{}
  attr :missing_message, :string, required: true
  attr :content_id, :string, default: nil

  @doc false
  def trace_resource_block(assigns) do
    ~H"""
    <div id={@id} class="trace-data-section">
      <h4>{@title}</h4>
      <%= if resource_payload_present?(@resource) do %>
        <pre id={@content_id || @id} class="trace-json ullm-mono"><%= payload_text(@resource) %></pre>
      <% else %>
        <p class="trace-resource-muted">{@missing_message}</p>
      <% end %>
    </div>
    """
  end

  defp value(map, key, default \\ nil)
  defp value(map, _key, default) when not is_map(map), do: default
  defp value(map, key, default), do: Map.get(map, key, default)

  defp list_value(map, key), do: if(is_list(value(map, key)), do: value(map, key), else: [])

  defp present?(value) when is_binary(value), do: String.trim(value) != ""
  defp present?(value), do: not is_nil(value)

  defp format_status(nil), do: ""
  defp format_status(value), do: value

  defp trace_url(resources), do: value(resources, "trace_url")
  defp curl(resources), do: value(resources, "curl")
  defp resource(resources, key), do: value(resources, key, %{})

  defp resource_available?(resources, key) do
    value(resource(resources, key), "available", false) == true
  end

  defp resource_payload_present?(resource) do
    is_map(resource) and
      value(resource, "available", false) == true and
      Map.has_key?(resource, "payload")
  end

  defp payload_text(resource) do
    case value(resource, "payload", :missing) do
      :missing -> ""
      value when is_binary(value) -> value
      nil -> "null"
      value -> Jason.encode!(value, pretty: true)
    end
  end

  defp artifact_links(resources) do
    resources
    |> value("artifacts", [])
    |> then(&if(is_list(&1), do: &1, else: []))
    |> Enum.filter(&(present?(value(&1, "href")) and present?(value(&1, "label"))))
  end

  defp stats_fields do
    [
      {"success", "Success"},
      {"failed", "Failed"},
      {"timeout", "Timeout"},
      {"prompt_tokens", "Prompt tokens"},
      {"cache_read_tokens", "Cache read"},
      {"output_tokens", "Output tokens"},
      {"total_tokens", "Tokens"},
      {"known_cost", "Known cost"},
      {"average_duration", "Avg duration ms"}
    ]
  end

  defp stats_display(stats, key) do
    stats_key = %{
      "success" => :success,
      "failed" => :failed,
      "timeout" => :timeout,
      "prompt_tokens" => :prompt_tokens,
      "cache_read_tokens" => :cache_read_tokens,
      "output_tokens" => :output_tokens,
      "total_tokens" => :total_tokens,
      "known_cost" => :known_cost,
      "average_duration" => :average_duration
    }

    result =
      case Map.fetch(stats, key) do
        :error -> Map.fetch(stats, Map.fetch!(stats_key, key))
        found -> found
      end

    case result do
      {:ok, nil} -> "—"
      {:ok, value} -> value
      :error -> "—"
    end
  end
end
