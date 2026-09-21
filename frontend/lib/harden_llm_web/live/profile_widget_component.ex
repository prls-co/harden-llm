defmodule HardenLlmWeb.ProfileWidgetComponent do
  @moduledoc """
  The provider-neutral LLM profile widget.

  This is deliberately a LiveComponent rather than a page-level editor. The
  compact row and every configuration fold can therefore be embedded beside a
  caller's prompt, output, or other controls without introducing navigation or
  a modal surface. Hosts that mount more than one instance pass a distinct
  `id_prefix`; it namespaces generated form/control IDs and parent messages,
  while the host supplies matching upload configurations.
  """

  use HardenLlmWeb, :live_component

  alias HardenLlmWeb.{
    APIError,
    HardenAPI,
    Observability,
    ProfileDefaults,
    ProfileForm,
    ProfileWidgetState
  }

  @api_inference_types [
    {"Chat Completions", "chat-completions"},
    {"OpenAI Responses", "responses"},
    {"Gemini Generate Content", "gemini-generate-content"},
    {"Anthropic Messages", "anthropic-messages"}
  ]

  @reasoning_options [{"lowest", "L"}, {"middle", "M"}, {"highest", "H"}]

  @recovery_target_paths [
    ["jsonRepair", "initial"],
    ["jsonRepair", "escalation"],
    ["rerun", "target"],
    ["rerun", "jsonRepair", "initial"],
    ["rerun", "jsonRepair", "escalation"]
  ]

  @fold_keys ~w(
    main_credential_open main_options_open main_retry_open main_pricing_open
  )

  @impl true
  def mount(socket) do
    {:ok,
     socket
     |> assign(:initialized?, false)
     |> assign(:web_search, false)
     |> assign(:recovery_policy_default, %{})
     |> assign(:active_recovery_policy, %{})
     |> assign(:id_prefix, "")
     |> assign(:host_context, "workspace")
     |> assign(:definition_form, nil)
     |> assign(:definition_revision, nil)
     |> assign(:target_only, false)
     |> assign(:target_value, %{})
     |> assign(:target_name, "recoveryTarget")
     |> assign(:recovery_target_config_open, %{})
     |> assign(:loaded_profile_id, nil)
     |> assign(:profiles_revision, nil)
     |> assign(:main_form, to_form(ProfileForm.empty_form(%{}), as: :profile))
     |> assign(:main_dirty?, false)
     |> assign(:main_revision, 0)
     |> assign(:main_requires_save?, false)
     |> assign(:main_staged_key, "")
     |> assign(:main_config_open, false)
     |> assign(:main_credential_open, false)
     |> assign(:main_options_open, false)
     |> assign(:main_retry_open, false)
     |> assign(:main_pricing_open, false)
     |> assign(:api_inference_types, @api_inference_types)
     |> assign(:model_catalog, nil)
     |> assign(:model_options, [])
     |> assign(:category_name, "LLM")
     |> assign(:field_errors, %{})
     |> assign(:recovery_field_errors, %{})
     |> assign(:fold_disabled, false)
     |> assign(:pending, nil)
     |> assign(:pending_profile_revision, nil)
     |> assign(:pending_target_path, nil)
     |> assign(:operation_error, nil)
     |> assign(:delete_confirm, false)}
  end

  @impl true
  def update(assigns, socket) do
    profiles = Map.get(assigns, :profiles, [])
    selected_profile_id = Map.get(assigns, :selected_profile_id, "") || ""
    revision = :erlang.phash2(profiles)
    active_recovery_policy = active_recovery_policy(assigns, socket.assigns)
    host_context = Map.get(assigns, :host_context, socket.assigns.host_context)
    definition_form = Map.get(assigns, :definition_form)
    definition_revision = Map.get(assigns, :definition_revision)

    definition_changed? =
      host_context == "profile_definition" and not is_nil(definition_form) and
        definition_revision != socket.assigns.definition_revision

    component_assigns = Map.delete(assigns, :recovery_policy)

    socket =
      socket
      |> assign(component_assigns)
      |> assign(:id_prefix, Map.get(assigns, :id_prefix, socket.assigns.id_prefix))
      |> assign(:host_context, Map.get(assigns, :host_context, socket.assigns.host_context))
      |> assign(
        :target_only,
        Map.get(assigns, :target_only, Map.get(socket.assigns, :target_only, false))
      )
      |> assign(
        :target_value,
        Map.get(assigns, :target_value, Map.get(socket.assigns, :target_value, %{}))
      )
      |> assign(
        :target_name,
        Map.get(assigns, :target_name, Map.get(socket.assigns, :target_name, "recoveryTarget"))
      )
      |> assign(:active_recovery_policy, active_recovery_policy)

    socket =
      if definition_changed? do
        socket
        |> assign(:definition_form, definition_form)
        |> assign(:definition_revision, definition_revision)
        |> assign(:main_form, definition_form)
        |> assign(:main_dirty?, false)
      else
        socket
      end

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

    socket =
      if definition_changed?,
        do: assign(socket, :main_form, definition_form),
        else: socket

    socket = sync_active_recovery_policy(socket)

    socket = if needs_profile_reset?, do: notify_profile_runtime(socket), else: socket

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
     |> assign(:cache_mode, Map.get(assigns, :cache_mode, ProfileDefaults.cache_mode_default()))
     |> assign(
       :web_search,
       truthy?(Map.get(assigns, :web_search, Map.get(socket.assigns, :web_search, false)))
     )}
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

    provider_options =
      runtime_provider_options(socket.assigns.main_form.params["defaultOptionsJson"])

    selection = %{
      profile_id: selected_profile_id,
      model_id: model_id,
      reasoning_effort: reasoning_effort,
      recovery_policy: socket.assigns.active_recovery_policy,
      provider_options: provider_options
    }

    socket
    |> mark_main_edit()
    |> assign(:loaded_profile_id, selected_profile_id)
    |> assign(:main_dirty?, false)
    |> assign(:reasoning_effort, reasoning_effort)
    |> notify_parent({:profile_widget_selection, selection})
    |> notify_parent({:profile_widget_profile_dirty, false})
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

  def handle_event("toggle-web-search", _params, socket) do
    next_enabled = not socket.assigns.web_search

    socket
    |> assign(:web_search, next_enabled)
    |> notify_parent({:profile_widget_control, "webSearch", next_enabled})
    |> noreply()
  end

  def handle_event("toggle-fold", %{"fold" => fold}, socket) do
    key = "main_#{fold}_open"

    if key in @fold_keys and not socket.assigns.fold_disabled do
      atom_key = String.to_existing_atom(key)
      socket = update(socket, atom_key, &(!&1))

      case fold_ui_name(fold) do
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

  def handle_event("toggle-section", %{"section" => section}, socket)
      when section in ["options_open", "retry_open", "pricing_open", "credential_open"] do
    fold = String.replace_suffix(section, "_open", "")
    handle_event("toggle-fold", %{"fold" => fold}, socket)
  end

  def handle_event("toggle-section", _params, socket), do: {:noreply, socket}

  def handle_event("profile-draft-change", %{"profile" => params}, socket) do
    socket = update_profile_form(socket, params)

    recovery_intent =
      if Map.has_key?(params, "recoveryPolicy"),
        do: socket.assigns.active_recovery_policy,
        else: nil

    socket = notify_profile_runtime(socket, recovery_intent)
    noreply(socket)
  end

  def handle_event("recovery-target-change", params, socket) when is_map(params) do
    target_params = Map.get(params, socket.assigns.target_name, %{})
    target_params = if is_map(target_params), do: target_params, else: %{}

    target =
      socket.assigns.target_value
      |> ProfileWidgetState.merge_draft(target_params)
      |> ProfileWidgetState.serialize_recovery_target()

    socket
    |> assign(:target_value, target)
    |> notify_parent({:profile_widget_target, socket.assigns.target_name, target})
    |> noreply()
  end

  def handle_event("recovery-target-change", _params, socket), do: {:noreply, socket}

  def handle_event("toggle-json-repair", %{"node-id" => "original"}, socket),
    do:
      toggle_recovery_branch(socket, ["jsonRepair"], fn ->
        default_recovery_branch(
          socket,
          ["jsonRepair"],
          &ProfileWidgetState.default_recovery_repair_plan/0
        )
      end)

  def handle_event("toggle-json-repair", _params, socket), do: {:noreply, socket}

  def handle_event("toggle-rerun", %{"node-id" => "original"}, socket),
    do:
      toggle_recovery_branch(socket, ["rerun"], fn ->
        default_recovery_branch(
          socket,
          ["rerun"],
          &ProfileWidgetState.default_recovery_rerun_plan/0
        )
      end)

  def handle_event("toggle-rerun", _params, socket), do: {:noreply, socket}

  def handle_event(
        "toggle-rerun-json-repair",
        %{"path" => "rerun.jsonRepair", "node-id" => "rerun"},
        socket
      ) do
    toggle_recovery_branch(socket, ["rerun", "jsonRepair"], fn ->
      default_recovery_branch(
        socket,
        ["rerun", "jsonRepair"],
        &ProfileWidgetState.default_recovery_repair_plan/0
      )
    end)
  end

  def handle_event("toggle-rerun-json-repair", _params, socket), do: {:noreply, socket}

  def handle_event("toggle-recovery-target-config", %{"path" => path}, socket)
      when is_binary(path) and path != "" do
    if not valid_target_config_path?(path) do
      {:noreply, socket}
    else
      open? = not Map.get(socket.assigns.recovery_target_config_open, path, false)

      socket
      |> update(:recovery_target_config_open, &Map.put(&1, path, open?))
      |> noreply()
    end
  end

  def handle_event("toggle-recovery-target-config", _params, socket), do: {:noreply, socket}

  def handle_event("toggle-recovery-target-fold", %{"path" => path, "fold" => fold}, socket)
      when is_binary(path) and path != "" and fold in ["options", "retry", "pricing"] do
    if not valid_target_config_path?(path) do
      {:noreply, socket}
    else
      key = "#{path}.#{fold}"

      socket
      |> update(:recovery_target_config_open, fn values ->
        Map.update(values, key, true, fn open? -> not open? end)
      end)
      |> noreply()
    end
  end

  def handle_event("toggle-recovery-target-fold", _params, socket), do: {:noreply, socket}

  def handle_event(
        "toggle-repair-escalation",
        %{"path" => path, "node-id" => node_id},
        socket
      )
      when (path == "jsonRepair" and node_id == "original") or
             (path == "rerun.jsonRepair" and node_id == "rerun") do
    keys = String.split(path, ".", trim: true)

    update_recovery_draft(socket, fn policy ->
      update_in(policy, keys, fn
        plan when is_map(plan) ->
          Map.update!(plan, "escalation", fn
            nil ->
              default_recovery_target(
                socket,
                keys ++ ["escalation"],
                %{"source" => "generation"}
              )

            _target ->
              nil
          end)

        other ->
          other
      end)
    end)
  end

  def handle_event("toggle-repair-escalation", _params, socket), do: {:noreply, socket}

  def handle_event(
        "use-recovery-default",
        %{"path" => path, "node-id" => node_id},
        socket
      )
      when (path in ["jsonRepair", "rerun"] and node_id == "original") or
             (path == "rerun.jsonRepair" and node_id == "rerun") do
    keys = String.split(path, ".", trim: true)

    update_recovery_draft(socket, fn policy ->
      case default_recovery_branch(socket, keys, fn -> nil end) do
        plan when is_map(plan) -> put_in(policy, keys, plan)
        _ -> policy
      end
    end)
  end

  def handle_event("use-recovery-default", _params, socket), do: {:noreply, socket}

  def handle_event("toggle-credential", _params, socket) do
    if socket.assigns.fold_disabled do
      {:noreply, socket}
    else
      key = :main_credential_open
      open = not truthy?(socket.assigns.main_credential_open)

      socket
      |> assign(key, open)
      |> notify_parent({:profile_widget_ui, "credentialOpen", open})
      |> noreply()
    end
  end

  def handle_event("stage-key", params, socket) do
    form = socket.assigns.main_form
    key = String.trim(params["apiKey"] || params["api-key"] || form.params["apiKey"] || "")

    if key == "" do
      {:noreply,
       assign(socket, :operation_error, "Enter a replacement API key before staging it.")}
    else
      form = to_form(Map.put(form.params, "apiKey", ""), as: :profile)

      socket =
        socket
        |> assign(:main_form, form)
        |> assign(:main_staged_key, key)
        |> assign(:main_credential_open, false)
        |> assign(:operation_error, nil)
        |> mark_main_edit()

      socket = notify_profile_runtime(socket)
      {:noreply, notify_parent(socket, {:profile_widget_ui, "credentialOpen", false})}
    end
  end

  def handle_event("clear-staged-key", _params, socket) do
    {:noreply,
     socket
     |> assign(:main_staged_key, "")
     |> update_profile_form(%{"apiKey" => ""})
     |> notify_profile_runtime()}
  end

  def handle_event("cancel-key", _params, socket) do
    socket =
      socket
      |> assign(:main_staged_key, "")
      |> assign(:main_credential_open, false)
      |> update_profile_form(%{"apiKey" => ""})
      |> notify_profile_runtime()

    {:noreply, notify_parent(socket, {:profile_widget_ui, "credentialOpen", false})}
  end

  def handle_event("new-profile", _params, socket) do
    selected_profile_id = ""
    socket = reset_profile_forms(socket, socket.assigns.profiles, selected_profile_id)

    provider_options =
      runtime_provider_options(socket.assigns.main_form.params["defaultOptionsJson"])

    selection = %{
      profile_id: selected_profile_id,
      model_id: "",
      reasoning_effort: socket.assigns.reasoning_effort,
      recovery_policy: socket.assigns.active_recovery_policy,
      provider_options: provider_options
    }

    socket
    |> mark_main_edit()
    |> assign(:loaded_profile_id, selected_profile_id)
    |> assign(:main_config_open, true)
    |> assign(:main_dirty?, true)
    |> maybe_notify_definition_draft(socket.assigns.main_form.params)
    |> notify_parent({:profile_widget_selection, selection})
    |> notify_profile_runtime()
    |> noreply()
  end

  def handle_event("profile-confirm-delete", _params, socket),
    do: {:noreply, assign(socket, :delete_confirm, true)}

  def handle_event("profile-cancel-delete", _params, socket),
    do: {:noreply, assign(socket, :delete_confirm, false)}

  def handle_event("profile-delete", _params, socket) do
    if socket.assigns.pending != nil do
      {:noreply, socket}
    else
      id = profile_id(socket.assigns.main_form)

      if id == "" do
        {:noreply, assign(socket, :delete_confirm, false)}
      else
        reference = System.unique_integer([:positive, :monotonic])
        handle = socket.assigns.session_handle

        {:noreply,
         socket
         |> assign(:pending, reference)
         |> start_async(
           {:profile_delete, reference},
           Observability.propagate(fn -> HardenAPI.delete_profile(handle, id) end)
         )}
      end
    end
  end

  def handle_event("profile-save", %{"node-path" => path}, socket)
      when is_binary(path) and path != "" do
    save_target_profile(socket, path)
  end

  def handle_event("profile-save", _params, socket) do
    if socket.assigns.pending != nil do
      {:noreply, socket}
    else
      params =
        socket
        |> params_with_staged_key()
        |> Map.put("recoveryPolicy", socket.assigns.active_recovery_policy)

      case ProfileForm.profile_payload(params) do
        {:ok, payload} ->
          reference = System.unique_integer([:positive, :monotonic])
          handle = socket.assigns.session_handle
          id = params["profileId"] || ""
          save_revision = socket.assigns.main_revision

          {:noreply,
           socket
           |> assign(:pending, reference)
           |> assign(:pending_profile_revision, save_revision)
           |> assign(:main_form, to_form(params, as: :profile))
           |> start_async(
             {:profile_save, reference},
             Observability.propagate(fn -> HardenAPI.save_profile(handle, id, payload) end)
           )}

        {:error, message} ->
          {:noreply, assign(socket, :operation_error, message)}
      end
    end
  end

  def handle_event("profile-refresh", _params, socket) do
    id = profile_id(socket.assigns.main_form)

    cond do
      socket.assigns.pending != nil ->
        {:noreply, socket}

      id == "" ->
        {:noreply, socket}

      profile_requires_save?(socket) ->
        {:noreply, assign(socket, :operation_error, "Save profile before refreshing models.")}

      true ->
        reference = System.unique_integer([:positive, :monotonic])
        handle = socket.assigns.session_handle

        {:noreply,
         socket
         |> assign(:pending, reference)
         |> start_async(
           {:profile_refresh, reference},
           Observability.propagate(fn -> HardenAPI.refresh_profile_models(handle, id) end)
         )}
    end
  end

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

    socket =
      case Map.fetch(params, "webSearch") do
        {:ok, value} when value in [true, false] ->
          socket
          |> assign(:web_search, value)
          |> notify_parent({:profile_widget_control, "webSearch", value})

        {:ok, value} when is_binary(value) and value != "" ->
          enabled = truthy?(value)

          socket
          |> assign(:web_search, enabled)
          |> notify_parent({:profile_widget_control, "webSearch", enabled})

        _ ->
          socket
      end

    {:noreply, socket}
  end

  defp notify_workspace_controls(socket, _params), do: {:noreply, socket}

  @impl true
  def handle_async({_operation, reference}, _result, %{assigns: %{pending: pending}} = socket)
      when reference != pending, do: {:noreply, socket}

  def handle_async({:profile_save, _reference}, {:ok, {:ok, profile_state, _state}}, socket) do
    profiles = replace_profile(socket.assigns.profiles, profile_state)
    saved_revision = socket.assigns.pending_profile_revision
    newer_edits? = not is_nil(saved_revision) and saved_revision != socket.assigns.main_revision

    socket =
      socket
      |> assign(:pending, nil)
      |> assign(:pending_profile_revision, nil)
      |> assign(:operation_error, nil)
      |> assign(:field_errors, %{})
      |> assign(:delete_confirm, false)
      |> assign(:profiles, profiles)
      |> assign(:profiles_revision, :erlang.phash2(profiles))

    socket =
      if newer_edits? do
        socket
        |> sync_active_recovery_policy()
        |> notify_profile_runtime()
      else
        socket
        |> assign(:main_dirty?, false)
        |> assign(:main_form, to_form(ProfileForm.profile_form(profile_state), as: :profile))
        |> assign(:main_staged_key, "")
        |> sync_active_recovery_policy()
        |> notify_profile_runtime()
      end

    socket
    |> notify_parent({:profile_widget_catalog, profiles})
    |> noreply()
  end

  def handle_async(
        {:profile_save_target, reference, path},
        {:ok, {:ok, profile_state, _state}},
        %{assigns: %{pending: reference, pending_target_path: pending_path}} = socket
      )
      when pending_path == path do
    profiles = replace_profile(socket.assigns.profiles, profile_state)

    socket
    |> assign(:pending, nil)
    |> assign(:pending_target_path, nil)
    |> assign(:profiles, profiles)
    |> assign(:profiles_revision, :erlang.phash2(profiles))
    |> assign(:operation_error, nil)
    |> notify_parent({:profile_widget_catalog, profiles})
    |> noreply()
  end

  def handle_async(
        {:profile_save_target, reference, path},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{pending: reference, pending_target_path: pending_path}} = socket
      )
      when pending_path == path do
    socket
    |> assign(:pending, nil)
    |> assign(:pending_target_path, nil)
    |> assign(:operation_error, error.message)
    |> noreply()
  end

  def handle_async(
        {:profile_save_target, reference, path},
        _result,
        %{assigns: %{pending: reference, pending_target_path: pending_path}} = socket
      )
      when pending_path == path do
    socket
    |> assign(:pending, nil)
    |> assign(:pending_target_path, nil)
    |> assign(:operation_error, "The shared profile could not be saved.")
    |> noreply()
  end

  def handle_async({:profile_refresh, _reference}, {:ok, {:ok, profile_state, _state}}, socket) do
    profiles = replace_profile(socket.assigns.profiles, profile_state)

    socket
    |> assign(:pending, nil)
    |> assign(:profiles, profiles)
    |> assign(:profiles_revision, :erlang.phash2(profiles))
    |> assign(:operation_error, nil)
    |> assign(:field_errors, %{})
    |> assign(:main_form, to_form(ProfileForm.profile_form(profile_state), as: :profile))
    |> put_flash(:info, "Model catalog refreshed.")
    |> notify_parent({:profile_widget_catalog, profiles})
    |> noreply()
  end

  def handle_async({:profile_delete, _reference}, {:ok, {:ok, _result, _state}}, socket) do
    id = profile_id(socket.assigns.main_form)
    profiles = Enum.reject(socket.assigns.profiles, &(profile_id_from_state(&1) == id))

    socket
    |> assign(:pending, nil)
    |> assign(:delete_confirm, false)
    |> assign(:profiles, profiles)
    |> assign(:profiles_revision, :erlang.phash2(profiles))
    |> assign(:operation_error, nil)
    |> assign(:field_errors, %{})
    |> reset_profile_forms(profiles, "")
    |> assign(:loaded_profile_id, "")
    |> notify_parent({:profile_widget_catalog, profiles})
    |> noreply()
  end

  def handle_async({operation, _reference}, {:ok, {:error, %APIError{} = error}}, socket)
      when operation in [:profile_save, :profile_refresh, :profile_delete] do
    socket
    |> assign(:pending, nil)
    |> assign(:pending_profile_revision, nil)
    |> assign(:delete_confirm, false)
    |> assign(:field_errors, error.field_errors)
    |> assign(:operation_error, error.message)
    |> noreply()
  end

  def handle_async({operation, _reference}, _result, socket)
      when operation in [:profile_save, :profile_refresh, :profile_delete] do
    message =
      case operation do
        :profile_save -> "The profile could not be saved."
        :profile_refresh -> "The model catalog could not be refreshed."
        :profile_delete -> "The profile could not be deleted."
      end

    socket
    |> assign(:pending, nil)
    |> assign(:pending_profile_revision, nil)
    |> assign(:delete_confirm, false)
    |> assign(:operation_error, message)
    |> noreply()
  end

  def handle_async(_operation, _result, socket), do: {:noreply, socket}

  @impl true
  def render(assigns) do
    assigns = Map.put_new(assigns, :host_context, "workspace")

    ~H"""
    <section id={@id} class="ullm-widget ullm-model-config-widget" aria-label="LLM model config">
      <%= if @host_context == "profile_definition" do %>
        <.profile_editor
          form={@main_form}
          id_prefix={@id_prefix}
          target={@myself}
          profiles={@profiles}
          field_errors={@field_errors}
          recovery_policy_default={@recovery_policy_default}
          api_inference_types={@api_inference_types}
          model_catalog={@model_catalog}
          model_options={@model_options}
          requires_save={@main_requires_save?}
          fold_disabled={@fold_disabled}
          credential_open={@main_credential_open}
          options_open={@main_options_open}
          retry_open={@main_retry_open}
          pricing_open={@main_pricing_open}
          staged_key={@main_staged_key}
          cache_mode={@cache_mode}
          web_search={false}
          show_rerun_search={false}
          bundle_upload={nil}
          widget_id={@id_prefix}
          pending={@pending}
          delete_confirm={@delete_confirm}
          target_config_open={@recovery_target_config_open}
          generation_profile_id={@main_form.params["profileId"] || ""}
          generation_model_id={@main_form.params["modelId"] || ""}
          generation_reasoning={@main_form.params["reasoningEffort"] || ""}
          host_context={@host_context}
          show_identity_fields={true}
          fold_event={
            if @host_context == "profile_definition", do: "toggle-section", else: "toggle-fold"
          }
        />
      <% else %>
        <%= if @target_only do %>
          <.recovery_target_fields
            id_prefix={scope_id(@id_prefix, "target")}
            name={@target_name}
            target_value={@target_value}
            profiles={@profiles}
            target={@myself}
            change="recovery-target-change"
          />
        <% else %>
          <.profile_widget_node
            mode="root"
            category={@category_name}
            id_prefix={@id_prefix}
            profile_value={@selected_profile_id}
            profile_options={profile_combobox_options(@profiles)}
            reasoning_value={@reasoning_effort}
            reasoning_options={reasoning_options(@profiles, @selected_profile_id)}
            web_search={@web_search}
            cache_mode={@cache_mode}
            model_value={@model_id}
            config_open={@main_config_open}
            form={@main_form}
            target={@myself}
            profiles={@profiles}
            field_errors={Map.merge(@field_errors, @recovery_field_errors)}
            recovery_policy_default={@recovery_policy_default}
            api_inference_types={@api_inference_types}
            model_catalog={@model_catalog}
            model_options={@model_options}
            requires_save={@main_requires_save?}
            fold_disabled={@fold_disabled}
            credential_open={@main_credential_open}
            options_open={@main_options_open}
            retry_open={@main_retry_open}
            pricing_open={@main_pricing_open}
            staged_key={@main_staged_key}
            bundle_upload={@bundle_upload}
            widget_id={@id_prefix}
            pending={@pending}
            delete_confirm={@delete_confirm}
            target_config_open={@recovery_target_config_open}
          />
        <% end %>
      <% end %>
    </section>
    """
  end

  # The row and its configuration drawer are one widget at every generation or
  # recovery node. Recovery targets pass a namespaced form and role; the root
  # passes the workspace form. Keeping this seam in one function component makes
  # it difficult for a future target-specific picker to silently become a
  # reduced copy of the root editor again.
  attr(:mode, :string, required: true)
  attr(:category, :string, default: "LLM")
  attr(:id_prefix, :string, required: true)
  attr(:profile_value, :any, default: "")
  attr(:profile_options, :list, default: [])
  attr(:reasoning_value, :any, default: "")
  attr(:reasoning_options, :list, default: [])
  attr(:web_search, :boolean, default: false)
  attr(:cache_mode, :string, default: "cache")
  attr(:model_value, :any, default: "")
  attr(:config_open, :boolean, default: false)
  attr(:form, :any, default: nil)
  attr(:target, :any, default: nil)
  attr(:profiles, :list, default: [])
  attr(:field_errors, :map, default: %{})
  attr(:recovery_policy_default, :map, default: %{})
  attr(:api_inference_types, :list, default: @api_inference_types)
  attr(:model_catalog, :list, default: [])
  attr(:model_options, :list, default: [])
  attr(:requires_save, :boolean, default: false)
  attr(:fold_disabled, :boolean, default: false)
  attr(:credential_open, :boolean, default: false)
  attr(:options_open, :boolean, default: false)
  attr(:retry_open, :boolean, default: false)
  attr(:pricing_open, :boolean, default: false)
  attr(:staged_key, :string, default: "")
  attr(:bundle_upload, :any, default: nil)
  attr(:widget_id, :string, default: "")
  attr(:pending, :any, default: nil)
  attr(:delete_confirm, :boolean, default: false)
  attr(:target_config_open, :map, default: %{})
  attr(:target_name, :string, default: "recoveryTarget")
  attr(:disabled, :boolean, default: false)
  attr(:change, :string, default: "profile-draft-change")
  attr(:show_search, :boolean, default: false)
  attr(:search_enabled, :boolean, default: false)
  attr(:config_path, :string, default: nil)
  attr(:nested_repair_allowed, :boolean, default: false)
  attr(:nested_repair_plan, :map, default: nil)
  attr(:nested_repair_name, :string, default: "recoveryPolicy[rerun][jsonRepair]")
  attr(:nested_repair_path, :string, default: "rerun.jsonRepair")
  attr(:target_form, :any, default: nil)
  attr(:target_role, :string, default: "json_repair")
  attr(:target_path, :string, default: nil)
  attr(:inherited, :boolean, default: false)
  attr(:host_context, :string, default: "workspace")

  def profile_widget_node(assigns) do
    ~H"""
    <%= if @mode == "root" do %>
      <.profile_row
        category={@category}
        profile_input_id={scope_id(@id_prefix, "run_selectedProfileId")}
        profile_name="run[selectedProfileId]"
        profile_value={@profile_value}
        profile_options={@profile_options}
        profile_required={true}
        profile_class="ullm-input ullm-profile-select"
        profile_change="select-profile"
        reasoning_input_id={scope_id(@id_prefix, "workspace-reasoning")}
        reasoning_name="run[reasoningEffort]"
        reasoning_value={@reasoning_value}
        reasoning_options={@reasoning_options}
        reasoning_change="workspace-control"
        search_input_id={scope_id(@id_prefix, "workspace-web-search-toggle")}
        search_field_id={scope_id(@id_prefix, "workspace-web-search")}
        search_field_name="run[webSearch]"
        search_enabled={@web_search}
        cache_input_id={scope_id(@id_prefix, "workspace-cache-toggle")}
        cache_field_id={scope_id(@id_prefix, "workspace-cache")}
        cache_field_name="run[cacheMode]"
        cache_mode={@cache_mode}
        config_id={scope_id(@id_prefix, "model-config-toggle")}
        config_event="toggle-config"
        config_open={@config_open}
        model_input_id={scope_id(@id_prefix, "run_modelId")}
        model_input_name="run[modelId]"
        model_value={@model_value}
        target={@target}
        fold_disabled={@fold_disabled}
      />
      <div
        :if={@config_open}
        id={scope_id(@id_prefix, "model-options")}
        class="ullm-profile-config-body ullm-form-grid"
      >
        <.profile_editor
          form={@form}
          id_prefix={scope_id(@id_prefix, "profile")}
          target={@target}
          profiles={@profiles}
          field_errors={@field_errors}
          recovery_policy_default={@recovery_policy_default}
          api_inference_types={@api_inference_types}
          model_catalog={@model_catalog}
          model_options={@model_options}
          requires_save={@requires_save}
          fold_disabled={@fold_disabled}
          credential_open={@credential_open}
          options_open={@options_open}
          retry_open={@retry_open}
          pricing_open={@pricing_open}
          staged_key={@staged_key}
          cache_mode={@cache_mode}
          web_search={@web_search}
          show_rerun_search={true}
          bundle_upload={@bundle_upload}
          widget_id={@widget_id}
          pending={@pending}
          delete_confirm={@delete_confirm}
          target_config_open={@target_config_open}
          generation_profile_id={@profile_value}
          generation_model_id={@model_value}
          generation_reasoning={@reasoning_value}
          host_context={@host_context}
          show_identity_fields={false}
        />
      </div>
    <% else %>
      <.profile_row
        category="LLM"
        profile_input_id={"#{@id_prefix}-profile"}
        profile_name={"#{@target_name}[profileId]"}
        profile_value={@profile_value}
        profile_options={@profile_options}
        profile_required={true}
        profile_class="ullm-input ullm-profile-select"
        profile_change={@change}
        profile_disabled={@disabled and not @inherited}
        reasoning_input_id={"#{@id_prefix}-reasoning"}
        reasoning_name={"#{@target_name}[reasoningEffort]"}
        reasoning_value={@reasoning_value}
        reasoning_options={@reasoning_options}
        reasoning_change={@change}
        reasoning_disabled={@disabled or @inherited}
        search_input_id={if @show_search, do: "#{@id_prefix}-web-search-toggle"}
        search_enabled={@search_enabled}
        search_disabled={@disabled or @inherited}
        model_input_id={"#{@id_prefix}-model"}
        model_input_name={"#{@target_name}[modelId]"}
        model_value={@model_value}
        model_disabled={@disabled or @inherited}
        config_id={if @config_path, do: "#{@id_prefix}-config-toggle"}
        config_event="toggle-recovery-target-config"
        config_path={@config_path}
        config_open={@config_open}
        row_class="ullm-recovery-profile-row"
        target={@target}
        fold_disabled={@disabled or @inherited}
      />
      <p :if={@inherited} class="ullm-field-help ullm-recovery-inherited">
        Inherited from the generation model. Choose a profile to override it.
      </p>
      <.target_profile_config
        :if={@config_open}
        id_prefix={"#{@id_prefix}-config"}
        target={@target}
        profiles={@profiles}
        change={@change}
        enabled={not @disabled}
        nested_repair_allowed={@nested_repair_allowed}
        nested_repair_plan={@nested_repair_plan}
        nested_repair_name={@nested_repair_name}
        nested_repair_path={@nested_repair_path}
        target_form={@target_form}
        target_role={@target_role}
        target_path={@target_path}
        target_repair_plan={@nested_repair_plan}
        target_repair_name={@nested_repair_name}
        target_repair_path={@nested_repair_path}
        api_inference_types={@api_inference_types}
        target_config_open={@target_config_open}
        generation_profile_id={@profile_value}
        generation_model_id={@model_value}
        generation_reasoning={@reasoning_value}
      />
    <% end %>
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
  attr(:profile_disabled, :boolean, default: false)
  attr(:profile_allow_custom, :boolean, default: true)
  attr(:reasoning_disabled, :boolean, default: false)
  attr(:search_input_id, :string, default: nil)
  attr(:search_field_id, :string, default: nil)
  attr(:search_field_name, :string, default: nil)
  attr(:search_enabled, :boolean, default: false)
  attr(:search_event, :string, default: "toggle-web-search")
  attr(:search_disabled, :boolean, default: false)
  attr(:cache_input_id, :string, default: nil)
  attr(:cache_mode, :string, default: "cache")
  attr(:cache_field_id, :string, default: nil)
  attr(:cache_field_name, :string, default: nil)
  attr(:config_id, :string, default: nil)
  attr(:config_event, :string, default: nil)
  attr(:config_path, :string, default: nil)
  attr(:config_open, :boolean, default: false)
  attr(:model_input_id, :string, default: nil)
  attr(:model_input_name, :string, default: nil)
  attr(:model_value, :any, default: "")
  attr(:model_disabled, :boolean, default: false)
  attr(:target, :any, required: true)
  attr(:fold_disabled, :boolean, default: false)
  attr(:row_class, :string, default: "")

  def profile_row(assigns) do
    ~H"""
    <div class={[
      "ullm-profile-row",
      @search_input_id && "ullm-profile-row-with-search",
      (@config_id || @cache_input_id) && "ullm-profile-row-with-actions",
      @row_class
    ]}>
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
          allow_custom={@profile_allow_custom}
          required={@profile_required}
          disabled={@profile_disabled}
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
          disabled={@reasoning_disabled or @reasoning_options == []}
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
        :if={@search_input_id}
        id={@search_input_id}
        type="button"
        class={["ullm-btn", "ullm-profile-search-toggle", @search_enabled && "is-enabled"]}
        phx-click={@search_event}
        phx-target={@target}
        disabled={@search_disabled}
        aria-label={web_search_label(@search_enabled)}
        aria-pressed={to_string(@search_enabled)}
        data-web-search={to_string(@search_enabled)}
        title={web_search_title(@search_enabled)}
      ><span aria-hidden="true">🌐</span></button>
      <input
        :if={@search_field_id}
        id={@search_field_id}
        type="hidden"
        name={@search_field_name}
        value={to_string(@search_enabled)}
        class="ullm-sr-only"
        autocomplete="off"
      />
      <button
        :if={@cache_input_id}
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
        disabled={@model_disabled}
      />
      <button
        :if={@config_id}
        id={@config_id}
        type="button"
        class="ullm-btn ullm-profile-config-toggle"
        phx-click={@config_event}
        phx-target={@target}
        phx-value-path={@config_path}
        disabled={@fold_disabled}
        aria-expanded={to_string(@config_open)}
        aria-label="Profile config"
      >⚙</button>
    </div>
    """
  end

  attr(:form, :any, required: true)
  attr(:id_prefix, :string, required: true)
  attr(:target, :any, required: true)
  attr(:profiles, :list, required: true)
  attr(:field_errors, :map, default: %{})
  attr(:recovery_policy_default, :map, default: %{})
  attr(:api_inference_types, :list, default: @api_inference_types)
  attr(:model_catalog, :list, default: nil)
  attr(:model_options, :list, default: [])
  attr(:requires_save, :boolean, default: false)
  attr(:fold_disabled, :boolean, default: false)
  attr(:credential_open, :boolean, default: false)
  attr(:options_open, :boolean, default: false)
  attr(:retry_open, :boolean, default: false)
  attr(:pricing_open, :boolean, default: false)
  attr(:staged_key, :string, default: "")
  attr(:cache_mode, :string, default: "cache")
  attr(:web_search, :boolean, default: false)
  attr(:show_rerun_search, :boolean, default: false)
  attr(:bundle_upload, :any, default: nil)
  attr(:widget_id, :string, default: "")
  attr(:pending, :any, default: nil)
  attr(:delete_confirm, :boolean, default: false)
  attr(:target_only, :boolean, default: false)
  attr(:target_mode, :boolean, default: false)
  attr(:target_role, :string, default: nil)
  attr(:target_path, :string, default: nil)
  attr(:target_repair_plan, :map, default: nil)
  attr(:target_repair_name, :string, default: nil)
  attr(:target_repair_path, :string, default: nil)
  attr(:target_value, :map, default: %{})
  attr(:target_name, :string, default: "recoveryTarget")
  attr(:target_config_open, :map, default: %{})
  attr(:generation_profile_id, :string, default: "")
  attr(:generation_model_id, :string, default: "")
  attr(:generation_reasoning, :string, default: "")
  attr(:host_context, :string, default: "workspace")
  attr(:show_identity_fields, :boolean, default: false)
  attr(:fold_event, :string, default: "toggle-fold")
  attr(:fold_path, :string, default: nil)

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
            disabled={@target_mode}
          />
          <.field_error message={ProfileForm.field_error(@field_errors, "apiInferenceType")} />
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
            disabled={@target_mode}
          />
          <datalist :if={@host_context == "profile_definition"} id="profile-base-url-options">
            <option :for={base_url <- base_url_values(@profiles)} value={base_url} />
          </datalist>
          <.field_error message={ProfileForm.field_error(@field_errors, "baseUrl")} />
        </div>
      </div>

      <section class="ullm-credential-block">
        <div class="ullm-credential-row">
          <div
            id={editor_control_id(@id_prefix, "credential-status", @host_context)}
            class="ullm-credential-status"
          >
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
            id={editor_control_id(@id_prefix, "credential-toggle", @host_context)}
            class="ullm-btn ullm-btn-tiny"
            phx-click="toggle-credential"
            phx-target={@target}
            disabled={@fold_disabled or @target_mode}
            aria-expanded={to_string(@credential_open)}
          >{if @credential_open,
            do: "Hide key",
            else: if(credential_available?(@form, @staged_key), do: "Replace key", else: "Set key")}</button>
        </div>
        <div
          :if={@credential_open}
          id={editor_control_id(@id_prefix, "credential-drawer", @host_context)}
          class="ullm-credential-drawer"
          phx-hook="SecretStager"
        >
          <div class="ullm-options-grid">
            <.input
              field={@form[:credentialId]}
              id={field_id(@id_prefix, @form[:credentialId].id)}
              label="Credential ID"
              required
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
              disabled={@target_mode}
            />
            <.field_error message={ProfileForm.field_error(@field_errors, "credentialId")} />
            <.input
              field={@form[:endpointCredentialScope]}
              id={field_id(@id_prefix, @form[:endpointCredentialScope].id)}
              type="select"
              label="Credential scope"
              options={[{"User", "user"}, {"Global", "global"}]}
              phx-change="profile-draft-change"
              phx-target={@target}
              disabled={@target_mode}
            />
          </div>
          <.input
            field={@form[:apiKey]}
            id={field_id(@id_prefix, @form[:apiKey].id)}
            type="password"
            label="Replacement API Key"
            autocomplete="new-password"
            class="ullm-input ullm-input-mono"
            data-secret-input
          />
          <.field_error message={ProfileForm.field_error(@field_errors, "credential.apiKey")} />
          <div class="ullm-button-row ullm-button-row-end">
            <button
              :if={@staged_key != "" or @host_context == "profile_definition"}
              id={editor_control_id(@id_prefix, "clear-staged-key", @host_context)}
              type="button"
              class="ullm-btn ullm-btn-danger"
              phx-click="clear-staged-key"
              phx-target={@target}
            >Clear staged key</button>
            <button
              id={editor_control_id(@id_prefix, "cancel-key", @host_context)}
              type="button"
              class="ullm-btn"
              phx-click="cancel-key"
              phx-target={@target}
            >Cancel</button>
            <button
              id={editor_control_id(@id_prefix, "stage-key", @host_context)}
              type="button"
              class="ullm-btn ullm-btn-primary"
              phx-click="stage-key"
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
          phx-target={@target}
          disabled={@pending != nil or profile_id(@form) == "" or @requires_save or @target_mode}
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
            placeholder={ProfileDefaults.model_placeholder()}
            aria_label="Model ID"
            class="ullm-input ullm-input-mono"
            phx_change="profile-draft-change"
            phx_target={@target}
          />
          <datalist :if={@host_context == "profile_definition"} id="profile-model-options">
            <option
              :for={
                model <-
                  models_for(
                    @profiles,
                    profile_id(@form),
                    @model_options,
                    @model_catalog,
                    @form[:modelId].value
                  )
              }
              value={model["id"]}
            >
              {model["label"]}
            </option>
          </datalist>
          <.field_error message={ProfileForm.field_error(@field_errors, "modelId")} />
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

      <div
        :if={@show_identity_fields or new_profile_fields_visible?(@form, @profiles)}
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
        <.field_error message={ProfileForm.field_error(@field_errors, "llmProfile")} />
        <.input
          field={@form[:provider]}
          id={field_id(@id_prefix, @form[:provider].id)}
          label="Provider family"
          required
          class="ullm-input"
          phx-change="profile-draft-change"
          phx-target={@target}
        />
        <.field_error message={ProfileForm.field_error(@field_errors, "provider")} />
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
        <.input
          field={@form[:supportsWebSearch]}
          id={field_id(@id_prefix, @form[:supportsWebSearch].id)}
          type="checkbox"
          label="Supports native web search"
          phx-change="profile-draft-change"
          phx-target={@target}
        />
      </div>
      <input
        :if={not new_profile_fields_visible?(@form, @profiles)}
        type="hidden"
        name={@form[:supportsWebSearch].name}
        value={to_string(truthy?(@form[:supportsWebSearch].value))}
      />

      <div class="ullm-options-fold">
        <button
          id={editor_control_id(@id_prefix, "options-toggle", @host_context)}
          type="button"
          class="ullm-btn ullm-options-summary"
          phx-click={@fold_event}
          phx-value-fold="options"
          phx-value-section={if @host_context == "profile_definition", do: "options_open"}
          phx-value-path={@fold_path}
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

      <section class="ullm-options-fold">
        <button
          id={editor_control_id(@id_prefix, "retry-toggle", @host_context)}
          type="button"
          class="ullm-btn ullm-options-summary"
          phx-click={@fold_event}
          phx-value-fold="retry"
          phx-value-section={if @host_context == "profile_definition", do: "retry_open"}
          phx-value-path={@fold_path}
          phx-target={@target}
          disabled={@fold_disabled}
          aria-expanded={to_string(@retry_open)}
        >Retries &amp; Repair</button>
        <div :if={@retry_open} id={"#{@id_prefix}-retry-repair"} class="ullm-options-body">
          <%= if @target_mode do %>
            <.target_recovery_editor
              id_prefix={@id_prefix}
              target={@target}
              role={@target_role}
              plan={@target_repair_plan}
              name={@target_repair_name}
              path={@target_repair_path}
              profiles={@profiles}
              change="profile-draft-change"
              enabled={not @fold_disabled}
              target_config_open={@target_config_open}
              generation_profile_id={@generation_profile_id}
              generation_model_id={@generation_model_id}
              generation_reasoning={@generation_reasoning}
            />
          <% else %>
            <.recovery_fields
              form={@form}
              id_prefix={@id_prefix}
              target={@target}
              change="profile-draft-change"
              field_errors={@field_errors}
              profiles={@profiles}
              recovery_policy_default={@recovery_policy_default}
              web_search={@web_search}
              show_rerun_search={@show_rerun_search}
              target_config_open={@target_config_open}
              generation_profile_id={@generation_profile_id}
              generation_model_id={@generation_model_id}
              generation_reasoning={@generation_reasoning}
            />
          <% end %>
        </div>
      </section>

      <div class="ullm-options-fold ullm-pricing-section">
        <button
          id={editor_control_id(@id_prefix, "pricing-toggle", @host_context)}
          type="button"
          class="ullm-btn ullm-options-summary"
          phx-click={@fold_event}
          phx-value-fold="pricing"
          phx-value-section={if @host_context == "profile_definition", do: "pricing_open"}
          phx-value-path={@fold_path}
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
            disabled={@target_mode}
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
            disabled={@target_mode}
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
            disabled={@target_mode}
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
            disabled={@target_mode}
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
            disabled={@target_mode}
          />
        </div>
      </div>

      <div class="ullm-profile-actions ullm-button-row">
        <button
          :if={@host_context == "profile_definition"}
          id="profile-cancel"
          type="button"
          class="ullm-btn"
          phx-click="cancel-edit"
        >Cancel</button>
        <button
          id={scope_id(@id_prefix, "new")}
          type="button"
          class="ullm-btn"
          phx-click="new-profile"
          phx-target={@target}
          disabled={@target_mode}
        >+ New</button>
        <label id={scope_id(@id_prefix, "bundle-file")} class="ullm-btn ullm-file-button">
          Import Bundle
          <.live_file_input
            :if={@bundle_upload}
            upload={@bundle_upload}
            phx-change="import-bundle"
            phx-value-widget={@widget_id}
            disabled={@target_mode}
          />
        </label>
        <a id={scope_id(@id_prefix, "export-bundle")} href={~p"/profiles/bundle"} class="ullm-btn">Export Bundle</a>
        <button
          id={scope_id(@id_prefix, "save")}
          type="button"
          class="ullm-btn ullm-btn-primary"
          phx-click="profile-save"
          phx-target={@target}
          phx-value-node-path={@target_path}
          disabled={
            @pending != nil or profile_id(@form) == "" or
              not ProfileForm.options_valid?(@form[:defaultOptionsJson].value)
          }
        >{if @target_mode,
          do: "Save shared profile",
          else: if(@pending, do: "Saving…", else: "Save Profile")}</button>
        <button
          :if={profile_id(@form) != ""}
          id={scope_id(@id_prefix, "delete")}
          type="button"
          class="ullm-btn ullm-btn-danger"
          phx-click="profile-confirm-delete"
          phx-target={@target}
          phx-value-node-path={@target_path}
          disabled={@target_mode}
        >Delete Profile</button>
      </div>

      <div
        :if={@delete_confirm}
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
          phx-target={@target}
        >Confirm delete</button>
      </div>
    </div>
    """
  end

  attr(:id_prefix, :string, required: true)
  attr(:target, :any, default: nil)
  attr(:role, :string, default: "json_repair")
  attr(:plan, :map, default: nil)
  attr(:name, :string, default: nil)
  attr(:path, :string, default: nil)
  attr(:profiles, :list, default: [])
  attr(:change, :string, default: "profile-draft-change")
  attr(:enabled, :boolean, default: true)
  attr(:target_config_open, :map, default: %{})
  attr(:generation_profile_id, :string, default: "")
  attr(:generation_model_id, :string, default: "")
  attr(:generation_reasoning, :string, default: "")

  def target_recovery_editor(assigns) do
    ~H"""
    <div id={"#{@id_prefix}-target-recovery"} class="ullm-target-recovery-editor">
      <%= if @role == "rerun_generation" do %>
        <p class="ullm-field-help">
          Recovery after this rerun uses the same two-target repair editor. A
          repair target cannot start another fresh rerun.
        </p>
        <label class="ullm-checkbox-label">
          <input
            id={"#{@id_prefix}-json-repair-toggle"}
            type="checkbox"
            checked={is_map(@plan)}
            phx-click="toggle-rerun-json-repair"
            phx-value-path={@path && String.replace_suffix(@path, ".target", ".jsonRepair")}
            phx-value-node-id="rerun"
            phx-target={@target}
            disabled={not @enabled}
          /> LLM JSON repair after rerun
        </label>
        <.repair_plan_fields
          :if={is_map(@plan)}
          id_prefix={"#{@id_prefix}-json-repair"}
          name={@name}
          plan={@plan}
          path={@path && String.replace_suffix(@path, ".target", ".jsonRepair")}
          profiles={@profiles}
          target={@target}
          change={@change}
          enabled={@enabled}
          target_config_open={@target_config_open}
          label="LLM JSON repair after rerun"
          generation_profile_id={@generation_profile_id}
          generation_model_id={@generation_model_id}
          generation_reasoning={@generation_reasoning}
        />
      <% else %>
        <p class="ullm-field-help">
          JSON repair targets are terminal recovery stages. Their model,
          reasoning, provider options, and saved-profile actions are configurable,
          but they cannot start another repair or fresh rerun.
        </p>
      <% end %>
    </div>
    """
  end

  attr(:form, :any, required: true)
  attr(:id_prefix, :string, required: true)
  attr(:target, :any, default: nil)
  attr(:change, :string, default: nil)
  attr(:field_errors, :map, default: %{})
  attr(:profiles, :list, default: [])
  attr(:recovery_policy_default, :map, default: %{})
  attr(:web_search, :boolean, default: false)
  attr(:show_rerun_search, :boolean, default: false)
  attr(:target_config_open, :map, default: %{})
  attr(:generation_profile_id, :string, default: "")
  attr(:generation_model_id, :string, default: "")
  attr(:generation_reasoning, :string, default: "")

  def recovery_fields(assigns) do
    policy = assigns.form.params["recoveryPolicy"] || %{}
    default_policy = assigns[:recovery_policy_default] || %{}
    json_repair_enabled? = is_map(policy["jsonRepair"])
    rerun_enabled? = is_map(policy["rerun"])

    json_repair_plan =
      if json_repair_enabled?,
        do: recovery_plan_for_display(policy["jsonRepair"], default_policy["jsonRepair"]),
        else: nil

    rerun_plan =
      if rerun_enabled?,
        do: recovery_plan_for_display(policy["rerun"], default_policy["rerun"]),
        else: nil

    assigns =
      assigns
      |> assign(:policy, policy)
      |> assign(:json_repair_plan, json_repair_plan)
      |> assign(:rerun_plan, rerun_plan)
      |> assign(:json_repair_enabled?, json_repair_enabled?)
      |> assign(:rerun_enabled?, rerun_enabled?)
      |> assign(
        :json_repair_preview?,
        is_map(json_repair_plan) and
          not same_recovery_plan?(policy["jsonRepair"], json_repair_plan)
      )
      |> assign(
        :rerun_preview?,
        is_map(rerun_plan) and not same_recovery_plan?(policy["rerun"], rerun_plan)
      )
      |> assign(:name, "#{assigns.form.name}[recoveryPolicy]")
      |> assign(:categories, [
        {"network", "Network errors"},
        {"rate_limit", "Rate limits"},
        {"server_error", "Server errors"},
        {"empty_response", "Empty responses"},
        {"provider_retry", "Provider retry requests"}
      ])
      |> assign(:numbers, [
        {"maxAttempts", "Max attempts",
         "Total provider calls, including the first call, retries and repairs. The selected profile and model stay the same."},
        {"baseDelayMs", "Base delay (ms)", "Initial calculated backoff. Zero is allowed."},
        {"maxDelayMs", "Max delay (ms)",
         "Caps calculated backoff. A valid Retry-After on HTTP 429 or 503 remains a minimum; the caller deadline still applies."}
      ])
      |> assign(
        :explicit?,
        Map.has_key?(assigns.form.params["recoveryPolicy"] || %{}, "jsonRepair") or
          Map.has_key?(assigns.form.params["recoveryPolicy"] || %{}, "rerun")
      )

    ~H"""
    <div id={"#{@id_prefix}-recovery-policy"} class="recovery-policy">
      <.input
        :if={not @explicit?}
        type="checkbox"
        id={"#{@id_prefix}-repair-invalid-output"}
        name={"#{@name}[repairInvalidOutput]"}
        value={@policy["repairInvalidOutput"]}
        errors={
          List.wrap(ProfileForm.field_error(@field_errors, "recoveryPolicy.repairInvalidOutput"))
        }
        label="LLM JSON repair"
        info="Uses the original schema to repair invalid JSON or schema failures. Each repair uses the remaining call budget. Turn this off to stop on invalid output."
        phx-change={@change}
        phx-target={@target}
      />
      <div :if={@explicit?} class="recovery-policy-plans">
        <div class="recovery-policy-toggles">
          <label class="ullm-checkbox-label">
            <input
              id={"#{@id_prefix}-json-repair-toggle"}
              type="checkbox"
              checked={is_map(@policy["jsonRepair"])}
              phx-click="toggle-json-repair"
              phx-value-node-id="original"
              phx-target={@target}
            /> LLM JSON repair
          </label>
          <label class="ullm-checkbox-label">
            <input
              id={"#{@id_prefix}-rerun-toggle"}
              type="checkbox"
              checked={is_map(@policy["rerun"])}
              phx-click="toggle-rerun"
              phx-value-node-id="original"
              phx-target={@target}
            /> Fresh generation rerun
          </label>
        </div>
        <.repair_plan_fields
          :if={is_map(@json_repair_plan)}
          id_prefix={"#{@id_prefix}-original-repair"}
          name={"#{@name}[jsonRepair]"}
          plan={@json_repair_plan}
          path="jsonRepair"
          profiles={@profiles}
          target={@target}
          change={@change}
          enabled={@json_repair_enabled? and not @json_repair_preview?}
          preview={@json_repair_preview? or not @json_repair_enabled?}
          label="LLM JSON repair"
          target_config_open={@target_config_open}
          generation_profile_id={@generation_profile_id}
          generation_model_id={@generation_model_id}
          generation_reasoning={@generation_reasoning}
        />
        <.rerun_plan_fields
          :if={is_map(@rerun_plan)}
          id_prefix={"#{@id_prefix}-rerun"}
          name={"#{@name}[rerun]"}
          plan={@rerun_plan}
          path="rerun"
          profiles={@profiles}
          target={@target}
          change={@change}
          enabled={@rerun_enabled? and not @rerun_preview?}
          preview={@rerun_preview? or not @rerun_enabled?}
          search_enabled={@web_search}
          show_generation_search={@show_rerun_search}
          target_config_open={@target_config_open}
          generation_profile_id={@generation_profile_id}
          generation_model_id={@generation_model_id}
          generation_reasoning={@generation_reasoning}
        />
      </div>
      <div class="recovery-policy-categories">
        <.input
          :for={{category, label} <- @categories}
          type="checkbox"
          multiple
          id={"#{@id_prefix}-retry-#{category}"}
          name={"#{@name}[retryOn][]"}
          value={category}
          checked={category in (@policy["retryOn"] || [])}
          label={label}
          phx-change={@change}
          phx-target={@target}
        />
      </div>
      <.field_error message={ProfileForm.field_error(@field_errors, "recoveryPolicy.retryOn")} />
      <div class="recovery-policy-numbers">
        <.input
          :for={{key, label, info} <- @numbers}
          type="number"
          id={"#{@id_prefix}-recovery-#{key}"}
          name={if key == "maxAttempts", do: "#{@name}[#{key}]", else: "#{@name}[backoff][#{key}]"}
          value={if key == "maxAttempts", do: @policy[key], else: get_in(@policy, ["backoff", key])}
          errors={
            List.wrap(
              ProfileForm.field_error(
                @field_errors,
                if(key == "maxAttempts",
                  do: "recoveryPolicy.#{key}",
                  else: "recoveryPolicy.backoff.#{key}"
                )
              )
            )
          }
          label={label}
          info={info}
          step="1"
          required
          phx-change={@change}
          phx-target={@target}
        />
      </div>
      <.field_error message={ProfileForm.field_error(@field_errors, "recoveryPolicy")} />
      <.field_error message={ProfileForm.field_error(@field_errors, "recoveryPolicy.backoff")} />
    </div>
    """
  end

  attr(:id_prefix, :string, required: true)
  attr(:name, :string, required: true)
  attr(:target_value, :map, default: %{})
  attr(:profiles, :list, default: [])
  attr(:target, :any, default: nil)
  attr(:change, :string, default: "profile-draft-change")
  attr(:disabled, :boolean, default: false)
  attr(:show_search, :boolean, default: false)
  attr(:search_enabled, :boolean, default: false)
  attr(:config_path, :string, default: nil)
  attr(:config_open, :boolean, default: false)
  attr(:nested_repair_allowed, :boolean, default: false)
  attr(:nested_repair_plan, :map, default: nil)
  attr(:nested_repair_name, :string, default: "recoveryPolicy[rerun][jsonRepair]")
  attr(:nested_repair_path, :string, default: "rerun.jsonRepair")
  attr(:target_config_open, :map, default: %{})
  attr(:api_inference_types, :list, default: @api_inference_types)
  attr(:generation_profile_id, :string, default: "")
  attr(:generation_model_id, :string, default: "")
  attr(:generation_reasoning, :string, default: "")

  @doc "Shared leaf target picker used by both generation branches and hosts."
  def recovery_target_fields(assigns) do
    target = ProfileWidgetState.serialize_recovery_target(assigns.target_value)
    inherited? = target["source"] == "generation"

    display_target =
      effective_recovery_target(
        target,
        assigns.generation_profile_id,
        assigns.generation_model_id,
        assigns.generation_reasoning
      )

    profile_id = display_target["profileId"] || ""
    profile_options = profile_combobox_options(assigns.profiles)
    target_form = target_profile_form(assigns.profiles, display_target, assigns.name)

    assigns =
      assign(assigns,
        wire_target: target,
        leaf_target: display_target,
        inherited: inherited?,
        profile_options: profile_options,
        reasoning_options: reasoning_options(assigns.profiles, profile_id),
        target_form: target_form,
        target_path: assigns.config_path || assigns.nested_repair_path,
        target_config_visible?: assigns.config_open and not inherited?
      )

    ~H"""
    <div id={@id_prefix} class="ullm-recovery-target" data-target-only>
      <input
        type="hidden"
        name={"#{@name}[source]"}
        value={@leaf_target["source"] || "profile"}
        disabled={@disabled}
      />
      <div class="ullm-options-grid">
        <.profile_widget_node
          mode="target"
          id_prefix={@id_prefix}
          target_name={@name}
          profile_value={@leaf_target["profileId"] || ""}
          profile_options={@profile_options}
          reasoning_value={@leaf_target["reasoningEffort"] || ""}
          reasoning_options={@reasoning_options}
          model_value={@leaf_target["modelId"] || ""}
          target={@target}
          profiles={@profiles}
          change={@change}
          disabled={@disabled}
          show_search={@show_search}
          search_enabled={@search_enabled}
          config_path={@config_path}
          config_open={@target_config_visible?}
          nested_repair_allowed={@nested_repair_allowed}
          nested_repair_plan={@nested_repair_plan}
          nested_repair_name={@nested_repair_name}
          nested_repair_path={@nested_repair_path}
          target_form={@target_form}
          target_role={if @nested_repair_allowed, do: "rerun_generation", else: "json_repair"}
          target_path={@target_path}
          api_inference_types={@api_inference_types}
          target_config_open={@target_config_open}
          inherited={@inherited}
        />
      </div>
      <input
        type="hidden"
        name={"#{@name}[providerOptions]"}
        value={encode_target_options(@wire_target["providerOptions"])}
        disabled={@disabled}
      />
    </div>
    """
  end

  attr(:id_prefix, :string, required: true)
  attr(:target, :any, default: nil)
  attr(:profiles, :list, default: [])
  attr(:change, :string, default: "profile-draft-change")
  attr(:enabled, :boolean, default: true)
  attr(:nested_repair_allowed, :boolean, default: false)
  attr(:nested_repair_plan, :map, default: nil)
  attr(:nested_repair_name, :string, default: "recoveryPolicy[rerun][jsonRepair]")
  attr(:nested_repair_path, :string, default: "rerun.jsonRepair")
  attr(:target_config_open, :map, default: %{})
  attr(:target_form, :any, required: true)
  attr(:target_role, :string, default: "json_repair")
  attr(:target_path, :string, required: true)
  attr(:target_repair_plan, :map, default: nil)
  attr(:target_repair_name, :string, default: nil)
  attr(:target_repair_path, :string, default: nil)
  attr(:api_inference_types, :list, default: @api_inference_types)
  attr(:generation_profile_id, :string, default: "")
  attr(:generation_model_id, :string, default: "")
  attr(:generation_reasoning, :string, default: "")

  def target_profile_config(assigns) do
    ~H"""
    <div id={@id_prefix} class="ullm-recovery-target-config">
      <.profile_editor
        form={@target_form}
        id_prefix={@id_prefix}
        target={@target}
        profiles={@profiles}
        field_errors={%{}}
        recovery_policy_default={%{}}
        api_inference_types={@api_inference_types}
        model_catalog={[]}
        model_options={[]}
        requires_save={false}
        fold_disabled={not @enabled}
        credential_open={false}
        options_open={Map.get(@target_config_open, "#{@target_path}.options", false)}
        retry_open={Map.get(@target_config_open, "#{@target_path}.retry", true)}
        pricing_open={Map.get(@target_config_open, "#{@target_path}.pricing", false)}
        staged_key=""
        cache_mode="cache"
        web_search={false}
        show_rerun_search={false}
        bundle_upload={nil}
        widget_id={@target_path}
        pending={nil}
        delete_confirm={false}
        target_mode={true}
        target_role={@target_role}
        target_path={@target_path}
        target_repair_plan={@nested_repair_plan}
        target_repair_name={@nested_repair_name}
        target_repair_path={@nested_repair_path}
        target_config_open={@target_config_open}
        generation_profile_id={@generation_profile_id}
        generation_model_id={@generation_model_id}
        generation_reasoning={@generation_reasoning}
        fold_event="toggle-recovery-target-fold"
        fold_path={@target_path}
      />
    </div>
    """
  end

  attr(:id_prefix, :string, required: true)
  attr(:name, :string, required: true)
  attr(:plan, :map, default: %{})
  attr(:profiles, :list, default: [])
  attr(:target, :any, default: nil)
  attr(:change, :string, default: "profile-draft-change")
  attr(:label, :string, default: "JSON repair")
  attr(:path, :string, default: "jsonRepair")
  attr(:enabled, :boolean, default: true)
  attr(:preview, :boolean, default: false)
  attr(:target_config_open, :map, default: %{})
  attr(:generation_profile_id, :string, default: "")
  attr(:generation_model_id, :string, default: "")
  attr(:generation_reasoning, :string, default: "")

  def repair_plan_fields(assigns) do
    plan = assigns.plan || %{}
    assigns = assign(assigns, :plan, plan)

    ~H"""
    <fieldset id={@id_prefix} class="ullm-repair-plan">
      <legend>{@label}</legend>
      <div :if={@preview} class="ullm-recovery-default-preview">
        <p>Configured profile targets are shown below.</p>
        <button
          type="button"
          class="ullm-btn"
          phx-click="use-recovery-default"
          phx-value-path={@path}
          phx-value-node-id={recovery_node_for_path(@path)}
          phx-target={@target}
        >Use configured profile targets</button>
      </div>
      <.recovery_target_fields
        id_prefix={"#{@id_prefix}-initial"}
        name={"#{@name}[initial]"}
        target_value={@plan["initial"] || %{}}
        profiles={@profiles}
        target={@target}
        change={@change}
        disabled={not @enabled}
        config_path={"#{@path}.initial"}
        config_open={Map.get(@target_config_open, "#{@path}.initial", false)}
        target_config_open={@target_config_open}
        generation_profile_id={@generation_profile_id}
        generation_model_id={@generation_model_id}
        generation_reasoning={@generation_reasoning}
      />
      <label class="ullm-checkbox-label">
        <input
          id={"#{@id_prefix}-escalation-toggle"}
          type="checkbox"
          checked={is_map(@plan["escalation"])}
          phx-click="toggle-repair-escalation"
          phx-value-path={@path}
          phx-value-node-id={recovery_node_for_path(@path)}
          phx-target={@target}
          disabled={not @enabled}
        /> Escalated JSON repair
      </label>
      <.recovery_target_fields
        :if={is_map(@plan["escalation"])}
        id_prefix={"#{@id_prefix}-escalation"}
        name={"#{@name}[escalation]"}
        target_value={@plan["escalation"]}
        profiles={@profiles}
        target={@target}
        change={@change}
        disabled={not @enabled}
        config_path={"#{@path}.escalation"}
        config_open={Map.get(@target_config_open, "#{@path}.escalation", false)}
        target_config_open={@target_config_open}
        generation_profile_id={@generation_profile_id}
        generation_model_id={@generation_model_id}
        generation_reasoning={@generation_reasoning}
      />
    </fieldset>
    """
  end

  attr(:id_prefix, :string, required: true)
  attr(:name, :string, required: true)
  attr(:plan, :map, default: nil)
  attr(:profiles, :list, default: [])
  attr(:target, :any, default: nil)
  attr(:change, :string, default: "profile-draft-change")
  attr(:path, :string, default: "rerun")
  attr(:enabled, :boolean, default: true)
  attr(:preview, :boolean, default: false)
  attr(:show_generation_search, :boolean, default: false)
  attr(:search_enabled, :boolean, default: false)
  attr(:target_config_open, :map, default: %{})
  attr(:generation_profile_id, :string, default: "")
  attr(:generation_model_id, :string, default: "")
  attr(:generation_reasoning, :string, default: "")

  def rerun_plan_fields(assigns) do
    ~H"""
    <fieldset id={@id_prefix} class="ullm-rerun-plan">
      <legend>Fresh generation rerun</legend>
      <%= if is_map(@plan) do %>
        <div :if={@preview} class="ullm-recovery-default-preview">
          <p>Configured rerun and repair targets are shown below.</p>
          <button
            type="button"
            class="ullm-btn"
            phx-click="use-recovery-default"
            phx-value-path={@path}
            phx-value-node-id={recovery_node_for_path(@path)}
            phx-target={@target}
          >Use configured profile targets</button>
        </div>
        <.recovery_target_fields
          id_prefix={"#{@id_prefix}-generation"}
          name={"#{@name}[target]"}
          target_value={@plan["target"] || %{}}
          profiles={@profiles}
          target={@target}
          change={@change}
          disabled={not @enabled}
          show_search={@show_generation_search}
          search_enabled={@search_enabled}
          config_path={"#{@path}.target"}
          config_open={Map.get(@target_config_open, "#{@path}.target", false)}
          nested_repair_allowed={true}
          nested_repair_plan={@plan["jsonRepair"]}
          nested_repair_name={"#{@name}[jsonRepair]"}
          nested_repair_path={"#{@path}.jsonRepair"}
          target_config_open={@target_config_open}
          generation_profile_id={@generation_profile_id}
          generation_model_id={@generation_model_id}
          generation_reasoning={@generation_reasoning}
        />
      <% end %>
    </fieldset>
    """
  end

  defp target_profile_form(profiles, target, name) do
    profile_id = target["profileId"] || ""

    params =
      case Enum.find(profiles, &(profile_id_from_state(&1) == profile_id)) do
        nil -> ProfileForm.empty_form(%{})
        profile_state -> ProfileForm.profile_form(profile_state)
      end

    options =
      case target["providerOptions"] do
        value when is_map(value) -> value
        _ -> runtime_options(params["defaultOptionsJson"])
      end

    params
    |> Map.merge(%{
      "profileId" => profile_id,
      "modelId" => target["modelId"] || params["modelId"] || "",
      "reasoningEffort" => target["reasoningEffort"] || ProfileDefaults.reasoning_default(),
      "defaultOptionsJson" => Jason.encode!(options, pretty: true),
      "maxTokens" => option_text(options["max_tokens"] || options["maxTokens"]),
      "temperature" => option_text(options["temperature"]),
      "topP" => option_text(options["top_p"] || options["topP"]),
      "topK" => option_text(options["top_k"] || options["topK"]),
      "stopSequences" => stop_text(options["stop"]),
      "recoveryPolicy" => %{}
    })
    |> then(&to_form(&1, as: name))
  end

  defp effective_recovery_target(target, profile_id, model_id, reasoning)
       when is_map(target) do
    if target["source"] == "generation" do
      Map.merge(target, %{
        "profileId" => to_string(profile_id || ""),
        "modelId" => to_string(model_id || ""),
        "reasoningEffort" => to_string(reasoning || "")
      })
    else
      target
    end
  end

  defp effective_recovery_target(target, _profile_id, _model_id, _reasoning), do: target

  defp runtime_options(value) when is_binary(value) do
    case Jason.decode(value) do
      {:ok, options} when is_map(options) -> options
      _ -> %{}
    end
  end

  defp runtime_options(value) when is_map(value), do: value
  defp runtime_options(_value), do: %{}

  defp valid_target_config_path?(path) do
    path in [
      "jsonRepair.initial",
      "jsonRepair.escalation",
      "rerun.target",
      "rerun.jsonRepair.initial",
      "rerun.jsonRepair.escalation"
    ]
  end

  defp recovery_node_for_path("rerun.jsonRepair"), do: "rerun"
  defp recovery_node_for_path("rerun"), do: "original"
  defp recovery_node_for_path("jsonRepair"), do: "original"
  defp recovery_node_for_path(_path), do: "original"

  defp encode_target_options(options) when is_map(options), do: Jason.encode!(options)
  defp encode_target_options(_options), do: ""

  defp reset_profile_forms(socket, profiles, selected_profile_id) do
    form = profile_form_for(profiles, selected_profile_id, socket.assigns.recovery_policy_default)

    socket
    |> assign(:selected_profile_id, selected_profile_id)
    |> assign(:main_form, form)
    |> assign(:recovery_target_config_open, %{})
    |> assign(:main_staged_key, "")
    |> assign(:main_requires_save?, false)
    |> sync_active_recovery_policy()
  end

  defp update_profile_form(socket, incoming) do
    previous_params = socket.assigns.main_form.params

    params =
      previous_params
      |> ProfileWidgetState.merge_draft(incoming)
      |> synchronize_profile_options(incoming)
      |> reset_changed_recovery_targets(previous_params, socket.assigns.profiles)
      |> canonicalize_recovery_policy()

    socket =
      socket
      |> assign(:main_form, to_form(params, as: :profile))
      |> assign(:active_recovery_policy, params["recoveryPolicy"] || %{})
      |> assign(:main_dirty?, true)
      |> mark_main_edit()
      |> maybe_notify_definition_draft(params)

    if Map.has_key?(incoming, "modelId"),
      do: notify_parent(socket, {:profile_widget_control, "modelId", params["modelId"] || ""}),
      else: socket
  end

  defp canonicalize_recovery_policy(params) do
    policy =
      params["recoveryPolicy"]
      |> ProfileWidgetState.serialize_recovery_policy()

    Map.put(params, "recoveryPolicy", policy)
  end

  defp reset_changed_recovery_targets(params, previous_params, profiles) do
    Enum.reduce(@recovery_target_paths, params, fn path, acc ->
      previous = get_in(previous_params, ["recoveryPolicy" | path])
      current = get_in(acc, ["recoveryPolicy" | path])

      if is_map(previous) and is_map(current) and
           Map.get(previous, "profileId") != Map.get(current, "profileId") and
           is_binary(Map.get(current, "profileId")) and Map.get(current, "profileId") != "" do
        put_in(
          acc,
          ["recoveryPolicy" | path],
          ProfileWidgetState.reset_recovery_target_for_profile(current, profiles)
        )
      else
        acc
      end
    end)
  end

  defp toggle_recovery_branch(socket, path, default_fun) do
    update_recovery_draft(socket, fn policy ->
      case get_in(policy, path) do
        value when is_map(value) -> put_in(policy, path, nil)
        _ -> put_in(policy, path, default_fun.())
      end
    end)
  end

  defp default_recovery_branch(socket, path, fallback_fun) do
    case get_in(socket.assigns.recovery_policy_default || %{}, path) do
      plan when is_map(plan) -> plan
      _ -> fallback_fun.()
    end
  end

  defp recovery_plan_for_display(current, default) do
    cond do
      is_map(current) and recovery_plan_is_generation_relative?(current) and is_map(default) ->
        default

      is_map(current) ->
        current

      is_map(default) ->
        default

      true ->
        nil
    end
  end

  defp same_recovery_plan?(left, right) when is_map(left) and is_map(right), do: left == right
  defp same_recovery_plan?(_left, _right), do: false

  defp recovery_plan_is_generation_relative?(plan) when is_map(plan) do
    targets = Enum.reject(recovery_plan_targets(plan), &is_nil/1)

    targets != [] and
      Enum.all?(targets, fn target -> is_map(target) and target["source"] == "generation" end)
  end

  defp recovery_plan_targets(%{"initial" => initial, "escalation" => escalation}) do
    [initial, escalation]
  end

  defp recovery_plan_targets(%{"target" => target, "jsonRepair" => repair}) do
    [target | recovery_plan_targets(repair)]
  end

  defp recovery_plan_targets(_plan), do: []

  defp default_recovery_target(socket, path, fallback) do
    case get_in(socket.assigns.recovery_policy_default || %{}, path) do
      target when is_map(target) -> target
      _ -> fallback
    end
  end

  defp update_recovery_draft(socket, update_fun) do
    current = socket.assigns.main_form.params["recoveryPolicy"] || %{}

    policy =
      current
      |> ProfileWidgetState.serialize_current_recovery_policy()
      |> update_fun.()
      |> ProfileWidgetState.serialize_recovery_policy()

    params = Map.put(socket.assigns.main_form.params, "recoveryPolicy", policy)

    socket
    |> assign(:main_form, to_form(params, as: :profile))
    |> assign(:active_recovery_policy, policy)
    |> assign(:main_dirty?, true)
    |> mark_main_edit()
    |> notify_profile_runtime(policy)
    |> noreply()
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
    fields = ~w(maxTokens temperature topP topK stopSequences)

    if Enum.any?(fields, &Map.has_key?(incoming, &1)) do
      options = decode_options(params["defaultOptionsJson"])
      options = ProfileWidgetState.patch_options(options, params)
      Map.put(params, "defaultOptionsJson", Jason.encode!(options, pretty: true))
    else
      params
    end
  end

  defp sync_option_fields_from_json(params) do
    options = decode_options(params["defaultOptionsJson"])

    params
    |> Map.put(
      "maxTokens",
      option_text(options["max_tokens"] || ProfileDefaults.default_options()["max_tokens"])
    )
    |> Map.put("temperature", option_text(options["temperature"]))
    |> Map.put("topP", option_text(options["top_p"] || options["topP"]))
    |> Map.put("topK", option_text(options["top_k"] || options["topK"]))
    |> Map.put("stopSequences", stop_text(options["stop"]))
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

  defp save_target_profile(socket, path) do
    keys = String.split(path, ".", trim: true)
    policy = socket.assigns.main_form.params["recoveryPolicy"] || %{}
    target = get_in(policy, keys) |> ProfileWidgetState.serialize_recovery_target()
    profile_id = String.trim(target["profileId"] || "")

    with false <-
           is_nil(Enum.find(socket.assigns.profiles, &(profile_id_from_state(&1) == profile_id))),
         profile_state <-
           Enum.find(socket.assigns.profiles, &(profile_id_from_state(&1) == profile_id)),
         params <- target_profile_save_params(ProfileForm.profile_form(profile_state), target),
         {:ok, payload} <- ProfileForm.profile_payload(params) do
      reference = System.unique_integer([:positive, :monotonic])
      handle = socket.assigns.session_handle

      {:noreply,
       socket
       |> assign(:pending, reference)
       |> assign(:pending_target_path, path)
       |> start_async(
         {:profile_save_target, reference, path},
         Observability.propagate(fn -> HardenAPI.save_profile(handle, profile_id, payload) end)
       )}
    else
      true ->
        {:noreply, assign(socket, :operation_error, "Select a saved profile before saving it.")}

      nil ->
        {:noreply, assign(socket, :operation_error, "Select a saved profile before saving it.")}

      {:error, message} ->
        {:noreply, assign(socket, :operation_error, message)}

      _other ->
        {:noreply, assign(socket, :operation_error, "The shared profile could not be saved.")}
    end
  end

  defp target_profile_save_params(params, target) do
    params =
      case target["providerOptions"] do
        options when is_map(options) ->
          params
          |> Map.put("defaultOptionsJson", Jason.encode!(options, pretty: true))
          |> sync_option_fields_from_json()

        _ ->
          params
      end

    if is_binary(target["modelId"]) and target["modelId"] != "" do
      Map.put(params, "modelId", target["modelId"])
    else
      params
    end
  end

  defp params_with_staged_key(socket) do
    form = socket.assigns.main_form
    staged = socket.assigns.main_staged_key

    params = Map.delete(form.params, "apiKey")
    if staged == "", do: params, else: Map.put(params, "apiKey", staged)
  end

  defp profile_form_for(profiles, id, recovery_policy_default) do
    case Enum.find(profiles, &(profile_id_from_state(&1) == id)) do
      nil -> to_form(ProfileForm.empty_form(recovery_policy_default), as: :profile)
      state -> to_form(ProfileForm.profile_form(state), as: :profile)
    end
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
            scope_id(prefix, safe_field_id(id))
        end

      _ ->
        scope_id(prefix, safe_field_id(id))
    end
  end

  defp safe_field_id(id) do
    id
    |> String.replace(~r/[^A-Za-z0-9_-]+/u, "-")
    |> String.trim("-")
  end

  defp editor_control_id(prefix, suffix, "profile_definition") do
    legacy = %{
      "credential-toggle" => "credential-fold-toggle",
      "options-toggle" => "options-fold-toggle",
      "retry-toggle" => "retry-fold-toggle",
      "pricing-toggle" => "pricing-fold-toggle",
      "credential-status" => "credential-status",
      "credential-drawer" => "credential-drawer",
      "clear-staged-key" => "clear-profile-key",
      "cancel-key" => "cancel-profile-key",
      "stage-key" => "stage-profile-key"
    }

    Map.get(legacy, suffix, scope_id(prefix, suffix))
  end

  defp editor_control_id(prefix, suffix, _context), do: scope_id(prefix, suffix)

  defp base_url_values(profiles) do
    profiles
    |> Enum.map(&get_in(&1, ["profile", "baseUrl"]))
    |> Enum.filter(&(is_binary(&1) and &1 != ""))
    |> Enum.uniq()
    |> Enum.sort()
  end

  defp assign_fold_state(socket, assigns) do
    socket
    |> assign(
      :main_config_open,
      boolean_assign(assigns, :config_open, socket.assigns.main_config_open)
    )
    |> assign(
      :main_credential_open,
      boolean_assign(assigns, :credential_open, socket.assigns.main_credential_open)
    )
    |> assign(
      :main_options_open,
      boolean_assign(assigns, :options_open, socket.assigns.main_options_open)
    )
    |> assign(
      :main_retry_open,
      boolean_assign(assigns, :retry_open, socket.assigns.main_retry_open)
    )
    |> assign(
      :main_pricing_open,
      boolean_assign(assigns, :pricing_open, socket.assigns.main_pricing_open)
    )
    |> assign(
      :fold_disabled,
      boolean_assign(assigns, :fold_disabled, socket.assigns.fold_disabled)
    )
  end

  defp boolean_assign(assigns, key, current) do
    case Map.get(assigns, key) do
      value when is_boolean(value) -> value
      _ -> current
    end
  end

  defp fold_ui_name("options"), do: "modelOptionsOpen"
  defp fold_ui_name("retry"), do: "retryRepairOpen"
  defp fold_ui_name("pricing"), do: "pricingOpen"
  defp fold_ui_name(_), do: nil

  defp notify_profile_runtime(socket, recovery_intent \\ nil) do
    form = socket.assigns.main_form
    options = runtime_provider_options(form.params["defaultOptionsJson"])
    main_requires_save? = profile_requires_save?(socket)

    socket
    |> assign(:main_requires_save?, main_requires_save?)
    |> notify_parent({:profile_widget_provider_options, options})
    |> maybe_notify_recovery(recovery_intent)
    |> notify_parent({:profile_widget_profile_dirty, main_requires_save?})
  end

  defp maybe_notify_recovery(socket, recovery_intent) when is_map(recovery_intent) do
    notify_parent(socket, {:profile_widget_recovery, recovery_intent})
  end

  defp maybe_notify_recovery(socket, _recovery_intent), do: socket

  defp active_recovery_policy(assigns, current_assigns) do
    case Map.fetch(assigns, :recovery_policy) do
      {:ok, policy} when is_map(policy) -> policy
      {:ok, nil} -> Map.get(current_assigns, :active_recovery_policy, %{})
      :error -> Map.get(current_assigns, :active_recovery_policy, %{})
    end
  end

  defp sync_active_recovery_policy(socket) do
    policy = socket.assigns.active_recovery_policy

    update(socket, :main_form, fn form ->
      to_form(Map.put(form.params || %{}, "recoveryPolicy", policy), as: :profile)
    end)
  end

  defp mark_main_edit(socket), do: update(socket, :main_revision, &(&1 + 1))

  defp runtime_provider_options(value) do
    case Jason.decode(value || "{}") do
      {:ok, options} when is_map(options) ->
        options

      _ ->
        %{}
    end
  end

  defp profile_requires_save?(socket) do
    form = socket.assigns.main_form
    params = form.params || %{}
    id = String.trim(params["profileId"] || "")
    staged_key = socket.assigns.main_staged_key
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
          "credentialId" => credential["credentialId"]
        }

        staged_key != "" or ProfileWidgetState.dirty_fields(original, current) != MapSet.new()
    end
  end

  defp profile_dirty_params(params) do
    Map.take(
      params,
      ~w(profileId provider apiInferenceType baseUrl endpointCredentialScope credentialId)
    )
  end

  defp new_profile_dirty_baseline do
    %{
      "profileId" => "",
      "provider" => "",
      "apiInferenceType" => ProfileDefaults.api_inference_type_default(),
      "baseUrl" => "",
      "endpointCredentialScope" => "user",
      "credentialId" => ""
    }
  end

  defp normalize_text(value), do: String.trim(to_string(value || ""))

  defp options_error(value, errors) do
    ProfileForm.field_error(errors, "defaultOptionsJson") ||
      if(ProfileForm.options_valid?(value),
        do: nil,
        else: "Default options JSON must be a valid object."
      )
  end

  defp normalize_base_url(value),
    do: value |> normalize_text() |> String.trim_trailing("/")

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

  defp new_profile_fields_visible?(form, profiles) do
    id = profile_id(form)

    id == "" or
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

  defp web_search_label(true), do: "Disable web search"
  defp web_search_label(false), do: "Enable web search"

  defp web_search_title(true),
    do:
      "Web search is on; use native provider search when supported or Jina fallback. Cache still applies."

  defp web_search_title(false), do: "Web search is off."

  defp next_cache_mode("cache"), do: "refresh"
  defp next_cache_mode("refresh"), do: "cache"
  defp next_cache_mode(_), do: "refresh"

  defp notify_parent(socket, message) do
    send(self(), {:profile_widget, socket.assigns.id_prefix, message})
    socket
  end

  defp maybe_notify_definition_draft(socket, params) do
    if socket.assigns.host_context == "profile_definition" do
      notify_parent(socket, {:profile_widget_draft, params})
    else
      socket
    end
  end

  defp noreply(socket), do: {:noreply, socket}

  def field_error(assigns) do
    ~H"""
    <p :if={@message} class="ullm-field-error">{@message}</p>
    """
  end
end
