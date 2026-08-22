defmodule HardenLlmWeb.ProfileWidgetComponent do
  @moduledoc """
  The provider-neutral LLM profile widget.

  This is deliberately a LiveComponent rather than a page-level editor. The
  compact row and every configuration fold can therefore be embedded beside a
  caller's prompt, output, or other controls without introducing navigation or
  a modal surface.
  """

  use HardenLlmWeb, :live_component

  alias HardenLlmWeb.{APIError, HardenAPI, Observability, ProfilesLive}

  @api_inference_types [
    {"OpenAI Responses", "responses"},
    {"OpenAI Chat Completions", "chat-completions"},
    {"Gemini Generate Content", "gemini-generate-content"},
    {"Anthropic Messages", "anthropic-messages"}
  ]

  @reasoning_options [{"lowest", "L"}, {"middle", "M"}, {"highest", "H"}]

  @fold_keys ~w(
    main_identity_open main_credential_open main_fallback_open main_options_open
    main_retry_open main_pricing_open escalation_identity_open escalation_credential_open
    escalation_fallback_open escalation_options_open escalation_pricing_open
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
     |> assign(:main_staged_key, "")
     |> assign(:escalation_staged_key, "")
     |> assign(:main_config_open, false)
     |> assign(:main_identity_open, false)
     |> assign(:main_credential_open, false)
     |> assign(:main_fallback_open, false)
     |> assign(:main_options_open, false)
     |> assign(:main_retry_open, false)
     |> assign(:main_pricing_open, false)
     |> assign(:escalation_config_open, false)
     |> assign(:escalation_identity_open, false)
     |> assign(:escalation_credential_open, false)
     |> assign(:escalation_fallback_open, false)
     |> assign(:escalation_options_open, false)
     |> assign(:escalation_pricing_open, false)
     |> assign(:escalation_cache_mode, "cache")
     |> assign(:api_inference_types, @api_inference_types)
     |> assign(:pending, nil)
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

    {:ok,
     socket
     |> assign(
       :reasoning_effort,
       normalize_reasoning_effort(
         profiles,
         selected_profile_id,
         Map.get(assigns, :reasoning_effort, "lowest")
       )
     )
     |> assign(:cache_mode, Map.get(assigns, :cache_mode, "off"))}
  end

  @impl true
  def handle_event("toggle-config", _params, socket) do
    open = not socket.assigns.main_config_open

    socket
    |> assign(:main_config_open, open)
    |> notify_parent({:profile_widget_ui, "llmProfileConfigOpen", open})
    |> noreply()
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
    |> noreply()
  end

  def handle_event("workspace-control", %{"run" => params}, socket) do
    socket =
      Enum.reduce(["reasoningEffort", "cacheMode"], socket, fn key, acc ->
        case params[key] do
          value when is_binary(value) and value != "" ->
            notify_parent(acc, {:profile_widget_control, key, value})

          _ ->
            acc
        end
      end)

    {:noreply, socket}
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

    if key in @fold_keys do
      atom_key = String.to_existing_atom(key)
      {:noreply, update(socket, atom_key, &(!&1))}
    else
      {:noreply, socket}
    end
  end

  def handle_event("toggle-escalation-config", _params, socket) do
    {:noreply, update(socket, :escalation_config_open, &(!&1))}
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

    {:noreply, socket}
  end

  def handle_event("add-backup", %{"kind" => kind} = params, socket)
      when kind in ["main", "escalation"] do
    add_backup(socket, String.to_existing_atom(kind), params)
  end

  def handle_event("add-backup-main", params, socket), do: add_backup(socket, :main, params)

  def handle_event("add-backup-escalation", params, socket),
    do: add_backup(socket, :escalation, params)

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
       move_backup(backups, parse_index(index), direction)
     end)}
  end

  def handle_event("toggle-credential", %{"kind" => kind}, socket)
      when kind in ["main", "escalation"] do
    key = String.to_existing_atom("#{kind}_credential_open")
    {:noreply, update(socket, key, &(!&1))}
  end

  def handle_event("stage-key", %{"kind" => kind}, socket)
      when kind in ["main", "escalation"] do
    kind_atom = String.to_existing_atom(kind)
    form = form_for(socket, kind_atom)
    key = String.trim(form.params["apiKey"] || "")

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

      {:noreply, socket}
    end
  end

  def handle_event("clear-staged-key", %{"kind" => kind}, socket)
      when kind in ["main", "escalation"] do
    kind_atom = String.to_existing_atom(kind)

    {:noreply,
     socket
     |> assign(String.to_existing_atom("#{kind}_staged_key"), "")
     |> update_profile_form(kind_atom, %{"apiKey" => ""})}
  end

  def handle_event("cancel-key", %{"kind" => kind}, socket)
      when kind in ["main", "escalation"] do
    kind_atom = String.to_existing_atom(kind)

    {:noreply,
     socket
     |> assign(String.to_existing_atom("#{kind}_staged_key"), "")
     |> assign(String.to_existing_atom("#{kind}_credential_open"), false)
     |> update_profile_form(kind_atom, %{"apiKey" => ""})}
  end

  def handle_event("new-profile", _params, socket) do
    selected_profile_id = ""

    socket
    |> reset_profile_forms(socket.assigns.profiles, selected_profile_id)
    |> assign(:loaded_profile_id, selected_profile_id)
    |> assign(:main_config_open, true)
    |> assign(:main_identity_open, true)
    |> assign(:main_dirty?, true)
    |> notify_parent({:profile_widget_selection, selected_profile_id})
    |> noreply()
  end

  def handle_event("profile-confirm-delete", %{"kind" => kind}, socket)
      when kind in ["main", "escalation"], do: {:noreply, assign(socket, :delete_kind, kind)}

  def handle_event("profile-cancel-delete", _params, socket),
    do: {:noreply, assign(socket, :delete_kind, nil)}

  def handle_event("profile-delete", %{"kind" => kind}, %{assigns: %{pending: nil}} = socket)
      when kind in ["main", "escalation"] do
    kind_atom = String.to_existing_atom(kind)
    id = profile_id(form_for(socket, kind_atom))

    if id == "" do
      {:noreply, assign(socket, :delete_kind, nil)}
    else
      reference = System.unique_integer([:positive, :monotonic])
      handle = socket.assigns.session_handle

      {:noreply,
       socket
       |> assign(:pending, reference)
       |> start_async(
         {:profile_delete, reference, kind},
         Observability.propagate(fn -> HardenAPI.delete_profile(handle, id) end)
       )}
    end
  end

  def handle_event("profile-save", %{"kind" => kind}, %{assigns: %{pending: nil}} = socket)
      when kind in ["main", "escalation"] do
    kind_atom = String.to_existing_atom(kind)
    params = params_with_staged_key(socket, kind_atom)

    case ProfilesLive.profile_payload(params) do
      {:ok, payload} ->
        reference = System.unique_integer([:positive, :monotonic])
        handle = socket.assigns.session_handle
        id = params["profileId"] || ""

        {:noreply,
         socket
         |> assign(:pending, reference)
         |> assign_form(kind_atom, to_form(params, as: form_as(kind_atom)))
         |> start_async(
           {:profile_save, reference, kind},
           Observability.propagate(fn -> HardenAPI.save_profile(handle, id, payload) end)
         )}

      {:error, message} ->
        {:noreply, assign(socket, :operation_error, message)}
    end
  end

  def handle_event("profile-save", _params, socket), do: {:noreply, socket}

  def handle_event("profile-refresh", %{"kind" => kind}, %{assigns: %{pending: nil}} = socket)
      when kind in ["main", "escalation"] do
    kind_atom = String.to_existing_atom(kind)
    id = profile_id(form_for(socket, kind_atom))

    if id == "" do
      {:noreply, socket}
    else
      reference = System.unique_integer([:positive, :monotonic])
      handle = socket.assigns.session_handle

      {:noreply,
       socket
       |> assign(:pending, reference)
       |> start_async(
         {:profile_refresh, reference, kind},
         Observability.propagate(fn -> HardenAPI.refresh_profile_models(handle, id) end)
       )}
    end
  end

  def handle_event("profile-refresh", _params, socket), do: {:noreply, socket}

  defp add_backup(socket, kind, params) do
    id =
      params["id"] || params["fallbackProfile"] ||
        get_in(params, [Atom.to_string(kind), "fallbackProfile"]) || ""

    if String.trim(id) == "" do
      {:noreply, socket}
    else
      {:noreply, update_backup(socket, kind, fn backups -> backups ++ [id] end)}
    end
  end

  @impl true
  def handle_async(
        {:profile_save, reference, kind},
        {:ok, {:ok, profile_state, _state}},
        %{assigns: %{pending: reference}} = socket
      ) do
    id = get_in(profile_state, ["profile", "llmProfile"]) || ""
    profiles = replace_profile(socket.assigns.profiles, profile_state)
    kind_atom = String.to_existing_atom(kind)

    socket =
      socket
      |> assign(:pending, nil)
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
      |> assign(String.to_existing_atom("#{kind}_staged_key"), "")

    socket =
      if kind == "escalation" do
        update_profile_form(socket, :main, %{"escalationProfile" => id})
      else
        socket
      end

    socket
    |> notify_parent(
      {:profile_widget_profiles, profiles,
       if(kind == "main", do: id, else: socket.assigns.selected_profile_id)}
    )
    |> noreply()
  end

  def handle_async(
        {:profile_save, reference, _kind},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:operation_error, error.message)}
  end

  def handle_async(
        {:profile_save, reference, _kind},
        _result,
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:operation_error, "The profile could not be saved.")}
  end

  def handle_async(
        {:profile_refresh, reference, kind},
        {:ok, {:ok, profile_state, _state}},
        %{assigns: %{pending: reference}} = socket
      ) do
    profiles = replace_profile(socket.assigns.profiles, profile_state)
    kind_atom = String.to_existing_atom(kind)
    id = get_in(profile_state, ["profile", "llmProfile"]) || ""

    socket =
      socket
      |> assign(:pending, nil)
      |> assign(:profiles, profiles)
      |> assign(:profiles_revision, :erlang.phash2(profiles))
      |> assign(:operation_error, nil)
      |> assign_form(
        kind_atom,
        to_form(ProfilesLive.profile_form(profile_state), as: form_as(kind_atom))
      )
      |> put_flash(:info, "Model catalog refreshed.")

    socket
    |> notify_parent({:profile_widget_profiles, profiles, id})
    |> noreply()
  end

  def handle_async(
        {:profile_refresh, reference, _kind},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:operation_error, error.message)}
  end

  def handle_async(
        {:profile_refresh, reference, _kind},
        _result,
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:operation_error, "The model catalog could not be refreshed.")}
  end

  def handle_async(
        {:profile_delete, reference, kind},
        {:ok, {:ok, _result, _state}},
        %{assigns: %{pending: reference}} = socket
      ) do
    kind_atom = String.to_existing_atom(kind)
    id = profile_id(form_for(socket, kind_atom))
    profiles = Enum.reject(socket.assigns.profiles, &(profile_id_from_state(&1) == id))

    socket =
      socket
      |> assign(:pending, nil)
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

  def handle_async(
        {:profile_delete, reference, _kind},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:delete_kind, nil)
     |> assign(:operation_error, error.message)}
  end

  def handle_async(
        {:profile_delete, reference, _kind},
        _result,
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:delete_kind, nil)
     |> assign(:operation_error, "The profile could not be deleted.")}
  end

  def handle_async(_operation, _result, socket), do: {:noreply, socket}

  @impl true
  def render(assigns) do
    ~H"""
    <section id={@id} class="ullm-widget ullm-model-config-widget" aria-label="LLM model config">
      <div class="ullm-profile-row">
        <span class="ullm-profile-category" title="LLM">LLM</span>
        <div class="ullm-profile-picker">
          <label for={scope_id(@id_prefix, "run_selectedProfileId")} class="ullm-profile-label">
            <span aria-hidden="true">🤖</span><span class="ullm-sr-only">LLM Profile</span>
          </label>
          <input
            id={scope_id(@id_prefix, "run_selectedProfileId")}
            name="run[selectedProfileId]"
            value={@selected_profile_id}
            list={scope_id(@id_prefix, "workspace-profile-options")}
            autocomplete="off"
            required
            aria-label="LLM Profile"
            class="ullm-input ullm-profile-select"
            phx-change="select-profile"
            phx-target={@myself}
          />
          <datalist id={scope_id(@id_prefix, "workspace-profile-options")}>
            <option :for={profile <- @profiles} value={profile_id_from_state(profile)} />
          </datalist>
        </div>
        <div class="ullm-reasoning-field">
          <label for="workspace-reasoning" class="ullm-profile-label">
            <span aria-hidden="true">🧠</span><span class="ullm-sr-only">Reasoning</span>
          </label>
          <select
            id={scope_id(@id_prefix, "workspace-reasoning")}
            name="run[reasoningEffort]"
            aria-label="Reasoning"
            class="ullm-input ullm-compact-select"
            phx-change="workspace-control"
            phx-target={@myself}
            disabled={reasoning_options(@profiles, @selected_profile_id) == []}
          >
            <option
              :if={reasoning_options(@profiles, @selected_profile_id) == []}
              value=""
              selected
            >
              —
            </option>
            <option
              :for={{value, label} <- reasoning_options(@profiles, @selected_profile_id)}
              value={value}
              selected={@reasoning_effort == value}
            >
              {label}
            </option>
          </select>
        </div>
        <button
          id={scope_id(@id_prefix, "workspace-cache-toggle")}
          type="button"
          class="ullm-btn ullm-profile-cache-toggle"
          phx-click="toggle-cache"
          phx-target={@myself}
          aria-label={cache_label(@cache_mode)}
          aria-pressed={to_string(@cache_mode in ["cache", "refresh"])}
          title={cache_label(@cache_mode)}
        >💾</button>
        <select
          id={scope_id(@id_prefix, "workspace-cache")}
          name="run[cacheMode]"
          class="ullm-sr-only"
          aria-label="Cache mode"
          phx-change="workspace-control"
          phx-target={@myself}
        >
          <option value="off" selected={@cache_mode in [nil, "off"]}>off</option>
          <option value="cache" selected={@cache_mode == "cache"}>cache</option>
          <option value="refresh" selected={@cache_mode == "refresh"}>refresh</option>
        </select>
        <input
          id={scope_id(@id_prefix, "run_modelId")}
          name="run[modelId]"
          value={@model_id}
          class="ullm-sr-only"
          autocomplete="off"
        />
        <button
          id={scope_id(@id_prefix, "model-config-toggle")}
          type="button"
          class="ullm-btn ullm-profile-config-toggle"
          phx-click="toggle-config"
          phx-target={@myself}
          aria-expanded={to_string(@main_config_open)}
          aria-label="Profile config"
        >⚙</button>
      </div>

      <div :if={@operation_error} id="profile-widget-error" role="alert" class="ullm-widget-error">
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
          field_errors={%{}}
          api_inference_types={@api_inference_types}
          credential_open={@main_credential_open}
          identity_open={@main_identity_open}
          fallback_open={@main_fallback_open}
          options_open={@main_options_open}
          retry_open={@main_retry_open}
          pricing_open={@main_pricing_open}
          staged_key={@main_staged_key}
          cache_mode={@cache_mode}
          config_open={@escalation_config_open}
          include_retry={true}
          bundle_upload={@bundle_upload}
          pending={@pending}
          delete_kind={@delete_kind}
          escalation_form={@escalation_form}
          escalation_config_open={@escalation_config_open}
          escalation_credential_open={@escalation_credential_open}
          escalation_identity_open={@escalation_identity_open}
          escalation_fallback_open={@escalation_fallback_open}
          escalation_options_open={@escalation_options_open}
          escalation_pricing_open={@escalation_pricing_open}
          escalation_staged_key={@escalation_staged_key}
        />
      </div>
    </section>
    """
  end

  attr :form, :any, required: true
  attr :kind, :string, required: true
  attr :id_prefix, :string, required: true
  attr :target, :any, required: true
  attr :profiles, :list, required: true
  attr :field_errors, :map, default: %{}
  attr :api_inference_types, :list, default: @api_inference_types
  attr :credential_open, :boolean, default: false
  attr :identity_open, :boolean, default: false
  attr :fallback_open, :boolean, default: false
  attr :options_open, :boolean, default: false
  attr :retry_open, :boolean, default: false
  attr :pricing_open, :boolean, default: false
  attr :staged_key, :string, default: ""
  attr :cache_mode, :string, default: "cache"
  attr :config_open, :boolean, default: false
  attr :include_retry, :boolean, default: true
  attr :bundle_upload, :any, default: nil
  attr :pending, :any, default: nil
  attr :delete_kind, :any, default: nil
  attr :escalation_form, :any, default: nil
  attr :escalation_config_open, :boolean, default: false
  attr :escalation_credential_open, :boolean, default: false
  attr :escalation_identity_open, :boolean, default: false
  attr :escalation_fallback_open, :boolean, default: false
  attr :escalation_options_open, :boolean, default: false
  attr :escalation_pricing_open, :boolean, default: false
  attr :escalation_staged_key, :string, default: ""
  attr :escalation_id_prefix, :string, default: "escalation"

  def profile_editor(assigns) do
    ~H"""
    <div id={"#{@id_prefix}-config-fields"} class="ullm-form-grid">
      <div class="ullm-options-grid">
        <.input
          field={@form[:apiInferenceType]}
          label="API Inference Type"
          type="select"
          options={@api_inference_types}
          class="ullm-input"
          phx-change="profile-draft-change"
          phx-target={@target}
        />
        <.input
          field={@form[:baseUrl]}
          type="url"
          label="Base URL"
          required
          list={"#{@id_prefix}-base-url-options"}
          class="ullm-input ullm-input-mono"
          phx-change="profile-draft-change"
          phx-target={@target}
        />
        <datalist id={"#{@id_prefix}-base-url-options"}>
          <option :for={profile <- @profiles} value={get_in(profile, ["profile", "baseUrl"])} />
        </datalist>
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
            aria-expanded={to_string(@credential_open)}
          >{if @credential_open,
            do: "Hide key",
            else: if(credential_available?(@form, @staged_key), do: "Replace key", else: "Set key")}</button>
        </div>
        <div
          :if={@credential_open}
          id={"#{@id_prefix}-credential-drawer"}
          class="ullm-credential-drawer"
        >
          <.input
            field={@form[:credentialId]}
            label="Credential ID"
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:endpointCredentialScope]}
            type="select"
            label="Credential scope"
            options={[{"User", "user"}, {"Global", "global"}]}
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:apiKey]}
            type="password"
            label="Replacement API Key"
            autocomplete="new-password"
            class="ullm-input ullm-input-mono"
            phx-change="profile-draft-change"
            phx-target={@target}
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
              disabled={String.trim(@form[:apiKey].value || "") == ""}
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
          disabled={@pending != nil or profile_id(@form) == ""}
        >Refresh Models</button>
        <div class="ullm-model-slot-field">
          <.input
            field={@form[:modelId]}
            label="Model ID"
            list={"#{@id_prefix}-model-options"}
            class="ullm-input ullm-input-mono"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <datalist id={"#{@id_prefix}-model-options"}>
            <option :for={model <- models_for(@profiles, profile_id(@form))} value={model["id"]}>
              {model["label"] || model["id"]}
            </option>
          </datalist>
          <p class="ullm-field-help">{length(models_for(@profiles, profile_id(@form)))} options</p>
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
            phx-click="toggle-fold"
            phx-value-kind={@kind}
            phx-value-fold="fallback"
            phx-target={@target}
            aria-expanded={to_string(@fallback_open)}
          >+ Add Fallback LLM</button>
        </div>
        <div
          :if={@fallback_open}
          id={"#{@id_prefix}-fallback-options"}
          class="ullm-backup-profile-row"
        >
          <select
            id={"#{@id_prefix}-fallback-picker"}
            name="fallbackProfile"
            class="ullm-input ullm-backup-profile-select"
            aria-label="Add Fallback LLM"
            phx-change={if @kind == "main", do: "add-backup-main", else: "add-backup-escalation"}
            phx-target={@target}
          >
            <option value="">Choose a profile below</option>
            <option
              :for={profile <- available_backup_profiles(@profiles, profile_id(@form))}
              value={profile_id_from_state(profile)}
            >
              {profile_id_from_state(profile)}
            </option>
          </select>
          <div class="ullm-backup-profile-actions">
            <button
              :for={profile <- available_backup_profiles(@profiles, profile_id(@form))}
              type="button"
              class="ullm-btn ullm-btn-tiny"
              phx-click="add-backup"
              phx-value-kind={@kind}
              phx-value-id={profile_id_from_state(profile)}
              phx-target={@target}
            >{profile_id_from_state(profile)}</button>
          </div>
        </div>
        <ol id={"#{@id_prefix}-fallback-list"} class="ullm-backup-profile-list">
          <li :for={
            {backup, index} <- Enum.with_index(ProfilesLive.backup_list(@form[:backupProfiles].value))
          }>
            <span><span class="ullm-backup-index">{index + 1}.</span>{backup}</span>
            <span class="ullm-backup-profile-actions">
              <button
                type="button"
                class="ullm-btn ullm-btn-tiny"
                phx-click="move-backup"
                phx-value-kind={@kind}
                phx-value-index={index}
                phx-value-direction="up"
                phx-target={@target}
                aria-label={"Move #{backup} up"}
              >↑</button>
              <button
                type="button"
                class="ullm-btn ullm-btn-tiny"
                phx-click="move-backup"
                phx-value-kind={@kind}
                phx-value-index={index}
                phx-value-direction="down"
                phx-target={@target}
                aria-label={"Move #{backup} down"}
              >↓</button>
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
          </li>
        </ol>
      </section>

      <div class="ullm-options-fold">
        <button
          id={"#{@id_prefix}-identity-toggle"}
          type="button"
          class="ullm-btn ullm-options-summary"
          phx-click="toggle-fold"
          phx-value-kind={@kind}
          phx-value-fold="identity"
          phx-target={@target}
          aria-expanded={to_string(@identity_open)}
        >Profile identity</button>
        <div
          :if={@identity_open}
          id={"#{@id_prefix}-identity"}
          class="ullm-options-body ullm-options-grid"
        >
          <.input
            field={@form[:profileId]}
            label="Profile"
            required
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:provider]}
            label="Provider family"
            required
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:supportsTemperature]}
            type="checkbox"
            label="Supports temperature"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:supportsContractedStructuredOutput]}
            type="checkbox"
            label="Supports contracted structured output"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
        </div>
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
          aria-expanded={to_string(@options_open)}
        >Options</button>
        <div :if={@options_open} id={"#{@id_prefix}-options"} class="ullm-options-body">
          <div class="ullm-options-grid">
            <.input
              field={@form[:maxTokens]}
              type="number"
              label="Max Output Tokens"
              min="0"
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:temperature]}
              type="number"
              label="Temperature"
              min="0"
              step="any"
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:topP]}
              type="number"
              label="Top P"
              min="0"
              step="any"
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:topK]}
              type="number"
              label="Top K"
              min="0"
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
          </div>
          <.input
            field={@form[:stopSequences]}
            type="textarea"
            label="Stop Sequences"
            placeholder="one sequence per line"
            rows="2"
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:defaultOptionsJson]}
            type="textarea"
            label="Default Options JSON"
            rows="5"
            class="ullm-input ullm-input-mono"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
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
          aria-expanded={to_string(@retry_open)}
        >Retries &amp; Repair</button>
        <div :if={@retry_open} id={"#{@id_prefix}-retry-repair"} class="ullm-options-body">
          <div class="ullm-checkbox-grid">
            <.input
              field={@form[:structuredRepairRetryEnabled]}
              type="checkbox"
              label="Structured Repair"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:enableRetryOn429]}
              type="checkbox"
              label="Rate Limits"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:enableRetryOn5xx]}
              type="checkbox"
              label="Server Errors"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:enableRetryOnNetworkError]}
              type="checkbox"
              label="Network Errors"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:enableRetryOnParseError]}
              type="checkbox"
              label="Parse / Schema Errors"
              disabled={truthy?(@form[:structuredRepairRetryEnabled].value)}
              phx-change="profile-draft-change"
              phx-target={@target}
            />
          </div>
          <div class="ullm-options-grid">
            <.input
              field={@form[:retryMaxAttempts]}
              type="number"
              label="Max Attempts"
              min="1"
              max="10"
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:retryBaseDelayMs]}
              type="number"
              label="Base Delay Ms"
              min="0"
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:retryMaxDelayMs]}
              type="number"
              label="Max Delay Ms"
              min="0"
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
            <.input
              field={@form[:escalationAttempt]}
              type="number"
              label="Starting Attempt"
              min="2"
              max="10"
              class="ullm-input"
              phx-change="profile-draft-change"
              phx-target={@target}
            />
          </div>
          <div class="ullm-escalation-profile-editor">
            <div class="ullm-profile-row ullm-escalation-profile-row">
              <span class="ullm-profile-category" title="Escalation Model">Escalation Model</span>
              <div class="ullm-profile-picker">
                <label for={"#{@id_prefix}-escalation-profile"} class="ullm-profile-label"><span aria-hidden="true">🤖</span><span class="ullm-sr-only">LLM Profile</span></label>
                <input
                  id={"#{@id_prefix}-escalation-profile"}
                  name={"#{@form.name}[escalationProfile]"}
                  value={@form[:escalationProfile].value}
                  list={"#{@id_prefix}-escalation-profile-options"}
                  aria-label="LLM Profile"
                  class="ullm-input"
                  phx-change="profile-draft-change"
                  phx-target={@target}
                />
                <datalist id={"#{@id_prefix}-escalation-profile-options"}><option
                  :for={profile <- @profiles}
                  value={profile_id_from_state(profile)}
                /></datalist>
              </div>
              <div class="ullm-reasoning-field">
                <label for={"#{@id_prefix}-escalation-reasoning"} class="ullm-profile-label"><span aria-hidden="true">🧠</span><span class="ullm-sr-only">Reasoning</span></label>
                <select
                  id={"#{@id_prefix}-escalation-reasoning"}
                  name={"#{@form.name}[escalationReasoning]"}
                  class="ullm-input ullm-compact-select"
                  aria-label="Reasoning"
                  phx-change="profile-draft-change"
                  phx-target={@target}
                  disabled={escalation_reasoning_options(@profiles, @form) == []}
                >
                  <option
                    :if={escalation_reasoning_options(@profiles, @form) == []}
                    value=""
                    selected
                  >
                    —
                  </option>
                  <option
                    :for={{value, label} <- escalation_reasoning_options(@profiles, @form)}
                    value={value}
                    selected={@form[:escalationReasoning].value == value}
                  >
                    {label}
                  </option>
                </select>
              </div>
              <button
                type="button"
                class="ullm-btn ullm-profile-cache-toggle"
                phx-click="toggle-cache"
                phx-target={@target}
                aria-label={cache_label(@cache_mode)}
                aria-pressed={to_string(@cache_mode in ["cache", "refresh"])}
                title={cache_label(@cache_mode)}
              >💾</button>
              <button
                type="button"
                id={"#{@id_prefix}-escalation-config-toggle"}
                class="ullm-btn ullm-profile-config-toggle"
                phx-click="toggle-escalation-config"
                phx-target={@target}
                aria-expanded={to_string(@config_open)}
                aria-label="Profile config"
              >⚙</button>
            </div>
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
                field_errors={@field_errors}
                credential_open={@escalation_credential_open}
                identity_open={@escalation_identity_open}
                fallback_open={@escalation_fallback_open}
                options_open={@escalation_options_open}
                retry_open={false}
                pricing_open={@escalation_pricing_open}
                staged_key={@escalation_staged_key}
                cache_mode={@cache_mode}
                config_open={false}
                include_retry={false}
                bundle_upload={nil}
                pending={@pending}
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
          aria-expanded={to_string(@pricing_open)}
        >Pricing</button>
        <div
          :if={@pricing_open}
          id={"#{@id_prefix}-pricing"}
          class="ullm-options-body ullm-options-grid"
        >
          <.input
            field={@form[:pricingInput]}
            type="number"
            label="Input $/1M tokens"
            min="0"
            step="any"
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:pricingOutput]}
            type="number"
            label="Output $/1M tokens"
            min="0"
            step="any"
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:pricingCacheRead]}
            type="number"
            label="Cache read $/1M tokens"
            min="0"
            step="any"
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:pricingCacheWrite]}
            type="number"
            label="Cache write $/1M tokens"
            min="0"
            step="any"
            class="ullm-input"
            phx-change="profile-draft-change"
            phx-target={@target}
          />
          <.input
            field={@form[:pricingReasoning]}
            type="number"
            label="Reasoning output $/1M tokens"
            min="0"
            step="any"
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
          Import Bundle <.live_file_input :if={@bundle_upload} upload={@bundle_upload} />
        </label>
        <button
          :if={@bundle_upload}
          id={scope_id(@id_prefix, "import-bundle")}
          type="button"
          class="ullm-btn"
          phx-click="import-bundle"
        >Import</button>
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
        >Save Profile</button>
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
        >Save Profile</button>
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
    escalation_id = escalation_profile_id(main_form, selected_profile_id)

    socket
    |> assign(:selected_profile_id, selected_profile_id)
    |> assign(:main_form, main_form)
    |> assign(:escalation_form, profile_form_for(profiles, escalation_id, :escalation))
    |> assign(:main_staged_key, "")
    |> assign(:escalation_staged_key, "")
    |> assign(:escalation_cache_mode, "cache")
  end

  defp update_profile_form(socket, :main, incoming) do
    current = socket.assigns.main_form.params || %{}

    params =
      current
      |> Map.merge(incoming)
      |> synchronize_profile_options(incoming)

    form = to_form(params, as: :profile)

    escalation_id =
      String.trim(params["escalationProfile"] || socket.assigns.selected_profile_id || "")

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
    option_keys = ~w(maxTokens temperature topP topK stopSequences)
    retry_keys = ~w(
      structuredRepairRetryEnabled enableRetryOn429 enableRetryOn5xx
      enableRetryOnNetworkError enableRetryOnParseError retryMaxAttempts
      retryBaseDelayMs retryMaxDelayMs escalationAttempt escalationProfile
      escalationReasoning
    )

    if Enum.any?(option_keys ++ retry_keys, &Map.has_key?(incoming, &1)) do
      options = decode_options(params["defaultOptionsJson"])
      options = sync_scalar_options(options, params)

      options =
        if Enum.any?(retry_keys, &Map.has_key?(incoming, &1)),
          do: sync_retry_options(options, params),
          else: options

      Map.put(params, "defaultOptionsJson", Jason.encode!(options, pretty: true))
    else
      params
    end
  end

  defp sync_scalar_options(options, params) do
    options
    |> sync_number_option("max_tokens", params, "maxTokens", :integer)
    |> sync_number_option("temperature", params, "temperature", :float)
    |> sync_number_option("top_p", params, "topP", :float)
    |> sync_number_option("top_k", params, "topK", :integer)
    |> sync_stop_option(params)
  end

  defp sync_number_option(options, key, params, field, kind) do
    if Map.has_key?(params, field) do
      case parse_option_number(params[field], kind) do
        {:ok, nil} -> Map.delete(options, key)
        {:ok, value} -> Map.put(options, key, value)
        :error -> options
      end
    else
      options
    end
  end

  defp sync_stop_option(options, params) do
    if Map.has_key?(params, "stopSequences") do
      stops =
        params["stopSequences"]
        |> to_string()
        |> String.split("\n")
        |> Enum.map(&String.trim/1)
        |> Enum.reject(&(&1 == ""))

      if stops == [], do: Map.delete(options, "stop"), else: Map.put(options, "stop", stops)
    else
      options
    end
  end

  defp sync_retry_options(options, params) do
    existing = options["structuredRepairRetry"]

    repair_enabled =
      if Map.has_key?(params, "structuredRepairRetryEnabled"),
        do: truthy?(params["structuredRepairRetryEnabled"]),
        else: is_map(existing) and existing["enabled"] == true

    if not repair_enabled do
      Map.put(options, "structuredRepairRetry", false)
    else
      retry = if is_map(existing), do: existing, else: %{}

      retry =
        retry
        |> Map.put("enabled", true)
        |> sync_retry_toggle(params, "enableRetryOn429")
        |> sync_retry_toggle(params, "enableRetryOn5xx")
        |> sync_retry_toggle(params, "enableRetryOnNetworkError")
        |> Map.delete("enableRetryOnParseError")
        |> sync_retry_number(params, "maxAttempts", "retryMaxAttempts", 4)
        |> sync_retry_number(params, "baseDelayMs", "retryBaseDelayMs", 500)
        |> sync_retry_number(params, "maxDelayMs", "retryMaxDelayMs", 8000)

      escalation = if is_map(retry["escalation"]), do: retry["escalation"], else: %{}

      escalation =
        escalation
        |> sync_retry_number(params, "attempt", "escalationAttempt", 3)
        |> sync_retry_text(params, "llmProfile", "escalationProfile")
        |> sync_retry_text(params, "reasoningEffort", "escalationReasoning")

      Map.put(options, "structuredRepairRetry", Map.put(retry, "escalation", escalation))
    end
  end

  defp sync_retry_toggle(retry, params, field) do
    if Map.has_key?(params, field) do
      if truthy?(params[field]), do: Map.delete(retry, field), else: Map.put(retry, field, false)
    else
      retry
    end
  end

  defp sync_retry_number(retry, params, key, field, default) do
    if Map.has_key?(params, field) do
      case parse_option_number(params[field], :integer) do
        {:ok, nil} -> Map.put(retry, key, default)
        {:ok, value} -> Map.put(retry, key, value)
        :error -> retry
      end
    else
      retry
    end
  end

  defp sync_retry_text(retry, params, key, field) do
    if Map.has_key?(params, field),
      do: Map.put(retry, key, to_string(params[field] || "")),
      else: retry
  end

  defp sync_option_fields_from_json(params) do
    options = decode_options(params["defaultOptionsJson"])
    retry = options["structuredRepairRetry"]
    retry_map = if is_map(retry), do: retry, else: %{}
    escalation = if is_map(retry_map["escalation"]), do: retry_map["escalation"], else: %{}

    params
    |> Map.put("maxTokens", option_text(options["max_tokens"]))
    |> Map.put("temperature", option_text(options["temperature"]))
    |> Map.put("topP", option_text(options["top_p"]))
    |> Map.put("topK", option_text(options["top_k"]))
    |> Map.put("stopSequences", stop_text(options["stop"]))
    |> Map.put(
      "structuredRepairRetryEnabled",
      to_string(retry != false and retry_map["enabled"] == true)
    )
    |> Map.put("enableRetryOn429", to_string(retry_map["enableRetryOn429"] != false))
    |> Map.put("enableRetryOn5xx", to_string(retry_map["enableRetryOn5xx"] != false))
    |> Map.put(
      "enableRetryOnNetworkError",
      to_string(retry_map["enableRetryOnNetworkError"] != false)
    )
    |> Map.put(
      "enableRetryOnParseError",
      to_string(retry != false and retry_map["enableRetryOnParseError"] != false)
    )
    |> Map.put("retryMaxAttempts", option_text(retry_map["maxAttempts"] || 4))
    |> Map.put("retryBaseDelayMs", option_text(retry_map["baseDelayMs"] || 500))
    |> Map.put("retryMaxDelayMs", option_text(retry_map["maxDelayMs"] || 8000))
    |> Map.put("escalationAttempt", option_text(escalation["attempt"] || 3))
    |> Map.put("escalationProfile", escalation["llmProfile"] || "")
    |> Map.put("escalationReasoning", escalation["reasoningEffort"] || "highest")
  end

  defp decode_options(value) do
    case Jason.decode(value || "{}") do
      {:ok, options} when is_map(options) -> options
      _ -> %{}
    end
  end

  defp parse_option_number(value, _kind) when value in [nil, ""], do: {:ok, nil}

  defp parse_option_number(value, :integer) do
    case Integer.parse(to_string(value)) do
      {number, ""} -> {:ok, number}
      _ -> :error
    end
  end

  defp parse_option_number(value, :float) do
    case Float.parse(to_string(value)) do
      {number, ""} -> {:ok, number}
      _ -> :error
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
    backups = ProfilesLive.backup_list(form.params["backupProfiles"])

    next =
      backups |> update.() |> Enum.map(&String.trim/1) |> Enum.reject(&(&1 == "")) |> Enum.uniq()

    assign_form(
      socket,
      kind,
      to_form(Map.put(form.params, "backupProfiles", Enum.join(next, ", ")), as: form_as(kind))
    )
  end

  defp form_for(socket, :main), do: socket.assigns.main_form
  defp form_for(socket, :escalation), do: socket.assigns.escalation_form
  defp assign_form(socket, :main, form), do: assign(socket, :main_form, form)
  defp assign_form(socket, :escalation, form), do: assign(socket, :escalation_form, form)
  defp form_as(:main), do: :profile
  defp form_as(:escalation), do: :escalation

  defp params_with_staged_key(socket, kind) do
    form = form_for(socket, kind)
    staged = Map.get(socket.assigns, String.to_existing_atom("#{kind}_staged_key"), "")
    if staged == "", do: form.params, else: Map.put(form.params, "apiKey", staged)
  end

  defp profile_form_for(_profiles, "", kind), do: to_form(ProfilesLive.empty_form(), as: kind)

  defp profile_form_for(profiles, id, kind) do
    state = Enum.find(profiles, &(profile_id_from_state(&1) == id))

    case state do
      nil -> to_form(Map.put(ProfilesLive.empty_form(), "profileId", id), as: kind)
      profile_state -> to_form(ProfilesLive.profile_form(profile_state), as: kind)
    end
  end

  defp escalation_profile_id(form, selected_profile_id) do
    String.trim(form.params["escalationProfile"] || selected_profile_id || "")
  end

  defp profile_id(%Phoenix.HTML.Form{} = form), do: String.trim(form.params["profileId"] || "")
  defp profile_id_from_state(state), do: get_in(state, ["profile", "llmProfile"]) || ""

  defp scope_id("", suffix), do: suffix
  defp scope_id(nil, suffix), do: suffix
  defp scope_id(prefix, suffix), do: "#{prefix}-#{suffix}"

  defp models_for(profiles, id) do
    profiles
    |> Enum.find(%{}, &(profile_id_from_state(&1) == id))
    |> get_in(["profile", "models"])
    |> Kernel.||([])
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

  defp move_backup(backups, index, direction) do
    target = if direction == "up", do: index - 1, else: index + 1

    if index < 0 or target < 0 or index >= length(backups) or target >= length(backups) do
      backups
    else
      current = Enum.at(backups, index)
      other = Enum.at(backups, target)
      backups |> List.replace_at(index, other) |> List.replace_at(target, current)
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

  defp cache_label("refresh"), do: "Refresh cache on next run"
  defp cache_label("cache"), do: "Use cache"
  defp cache_label(_), do: "Cache off"

  defp next_cache_mode("off"), do: "cache"
  defp next_cache_mode("cache"), do: "refresh"
  defp next_cache_mode(_), do: "off"

  defp notify_parent(socket, message) do
    send(self(), message)
    socket
  end

  defp noreply(socket), do: {:noreply, socket}

  def field_error(assigns) do
    ~H"""
    <p :if={@message} class="ullm-field-error">{@message}</p>
    """
  end
end
