defmodule HardenLlmUI.TestHostLive do
  use Phoenix.LiveView, layout: false

  import HardenLlmUI.{
    InputComponents,
    ResponseComponents,
    TraceComponents,
    HistoryComponents,
    Pagination
  }

  alias HardenLlmUI.{HistoryComponents, TraceProjection}

  def mount(_params, _session, socket) do
    fixture =
      Path.expand("../../../../../test/fixtures/reference/workspace.json", __DIR__)
      |> File.read!()
      |> Jason.decode!()

    {:ok,
     assign(socket,
       profiles: fixture["profiles"]["profiles"],
       fixture: fixture,
       messages: [],
       settings:
         Map.new(
           ["left", "right"],
           &{&1, %{selected: "CPA Responses", open: false, trace_open: false}}
         )
     )}
  end

  def render(assigns) do
    ~H"""
    <main id="independent-host">
      <.live_component
        :for={id <- ["left", "right"]}
        module={HardenLlmUI.ProfileWidgetComponent}
        id={id}
        id_prefix={id}
        profiles={@profiles}
        selected_profile_id={@settings[id].selected}
        model_id="fixture-model"
        model_catalog={[]}
        config_open={@settings[id].open}
        reasoning_effort="lowest"
        cache_mode="cache"
        bundle_export_url="/test-host/bundle"
        recovery_policy_default={@fixture["details"]["CPA Responses"]["definition"]["recoveryPolicy"]}
        recovery_policy={@fixture["details"]["CPA Responses"]["inputs"]["recoveryPolicy"]}
      />
      <section :for={id <- ["left", "right"]} id={"#{id}-surface"}>
        <.input
          id={"#{id}-prompt"}
          name={"#{id}[prompt]"}
          type="textarea"
          label="Prompt"
          value={id <> " input"}
        />
        <.llm_result id={"#{id}-response"} input={id <> " input"} output={%{"ok" => true}}>
          <:stats>
            <.llm_trace
              id={"#{id}-stats"}
              summary={TraceProjection.summary(@fixture["record"]["result"])}
              controls_event="toggle-record"
              run_id={id}
              controls_open={@settings[id].trace_open}
            />
          </:stats>
        </.llm_result>
        <.history_row
          id={"#{id}-history"}
          item={hd(@fixture["history"]["items"])}
          states={%{@fixture["record"]["runId"] => %{record: @fixture["record"]}}}
          trace_assigns={
            %{
              id: "#{id}-history-trace",
              summary:
                HistoryComponents.summary(hd(@fixture["history"]["items"]), @fixture["record"]),
              controls_event: "toggle-record",
              run_id: id,
              controls_open: @settings[id].trace_open
            }
          }
          restore_event="restore-record"
          delete_event="delete-record"
        />
        <.pagination id={"#{id}-pages"} page={1} page_size={25} total_count={26} event="paginate" />
      </section>
      <output id="component-messages">{inspect(@messages)}</output>
    </main>
    """
  end

  def handle_event("toggle-record", %{"run-id" => id}, socket) do
    {:noreply,
     update(socket, :settings, &update_in(&1, [id, :trace_open], fn open -> not open end))}
  end

  def handle_info({:profile_widget, id, message}, socket) do
    socket = update(socket, :messages, &[{id, message} | &1])
    settings = socket.assigns.settings

    settings =
      case message do
        {:profile_widget_ui, "llmProfileConfigOpen", open} ->
          put_in(settings, [id, :open], open)

        {:profile_widget_selection, %{profile_id: profile}} ->
          put_in(settings, [id, :selected], profile)

        _ ->
          settings
      end

    {:noreply, assign(socket, :settings, settings)}
  end
end

defmodule HardenLlmUI.TestRouter do
  use Phoenix.Router
  import Phoenix.LiveView.Router

  pipeline :browser do
    plug :fetch_session
    plug :put_root_layout, false
  end

  scope "/" do
    pipe_through :browser
    live "/components", HardenLlmUI.TestHostLive
  end
end

defmodule HardenLlmUI.TestEndpoint do
  use Phoenix.Endpoint, otp_app: :harden_llm_ui

  socket "/live", Phoenix.LiveView.Socket
  plug Plug.Session, store: :cookie, key: "independent_ui", signing_salt: "test-salt"
  plug HardenLlmUI.TestRouter
end

defmodule HardenLlmUI.TestErrorHTML do
  def render(template, _assigns), do: Phoenix.Controller.status_message_from_template(template)
end
