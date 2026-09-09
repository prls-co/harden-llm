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
        "artifacts" => [%{"available" => true, "label" => "trace · 42 bytes"}]
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
        aria-label={if @controls_open, do: "Hide trace controls", else: "Show trace controls"}
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
          <button
            id={@details_toggle_id}
            type="button"
            class={[
              "trace-action",
              if(@details_open, do: "trace-action-active", else: "trace-action-inactive")
            ]}
            phx-click={@details_event}
            phx-value-name={@details_name}
            phx-value-open={to_string(!@details_open)}
            phx-target={@target}
            aria-label="Trace details"
            title="Trace details"
            aria-controls={@details_id}
            aria-expanded={to_string(@details_open)}
            aria-pressed={to_string(@details_open)}
            disabled={@details_disabled}
          >Details</button>

          <button
            id={@trace_toggle_id}
            type="button"
            class={[
              "trace-action",
              if(@trace_loading?, do: "trace-action-loading", else: nil),
              if(@trace_open, do: "trace-action-active", else: "trace-action-inactive")
            ]}
            phx-click={@resource_event}
            phx-value-kind="trace"
            phx-target={@target}
            aria-label="JSON trace"
            title="JSON trace"
            aria-expanded={to_string(@trace_open)}
            aria-pressed={to_string(@trace_open)}
            aria-busy={to_string(@trace_loading?)}
            disabled={not present?(trace_url(@resources)) or @trace_loading?}
          >JSON</button>

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

          <button
            id={@request_toggle_id}
            type="button"
            class={[
              "trace-action",
              if(@request_open, do: "trace-action-active", else: "trace-action-inactive")
            ]}
            phx-click={@resource_event}
            phx-value-kind="request"
            phx-target={@target}
            aria-label="Request payload"
            title="Request payload"
            aria-expanded={to_string(@request_open)}
            aria-pressed={to_string(@request_open)}
            disabled={not resource_available?(@resources, "request") or @resource_loading?}
          >Request</button>

          <button
            id={@response_toggle_id}
            type="button"
            class={[
              "trace-action",
              if(@response_open, do: "trace-action-active", else: "trace-action-inactive")
            ]}
            phx-click={@resource_event}
            phx-value-kind="response"
            phx-target={@target}
            aria-label="Response payload"
            title="Response payload"
            aria-expanded={to_string(@response_open)}
            aria-pressed={to_string(@response_open)}
            disabled={not resource_available?(@resources, "response") or @resource_loading?}
          >Response</button>

          <%= for {artifact, index} <- Enum.with_index(artifact_links(@resources)) do %>
            <button
              :if={value(artifact, "available", false)}
              id={"#{@id}-artifact-#{index}"}
              type="button"
              phx-click={@resource_event}
              phx-value-kind="trace"
              phx-target={@target}
              class={[
                "trace-action",
                if(@trace_open, do: "trace-action-active", else: "trace-action-inactive")
              ]}
              aria-label={value(artifact, "label")}
              title={value(artifact, "label")}
              aria-expanded={to_string(@trace_open)}
              aria-pressed={to_string(@trace_open)}
              disabled={@trace_loading?}
            >{value(artifact, "label")}</button>
            <span :if={not value(artifact, "available", false)} aria-disabled="true">
              {value(artifact, "label")}
            </span>
          <% end %>
        </div>

        <div
          :if={@details_open}
          id={@details_id}
          class="llm-trace-details"
        >
          <p><strong>Trace ID:</strong> {value(@details, "trace_id") || "—"}</p>
          <p><strong>Diagnostics schema:</strong> {value(@details, "schema_label") || "—"}</p>
          <p :if={present?(value(@details, "run_id"))}>
            <strong>Run ID:</strong> {value(@details, "run_id")}
          </p>
          <p :if={present?(value(@details, "profile_id"))}>
            <strong>Profile:</strong> {value(@details, "profile_id")}
          </p>
          <p :if={present?(value(@details, "model_id"))}>
            <strong>Model:</strong> {value(@details, "model_id")}
          </p>
          <p :if={present?(value(@details, "provider"))}>
            <strong>Provider:</strong> {value(@details, "provider")}
          </p>
          <p :if={present?(value(@details, "api_inference_type"))}>
            <strong>API inference type:</strong> {value(@details, "api_inference_type")}
          </p>
          <p :if={present?(value(@details, "provider_base_url"))}>
            <strong>Selected endpoint:</strong> {value(@details, "provider_base_url")}
          </p>
          <p :if={present?(value(@details, "result_source"))}>
            <strong>Result source:</strong> {value(@details, "result_source")}
          </p>
          <p :if={present?(value(@details, "producer_profile_id"))}>
            <strong>Producer profile:</strong> {value(@details, "producer_profile_id")}
          </p>
          <p :if={present?(value(@details, "producer_provider"))}>
            <strong>Producer target:</strong>
            {value(@details, "producer_provider")} · {value(@details, "producer_protocol")} · {value(
              @details,
              "producer_model_id"
            )} · {value(@details, "producer_endpoint")}
          </p>
          <p :if={not is_nil(value(@details, "provider_invoked"))}>
            <strong>Provider invoked this run:</strong>
            {if value(@details, "provider_invoked"), do: "Yes", else: "No"}
          </p>
          <p :if={present?(value(@details, "result_usage_status"))}>
            <strong>Result accounting:</strong>
            usage {value(@details, "result_usage_status")} · cost {value(
              @details,
              "result_cost_status"
            )}
          </p>
          <p :if={present?(value(@details, "provider_usage_status"))}>
            <strong>Provider accounting:</strong>
            usage {value(@details, "provider_usage_status")} · cost {value(
              @details,
              "provider_cost_status"
            )}
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
              Attempt {value(attempt, "attempt")}<span :if={value(attempt, "retry_local_attempt")}>
                / retry {value(attempt, "retry_local_attempt")}</span>: {value(
                attempt,
                "category"
              )} ({format_status(value(attempt, "status_code"))}) · {value(attempt, "duration_ms") ||
                "—"}ms
              <span :if={present?(value(attempt, "provider"))}>
                · {value(attempt, "provider")} / {value(attempt, "model_id")}
              </span>
              <span :if={not is_nil(value(attempt, "provider_used"))}>
                · provider {if value(attempt, "provider_used"), do: "used", else: "not used"}
              </span>
              <span :if={value(attempt, "retryable")}>
                - Retried after {value(attempt, "delay_ms")}ms
              </span>
            </li>
          </ul>
        </div>

        <p
          :if={@trace_loading? or @resource_loading?}
          id={"#{@id}-resource-loading"}
          class="trace-resource-muted"
          role="status"
        >
          Loading trace JSON…
        </p>
        <p
          :if={present?(@trace_error) or present?(@resource_error)}
          id={"#{@id}-resource-error"}
          class="trace-resource-muted"
          role="alert"
        >
          {@trace_error || @resource_error}
        </p>

        <div
          :if={
            not (@trace_loading? or @resource_loading?) and is_nil(@trace_error) and
              is_nil(@resource_error) and
              (@trace_open or @request_open or @response_open)
          }
          class="trace-data-display"
        >
          <section
            :if={@trace_open and is_map(@trace_data)}
            id={@trace_json_id}
            class="trace-data-section"
          >
            <h4>JSON Trace</h4>
            <div id={"#{@trace_json_id}-content"} class="trace-json trace-json-display">
              <.json_value id={"#{@trace_json_id}-root"} value={@trace_data} root />
            </div>
          </section>
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
    </div>
    """
  end

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

  @doc "Renders reusable aggregate LLM statistics from a normalized stats map."
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
          <dt class="text-slate-500">{Map.get(@labels, key, default_label)}</dt>
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
        <div id={@content_id || @id} class="trace-json trace-json-display">
          <.json_value
            id={"#{@content_id || @id}-root"}
            value={resource_payload(@resource)}
            root
          />
        </div>
      <% else %>
        <p class="trace-resource-muted">{@missing_message}</p>
      <% end %>
    </div>
    """
  end

  attr :id, :string, required: true
  attr :value, :any, required: true
  attr :label, :any, default: nil
  attr :root, :boolean, default: false

  @doc "Renders a dependency-free, foldable JSON value tree."
  def json_value(assigns) do
    ~H"""
    <%= if json_container?(@value) do %>
      <details id={@id} class="trace-json-node" open={@root}>
        <summary>
          <span :if={not is_nil(@label)} class="trace-json-key">{json_key(@label)}</span>
          <span class="trace-json-type">{json_container_label(@value)}</span>
        </summary>
        <div class="trace-json-children">
          <.json_value
            :for={{key, child, index} <- json_entries(@value)}
            id={json_child_id(@id, index)}
            label={key}
            value={child}
          />
        </div>
      </details>
    <% else %>
      <div id={@id} class="trace-json-leaf">
        <span :if={not is_nil(@label)} class="trace-json-key">{json_key(@label)}</span>
        <span class="trace-json-value">{json_scalar(@value)}</span>
      </div>
    <% end %>
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
    case value(resource, "payload", :missing) do
      :missing -> nil
      payload when is_binary(payload) -> decode_json_payload(payload)
      payload -> payload
    end
  end

  defp decode_json_payload(payload) do
    case Jason.decode(payload) do
      {:ok, value} -> value
      {:error, _reason} -> payload
    end
  end

  defp json_container?(value), do: is_map(value) or is_list(value)

  defp json_entries(value) when is_map(value) do
    value
    |> Enum.sort_by(fn {key, _value} -> json_key_sort_value(key) end)
    |> Enum.with_index()
    |> Enum.map(fn {{key, child}, index} -> {key, child, index} end)
  end

  defp json_entries(value) when is_list(value) do
    Enum.with_index(value)
    |> Enum.map(fn {child, index} -> {index, child, index} end)
  end

  defp json_entries(_value), do: []

  defp json_key_sort_value(key) when is_binary(key), do: key
  defp json_key_sort_value(key), do: inspect(key)

  defp json_child_id(parent_id, index), do: "#{parent_id}-#{index}"

  defp json_container_label(value) when is_map(value), do: "{#{map_size(value)} keys}"
  defp json_container_label(value) when is_list(value), do: "[#{length(value)} items]"

  defp json_key(key) when is_integer(key), do: "[#{key}]"
  defp json_key(key), do: Jason.encode!(to_string(key)) <> ":"

  defp json_scalar(nil), do: "null"
  defp json_scalar(value) when is_boolean(value) or is_number(value), do: Jason.encode!(value)
  defp json_scalar(value) when is_binary(value), do: Jason.encode!(value)
  defp json_scalar(value), do: Jason.encode!(inspect(value))

  defp artifact_links(resources) do
    resources
    |> value("artifacts", [])
    |> then(&if(is_list(&1), do: &1, else: []))
    |> Enum.filter(fn artifact ->
      present?(value(artifact, "label")) and
        (value(artifact, "available", true) == false or present?(value(artifact, "href")))
    end)
  end

  defp metric_id(widget_id, metric) do
    case value(metric, "key") do
      key when is_binary(key) and key != "" -> "#{widget_id}-#{key}"
      _ -> nil
    end
  end

  defp stats_fields do
    [
      {"runs", "Runs"},
      {"success", "Success"},
      {"failed", "Failed"},
      {"timeout", "Timeout"},
      {"result_prompt_tokens", "Result prompt tokens"},
      {"result_cache_read_tokens", "Result cache read"},
      {"result_cache_creation_tokens", "Result cache creation"},
      {"result_output_tokens", "Result output tokens"},
      {"result_reasoning_tokens", "Result reasoning tokens"},
      {"result_total_tokens", "Result tokens"},
      {"result_usage_coverage", "Result usage coverage"},
      {"result_cost", "Result cost"},
      {"provider_prompt_tokens", "Provider prompt tokens"},
      {"provider_output_tokens", "Provider output tokens"},
      {"provider_reasoning_tokens", "Provider reasoning tokens"},
      {"provider_total_tokens", "Provider tokens"},
      {"provider_usage_coverage", "Provider usage coverage"},
      {"provider_cost", "Provider cost"},
      {"cached_cost", "Cached cost"},
      {"cached_count", "Cached runs"},
      {"total_duration", "Total duration ms"},
      {"average_duration", "Avg duration ms"},
      {"max_duration", "Max duration ms"},
      {"over_budget_count", "Over budget"},
      {"max_over_budget", "Max over budget ms"}
    ]
  end

  defp stats_display(stats, key) do
    stats_key = %{
      "runs" => :runs,
      "success" => :success,
      "failed" => :failed,
      "timeout" => :timeout,
      "result_prompt_tokens" => :result_prompt_tokens,
      "result_cache_read_tokens" => :result_cache_read_tokens,
      "result_cache_creation_tokens" => :result_cache_creation_tokens,
      "result_output_tokens" => :result_output_tokens,
      "result_reasoning_tokens" => :result_reasoning_tokens,
      "result_total_tokens" => :result_total_tokens,
      "result_usage_coverage" => :result_usage_coverage,
      "result_cost" => :result_cost,
      "provider_prompt_tokens" => :provider_prompt_tokens,
      "provider_output_tokens" => :provider_output_tokens,
      "provider_reasoning_tokens" => :provider_reasoning_tokens,
      "provider_total_tokens" => :provider_total_tokens,
      "provider_usage_coverage" => :provider_usage_coverage,
      "provider_cost" => :provider_cost,
      "cached_cost" => :cached_cost,
      "cached_count" => :cached_count,
      "total_duration" => :total_duration,
      "average_duration" => :average_duration,
      "max_duration" => :max_duration,
      "over_budget_count" => :over_budget_count,
      "max_over_budget" => :max_over_budget
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

  defp metric_details?(value), do: is_map(value) and is_binary(value[:detail])
end
