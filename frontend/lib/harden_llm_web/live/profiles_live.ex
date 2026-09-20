defmodule HardenLlmWeb.ProfilesLive do
  use HardenLlmWeb, :live_view

  alias HardenLlmWeb.{
    APIError,
    Auth,
    HardenAPI,
    Observability,
    ProfileDefaults,
    ProfileForm,
    ProfileWidgetState
  }

  @doc "Returns the blank profile editor shape used by reusable profile controls."
  def empty_form(policy), do: ProfileDefaults.empty_form(policy)

  @impl true
  def mount(_params, _session, socket) do
    socket =
      socket
      |> assign(:page_title, "Profiles")
      |> assign(:loading?, true)
      |> assign(:profiles_by_id, %{})
      |> assign(:recovery_policy_default, %{})
      |> assign(:editing?, false)
      |> assign(:form, to_form(empty_form(%{}), as: :profile))
      |> assign(:field_errors, %{})
      |> assign(:operation_error, nil)
      |> assign(:pending, nil)
      |> assign(:delete_id, nil)
      |> assign(:requested_edit_id, nil)
      |> assign(:editor_form_revision, 0)
      |> assign(:credential_staged?, false)
      |> assign(:options_open, false)
      |> assign(:retry_open, false)
      |> assign(:pricing_open, false)
      |> assign(:credential_open, false)
      |> assign(:recovery_target_config_open, %{})
      |> stream_configure(:profiles, dom_id: &profile_dom_id/1)
      |> stream(:profiles, [])
      |> allow_upload(:bundle,
        accept: ~w(.json application/json),
        max_entries: 1,
        max_file_size: max_bundle_bytes()
      )

    if connected?(socket) do
      handle = socket.assigns.session_handle

      {:ok,
       start_async(
         socket,
         :load_profiles,
         Observability.propagate(fn -> HardenAPI.list_profiles(handle) end)
       )}
    else
      {:ok, socket}
    end
  end

  @impl true
  def handle_params(%{"new" => "1"}, _uri, socket) do
    {:noreply,
     socket
     |> assign(:editing?, true)
     |> assign(:form, to_form(empty_form(socket.assigns.recovery_policy_default), as: :profile))
     |> update(:editor_form_revision, &(&1 + 1))
     |> assign(:field_errors, %{})
     |> assign(:operation_error, nil)
     |> assign(:requested_edit_id, nil)
     |> assign(:credential_staged?, false)
     |> reset_sections()
     |> assign(:credential_open, true)}
  end

  def handle_params(%{"edit" => id}, _uri, socket) when is_binary(id) and id != "" do
    {:noreply,
     socket
     |> assign(:requested_edit_id, id)
     |> maybe_open_requested_edit()}
  end

  def handle_params(_params, _uri, socket), do: {:noreply, socket}

  @impl true
  def handle_async(_operation, {:ok, {:error, %APIError{status: 401}}}, socket) do
    {:noreply, Auth.expire_live(socket)}
  end

  def handle_async(
        :load_profiles,
        {:ok,
         {:ok, %{"profiles" => profiles, "defaults" => %{"recoveryPolicy" => policy}}, _state}},
        socket
      ) do
    socket = assign(socket, :recovery_policy_default, policy)

    socket =
      if socket.assigns.editing? and socket.assigns.requested_edit_id == nil,
        do: assign(socket, :form, to_form(empty_form(policy), as: :profile)),
        else: socket

    {:noreply, socket |> put_profiles(profiles) |> maybe_open_requested_edit()}
  end

  def handle_async(:load_profiles, _result, socket) do
    {:noreply,
     socket
     |> assign(:loading?, false)
     |> assign(:operation_error, "Profiles are temporarily unavailable.")}
  end

  def handle_async(
        {:save, reference},
        {:ok, {:ok, profile_state, _state}},
        %{assigns: %{pending: reference}} = socket
      ) do
    id = profile_state["profile"]["llmProfile"]
    profiles_by_id = Map.put(socket.assigns.profiles_by_id, id, profile_state)

    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:editing?, false)
     |> assign(:field_errors, %{})
     |> assign(:operation_error, nil)
     |> assign(:profiles_by_id, profiles_by_id)
     |> stream_insert(:profiles, profile_state)}
  end

  def handle_async(
        {:save, reference},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:field_errors, error.field_errors)
     |> assign(:operation_error, error.message)}
  end

  def handle_async(
        {:refresh, reference, id},
        {:ok, {:ok, profile_state, _state}},
        %{assigns: %{pending: reference}} = socket
      ) do
    profiles_by_id = Map.put(socket.assigns.profiles_by_id, id, profile_state)

    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:profiles_by_id, profiles_by_id)
     |> stream_insert(:profiles, profile_state)
     |> put_flash(:info, "Model catalog refreshed.")}
  end

  def handle_async(
        {:delete, reference, id},
        {:ok, {:ok, _result, _state}},
        %{assigns: %{pending: reference}} = socket
      ) do
    profile_state = socket.assigns.profiles_by_id[id]

    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:delete_id, nil)
     |> assign(:profiles_by_id, Map.delete(socket.assigns.profiles_by_id, id))
     |> stream_delete(:profiles, profile_state)
     |> put_flash(:info, "Profile deleted.")}
  end

  def handle_async(
        {_operation, reference, _id},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:delete_id, nil)
     |> assign(:operation_error, error.message)}
  end

  def handle_async(
        {:save, reference},
        _result,
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:operation_error, "The profile could not be saved.")}
  end

  def handle_async(
        {_operation, reference, _id},
        _result,
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:delete_id, nil)
     |> assign(:operation_error, "The profile operation could not be completed.")}
  end

  def handle_async(_operation, _result, socket), do: {:noreply, socket}

  @impl true
  def handle_info({:profile_widget, "profile", {:profile_widget_draft, params}}, socket)
      when is_map(params) do
    {:noreply, assign(socket, :form, to_form(params, as: :profile))}
  end

  def handle_info(
        {:profile_widget, "profile", {:profile_widget_selection, %{profile_id: profile_id}}},
        socket
      )
      when is_binary(profile_id) do
    form =
      case socket.assigns.profiles_by_id[profile_id] do
        nil -> empty_form(socket.assigns.recovery_policy_default)
        profile_state -> profile_form(profile_state)
      end

    {:noreply, assign(socket, :form, to_form(form, as: :profile))}
  end

  def handle_info({:profile_widget, "profile", {:profile_widget_ui, name, open}}, socket) do
    field =
      case name do
        "modelOptionsOpen" -> :options_open
        "retryRepairOpen" -> :retry_open
        "pricingOpen" -> :pricing_open
        "credentialOpen" -> :credential_open
        "llmProfileConfigOpen" -> :editing?
        _ -> nil
      end

    if field, do: {:noreply, assign(socket, field, truthy?(open))}, else: {:noreply, socket}
  end

  def handle_info({:profile_widget, "profile", {:profile_widget_catalog, profiles}}, socket)
      when is_list(profiles) do
    {:noreply, put_profiles(socket, profiles)}
  end

  def handle_info({:profile_widget, "profile", _message}, socket), do: {:noreply, socket}

  @impl true
  def handle_event("new", _params, socket) do
    {:noreply,
     socket
     |> assign(:editing?, true)
     |> assign(:form, to_form(empty_form(socket.assigns.recovery_policy_default), as: :profile))
     |> update(:editor_form_revision, &(&1 + 1))
     |> assign(:field_errors, %{})
     |> assign(:operation_error, nil)
     |> assign(:requested_edit_id, nil)
     |> assign(:credential_staged?, false)
     |> reset_sections()
     |> assign(:credential_open, true)}
  end

  def handle_event("edit", %{"id" => id}, socket) do
    case socket.assigns.profiles_by_id[id] do
      nil ->
        {:noreply, socket}

      profile_state ->
        {:noreply, open_profile_editor(socket, profile_state)}
    end
  end

  def handle_event("cancel-edit", _params, socket),
    do: {:noreply, assign(socket, :editing?, false)}

  def handle_event("draft-change", %{"profile" => params}, socket) when is_map(params) do
    {:noreply,
     assign(
       socket,
       :form,
       to_form(ProfileWidgetState.merge_draft(socket.assigns.form.params, params), as: :profile)
     )
     |> update(:editor_form_revision, &(&1 + 1))}
  end

  def handle_event("save", %{"profile" => params}, %{assigns: %{pending: nil}} = socket) do
    case profile_payload(params) do
      {:ok, payload} ->
        reference = System.unique_integer([:positive, :monotonic])
        handle = socket.assigns.session_handle
        id = params["profileId"] || ""

        {:noreply,
         socket
         |> assign(:pending, reference)
         |> assign(:form, to_form(params, as: :profile))
         |> update(:editor_form_revision, &(&1 + 1))
         |> start_async(
           {:save, reference},
           Observability.propagate(fn -> HardenAPI.save_profile(handle, id, payload) end)
         )}

      {:error, message} ->
        {:noreply,
         socket
         |> assign(:form, to_form(params, as: :profile))
         |> update(:editor_form_revision, &(&1 + 1))
         |> assign(:operation_error, message)}
    end
  end

  def handle_event("save", _params, socket), do: {:noreply, socket}

  def handle_event("refresh", %{"id" => id}, %{assigns: %{pending: nil}} = socket) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    {:noreply,
     socket
     |> assign(:pending, reference)
     |> start_async(
       {:refresh, reference, id},
       Observability.propagate(fn -> HardenAPI.refresh_profile_models(handle, id) end)
     )}
  end

  def handle_event("confirm-delete", %{"id" => id}, socket),
    do: {:noreply, assign(socket, :delete_id, id)}

  def handle_event("cancel-delete", _params, socket),
    do: {:noreply, assign(socket, :delete_id, nil)}

  def handle_event("delete", _params, %{assigns: %{pending: nil, delete_id: id}} = socket)
      when is_binary(id) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    {:noreply,
     socket
     |> assign(:pending, reference)
     |> start_async(
       {:delete, reference, id},
       Observability.propagate(fn -> HardenAPI.delete_profile(handle, id) end)
     )}
  end

  def handle_event("delete", _params, socket), do: {:noreply, socket}
  def handle_event("validate-bundle", _params, socket), do: {:noreply, socket}

  def handle_event("import-bundle", _params, socket) do
    results =
      consume_uploaded_entries(socket, :bundle, fn %{path: path}, _entry ->
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
       socket |> put_profiles(profiles) |> put_flash(:info, "Profile bundle imported atomically.")}
    else
      {:error, %APIError{status: 401}} -> {:noreply, Auth.expire_live(socket)}
      _ -> {:noreply, assign(socket, :operation_error, "The selected bundle was rejected.")}
    end
  end

  defdelegate field_error(errors, name), to: ProfileForm

  attr(:message, :string, default: nil)

  def field_error(assigns) do
    ~H"""
    <p :if={@message} class="mt-1 text-xs text-rose-700">{@message}</p>
    """
  end

  def profile_host(profile_state) do
    with base_url when is_binary(base_url) <- get_in(profile_state, ["profile", "baseUrl"]),
         %URI{host: host} when is_binary(host) <- URI.parse(base_url) do
      host
    else
      _ -> "invalid endpoint"
    end
  end

  def models_count(profile_state), do: length(get_in(profile_state, ["profile", "models"]) || [])

  def known_base_urls(profiles_by_id) do
    profiles_by_id
    |> Map.values()
    |> Enum.map(&get_in(&1, ["profile", "baseUrl"]))
    |> Enum.filter(&(is_binary(&1) and &1 != ""))
    |> Enum.uniq()
    |> Enum.sort()
  end

  def model_options(profiles_by_id, profile_id) do
    profiles_by_id
    |> Map.get(profile_id, %{})
    |> get_in(["profile", "models"])
    |> Kernel.||([])
  end

  def option_present?(profile_state),
    do: map_size(get_in(profile_state, ["profile", "defaultOptions"]) || %{}) > 0

  def pricing_present?(profile_state),
    do: not is_nil(get_in(profile_state, ["profile", "pricing"]))

  defdelegate options_valid?(value), to: ProfileForm

  defp truthy?(value), do: value in [true, "true", "on", "1"]

  defp put_profiles(socket, profiles) do
    profiles_by_id =
      Map.new(profiles, fn profile_state ->
        {profile_state["profile"]["llmProfile"], profile_state}
      end)

    socket
    |> assign(:loading?, false)
    |> assign(:operation_error, nil)
    |> assign(:profiles_by_id, profiles_by_id)
    |> stream(:profiles, profiles, reset: true)
  end

  defp maybe_open_requested_edit(%{assigns: %{requested_edit_id: id}} = socket)
       when is_binary(id) and id != "" do
    case socket.assigns.profiles_by_id[id] do
      nil ->
        socket

      profile_state ->
        socket |> open_profile_editor(profile_state) |> assign(:requested_edit_id, nil)
    end
  end

  defp maybe_open_requested_edit(socket), do: socket

  defp open_profile_editor(socket, profile_state) do
    socket
    |> assign(:editing?, true)
    |> assign(:form, to_form(profile_form(profile_state), as: :profile))
    |> update(:editor_form_revision, &(&1 + 1))
    |> assign(:field_errors, %{})
    |> assign(:operation_error, nil)
    |> assign(:credential_staged?, false)
    |> reset_sections()
  end

  defdelegate profile_form(profile_state), to: ProfileForm
  defdelegate profile_payload(params), to: ProfileForm

  defp reset_sections(socket),
    do:
      assign(socket,
        options_open: false,
        retry_open: false,
        pricing_open: false,
        credential_open: false,
        recovery_target_config_open: %{}
      )

  defp profile_dom_id(profile_state), do: "profile-#{profile_state["profile"]["llmProfile"]}"
  defp max_bundle_bytes, do: Application.get_env(:harden_llm, :max_bundle_bytes, 2_097_152)
end
