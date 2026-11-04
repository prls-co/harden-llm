defmodule HardenLlmUI.TestHostLive do
  use Phoenix.LiveView, layout: false

  import HardenLlmUI.{
    InputComponents,
    ResponseComponents,
    TraceComponents,
    HistoryComponents,
    Pagination
  }

  alias HardenLlmUI.{HistoryComponents, ProfileWidgetState, TraceProjection}

  def mount(_params, _session, socket) do
    fixture =
      Path.expand("../../../../../test/fixtures/reference/workspace.json", __DIR__)
      |> File.read!()
      |> Jason.decode!()

    profiles = fixture["profiles"]["profiles"]

    rows =
      Map.new([{"query", nil, "lowest"}, {"utility", "Repair Provider", "highest"}], fn
        {role, selected, reasoning} ->
          id = ProfileWidgetState.resolve_selected_profile_id(profiles, selected)
          profile = Enum.find(profiles, &(&1["id"] == id))

          {role,
           %{
             profile: id,
             reasoning: ProfileWidgetState.profile_reasoning_effort(profile, reasoning)
           }}
      end)

    {:ok,
     assign(socket,
       profiles: profiles,
       rows: rows,
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
      <form id="portable-profile-form" phx-change="portable-profiles-change">
        <HardenLlmUI.ProfileWidgetComponent.profile_row
          :for={role <- ["query", "utility"]}
          category={role}
          profile_input_id={"portable-#{role}-profile"}
          profile_name={"row[#{role}_profile]"}
          profile_value={@rows[role].profile}
          profile_options={ProfileWidgetState.profile_options(@profiles)}
          profile_allow_custom={false}
          profile_required={true}
          profile_change={nil}
          reasoning_input_id={"portable-#{role}-reasoning"}
          reasoning_name={"row[#{role}_reasoning]"}
          reasoning_value={@rows[role].reasoning}
          reasoning_options={ProfileWidgetState.reasoning_options(@profiles, @rows[role].profile)}
          reasoning_change={nil}
          target={nil}
        />
      </form>
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

  def handle_event("portable-profiles-change", %{"row" => values}, socket) do
    rows =
      Map.new(socket.assigns.rows, fn {role, current} ->
        id = Map.get(values, "#{role}_profile", current.profile)
        profile = Enum.find(socket.assigns.profiles, &(&1["id"] == id))
        reasoning = Map.get(values, "#{role}_reasoning", current.reasoning)

        {role,
         %{
           profile: id,
           reasoning: ProfileWidgetState.profile_reasoning_effort(profile, reasoning)
         }}
      end)

    {:noreply, assign(socket, :rows, rows)}
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
