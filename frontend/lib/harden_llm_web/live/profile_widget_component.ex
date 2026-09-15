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
    main_credential_open main_options_open main_retry_open main_pricing_open
  )

  @impl true
  def mount(socket) do
    {:ok,
     socket
     |> assign(:initialized?, false)
     |> assign(:web_search, false)
     |> assign(:recovery_policy_default, %{})
     |> assign(:id_prefix, "")
     |> assign(:loaded_profile_id, nil)
     |> assign(:profiles_revision, nil)
     |> assign(:main_form, to_form(ProfilesLive.empty_form(%{}), as: :profile))
     |> assign(:main_dirty?, false)
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
     |> assign(:operation_error, nil)
     |> assign(:delete_confirm, false)}
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

    initial? = not socket.assigns.initialized?

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
      if Map.has_key?(assigns, :recovery_policy) and (initial? or not needs_profile_reset?) do
        update(socket, :main_form, fn form ->
          to_form(Map.put(form.params, "recoveryPolicy", assigns.recovery_policy), as: :profile)
        end)
      else
        socket
      end

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

    socket
    |> assign(:loaded_profile_id, selected_profile_id)
    |> assign(:main_dirty?, false)
    |> assign(:reasoning_effort, reasoning_effort)
    |> notify_parent({:profile_widget_selection, selected_profile_id})
    |> notify_parent({:profile_widget_control, "modelId", model_id})
    |> notify_parent({:profile_widget_control, "reasoningEffort", reasoning_effort})
    |> notify_profile_runtime()
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

  def handle_event("profile-draft-change", %{"profile" => params}, socket) do
    socket |> update_profile_form(params) |> notify_profile_runtime() |> noreply()
  end

  def handle_event("toggle-credential", _params, socket) do
    if socket.assigns.fold_disabled do
      {:noreply, socket}
    else
      key = :main_credential_open
      {:noreply, update(socket, key, &(!&1))}
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

      {:noreply, notify_profile_runtime(socket)}
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
    {:noreply,
     socket
     |> assign(:main_staged_key, "")
     |> assign(:main_credential_open, false)
     |> update_profile_form(%{"apiKey" => ""})
     |> notify_profile_runtime()}
  end

  def handle_event("new-profile", _params, socket) do
    selected_profile_id = ""

    socket
    |> reset_profile_forms(socket.assigns.profiles, selected_profile_id)
    |> assign(:loaded_profile_id, selected_profile_id)
    |> assign(:main_config_open, true)
    |> assign(:main_dirty?, true)
    |> notify_parent({:profile_widget_selection, selected_profile_id})
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

  def handle_event("profile-save", _params, socket) do
    if socket.assigns.pending != nil do
      {:noreply, socket}
    else
      params = params_with_staged_key(socket)

      case ProfilesLive.profile_payload(params) do
        {:ok, payload} ->
          reference = System.unique_integer([:positive, :monotonic])
          handle = socket.assigns.session_handle
          id = params["profileId"] || ""

          {:noreply,
           socket
           |> assign(:pending, reference)
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
    id = profile_id_from_state(profile_state)
    profiles = replace_profile(socket.assigns.profiles, profile_state)

    socket
    |> assign(:pending, nil)
    |> assign(:operation_error, nil)
    |> assign(:field_errors, %{})
    |> assign(:delete_confirm, false)
    |> assign(:profiles, profiles)
    |> assign(:profiles_revision, :erlang.phash2(profiles))
    |> assign(:main_dirty?, false)
    |> assign(:main_form, to_form(ProfilesLive.profile_form(profile_state), as: :profile))
    |> assign(:main_staged_key, "")
    |> notify_profile_runtime()
    |> notify_parent({:profile_widget_profiles, profiles, id})
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
    |> assign(:main_form, to_form(ProfilesLive.profile_form(profile_state), as: :profile))
    |> put_flash(:info, "Model catalog refreshed.")
    |> notify_parent({:profile_widget_profiles, profiles, profile_id_from_state(profile_state)})
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
    |> notify_parent({:profile_widget_profiles, profiles, ""})
    |> noreply()
  end

  def handle_async({operation, _reference}, {:ok, {:error, %APIError{} = error}}, socket)
      when operation in [:profile_save, :profile_refresh, :profile_delete] do
    socket
    |> assign(:pending, nil)
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
    |> assign(:delete_confirm, false)
    |> assign(:operation_error, message)
    |> noreply()
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
          id_prefix={scope_id(@id_prefix, "profile")}
          target={@myself}
          profiles={@profiles}
          field_errors={Map.merge(@field_errors, @recovery_field_errors)}
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
          bundle_upload={@bundle_upload}
          widget_id={@id_prefix}
          pending={@pending}
          delete_confirm={@delete_confirm}
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
  attr(:search_input_id, :string, default: nil)
  attr(:search_field_id, :string, default: nil)
  attr(:search_field_name, :string, default: nil)
  attr(:search_enabled, :boolean, default: false)
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
    <div class={"ullm-profile-row #{if @search_input_id, do: "ullm-profile-row-with-search", else: ""} #{@row_class}"}>
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
        :if={@search_input_id}
        id={@search_input_id}
        type="button"
        class={["ullm-btn", "ullm-profile-search-toggle", @search_enabled && "is-enabled"]}
        phx-click="toggle-web-search"
        phx-target={@target}
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
  attr(:id_prefix, :string, required: true)
  attr(:target, :any, required: true)
  attr(:profiles, :list, required: true)
  attr(:field_errors, :map, default: %{})
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
  attr(:bundle_upload, :any, default: nil)
  attr(:widget_id, :string, default: "")
  attr(:pending, :any, default: nil)
  attr(:delete_confirm, :boolean, default: false)

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
              phx-target={@target}
            >Clear staged key</button>
            <button
              id={"#{@id_prefix}-cancel-key"}
              type="button"
              class="ullm-btn"
              phx-click="cancel-key"
              phx-target={@target}
            >Cancel</button>
            <button
              id={"#{@id_prefix}-stage-key"}
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
            placeholder={ProfileDefaults.model_placeholder()}
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

      <div
        :if={new_profile_fields_visible?(@form, @profiles)}
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
          id={"#{@id_prefix}-options-toggle"}
          type="button"
          class="ullm-btn ullm-options-summary"
          phx-click="toggle-fold"
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

      <section class="ullm-options-fold">
        <button
          id={"#{@id_prefix}-retry-toggle"}
          type="button"
          class="ullm-btn ullm-options-summary"
          phx-click="toggle-fold"
          phx-value-fold="retry"
          phx-target={@target}
          disabled={@fold_disabled}
          aria-expanded={to_string(@retry_open)}
        >Retries &amp; Repair</button>
        <div :if={@retry_open} id={"#{@id_prefix}-retry-repair"} class="ullm-options-body">
          <.recovery_fields
            form={@form}
            id_prefix={@id_prefix}
            target={@target}
            change="profile-draft-change"
            field_errors={@field_errors}
          />
        </div>
      </section>

      <div class="ullm-options-fold ullm-pricing-section">
        <button
          id={"#{@id_prefix}-pricing-toggle"}
          type="button"
          class="ullm-btn ullm-options-summary"
          phx-click="toggle-fold"
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

      <div class="ullm-profile-actions ullm-button-row">
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
            phx-value-widget={@widget_id}
          />
        </label>
        <a id={scope_id(@id_prefix, "export-bundle")} href={~p"/profiles/bundle"} class="ullm-btn">Export Bundle</a>
        <button
          id={scope_id(@id_prefix, "save")}
          type="button"
          class="ullm-btn ullm-btn-primary"
          phx-click="profile-save"
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
          phx-target={@target}
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

  attr(:form, :any, required: true)
  attr(:id_prefix, :string, required: true)
  attr(:target, :any, default: nil)
  attr(:change, :string, default: nil)
  attr(:field_errors, :map, default: %{})

  def recovery_fields(assigns) do
    assigns =
      assigns
      |> assign(:policy, assigns.form.params["recoveryPolicy"] || %{})
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

    ~H"""
    <div id={"#{@id_prefix}-recovery-policy"} class="recovery-policy">
      <.input
        type="checkbox"
        id={"#{@id_prefix}-repair-invalid-output"}
        name={"#{@name}[repairInvalidOutput]"}
        value={@policy["repairInvalidOutput"]}
        errors={
          List.wrap(ProfilesLive.field_error(@field_errors, "recoveryPolicy.repairInvalidOutput"))
        }
        label="Repair invalid structured output"
        info="Uses the original schema to repair invalid JSON or schema failures. Each repair uses the remaining call budget. Turn this off to stop on invalid output."
        phx-change={@change}
        phx-target={@target}
      />
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
      <.field_error message={ProfilesLive.field_error(@field_errors, "recoveryPolicy.retryOn")} />
      <div class="recovery-policy-numbers">
        <.input
          :for={{key, label, info} <- @numbers}
          type="number"
          id={"#{@id_prefix}-recovery-#{key}"}
          name={if key == "maxAttempts", do: "#{@name}[#{key}]", else: "#{@name}[backoff][#{key}]"}
          value={if key == "maxAttempts", do: @policy[key], else: get_in(@policy, ["backoff", key])}
          errors={
            List.wrap(
              ProfilesLive.field_error(
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
      <.field_error message={ProfilesLive.field_error(@field_errors, "recoveryPolicy")} />
      <.field_error message={ProfilesLive.field_error(@field_errors, "recoveryPolicy.backoff")} />
    </div>
    """
  end

  defp reset_profile_forms(socket, profiles, selected_profile_id) do
    form = profile_form_for(profiles, selected_profile_id, socket.assigns.recovery_policy_default)

    socket
    |> assign(:selected_profile_id, selected_profile_id)
    |> assign(:main_form, form)
    |> assign(:main_staged_key, "")
    |> assign(:main_requires_save?, false)
  end

  defp update_profile_form(socket, incoming) do
    params =
      socket.assigns.main_form.params
      |> ProfileWidgetState.merge_draft(incoming)
      |> synchronize_profile_options(incoming)

    socket =
      socket |> assign(:main_form, to_form(params, as: :profile)) |> assign(:main_dirty?, true)

    if Map.has_key?(incoming, "modelId"),
      do: notify_parent(socket, {:profile_widget_control, "modelId", params["modelId"] || ""}),
      else: socket
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

  defp params_with_staged_key(socket) do
    form = socket.assigns.main_form
    staged = socket.assigns.main_staged_key

    params = Map.delete(form.params, "apiKey")
    if staged == "", do: params, else: Map.put(params, "apiKey", staged)
  end

  defp profile_form_for(profiles, id, recovery_policy_default) do
    case Enum.find(profiles, &(profile_id_from_state(&1) == id)) do
      nil -> to_form(ProfilesLive.empty_form(recovery_policy_default), as: :profile)
      state -> to_form(ProfilesLive.profile_form(state), as: :profile)
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
    |> assign(:fold_disabled, Map.get(assigns, :fold_disabled, socket.assigns.fold_disabled))
  end

  defp fold_ui_name("options"), do: "modelOptionsOpen"
  defp fold_ui_name("retry"), do: "retryRepairOpen"
  defp fold_ui_name("pricing"), do: "pricingOpen"
  defp fold_ui_name(_), do: nil

  defp notify_profile_runtime(socket) do
    form = socket.assigns.main_form
    options = runtime_provider_options(form.params["defaultOptionsJson"])
    main_requires_save? = profile_requires_save?(socket)

    socket
    |> assign(:main_requires_save?, main_requires_save?)
    |> notify_parent({:profile_widget_provider_options, options})
    |> notify_parent(
      {:profile_widget_recovery,
       ProfileWidgetState.serialize_recovery_policy(form.params["recoveryPolicy"])}
    )
    |> notify_parent({:profile_widget_profile_dirty, main_requires_save?})
  end

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
    ProfilesLive.field_error(errors, "defaultOptionsJson") ||
      if(ProfilesLive.options_valid?(value),
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

  defp noreply(socket), do: {:noreply, socket}

  def field_error(assigns) do
    ~H"""
    <p :if={@message} class="ullm-field-error">{@message}</p>
    """
  end
end
