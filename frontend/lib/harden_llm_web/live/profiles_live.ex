defmodule HardenLlmWeb.ProfilesLive do
  use HardenLlmWeb, :live_view

  alias HardenLlmWeb.{APIError, HardenAPI, Observability}

  @empty_form %{
    "profileId" => "",
    "provider" => "openai",
    "apiInferenceType" => "responses",
    "baseUrl" => "https://api.openai.com/v1",
    "modelId" => "",
    "credentialId" => "",
    "apiKey" => "",
    "backupProfiles" => "",
    "supportsTemperature" => "true",
    "supportsContractedStructuredOutput" => "true"
  }

  @impl true
  def mount(_params, _session, socket) do
    socket =
      socket
      |> assign(:page_title, "Profiles")
      |> assign(:loading?, true)
      |> assign(:profiles_by_id, %{})
      |> assign(:editing?, false)
      |> assign(:form, to_form(@empty_form, as: :profile))
      |> assign(:field_errors, %{})
      |> assign(:operation_error, nil)
      |> assign(:pending, nil)
      |> assign(:delete_id, nil)
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
  def handle_async(:load_profiles, {:ok, {:ok, %{"profiles" => profiles}, _state}}, socket) do
    {:noreply, put_profiles(socket, profiles)}
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

  def handle_async(_operation, _result, socket), do: {:noreply, socket}

  @impl true
  def handle_event("new", _params, socket) do
    {:noreply,
     socket
     |> assign(:editing?, true)
     |> assign(:form, to_form(@empty_form, as: :profile))
     |> assign(:field_errors, %{})
     |> assign(:operation_error, nil)}
  end

  def handle_event("edit", %{"id" => id}, socket) do
    case socket.assigns.profiles_by_id[id] do
      nil ->
        {:noreply, socket}

      profile_state ->
        {:noreply,
         socket
         |> assign(:editing?, true)
         |> assign(:form, to_form(profile_form(profile_state), as: :profile))
         |> assign(:field_errors, %{})
         |> assign(:operation_error, nil)}
    end
  end

  def handle_event("cancel-edit", _params, socket),
    do: {:noreply, assign(socket, :editing?, false)}

  def handle_event("save", %{"profile" => params}, %{assigns: %{pending: nil}} = socket) do
    reference = System.unique_integer([:positive, :monotonic])
    payload = profile_payload(params)
    handle = socket.assigns.session_handle
    id = params["profileId"] || ""

    {:noreply,
     socket
     |> assign(:pending, reference)
     |> assign(:form, to_form(params, as: :profile))
     |> start_async(
       {:save, reference},
       Observability.propagate(fn -> HardenAPI.save_profile(handle, id, payload) end)
     )}
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
      _ -> {:noreply, assign(socket, :operation_error, "The selected bundle was rejected.")}
    end
  end

  def field_error(errors, name), do: errors[name] || errors["profile." <> name]

  attr :message, :string, default: nil

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

  defp profile_form(profile_state) do
    profile = profile_state["profile"] || %{}
    credential = profile_state["credential"] || %{}

    %{
      "profileId" => profile["llmProfile"] || "",
      "provider" => profile["provider"] || "openai",
      "apiInferenceType" => profile["apiInferenceType"] || "responses",
      "baseUrl" => profile["baseUrl"] || "",
      "modelId" => profile["modelId"] || "",
      "credentialId" => credential["credentialId"] || "",
      "apiKey" => "",
      "backupProfiles" => Enum.join(profile["backupProfiles"] || [], ", "),
      "supportsTemperature" => to_string(profile["supportsTemperature"] || false),
      "supportsContractedStructuredOutput" =>
        to_string(profile["supportsContractedStructuredOutput"] || false)
    }
  end

  defp profile_payload(params) do
    credential = String.trim(params["apiKey"] || "")

    payload = %{
      "profile" => %{
        "schemaVersion" => 1,
        "llmProfile" => params["profileId"] || "",
        "provider" => params["provider"] || "",
        "apiInferenceType" => params["apiInferenceType"] || "responses",
        "endpointCredentialScope" => "user",
        "baseUrl" => params["baseUrl"] || "",
        "modelId" => params["modelId"] || "",
        "pricing" => nil,
        "supportsTemperature" => truthy?(params["supportsTemperature"]),
        "supportsContractedStructuredOutput" =>
          truthy?(params["supportsContractedStructuredOutput"]),
        "tokensParam" => nil,
        "responsesTokensParam" => nil,
        "defaultOptions" => %{},
        "backupProfiles" => parse_backups(params["backupProfiles"])
      },
      "credentialId" => params["credentialId"] || ""
    }

    if credential == "",
      do: payload,
      else: Map.put(payload, "credential", %{"apiKey" => credential})
  end

  defp parse_backups(value) when is_binary(value) do
    value
    |> String.split(",", trim: true)
    |> Enum.map(&String.trim/1)
    |> Enum.reject(&(&1 == ""))
    |> Enum.uniq()
  end

  defp parse_backups(_value), do: []
  defp truthy?(value), do: value in [true, "true", "on", "1"]
  defp profile_dom_id(profile_state), do: "profile-#{profile_state["profile"]["llmProfile"]}"
  defp max_bundle_bytes, do: Application.get_env(:harden_llm, :max_bundle_bytes, 2_097_152)
end
