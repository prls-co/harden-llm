defmodule HardenLlmWeb.WorkspaceLive do
  use HardenLlmWeb, :live_view

  alias HardenLlmWeb.{APIError, Auth, HardenAPI, Observability, ProfileWidgetState}

  @schema_keywords ~w($schema $defs additionalProperties allOf anyOf const default definitions description enum examples exclusiveMaximum exclusiveMinimum format items maxItems maxLength maximum minItems minLength minimum multipleOf not oneOf pattern prefixItems properties propertyOrdering required title type uniqueItems)
  @contracted_schema_keywords ~w(type properties required additionalProperties items description enum)
  @schema_types ~w(object array string number integer boolean)
  @reasoning_options [{"Lowest", "lowest"}, {"Middle", "middle"}, {"Highest", "highest"}]
  @ui_keys ~w(llmProfileConfigOpen modelOptionsOpen pricingOpen retryRepairOpen inputAdvancedOpen historyOpen outputDetailsOpen)

  @default_ui %{
    "llmProfileConfigOpen" => false,
    "modelOptionsOpen" => false,
    "pricingOpen" => false,
    "retryRepairOpen" => false,
    "inputAdvancedOpen" => false,
    "historyOpen" => false,
    "outputDetailsOpen" => false
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
    "schemaVersion" => 1,
    "selectedProfileId" => "",
    "modelId" => "",
    "systemPrompt" => "You are a helpful assistant",
    "userPrompt" => "write a haiku joke",
    "schemaShorthand" => @default_schema_shorthand,
    "schema" => @default_schema,
    "callType" => "structured",
    "structuredRepair" => true,
    "cacheMode" => "cache",
    "reasoningEffort" => "lowest",
    "reasoningByProfile" => %{},
    "maxAttempts" => 4,
    "initialBackoffMs" => 500,
    "maximumBackoffMs" => 8000,
    "retryNetwork" => true,
    "retryRateLimit" => true,
    "retryServerError" => true,
    "retryEmpty" => true,
    "retryParse" => true,
    "repairEscalation" => nil,
    "ui" => @default_ui
  }

  @impl true
  def mount(_params, _session, socket) do
    socket =
      socket
      |> assign(:page_title, "Workspace")
      |> assign(:loading?, true)
      |> assign(:backend_state, :loading)
      |> assign(:profiles, [])
      |> assign(:history, [])
      |> assign(:history_loaded?, false)
      |> assign(:history_loading?, false)
      |> assign(:history_error, nil)
      |> assign(:history_pending, nil)
      |> assign(:run_result, nil)
      |> assign(:run_error, nil)
      |> assign(:run_ref, nil)
      |> assign(:draft_error, nil)
      |> assign(:ui_error, nil)
      |> assign(:ui_save_pending?, false)
      |> assign(:output_request_open?, false)
      |> assign(:output_response_open?, false)
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
      |> allow_upload(:escalation_profile_bundle,
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
  def handle_info({:profile_widget, _prefix, {:profile_widget_ui, name, open}}, socket)
      when name in @ui_keys do
    toggle_ui(socket, name, to_string(open))
  end

  def handle_info({:profile_widget, _prefix, {:profile_widget_selection, profile_id}}, socket) do
    update_workspace_form(socket, "selectedProfileId", profile_id)
  end

  def handle_info({:profile_widget, _prefix, {:profile_widget_control, key, value}}, socket)
      when key in ["reasoningEffort", "cacheMode", "modelId"] do
    update_workspace_form(socket, key, value)
  end

  def handle_info(
        {:profile_widget, _prefix, {:profile_widget_provider_options, options}},
        socket
      )
      when is_map(options) do
    {:noreply, assign(socket, :profile_provider_options, options)}
  end

  def handle_info(
        {:profile_widget, _prefix, {:profile_widget_profile_dirty, requires_save?}},
        socket
      ) do
    {:noreply, assign(socket, :profile_requires_save?, requires_save?)}
  end

  def handle_info({:profile_widget, _prefix, {:profile_widget_retry, retry}}, socket)
      when is_map(retry) do
    params = Map.merge(socket.assigns.form.params || %{}, Map.delete(retry, "repairEscalation"))

    params =
      case retry["repairEscalation"] do
        escalation when is_map(escalation) ->
          params
          |> Map.put("repairEscalationProfileId", escalation["profileId"] || "")
          |> Map.put("repairEscalationModelId", escalation["modelId"] || "")
          |> Map.put("repairEscalationAttempt", to_string(escalation["attempt"] || 3))
          |> Map.put("repairEscalationReasoning", escalation["reasoningEffort"] || "highest")

        _ ->
          params
          |> Map.put("repairEscalationProfileId", "")
          |> Map.put("repairEscalationModelId", "")
      end

    state = state_from_params(params, socket.assigns.ui, socket.assigns.reasoning_by_profile)

    {:noreply,
     socket
     |> assign(:form, to_form(params, as: :run))
     |> assign(:reasoning_by_profile, state["reasoningByProfile"])}
  end

  def handle_info(
        {:profile_widget, _prefix, {:profile_widget_profiles, profiles, selected_profile_id}},
        socket
      ) do
    socket = assign(socket, :profiles, profiles)

    if selected_profile_id == (socket.assigns.form.params || %{})["selectedProfileId"] do
      {:noreply, socket}
    else
      update_workspace_form(socket, "selectedProfileId", selected_profile_id)
    end
  end

  @impl true
  def handle_async(_operation, {:ok, {:error, %APIError{status: 401}}}, socket) do
    {:noreply, Auth.expire_live(socket)}
  end

  def handle_async(:hydrate, {:ok, {:ok, hydration}}, socket) do
    state =
      @default_state
      |> Map.merge(hydration.state || %{})
      |> Map.update("cacheMode", "cache", &normalize_cache_mode/1)
      |> Map.put("ui", normalize_ui((hydration.state || %{})["ui"]))

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
      |> assign(:history, [])
      |> assign(:history_loaded?, false)
      |> assign(:history_loading?, false)
      |> assign(:history_error, nil)
      |> assign(:ui, state["ui"])
      |> assign(:reasoning_by_profile, reasoning_by_profile)
      |> assign(:form, to_form(stringify_form(state), as: :run))
      |> assign(:schema_check, schema_check_for_state(state))

    {:noreply, maybe_start_history_load(socket)}
  end

  def handle_async(:hydrate, _result, socket) do
    {:noreply,
     socket
     |> assign(:loading?, false)
     |> assign(:backend_state, :unavailable)}
  end

  def handle_async(:save_draft, {:ok, {:ok, _result, _state}}, socket) do
    {:noreply, assign(socket, :draft_error, nil)}
  end

  def handle_async(:save_draft, {:ok, {:error, %APIError{} = error}}, socket) do
    {:noreply, assign(socket, :draft_error, error.message)}
  end

  def handle_async(:save_draft, _result, socket) do
    {:noreply, assign(socket, :draft_error, "The draft could not be saved.")}
  end

  def handle_async(:save_ui, {:ok, {:ok, _result, _state}}, socket) do
    {:noreply,
     socket
     |> assign(:ui_save_pending?, false)
     |> assign(:ui_error, nil)}
  end

  def handle_async(:save_ui, {:ok, {:error, %APIError{} = error}}, socket) do
    {:noreply,
     socket
     |> assign(:ui_save_pending?, false)
     |> assign(:ui_error, error.message)}
  end

  def handle_async(:save_ui, _result, socket) do
    {:noreply,
     socket
     |> assign(:ui_save_pending?, false)
     |> assign(:ui_error, "The workspace display state could not be saved.")}
  end

  def handle_async(:load_history, {:ok, {:ok, %{"items" => history}, _state}}, socket) do
    {:noreply,
     socket
     |> assign(:history, history)
     |> assign(:history_loaded?, true)
     |> assign(:history_loading?, false)
     |> assign(:history_error, nil)}
  end

  def handle_async(:load_history, {:ok, {:error, %APIError{} = error}}, socket) do
    {:noreply,
     socket
     |> assign(:history_loading?, false)
     |> assign(:history_error, error.message)}
  end

  def handle_async(:load_history, _result, socket) do
    {:noreply,
     socket
     |> assign(:history_loading?, false)
     |> assign(:history_error, "History is temporarily unavailable.")}
  end

  def handle_async(
        {:run, reference},
        {:ok, {:ok, result, _state}},
        %{assigns: %{run_ref: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:run_ref, nil)
     |> assign(:run_result, result)
     |> assign(:run_error, nil)
     |> assign(:output_request_open?, false)
     |> assign(:output_response_open?, false)
     |> maybe_refresh_history()}
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

    {:noreply, socket |> assign(:run_ref, nil) |> assign(:run_error, message)}
  end

  def handle_async(
        {:run, reference},
        _result,
        %{assigns: %{run_ref: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:run_ref, nil)
     |> assign(:run_error, "The run could not be completed. Try again or check History.")}
  end

  def handle_async({:run, _stale_reference}, _result, socket), do: {:noreply, socket}

  def handle_async(
        {:restore_history, reference},
        {:ok, {:ok, _result, _state}},
        %{assigns: %{history_pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:history_pending, nil)
     |> assign(:history_error, nil)
     |> assign(:draft_error, nil)}
  end

  def handle_async(
        {:restore_history, reference},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{history_pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:history_pending, nil)
     |> assign(:history_error, error.message)}
  end

  def handle_async(
        {:restore_history, reference},
        _result,
        %{assigns: %{history_pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:history_pending, nil)
     |> assign(:history_error, "This history item could not be restored.")}
  end

  def handle_async(
        {:delete_history, reference, run_id},
        {:ok, {:ok, _result, _state}},
        %{assigns: %{history_pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:history_pending, nil)
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
     |> assign(:history, [])
     |> assign(:history_loaded?, true)
     |> assign(:history_error, nil)}
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
      when scope in ["profile", "escalation"] do
    {:noreply, socket}
  end

  def handle_event("save-draft", event_params, socket) do
    params = event_form_params(event_params, socket)
    state = state_from_params(params, socket.assigns.ui, socket.assigns.reasoning_by_profile)
    handle = socket.assigns.session_handle

    {:noreply,
     socket
     |> assign(:form, to_form(params, as: :run))
     |> assign(:run_error, nil)
     |> assign(:reasoning_by_profile, state["reasoningByProfile"])
     |> assign(:schema_check, schema_check_from_params(params))
     |> start_async(
       :save_draft,
       Observability.propagate(fn -> HardenAPI.save_state(handle, state) end)
     )}
  end

  def handle_event("validate-bundle", _params, socket), do: {:noreply, socket}

  def handle_event("import-bundle", params, socket) do
    upload = workspace_upload_name(params["kind"], params["widget"])

    results =
      consume_uploaded_entries(socket, upload, fn %{path: path}, _entry ->
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
      _ -> {:noreply, assign(socket, :ui_error, "The selected bundle was rejected.")}
    end
  end

  def handle_event("toggle-ui", %{"name" => name, "open" => value}, socket)
      when name in @ui_keys do
    if socket.assigns.ui_save_pending? do
      {:noreply, socket}
    else
      toggle_ui(socket, name, value)
    end
  end

  def handle_event("restore-history", %{"run-id" => run_id}, socket) do
    with item when is_map(item) <- Enum.find(socket.assigns.history, &(&1["runId"] == run_id)),
         request when is_map(request) <- item["request"] do
      state = restore_state(request, item, socket.assigns.profiles, socket.assigns.ui)
      params = stringify_form(state)
      reference = System.unique_integer([:positive, :monotonic])
      handle = socket.assigns.session_handle

      {:noreply,
       socket
       |> assign(:form, to_form(params, as: :run))
       |> assign(:reasoning_by_profile, state["reasoningByProfile"] || %{})
       |> assign(:schema_check, schema_check_for_state(state))
       |> assign(:history_pending, reference)
       |> start_async(
         {:restore_history, reference},
         Observability.propagate(fn -> HardenAPI.save_state(handle, state) end)
       )}
    else
      _ -> {:noreply, assign(socket, :history_error, "This history item could not be restored.")}
    end
  end

  def handle_event("restore-history", _params, socket), do: {:noreply, socket}

  def handle_event(
        "delete-history",
        %{"run-id" => run_id},
        %{assigns: %{history_pending: nil}} = socket
      ) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    {:noreply,
     socket
     |> assign(:history_pending, reference)
     |> start_async(
       {:delete_history, reference, run_id},
       Observability.propagate(fn -> HardenAPI.delete_history(handle, run_id) end)
     )}
  end

  def handle_event("delete-history", _params, socket), do: {:noreply, socket}

  def handle_event("clear-history", _params, %{assigns: %{history_pending: nil}} = socket) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    {:noreply,
     socket
     |> assign(:history_pending, reference)
     |> start_async(
       {:clear_history, reference},
       Observability.propagate(fn -> HardenAPI.clear_history(handle) end)
     )}
  end

  def handle_event("clear-history", _params, socket), do: {:noreply, socket}

  def handle_event("generate-schema", event_params, socket) do
    params = event_form_params(event_params, socket)
    shorthand = params["schemaShorthand"] || ""

    case generate_schema(shorthand) do
      {:ok, schema, message} ->
        next_params = Map.put(params, "schema", Jason.encode!(schema, pretty: true))

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
    next_params = params |> Map.put("schemaShorthand", "") |> Map.put("schema", "")

    {:noreply,
     socket
     |> assign(:form, to_form(next_params, as: :run))
     |> assign(:schema_check, %{status: :idle, message: ""})
     |> persist_form_state(next_params)}
  end

  def handle_event("clear-prompt", event_params, socket) do
    params = event_form_params(event_params, socket)

    next_params =
      params
      |> Map.put("systemPrompt", "")
      |> Map.put("userPrompt", "")
      |> Map.put("schemaShorthand", "")
      |> Map.put("schema", "")

    {:noreply,
     socket
     |> assign(:form, to_form(next_params, as: :run))
     |> assign(:schema_check, %{status: :idle, message: ""})
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
           "Save the LLM profile before running endpoint, credential, fallback, or identity changes."
         )}

      true ->
        case run_payload(params, socket.assigns.profiles, socket.assigns.profile_provider_options) do
          {:ok, payload} ->
            reference = System.unique_integer([:positive, :monotonic])
            handle = socket.assigns.session_handle

            {:noreply,
             socket
             |> assign(:form, to_form(params, as: :run))
             |> assign(:run_ref, reference)
             |> assign(:run_result, nil)
             |> assign(:run_error, nil)
             |> assign(:output_request_open?, false)
             |> assign(:output_response_open?, false)
             |> start_async(
               {:run, reference},
               Observability.propagate(fn -> HardenAPI.run(handle, payload) end)
             )}

          {:error, message} ->
            {:noreply, assign(socket, :run_error, message)}
        end
    end
  end

  def handle_event("run", _params, socket), do: {:noreply, socket}

  def handle_event("toggle-output-data", %{"kind" => "request"}, socket) do
    {:noreply, update(socket, :output_request_open?, &(!&1))}
  end

  def handle_event("toggle-output-data", %{"kind" => "response"}, socket) do
    {:noreply, update(socket, :output_response_open?, &(!&1))}
  end

  defp toggle_ui(socket, name, value) do
    ui = Map.put(socket.assigns.ui, name, truthy?(value))

    state =
      state_from_params(
        socket.assigns.form.params || %{},
        ui,
        socket.assigns.reasoning_by_profile
      )

    handle = socket.assigns.session_handle

    socket =
      socket
      |> assign(:ui, ui)
      |> assign(:ui_save_pending?, true)
      |> assign(:ui_error, nil)
      |> start_async(
        :save_ui,
        Observability.propagate(fn -> HardenAPI.save_state(handle, state) end)
      )

    {:noreply,
     if(name == "historyOpen" and truthy?(value),
       do: maybe_start_history_load(socket),
       else: socket
     )}
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

  def history_prompt_preview(item) do
    item
    |> get_in(["request", "userPrompt"])
    |> case do
      prompt when is_binary(prompt) and prompt != "" -> String.slice(String.trim(prompt), 0, 140)
      _ -> "No prompt preview"
    end
  end

  def safe_output(value) when is_binary(value), do: value
  def safe_output(value), do: Jason.encode!(value, pretty: true)

  def history_curl(item) do
    body = Jason.encode!(item["request"] || %{})
    "curl -X POST /api/v1/run -H 'content-type: application/json' --data-raw '#{body}'"
  end

  def history_result_json(item), do: safe_output(item["result"] || %{})

  def history_item_stats(item) do
    usage = get_in(item, ["result", "usage"]) || %{}
    cost = get_in(item, ["result", "cost"]) || %{}

    %{
      duration: duration_ms(item),
      input_tokens: usage["inputTokens"] || 0,
      output_tokens: usage["outputTokens"] || 0,
      total_tokens: usage["totalTokens"] || 0,
      cost: if(cost["known"] == false, do: nil, else: cost["totalUsd"]),
      attempts: length(get_in(item, ["result", "attempts"]) || [])
    }
  end

  def stats_summary(history) do
    durations = history |> Enum.map(&duration_ms/1) |> Enum.reject(&is_nil/1)
    usage = Enum.map(history, &(get_in(&1, ["result", "usage"]) || %{}))
    costs = Enum.map(history, &(get_in(&1, ["result", "cost"]) || %{}))

    %{
      success: Enum.count(history, &(&1["status"] == "succeeded")),
      failed: Enum.count(history, &(&1["status"] == "failed")),
      timeout: Enum.count(history, &(&1["status"] == "timeout")),
      prompt_tokens: sum_metric(usage, "inputTokens"),
      cache_read_tokens: sum_metric(usage, "cacheReadTokens"),
      cache_creation_tokens: sum_metric(usage, "cacheCreationTokens"),
      output_tokens: sum_metric(usage, "outputTokens"),
      reasoning_tokens: sum_metric(usage, "reasoningTokens"),
      total_tokens: sum_metric(usage, "totalTokens"),
      known_cost:
        Enum.sum(Enum.map(costs, &if(&1["known"] == false, do: 0, else: &1["totalUsd"] || 0))),
      average_duration:
        if(durations == [], do: nil, else: div(Enum.sum(durations), length(durations)))
    }
  end

  def run_request_json(params), do: run_request_json(params, nil)

  def run_request_json(params, profiles), do: safe_output(run_request(params, profiles))

  def run_curl(params), do: run_curl(params, nil)

  def run_curl(params, profiles) do
    body = Jason.encode!(run_request(params, profiles))

    "curl -X POST /api/v1/run -H 'content-type: application/json' --data-raw '#{body}'"
  end

  defp run_request(params, profiles) do
    case run_payload(params || %{}, profiles) do
      {:ok, payload} -> payload
      {:error, _message} -> %{}
    end
  end

  defp sum_metric(values, key), do: Enum.sum(Enum.map(values, &(&1[key] || 0)))

  def schema_status_text(%{status: :valid, message: message})
      when is_binary(message) and message != "", do: message

  def schema_status_text(%{status: :valid}), do: "Schema valid."
  def schema_status_text(%{status: :error, message: message}), do: message
  def schema_status_text(%{status: :pending}), do: "Schema check pending."
  def schema_status_text(_), do: ""

  def schema_status_class(%{status: :error}), do: "text-rose-700"
  def schema_status_class(%{status: :valid}), do: "text-emerald-700"
  def schema_status_class(_), do: "text-slate-500"

  attr :label, :string, required: true
  attr :value, :any, default: nil

  def result_fact(assigns) do
    ~H"""
    <div class="min-w-0 rounded-lg border border-slate-800 p-2">
      <dt class="text-slate-500">{@label}</dt>
      <dd class="mt-1 truncate font-mono text-slate-200" title={to_string(@value || "—")}>
        {@value || "—"}
      </dd>
    </div>
    """
  end

  attr :label, :string, required: true
  attr :value, :any, default: nil

  def history_fact(assigns) do
    ~H"""
    <div class="min-w-0 rounded-lg bg-white p-2">
      <dt class="text-slate-500">{@label}</dt>
      <dd
        class="mt-1 truncate font-mono font-semibold text-slate-700"
        title={to_string(@value || "—")}
      >
        {@value || "—"}
      </dd>
    </div>
    """
  end

  defp hydrate(handle) do
    with {:ok, _result, state} <- HardenAPI.get_state(handle),
         {:ok, %{"profiles" => profiles}, _} <- HardenAPI.list_profiles(handle),
         true <- is_list(profiles) do
      {:ok, %{state: state || %{}, profiles: profiles}}
    end
  end

  defp maybe_start_history_load(socket) do
    if socket.assigns.ui["historyOpen"] and not socket.assigns.history_loaded? and
         not socket.assigns.history_loading? do
      start_history_load(socket)
    else
      socket
    end
  end

  defp maybe_refresh_history(socket) do
    if socket.assigns.history_loaded? and not socket.assigns.history_loading? do
      start_history_load(socket)
    else
      socket
    end
  end

  defp start_history_load(socket) do
    handle = socket.assigns.session_handle

    socket
    |> assign(:history_loading?, true)
    |> assign(:history_error, nil)
    |> start_async(
      :load_history,
      Observability.propagate(fn -> HardenAPI.list_history(handle, limit: 10) end)
    )
  end

  defp restore_state(request, item, profiles, ui) do
    selected_profile_id = request["profileId"] || item["profileId"] || ""

    selected_profile_id =
      if(profile_known?(profiles, selected_profile_id), do: selected_profile_id, else: "")

    reasoning = request["reasoningEffort"] || "lowest"

    %{
      "schemaVersion" => 1,
      "selectedProfileId" => selected_profile_id,
      "modelId" => request["modelId"] || "",
      "systemPrompt" => request["systemPrompt"] || "",
      "userPrompt" => request["userPrompt"] || "",
      "schemaShorthand" => request["schemaShorthand"] || "",
      "callType" =>
        request["callType"] || if(is_map(request["schema"]), do: "structured", else: "text"),
      "schema" => request["schema"],
      "reasoningEffort" => reasoning,
      "reasoningByProfile" =>
        if(selected_profile_id == "", do: %{}, else: %{selected_profile_id => reasoning}),
      "structuredRepair" =>
        if(Map.has_key?(request, "structuredRepair"),
          do: truthy?(request["structuredRepair"]),
          else: is_map(request["schema"])
        ),
      "cacheMode" => normalize_cache_mode(request["cacheMode"]),
      "maxAttempts" => request["maxAttempts"] || 0,
      "initialBackoffMs" => request["initialBackoffMs"] || 0,
      "maximumBackoffMs" => request["maximumBackoffMs"] || 0,
      "retryNetwork" => request["retryNetwork"],
      "retryRateLimit" => request["retryRateLimit"],
      "retryServerError" => request["retryServerError"],
      "retryEmpty" => request["retryEmpty"],
      "retryParse" => request["retryParse"],
      "repairEscalation" => request["repairEscalation"],
      "ui" => normalize_ui(ui)
    }
  end

  defp persist_form_state(socket, params) do
    handle = socket.assigns.session_handle
    state = state_from_params(params, socket.assigns.ui, socket.assigns.reasoning_by_profile)

    start_async(
      socket,
      :save_draft,
      Observability.propagate(fn -> HardenAPI.save_state(handle, state) end)
    )
  end

  defp update_workspace_form(socket, key, value) do
    params = Map.put(socket.assigns.form.params || %{}, key, value)
    state = state_from_params(params, socket.assigns.ui, socket.assigns.reasoning_by_profile)

    {:noreply,
     socket
     |> assign(:form, to_form(params, as: :run))
     |> assign(:reasoning_by_profile, state["reasoningByProfile"])
     |> persist_form_state(params)}
  end

  defp event_form_params(%{"run" => params}, socket) when is_map(params) do
    Map.merge(socket.assigns.form.params || %{}, params)
  end

  defp event_form_params(_event_params, socket), do: socket.assigns.form.params || %{}

  defp run_payload(params, profiles, profile_provider_options \\ %{}) do
    prompt = String.trim(params["userPrompt"] || "")
    profile_id = String.trim(params["selectedProfileId"] || "")
    call_type = params["callType"] || "text"

    cond do
      profile_id == "" ->
        {:error, "Choose a profile before running."}

      prompt == "" ->
        {:error, "Enter a prompt before running."}

      true ->
        with {:ok, schema} <- parse_schema(params["schema"], call_type),
             {:ok, retry} <- retry_payload(params),
             {:ok, payload} <-
               base_run_payload(
                 params,
                 profile_id,
                 prompt,
                 call_type,
                 schema,
                 retry,
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

    run_ref != nil or profile_id == "" or
      (schema != "" and schema_check.status != :valid)
  end

  defp base_run_payload(
         params,
         profile_id,
         prompt,
         call_type,
         schema,
         retry,
         profiles,
         profile_provider_options
       ) do
    structured_repair = structured_repair?(params, call_type)

    payload = %{
      "profileId" => profile_id,
      "userPrompt" => prompt,
      "callType" => call_type,
      "cacheMode" => normalize_cache_mode(params["cacheMode"]),
      "structuredRepair" => structured_repair
    }

    payload = put_optional(payload, "modelId", params["modelId"])
    payload = put_optional(payload, "systemPrompt", params["systemPrompt"])
    payload = put_reasoning_effort(payload, params["reasoningEffort"], profiles, profile_id)
    payload = if schema, do: Map.put(payload, "schema", schema), else: payload

    payload =
      if is_map(profile_provider_options) and map_size(profile_provider_options) > 0,
        do: Map.put(payload, "providerOptions", profile_provider_options),
        else: payload

    retry =
      if structured_repair,
        do: retry,
        else: Map.delete(retry, "repairEscalation")

    {payload, retry} =
      normalize_repair_reasoning(payload, retry, params, profiles, profile_id)

    {:ok, Map.merge(payload, retry)}
  end

  defp retry_payload(params) do
    with {:ok, max_attempts} <- integer_value(params["maxAttempts"], "Max Attempts", 0, 10, 4),
         {:ok, initial_backoff} <-
           integer_value(params["initialBackoffMs"], "Base Delay Ms", 0, 60_000, 500),
         {:ok, maximum_backoff} <-
           integer_value(params["maximumBackoffMs"], "Max Delay Ms", 0, 600_000, 8000),
         {:ok, escalation} <- escalation_payload(params) do
      payload = %{
        "maxAttempts" => max_attempts,
        "initialBackoffMs" => initial_backoff,
        "maximumBackoffMs" => maximum_backoff,
        "retryNetwork" => boolean_value(params["retryNetwork"], true),
        "retryRateLimit" => boolean_value(params["retryRateLimit"], true),
        "retryServerError" => boolean_value(params["retryServerError"], true),
        "retryEmpty" => boolean_value(params["retryEmpty"], true),
        "retryParse" => boolean_value(params["retryParse"], true)
      }

      {:ok, if(escalation, do: Map.put(payload, "repairEscalation", escalation), else: payload)}
    end
  end

  defp escalation_payload(params) do
    model_id = String.trim(params["repairEscalationModelId"] || "")

    if model_id == "" do
      {:ok, nil}
    else
      with {:ok, attempt} <-
             integer_value(params["repairEscalationAttempt"], "Starting Attempt", 2, 10, 3),
           reasoning <- params["repairEscalationReasoning"] || "highest",
           true <- reasoning in ["lowest", "middle", "highest"] do
        escalation = %{
          "attempt" => attempt,
          "modelId" => model_id,
          "reasoningEffort" => reasoning
        }

        profile_id = String.trim(params["repairEscalationProfileId"] || "")

        {:ok,
         if(profile_id == "", do: escalation, else: Map.put(escalation, "profileId", profile_id))}
      else
        false -> {:error, "Escalation reasoning must be lowest, middle, or highest."}
        {:error, message} -> {:error, message}
      end
    end
  end

  defp state_from_params(params, ui, existing_reasoning_by_profile) do
    schema = state_schema(params["schema"])
    profile_id = params["selectedProfileId"] || ""
    reasoning_effort = params["reasoningEffort"] || "lowest"
    call_type = params["callType"] || if(is_map(schema), do: "structured", else: "text")

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
      "schemaVersion" => 1,
      "selectedProfileId" => params["selectedProfileId"] || "",
      "modelId" => params["modelId"] || "",
      "systemPrompt" => params["systemPrompt"] || "",
      "userPrompt" => params["userPrompt"] || "",
      "schemaShorthand" => params["schemaShorthand"] || "",
      "callType" => call_type,
      "schema" => schema,
      "reasoningEffort" => reasoning_effort,
      "reasoningByProfile" => reasoning_by_profile,
      "structuredRepair" => structured_repair?(params, call_type),
      "cacheMode" => normalize_cache_mode(params["cacheMode"]),
      "maxAttempts" => integer_or_zero(params["maxAttempts"]),
      "initialBackoffMs" => integer_or_zero(params["initialBackoffMs"]),
      "maximumBackoffMs" => integer_or_zero(params["maximumBackoffMs"]),
      "retryNetwork" => truthy?(params["retryNetwork"]),
      "retryRateLimit" => truthy?(params["retryRateLimit"]),
      "retryServerError" => truthy?(params["retryServerError"]),
      "retryEmpty" => truthy?(params["retryEmpty"]),
      "retryParse" => truthy?(params["retryParse"]),
      "repairEscalation" => state_escalation(params),
      "ui" => normalize_ui(ui)
    }
  end

  defp state_escalation(params) do
    case escalation_payload(params) do
      {:ok, nil} -> nil
      {:ok, escalation} -> escalation
      {:error, _message} -> nil
    end
  end

  defp structured_repair?(params, "structured") do
    if Map.has_key?(params, "structuredRepair"),
      do: truthy?(params["structuredRepair"]),
      else: true
  end

  defp structured_repair?(params, _call_type), do: truthy?(params["structuredRepair"])

  defp parse_schema(value, call_type) when is_binary(value) do
    case schema_check(value) do
      {:ok, nil, _message} ->
        if call_type == "structured" do
          {:error, "Structured output requires a valid JSON Schema object."}
        else
          {:ok, nil}
        end

      {:ok, schema, _message} ->
        if call_type == "structured", do: {:ok, schema}, else: {:ok, nil}

      {:error, message} ->
        if call_type == "structured" do
          {:error, "Structured output requires a valid JSON object schema. #{message}"}
        else
          {:error, message}
        end
    end
  end

  defp parse_schema(_value, _call_type), do: {:ok, nil}

  defp schema_check_for_state(state) do
    case state["schema"] do
      schema when is_map(schema) ->
        case schema_check(Jason.encode!(schema)) do
          {:ok, _schema, message} -> %{status: :valid, message: message}
          {:error, message} -> %{status: :error, message: message}
        end

      _ ->
        %{status: :idle, message: ""}
    end
  end

  defp schema_check_from_params(params) do
    case schema_check(params["schema"] || "") do
      {:ok, nil, _message} -> %{status: :idle, message: ""}
      {:ok, _schema, message} -> %{status: :valid, message: message}
      {:error, message} -> %{status: :error, message: message}
    end
  end

  defp schema_check(value) when is_binary(value) do
    text = String.trim(value)

    if text == "" do
      {:ok, nil, ""}
    else
      case Jason.decode(text) do
        {:ok, schema} when is_map(schema) ->
          cond do
            not schema_object?(schema) ->
              {:error,
               "schemaJson must be a JSON Schema object. Generate it from shorthand first."}

            true ->
              case validate_contracted_schema(schema) do
                :ok -> {:ok, schema, "Schema valid."}
                {:error, message} -> {:error, message}
              end
          end

        {:ok, _} ->
          {:error, "schemaJson must be a JSON object."}

        {:error, _} ->
          {:error, "schemaJson must be valid JSON."}
      end
    end
  end

  defp schema_check(_value), do: {:error, "schemaJson must be valid JSON."}

  defp generate_schema(value) do
    case Jason.decode(String.trim(value || "{}")) do
      {:ok, shorthand} when is_map(shorthand) ->
        schema = shorthand_schema(shorthand) |> prepare_schema() |> Map.delete("$schema")

        case validate_contracted_schema(schema) do
          :ok -> {:ok, schema, "Schema generated."}
          {:error, message} -> {:error, message}
        end

      {:ok, _} ->
        {:error, "schemaShorthand must be a JSON object."}

      {:error, _} ->
        {:error, "schemaShorthand must be valid JSON."}
    end
  end

  defp shorthand_schema(value) when is_map(value) do
    if schema_object?(value) do
      normalize_schema_object(value)
    else
      properties =
        Map.new(value, fn {key, descriptor} -> {key, schema_descriptor(descriptor)} end)

      %{
        "type" => "object",
        "properties" => properties,
        "required" => Map.keys(properties),
        "additionalProperties" => false
      }
    end
  end

  defp schema_descriptor(value) when is_binary(value) do
    %{"type" => normalize_type(value) || "string"}
  end

  defp schema_descriptor(value) when is_list(value) do
    %{"type" => "array", "items" => schema_descriptor(List.first(value) || "string")}
  end

  defp schema_descriptor(value) when is_map(value), do: shorthand_schema(value)
  defp schema_descriptor(_value), do: %{"type" => "string"}

  defp normalize_schema_object(value) do
    value =
      if is_binary(value["type"]),
        do: Map.put(value, "type", normalize_type(value["type"]) || value["type"]),
        else: value

    value =
      if is_map(value["properties"]),
        do:
          Map.put(
            value,
            "properties",
            Map.new(value["properties"], fn {key, descriptor} ->
              {key, schema_descriptor(descriptor)}
            end)
          ),
        else: value

    if is_map(value["items"]),
      do: Map.put(value, "items", schema_descriptor(value["items"])),
      else: value
  end

  defp prepare_schema(value) when is_map(value) do
    value =
      value
      |> maybe_prepare_properties()
      |> maybe_prepare_items()

    if value["type"] == "object" || is_map(value["properties"]),
      do: Map.put_new(value, "additionalProperties", false),
      else: value
  end

  defp prepare_schema(value), do: value

  defp maybe_prepare_properties(value) do
    if is_map(value["properties"]),
      do:
        Map.put(
          value,
          "properties",
          Map.new(value["properties"], fn {key, child} -> {key, prepare_schema(child)} end)
        ),
      else: value
  end

  defp maybe_prepare_items(value) do
    if is_map(value["items"]),
      do: Map.put(value, "items", prepare_schema(value["items"])),
      else: value
  end

  defp validate_contracted_schema(schema) when is_map(schema),
    do: validate_schema_node(schema, "", true)

  defp validate_schema_node(node, path, root?) when is_map(node) do
    with :ok <- validate_schema_keys(node, path),
         :ok <- validate_schema_type(node, path, root?),
         :ok <- validate_schema_enum(node, path),
         :ok <- validate_schema_object(node, path),
         :ok <- validate_schema_array(node, path) do
      validate_schema_children(node, path)
    end
  end

  defp validate_schema_node(_node, path, _root?),
    do: {:error, "#{path || "schema"} must be an object."}

  defp validate_schema_keys(node, path) do
    case Enum.find(Map.keys(node), &(&1 not in @contracted_schema_keywords)) do
      nil ->
        :ok

      key ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, key)}: " <>
           "#{key} is not part of the utility-llm contracted schema subset."}
    end
  end

  defp validate_schema_type(node, path, root?) do
    type = node["type"]

    cond do
      not is_binary(type) ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "type")}: " <>
           "type must be a contracted string type."}

      type not in @schema_types ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "type")}: " <>
           "#{type} is not a contracted schema type."}

      root? and type != "object" ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "type")}: " <>
           "root schema must be an object."}

      true ->
        :ok
    end
  end

  defp validate_schema_enum(%{"enum" => values}, path)
       when is_list(values) do
    if Enum.all?(values, &scalar_enum_value?/1) do
      :ok
    else
      {:error,
       "Unsupported structured output schema at #{json_pointer(path, "enum")}: " <>
         "enum must contain only scalar values."}
    end
  end

  defp validate_schema_enum(%{"enum" => _values}, path),
    do:
      {:error,
       "Unsupported structured output schema at #{json_pointer(path, "enum")}: " <>
         "enum must contain only scalar values."}

  defp validate_schema_enum(_node, _path), do: :ok

  defp validate_schema_object(%{"type" => "object"} = node, path) do
    cond do
      not is_map(node["properties"]) ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "properties")}: " <>
           "object schemas must define properties."}

      node["additionalProperties"] != false ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "additionalProperties")}: " <>
           "object schemas must set additionalProperties: false."}

      not is_list(node["required"]) ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "required")}: " <>
           "object schemas must list all required properties."}

      Enum.any?(node["required"], &(not Map.has_key?(node["properties"], &1))) ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "required")}: " <>
           "required property is not defined in properties."}

      Enum.any?(Map.keys(node["properties"]), &(&1 not in node["required"])) ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "properties")}: " <>
           "every property must be listed in required."}

      true ->
        :ok
    end
  end

  defp validate_schema_object(_node, _path), do: :ok

  defp validate_schema_array(%{"type" => "array"} = node, path) do
    if is_map(node["items"]) do
      :ok
    else
      {:error,
       "Unsupported structured output schema at #{json_pointer(path, "items")}: " <>
         "array schemas must define a single object-form items schema."}
    end
  end

  defp validate_schema_array(_node, _path), do: :ok

  defp scalar_enum_value?(value),
    do: is_nil(value) or is_binary(value) or is_number(value) or is_boolean(value)

  defp json_pointer(path, key) do
    escaped = key |> to_string() |> String.replace("~", "~0") |> String.replace("/", "~1")
    "#{path}/#{escaped}"
  end

  defp validate_schema_children(node, path) do
    property_result =
      if is_map(node["properties"]) do
        Enum.reduce_while(node["properties"], :ok, fn {key, child}, :ok ->
          case validate_schema_node(child, "#{path}/properties/#{key}", false) do
            :ok -> {:cont, :ok}
            error -> {:halt, error}
          end
        end)
      else
        :ok
      end

    with :ok <- property_result do
      if is_map(node["items"]),
        do: validate_schema_node(node["items"], "#{path}/items", false),
        else: :ok
    end
  end

  defp schema_object?(value) do
    keys = Map.keys(value)
    type = value["type"]

    Enum.any?(keys, &(&1 != "type" and &1 in @schema_keywords)) or
      ((is_binary(type) or is_list(type)) and Enum.all?(keys, &(&1 in @schema_keywords)))
  end

  defp normalize_type(value) do
    %{
      "array" => "array",
      "bool" => "boolean",
      "boolean" => "boolean",
      "double" => "number",
      "float" => "number",
      "int" => "integer",
      "integer" => "integer",
      "list" => "array",
      "null" => "null",
      "number" => "number",
      "object" => "object",
      "str" => "string",
      "string" => "string",
      "text" => "string"
    }[String.downcase(String.trim(value))]
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

    escalation = state["repairEscalation"] || %{}

    state
    |> Map.put("schema", schema)
    |> Map.put("schemaShorthand", state["schemaShorthand"] || "")
    |> Map.put("repairEscalationModelId", escalation["modelId"] || "")
    |> Map.put("repairEscalationProfileId", escalation["profileId"] || "")
    |> Map.put("repairEscalationAttempt", to_string(escalation["attempt"] || 3))
    |> Map.put("repairEscalationReasoning", escalation["reasoningEffort"] || "highest")
    |> stringify_numbers(["maxAttempts", "initialBackoffMs", "maximumBackoffMs"])
  end

  defp stringify_numbers(state, keys) do
    Enum.reduce(keys, state, fn key, acc ->
      Map.update(acc, key, "", fn value -> to_string(value) end)
    end)
  end

  defp normalize_ui(value) when is_map(value),
    do: Map.merge(@default_ui, Map.take(value, @ui_keys))

  defp normalize_ui(_value), do: @default_ui

  defp duration_ms(item) do
    with {:ok, started, _} <- DateTime.from_iso8601(item["startedAt"] || ""),
         {:ok, completed, _} <- DateTime.from_iso8601(item["completedAt"] || "") do
      DateTime.diff(completed, started, :millisecond)
    else
      _ -> nil
    end
  end

  defp integer_value(value, _label, _minimum, _maximum, default) when value in [nil, ""],
    do: {:ok, default}

  defp integer_value(value, label, minimum, maximum, _default) do
    text = String.trim(to_string(value || ""))

    case Integer.parse(text) do
      {number, ""} when number >= minimum and number <= maximum -> {:ok, number}
      _ -> {:error, "#{label} must be between #{minimum} and #{maximum}."}
    end
  end

  defp integer_or_zero(value) do
    case Integer.parse(String.trim(to_string(value || ""))) do
      {number, ""} when number >= 0 -> number
      _ -> 0
    end
  end

  defp truthy?(value), do: value in [true, "true", "on", "1"]

  defp normalize_cache_mode("refresh"), do: "refresh"
  defp normalize_cache_mode(_value), do: "cache"

  defp boolean_value(value, _default) when value in [true, "true", "on", "1"], do: true
  defp boolean_value(value, _default) when value in [false, "false", "0"], do: false
  defp boolean_value(_value, default), do: default

  defp put_reasoning_effort(payload, value, nil, _profile_id),
    do: put_optional(payload, "reasoningEffort", value)

  defp put_reasoning_effort(payload, value, profiles, profile_id) do
    if reasoning_supported?(profiles, profile_id, value) do
      put_optional(payload, "reasoningEffort", value)
    else
      payload
    end
  end

  defp normalize_repair_reasoning(payload, retry, _params, nil, _profile_id),
    do: {payload, retry}

  defp normalize_repair_reasoning(payload, retry, params, profiles, profile_id) do
    case retry["repairEscalation"] do
      escalation when is_map(escalation) ->
        escalation_profile_id =
          String.trim(to_string(escalation["profileId"] || profile_id || ""))

        escalation_reasoning = String.trim(to_string(escalation["reasoningEffort"] || ""))
        main_reasoning = String.trim(to_string(params["reasoningEffort"] || ""))
        escalation_profile_known? = profile_known?(profiles, escalation_profile_id)

        escalation_reasoning_supported? =
          not escalation_profile_known? or
            escalation_reasoning == "" or
            reasoning_supported?(profiles, escalation_profile_id, escalation_reasoning)

        escalation =
          if escalation_reasoning_supported?,
            do: escalation,
            else: Map.delete(escalation, "reasoningEffort")

        payload =
          if not escalation_profile_known? or
               (escalation_reasoning_supported? and escalation_reasoning != "") do
            payload
          else
            main_reasoning_supported? =
              main_reasoning == "" or
                reasoning_supported?(profiles, escalation_profile_id, main_reasoning)

            if main_reasoning_supported?,
              do: payload,
              else: Map.delete(payload, "reasoningEffort")
          end

        {payload, Map.put(retry, "repairEscalation", escalation)}

      _ ->
        {payload, retry}
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

  defp workspace_upload_name("escalation", _widget), do: :escalation_profile_bundle
  defp workspace_upload_name(_kind, _widget), do: :profile_bundle

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
