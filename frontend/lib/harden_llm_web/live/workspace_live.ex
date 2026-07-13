defmodule HardenLlmWeb.WorkspaceLive do
  use HardenLlmWeb, :live_view

  alias HardenLlmWeb.{APIError, HardenAPI, Observability}

  @default_state %{
    "schemaVersion" => 1,
    "selectedProfileId" => "",
    "modelId" => "",
    "systemPrompt" => "",
    "userPrompt" => "",
    "callType" => "text",
    "structuredRepair" => false,
    "cacheMode" => "off"
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
      |> assign(:run_result, nil)
      |> assign(:run_error, nil)
      |> assign(:run_ref, nil)
      |> assign(:form, to_form(@default_state, as: :run))

    if connected?(socket) do
      handle = socket.assigns.session_handle
      {:ok, start_async(socket, :hydrate, Observability.propagate(fn -> hydrate(handle) end))}
    else
      {:ok, socket}
    end
  end

  @impl true
  def handle_async(:hydrate, {:ok, {:ok, hydration}}, socket) do
    state = Map.merge(@default_state, hydration.state)

    {:noreply,
     socket
     |> assign(:loading?, false)
     |> assign(:backend_state, :ready)
     |> assign(:profiles, hydration.profiles)
     |> assign(:history, hydration.history)
     |> assign(:form, to_form(stringify_form(state), as: :run))}
  end

  def handle_async(:hydrate, _result, socket) do
    {:noreply,
     socket
     |> assign(:loading?, false)
     |> assign(:backend_state, :unavailable)}
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
     |> assign(:run_error, nil)}
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

  def handle_async({:run, _stale_reference}, _result, socket), do: {:noreply, socket}
  def handle_async(:save_draft, _result, socket), do: {:noreply, socket}

  @impl true
  def handle_event("save-draft", %{"run" => params}, socket) do
    state = state_from_params(params)
    handle = socket.assigns.session_handle

    {:noreply,
     socket
     |> assign(:form, to_form(params, as: :run))
     |> start_async(
       :save_draft,
       Observability.propagate(fn -> HardenAPI.save_state(handle, state) end)
     )}
  end

  def handle_event("run", %{"run" => params}, %{assigns: %{run_ref: nil}} = socket) do
    case run_payload(params) do
      {:ok, payload} ->
        reference = System.unique_integer([:positive, :monotonic])
        handle = socket.assigns.session_handle

        {:noreply,
         socket
         |> assign(:run_ref, reference)
         |> assign(:run_result, nil)
         |> assign(:run_error, nil)
         |> start_async(
           {:run, reference},
           Observability.propagate(fn -> HardenAPI.run(handle, payload) end)
         )}

      {:error, message} ->
        {:noreply, assign(socket, :run_error, message)}
    end
  end

  def handle_event("run", _params, socket), do: {:noreply, socket}

  def status_label(:loading), do: "Checking backend"
  def status_label(:ready), do: "Backend ready"
  def status_label(:unavailable), do: "Backend unavailable"

  def profile_options(profiles) do
    Enum.map(profiles, fn profile_state ->
      profile = profile_state["profile"] || %{}
      {profile["llmProfile"] || "Unnamed", profile["llmProfile"] || ""}
    end)
  end

  def safe_output(value) when is_binary(value), do: value
  def safe_output(value), do: Jason.encode!(value, pretty: true)

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

  defp hydrate(handle) do
    with {:ok, _result, state} <- HardenAPI.get_state(handle),
         {:ok, %{"profiles" => profiles}, _} <- HardenAPI.list_profiles(handle),
         {:ok, %{"items" => history}, _} <- HardenAPI.list_history(handle, limit: 10) do
      {:ok, %{state: state, profiles: profiles, history: history}}
    end
  end

  defp run_payload(params) do
    prompt = String.trim(params["userPrompt"] || "")
    profile_id = String.trim(params["selectedProfileId"] || "")

    cond do
      profile_id == "" ->
        {:error, "Choose a profile before running."}

      prompt == "" ->
        {:error, "Enter a prompt before running."}

      true ->
        payload = %{
          "profileId" => profile_id,
          "userPrompt" => prompt,
          "callType" => params["callType"] || "text",
          "cacheMode" => params["cacheMode"] || "off",
          "structuredRepair" => truthy?(params["structuredRepair"])
        }

        payload = put_optional(payload, "systemPrompt", params["systemPrompt"])

        case parse_schema(params["schema"], payload["callType"]) do
          {:ok, nil} -> {:ok, payload}
          {:ok, schema} -> {:ok, Map.put(payload, "schema", schema)}
          {:error, message} -> {:error, message}
        end
    end
  end

  defp state_from_params(params) do
    %{
      "schemaVersion" => 1,
      "selectedProfileId" => params["selectedProfileId"] || "",
      "modelId" => params["modelId"] || "",
      "systemPrompt" => params["systemPrompt"] || "",
      "userPrompt" => params["userPrompt"] || "",
      "callType" => params["callType"] || "text",
      "structuredRepair" => truthy?(params["structuredRepair"]),
      "cacheMode" => params["cacheMode"] || "off"
    }
  end

  defp parse_schema(value, "structured") when is_binary(value) do
    case Jason.decode(value) do
      {:ok, schema} when is_map(schema) -> {:ok, schema}
      _ -> {:error, "Structured output requires a valid JSON object schema."}
    end
  end

  defp parse_schema(_value, _call_type), do: {:ok, nil}
  defp truthy?(value), do: value in [true, "true", "on", "1"]

  defp put_optional(map, _key, value) when value in [nil, ""], do: map
  defp put_optional(map, key, value), do: Map.put(map, key, value)

  defp stringify_form(state) do
    schema =
      if is_map(state["schema"]), do: Jason.encode!(state["schema"], pretty: true), else: ""

    Map.put(state, "schema", schema)
  end
end
