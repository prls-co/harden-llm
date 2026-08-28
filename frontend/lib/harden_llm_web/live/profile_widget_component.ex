defmodule HardenLlmWeb.ProfileWidgetComponent do
  @moduledoc """
  The provider-neutral LLM profile widget.

  This is deliberately a LiveComponent rather than a page-level editor. The
  compact row and every configuration fold can therefore be embedded beside a
  caller's prompt, output, or other controls without introducing navigation or
  a modal surface. Hosts that mount more than one instance pass a distinct
  `id_prefix`; it namespaces generated form/control IDs and parent messages,
  while the host supplies matching main/escalation upload configurations.
  """

  use HardenLlmWeb, :live_component

  alias HardenLlmWeb.{
    APIError,
    HardenAPI,
    Observability,
    ProfileDefaults,
    ProfileWidgetState,
    ProfilesLive
  }

  @api_inference_types [
    {"Chat Completions", "chat-completions"},
    {"OpenAI Responses", "responses"},
    {"Gemini Generate Content", "gemini-generate-content"},
    {"Anthropic Messages", "anthropic-messages"}
  ]

  @reasoning_options [{"lowest", "L"}, {"middle", "M"}, {"highest", "H"}]

  @fold_keys ~w(
    main_credential_open main_fallback_open main_options_open main_retry_open main_pricing_open
    escalation_credential_open escalation_fallback_open escalation_options_open escalation_pricing_open
  )

  @impl true
  def mount(socket) do
    {:ok,
     socket
     |> assign(:initialized?, false)
     |> assign(:id_prefix, "")
     |> assign(:loaded_profile_id, nil)
     |> assign(:profiles_revision, nil)
     |> assign(:main_form, to_form(ProfilesLive.empty_form(), as: :profile))
     |> assign(:escalation_form, to_form(ProfilesLive.empty_form(), as: :escalation))
     |> assign(:main_dirty?, false)
     |> assign(:escalation_dirty?, false)
     |> assign(:main_requires_save?, false)
     |> assign(:escalation_requires_save?, false)
     |> assign(:main_staged_key, "")
     |> assign(:escalation_staged_key, "")
     |> assign(:main_backup_rows, [])
     |> assign(:escalation_backup_rows, [])
     |> assign(:main_config_open, false)
     |> assign(:main_credential_open, false)
     |> assign(:main_fallback_open, false)
     |> assign(:main_options_open, false)
     |> assign(:main_retry_open, false)
     |> assign(:main_pricing_open, false)
     |> assign(:escalation_config_open, false)
     |> assign(:escalation_credential_open, false)
     |> assign(:escalation_fallback_open, false)
     |> assign(:escalation_options_open, false)
     |> assign(:escalation_pricing_open, false)
     |> assign(:api_inference_types, @api_inference_types)
     |> assign(:model_catalog, nil)
     |> assign(:model_options, [])
     |> assign(:category_name, "LLM")
     |> assign(:field_errors, %{})
     |> assign(:fold_disabled, false)
     |> assign(:pending, %{main: nil, escalation: nil})
     |> assign(:operation_error, nil)
     |> assign(:delete_kind, nil)}
  end

  @impl true
  def update(assigns, socket) do
    profiles = Map.get(assigns, :profiles, [])
    selected_profile_id = Map.get(assigns, :selected_profile_id, "") || ""
    revision = :erlang.phash2(profiles)

    socket =
      socket
      |> assign(assigns)
      |> assign(:id_prefix, Map.get(assigns, :id_prefix, socket.assigns.id_prefix))

    needs_profile_reset? =
      not socket.assigns.initialized? or
        socket.assigns.loaded_profile_id != selected_profile_id or
        (socket.assigns.profiles_revision != revision and not socket.assigns.main_dirty?)

    socket =
      if needs_profile_reset? do
        socket
        |> reset_profile_forms(profiles, selected_profile_id)
        |> assign(:initialized?, true)
        |> assign(:loaded_profile_id, selected_profile_id)
        |> assign(:profiles_revision, revision)
        |> assign(:main_config_open, Map.get(assigns, :config_open, false))
      else
        socket
        |> assign(
          :main_config_open,
          Map.get(assigns, :config_open, socket.assigns.main_config_open)
        )
        |> assign(:profiles_revision, revision)
      end

    socket = if needs_profile_reset?, do: notify_profile_runtime(socket, :main), else: socket

    {:ok,
     socket
     |> assign_fold_state(assigns)
     |> assign(
       :reasoning_effort,
       normalize_reasoning_effort(
         profiles,
         selected_profile_id,
         Map.get(assigns, :reasoning_effort, ProfileDefaults.reasoning_default())
       )
     )
     |> assign(:cache_mode, Map.get(assigns, :cache_mode, ProfileDefaults.cache_mode_default()))}
  end

  @impl true
  def handle_event("toggle-config", _params, socket) do
    if socket.assigns.fold_disabled do
      {:noreply, socket}
    else
      open = not socket.assigns.main_config_open

      socket
      |> assign(:main_config_open, open)
      |> notify_parent({:profile_widget_ui, "llmProfileConfigOpen", open})
      |> noreply()
    end
  end

  def handle_event("select-profile", params, socket) do
    run_params = params["run"] || %{}
    selected_profile_id = String.trim(run_params["selectedProfileId"] || "")
    socket = reset_profile_forms(socket, socket.assigns.profiles, selected_profile_id)
    model_id = socket.assigns.main_form.params["modelId"] || ""

    reasoning_effort =
      normalize_reasoning_effort(
        socket.assigns.profiles,
        selected_profile_id,
        socket.assigns.reasoning_effort
      )

    socket
    |> assign(:loaded_profile_id, selected_profile_id)
    |> assign(:main_dirty?, false)
    |> assign(:reasoning_effort, reasoning_effort)
    |> notify_parent({:profile_widget_selection, selected_profile_id})
    |> notify_parent({:profile_widget_control, "modelId", model_id})
    |> notify_parent({:profile_widget_control, "reasoningEffort", reasoning_effort})
    |> notify_profile_runtime(:main)
    |> noreply()
  end

  def handle_event("workspace-control", %{"run" => params}, socket) do
    notify_workspace_controls(socket, params)
  end

  def handle_event("workspace-control", params, socket) when is_map(params) do
    notify_workspace_controls(socket, Map.get(params, "run", params))
  end

  def handle_event("toggle-cache", _params, socket) do
    next_mode = next_cache_mode(socket.assigns.cache_mode)

    socket
    |> assign(:cache_mode, next_mode)
    |> notify_parent({:profile_widget_control, "cacheMode", next_mode})
    |> noreply()
  end

  def handle_event("toggle-fold", %{"kind" => kind, "fold" => fold}, socket)
      when kind in ["main", "escalation"] do
    key = "#{kind}_#{fold}_open"

    if key in @fold_keys and not socket.assigns.fold_disabled do
      atom_key = String.to_existing_atom(key)
      socket = update(socket, atom_key, &(!&1))

      case fold_ui_name(kind, fold) do
        nil ->
          {:noreply, socket}

        name ->
          {:noreply,
           notify_parent(socket, {:profile_widget_ui, name, Map.get(socket.assigns, atom_key)})}
      end
    else
      {:noreply, socket}
    end
  end

  def handle_event("toggle-escalation-config", _params, socket) do
    if socket.assigns.fold_disabled do
      {:noreply, socket}
    else
      {:noreply, update(socket, :escalation_config_open, &(!&1))}
    end
  end

  def handle_event("profile-draft-change", params, socket) do
    socket =
      cond do
        is_map(params["profile"]) ->
          update_profile_form(socket, :main, params["profile"])

        is_map(params["escalation"]) ->
          update_profile_form(socket, :escalation, params["escalation"])

        true ->
          socket
      end

    {:noreply,
     notify_profile_runtime(
       socket,
       if(is_map(params["escalation"]), do: :escalation, else: :main)
     )}
  end

  def handle_event("add-backup", %{"kind" => kind} = params, socket)
      when kind in ["main", "escalation"] do
    add_backup(socket, String.to_existing_atom(kind), params)
  end

  def handle_event("add-backup-main", params, socket), do: add_backup(socket, :main, params)

  def handle_event("add-backup-escalation", params, socket),
    do: add_backup(socket, :escalation, params)

  def handle_event("edit-backup", %{"index" => index, "profile" => profile}, socket)
      when is_binary(index) do
    value = Map.get(profile, "backupProfiles", "")

    {:noreply, update_backup_at(socket, :main, parse_index(index), value)}
  end

  def handle_event("edit-backup", %{"index" => index, "escalation" => escalation}, socket)
      when is_binary(index) do
    value = Map.get(escalation, "backupProfiles", "")

    {:noreply, update_backup_at(socket, :escalation, parse_index(index), value)}
  end

  def handle_event("remove-backup", %{"kind" => kind, "index" => index}, socket)
      when kind in ["main", "escalation"] do
    {:noreply,
     update_backup(socket, String.to_existing_atom(kind), fn backups ->
       List.delete_at(backups, parse_index(index))
     end)}
  end

  def handle_event(
        "move-backup",
        %{"kind" => kind, "index" => index, "direction" => direction},
        socket
      )
      when kind in ["main", "escalation"] do
    {:noreply,
     update_backup(socket, String.to_existing_atom(kind), fn backups ->
       ProfileWidgetState.move_fallback(backups, parse_index(index), direction)
     end)}
  end

  def handle_event("toggle-credential", %{"kind" => kind}, socket)
      when kind in ["main", "escalation"] do
    if socket.assigns.fold_disabled do
      {:noreply, socket}
    else
      key = String.to_existing_atom("#{kind}_credential_open")
      {:noreply, update(socket, key, &(!&1))}
    end
  end

  def handle_event("stage-key", %{"kind" => kind} = params, socket)
      when kind in ["main", "escalation"] do
    kind_atom = String.to_existing_atom(kind)
    form = form_for(socket, kind_atom)
    key = String.trim(params["apiKey"] || params["api-key"] || form.params["apiKey"] || "")

    if key == "" do
      {:noreply,
       assign(socket, :operation_error, "Enter a replacement API key before staging it.")}
    else
      form = to_form(Map.put(form.params, "apiKey", ""), as: form_as(kind_atom))

      socket =
        socket
        |> assign_form(kind_atom, form)
        |> assign(String.to_existing_atom("#{kind}_staged_key"), key)
        |> assign(String.to_existing_atom("#{kind}_credential_open"), false)
        |> assign(:operation_error, nil)

      {:noreply, notify_profile_runtime(socket, kind_atom)}
    end
  end

  def handle_event("clear-staged-key", %{"kind" => kind}, socket)
      when kind in ["main", "escalation"] do
    kind_atom = String.to_existing_atom(kind)

    {:noreply,
     socket
     |> assign(String.to_existing_atom("#{kind}_staged_key"), "")
     |> update_profile_form(kind_atom, %{"apiKey" => ""})
     |> notify_profile_runtime(kind_atom)}
  end

  def handle_event("cancel-key", %{"kind" => kind}, socket)
      when kind in ["main", "escalation"] do
    kind_atom = String.to_existing_atom(kind)

    {:noreply,
     socket
     |> assign(String.to_existing_atom("#{kind}_staged_key"), "")
     |> assign(String.to_existing_atom("#{kind}_credential_open"), false)
     |> update_profile_form(kind_atom, %{"apiKey" => ""})
     |> notify_profile_runtime(kind_atom)}
  end

  def handle_event("new-profile", %{"kind" => "escalation"}, socket) do
    socket
    |> assign(:escalation_form, to_form(ProfilesLive.empty_form(), as: :escalation))
    |> assign(:escalation_backup_rows, [])
    |> assign(:escalation_dirty?, true)
    |> assign(:escalation_config_open, true)
    |> notify_profile_runtime(:escalation)
    |> noreply()
  end

  def handle_event("new-profile", _params, socket) do
    selected_profile_id = ""

    socket
    |> reset_profile_forms(socket.assigns.profiles, selected_profile_id)
    |> assign(:loaded_profile_id, selected_profile_id)
    |> assign(:main_config_open, true)
    |> assign(:main_dirty?, true)
    |> assign(:main_backup_rows, [])
    |> notify_parent({:profile_widget_selection, selected_profile_id})
    |> notify_profile_runtime(:main)
    |> noreply()
  end

  def handle_event("profile-confirm-delete", %{"kind" => kind}, socket)
      when kind in ["main", "escalation"], do: {:noreply, assign(socket, :delete_kind, kind)}

  def handle_event("profile-cancel-delete", _params, socket),
    do: {:noreply, assign(socket, :delete_kind, nil)}

  def handle_event("profile-delete", %{"kind" => kind}, socket)
      when kind in ["main", "escalation"] do
    if pending_for(socket, kind) != nil do
      {:noreply, socket}
    else
      kind_atom = String.to_existing_atom(kind)
      id = profile_id(form_for(socket, kind_atom))

      if id == "" do
        {:noreply, assign(socket, :delete_kind, nil)}
      else
        reference = System.unique_integer([:positive, :monotonic])
        handle = socket.assigns.session_handle

        {:noreply,
         socket
         |> put_pending(kind, reference)
         |> start_async(
           {:profile_delete, reference, kind},
           Observability.propagate(fn -> HardenAPI.delete_profile(handle, id) end)
         )}
      end
    end
  end

  def handle_event("profile-save", %{"kind" => kind}, socket)
      when kind in ["main", "escalation"] do
    if pending_for(socket, kind) != nil do
      {:noreply, socket}
    else
      kind_atom = String.to_existing_atom(kind)
      params = params_with_staged_key(socket, kind_atom)

      case ProfilesLive.profile_payload(params) do
        {:ok, payload} ->
          reference = System.unique_integer([:positive, :monotonic])
          handle = socket.assigns.session_handle
          id = params["profileId"] || ""

          {:noreply,
           socket
           |> put_pending(kind, reference)
           |> assign_form(kind_atom, to_form(params, as: form_as(kind_atom)))
           |> start_async(
             {:profile_save, reference, kind},
             Observability.propagate(fn -> HardenAPI.save_profile(handle, id, payload) end)
           )}

        {:error, message} ->
          {:noreply, assign(socket, :operation_error, message)}
      end
    end
  end

  def handle_event("profile-save", _params, socket), do: {:noreply, socket}

  def handle_event("profile-refresh", %{"kind" => kind}, socket)
      when kind in ["main", "escalation"] do
    kind_atom = String.to_existing_atom(kind)
    id = profile_id(form_for(socket, kind_atom))

    cond do
      pending_for(socket, kind) != nil ->
        {:noreply, socket}

      id == "" ->
        {:noreply, socket}

      profile_requires_save?(socket, kind_atom) ->
        {:noreply, assign(socket, :operation_error, "Save profile before refreshing models.")}

      true ->
        reference = System.unique_integer([:positive, :monotonic])
        handle = socket.assigns.session_handle

        {:noreply,
         socket
         |> put_pending(kind, reference)
         |> start_async(
           {:profile_refresh, reference, kind},
           Observability.propagate(fn -> HardenAPI.refresh_profile_models(handle, id) end)
         )}
    end
  end

  def handle_event("profile-refresh", _params, socket), do: {:noreply, socket}

  defp notify_workspace_controls(socket, params) when is_map(params) do
    socket =
      Enum.reduce(["reasoningEffort", "cacheMode"], socket, fn key, acc ->
        case params[key] do
          value when is_binary(value) and value != "" ->
            acc =
              case key do
                "reasoningEffort" ->
                  assign(acc, :reasoning_effort, value)

                "cacheMode" ->
                  assign(
                    acc,
                    :cache_mode,
                    if(value == "refresh",
                      do: "refresh",
                      else: ProfileDefaults.cache_mode_default()
                    )
                  )
              end

            notify_parent(acc, {:profile_widget_control, key, value})

          _ ->
            acc
        end
      end)

    {:noreply, socket}
  end

  defp notify_workspace_controls(socket, _params), do: {:noreply, socket}

  defp add_backup(socket, kind, params) do
    id =
      params["id"] || params["fallbackProfile"] ||
        get_in(params, [Atom.to_string(kind), "fallbackProfile"]) || ""

    {:noreply, update_backup(socket, kind, fn backups -> backups ++ [String.trim(id)] end)}
  end

  @impl true
  def handle_async(
        {:profile_save, reference, kind},
        {:ok, {:ok, profile_state, _state}},
        socket
      ) do
    if pending_for(socket, kind) != reference do
      {:noreply, socket}
    else
      id = get_in(profile_state, ["profile", "llmProfile"]) || ""
      profiles = replace_profile(socket.assigns.profiles, profile_state)
      kind_atom = String.to_existing_atom(kind)

      socket =
        socket
        |> clear_pending(kind)
        |> assign(:operation_error, nil)
        |> assign(:delete_kind, nil)
        |> assign(:profiles, profiles)
        |> assign(:profiles_revision, :erlang.phash2(profiles))
        |> assign(:main_dirty?, if(kind == "main", do: false, else: socket.assigns.main_dirty?))
        |> assign(
          :escalation_dirty?,
          if(kind == "escalation", do: false, else: socket.assigns.escalation_dirty?)
        )
        |> assign_form(
          kind_atom,
          to_form(ProfilesLive.profile_form(profile_state), as: form_as(kind_atom))
        )
        |> assign(
          String.to_existing_atom("#{kind}_backup_rows"),
          ProfilesLive.backup_list(get_in(profile_state, ["profile", "backupProfiles"]))
        )
        |> assign(String.to_existing_atom("#{kind}_staged_key"), "")

      socket =
        if kind == "escalation" do
          update_profile_form(socket, :main, %{"escalationProfile" => id})
        else
          socket
        end

      socket = notify_profile_runtime(socket, :main)

      socket
      |> notify_parent(
        {:profile_widget_profiles, profiles,
         if(kind == "main", do: id, else: socket.assigns.selected_profile_id)}
      )
      |> noreply()
    end
  end

  def handle_async(
        {:profile_save, reference, kind},
        {:ok, {:error, %APIError{} = error}},
        socket
      ) do
    if pending_for(socket, kind) != reference do
      {:noreply, socket}
    else
      {:noreply,
       socket
       |> clear_pending(kind)
       |> assign(:operation_error, error.message)}
    end
  end

  def handle_async(
        {:profile_save, reference, kind},
        _result,
        socket
      ) do
    if pending_for(socket, kind) != reference do
      {:noreply, socket}
    else
      {:noreply,
       socket
       |> clear_pending(kind)
       |> assign(:operation_error, "The profile could not be saved.")}
    end
  end

  def handle_async(
        {:profile_refresh, reference, kind},
        {:ok, {:ok, profile_state, _state}},
        socket
      ) do
    if pending_for(socket, kind) != reference do
      {:noreply, socket}
    else
      profiles = replace_profile(socket.assigns.profiles, profile_state)
      kind_atom = String.to_existing_atom(kind)
      id = get_in(profile_state, ["profile", "llmProfile"]) || ""

      socket =
        socket
        |> clear_pending(kind)
        |> assign(:profiles, profiles)
        |> assign(:profiles_revision, :erlang.phash2(profiles))
        |> assign(:operation_error, nil)
        |> assign_form(
          kind_atom,
          to_form(ProfilesLive.profile_form(profile_state), as: form_as(kind_atom))
        )
        |> assign(
          String.to_existing_atom("#{kind}_backup_rows"),
          ProfilesLive.backup_list(get_in(profile_state, ["profile", "backupProfiles"]))
        )
        |> put_flash(:info, "Model catalog refreshed.")

      socket
      |> notify_parent({:profile_widget_profiles, profiles, id})
      |> noreply()
    end
  end

  def handle_async(
        {:profile_refresh, reference, kind},
        {:ok, {:error, %APIError{} = error}},
        socket
      ) do
    if pending_for(socket, kind) != reference do
      {:noreply, socket}
    else
      {:noreply,
       socket
       |> clear_pending(kind)
       |> assign(:operation_error, error.message)}
    end
  end

  def handle_async(
        {:profile_refresh, reference, kind},
        _result,
        socket
      ) do
    if pending_for(socket, kind) != reference do
      {:noreply, socket}
    else
      {:noreply,
       socket
       |> clear_pending(kind)
       |> assign(:operation_error, "The model catalog could not be refreshed.")}
    end
  end

  def handle_async(
        {:profile_delete, reference, kind},
        {:ok, {:ok, _result, _state}},
        socket
      ) do
    if pending_for(socket, kind) != reference do
      {:noreply, socket}
    else
      kind_atom = String.to_existing_atom(kind)
      id = profile_id(form_for(socket, kind_atom))
      profiles = Enum.reject(socket.assigns.profiles, &(profile_id_from_state(&1) == id))

      socket =
        socket
        |> clear_pending(kind)
        |> assign(:delete_kind, nil)
        |> assign(:profiles, profiles)
        |> assign(:profiles_revision, :erlang.phash2(profiles))
        |> assign(:operation_error, nil)

      socket =
        if kind == "main" do
          socket
          |> reset_profile_forms(profiles, "")
          |> assign(:loaded_profile_id, "")
        else
          update_profile_form(socket, :main, %{"escalationProfile" => ""})
        end

      socket
      |> notify_parent(
        {:profile_widget_profiles, profiles,
         if(kind == "main", do: "", else: socket.assigns.selected_profile_id)}
      )
      |> noreply()
    end
  end

  def handle_async(
        {:profile_delete, reference, kind},
        {:ok, {:error, %APIError{} = error}},
        socket
      ) do
    if pending_for(socket, kind) != reference do
      {:noreply, socket}
    else
      {:noreply,
       socket
       |> clear_pending(kind)
       |> assign(:delete_kind, nil)
       |> assign(:operation_error, error.message)}
    end
  end

  def handle_async(
        {:profile_delete, reference, kind},
        _result,
        socket
      ) do
    if pending_for(socket, kind) != reference do
      {:noreply, socket}
    else
      {:noreply,
       socket
       |> clear_pending(kind)
       |> assign(:delete_kind, nil)
       |> assign(:operation_error, "The profile could not be deleted.")}
    end
  end

  def handle_async(_operation, _result, socket), do: {:noreply, socket}

  @impl true
  def render(assigns) do
    ~H"""
    <section id={@id} class="ullm-widget ullm-model-config-widget" aria-label="LLM model config">
      <.profile_row
        category={@category_name}
        profile_input_id={scope_id(@id_prefix, "run_selectedProfileId")}
        profile_name="run[selectedProfileId]"
        profile_value={@selected_profile_id}
        profile_options={profile_combobox_options(@profiles)}
        profile_required={true}
        profile_class="ullm-input ullm-profile-select"
        profile_change="select-profile"
        reasoning_input_id={scope_id(@id_prefix, "workspace-reasoning")}
        reasoning_name="run[reasoningEffort]"
        reasoning_value={@reasoning_effort}
        reasoning_options={reasoning_options(@profiles, @selected_profile_id)}
        reasoning_change="workspace-control"
        cache_input_id={scope_id(@id_prefix, "workspace-cache-toggle")}
        cache_field_id={scope_id(@id_prefix, "workspace-cache")}
        cache_field_name="run[cacheMode]"
        cache_mode={@cache_mode}
        config_id={scope_id(@id_prefix, "model-config-toggle")}
        config_event="toggle-config"
        config_open={@main_config_open}
        model_input_id={scope_id(@id_prefix, "run_modelId")}
        model_input_name="run[modelId]"
        model_value={@model_id}
        target={@myself}
        fold_disabled={@fold_disabled}
      />

      <div
        :if={@operation_error}
        id={scope_id(@id_prefix, "widget-error")}
        role="alert"
        class="ullm-widget-error"
      >
        {@operation_error}
      </div>

      <div
        :if={@main_config_open}
        id={scope_id(@id_prefix, "model-options")}
        class="ullm-profile-config-body ullm-form-grid"
      >
        <.profile_editor
          form={@main_form}
          kind="main"
          id_prefix={scope_id(@id_prefix, "profile")}
          escalation_id_prefix={scope_id(@id_prefix, "escalation")}
          target={@myself}
          profiles={@profiles}
          field_errors={@field_errors}
          api_inference_types={@api_inference_types}
          model_catalog={@model_catalog}
          model_options={@model_options}
          requires_save={@main_requires_save?}
          escalation_requires_save={@escalation_requires_save?}
          backup_rows={@main_backup_rows}
          fold_disabled={@fold_disabled}
          credential_open={@main_credential_open}
          fallback_open={@main_fallback_open}
          options_open={@main_options_open}
          retry_open={@main_retry_open}
          pricing_open={@main_pricing_open}
          staged_key={@main_staged_key}
          cache_mode={@cache_mode}
          config_open={@escalation_config_open}
          include_retry={true}
          bundle_upload={@bundle_upload}
          escalation_bundle_upload={@escalation_bundle_upload}
          widget_id={@id_prefix}
          pending={pending_value(@pending, "main")}
          delete_kind={@delete_kind}
          escalation_form={@escalation_form}
          escalation_config_open={@escalation_config_open}
          escalation_credential_open={@escalation_credential_open}
          escalation_fallback_open={@escalation_fallback_open}
          escalation_options_open={@escalation_options_open}
          escalation_pricing_open={@escalation_pricing_open}
          escalation_staged_key={@escalation_staged_key}
          escalation_backup_rows={@escalation_backup_rows}
        />
      </div>
    </section>
    """
  end

  attr(:id, :string, required: true)
  attr(:name, :string, required: true)
  attr(:value, :any, default: "")
  attr(:options, :list, default: [])
  attr(:allow_custom, :boolean, default: false)
  attr(:required, :boolean, default: false)
  attr(:disabled, :boolean, default: false)
  attr(:aria_label, :string, default: nil)
  attr(:class, :string, default: "ullm-input")
  attr(:placeholder, :string, default: nil)
  attr(:phx_change, :string, default: nil)
  attr(:phx_target, :any, default: nil)
  attr(:index, :any, default: nil)

  def searchable_input(assigns) do
    assigns = assign(assigns, :normalized_options, normalize_combobox_options(assigns.options))

    ~H"""
    <div
      id={"#{@id}-combobox"}
      class="ullm-combobox"
      phx-hook="SearchableCombobox"
      data-allow-custom={to_string(@allow_custom)}
    >
      <input
        id={@id}
        name={@name}
        value={@value || ""}
        type="text"
        class={@class}
        autocomplete="off"
        placeholder={@placeholder}
        required={@required}
        disabled={@disabled}
        aria-label={@aria_label}
        aria-autocomplete="list"
        aria-controls={"#{@id}-options"}
        aria-expanded="false"
        role="combobox"
        phx-change={@phx_change}
        phx-target={@phx_target}
        phx-value-index={@index}
      />
      <div
        id={"#{@id}-options"}
        class="ullm-combobox-options"
        role="listbox"
        hidden
      >
        <button
          :for={option <- @normalized_options}
          type="button"
          role="option"
          class="ullm-combobox-option"
          data-value={option.value}
          data-search={option.search}
          aria-selected={to_string(option.value == to_string(@value || ""))}
        >{option.label}</button>
        <span class="ullm-combobox-empty" hidden>No matching options</span>
      </div>
    </div>
    """
  end

  attr(:category, :string, required: true)
  attr(:profile_input_id, :string, required: true)
  attr(:profile_name, :string, required: true)
  attr(:profile_value, :any, default: "")
  attr(:profile_options, :list, default: [])
  attr(:profile_required, :boolean, default: false)
  attr(:profile_class, :string, default: "ullm-input")
  attr(:profile_change, :string, default: "profile-draft-change")
  attr(:profile_placeholder, :string, default: nil)
  attr(:reasoning_input_id, :string, required: true)
  attr(:reasoning_name, :string, required: true)
  attr(:reasoning_value, :any, default: "")
  attr(:reasoning_options, :list, default: [])
  attr(:reasoning_change, :string, default: "profile-draft-change")
  attr(:cache_input_id, :string, required: true)
  attr(:cache_mode, :string, required: true)
  attr(:cache_field_id, :string, default: nil)
  attr(:cache_field_name, :string, default: nil)
  attr(:config_id, :string, required: true)
  attr(:config_event, :string, required: true)
  attr(:config_open, :boolean, default: false)
  attr(:model_input_id, :string, default: nil)
  attr(:model_input_name, :string, default: nil)
  attr(:model_value, :any, default: "")
  attr(:target, :any, required: true)
  attr(:fold_disabled, :boolean, default: false)
  attr(:row_class, :string, default: "")

  def profile_row(assigns) do
    ~H"""
    <div class={"ullm-profile-row #{@row_class}"}>
      <span class="ullm-profile-category" title={@category}>{@category}</span>
      <div class="ullm-profile-picker">
        <label for={@profile_input_id} class="ullm-profile-label">
          <span aria-hidden="true">🤖</span><span class="ullm-sr-only">LLM Profile</span>
        </label>
        <.searchable_input
          id={@profile_input_id}
          name={@profile_name}
          value={@profile_value}
          options={@profile_options}
          allow_custom
          required={@profile_required}
          placeholder={@profile_placeholder || ProfileDefaults.profile_placeholder()}
          aria_label="LLM Profile"
          class={@profile_class}
          phx_change={@profile_change}
          phx_target={@target}
        />
      </div>
      <div class="ullm-reasoning-field">
        <label for={@reasoning_input_id} class="ullm-profile-label">
          <span aria-hidden="true">🧠</span><span class="ullm-sr-only">Reasoning</span>
        </label>
        <select
          id={@reasoning_input_id}
          name={@reasoning_name}
          aria-label="Reasoning"
          class="ullm-input ullm-compact-select"
          phx-change={@reasoning_change}
          phx-target={@target}
          disabled={@reasoning_options == []}
        >
          <option :if={@reasoning_options == []} value="" selected>—</option>
          <option
            :for={{value, label} <- @reasoning_options}
            value={value}
            selected={@reasoning_value == value}
          >
            {label}
          </option>
        </select>
      </div>
      <button
        id={@cache_input_id}
        type="button"
        class="ullm-btn ullm-profile-cache-toggle"
        phx-click="toggle-cache"
        phx-target={@target}
        aria-label={cache_label(@cache_mode)}
        aria-pressed={to_string(@cache_mode == "cache")}
        data-cache-mode={@cache_mode}
        title={cache_title(@cache_mode)}
      ><span aria-hidden="true">{cache_icon(@cache_mode)}</span></button>
      <select
        :if={@cache_field_id}
        id={@cache_field_id}
        name={@cache_field_name}
        class="ullm-sr-only"
        aria-label="Cache mode"
        phx-change="workspace-control"
        phx-target={@target}
      >
        <option value="cache" selected={@cache_mode != "refresh"}>cache</option>
        <option value="refresh" selected={@cache_mode == "refresh"}>refresh</option>
      </select>
      <input
        :if={@model_input_id}
        id={@model_input_id}
        name={@model_input_name}
        value={@model_value}
        class="ullm-sr-only"
        autocomplete="off"
      />
      <button
        id={@config_id}
        type="button"
        class="ullm-btn ullm-profile-config-toggle"
        phx-click={@config_event}
        phx-target={@target}
        disabled={@fold_disabled}
        aria-expanded={to_string(@config_open)}
        aria-label="Profile config"
      >⚙</button>
    </div>
    """
  end

  attr(:form, :any, required: true)
  attr(:kind, :string, required: true)
  attr(:id_prefix, :string, required: true)
  attr(:target, :any, required: true)
  attr(:profiles, :list, required: true)
  attr(:field_errors, :map, default: %{})
  attr(:api_inference_types, :list, default: @api_inference_types)
  attr(:model_catalog, :list, default: nil)
  attr(:model_options, :list, default: [])
  attr(:requires_save, :boolean, default: false)
  attr(:escalation_requires_save, :boolean, default: false)
  attr(:backup_rows, :list, default: [])
  attr(:escalation_backup_rows, :list, default: [])
  attr(:fold_disabled, :boolean, default: false)
  attr(:credential_open, :boolean, default: false)
  attr(:fallback_open, :boolean, default: false)
  attr(:options_open, :boolean, default: false)
  attr(:retry_open, :boolean, default: false)
  attr(:pricing_open, :boolean, default: false)
  attr(:staged_key, :string, default: "")
  attr(:cache_mode, :string, default: "cache")
  attr(:config_open, :boolean, default: false)
  attr(:include_retry, :boolean, default: true)
  attr(:bundle_upload, :any, default: nil)
  attr(:escalation_bundle_upload, :any, default: nil)
  attr(:widget_id, :string, default: "")
  attr(:pending, :any, default: nil)
  attr(:delete_kind, :any, default: nil)
  attr(:escalation_form, :any, default: nil)
  attr(:escalation_config_open, :boolean, default: false)
  attr(:escalation_credential_open, :boolean, default: false)
  attr(:escalation_fallback_open, :boolean, default: false)
  attr(:escalation_options_open, :boolean, default: false)
  attr(:escalation_pricing_open, :boolean, default: false)
  attr(:escalation_staged_key, :string, default: "")
  attr(:escalation_id_prefix, :string, default: "escalation")

  def profile_editor(assigns) do
    ~H"""
    <div id={"#{@id_prefix}-config-fields"} class="ullm-form-grid">
      <div class="ullm-options-grid">
        <div class="ullm-field">
          <label for={field_id(@id_prefix, @form[:apiInferenceType].id)}>API Inference Type</label>
          <.searchable_input
            id={field_id(@id_prefix, @form[:apiInferenceType].id)}
            name={@form[:apiInferenceType].name}
            value={@form[:apiInferenceType].value}
            options={api_inference_combobox_options(@api_inference_types)}
            aria_label="API Inference Type"
            class="ullm-input"
            phx_change="profile-draft-change"
            phx_target={@target}
          />
        </div>
        <div class="ullm-field">
          <label for={field_id(@id_prefix, @form[:baseUrl].id)}>Base URL</label>
          <.searchable_input
            id={field_id(@id_prefix, @form[:baseUrl].id)}
            name={@form[:baseUrl].name}
            value={@form[:baseUrl].value}
            options={base_url_combobox_options(@profiles, @form[:baseUrl].value)}
            allow_custom
            required
            placeholder={ProfileDefaults.base_url_placeholder()}
            aria_label="Base URL"
            class="ullm-input ullm-input-mono"
            phx_change="profile-draft-change"
            phx_target={@target}
          />
        </div>
      </div>

      <section class="ullm-credential-block">
        <div class="ullm-credential-row">
          <div class="ullm-credential-status">
            <span
              class={"ullm-key-dot #{if credential_available?(@form, @staged_key), do: "ullm-key-dot-on"}"}
              aria-hidden="true"
            ></span>
            <div>
              <div class="ullm-credential-label">Endpoint credential</div>
              <div class="ullm-credential-copy">{credential_status(@form, @staged_key)}</div>
            </div>
          </div>
          <button
            type="button"
            id={"#{@id_prefix}-credential-toggle"}
            class="ullm-btn ullm-btn-tiny"
            phx-click="toggle-credential"
            phx-value-kind={@kind}
            phx-target={@target}
            disabled={@fold_disabled}
            aria-expanded={to_string(@credential_open)}
          >{if @credential_open,
            do: "Hide key",
            else: if(credential_available?(@form, @staged_key), do: "Replace key", else: "Set key")}</button>
        </div>
        <div
          :if={@credential_open}
          id={"#{@id_prefix}-credential-drawer"}
          class="ullm-credential-drawer"
          phx-hook="SecretStager"
        >
          <.input
            field={@form[:apiKey]}
            id={field_id(@id_prefix, @form[:apiKey].id)}
            type="password"
            label="Replacement API Key"
            autocomplete="new-password"
            class="ullm-input ullm-input-mono"
            data-secret-input
          />
          <div class="ullm-button-row ullm-button-row-end">
            <button
              :if={@staged_key != ""}
              id={"#{@id_prefix}-clear-staged-key"}
              type="button"
              class="ullm-btn ullm-btn-danger"
              phx-click="clear-staged-key"
              phx-value-kind={@kind}
              phx-target={@target}
            >Clear staged key</button>
            <button
              id={"#{@id_prefix}-cancel-key"}
              type="button"
              class="ullm-btn"
              phx-click="cancel-key"
              phx-value-kind={@kind}
              phx-target={@target}
            >Cancel</button>
            <button
              id={"#{@id_prefix}-stage-key"}
              type="button"
              class="ullm-btn ullm-btn-primary"
              phx-click="stage-key"
              phx-value-kind={@kind}
              phx-target={@target}
              data-stage-key
            >Stage key</button>
          </div>
        </div>
      </section>

      <div class="ullm-model-slot-row">
        <button
          type="button"
          id={"#{@id_prefix}-refresh-models"}
          class="ullm-btn ullm-model-refresh-button"
          phx-click="profile-refresh"
          phx-value-kind={@kind}
          phx-target={@target}
          disabled={@pending != nil or profile_id(@form) == "" or @requires_save}
        >Refresh Models</button>
        <div class="ullm-model-slot-field">
          <label for={field_id(@id_prefix, @form[:modelId].id)}>Model ID</label>
          <.searchable_input
            id={field_id(@id_prefix, @form[:modelId].id)}
            name={@form[:modelId].name}
            value={@form[:modelId].value}
            options={
              model_combobox_options(
                models_for(
                  @profiles,
                  profile_id(@form),
                  @model_options,
                  @model_catalog,
                  @form[:modelId].value
                )
              )
            }
            allow_custom
            placeholder={ProfileDefaults.model_placeholder(@kind)}
            aria_label="Model ID"
            class="ullm-input ullm-input-mono"
            phx_change="profile-draft-change"
            phx_target={@target}
          />
          <p class="ullm-field-help">
            {length(
              models_for(
                @profiles,
                profile_id(@form),
                @model_options,
                @model_catalog,
                @form[:modelId].value
              )
            )} options
          </p>
          <p
            :if={@requires_save}
            id={"#{@id_prefix}-save-required"}
            class="ullm-field-error"
            role="alert"
          >
            Save profile before refreshing models.
          </p>
        </div>
      </div>

      <section class="ullm-backup-profiles">
        <div class="ullm-backup-profiles-header">
          <div>
            <div class="ullm-backup-profiles-title">Fallback LLMs</div>
            <p class="ullm-field-help">Fallback order is explicit and backend-validated.</p>
          </div>
          <button
            type="button"
            id={"#{@id_prefix}-fallback-toggle"}
            class="ullm-btn ullm-btn-tiny"
            phx-click="add-backup"
            phx-value-kind={@kind}
            phx-value-id=""
            phx-target={@target}
            disabled={@fold_disabled}
          >+ Add Fallback LLM</button>
        </div>
        <div id={"#{@id_prefix}-fallback-options"} class="ullm-backup-profile-row">
          <div id={"#{@id_prefix}-fallback-list"} class="ullm-backup-profile-list">
            <div
              :for={{backup, index} <- Enum.with_index(@backup_rows)}
              class="ullm-backup-profile-item"
            >
              <.searchable_input
                id={"#{@id_prefix}-fallback-#{index}"}
                name={"#{@form.name}[backupProfiles]"}
                value={backup}
                options={
                  profile_combobox_options(available_backup_profiles(@profiles, profile_id(@form)))
                }
                allow_custom
                aria_label={"Fallback LLM #{index + 1}"}
                class="ullm-input ullm-input-mono ullm-backup-profile-input"
                phx_change="edit-backup"
                phx_target={@target}
                index={index}
              />
              <span class="ullm-backup-profile-actions">
                <button
                  type="button"
                  id={"#{@id_prefix}-fallback-#{index}-up"}
                  class="ullm-btn ullm-btn-tiny"
                  phx-click="move-backup"
                  phx-value-kind={@kind}
                  phx-value-index={index}
                  phx-value-direction="up"
                  phx-target={@target}
                  disabled={@fold_disabled or index == 0}
                  aria-label={"Move #{backup} up"}
                >Up</button>
                <button
                  type="button"
                  id={"#{@id_prefix}-fallback-#{index}-down"}
                  class="ullm-btn ullm-btn-tiny"
                  phx-click="move-backup"
                  phx-value-kind={@kind}
                  phx-value-index={index}
                  phx-value-direction="down"
                  phx-target={@target}
                  disabled={@fold_disabled or index == length(@backup_rows) - 1}
                  aria-label={"Move #{backup} down"}
                >Down</button>
                <button
                  type="button"
                  class="ullm-btn ullm-btn-tiny ullm-btn-danger"
                  phx-click="remove-backup"
                  phx-value-kind={@kind}
                  phx-value-index={index}
                  phx-target={@target}
                  aria-label={"Remove #{backup}"}
                >Remove</button>
              </span>
            </div>
          </div>
        </div>
      </section>

      <div
        :if={new_profile_fields_visible?(@kind, @form, @profiles)}
        class="ullm-new-profile-fields ullm-options-grid"
      >
        <.input
          field={@form[:profileId]}
          id={field_id(@id_prefix, @form[:profileId].id)}
          label="Profile"
          required
          class="ullm-input"
          phx-change="profile-draft-change"
          phx-target={@target}
        />
        <.input
          field={@form[:provider]}
          id={field_id(@id_prefix, @form[:provider].id)}
          label="Provider family"
          required
          class="ullm-input"
          phx-change="profile-draft-change"
          phx-target={@target}
        />
        <.input
          field={@form[:supportsTemperature]}
          id={field_id(@id_prefix, @form[:supportsTemperature].id)}
          type="checkbox"
          label="Supports temperature"
          phx-change="profile-draft-change"
          phx-target={@target}
        />
        <.input
          field={@form[:supportsContractedStructuredOutput]}
          id={field_id(@id_prefix, @form[:supportsContractedStructuredOutput].id)}
          type="checkbox"
          label="Supports contracted structured output"
          phx-change="profile-draft-change"
          phx-target={@target}
        />
      </div>

      <div class="ullm-options-fold">
        <button
          id={"#{@id_prefix}-options-toggle"}
          type="button"
          class="ullm-btn ullm-options-summary"
          phx-click="toggle-fold"
          phx-value-kind={@kind}
          phx-value-fold="options"
          phx-target={@target}
          disabled={@fold_disabled}
          aria-expanded={to_string(@options_open)}
        >Options</button>
        <div :if={@options_open} id={"#{@id_prefix}-options"} class="ullm-options-body">
          <div class="ullm-options-grid">
            <.input
              field={@form[:maxTokens]}
              id={field_id(@id_prefix, @form[:maxTokens].id)}
              type="number"
              label="Max Output Tokens"
              min="0"
              placeholder={ProfileDefaults.option_placeholder("maxTokens")}
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:temperature]}
              id={field_id(@id_prefix, @form[:temperature].id)}
              type="number"
              label="Temperature"
              min="0"
              step="any"
              placeholder={ProfileDefaults.option_placeholder("temperature")}
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:topP]}
              id={field_id(@id_prefix, @form[:topP].id)}
              type="number"
              label="Top P"
              min="0"
              step="any"
              placeholder={ProfileDefaults.option_placeholder("topP")}
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:topK]}
              id={field_id(@id_prefix, @form[:topK].id)}
              type="number"
              label="Top K"
              min="0"
              placeholder={ProfileDefaults.option_placeholder("topK")}
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
          </div>
          <.input
            field={@form[:stopSequences]}
            id={field_id(@id_prefix, @form[:stopSequences].id)}
            type="textarea"
            label="Stop Sequences"
            placeholder={ProfileDefaults.option_placeholder("stopSequences")}
            rows="2"
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:defaultOptionsJson]}
            id={field_id(@id_prefix, @form[:defaultOptionsJson].id)}
            type="textarea"
            label="Default Options JSON"
            placeholder={ProfileDefaults.option_placeholder("defaultOptionsJson")}
            rows="5"
            class="ullm-input ullm-input-mono"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.field_error message={options_error(@form[:defaultOptionsJson].value, @field_errors)} />
        </div>
      </div>

      <section :if={@include_retry} class="ullm-options-fold">
        <button
          id={"#{@id_prefix}-retry-toggle"}
          type="button"
          class="ullm-btn ullm-options-summary"
          phx-click="toggle-fold"
          phx-value-kind={@kind}
          phx-value-fold="retry"
          phx-target={@target}
          disabled={@fold_disabled}
          aria-expanded={to_string(@retry_open)}
        >Retries &amp; Repair</button>
        <div :if={@retry_open} id={"#{@id_prefix}-retry-repair"} class="ullm-options-body">
          <div class="ullm-checkbox-grid">
            <.input
              field={@form[:structuredRepairRetryEnabled]}
              id={field_id(@id_prefix, @form[:structuredRepairRetryEnabled].id)}
              type="checkbox"
              label="Structured Repair"
              info={ProfileDefaults.field_info("structuredRepairRetry")}
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:enableRetryOn429]}
              id={field_id(@id_prefix, @form[:enableRetryOn429].id)}
              type="checkbox"
              label="Rate Limits"
              info={ProfileDefaults.field_info("enableRetryOn429")}
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:enableRetryOn5xx]}
              id={field_id(@id_prefix, @form[:enableRetryOn5xx].id)}
              type="checkbox"
              label="Server Errors"
              info={ProfileDefaults.field_info("enableRetryOn5xx")}
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:enableRetryOnNetworkError]}
              id={field_id(@id_prefix, @form[:enableRetryOnNetworkError].id)}
              type="checkbox"
              label="Network Errors"
              info={ProfileDefaults.field_info("enableRetryOnNetworkError")}
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:enableRetryOnParseError]}
              id={field_id(@id_prefix, @form[:enableRetryOnParseError].id)}
              type="checkbox"
              label="Parse / Schema Errors"
              disabled={truthy?(@form[:structuredRepairRetryEnabled].value)}
              info={ProfileDefaults.field_info("enableRetryOnParseError")}
              phx-change="profile-draft-change"
              phx-target={@target}
            />
          </div>
          <div class="ullm-options-grid">
            <.input
              field={@form[:retryMaxAttempts]}
              id={field_id(@id_prefix, @form[:retryMaxAttempts].id)}
              type="number"
              label="Max Attempts"
              min="1"
              max="10"
              placeholder={ProfileDefaults.retry_placeholder("retryMaxAttempts")}
              info={ProfileDefaults.field_info("maxAttempts")}
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:retryBaseDelayMs]}
              id={field_id(@id_prefix, @form[:retryBaseDelayMs].id)}
              type="number"
              label="Base Delay Ms"
              min="0"
              placeholder={ProfileDefaults.retry_placeholder("retryBaseDelayMs")}
              info={ProfileDefaults.field_info("baseDelayMs")}
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:retryMaxDelayMs]}
              id={field_id(@id_prefix, @form[:retryMaxDelayMs].id)}
              type="number"
              label="Max Delay Ms"
              min="0"
              placeholder={ProfileDefaults.retry_placeholder("retryMaxDelayMs")}
              info={ProfileDefaults.field_info("maxDelayMs")}
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:escalationAttempt]}
              id={field_id(@id_prefix, @form[:escalationAttempt].id)}
              type="number"
              label="Starting Attempt"
              min="2"
              max="10"
              placeholder={ProfileDefaults.retry_placeholder("escalationAttempt")}
              info={ProfileDefaults.field_info("escalationAttempt")}
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
          </div>
          <div class="ullm-escalation-profile-editor">
            <.profile_row
              category="Escalation"
              row_class="ullm-escalation-profile-row"
              profile_input_id={"#{@id_prefix}-escalation-profile"}
              profile_name={"#{@form.name}[escalationProfile]"}
              profile_value={@form[:escalationProfile].value}
              profile_options={profile_combobox_options(@profiles)}
              profile_change="profile-draft-change"
              reasoning_input_id={"#{@id_prefix}-escalation-reasoning"}
              reasoning_name={"#{@form.name}[escalationReasoning]"}
              reasoning_value={@form[:escalationReasoning].value}
              reasoning_options={escalation_reasoning_options(@profiles, @form)}
              cache_input_id={"#{@id_prefix}-escalation-cache-toggle"}
              cache_mode={@cache_mode}
              config_id={"#{@id_prefix}-escalation-config-toggle"}
              config_event="toggle-escalation-config"
              config_open={@config_open}
              target={@target}
              fold_disabled={@fold_disabled}
            />
            <div
              :if={@config_open}
              id={"#{@id_prefix}-escalation-config"}
              class="ullm-profile-config-body ullm-form-grid"
            >
              <.profile_editor
                form={@escalation_form}
                kind="escalation"
                id_prefix={@escalation_id_prefix}
                target={@target}
                profiles={@profiles}
                model_catalog={@model_catalog}
                field_errors={@field_errors}
                credential_open={@escalation_credential_open}
                fallback_open={@escalation_fallback_open}
                options_open={@escalation_options_open}
                retry_open={false}
                pricing_open={@escalation_pricing_open}
                staged_key={@escalation_staged_key}
                backup_rows={@escalation_backup_rows}
                model_options={@model_options}
                requires_save={@escalation_requires_save}
                fold_disabled={@fold_disabled}
                cache_mode={@cache_mode}
                config_open={false}
                include_retry={false}
                bundle_upload={@escalation_bundle_upload}
                widget_id={@id_prefix}
                pending={pending_value(@pending, "escalation")}
                delete_kind={@delete_kind}
              />
            </div>
          </div>
        </div>
      </section>

      <div class="ullm-options-fold ullm-pricing-section">
        <button
          id={"#{@id_prefix}-pricing-toggle"}
          type="button"
          class="ullm-btn ullm-options-summary"
          phx-click="toggle-fold"
          phx-value-kind={@kind}
          phx-value-fold="pricing"
          phx-target={@target}
          disabled={@fold_disabled}
          aria-expanded={to_string(@pricing_open)}
        >Pricing</button>
        <div
          :if={@pricing_open}
          id={"#{@id_prefix}-pricing"}
          class="ullm-options-body ullm-options-grid"
        >
          <.input
            field={@form[:pricingInput]}
            id={field_id(@id_prefix, @form[:pricingInput].id)}
            type="number"
            label="Input $/1M tokens"
            min="0"
            step="any"
            placeholder={ProfileDefaults.pricing_placeholder()}
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:pricingOutput]}
            id={field_id(@id_prefix, @form[:pricingOutput].id)}
            type="number"
            label="Output $/1M tokens"
            min="0"
            step="any"
            placeholder={ProfileDefaults.pricing_placeholder()}
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:pricingCacheRead]}
            id={field_id(@id_prefix, @form[:pricingCacheRead].id)}
            type="number"
            label="Cache read $/1M tokens"
            min="0"
            step="any"
            placeholder={ProfileDefaults.pricing_placeholder()}
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:pricingCacheWrite]}
            id={field_id(@id_prefix, @form[:pricingCacheWrite].id)}
            type="number"
            label="Cache write $/1M tokens"
            min="0"
            step="any"
            placeholder={ProfileDefaults.pricing_placeholder()}
            info={ProfileDefaults.field_info("pricingCacheWrite")}
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:pricingReasoning]}
            id={field_id(@id_prefix, @form[:pricingReasoning].id)}
            type="number"
            label="Reasoning output $/1M tokens"
            min="0"
            step="any"
            placeholder={ProfileDefaults.pricing_placeholder()}
            info={ProfileDefaults.field_info("pricingReasoning")}
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
        </div>
      </div>

      <div :if={@kind == "main"} class="ullm-profile-actions ullm-button-row">
        <button
          id={scope_id(@id_prefix, "new")}
          type="button"
          class="ullm-btn"
          phx-click="new-profile"
          phx-target={@target}
        >+ New</button>
        <label id={scope_id(@id_prefix, "bundle-file")} class="ullm-btn ullm-file-button">
          Import Bundle
          <.live_file_input
            :if={@bundle_upload}
            upload={@bundle_upload}
            phx-change="import-bundle"
            phx-value-kind={@kind}
            phx-value-widget={@widget_id}
          />
        </label>
        <a id={scope_id(@id_prefix, "export-bundle")} href={~p"/profiles/bundle"} class="ullm-btn">Export Bundle</a>
        <button
          id={scope_id(@id_prefix, "save")}
          type="button"
          class="ullm-btn ullm-btn-primary"
          phx-click="profile-save"
          phx-value-kind={@kind}
          phx-target={@target}
          disabled={
            @pending != nil or profile_id(@form) == "" or
              not ProfilesLive.options_valid?(@form[:defaultOptionsJson].value)
          }
        >{if @pending, do: "Saving…", else: "Save Profile"}</button>
        <button
          :if={profile_id(@form) != ""}
          id={scope_id(@id_prefix, "delete")}
          type="button"
          class="ullm-btn ullm-btn-danger"
          phx-click="profile-confirm-delete"
          phx-value-kind={@kind}
          phx-target={@target}
        >Delete Profile</button>
      </div>
      <div :if={@kind == "escalation"} class="ullm-profile-actions ullm-button-row">
        <button
          id={scope_id(@id_prefix, "new")}
          type="button"
          class="ullm-btn"
          phx-click="new-profile"
          phx-value-kind="escalation"
          phx-target={@target}
        >+ New</button>
        <label id={scope_id(@id_prefix, "bundle-file")} class="ullm-btn ullm-file-button">
          Import Bundle
          <.live_file_input
            :if={@bundle_upload}
            upload={@bundle_upload}
            phx-change="import-bundle"
            phx-value-kind={@kind}
            phx-value-widget={@widget_id}
          />
        </label>
        <a id={scope_id(@id_prefix, "export-bundle")} href={~p"/profiles/bundle"} class="ullm-btn">Export Bundle</a>
        <button
          id={scope_id(@id_prefix, "save")}
          type="button"
          class="ullm-btn ullm-btn-primary"
          phx-click="profile-save"
          phx-value-kind={@kind}
          phx-target={@target}
          disabled={
            @pending != nil or profile_id(@form) == "" or
              not ProfilesLive.options_valid?(@form[:defaultOptionsJson].value)
          }
        >{if @pending, do: "Saving…", else: "Save Profile"}</button>
        <button
          :if={profile_id(@form) != ""}
          id={scope_id(@id_prefix, "delete")}
          type="button"
          class="ullm-btn ullm-btn-danger"
          phx-click="profile-confirm-delete"
          phx-value-kind={@kind}
          phx-target={@target}
        >Delete Profile</button>
      </div>
      <div
        :if={@delete_kind == @kind}
        id={"#{@id_prefix}-delete-confirmation"}
        class="ullm-delete-confirm"
        role="alert"
      >
        <span>Delete <strong>{profile_id(@form)}</strong>?</span>
        <button
          id={"#{@id_prefix}-delete-cancel"}
          type="button"
          class="ullm-btn"
          phx-click="profile-cancel-delete"
          phx-target={@target}
        >Cancel</button>
        <button
          id={"#{@id_prefix}-delete-confirm"}
          type="button"
          class="ullm-btn ullm-btn-danger"
          phx-click="profile-delete"
          phx-value-kind={@kind}
          phx-target={@target}
        >Confirm delete</button>
      </div>
    </div>
    """
  end

  defp reset_profile_forms(socket, profiles, selected_profile_id) do
    main_form = profile_form_for(profiles, selected_profile_id, :profile)
    escalation_id = escalation_profile_id(main_form, selected_profile_id, profiles)
    main_form = put_escalation_profile(main_form, escalation_id)
    escalation_form = profile_form_for(profiles, escalation_id, :escalation)

    socket
    |> assign(:selected_profile_id, selected_profile_id)
    |> assign(:main_form, main_form)
    |> assign(:escalation_form, escalation_form)
    |> assign(:main_backup_rows, ProfilesLive.backup_list(main_form.params["backupProfiles"]))
    |> assign(
      :escalation_backup_rows,
      ProfilesLive.backup_list(escalation_form.params["backupProfiles"])
    )
    |> assign(:main_staged_key, "")
    |> assign(:escalation_staged_key, "")
    |> assign(:main_requires_save?, false)
    |> assign(:escalation_requires_save?, false)
  end

  defp update_profile_form(socket, :main, incoming) do
    current = socket.assigns.main_form.params || %{}

    params =
      current
      |> Map.merge(incoming)
      |> synchronize_profile_options(incoming)

    form = to_form(params, as: :profile)

    escalation_id =
      escalation_profile_id(
        to_form(params, as: :profile),
        socket.assigns.selected_profile_id,
        socket.assigns.profiles
      )

    form = put_escalation_profile(form, escalation_id)

    socket =
      socket
      |> assign(:main_form, form)
      |> assign(:main_dirty?, true)
      |> maybe_update_escalation_form(escalation_id)

    if Map.has_key?(incoming, "modelId") do
      notify_parent(socket, {:profile_widget_control, "modelId", params["modelId"] || ""})
    else
      socket
    end
  end

  defp update_profile_form(socket, :escalation, incoming) do
    current = socket.assigns.escalation_form.params || %{}

    params =
      current
      |> Map.merge(incoming)
      |> synchronize_profile_options(incoming)

    socket
    |> assign(:escalation_form, to_form(params, as: :escalation))
    |> assign(:escalation_dirty?, true)
  end

  defp synchronize_profile_options(params, incoming) do
    case changed_field(incoming) do
      "defaultOptionsJson" -> sync_option_fields_from_json(params)
      _ -> sync_default_options_from_fields(params, incoming)
    end
  end

  defp changed_field(%{"_target" => target}) when is_list(target), do: List.last(target)
  defp changed_field(%{"_target" => target}) when is_binary(target), do: target

  defp changed_field(incoming) do
    if map_size(incoming) == 1 and Map.has_key?(incoming, "defaultOptionsJson"),
      do: "defaultOptionsJson",
      else: nil
  end

  defp sync_default_options_from_fields(params, incoming) do
    fields = ~w(
      maxTokens temperature topP topK stopSequences
      structuredRepairRetryEnabled enableRetryOn429 enableRetryOn5xx
      enableRetryOnNetworkError enableRetryOnParseError retryMaxAttempts
      retryBaseDelayMs retryMaxDelayMs escalationAttempt escalationProfile
      escalationReasoning
    )

    if Enum.any?(fields, &Map.has_key?(incoming, &1)) do
      options = decode_options(params["defaultOptionsJson"])
      options = ProfileWidgetState.patch_options(options, params)
      Map.put(params, "defaultOptionsJson", Jason.encode!(options, pretty: true))
    else
      params
    end
  end

  defp retry_option(options, retry, key, default),
    do: Map.get(options, key, Map.get(retry, key, default))

  defp sync_option_fields_from_json(params) do
    options = decode_options(params["defaultOptionsJson"])
    retry = options["structuredRepairRetry"]
    retry_map = if is_map(retry), do: retry, else: %{}
    escalation = if is_map(retry_map["escalation"]), do: retry_map["escalation"], else: %{}

    params
    |> Map.put(
      "maxTokens",
      option_text(options["max_tokens"] || ProfileDefaults.default_options()["max_tokens"])
    )
    |> Map.put("temperature", option_text(options["temperature"]))
    |> Map.put("topP", option_text(options["top_p"] || options["topP"]))
    |> Map.put("topK", option_text(options["top_k"] || options["topK"]))
    |> Map.put("stopSequences", stop_text(options["stop"]))
    |> Map.put(
      "structuredRepairRetryEnabled",
      to_string(retry != false)
    )
    |> Map.put(
      "enableRetryOn429",
      to_string(retry_option(options, retry_map, "enableRetryOn429", true))
    )
    |> Map.put(
      "enableRetryOn5xx",
      to_string(retry_option(options, retry_map, "enableRetryOn5xx", true))
    )
    |> Map.put(
      "enableRetryOnNetworkError",
      to_string(retry_option(options, retry_map, "enableRetryOnNetworkError", true))
    )
    |> Map.put(
      "enableRetryOnParseError",
      to_string(retry_option(options, retry_map, "enableRetryOnParseError", true))
    )
    |> Map.put(
      "retryMaxAttempts",
      option_text(
        options["maxAttempts"] ||
          retry_map["maxAttempts"] || ProfileDefaults.retry_default("maxAttempts")
      )
    )
    |> Map.put(
      "retryBaseDelayMs",
      option_text(
        options["baseDelayMs"] ||
          retry_map["baseDelayMs"] || ProfileDefaults.retry_default("baseDelayMs")
      )
    )
    |> Map.put(
      "retryMaxDelayMs",
      option_text(
        options["maxDelayMs"] ||
          retry_map["maxDelayMs"] || ProfileDefaults.retry_default("maxDelayMs")
      )
    )
    |> Map.put(
      "escalationAttempt",
      option_text(escalation["attempt"] || ProfileDefaults.retry_default("escalationAttempt"))
    )
    |> Map.put(
      "escalationProfile",
      escalation["llmProfile"] || ProfileDefaults.default_escalation_profile_id()
    )
    |> Map.put(
      "escalationReasoning",
      escalation["reasoningEffort"] || ProfileDefaults.repair_reasoning_default()
    )
  end

  defp decode_options(value) do
    case Jason.decode(value || "{}") do
      {:ok, options} when is_map(options) -> options
      _ -> %{}
    end
  end

  defp option_text(nil), do: ""
  defp option_text(value), do: to_string(value)

  defp stop_text(value) when is_list(value), do: Enum.join(value, "\n")
  defp stop_text(_value), do: ""

  defp maybe_update_escalation_form(socket, ""), do: socket

  defp maybe_update_escalation_form(socket, id) do
    if profile_id(socket.assigns.escalation_form) != id and not socket.assigns.escalation_dirty? do
      assign(socket, :escalation_form, profile_form_for(socket.assigns.profiles, id, :escalation))
    else
      socket
    end
  end

  defp update_backup(socket, kind, update) do
    form = form_for(socket, kind)
    backups = Map.get(socket.assigns, String.to_existing_atom("#{kind}_backup_rows"), [])

    next =
      backups |> update.() |> Enum.map(&String.trim(to_string(&1))) |> Enum.uniq()

    socket =
      socket
      |> assign(String.to_existing_atom("#{kind}_backup_rows"), next)
      |> assign_form(
        kind,
        to_form(
          Map.put(
            form.params,
            "backupProfiles",
            Enum.reject(next, &(&1 == "")) |> Enum.join(", ")
          ),
          as: form_as(kind)
        )
      )
      |> assign(String.to_existing_atom("#{kind}_dirty?"), true)

    notify_profile_runtime(socket, kind)
  end

  defp update_backup_at(socket, kind, index, value) do
    update_backup(socket, kind, fn backups ->
      List.update_at(backups, index, fn _ -> String.trim(to_string(value || "")) end)
    end)
  end

  defp pending_for(socket, kind), do: pending_value(socket.assigns.pending, kind)

  defp pending_value(pending, kind) when is_map(pending) and is_binary(kind),
    do: Map.get(pending, String.to_existing_atom(kind))

  defp pending_value(pending, kind) when is_map(pending) and is_atom(kind),
    do: Map.get(pending, kind)

  defp pending_value(_pending, _kind), do: nil

  defp put_pending(socket, kind, reference),
    do: update(socket, :pending, &Map.put(&1, kind_key(kind), reference))

  defp clear_pending(socket, kind),
    do: update(socket, :pending, &Map.put(&1, kind_key(kind), nil))

  defp kind_key(kind) when is_binary(kind), do: String.to_existing_atom(kind)
  defp kind_key(kind), do: kind

  defp form_for(socket, :main), do: socket.assigns.main_form
  defp form_for(socket, :escalation), do: socket.assigns.escalation_form
  defp assign_form(socket, :main, form), do: assign(socket, :main_form, form)
  defp assign_form(socket, :escalation, form), do: assign(socket, :escalation_form, form)
  defp form_as(:main), do: :profile
  defp form_as(:escalation), do: :escalation

  defp params_with_staged_key(socket, kind) do
    form = form_for(socket, kind)
    staged = Map.get(socket.assigns, String.to_existing_atom("#{kind}_staged_key"), "")

    params = Map.delete(form.params, "apiKey")
    if staged == "", do: params, else: Map.put(params, "apiKey", staged)
  end

  defp profile_form_for(_profiles, "", kind), do: to_form(ProfilesLive.empty_form(), as: kind)

  defp profile_form_for(profiles, id, kind) do
    state = Enum.find(profiles, &(profile_id_from_state(&1) == id))

    case state do
      nil -> to_form(Map.put(ProfilesLive.empty_form(), "profileId", id), as: kind)
      profile_state -> to_form(ProfilesLive.profile_form(profile_state), as: kind)
    end
  end

  defp escalation_profile_id(form, selected_profile_id, profiles) do
    escalation_id = String.trim(form.params["escalationProfile"] || "")

    if escalation_id == ProfileDefaults.default_escalation_profile_id() do
      ProfileWidgetState.resolve_escalation_profile_id(profiles, selected_profile_id)
    else
      escalation_id
    end
  end

  defp put_escalation_profile(form, escalation_id) do
    to_form(Map.put(form.params, "escalationProfile", escalation_id), as: form.name)
  end

  defp profile_id(%Phoenix.HTML.Form{} = form), do: String.trim(form.params["profileId"] || "")
  defp profile_id_from_state(state), do: get_in(state, ["profile", "llmProfile"]) || ""

  defp scope_id("", suffix), do: suffix
  defp scope_id(nil, suffix), do: suffix
  defp scope_id(prefix, suffix), do: "#{prefix}-#{suffix}"

  defp field_id(prefix, id) do
    id = to_string(id)

    case String.split(id, "_", parts: 2) do
      [base, _rest] ->
        cond do
          prefix == base ->
            id

          is_binary(prefix) and String.ends_with?(prefix, "-#{base}") ->
            String.replace_prefix(id, base, prefix)

          true ->
            scope_id(prefix, id)
        end

      _ ->
        scope_id(prefix, id)
    end
  end

  defp assign_fold_state(socket, assigns) do
    socket
    |> assign(:main_config_open, Map.get(assigns, :config_open, socket.assigns.main_config_open))
    |> assign(
      :main_options_open,
      Map.get(assigns, :options_open, socket.assigns.main_options_open)
    )
    |> assign(:main_retry_open, Map.get(assigns, :retry_open, socket.assigns.main_retry_open))
    |> assign(
      :main_pricing_open,
      Map.get(assigns, :pricing_open, socket.assigns.main_pricing_open)
    )
    |> assign(
      :escalation_config_open,
      Map.get(assigns, :escalation_config_open, socket.assigns.escalation_config_open)
    )
    |> assign(
      :escalation_options_open,
      Map.get(assigns, :escalation_options_open, socket.assigns.escalation_options_open)
    )
    |> assign(
      :escalation_pricing_open,
      Map.get(assigns, :escalation_pricing_open, socket.assigns.escalation_pricing_open)
    )
    |> assign(:fold_disabled, Map.get(assigns, :fold_disabled, socket.assigns.fold_disabled))
  end

  defp fold_ui_name("main", "options"), do: "modelOptionsOpen"
  defp fold_ui_name("main", "retry"), do: "retryRepairOpen"
  defp fold_ui_name("main", "pricing"), do: "pricingOpen"
  defp fold_ui_name(_, _), do: nil

  defp notify_profile_runtime(socket, :main) do
    form = socket.assigns.main_form
    options = runtime_provider_options(form.params["defaultOptionsJson"])
    main_requires_save? = profile_requires_save?(socket, :main)

    socket
    |> assign(:main_requires_save?, main_requires_save?)
    |> notify_parent({:profile_widget_provider_options, options})
    |> notify_parent({:profile_widget_retry, retry_controls(socket)})
    |> notify_parent({:profile_widget_profile_dirty, profile_requires_save?(socket)})
  end

  defp notify_profile_runtime(socket, :escalation) do
    escalation_requires_save? = profile_requires_save?(socket, :escalation)

    socket
    |> assign(:escalation_requires_save?, escalation_requires_save?)
    |> notify_parent({:profile_widget_retry, retry_controls(socket)})
    |> notify_parent({:profile_widget_profile_dirty, profile_requires_save?(socket)})
  end

  defp runtime_provider_options(value) do
    case Jason.decode(value || "{}") do
      {:ok, options} when is_map(options) ->
        Map.drop(options, ~w(
          timeout maxRetries overallTimeoutMs maxAttempts baseDelayMs maxDelayMs
          enableRetryOn429 enableRetryOn5xx enableRetryOnNetworkError
          enableRetryOnParseError cacheMode cacheVersion callType reasoningEffort
          structuredRepairRetry useResponsesApi
        ))

      _ ->
        %{}
    end
  end

  defp retry_controls(socket) do
    params = socket.assigns.main_form.params || %{}
    escalation_params = socket.assigns.escalation_form.params || %{}
    enabled = truthy?(params["structuredRepairRetryEnabled"])

    escalation_model = String.trim(escalation_params["modelId"] || "")
    escalation_profile = String.trim(params["escalationProfile"] || "")

    escalation =
      if enabled and escalation_model != "" do
        %{
          "attempt" =>
            integer_or_default(
              params["escalationAttempt"],
              ProfileDefaults.retry_default("escalationAttempt")
            ),
          "profileId" => escalation_profile,
          "modelId" => escalation_model,
          "reasoningEffort" =>
            params["escalationReasoning"] || ProfileDefaults.repair_reasoning_default()
        }
      end

    %{
      "structuredRepair" => enabled,
      "maxAttempts" =>
        integer_or_default(
          params["retryMaxAttempts"],
          ProfileDefaults.retry_default("maxAttempts")
        ),
      "initialBackoffMs" =>
        integer_or_default(
          params["retryBaseDelayMs"],
          ProfileDefaults.retry_default("baseDelayMs")
        ),
      "maximumBackoffMs" =>
        integer_or_default(
          params["retryMaxDelayMs"],
          ProfileDefaults.retry_default("maxDelayMs")
        ),
      "retryNetwork" => truthy?(params["enableRetryOnNetworkError"]),
      "retryRateLimit" => truthy?(params["enableRetryOn429"]),
      "retryServerError" => truthy?(params["enableRetryOn5xx"]),
      "retryEmpty" => true,
      "retryParse" => truthy?(params["enableRetryOnParseError"]),
      "repairEscalation" => escalation
    }
  end

  defp profile_requires_save?(socket) do
    profile_requires_save?(socket, :main) or profile_requires_save?(socket, :escalation)
  end

  defp profile_requires_save?(socket, kind) do
    form = form_for(socket, kind)
    params = form.params || %{}
    id = String.trim(params["profileId"] || "")
    staged_key = Map.get(socket.assigns, String.to_existing_atom("#{kind}_staged_key"), "")
    current = profile_dirty_params(params)

    case Enum.find(socket.assigns.profiles, &(profile_id_from_state(&1) == id)) do
      nil ->
        staged_key != "" or
          ProfileWidgetState.dirty_fields(new_profile_dirty_baseline(), current) != MapSet.new()

      profile_state ->
        profile = profile_state["profile"] || %{}
        credential = profile_state["credential"] || %{}

        original = %{
          "profileId" => profile["llmProfile"],
          "provider" => profile["provider"],
          "apiInferenceType" =>
            profile["apiInferenceType"] || ProfileDefaults.api_inference_type_default(),
          "baseUrl" => profile["baseUrl"],
          "endpointCredentialScope" => profile["endpointCredentialScope"] || "user",
          "credentialId" => credential["credentialId"],
          "backupProfiles" => profile["backupProfiles"] || []
        }

        staged_key != "" or ProfileWidgetState.dirty_fields(original, current) != MapSet.new()
    end
  end

  defp profile_dirty_params(params) do
    Map.take(
      params,
      ~w(profileId provider apiInferenceType baseUrl endpointCredentialScope credentialId backupProfiles)
    )
  end

  defp new_profile_dirty_baseline do
    %{
      "profileId" => "",
      "provider" => "",
      "apiInferenceType" => ProfileDefaults.api_inference_type_default(),
      "baseUrl" => "",
      "endpointCredentialScope" => "user",
      "credentialId" => "",
      "backupProfiles" => []
    }
  end

  defp normalize_text(value), do: String.trim(to_string(value || ""))

  defp options_error(value, errors) do
    ProfilesLive.field_error(errors, "defaultOptionsJson") ||
      if(ProfilesLive.options_valid?(value),
        do: nil,
        else: "Default options JSON must be a valid object."
      )
  end

  defp normalize_base_url(value),
    do: value |> normalize_text() |> String.trim_trailing("/")

  defp integer_or_default(value, default) do
    case Integer.parse(normalize_text(value)) do
      {number, ""} when number >= 0 -> number
      _ -> default
    end
  end

  defp normalize_combobox_options(options) do
    options
    |> Enum.map(fn
      %{value: value} = option ->
        value = to_string(value || "")

        %{
          value: value,
          label: to_string(option[:label] || value),
          search: to_string(option[:search] || value)
        }

      {label, value} ->
        value = to_string(value || "")
        %{value: value, label: to_string(label), search: value}

      value ->
        value = to_string(value || "")
        %{value: value, label: value, search: value}
    end)
    |> Enum.reject(&(&1.value == ""))
    |> Enum.uniq_by(& &1.value)
  end

  defp profile_combobox_options(profiles) do
    Enum.map(profiles, fn profile_state ->
      profile = profile_state["profile"] || %{}
      id = profile["llmProfile"] || ""
      models = Enum.map(profile["models"] || [], &(&1["id"] || ""))

      %{
        value: id,
        label: id,
        search:
          Enum.join(
            [id, profile["modelId"], profile["baseUrl"], profile["apiInferenceType"] | models],
            " "
          )
      }
    end)
  end

  defp api_inference_combobox_options(options) do
    Enum.map(options, fn {label, value} ->
      %{value: value, label: label, search: "#{label} #{value}"}
    end)
  end

  defp base_url_combobox_options(profiles, current) do
    urls =
      profiles
      |> Enum.map(&get_in(&1, ["profile", "baseUrl"]))
      |> Kernel.++([current])
      |> Enum.map(&normalize_base_url/1)
      |> Enum.reject(&(&1 == ""))
      |> Enum.uniq()

    Enum.map(urls, &%{value: &1, label: &1, search: &1})
  end

  defp model_combobox_options(models) do
    Enum.map(models, fn model ->
      id = to_string(model["id"] || "")
      %{value: id, label: model["label"] || id, search: "#{id} #{model["label"] || ""}"}
    end)
  end

  defp models_for(profiles, id, extra_models, host_catalog, current_id) do
    profile_models =
      profiles
      |> Enum.find(%{}, &(profile_id_from_state(&1) == id))
      |> get_in(["profile", "models"])
      |> Kernel.||([])

    ProfileWidgetState.model_options(
      host_catalog,
      List.wrap(profile_models) ++ List.wrap(extra_models),
      current_id
    )
  end

  defp reasoning_options(profiles, selected_profile_id) do
    selected_profile_id = String.trim(to_string(selected_profile_id || ""))

    cond do
      selected_profile_id == "" ->
        @reasoning_options

      true ->
        case Enum.find(profiles, &(profile_id_from_state(&1) == selected_profile_id)) do
          %{} = profile_state ->
            case get_in(profile_state, ["profile", "reasoningEffortMap"]) do
              map when is_map(map) ->
                Enum.filter(@reasoning_options, fn {value, _label} ->
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

  defp normalize_reasoning_effort(profiles, selected_profile_id, current) do
    case reasoning_options(profiles, selected_profile_id) do
      [] ->
        ""

      options ->
        values = Enum.map(options, &elem(&1, 0))
        if current in values, do: current, else: hd(values)
    end
  end

  defp escalation_reasoning_options(profiles, form) do
    reasoning_options(profiles, form[:escalationProfile].value)
  end

  defp available_backup_profiles(profiles, id),
    do: Enum.reject(profiles, &(profile_id_from_state(&1) in [id, ""]))

  defp new_profile_fields_visible?(kind, form, profiles) do
    id = profile_id(form)

    kind == "escalation" or id == "" or
      not Enum.any?(profiles, &(profile_id_from_state(&1) == id))
  end

  defp replace_profile(profiles, profile_state) do
    id = profile_id_from_state(profile_state)

    if Enum.any?(profiles, &(profile_id_from_state(&1) == id)) do
      Enum.map(profiles, fn profile ->
        if profile_id_from_state(profile) == id, do: profile_state, else: profile
      end)
    else
      [profile_state | profiles]
    end
  end

  defp parse_index(value), do: String.to_integer(to_string(value))

  defp credential_available?(form, staged),
    do: staged != "" or form[:credentialConfigured].value in [true, "true"]

  defp credential_status(form, staged) do
    cond do
      staged != "" -> "New key staged for save"
      form[:credentialConfigured].value in [true, "true"] -> "Stored key available"
      true -> "No credential stored"
    end
  end

  defp truthy?(value), do: value in [true, "true", "on", "1"]

  defp cache_icon("refresh"), do: "↻"
  defp cache_icon(_), do: "💾"

  defp cache_label("refresh"), do: "Overwrite cache on next run"
  defp cache_label(_), do: "Use cache"

  defp cache_title("refresh"),
    do: "Fresh run: skips old cache and overwrites the saved response after success."

  defp cache_title(_), do: "Uses a saved response when this exact operation has already run."

  defp next_cache_mode("cache"), do: "refresh"
  defp next_cache_mode("refresh"), do: "cache"
  defp next_cache_mode(_), do: "refresh"

  defp notify_parent(socket, message) do
    send(self(), {:profile_widget, socket.assigns.id_prefix, message})
    socket
  end

  defp noreply(socket), do: {:noreply, socket}

  def field_error(assigns) do
    ~H"""
    <p :if={@message} class="ullm-field-error">{@message}</p>
    """
  end
end
