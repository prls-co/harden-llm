defmodule HardenLlmWeb.LlmTraceComponents do
  @moduledoc """
  Backend-agnostic LLM trace and stats presentation components.

  `llm_trace/1` is deliberately a presentational boundary. The host supplies
  normalized summary/detail maps, persisted resource payloads, and LiveView
  event names. The component does not know how a trace was produced, where it
  is stored, or which API client loads it. This keeps the widget embeddable in
  other LLM-facing LiveViews and applications.

  Hosts open the overview (`details_open`) when expanding the controls.

  The resource contract is a map with string keys:

      %{
        "trace_url" => "/traces/trace-1",
        "curl" => "curl ...",
        "request" => %{"available" => true, "payload" => %{}},
        "response" => %{"available" => false, "message" => "..."}
      }

  A resource without `available: true` is rendered as unavailable rather than
  as an empty or fabricated payload.
  """

  use Phoenix.Component

  import HardenLlmWeb.JsonViewer, only: [json_viewer: 1]

  attr :id, :string, required: true
  attr :summary, :map, required: true
  attr :details, :map, default: %{}
  attr :resources, :map, default: %{}
  attr :details_open, :boolean, default: true
  attr :request_open, :boolean, default: false
  attr :response_open, :boolean, default: false
  attr :trace_open, :boolean, default: false
  attr :trace_loading?, :boolean, default: false
  attr :trace_error, :string, default: nil
  attr :trace_data, :any, default: nil
  attr :details_event, :string, default: nil
  attr :details_name, :string, default: "detailsOpen"
  attr :controls_open, :boolean, default: true
  attr :controls_event, :string, default: nil
  attr :controls_name, :string, default: "controlsOpen"
  attr :resource_event, :string, default: nil
  attr :target, :any, default: nil
  attr :details_disabled, :boolean, default: false
  attr :controls_disabled, :boolean, default: false
  attr :class, :any, default: nil

  @doc "Renders one LLM trace summary, detail panel, and resources row."
  def llm_trace(assigns) do
    assigns =
      assigns
      |> assign(:details_id, "#{assigns.id}-details")
      |> assign(:summary_id, "#{assigns.id}-summary")
      |> assign(:content_id, "#{assigns.id}-content")
      |> assign(:controls_id, "#{assigns.id}-controls")
      |> assign(:details_toggle_id, "#{assigns.id}-details-toggle")
      |> assign(:trace_toggle_id, "#{assigns.id}-view-json")
      |> assign(:trace_json_id, "#{assigns.id}-trace-json")
      |> assign(:curl_id, "#{assigns.id}-copy-curl")
      |> assign(:request_toggle_id, "#{assigns.id}-show-request")
      |> assign(:response_toggle_id, "#{assigns.id}-show-response")
      |> assign(:request_id, "#{assigns.id}-request")
      |> assign(:response_id, "#{assigns.id}-response")
      |> assign(:request_content_id, "#{assigns.id}-request-content")
      |> assign(:response_content_id, "#{assigns.id}-response-content")

    ~H"""
    <div id={@id} class={[@class, "llm-trace-item"]}>
      <button
        id={@summary_id}
        type="button"
        class="llm-trace-summary"
        phx-click={@controls_event}
        phx-value-name={@controls_name}
        phx-value-open={to_string(!@controls_open)}
        phx-target={@target}
        aria-controls={@content_id}
        aria-expanded={to_string(@controls_open)}
        aria-label="Trace controls"
        disabled={@controls_disabled}
      >
        <span>
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
        </span>
        <span>
          <span
            :for={metric <- list_value(@summary, "metrics")}
            id={metric_id(@id, metric)}
            class={value(metric, "class")}
            title={value(metric, "title") || value(metric, "label")}
            role={value(metric, "role")}
            aria-label={value(metric, "aria_label")}
            data-cache-status={value(metric, "data_cache_status")}
          >{value(metric, "value")}</span>
        </span>
      </button>

      <div
        id={@content_id}
        class="llm-trace-content"
        hidden={not @controls_open}
      >
        <div class="trace-resources trace-controls" id={@controls_id} aria-label="Trace resources">
          <.trace_disclosure
            id={@details_toggle_id}
            label="Overview"
            event={@details_event}
            value_name={@details_name}
            value_open={to_string(!@details_open)}
            target={@target}
            aria_label="Execution overview"
            title="Execution overview"
            controls={@details_id}
            expanded={@details_open}
            disabled={@details_disabled}
          />

          <.trace_disclosure
            id={@trace_toggle_id}
            label="Details"
            event={@resource_event}
            kind="trace"
            target={@target}
            aria_label="Full trace details"
            title="Full trace details"
            controls={@trace_json_id}
            expanded={@trace_open}
            busy={@trace_loading?}
            disabled={not present?(trace_url(@resources))}
          />

          <button
            id={@curl_id}
            type="button"
            class="trace-action"
            phx-hook="Clipboard"
            data-copy-value={curl(@resources)}
            aria-label="Copy cURL"
            title="Copy cURL"
            disabled={not present?(curl(@resources))}
          >cURL</button>

          <.trace_disclosure
            id={@request_toggle_id}
            label="Request"
            event={@resource_event}
            kind="request"
            target={@target}
            aria_label="Request payload"
            title="Request payload"
            controls={@request_id}
            expanded={@request_open}
            disabled={not resource_available?(@resources, "request")}
          />

          <.trace_disclosure
            id={@response_toggle_id}
            label="Response"
            event={@resource_event}
            kind="response"
            target={@target}
            aria_label="Response payload"
            title="Response payload"
            controls={@response_id}
            expanded={@response_open}
            disabled={not resource_available?(@resources, "response")}
          />
        </div>

        <div
          id={@details_id}
          class="llm-trace-details"
          hidden={not @details_open}
        >
          <.json_viewer id={"#{@details_id}-json"} data={@details} expand_depth={1} />
        </div>

        <section
          id={@trace_json_id}
          class="trace-data-section"
          hidden={not @trace_open}
          aria-busy={to_string(@trace_loading?)}
        >
          <h4>Trace details</h4>
          <p
            :if={@trace_loading?}
            id={"#{@id}-trace-loading"}
            class="trace-resource-muted"
            role="status"
          >
            Loading trace JSON…
          </p>
          <p
            :if={present?(@trace_error)}
            id={"#{@id}-trace-error"}
            class="trace-resource-muted"
            role="alert"
          >
            {@trace_error}
          </p>
          <.json_viewer
            :if={not is_nil(@trace_data) and is_nil(@trace_error)}
            id={"#{@trace_json_id}-content"}
            data={@trace_data}
            expand_depth={1}
          />
        </section>

        <.trace_resource_block
          id={@request_id}
          title="Request"
          resource={resource(@resources, "request")}
          missing_message="Request payload is not available for this trace."
          content_id={@request_content_id}
          open={@request_open}
        />
        <.trace_resource_block
          id={@response_id}
          title="Response"
          resource={resource(@resources, "response")}
          missing_message="Response payload is not available for this trace."
          content_id={@response_content_id}
          open={@response_open}
        />
      </div>
    </div>
    """
  end

  attr :id, :string, required: true
  attr :label, :string, required: true
  attr :event, :string, default: nil
  attr :kind, :string, default: nil
  attr :value_name, :string, default: nil
  attr :value_open, :string, default: nil
  attr :target, :any, default: nil
  attr :aria_label, :string, required: true
  attr :title, :string, required: true
  attr :controls, :string, default: nil
  attr :expanded, :boolean, default: false
  attr :busy, :boolean, default: false
  attr :disabled, :boolean, default: false

  @doc false
  defp trace_disclosure(assigns) do
    ~H"""
    <button
      id={@id}
      type="button"
      class="trace-action"
      phx-click={@event}
      phx-value-kind={@kind}
      phx-value-name={@value_name}
      phx-value-open={@value_open}
      phx-target={@target}
      aria-label={@aria_label}
      title={@title}
      aria-controls={@controls}
      aria-expanded={to_string(@expanded)}
      aria-busy={to_string(@busy)}
      disabled={@disabled}
    >{@label}</button>
    """
  end

  attr :id, :string, required: true
  attr :title, :string, required: true
  attr :resource, :map, default: %{}
  attr :missing_message, :string, required: true
  attr :content_id, :string, default: nil
  attr :open, :boolean, default: false

  @doc false
  def trace_resource_block(assigns) do
    ~H"""
    <div id={@id} class="trace-data-section" hidden={not @open}>
      <h4>{@title}</h4>
      <%= if resource_payload_present?(@resource) do %>
        <.json_viewer
          id={@content_id || @id}
          data={resource_payload(@resource)}
          expand_depth={1}
        />
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

  defp resource_payload(resource) do
    Map.get(resource, "payload")
  end

  defp metric_id(widget_id, metric) do
    case value(metric, "key") do
      key when is_binary(key) and key != "" -> "#{widget_id}-#{key}"
      _ -> nil
    end
  end
end
