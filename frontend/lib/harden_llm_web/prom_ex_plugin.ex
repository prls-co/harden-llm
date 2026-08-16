defmodule HardenLlmWeb.PromExPlugin do
  @moduledoc "PromEx metrics whose labels are restricted to finite operational sets."

  use PromEx.Plugin

  @routes [
    "/",
    "/healthz",
    "/history",
    "/login",
    "/logout",
    "/profiles",
    "/profiles/bundle",
    "/session/expired",
    "/traces/:trace_id/artifacts/:artifact_id",
    "/workspace"
  ]
  @live_views [
    HardenLlmWeb.HistoryLive,
    HardenLlmWeb.ProfilesLive,
    HardenLlmWeb.WorkspaceLive
  ]
  @operations HardenLlmWeb.HardenAPI.operations() |> Enum.map(& &1.id)
  @status_classes ~w(2xx 3xx 4xx 5xx transport)
  @outcomes ~w(success error)
  @duration_buckets [5, 10, 25, 50, 100, 250, 500, 1_000, 2_500, 5_000, 15_000, 65_000]

  @impl true
  def event_metrics(_opts) do
    [
      http_metrics(),
      live_view_metrics(),
      api_metrics()
    ]
  end

  @impl true
  def polling_metrics(_opts) do
    Polling.build(
      :harden_llm_web_vault_polling_metrics,
      10_000,
      {__MODULE__, :execute_vault_count, []},
      [
        last_value([:harden_llm_web, :session_vault, :entries],
          event_name: [:harden_llm_web, :session_vault, :count],
          description: "Number of ephemeral backend tokens held by the frontend vault.",
          measurement: :count
        )
      ],
      detach_on_error: false
    )
  end

  def execute_vault_count do
    count = HardenLlmWeb.SessionVault.count()
    :telemetry.execute([:harden_llm_web, :session_vault, :count], %{count: count}, %{})
  catch
    :exit, _reason ->
      :telemetry.execute([:harden_llm_web, :session_vault, :count], %{count: 0}, %{})
  end

  def http_tags(metadata) do
    status_class = status_class(get_in(metadata, [:conn, Access.key(:status)]))

    %{
      route: safe_member(metadata[:route], @routes, "other"),
      status_class: status_class,
      outcome: if(status_class in ["2xx", "3xx"], do: "success", else: "error")
    }
  end

  def http_exception_tags(_metadata),
    do: %{route: "exception", status_class: "5xx", outcome: "error"}

  def live_view_tags(metadata, outcome) do
    view = get_in(metadata, [:socket, Access.key(:view)])

    %{
      live_view: if(view in @live_views, do: inspect(view), else: "other"),
      outcome: safe_member(outcome, @outcomes, "error")
    }
  end

  def api_tags(metadata) do
    %{
      operation: safe_member(metadata[:operation], @operations, "other"),
      status_class: safe_member(metadata[:status_class], @status_classes, "transport"),
      outcome: safe_member(metadata[:outcome], @outcomes, "error")
    }
  end

  defp http_metrics do
    Event.build(
      :harden_llm_web_http_event_metrics,
      [
        counter([:harden_llm_web, :http, :requests],
          event_name: [:phoenix, :router_dispatch, :stop],
          description: "Frontend requests by bounded route and outcome.",
          measurement: fn _measurements -> 1 end,
          tags: [:route, :status_class, :outcome],
          tag_values: &__MODULE__.http_tags/1
        ),
        distribution([:harden_llm_web, :http, :request, :duration, :milliseconds],
          event_name: [:phoenix, :router_dispatch, :stop],
          description: "Frontend request latency.",
          measurement: :duration,
          tags: [:route, :status_class, :outcome],
          tag_values: &__MODULE__.http_tags/1,
          unit: {:native, :millisecond},
          reporter_options: [buckets: @duration_buckets]
        ),
        counter([:harden_llm_web, :http, :exceptions],
          event_name: [:phoenix, :router_dispatch, :exception],
          description: "Frontend request exceptions.",
          measurement: fn _measurements -> 1 end,
          tags: [:route, :status_class, :outcome],
          tag_values: &__MODULE__.http_exception_tags/1
        )
      ]
    )
  end

  defp live_view_metrics do
    Event.build(
      :harden_llm_web_live_view_event_metrics,
      [
        distribution([:harden_llm_web, :live_view, :mount, :duration, :milliseconds],
          event_name: [:phoenix, :live_view, :mount, :stop],
          description: "LiveView mount latency by module.",
          measurement: :duration,
          tags: [:live_view, :outcome],
          tag_values: &__MODULE__.live_view_success_tags/1,
          unit: {:native, :millisecond},
          reporter_options: [buckets: @duration_buckets]
        ),
        distribution([:harden_llm_web, :live_view, :event, :duration, :milliseconds],
          event_name: [:phoenix, :live_view, :handle_event, :stop],
          description: "LiveView event latency by module.",
          measurement: :duration,
          tags: [:live_view, :outcome],
          tag_values: &__MODULE__.live_view_success_tags/1,
          unit: {:native, :millisecond},
          reporter_options: [buckets: @duration_buckets]
        ),
        counter([:harden_llm_web, :live_view, :exceptions],
          event_name: [:phoenix, :live_view, :handle_event, :exception],
          description: "LiveView callback exceptions by module.",
          measurement: fn _measurements -> 1 end,
          tags: [:live_view, :outcome],
          tag_values: &__MODULE__.live_view_error_tags/1
        )
      ]
    )
  end

  def live_view_success_tags(metadata), do: live_view_tags(metadata, "success")
  def live_view_error_tags(metadata), do: live_view_tags(metadata, "error")

  defp api_metrics do
    Event.build(
      :harden_llm_web_api_event_metrics,
      [
        counter([:harden_llm_web, :api, :requests],
          event_name: [:harden_llm_web, :api, :stop],
          description: "Backend REST operations by bounded outcome.",
          measurement: fn _measurements -> 1 end,
          tags: [:operation, :status_class, :outcome],
          tag_values: &__MODULE__.api_tags/1
        ),
        distribution([:harden_llm_web, :api, :request, :duration, :milliseconds],
          event_name: [:harden_llm_web, :api, :stop],
          description: "Backend REST operation latency.",
          measurement: :duration,
          tags: [:operation, :status_class, :outcome],
          tag_values: &__MODULE__.api_tags/1,
          unit: {:native, :millisecond},
          reporter_options: [buckets: @duration_buckets]
        )
      ]
    )
  end

  defp status_class(status) when is_integer(status) and status in 100..599,
    do: "#{div(status, 100)}xx"

  defp status_class(_status), do: "5xx"

  defp safe_member(value, allowed, fallback) do
    if value in allowed, do: value, else: fallback
  end
end
