defmodule HardenLlmWeb.WorkspaceLive do
  use HardenLlmWeb, :live_view

  alias HardenLlm.LlmTraceProjection

  alias HardenLlmWeb.{
    APIError,
    Auth,
    HardenAPI,
    Observability,
    ProfileWidgetState,
    WorkspaceSchema
  }

  alias PrlsUI.PaginationState

  @reasoning_options [{"Lowest", "lowest"}, {"Middle", "middle"}, {"Highest", "highest"}]
  @history_page_size_options [10, 25, 50, 100]
  @maximum_history_page 9_223_372_036_854_775_807
  # Workspace keeps the historical empty prefix so existing DOM IDs remain
  # stable; reject messages from any other top-level widget instance.
  @profile_widget_prefix ""
  @ui_keys ~w(llmProfileConfigOpen modelOptionsOpen pricingOpen retryRepairOpen credentialOpen inputAdvancedOpen historyOpen outputDetailsOpen outputControlsOpen)

  @default_ui %{
    "llmProfileConfigOpen" => false,
    "modelOptionsOpen" => false,
    "pricingOpen" => false,
    "retryRepairOpen" => false,
    "credentialOpen" => false,
    "inputAdvancedOpen" => false,
    "historyOpen" => false,
    "outputDetailsOpen" => true,
    "outputControlsOpen" => true
  }

  @default_schema %{
    "type" => "object",
    "properties" => %{
      "joke" => %{"type" => "string"},
      "explanation" => %{"type" => "string"}
    },
    "required" => ["joke", "explanation"],
    "additionalProperties" => false
  }

  @default_schema_shorthand ~s({
    "joke": "string",
    "explanation": "string"
  })

  @default_state %{
    "schemaVersion" => 3,
    "selectedProfileId" => "",
    "modelId" => "",
    "systemPrompt" => "You are a helpful assistant",
    "userPrompt" => "write a haiku joke",
    "schemaShorthand" => @default_schema_shorthand,
    "schema" => @default_schema,
    "callType" => "structured",
    "cacheMode" => "cache",
    "webSearch" => false,
    "reasoningEffort" => "lowest",
    "reasoningByProfile" => %{},
    "ui" => @default_ui
  }

  @impl true
  def mount(params, _session, socket) do
    socket =
      socket
      |> assign(:page_title, "Workspace")
      |> assign(:loading?, true)
      |> assign(:backend_state, :loading)
      |> assign(:profiles, [])
      |> assign(:recovery_policy_default, %{})
      |> assign(:recovery_field_errors, %{})
      |> assign(:history, [])
      |> assign(:history_result_states, %{})
      |> assign(:history_loaded?, false)
      |> assign(:history_loading?, false)
      |> assign(:history_load_ref, nil)
      |> assign(:history_load_operation, nil)
      |> assign(:history_page, PaginationState.normalize_page(params["history_page"]))
      |> assign(
        :history_page_size,
        PaginationState.normalize_page_size(
          params["history_page_size"],
          @history_page_size_options
        )
      )
      |> assign(:history_requested_page, PaginationState.normalize_page(params["history_page"]))
      |> assign(
        :history_requested_page_size,
        PaginationState.normalize_page_size(
          params["history_page_size"],
          @history_page_size_options
        )
      )
      |> assign(:history_page_size_options, @history_page_size_options)
      |> assign(:history_total_count, 0)
      |> assign(:history_changed?, false)
      |> assign(:history_retry_request, nil)
      |> assign(:history_route_params, history_route_params(params))
      |> assign(:history_route_open_requested?, history_route_explicit?(params))
      |> assign(:history_refresh_pending?, false)
      |> assign(:history_error, nil)
      |> assign(:history_pending, nil)
      |> assign(:history_delete_rollback, nil)
      |> assign(:run_result, nil)
      |> assign(:run_error, nil)
      |> assign(:run_ref, nil)
      |> assign(:diagnostic_ref, nil)
      |> assign(:run_request_payload, nil)
      |> assign(:conversation_trace_id, normalize_trace_id(params["trace_id"]))
      |> assign(:conversation_trace_ref, nil)
      |> assign(:draft_error, nil)
      |> assign(:state_save_in_flight, nil)
      |> assign(:state_save_pending, nil)
      |> assign(:state_save_sequence, 0)
      |> assign(:output_request_open?, false)
      |> assign(:output_response_open?, false)
      |> assign(:output_trace_resources, %{})
      |> assign(:output_trace_data, nil)
      |> assign(:output_trace_open?, false)
      |> assign(:output_trace_loading?, false)
      |> assign(:output_trace_error, nil)
      |> assign(:output_trace_ref, nil)
      |> assign(:schema_check, %{status: :idle, message: ""})
      |> assign(:profile_provider_options, %{})
      |> assign(:profile_requires_save?, false)
      |> assign(:reasoning_by_profile, %{})
      |> assign(:ui, @default_ui)
      |> assign(:form, to_form(stringify_form(@default_state), as: :run))
      |> allow_upload(:profile_bundle,
        accept: ~w(.json application/json),
        max_entries: 1,
        max_file_size: Application.get_env(:harden_llm, :max_bundle_bytes, 2_097_152)
      )

    if connected?(socket) do
      handle = socket.assigns.session_handle

      {:ok, start_async(socket, :hydrate, Observability.propagate(fn -> hydrate(handle) end))}
    else
      {:ok, socket}
    end
  end

  @impl true
  def handle_params(params, _uri, socket) do
    pagination = history_route_pagination(params)

    socket =
      socket
      |> sync_history_route(params, pagination)
      |> sync_conversation_route(params)
      |> maybe_start_conversation_load()
      |> maybe_start_history_load()

    {:noreply, socket}
  end

  @impl true
  def handle_info(
        {:profile_widget, @profile_widget_prefix, {:profile_widget_ui, name, open}},
        socket
      )
      when name in @ui_keys do
    toggle_ui(socket, name, to_string(open))
  end

  def handle_info(
        {:profile_widget, @profile_widget_prefix, {:profile_widget_selection, selection}},
        socket
      )
      when is_map(selection) do
    apply_profile_selection(socket, selection)
  end

  def handle_info(
        {:profile_widget, @profile_widget_prefix, {:profile_widget_control, key, value}},
        socket
      )
      when key in ["reasoningEffort", "cacheMode", "modelId", "webSearch"] do
    update_workspace_form(socket, key, value)
  end

  def handle_info(
        {:profile_widget, @profile_widget_prefix, {:profile_widget_provider_options, options}},
        socket
      )
      when is_map(options) do
    {:noreply, assign(socket, :profile_provider_options, options)}
  end

  def handle_info(
        {:profile_widget, @profile_widget_prefix,
         {:profile_widget_profile_dirty, requires_save?}},
        socket
      ) do
    {:noreply, assign(socket, :profile_requires_save?, requires_save?)}
  end

  def handle_info(
        {:profile_widget, @profile_widget_prefix, {:profile_widget_recovery, policy}},
        socket
      ) do
    current = get_in(socket.assigns.form.params || %{}, ["recoveryPolicy"]) || %{}
    policy = ProfileWidgetState.merge_recovery_policy(current, policy)

    {:noreply,
     update_workspace_snapshot(socket, %{"recoveryPolicy" => policy}, clear_recovery_errors: true)}
  end

  def handle_info(
        {:profile_widget, @profile_widget_prefix, {:profile_widget_catalog, profiles}},
        socket
      )
      when is_list(profiles),
      do: {:noreply, assign(socket, :profiles, profiles)}

  @impl true
  def handle_async(
        {:save_state, reference, sequence},
        {:ok, {:error, %APIError{status: 401}}},
        %{assigns: %{state_save_in_flight: %{reference: reference, sequence: sequence}}} = socket
      ) do
    {:noreply,
     socket
     |> clear_state_save()
     |> assign(:state_save_pending, nil)
     |> assign(:history_pending, nil)
     |> Auth.expire_live()}
  end

  def handle_async(_operation, {:ok, {:error, %APIError{status: 401}}}, socket) do
    {:noreply, Auth.expire_live(socket)}
  end

  def handle_async(:hydrate, {:ok, {:ok, hydration}}, socket) do
    state =
      @default_state
      |> Map.merge(hydration.state || %{})
      |> Map.put_new("recoveryPolicy", hydration.recovery_policy_default)
      |> Map.update("cacheMode", "cache", &normalize_cache_mode/1)
      |> Map.update("webSearch", false, &truthy?/1)
      |> Map.put("ui", normalize_ui((hydration.state || %{})["ui"]))
      |> normalize_response_state()

    state =
      if socket.assigns.history_route_open_requested?,
        do: Map.put(state, "ui", Map.put(state["ui"], "historyOpen", true)),
        else: state

    reasoning_by_profile = state["reasoningByProfile"] || %{}

    selected_profile_id =
      ProfileWidgetState.resolve_selected_profile_id(
        hydration.profiles,
        state["selectedProfileId"]
      )

    model_id =
      ProfileWidgetState.resolve_selected_model_id(
        hydration.profiles,
        selected_profile_id,
        state["modelId"]
      )

    state =
      Map.put(
        state,
        "reasoningEffort",
        reasoning_by_profile[selected_profile_id] || state["reasoningEffort"] || "lowest"
      )
      |> Map.put("selectedProfileId", selected_profile_id)
      |> Map.put("modelId", model_id)

    socket =
      socket
      |> assign(:loading?, false)
      |> assign(:backend_state, :ready)
      |> assign(:profiles, hydration.profiles)
      |> assign(:recovery_policy_default, hydration.recovery_policy_default)
      |> assign(:history, [])
      |> assign(:history_loaded?, false)
      |> assign(:history_loading?, false)
      |> assign(:history_load_operation, nil)
      |> assign(:history_refresh_pending?, false)
      |> assign(:history_error, nil)
      |> assign(:history_delete_rollback, nil)
      |> assign(:history_total_count, 0)
      |> assign(:history_changed?, false)
      |> assign(:history_retry_request, nil)
      |> assign(:ui, state["ui"])
      |> assign(:history_route_open_requested?, false)
      |> assign(:reasoning_by_profile, reasoning_by_profile)
      |> assign(:form, to_form(stringify_form(state), as: :run))
      |> assign(:schema_check, schema_check_for_state(state))
      |> maybe_start_conversation_load()

    {:noreply, maybe_start_history_load(socket)}
  end

  def handle_async(:hydrate, _result, socket) do
    {:noreply,
     socket
     |> assign(:loading?, false)
     |> assign(:backend_state, :unavailable)}
  end

  def handle_async(
        {:save_state, reference, sequence},
        result,
        %{assigns: %{state_save_in_flight: %{reference: reference, sequence: sequence}}} = socket
      ) do
    error = state_save_error(result)
    in_flight = socket.assigns.state_save_in_flight
    pending = socket.assigns.state_save_pending

    socket =
      socket
      |> clear_state_save()
      |> assign(:draft_error, error)
      |> finish_restore_save(in_flight, pending, error)

    if is_map(pending) do
      {:noreply, start_state_save(socket, pending)}
    else
      {:noreply, socket}
    end
  end

  def handle_async({:save_state, _reference, _sequence}, _result, socket),
    do: {:noreply, socket}

  def handle_async(
        {:load_history, reference, requested_page, requested_page_size},
        {:ok, {:ok, %{"items" => items, "pagination" => pagination}, _state}},
        %{assigns: %{history_load_ref: reference}} = socket
      ) do
    history = suppress_pending_history_delete(items, socket.assigns.history_delete_rollback)
    effective_page = pagination["page"]
    effective_page_size = pagination["pageSize"]

    socket =
      socket
      |> assign(:history, history)
      |> assign(:history_page, effective_page)
      |> assign(:history_page_size, effective_page_size)
      |> assign(:history_requested_page, effective_page)
      |> assign(:history_requested_page_size, effective_page_size)
      |> assign(:history_total_count, pagination["totalCount"])
      |> assign(:history_load_ref, nil)
      |> assign(:history_load_operation, nil)
      |> assign(:history_loaded?, true)
      |> update(
        :history_result_states,
        &Map.take(&1, Enum.map(history, fn item -> item["runId"] end))
      )
      |> assign(:history_loading?, false)
      |> assign(:history_error, nil)
      |> assign(:history_retry_request, nil)
      |> assign(:history_changed?, false)
      |> maybe_continue_history_refresh()

    if requested_page != effective_page or requested_page_size != effective_page_size do
      {:noreply, push_history_url(socket, effective_page, effective_page_size)}
    else
      {:noreply, socket}
    end
  end

  def handle_async(
        {:load_history, reference, requested_page, requested_page_size},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{history_load_ref: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:history_load_ref, nil)
     |> assign(:history_load_operation, nil)
     |> assign(:history_loading?, false)
     |> assign(:history_error, error.message)
     |> assign(:history_retry_request, %{page: requested_page, page_size: requested_page_size})
     |> maybe_continue_history_refresh()}
  end

  def handle_async(
        {:load_history, reference, requested_page, requested_page_size},
        _result,
        %{assigns: %{history_load_ref: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:history_load_ref, nil)
     |> assign(:history_load_operation, nil)
     |> assign(:history_loading?, false)
     |> assign(:history_error, "History is temporarily unavailable.")
     |> assign(:history_retry_request, %{page: requested_page, page_size: requested_page_size})
     |> maybe_continue_history_refresh()}
  end

  def handle_async({:load_history, _reference, _page, _page_size}, _result, socket),
    do: {:noreply, socket}

  def handle_async(
        {:run, reference},
        {:ok, {:ok, result, _state}},
        %{assigns: %{run_ref: reference}} = socket
      ) do
    socket =
      socket
      |> assign(:run_ref, nil)
      |> assign(:run_result, result)
      |> assign(:diagnostic_ref, nil)
      |> assign(:conversation_trace_ref, nil)
      |> assign(
        :output_trace_resources,
        output_trace_resources(result, socket.assigns.run_request_payload)
      )
      |> assign(:run_request_payload, nil)
      |> assign(:run_error, nil)
      |> assign(:output_request_open?, false)
      |> assign(:output_response_open?, false)
      |> reset_output_trace()
      |> maybe_refresh_history()

    {:noreply, push_conversation_url(socket, LlmTraceProjection.trace_id(result))}
  end

  def handle_async(
        {:run, reference},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{run_ref: reference}} = socket
      ) do
    message =
      if error.ambiguous? do
        "The run outcome is unknown. Refresh History before deciding whether to run again."
      else
        error.message
      end

    socket =
      socket
      |> assign(:run_ref, nil)
      |> assign(:run_result, nil)
      |> assign(:diagnostic_ref, nil)
      |> assign(:conversation_trace_ref, nil)
      |> assign(:run_request_payload, nil)
      |> assign(:output_trace_resources, %{})
      |> assign(:run_error, message)
      |> assign(:recovery_field_errors, error.field_errors)
      |> reset_output_trace()
      |> maybe_refresh_history()
      |> maybe_load_run_diagnostics(error.trace_id)

    {:noreply, socket}
  end

  def handle_async(
        {:run, reference},
        _result,
        %{assigns: %{run_ref: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:run_ref, nil)
     |> assign(:run_result, nil)
     |> assign(:diagnostic_ref, nil)
     |> assign(:conversation_trace_ref, nil)
     |> assign(:run_request_payload, nil)
     |> assign(:output_trace_resources, %{})
     |> assign(:run_error, "The run could not be completed. Try again or check History.")
     |> reset_output_trace()
     |> maybe_refresh_history()}
  end

  def handle_async({:run, _stale_reference}, _result, socket), do: {:noreply, socket}

  def handle_async(
        {:load_run_diagnostics, reference, trace_id},
        {:ok, {:ok, trace, _state}},
        %{assigns: %{diagnostic_ref: reference}} = socket
      ) do
    case LlmTraceProjection.run_result(trace) do
      {:ok, result} ->
        socket =
          socket
          |> assign(:diagnostic_ref, nil)
          |> assign(:run_result, result)
          |> assign(:output_trace_resources, trace_output_resources(trace, result))
          |> assign(:output_trace_data, trace)
          |> assign(:output_trace_open?, false)
          |> assign(:output_trace_loading?, false)
          |> assign(:output_trace_error, nil)
          |> assign(:output_trace_ref, nil)
          |> assign(:output_request_open?, false)
          |> assign(:output_response_open?, false)

        {:noreply, push_conversation_url(socket, trace_id)}

      :error ->
        {:noreply, assign(socket, :diagnostic_ref, nil)}
    end
  end

  def handle_async(
        {:load_run_diagnostics, reference, _trace_id},
        _result,
        %{assigns: %{diagnostic_ref: reference}} = socket
      ) do
    {:noreply, assign(socket, :diagnostic_ref, nil)}
  end

  def handle_async({:load_run_diagnostics, _reference, _trace_id}, _result, socket),
    do: {:noreply, socket}

  def handle_async(
        {:load_conversation, reference, trace_id},
        {:ok, {:ok, trace, _state}},
        %{assigns: %{conversation_trace_ref: reference, conversation_trace_id: trace_id}} = socket
      ) do
    case LlmTraceProjection.run_result(trace) do
      {:ok, result} ->
        {:noreply,
         socket
         |> assign(:conversation_trace_ref, nil)
         |> assign(:run_result, result)
         |> assign(:output_trace_resources, trace_output_resources(trace, result))
         |> assign(:output_trace_data, trace)
         |> assign(:output_trace_open?, false)
         |> assign(:output_trace_loading?, false)
         |> assign(:output_trace_error, nil)
         |> assign(:output_trace_ref, nil)
         |> assign(:run_request_payload, nil)
         |> assign(:run_error, nil)
         |> assign(:output_request_open?, false)
         |> assign(:output_response_open?, false)}

      :error ->
        {:noreply,
         socket
         |> assign(:conversation_trace_ref, nil)
         |> assign(:run_result, nil)
         |> assign(:output_trace_resources, %{})
         |> reset_output_trace()
         |> assign(:run_error, "This conversation could not be restored.")}
    end
  end

  def handle_async(
        {:load_conversation, reference, trace_id},
        {:ok, {:error, %APIError{}}},
        %{assigns: %{conversation_trace_ref: reference, conversation_trace_id: trace_id}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:conversation_trace_ref, nil)
     |> assign(:run_result, nil)
     |> assign(:output_trace_resources, %{})
     |> assign(:run_error, "This conversation could not be restored.")}
  end

  def handle_async({:load_conversation, _reference, _trace_id}, _result, socket),
    do: {:noreply, socket}

  def handle_async(
        {:load_output_trace, reference, trace_id},
        {:ok, {:ok, trace, _state}},
        %{assigns: %{output_trace_ref: reference, run_result: result}} = socket
      ) do
    if LlmTraceProjection.trace_id(result) == trace_id do
      socket =
        socket
        |> assign(:output_trace_ref, nil)
        |> assign(:output_trace_loading?, false)
        |> assign(:output_trace_error, nil)
        |> assign(:output_trace_data, trace)

      socket =
        case LlmTraceProjection.run_result(trace) do
          {:ok, _trace_result} ->
            assign(socket, :output_trace_resources, trace_output_resources(trace, result))

          :error ->
            socket
        end

      {:noreply, socket}
    else
      {:noreply,
       socket
       |> assign(:output_trace_ref, nil)
       |> assign(:output_trace_loading?, false)}
    end
  end

  def handle_async(
        {:load_output_trace, reference, _trace_id},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{output_trace_ref: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:output_trace_ref, nil)
     |> assign(:output_trace_loading?, false)
     |> assign(:output_trace_error, error.message)}
  end

  def handle_async(
        {:load_output_trace, reference, _trace_id},
        _result,
        %{assigns: %{output_trace_ref: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:output_trace_ref, nil)
     |> assign(:output_trace_loading?, false)
     |> assign(:output_trace_error, "Trace JSON is temporarily unavailable.")}
  end

  def handle_async({:load_output_trace, _reference, _trace_id}, _result, socket),
    do: {:noreply, socket}

  def handle_async({:history_result_trace, run_id, reference}, result, socket) do
    state = socket.assigns.history_result_states[run_id]

    if state && state.load_ref == reference &&
         Enum.any?(socket.assigns.history, &(&1["runId"] == run_id)) do
      state = %{state | load_ref: nil, trace_loading?: false}

      state =
        case result do
          {:ok, {:ok, trace, _}} -> %{state | trace_data: trace, trace_error: nil}
          {:ok, {:error, %APIError{} = error}} -> %{state | trace_error: error.message}
          _ -> %{state | trace_error: "Trace details are temporarily unavailable."}
        end

      {:noreply, put_history_result_state(socket, run_id, state)}
    else
      {:noreply, socket}
    end
  end

  def handle_async(
        {:delete_history, reference, run_id},
        {:ok, {:ok, _result, _state}},
        %{assigns: %{history_pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:history_pending, nil)
     |> assign(:history_delete_rollback, nil)
     |> assign(:history, Enum.reject(socket.assigns.history, &(&1["runId"] == run_id)))
     |> assign(:history_error, nil)
     |> maybe_refresh_history()}
  end

  def handle_async(
        {:delete_history, reference, _run_id},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{history_pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:history_pending, nil)
     |> rollback_history_delete()
     |> assign(:history_error, error.message)}
  end

  def handle_async(
        {:delete_history, reference, _run_id},
        _result,
        %{assigns: %{history_pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:history_pending, nil)
     |> rollback_history_delete()
     |> assign(:history_error, "The history item could not be deleted.")}
  end

  def handle_async(
        {:clear_history, reference},
        {:ok, {:ok, _result, _state}},
        %{assigns: %{history_pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:history_pending, nil)
     |> assign(:history_delete_rollback, nil)
     |> assign(:history, [])
     |> assign(:history_result_states, %{})
     |> assign(:history_load_ref, nil)
     |> assign(:history_load_operation, nil)
     |> assign(:history_loading?, false)
     |> assign(:history_loaded?, true)
     |> assign(:history_page, 1)
     |> assign(:history_requested_page, 1)
     |> assign(:history_total_count, 0)
     |> assign(:history_retry_request, nil)
     |> assign(:history_changed?, false)
     |> assign(:history_refresh_pending?, false)
     |> assign(:history_error, nil)
     |> then(&push_history_url(&1, 1, &1.assigns.history_page_size))}
  end

  def handle_async(
        {:clear_history, reference},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{history_pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:history_pending, nil)
     |> assign(:history_error, error.message)}
  end

  def handle_async(
        {:clear_history, reference},
        _result,
        %{assigns: %{history_pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:history_pending, nil)
     |> assign(:history_error, "History could not be cleared.")}
  end

  @impl true
  def handle_event("save-draft", %{"_target" => [scope | _]}, socket)
      when scope == "profile" do
    {:noreply, socket}
  end

  def handle_event("save-draft", event_params, socket) do
    params = event_form_params(event_params, socket)
    state = workspace_state(params, socket.assigns.ui, socket.assigns.reasoning_by_profile)

    {:noreply,
     socket
     |> assign(:form, to_form(params, as: :run))
     |> assign(:run_error, nil)
     |> assign(:reasoning_by_profile, state["reasoningByProfile"])
     |> assign(:schema_check, schema_check_from_params(params))
     |> queue_state_save(state)}
  end

  def handle_event("validate-bundle", _params, socket), do: {:noreply, socket}

  def handle_event("import-bundle", _params, socket) do
    results =
      consume_uploaded_entries(socket, :profile_bundle, fn %{path: path}, _entry ->
        case File.read(path) do
          {:ok, bytes} -> {:ok, bytes}
          {:error, _reason} -> {:postpone, :read_failed}
        end
      end)

    with [bytes] <- results,
         {:ok, bundle} <- Jason.decode(bytes),
         true <- is_map(bundle),
         {:ok, %{"profiles" => profiles}, _state} <-
           HardenAPI.import_profile_bundle(socket.assigns.session_handle, bundle) do
      {:noreply,
       socket
       |> assign(:profiles, profiles)
       |> put_flash(:info, "Profile bundle imported atomically.")}
    else
      {:error, %APIError{status: 401}} -> {:noreply, Auth.expire_live(socket)}
      _ -> {:noreply, assign(socket, :draft_error, "The selected bundle was rejected.")}
    end
  end

  def handle_event("toggle-ui", %{"name" => name, "open" => value}, socket)
      when name in @ui_keys do
    toggle_ui(socket, name, value)
  end

  def handle_event("restore-history", %{"run-id" => run_id}, socket) do
    with item when is_map(item) <- Enum.find(socket.assigns.history, &(&1["runId"] == run_id)),
         request when is_map(request) <- item["request"],
         true <- is_map(request["recoveryPolicy"]) do
      state = restore_state(request, item, socket.assigns.profiles, socket.assigns.ui)
      params = stringify_form(state)

      socket =
        socket
        |> assign(:form, to_form(params, as: :run))
        |> assign(:reasoning_by_profile, state["reasoningByProfile"] || %{})
        |> assign(:schema_check, schema_check_for_state(state))
        |> queue_state_save(state, :restore)

      {:noreply, assign(socket, :history_pending, socket.assigns.state_save_sequence)}
    else
      _ ->
        {:noreply,
         assign(
           socket,
           :history_error,
           "The recorded request uses an unsupported format and cannot be restored."
         )}
    end
  end

  def handle_event("restore-history", _params, socket), do: {:noreply, socket}

  def handle_event(
        "paginate-history",
        %{"pagination-id" => "workspace-history-pagination"} = params,
        socket
      ) do
    cond do
      not socket.assigns.ui["historyOpen"] or not is_nil(socket.assigns.history_pending) ->
        {:noreply, socket}

      true ->
        current = %{
          page: socket.assigns.history_requested_page,
          page_size: socket.assigns.history_requested_page_size
        }

        intent =
          if Map.has_key?(params, "page-size"),
            do: {:page_size, params["page-size"]},
            else: {:page, params["page"]}

        case PaginationState.transition(
               current,
               intent,
               page_size_options: @history_page_size_options
             ) do
          {:ok, %{page: page, page_size: page_size}} when page <= @maximum_history_page ->
            if not is_nil(socket.assigns.history_error) and
                 page == socket.assigns.history_requested_page and
                 page_size == socket.assigns.history_requested_page_size and
                 not socket.assigns.history_loading? do
              {:noreply, start_history_load(socket, page, page_size)}
            else
              {:noreply, push_history_url(socket, page, page_size)}
            end

          :unchanged ->
            if not is_nil(socket.assigns.history_error) and not socket.assigns.history_loading? do
              {:noreply,
               start_history_load(
                 socket,
                 socket.assigns.history_requested_page,
                 socket.assigns.history_requested_page_size
               )}
            else
              {:noreply, socket}
            end

          _ ->
            {:noreply, assign(socket, :history_error, "Enter a valid positive page number.")}
        end
    end
  end

  def handle_event("paginate-history", _params, socket), do: {:noreply, socket}

  def handle_event("refresh-history", _params, socket),
    do: {:noreply, refresh_history(socket)}

  def handle_event(
        "delete-history",
        %{"run-id" => run_id},
        %{assigns: %{history_pending: nil}} = socket
      ) do
    case Enum.find_index(socket.assigns.history, &(&1["runId"] == run_id)) do
      nil ->
        {:noreply, assign(socket, :history_error, "This history item is no longer available.")}

      index ->
        reference = System.unique_integer([:positive, :monotonic])
        handle = socket.assigns.session_handle
        item = Enum.at(socket.assigns.history, index)

        {:noreply,
         socket
         |> cancel_history_load()
         |> assign(:history_pending, reference)
         |> assign(:history_load_ref, nil)
         |> assign(:history_delete_rollback, %{item: item, index: index, run_id: run_id})
         |> assign(:history, List.delete_at(socket.assigns.history, index))
         |> update(:history_result_states, &Map.delete(&1, run_id))
         |> assign(:history_error, nil)
         |> start_async(
           {:delete_history, reference, run_id},
           Observability.propagate(fn -> HardenAPI.delete_history(handle, run_id) end)
         )}
    end
  end

  def handle_event("delete-history", _params, socket), do: {:noreply, socket}

  def handle_event("clear-history", _params, %{assigns: %{history_pending: nil}} = socket) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    {:noreply,
     socket
     |> cancel_history_load()
     |> assign(:history_pending, reference)
     |> assign(:history_load_ref, nil)
     |> start_async(
       {:clear_history, reference},
       Observability.propagate(fn -> HardenAPI.clear_history(handle) end)
     )}
  end

  def handle_event("clear-history", _params, socket), do: {:noreply, socket}

  def handle_event("generate-schema", event_params, socket) do
    params = event_form_params(event_params, socket)
    shorthand = params["schemaShorthand"] || ""

    case WorkspaceSchema.generate_schema(shorthand) do
      {:ok, schema, message} ->
        next_params =
          params
          |> Map.put("callType", "structured")
          |> Map.put("schema", Jason.encode!(schema, pretty: true))

        {:noreply,
         socket
         |> assign(:form, to_form(next_params, as: :run))
         |> assign(:schema_check, %{status: :valid, message: message})
         |> persist_form_state(next_params)}

      {:error, message} ->
        {:noreply,
         socket
         |> assign(:form, to_form(params, as: :run))
         |> assign(:schema_check, %{status: :error, message: message})}
    end
  end

  def handle_event("check-schema", event_params, socket) do
    params = event_form_params(event_params, socket)

    {:noreply,
     socket
     |> assign(:form, to_form(params, as: :run))
     |> assign(:schema_check, schema_check_from_params(params))}
  end

  def handle_event("clear-schema", event_params, socket) do
    params = event_form_params(event_params, socket)

    next_params =
      params
      |> Map.put("callType", "text")
      |> Map.put("schemaShorthand", "")
      |> Map.put("schema", "")

    {:noreply,
     socket
     |> assign(:form, to_form(next_params, as: :run))
     |> assign(:schema_check, %{status: :idle, message: ""})
     |> persist_form_state(next_params)}
  end

  def handle_event("new-conversation", event_params, socket) do
    params = event_form_params(event_params, socket)

    next_params =
      params
      |> Map.put("systemPrompt", "")
      |> Map.put("userPrompt", "")
      |> Map.put("callType", "text")
      |> Map.put("schemaShorthand", "")
      |> Map.put("schema", "")

    socket =
      socket
      |> clear_conversation_selection()
      |> assign(:form, to_form(next_params, as: :run))
      |> assign(:schema_check, %{status: :idle, message: ""})
      |> persist_form_state(next_params)

    {:noreply, push_patch(socket, to: ~p"/")}
  end

  def handle_event("new-prompt", event_params, socket) do
    params = event_form_params(event_params, socket)
    next_params = Map.put(params, "userPrompt", "")

    {:noreply,
     socket
     |> assign(:form, to_form(next_params, as: :run))
     |> assign(:run_error, nil)
     |> persist_form_state(next_params)}
  end

  def handle_event("clear-system-prompt", event_params, socket) do
    params = event_form_params(event_params, socket)
    next_params = Map.put(params, "systemPrompt", "")

    {:noreply,
     socket
     |> assign(:form, to_form(next_params, as: :run))
     |> assign(:run_error, nil)
     |> persist_form_state(next_params)}
  end

  def handle_event("run", %{"run" => params}, %{assigns: %{run_ref: nil}} = socket) do
    params = event_form_params(%{"run" => params}, socket)

    cond do
      socket.assigns.profile_requires_save? ->
        {:noreply,
         assign(
           socket,
           :run_error,
           "Save the LLM profile before running endpoint, credential, or identity changes."
         )}

      true ->
        case run_payload(params, socket.assigns.profiles, socket.assigns.profile_provider_options) do
          {:ok, payload} ->
            {:noreply, socket |> assign(:form, to_form(params, as: :run)) |> start_run(payload)}

          {:error, message} ->
            {:noreply, assign(socket, :run_error, message)}
        end
    end
  end

  def handle_event("run", _params, socket), do: {:noreply, socket}

  def handle_event(
        "rerun-result",
        %{"run-id" => run_id},
        %{assigns: %{run_ref: nil, profile_requires_save?: false}} = socket
      ) do
    resources =
      cond do
        is_map(socket.assigns.run_result) and socket.assigns.run_result["runId"] == run_id ->
          socket.assigns.output_trace_resources

        item = Enum.find(socket.assigns.history, &(&1["runId"] == run_id)) ->
          output_trace_resources(history_result(item), item["request"])

        true ->
          %{}
      end

    if rerun_available?(resources) do
      {:noreply, start_run(socket, get_in(resources, ["request", "payload"]))}
    else
      {:noreply,
       assign(
         socket,
         :run_error,
         "The recorded request is unavailable or uses an unsupported format; this result cannot be rerun."
       )}
    end
  end

  def handle_event("rerun-result", _params, socket), do: {:noreply, socket}

  def handle_event("toggle-history-result", %{"run-id" => run_id} = params, socket) do
    kind = params["kind"] || params["name"]
    item = Enum.find(socket.assigns.history, &(&1["runId"] == run_id))

    if item && kind in ~w(controls overview request response trace) do
      state = Map.get(socket.assigns.history_result_states, run_id, history_result_state())

      state =
        case kind do
          "controls" -> %{state | controls_open: not state.controls_open, details_open: true}
          "overview" -> %{state | details_open: not state.details_open}
          "request" -> %{state | request_open: not state.request_open}
          "response" -> %{state | response_open: not state.response_open}
          "trace" -> %{state | trace_open: not state.trace_open}
        end

      if kind == "trace" && state.trace_open && is_nil(state.trace_data) && is_nil(state.load_ref) do
        reference = System.unique_integer([:positive, :monotonic])
        handle = socket.assigns.session_handle
        trace_id = item["traceId"]
        state = %{state | trace_loading?: true, trace_error: nil, load_ref: reference}

        {:noreply,
         socket
         |> put_history_result_state(run_id, state)
         |> start_async(
           {:history_result_trace, run_id, reference},
           Observability.propagate(fn -> HardenAPI.get_trace(handle, trace_id) end)
         )}
      else
        {:noreply, put_history_result_state(socket, run_id, state)}
      end
    else
      {:noreply, socket}
    end
  end

  def handle_event("toggle-history-result", _params, socket), do: {:noreply, socket}

  def handle_event("toggle-output-data", %{"kind" => "request"}, socket) do
    {:noreply, update(socket, :output_request_open?, &(!&1))}
  end

  def handle_event("toggle-output-data", %{"kind" => "response"}, socket) do
    {:noreply, update(socket, :output_response_open?, &(!&1))}
  end

  def handle_event("toggle-output-data", %{"kind" => "trace"}, socket) do
    {:noreply, toggle_output_trace(socket)}
  end

  defp toggle_output_trace(%{assigns: %{output_trace_open?: true}} = socket),
    do: assign(socket, :output_trace_open?, false)

  defp toggle_output_trace(%{assigns: %{output_trace_ref: reference}} = socket)
       when not is_nil(reference),
       do: assign(socket, :output_trace_open?, true)

  defp toggle_output_trace(%{assigns: %{output_trace_data: trace}} = socket)
       when is_map(trace),
       do: assign(socket, :output_trace_open?, true)

  defp toggle_output_trace(socket) do
    case LlmTraceProjection.trace_id(socket.assigns.run_result) do
      trace_id when is_binary(trace_id) and trace_id != "" ->
        reference = System.unique_integer([:positive, :monotonic])
        handle = socket.assigns.session_handle

        socket
        |> assign(:output_trace_ref, reference)
        |> assign(:output_trace_open?, true)
        |> assign(:output_trace_loading?, true)
        |> assign(:output_trace_error, nil)
        |> start_async(
          {:load_output_trace, reference, trace_id},
          Observability.propagate(fn -> HardenAPI.get_trace(handle, trace_id) end)
        )

      _ ->
        socket
        |> assign(:output_trace_open?, true)
        |> assign(:output_trace_error, "Trace JSON is not available for this run.")
    end
  end

  defp toggle_ui(socket, name, value) do
    ui = Map.put(socket.assigns.ui, name, truthy?(value))

    # Expanding the output always reveals Overview, even if it was closed before.
    ui =
      if name == "outputControlsOpen" and truthy?(value),
        do: Map.put(ui, "outputDetailsOpen", true),
        else: ui

    socket =
      socket
      |> assign(:ui, ui)
      |> assign(:draft_error, nil)
      |> maybe_clear_history_route_open_request(name, value)
      |> queue_state_save()

    {:noreply,
     if(name == "historyOpen" and truthy?(value),
       do: maybe_start_history_load(socket),
       else: socket
     )}
  end

  defp reset_output_trace(socket) do
    socket
    |> assign(:output_trace_data, nil)
    |> assign(:output_trace_open?, false)
    |> assign(:output_trace_loading?, false)
    |> assign(:output_trace_error, nil)
    |> assign(:output_trace_ref, nil)
  end

  def status_label(:loading), do: "Checking backend"
  def status_label(:ready), do: "Backend ready"
  def status_label(:unavailable), do: "Backend unavailable"

  def profile_options(profiles) do
    Enum.map(profiles, fn profile_state ->
      profile = profile_state["profile"] || %{}
      {profile["llmProfile"] || "Unnamed", profile["llmProfile"] || ""}
    end)
  end

  def selected_profile(profiles, profile_id) do
    Enum.find(profiles, fn profile_state ->
      get_in(profile_state, ["profile", "llmProfile"]) == profile_id
    end)
  end

  def profile_known?(profiles, profile_id), do: not is_nil(selected_profile(profiles, profile_id))

  def selected_profile_models(profiles, profile_id) do
    profile_state =
      Enum.find(profiles, fn profile_state ->
        get_in(profile_state, ["profile", "llmProfile"]) == profile_id
      end)

    case profile_state do
      nil -> []
      profile_state -> get_in(profile_state, ["profile", "models"]) || []
    end
  end

  def reasoning_options(profiles, profile_id) do
    profile_id = String.trim(to_string(profile_id || ""))

    cond do
      profile_id == "" ->
        @reasoning_options

      true ->
        case selected_profile(profiles, profile_id) do
          %{} = profile_state ->
            case get_in(profile_state, ["profile", "reasoningEffortMap"]) do
              map when is_map(map) ->
                Enum.filter(@reasoning_options, fn {_label, value} ->
                  Map.has_key?(map, value)
                end)

              _ ->
                []
            end

          _ ->
            []
        end
    end
  end

  def schema_status_text(%{status: :valid, message: message})
      when is_binary(message) and message != "", do: message

  def schema_status_text(%{status: :valid}), do: "Schema valid."
  def schema_status_text(%{status: :error, message: message}), do: message
  def schema_status_text(%{status: :pending}), do: "Schema check pending."
  def schema_status_text(_), do: ""

  def schema_status_class(%{status: :error}), do: "text-rose-700"
  def schema_status_class(%{status: :valid}), do: "text-emerald-700"
  def schema_status_class(_), do: "text-slate-500"

  defp hydrate(handle) do
    with {:ok, _result, state} <- HardenAPI.get_state(handle),
         {:ok, %{"profiles" => profiles, "defaults" => %{"recoveryPolicy" => policy}}, _} <-
           HardenAPI.list_profiles(handle),
         true <- is_list(profiles) do
      {:ok, %{state: state, profiles: profiles, recovery_policy_default: policy}}
    end
  end

  defp history_route_pagination(params) do
    %{
      page: normalize_history_page(params["history_page"]),
      page_size:
        PaginationState.normalize_page_size(
          params["history_page_size"],
          @history_page_size_options
        )
    }
  end

  defp history_route_explicit?(params) do
    Map.has_key?(params, "history_page") or Map.has_key?(params, "history_page_size")
  end

  defp normalize_history_page(value) do
    case PaginationState.positive_integer(value) do
      {:ok, page} when page <= @maximum_history_page -> page
      _ -> PaginationState.default_page()
    end
  end

  defp history_route_params(params) do
    params
    |> Enum.reduce(%{}, fn
      {"trace_id", value}, route when is_binary(value) and value != "" ->
        Map.put(route, "trace_id", value)

      {"history_page", value}, route ->
        case PaginationState.positive_integer(value) do
          {:ok, page} when page <= @maximum_history_page ->
            Map.put(route, "history_page", Integer.to_string(page))

          _ ->
            route
        end

      {"history_page_size", value}, route ->
        page_size =
          PaginationState.normalize_page_size(
            value,
            @history_page_size_options,
            nil
          )

        if is_integer(page_size),
          do: Map.put(route, "history_page_size", Integer.to_string(page_size)),
          else: route

      _entry, route ->
        route
    end)
  end

  defp sync_history_route(socket, params, %{page: page, page_size: page_size}) do
    changed? =
      page != socket.assigns.history_requested_page or
        page_size != socket.assigns.history_requested_page_size

    open_requested? =
      socket.assigns.history_route_open_requested? or
        (changed? and history_route_explicit?(params))

    socket =
      socket
      |> assign(:history_route_params, history_route_params(params))
      |> assign(:history_requested_page, page)
      |> assign(:history_requested_page_size, page_size)
      |> assign(:history_route_open_requested?, open_requested?)
      |> maybe_open_history_from_route(open_requested?)

    if changed? and connected?(socket) and socket.assigns.backend_state == :ready and
         socket.assigns.ui["historyOpen"] and is_nil(socket.assigns.history_pending) do
      start_history_load(socket, page, page_size)
    else
      socket
    end
  end

  defp sync_conversation_route(socket, params) do
    case normalize_trace_id(params["trace_id"]) do
      nil ->
        clear_conversation_selection(socket)

      trace_id ->
        current_trace_id = LlmTraceProjection.trace_id(socket.assigns.run_result)
        route_changed? = trace_id != socket.assigns.conversation_trace_id

        cond do
          not route_changed? ->
            socket

          trace_id == current_trace_id ->
            socket
            |> assign(:conversation_trace_id, trace_id)
            |> assign(:conversation_trace_ref, nil)
            |> assign(:diagnostic_ref, nil)

          true ->
            socket
            |> assign(:conversation_trace_id, trace_id)
            |> reset_conversation_selection()
        end
    end
  end

  defp maybe_open_history_from_route(socket, true),
    do: update(socket, :ui, &Map.put(&1, "historyOpen", true))

  defp maybe_open_history_from_route(socket, false), do: socket

  defp maybe_clear_history_route_open_request(socket, "historyOpen", value) do
    if truthy?(value), do: socket, else: assign(socket, :history_route_open_requested?, false)
  end

  defp maybe_clear_history_route_open_request(socket, _name, _value), do: socket

  defp push_history_url(socket, page, page_size) do
    params =
      socket.assigns.history_route_params
      |> Map.put("history_page", Integer.to_string(page))
      |> Map.put("history_page_size", Integer.to_string(page_size))
      |> workspace_query_params()

    push_patch(socket, to: workspace_path(params))
  end

  defp workspace_query_params(params),
    do: Map.reject(params, fn {_key, value} -> is_nil(value) end)

  defp workspace_path(params) when map_size(params) == 0, do: ~p"/"
  defp workspace_path(params), do: "/?" <> URI.encode_query(params)

  defp maybe_start_history_load(socket) do
    if socket.assigns.ui["historyOpen"] and
         (not socket.assigns.history_loaded? or socket.assigns.history_refresh_pending?) and
         not socket.assigns.history_loading? do
      start_history_load(
        socket,
        socket.assigns.history_requested_page,
        socket.assigns.history_requested_page_size
      )
    else
      socket
    end
  end

  defp maybe_refresh_history(socket) do
    cond do
      not is_nil(socket.assigns.history_pending) ->
        assign(socket, :history_refresh_pending?, true)

      socket.assigns.history_loading? ->
        assign(socket, :history_refresh_pending?, true)

      is_map(socket.assigns.history_retry_request) ->
        request = socket.assigns.history_retry_request
        start_history_load(socket, request.page, request.page_size)

      socket.assigns.history_page > 1 or socket.assigns.history_requested_page > 1 ->
        assign(socket, :history_changed?, true)

      socket.assigns.ui["historyOpen"] ->
        start_history_load(socket, socket.assigns.history_page, socket.assigns.history_page_size)

      true ->
        assign(socket, :history_refresh_pending?, true)
    end
  end

  defp refresh_history(socket) do
    cond do
      not is_nil(socket.assigns.history_pending) ->
        assign(socket, :history_refresh_pending?, true)

      socket.assigns.history_loading? ->
        assign(socket, :history_refresh_pending?, true)

      is_map(socket.assigns.history_retry_request) ->
        request = socket.assigns.history_retry_request
        start_history_load(socket, request.page, request.page_size)

      socket.assigns.ui["historyOpen"] ->
        start_history_load(
          socket,
          socket.assigns.history_requested_page,
          socket.assigns.history_requested_page_size
        )

      true ->
        assign(socket, :history_refresh_pending?, true)
    end
  end

  defp maybe_continue_history_refresh(socket) do
    if socket.assigns.history_refresh_pending? and socket.assigns.ui["historyOpen"] and
         is_nil(socket.assigns.history_pending) do
      start_history_load(
        socket,
        socket.assigns.history_requested_page,
        socket.assigns.history_requested_page_size
      )
    else
      socket
    end
  end

  defp maybe_load_run_diagnostics(socket, trace_id)
       when is_binary(trace_id) and trace_id != "" do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    socket
    |> assign(:diagnostic_ref, reference)
    |> start_async(
      {:load_run_diagnostics, reference, trace_id},
      Observability.propagate(fn -> HardenAPI.get_trace(handle, trace_id) end)
    )
  end

  defp maybe_load_run_diagnostics(socket, _trace_id), do: socket

  defp maybe_start_conversation_load(socket) do
    trace_id = socket.assigns.conversation_trace_id

    if connected?(socket) and socket.assigns.backend_state == :ready and
         is_binary(trace_id) and trace_id != "" and
         is_nil(socket.assigns.conversation_trace_ref) and
         LlmTraceProjection.trace_id(socket.assigns.run_result) != trace_id do
      reference = System.unique_integer([:positive, :monotonic])
      handle = socket.assigns.session_handle

      socket
      |> assign(:conversation_trace_ref, reference)
      |> start_async(
        {:load_conversation, reference, trace_id},
        Observability.propagate(fn -> HardenAPI.get_trace(handle, trace_id) end)
      )
    else
      socket
    end
  end

  defp clear_conversation_selection(socket) do
    socket
    |> assign(:conversation_trace_id, nil)
    |> reset_conversation_selection()
  end

  defp reset_conversation_selection(socket) do
    socket
    |> assign(:conversation_trace_ref, nil)
    |> assign(:diagnostic_ref, nil)
    |> assign(:run_result, nil)
    |> assign(:run_request_payload, nil)
    |> assign(:output_trace_resources, %{})
    |> assign(:run_error, nil)
    |> assign(:output_request_open?, false)
    |> assign(:output_response_open?, false)
    |> reset_output_trace()
  end

  defp output_trace_resources(result, request) do
    trace_id = LlmTraceProjection.trace_id(result)

    LlmTraceProjection.resources_from_run(
      result,
      request,
      HardenAPI.public_base_url(),
      trace_url(trace_id),
      &artifact_url/2
    )
  end

  defp start_run(socket, payload) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    socket
    |> assign(:run_ref, reference)
    |> assign(:run_request_payload, payload)
    |> assign(:run_result, nil)
    |> assign(:diagnostic_ref, nil)
    |> assign(:conversation_trace_ref, nil)
    |> assign(:output_trace_resources, %{})
    |> assign(:run_error, nil)
    |> assign(:output_request_open?, false)
    |> assign(:output_response_open?, false)
    |> reset_output_trace()
    |> start_async(
      {:run, reference},
      Observability.propagate(fn -> HardenAPI.run(handle, payload) end)
    )
  end

  defp rerun_available?(%{"request" => %{"available" => true, "payload" => request}})
       when is_map(request) do
    is_map(request["recoveryPolicy"]) and
      Enum.all?(~w(profileId userPrompt), fn key ->
        is_binary(request[key]) and String.trim(request[key]) != ""
      end)
  end

  defp rerun_available?(_resources), do: false

  defp history_result_state do
    %{
      controls_open: false,
      details_open: true,
      request_open: false,
      response_open: false,
      trace_open: false,
      trace_loading?: false,
      trace_data: nil,
      trace_error: nil,
      load_ref: nil
    }
  end

  defp put_history_result_state(socket, run_id, state),
    do: update(socket, :history_result_states, &Map.put(&1, run_id, state))

  defp history_result(item) do
    (item["result"] || %{})
    |> Map.put_new("runId", item["runId"])
    |> Map.put_new("traceId", item["traceId"])
    |> Map.put_new("status", item["status"])
  end

  defp history_trace_assigns(item, states, run_ref, profile_requires_save?) do
    state = Map.get(states, item["runId"], history_result_state())
    result = history_result(item)

    resources =
      if is_map(state.trace_data),
        do: trace_output_resources(state.trace_data, result),
        else: output_trace_resources(result, item["request"])

    state
    |> Map.delete(:load_ref)
    |> Map.merge(%{
      id: "history-trace-#{item["runId"]}",
      summary: LlmTraceProjection.summary(result),
      details: LlmTraceProjection.details(result),
      resources: resources,
      run_id: item["runId"],
      controls_event: "toggle-history-result",
      controls_name: "controls",
      details_event: "toggle-history-result",
      details_name: "overview",
      resource_event: "toggle-history-result",
      rerun_event: "rerun-result",
      rerun_disabled:
        not is_nil(run_ref) or profile_requires_save? or not rerun_available?(resources)
    })
  end

  defp trace_output_resources(trace, result) do
    trace_id = LlmTraceProjection.trace_id(result)

    LlmTraceProjection.resources_from_trace(
      trace,
      result,
      HardenAPI.public_base_url(),
      trace_url(trace_id),
      &artifact_url/2
    )
  end

  defp trace_url(trace_id) when is_binary(trace_id) and trace_id != "",
    do: ~p"/traces/#{trace_id}"

  defp trace_url(_trace_id), do: nil

  defp artifact_url(trace_id, artifact_id),
    do: ~p"/traces/#{trace_id}/artifacts/#{artifact_id}"

  defp push_conversation_url(socket, nil), do: socket

  defp push_conversation_url(socket, trace_id) do
    params =
      socket.assigns.history_route_params
      |> Map.put("trace_id", trace_id)
      |> workspace_query_params()

    push_patch(socket, to: workspace_path(params))
  end

  defp start_history_load(socket, page, page_size) do
    handle = socket.assigns.session_handle
    reference = System.unique_integer([:positive, :monotonic])
    operation = {:load_history, reference, page, page_size}

    socket
    |> cancel_history_load()
    |> assign(:history_load_ref, reference)
    |> assign(:history_load_operation, operation)
    |> assign(:history_loading?, true)
    |> assign(:history_refresh_pending?, false)
    |> assign(:history_changed?, false)
    |> assign(:history_error, nil)
    |> assign(:history_retry_request, %{page: page, page_size: page_size})
    |> assign(:history_requested_page, page)
    |> assign(:history_requested_page_size, page_size)
    |> start_async(
      operation,
      Observability.propagate(fn ->
        HardenAPI.list_history(handle, page: page, limit: page_size)
      end)
    )
  end

  defp cancel_history_load(%{assigns: %{history_load_operation: nil}} = socket), do: socket

  defp cancel_history_load(%{assigns: %{history_load_operation: operation}} = socket) do
    socket
    |> cancel_async(operation)
    |> assign(:history_load_operation, nil)
    |> assign(:history_load_ref, nil)
    |> assign(:history_loading?, false)
  end

  defp suppress_pending_history_delete(history, %{run_id: run_id}),
    do: Enum.reject(history, &(&1["runId"] == run_id))

  defp suppress_pending_history_delete(history, _rollback), do: history

  defp rollback_history_delete(socket) do
    case socket.assigns.history_delete_rollback do
      %{item: item, index: index, run_id: run_id} ->
        history =
          if Enum.any?(socket.assigns.history, &(&1["runId"] == run_id)) do
            socket.assigns.history
          else
            List.insert_at(socket.assigns.history, index, item)
          end

        socket
        |> assign(:history, history)
        |> assign(:history_delete_rollback, nil)

      _ ->
        socket
    end
  end

  defp queue_state_save(socket, state \\ nil, context \\ :draft) do
    state =
      state ||
        workspace_state(
          socket.assigns.form.params || %{},
          socket.assigns.ui,
          socket.assigns.reasoning_by_profile
        )

    sequence = socket.assigns.state_save_sequence + 1
    snapshot = %{sequence: sequence, state: state, context: context}

    socket =
      socket
      |> assign(:state_save_sequence, sequence)
      |> assign(:draft_error, nil)
      |> maybe_clear_restore_pending(context)

    case socket.assigns.state_save_in_flight do
      nil ->
        start_state_save(socket, snapshot)

      _in_flight ->
        assign(socket, :state_save_pending, snapshot)
    end
  end

  defp start_state_save(socket, snapshot) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle
    operation = {:save_state, reference, snapshot.sequence}

    socket
    |> assign(:state_save_in_flight, Map.put(snapshot, :reference, reference))
    |> assign(:state_save_pending, nil)
    |> assign(:draft_error, nil)
    |> maybe_clear_restore_error(snapshot.context)
    |> start_async(
      operation,
      Observability.propagate(fn -> HardenAPI.save_state(handle, snapshot.state) end)
    )
  end

  defp clear_state_save(socket), do: assign(socket, :state_save_in_flight, nil)

  defp state_save_error({:ok, {:ok, _result, _state}}), do: nil
  defp state_save_error({:ok, {:error, %APIError{} = error}}), do: error.message
  defp state_save_error(_result), do: "The workspace draft could not be saved."

  defp finish_restore_save(socket, %{context: :restore}, %{context: :restore}, nil) do
    socket
  end

  defp finish_restore_save(socket, %{context: :restore}, %{context: :restore}, _error) do
    socket
    |> assign(:history_pending, nil)
    |> assign(:history_error, "This history item could not be restored.")
  end

  defp finish_restore_save(socket, %{context: :restore}, nil, nil) do
    socket
    |> assign(:history_pending, nil)
    |> assign(:history_error, nil)
  end

  defp finish_restore_save(socket, %{context: :restore}, nil, _error) do
    socket
    |> assign(:history_pending, nil)
    |> assign(:history_error, "This history item could not be restored.")
  end

  defp finish_restore_save(socket, _in_flight, _pending, _error), do: socket

  defp maybe_clear_restore_pending(socket, :restore), do: socket
  defp maybe_clear_restore_pending(socket, _context), do: assign(socket, :history_pending, nil)

  defp maybe_clear_restore_error(socket, :restore), do: assign(socket, :history_error, nil)
  defp maybe_clear_restore_error(socket, _context), do: socket

  defp maybe_clear_recovery_errors(socket, options) do
    if Keyword.get(options, :clear_recovery_errors, false),
      do: assign(socket, :recovery_field_errors, %{}),
      else: socket
  end

  defp restore_state(request, item, profiles, ui) do
    selected_profile_id = request["profileId"] || item["profileId"] || ""

    selected_profile_id =
      if(profile_known?(profiles, selected_profile_id), do: selected_profile_id, else: "")

    reasoning = request["reasoningEffort"] || "lowest"
    schema = state_schema(request["schema"])
    call_type = response_call_type(request)

    %{
      "schemaVersion" => 3,
      "selectedProfileId" => selected_profile_id,
      "modelId" => request["modelId"] || "",
      "systemPrompt" => request["systemPrompt"] || "",
      "userPrompt" => request["userPrompt"] || "",
      "schemaShorthand" => request["schemaShorthand"] || "",
      "callType" => call_type,
      "schema" => schema,
      "reasoningEffort" => reasoning,
      "reasoningByProfile" =>
        if(selected_profile_id == "", do: %{}, else: %{selected_profile_id => reasoning}),
      "recoveryPolicy" =>
        ProfileWidgetState.serialize_current_recovery_policy(request["recoveryPolicy"]),
      "cacheMode" => normalize_cache_mode(request["cacheMode"]),
      "webSearch" => truthy?(request["webSearch"]),
      "ui" => normalize_ui(ui)
    }
  end

  defp persist_form_state(socket, params) do
    queue_state_save(
      socket,
      workspace_state(params, socket.assigns.ui, socket.assigns.reasoning_by_profile)
    )
  end

  defp update_workspace_form(socket, key, value) do
    {:noreply, update_workspace_snapshot(socket, %{key => value})}
  end

  defp apply_profile_selection(socket, selection) do
    updates = %{
      "selectedProfileId" => Map.get(selection, :profile_id, ""),
      "modelId" => Map.get(selection, :model_id, ""),
      "reasoningEffort" => Map.get(selection, :reasoning_effort, "lowest"),
      "recoveryPolicy" => Map.get(selection, :recovery_policy, %{})
    }

    socket =
      socket
      |> assign(:profile_provider_options, Map.get(selection, :provider_options, %{}))
      |> update_workspace_snapshot(updates)

    {:noreply, socket}
  end

  defp update_workspace_snapshot(socket, updates, options \\ []) when is_map(updates) do
    params = Map.merge(socket.assigns.form.params || %{}, updates)
    state = workspace_state(params, socket.assigns.ui, socket.assigns.reasoning_by_profile)

    socket =
      socket
      |> assign(:form, to_form(params, as: :run))
      |> assign(:reasoning_by_profile, state["reasoningByProfile"])
      |> maybe_clear_recovery_errors(options)
      |> queue_state_save(state)

    socket
  end

  defp event_form_params(%{"run" => params}, socket) when is_map(params) do
    Map.merge(socket.assigns.form.params || %{}, params)
  end

  defp event_form_params(_event_params, socket), do: socket.assigns.form.params || %{}

  defp run_payload(params, profiles, profile_provider_options) do
    prompt = String.trim(params["userPrompt"] || "")
    profile_id = String.trim(params["selectedProfileId"] || "")
    call_type = response_call_type(params)

    cond do
      profile_id == "" ->
        {:error, "Choose a profile before running."}

      prompt == "" ->
        {:error, "Enter a prompt before running."}

      true ->
        with {:ok, schema} <- parse_schema(params["schema"], call_type),
             {:ok, payload} <-
               base_run_payload(
                 params,
                 profile_id,
                 prompt,
                 call_type,
                 schema,
                 profiles,
                 profile_provider_options
               ) do
          {:ok, payload}
        end
    end
  end

  defp run_disabled?(form, schema_check, run_ref) do
    profile_id = String.trim(form[:selectedProfileId].value || "")
    schema = String.trim(form[:schema].value || "")
    call_type = response_call_type(form.params || %{})

    run_ref != nil or profile_id == "" or
      (call_type == "structured" and (schema == "" or schema_check.status != :valid))
  end

  defp new_disabled?(form, run_result, conversation_trace_id) do
    params = form.params || %{}

    prompt_fields_empty? =
      Enum.all?(["userPrompt", "systemPrompt", "schemaShorthand", "schema"], fn key ->
        String.trim(params[key] || "") == ""
      end)

    prompt_fields_empty? and is_nil(run_result) and is_nil(conversation_trace_id)
  end

  defp base_run_payload(
         params,
         profile_id,
         prompt,
         call_type,
         schema,
         profiles,
         profile_provider_options
       ) do
    payload = %{
      "profileId" => profile_id,
      "userPrompt" => prompt,
      "callType" => call_type,
      "recoveryPolicy" =>
        ProfileWidgetState.serialize_current_recovery_policy(params["recoveryPolicy"]),
      "cacheMode" => normalize_cache_mode(params["cacheMode"]),
      "webSearch" => truthy?(params["webSearch"])
    }

    payload = put_optional(payload, "modelId", params["modelId"])
    payload = put_optional(payload, "systemPrompt", params["systemPrompt"])
    payload = put_reasoning_effort(payload, params["reasoningEffort"], profiles, profile_id)
    payload = if schema, do: Map.put(payload, "schema", schema), else: payload

    payload =
      if is_map(profile_provider_options) and map_size(profile_provider_options) > 0,
        do: Map.put(payload, "providerOptions", profile_provider_options),
        else: payload

    {:ok, payload}
  end

  defp workspace_state(params, ui, existing_reasoning_by_profile) do
    schema = state_schema(params["schema"])
    profile_id = params["selectedProfileId"] || ""
    reasoning_effort = params["reasoningEffort"] || "lowest"
    call_type = response_call_type(params)

    reasoning_by_profile =
      cond do
        profile_id == "" ->
          existing_reasoning_by_profile

        reasoning_effort in ["lowest", "middle", "highest"] ->
          Map.put(existing_reasoning_by_profile, profile_id, reasoning_effort)

        true ->
          Map.delete(existing_reasoning_by_profile, profile_id)
      end

    %{
      "schemaVersion" => 3,
      "selectedProfileId" => params["selectedProfileId"] || "",
      "modelId" => params["modelId"] || "",
      "systemPrompt" => params["systemPrompt"] || "",
      "userPrompt" => params["userPrompt"] || "",
      "schemaShorthand" => params["schemaShorthand"] || "",
      "callType" => call_type,
      "schema" => schema,
      "reasoningEffort" => reasoning_effort,
      "reasoningByProfile" => reasoning_by_profile,
      "recoveryPolicy" =>
        ProfileWidgetState.serialize_current_recovery_policy(params["recoveryPolicy"]),
      "cacheMode" => normalize_cache_mode(params["cacheMode"]),
      "webSearch" => truthy?(params["webSearch"]),
      "ui" => normalize_ui(ui)
    }
  end

  defp normalize_response_state(state) do
    schema = state_schema(state["schema"])
    call_type = response_call_type(state)

    state
    |> Map.put("schema", schema)
    |> Map.put("callType", call_type)
    |> Map.put("webSearch", truthy?(state["webSearch"]))
  end

  defp response_call_type(params) when is_map(params) do
    case params["callType"] do
      "text" -> "text"
      "structured" -> "structured"
      _ -> inferred_call_type(params["schema"])
    end
  end

  defp response_call_type(_params), do: "text"

  defp inferred_call_type(value) when is_binary(value) do
    if String.trim(value) == "", do: "text", else: "structured"
  end

  defp inferred_call_type(value) when is_map(value), do: "structured"
  defp inferred_call_type(_value), do: "text"

  defp parse_schema(_value, "text"), do: {:ok, nil}

  defp parse_schema(value, "structured") when is_binary(value) do
    case WorkspaceSchema.check(value) do
      {:ok, nil, _message} ->
        {:error, "Structured output requires a valid JSON Schema object."}

      {:ok, schema, _message} ->
        {:ok, schema}

      {:error, message} ->
        {:error, "Structured output requires a valid JSON object schema. #{message}"}
    end
  end

  defp parse_schema(_value, "structured"),
    do: {:error, "Structured output requires a valid JSON Schema object."}

  defp parse_schema(_value, _call_type), do: {:ok, nil}

  defp schema_check_for_state(state) do
    case state["schema"] do
      schema when is_map(schema) ->
        case WorkspaceSchema.check(Jason.encode!(schema)) do
          {:ok, _schema, message} -> %{status: :valid, message: message}
          {:error, message} -> %{status: :error, message: message}
        end

      _ ->
        %{status: :idle, message: ""}
    end
  end

  defp schema_check_from_params(params) do
    case WorkspaceSchema.check(params["schema"] || "") do
      {:ok, nil, _message} -> %{status: :idle, message: ""}
      {:ok, _schema, message} -> %{status: :valid, message: message}
      {:error, message} -> %{status: :error, message: message}
    end
  end

  defp state_schema(value) when is_binary(value) do
    case Jason.decode(String.trim(value)) do
      {:ok, schema} when is_map(schema) -> schema
      _ -> nil
    end
  end

  defp state_schema(value) when is_map(value), do: value
  defp state_schema(_value), do: nil

  defp stringify_form(state) do
    schema =
      if is_map(state["schema"]), do: Jason.encode!(state["schema"], pretty: true), else: ""

    state
    |> Map.put("schema", schema)
    |> Map.put("schemaShorthand", state["schemaShorthand"] || "")
    |> Map.put("webSearch", to_string(truthy?(state["webSearch"])))
  end

  defp normalize_ui(value) when is_map(value),
    do: Map.merge(@default_ui, Map.take(value, @ui_keys))

  defp normalize_ui(_value), do: @default_ui

  defp normalize_trace_id(value) when is_binary(value) do
    case String.trim(value) do
      "" -> nil
      trace_id -> trace_id
    end
  end

  defp normalize_trace_id(_value), do: nil

  defp truthy?(value), do: value in [true, "true", "on", "1"]

  defp normalize_cache_mode("refresh"), do: "refresh"
  defp normalize_cache_mode(_value), do: "cache"

  defp put_reasoning_effort(payload, value, nil, _profile_id),
    do: put_optional(payload, "reasoningEffort", value)

  defp put_reasoning_effort(payload, value, profiles, profile_id) do
    if reasoning_supported?(profiles, profile_id, value) do
      put_optional(payload, "reasoningEffort", value)
    else
      payload
    end
  end

  defp reasoning_supported?(profiles, profile_id, effort)
       when is_list(profiles) and is_binary(effort) do
    effort = String.trim(effort)

    effort != "" and
      effort in ["lowest", "middle", "highest"] and
      Enum.any?(reasoning_options(profiles, profile_id), fn {_label, value} -> value == effort end)
  end

  defp reasoning_supported?(_profiles, _profile_id, _effort), do: false

  defp put_optional(map, _key, value) when value in [nil, ""], do: map
  defp put_optional(map, key, value), do: Map.put(map, key, String.trim(value))

  defp host_model_catalog(profiles) do
    profiles
    |> Enum.flat_map(fn profile_state ->
      profile_state
      |> get_in(["profile", "models"])
      |> List.wrap()
    end)
    |> ProfileWidgetState.normalize_model_catalog()
    |> case do
      [] -> nil
      models -> models
    end
  end
end
