defmodule HardenLlmWeb.ProfilesLive do
  use HardenLlmWeb, :live_view

  alias HardenLlmWeb.{
    APIError,
    Auth,
    HardenAPI,
    Observability,
    ProfileDefaults,
    ProfileWidgetState
  }

  @section_keys ~w(options_open retry_open pricing_open credential_open)

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
      |> assign(:credential_staged?, false)
      |> assign(:options_open, false)
      |> assign(:retry_open, false)
      |> assign(:pricing_open, false)
      |> assign(:credential_open, false)
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
  def handle_event("new", _params, socket) do
    {:noreply,
     socket
     |> assign(:editing?, true)
     |> assign(:form, to_form(empty_form(socket.assigns.recovery_policy_default), as: :profile))
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
     )}
  end

  def handle_event("toggle-section", %{"section" => section}, socket)
      when section in @section_keys do
    key = String.to_existing_atom(section)
    {:noreply, assign(socket, key, !Map.get(socket.assigns, key))}
  end

  def handle_event("clear-staged-key", _params, socket) do
    {:noreply,
     socket
     |> assign(:form, update_form_value(socket.assigns.form, "apiKey", ""))
     |> assign(:credential_staged?, false)}
  end

  def handle_event("stage-key", _params, socket) do
    if String.trim(socket.assigns.form[:apiKey].value || "") == "" do
      {:noreply,
       assign(socket, :operation_error, "Enter a replacement API key before staging it.")}
    else
      {:noreply,
       socket
       |> assign(:credential_staged?, true)
       |> assign(:credential_open, false)
       |> assign(:operation_error, nil)}
    end
  end

  def handle_event("cancel-key", _params, socket) do
    {:noreply,
     socket
     |> assign(:form, update_form_value(socket.assigns.form, "apiKey", ""))
     |> assign(:credential_staged?, false)
     |> assign(:credential_open, false)}
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
         |> start_async(
           {:save, reference},
           Observability.propagate(fn -> HardenAPI.save_profile(handle, id, payload) end)
         )}

      {:error, message} ->
        {:noreply,
         socket
         |> assign(:form, to_form(params, as: :profile))
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

  def field_error(errors, name) do
    errors[name] ||
      Enum.find_value(errors, fn {field, message} ->
        if String.ends_with?(field, "." <> name), do: message
      end)
  end

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

  def options_valid?(value) do
    case decode_object(value, "Default options JSON") do
      {:ok, _options} -> true
      {:error, _message} -> false
    end
  end

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
    |> assign(:field_errors, %{})
    |> assign(:operation_error, nil)
    |> assign(:credential_staged?, false)
    |> reset_sections()
  end

  @doc "Converts a backend profile state into the editable profile form shape."
  def profile_form(profile_state) do
    profile = profile_state["profile"] || %{}
    credential = profile_state["credential"] || %{}
    options = ProfileDefaults.normalize_options(profile["defaultOptions"])
    pricing = profile["pricing"] || %{}

    Map.merge(ProfileDefaults.empty_form(Map.fetch!(profile, "recoveryPolicy")), %{
      "profileId" => profile["llmProfile"] || "",
      "provider" => profile["provider"] || "",
      "apiInferenceType" =>
        profile["apiInferenceType"] || ProfileDefaults.api_inference_type_default(),
      "baseUrl" => normalize_base_url(profile["baseUrl"] || ""),
      "modelId" => profile["modelId"] || "",
      "credentialId" => credential["credentialId"] || "",
      "credentialConfigured" => to_string(credential["configured"] || false),
      "endpointCredentialScope" => profile["endpointCredentialScope"] || "user",
      "apiKey" => "",
      "supportsTemperature" => to_string(profile["supportsTemperature"] || false),
      "supportsContractedStructuredOutput" =>
        to_string(profile["supportsContractedStructuredOutput"] || false),
      "supportsWebSearch" => to_string(supports_web_search?(profile)),
      "maxTokens" =>
        option_text(options["max_tokens"] || ProfileDefaults.default_options()["max_tokens"]),
      "temperature" => option_text(options["temperature"]),
      "topP" => option_text(options["top_p"] || options["topP"]),
      "topK" => option_text(options["top_k"] || options["topK"]),
      "stopSequences" => stop_text(options["stop"]),
      "defaultOptionsJson" => Jason.encode!(options, pretty: true),
      "pricingInput" => pricing_text(pricing["input_cost_per_token"]),
      "pricingOutput" => pricing_text(pricing["output_cost_per_token"]),
      "pricingCacheRead" => pricing_text(pricing["cache_read_input_token_cost"]),
      "pricingCacheWrite" => pricing_text(pricing["cache_creation_input_token_cost"]),
      "pricingReasoning" => pricing_text(pricing["output_cost_per_reasoning_token"])
    })
  end

  @doc "Builds the backend profile payload while preserving credential safety."
  def profile_payload(params) do
    with {:ok, options} <- options_payload(params),
         {:ok, pricing} <- pricing_payload(params) do
      credential = String.trim(params["apiKey"] || "")

      payload = %{
        "profile" => %{
          "schemaVersion" => 2,
          "llmProfile" => params["profileId"] || "",
          "provider" => params["provider"] || "",
          "apiInferenceType" =>
            params["apiInferenceType"] || ProfileDefaults.api_inference_type_default(),
          "endpointCredentialScope" => params["endpointCredentialScope"] || "user",
          "baseUrl" => normalize_base_url(params["baseUrl"] || ""),
          "modelId" => params["modelId"] || "",
          "pricing" => pricing,
          "supportsTemperature" => truthy?(params["supportsTemperature"]),
          "supportsContractedStructuredOutput" =>
            truthy?(params["supportsContractedStructuredOutput"]),
          "supportsWebSearch" => truthy?(params["supportsWebSearch"]),
          "tokensParam" => nil,
          "responsesTokensParam" => nil,
          "defaultOptions" => options,
          "recoveryPolicy" =>
            ProfileWidgetState.serialize_recovery_policy(params["recoveryPolicy"])
        },
        "credentialId" => params["credentialId"] || ""
      }

      {:ok,
       if(credential == "",
         do: payload,
         else: Map.put(payload, "credential", %{"apiKey" => credential})
       )}
    end
  end

  defp options_payload(params) do
    with {:ok, options} <- decode_object(params["defaultOptionsJson"], "Default options JSON"),
         {:ok, options} <-
           put_number_option(
             options,
             "max_tokens",
             params["maxTokens"],
             "Max Output Tokens",
             :integer
           ),
         {:ok, options} <-
           put_number_option(options, "temperature", params["temperature"], "Temperature", :float),
         {:ok, options} <- put_number_option(options, "top_p", params["topP"], "Top P", :float),
         {:ok, options} <- put_number_option(options, "top_k", params["topK"], "Top K", :integer),
         {:ok, options} <- put_stop_option(options, params["stopSequences"]) do
      {:ok, options}
    end
  end

  defp pricing_payload(params) do
    fields = %{
      "input_cost_per_token" => params["pricingInput"],
      "output_cost_per_token" => params["pricingOutput"],
      "cache_read_input_token_cost" => params["pricingCacheRead"],
      "cache_creation_input_token_cost" => params["pricingCacheWrite"],
      "output_cost_per_reasoning_token" => params["pricingReasoning"]
    }

    with {:ok, values} <-
           Enum.reduce_while(fields, {:ok, %{}}, fn {key, value}, {:ok, acc} ->
             case rate_value(value, key) do
               {:ok, nil} -> {:cont, {:ok, Map.put(acc, key, nil)}}
               {:ok, number} -> {:cont, {:ok, Map.put(acc, key, number / 1_000_000)}}
               {:error, message} -> {:halt, {:error, message}}
             end
           end) do
      if Enum.any?(values, fn {_key, value} -> not is_nil(value) end),
        do: {:ok, values},
        else: {:ok, nil}
    end
  end

  defp put_number_option(options, key, value, _label, _kind) when value in [nil, ""] do
    {:ok, Map.delete(options, option_alias(key))}
  end

  defp put_number_option(options, key, value, label, kind) do
    case number_value(value, label, kind) do
      {:ok, number} -> {:ok, options |> Map.put(key, number) |> Map.delete(option_alias(key))}
      {:error, message} -> {:error, message}
    end
  end

  defp option_alias("top_p"), do: "topP"
  defp option_alias("top_k"), do: "topK"
  defp option_alias(_key), do: nil

  defp put_stop_option(options, value) when value in [nil, ""], do: {:ok, options}

  defp put_stop_option(options, value) do
    stops = value |> String.split("\n") |> Enum.map(&String.trim/1) |> Enum.reject(&(&1 == ""))
    {:ok, Map.put(options, "stop", stops)}
  end

  defp decode_object(value, label) do
    text = String.trim(value || "")

    if text == "" do
      {:ok, %{}}
    else
      case Jason.decode(text) do
        {:ok, object} when is_map(object) -> {:ok, object}
        {:ok, _} -> {:error, "#{label} must be a JSON object."}
        {:error, _} -> {:error, "#{label} must be valid JSON."}
      end
    end
  end

  defp rate_value(value, _label) when value in [nil, ""], do: {:ok, nil}

  defp rate_value(value, label) do
    case Float.parse(String.trim(to_string(value))) do
      {number, ""} when number >= 0 -> {:ok, number}
      _ -> {:error, "#{label} must be a non-negative number."}
    end
  end

  defp number_value(value, label, :integer) do
    case Integer.parse(String.trim(to_string(value))) do
      {number, ""} when number >= 0 -> {:ok, number}
      _ -> {:error, "#{label} must be a non-negative integer."}
    end
  end

  defp number_value(value, label, :float) do
    case Float.parse(String.trim(to_string(value))) do
      {number, ""} when number >= 0 -> {:ok, number}
      _ -> {:error, "#{label} must be a non-negative number."}
    end
  end

  defp update_form_value(form, key, value),
    do: to_form(Map.put(form.params || %{}, key, value), as: :profile)

  defp option_text(value) when is_nil(value), do: ""
  defp option_text(value), do: to_string(value)

  defp normalize_base_url(value) do
    value
    |> to_string()
    |> String.trim()
    |> String.trim_trailing("/")
  end

  defp stop_text(value) when is_list(value), do: Enum.join(value, "\n")
  defp stop_text(_value), do: ""

  defp pricing_text(value) when is_number(value),
    do:
      :erlang.float_to_binary(value * 1.0 * 1_000_000, decimals: 12)
      |> String.trim_trailing("0")
      |> String.trim_trailing(".")

  defp pricing_text(_value), do: ""
  defp truthy?(value), do: value in [true, "true", "on", "1"]

  defp supports_web_search?(profile) do
    case Map.fetch(profile, "supportsWebSearch") do
      {:ok, value} -> truthy?(value)
      :error -> false
    end
  end

  defp reset_sections(socket),
    do:
      assign(socket,
        options_open: false,
        retry_open: false,
        pricing_open: false,
        credential_open: false
      )

  defp profile_dom_id(profile_state), do: "profile-#{profile_state["profile"]["llmProfile"]}"
  defp max_bundle_bytes, do: Application.get_env(:harden_llm, :max_bundle_bytes, 2_097_152)
end
