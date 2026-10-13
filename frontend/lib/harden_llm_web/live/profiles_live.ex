defmodule HardenLlmWeb.ProfilesLive do
  use HardenLlmWeb, :live_view

  alias HardenLlmUI.{
    APIError,
    Client,
    ProfileForm,
    ProfileDefaults,
    ProfileWidgetComponent,
    ProfileWidgetState
  }

  def mount(_params, _session, socket) do
    socket =
      socket
      |> assign(
        page_title: "Profiles",
        loading?: true,
        profiles_by_id: %{},
        recovery_policy_default: %{},
        editing?: false,
        form: to_form(ProfileForm.empty_form(%{}), as: :profile),
        field_errors: %{},
        operation_error: nil,
        pending: nil,
        delete_id: nil,
        requested_edit_id: nil,
        editor_form_revision: 0,
        credential_staged?: false,
        options_open: false,
        retry_open: false,
        pricing_open: false,
        credential_open: false,
        recovery_target_config_open: %{},
        model_catalog: [],
        run_inputs: %{},
        api_context: HardenLlmWeb.APIContext.server()
      )
      |> stream_configure(:profiles, dom_id: &profile_dom_id/1)
      |> stream(:profiles, [])
      |> allow_upload(:bundle,
        accept: ~w(.json application/json),
        max_entries: 1,
        max_file_size: 2_097_152
      )

    if connected?(socket) do
      context = socket.assigns.api_context

      {:ok,
       start_async(
         socket,
         :load_profiles,
         Client.propagate(fn -> Client.list_profiles(context) end)
       )}
    else
      {:ok, socket}
    end
  end

  def handle_params(%{"new" => "1"}, _uri, socket), do: {:noreply, new_editor(socket)}

  def handle_params(%{"edit" => id}, _uri, socket) do
    {:noreply, socket |> assign(:requested_edit_id, id) |> maybe_open_requested_edit()}
  end

  def handle_params(_params, _uri, socket), do: {:noreply, socket}

  def handle_async(:load_profiles, {:ok, {:ok, %{"profiles" => profiles}}}, socket) do
    {:noreply, socket |> put_profiles(profiles) |> maybe_open_requested_edit()}
  end

  def handle_async(:edit, {:ok, {:ok, draft}}, socket) do
    view = socket.assigns.profiles_by_id[draft["sourceProfileId"]]
    draft = if view, do: Map.merge(draft, Map.take(view, ~w(id name))), else: draft
    {:noreply, open_editor(socket, draft)}
  end

  def handle_async({:refresh, _id}, {:ok, {:ok, draft}}, socket) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> open_editor(draft)
     |> put_flash(:info, "Model catalog refreshed.")}
  end

  def handle_async({:delete, id}, {:ok, {:ok, nil}}, socket) do
    view = socket.assigns.profiles_by_id[id]

    {:noreply,
     socket
     |> assign(
       pending: nil,
       delete_id: nil,
       profiles_by_id: Map.delete(socket.assigns.profiles_by_id, id)
     )
     |> stream_delete(:profiles, view)
     |> put_flash(:info, "Profile deleted.")}
  end

  def handle_async(_operation, {:ok, {:error, %APIError{} = error}}, socket) do
    {:noreply,
     assign(socket,
       loading?: false,
       pending: nil,
       operation_error: error.message,
       field_errors: error.field_errors
     )}
  end

  def handle_async(_operation, _result, socket),
    do:
      {:noreply,
       assign(socket,
         loading?: false,
         pending: nil,
         operation_error: "The profile operation could not be completed."
       )}

  def handle_info({:profile_widget, "profile", {:profile_widget_editor, params}}, socket) do
    {:noreply,
     assign(socket,
       form: to_form(params, as: :profile),
       model_catalog: get_in(params, ["definition", "models"]) || []
     )}
  end

  def handle_info({:profile_widget, "profile", {:profile_widget_draft, params}}, socket) do
    {:noreply, assign(socket, :form, to_form(params, as: :profile))}
  end

  def handle_info({:profile_widget, "profile", {:profile_widget_saved, view}}, socket) do
    {:noreply,
     socket
     |> assign(requested_edit_id: nil, operation_error: nil, field_errors: %{})
     |> put_flash(:info, "Profile saved.")
     |> stream_insert(:profiles, view)}
  end

  def handle_info({:profile_widget, "profile", {:profile_widget_catalog, profiles}}, socket) do
    {:noreply, put_profiles(socket, profiles)}
  end

  def handle_info({:profile_widget, "profile", {:profile_widget_ui, name, open}}, socket) do
    key =
      %{
        "modelOptionsOpen" => :options_open,
        "retryRepairOpen" => :retry_open,
        "pricingOpen" => :pricing_open,
        "credentialOpen" => :credential_open
      }[name]

    {:noreply, if(key, do: assign(socket, key, open), else: socket)}
  end

  def handle_info({:profile_widget, "profile", _}, socket), do: {:noreply, socket}

  def handle_event("new", _, socket), do: {:noreply, new_editor(socket)}
  def handle_event("edit", %{"id" => id}, socket), do: {:noreply, load_editor(socket, id)}
  def handle_event("cancel-edit", _, socket), do: {:noreply, assign(socket, :editing?, false)}

  def handle_event("draft-change", %{"profile" => params}, socket) do
    form =
      ProfileWidgetState.merge_draft(socket.assigns.form.params, Map.delete(params, "apiKey"))

    {:noreply,
     socket
     |> assign(:form, to_form(form, as: :profile))
     |> update(:editor_form_revision, &(&1 + 1))}
  end

  def handle_event("save", _params, socket) do
    send_update(ProfileWidgetComponent, id: "profile-definition-widget", action: :save)
    {:noreply, socket}
  end

  def handle_event("refresh", %{"id" => id}, %{assigns: %{pending: nil}} = socket) do
    context = socket.assigns.api_context

    {:noreply,
     socket
     |> assign(:pending, id)
     |> start_async(
       {:refresh, id},
       Client.propagate(fn -> Client.refresh_profile_models(context, id) end)
     )}
  end

  def handle_event("refresh", _, socket), do: {:noreply, socket}

  def handle_event("confirm-delete", %{"id" => id}, socket),
    do: {:noreply, assign(socket, :delete_id, id)}

  def handle_event("cancel-delete", _, socket), do: {:noreply, assign(socket, :delete_id, nil)}

  def handle_event("delete", _, %{assigns: %{pending: nil, delete_id: id}} = socket)
      when is_binary(id) do
    context = socket.assigns.api_context

    {:noreply,
     socket
     |> assign(:pending, id)
     |> start_async({:delete, id}, Client.propagate(fn -> Client.delete_profile(context, id) end))}
  end

  def handle_event("delete", _, socket), do: {:noreply, socket}
  def handle_event("validate-bundle", _, socket), do: {:noreply, socket}

  def handle_event("import-bundle", _, socket) do
    values =
      consume_uploaded_entries(socket, :bundle, fn %{path: path}, _ -> {:ok, File.read!(path)} end)

    with [bytes] <- values,
         {:ok, bundle} <- Jason.decode(bytes),
         {:ok, %{"profiles" => profiles}} <-
           Client.import_profile_bundle(socket.assigns.api_context, bundle) do
      {:noreply,
       socket |> put_profiles(profiles) |> put_flash(:info, "Profile bundle imported atomically.")}
    else
      _ -> {:noreply, assign(socket, :operation_error, "The selected bundle was rejected.")}
    end
  end

  def profile_host(view), do: URI.parse(view["definition"]["baseUrl"]).host
  def models_count(view), do: length(view["definition"]["models"] || [])

  defp put_profiles(socket, profiles) do
    policy =
      case profiles do
        [view | _] -> view["definition"]["recoveryPolicy"]
        [] -> %{}
      end

    socket
    |> assign(
      loading?: false,
      profiles_by_id: Map.new(profiles, &{&1["id"], &1}),
      recovery_policy_default: policy
    )
    |> stream(:profiles, profiles, reset: true)
  end

  defp maybe_open_requested_edit(%{assigns: %{requested_edit_id: id, loading?: false}} = socket)
       when is_binary(id), do: load_editor(socket, id)

  defp maybe_open_requested_edit(socket), do: socket

  defp load_editor(socket, id) do
    context = socket.assigns.api_context
    start_async(socket, :edit, Client.propagate(fn -> Client.get_profile(context, id) end))
  end

  defp open_editor(socket, draft) do
    socket
    |> assign(
      editing?: true,
      form: to_form(ProfileForm.profile_form(draft), as: :profile),
      run_inputs: draft["inputs"],
      model_catalog: draft["definition"]["models"] || [],
      field_errors: %{},
      operation_error: nil,
      requested_edit_id: nil
    )
    |> update(:editor_form_revision, &(&1 + 1))
    |> reset_sections()
  end

  defp new_editor(socket) do
    socket
    |> assign(
      editing?: true,
      form:
        to_form(ProfileDefaults.empty_form(socket.assigns.recovery_policy_default), as: :profile),
      run_inputs: %{},
      model_catalog: [],
      field_errors: %{},
      operation_error: nil,
      requested_edit_id: nil
    )
    |> update(:editor_form_revision, &(&1 + 1))
    |> reset_sections()
    |> assign(:credential_open, true)
  end

  defp reset_sections(socket),
    do:
      assign(socket,
        options_open: false,
        retry_open: false,
        pricing_open: false,
        credential_open: false,
        recovery_target_config_open: %{}
      )

  defp profile_dom_id(view), do: "profile-#{view["id"]}"
end
