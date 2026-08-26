defmodule HardenLlmWeb.EmbeddingLive do
  @moduledoc """
  A small authenticated host surface for the reusable LLM widget.

  This route is intentionally an ordinary in-flow page rather than a second
  application shell. It demonstrates the host contract by mounting two
  independent widget instances with distinct DOM and upload namespaces.
  """

  use HardenLlmWeb, :live_view

  alias HardenLlmWeb.{APIError, Auth, HardenAPI, Observability, ProfileWidgetState}

  @instance_specs [
    %{
      key: :primary,
      label: "Primary instance",
      component_id: "embed-primary-llm-widget",
      prefix: "embed-primary",
      bundle_upload: :embed_primary_profile_bundle,
      escalation_bundle_upload: :embed_primary_escalation_profile_bundle
    },
    %{
      key: :secondary,
      label: "Secondary instance",
      component_id: "embed-secondary-llm-widget",
      prefix: "embed-secondary",
      bundle_upload: :embed_secondary_profile_bundle,
      escalation_bundle_upload: :embed_secondary_escalation_profile_bundle
    }
  ]

  @ui_field_by_name %{
    "llmProfileConfigOpen" => :config_open,
    "modelOptionsOpen" => :options_open,
    "retryRepairOpen" => :retry_open,
    "pricingOpen" => :pricing_open
  }

  @impl true
  def mount(_params, _session, socket) do
    socket =
      socket
      |> assign(:page_title, "Embed LLM")
      |> assign(:loading?, true)
      |> assign(:backend_state, :loading)
      |> assign(:profiles, [])
      |> assign(:instances, initial_instances())
      |> assign(:upload_error, nil)
      |> assign(:instance_specs, @instance_specs)
      |> allow_upload(:embed_primary_profile_bundle, bundle_upload_options())
      |> allow_upload(:embed_primary_escalation_profile_bundle, bundle_upload_options())
      |> allow_upload(:embed_secondary_profile_bundle, bundle_upload_options())
      |> allow_upload(:embed_secondary_escalation_profile_bundle, bundle_upload_options())

    if connected?(socket) do
      handle = socket.assigns.session_handle
      {:ok, start_async(socket, :hydrate, Observability.propagate(fn -> hydrate(handle) end))}
    else
      {:ok, socket}
    end
  end

  @impl true
  def handle_async(_operation, {:ok, {:error, %APIError{status: 401}}}, socket) do
    {:noreply, Auth.expire_live(socket)}
  end

  def handle_async(:hydrate, {:ok, {:ok, hydration}}, socket) do
    {:noreply,
     socket
     |> assign(:loading?, false)
     |> assign(:backend_state, :ready)
     |> assign(:profiles, hydration.profiles)
     |> assign(:instances, hydration.instances)}
  end

  def handle_async(:hydrate, _result, socket) do
    {:noreply,
     socket
     |> assign(:loading?, false)
     |> assign(:backend_state, :unavailable)}
  end

  @impl true
  def handle_info({:profile_widget, prefix, message}, socket) do
    case instance_key(prefix) do
      nil -> {:noreply, socket}
      key -> route_widget_message(socket, key, message)
    end
  end

  @impl true
  def handle_event("validate-bundle", _params, socket), do: {:noreply, socket}

  def handle_event("import-bundle", %{"kind" => kind, "widget" => prefix}, socket) do
    case upload_name(prefix, kind) do
      nil -> {:noreply, assign(socket, :upload_error, "The widget upload namespace is invalid.")}
      upload -> import_bundle(socket, upload)
    end
  end

  def handle_event("import-bundle", _params, socket),
    do: {:noreply, assign(socket, :upload_error, "The selected bundle could not be scoped.")}

  defp import_bundle(socket, upload) do
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
       |> assign(:upload_error, nil)
       |> put_flash(:info, "Profile bundle imported atomically.")}
    else
      {:error, %APIError{status: 401}} -> {:noreply, Auth.expire_live(socket)}
      _ -> {:noreply, assign(socket, :upload_error, "The selected bundle was rejected.")}
    end
  end

  defp route_widget_message(socket, key, {:profile_widget_ui, name, open}) do
    case Map.fetch(@ui_field_by_name, name) do
      {:ok, field} -> update_instance(socket, key, &Map.put(&1, field, truthy?(open)))
      :error -> {:noreply, socket}
    end
  end

  defp route_widget_message(socket, key, {:profile_widget_selection, profile_id}) do
    update_instance(socket, key, &Map.put(&1, :selected_profile_id, profile_id))
  end

  defp route_widget_message(socket, key, {:profile_widget_control, "modelId", value}) do
    update_instance(socket, key, &Map.put(&1, :model_id, value))
  end

  defp route_widget_message(socket, key, {:profile_widget_control, "reasoningEffort", value}) do
    update_instance(socket, key, &Map.put(&1, :reasoning_effort, value))
  end

  defp route_widget_message(socket, key, {:profile_widget_control, "cacheMode", value}) do
    update_instance(socket, key, &Map.put(&1, :cache_mode, value))
  end

  defp route_widget_message(socket, key, {:profile_widget_provider_options, options})
       when is_map(options) do
    update_instance(socket, key, &Map.put(&1, :provider_options, options))
  end

  defp route_widget_message(socket, key, {:profile_widget_retry, retry}) when is_map(retry) do
    update_instance(socket, key, &Map.put(&1, :retry, retry))
  end

  defp route_widget_message(socket, key, {:profile_widget_profile_dirty, requires_save?}) do
    update_instance(socket, key, &Map.put(&1, :requires_save?, requires_save?))
  end

  defp route_widget_message(
         socket,
         key,
         {:profile_widget_profiles, profiles, selected_profile_id}
       ) do
    socket
    |> assign(:profiles, profiles)
    |> update_instance(key, &Map.put(&1, :selected_profile_id, selected_profile_id))
  end

  defp route_widget_message(socket, _key, _message), do: {:noreply, socket}

  defp update_instance(socket, key, update) do
    {:noreply, update(socket, :instances, &Map.update!(&1, key, update))}
  end

  defp hydrate(handle) do
    with {:ok, _result, state} <- HardenAPI.get_state(handle),
         {:ok, %{"profiles" => profiles}, _} <- HardenAPI.list_profiles(handle),
         true <- is_list(profiles) do
      state = state || %{}

      selected_profile_id =
        ProfileWidgetState.resolve_selected_profile_id(profiles, state["selectedProfileId"])

      model_id =
        ProfileWidgetState.resolve_selected_model_id(
          profiles,
          selected_profile_id,
          state["modelId"]
        )

      reasoning_effort = state["reasoningEffort"] || "lowest"
      cache_mode = normalize_cache_mode(state["cacheMode"])

      instances =
        initial_instances()
        |> Enum.into(%{}, fn {key, instance} ->
          {key,
           instance
           |> Map.put(:selected_profile_id, selected_profile_id)
           |> Map.put(:model_id, model_id)
           |> Map.put(:reasoning_effort, reasoning_effort)
           |> Map.put(:cache_mode, cache_mode)}
        end)

      {:ok, %{profiles: profiles, instances: instances}}
    end
  end

  defp initial_instances do
    Enum.into(@instance_specs, %{}, fn spec ->
      {spec.key,
       %{
         selected_profile_id: "",
         model_id: "",
         reasoning_effort: "lowest",
         cache_mode: "cache",
         config_open: false,
         options_open: false,
         retry_open: false,
         pricing_open: false,
         provider_options: %{},
         retry: %{},
         requires_save?: false
       }}
    end)
  end

  defp instance_key(prefix) do
    Enum.find_value(@instance_specs, fn spec -> if spec.prefix == prefix, do: spec.key end)
  end

  defp upload_name(prefix, "escalation") do
    Enum.find_value(@instance_specs, fn spec ->
      if spec.prefix == prefix, do: spec.escalation_bundle_upload
    end)
  end

  defp upload_name(prefix, _kind) do
    Enum.find_value(@instance_specs, fn spec ->
      if spec.prefix == prefix, do: spec.bundle_upload
    end)
  end

  defp bundle_upload_options do
    [
      accept: ~w(.json application/json),
      max_entries: 1,
      max_file_size: Application.get_env(:harden_llm, :max_bundle_bytes, 2_097_152)
    ]
  end

  defp normalize_cache_mode("refresh"), do: "refresh"
  defp normalize_cache_mode(_), do: "cache"

  defp truthy?(value), do: value in [true, "true", "on", "1"]

  @impl true
  def render(assigns) do
    ~H"""
    <Layouts.app flash={@flash} current_scope={@current_scope} current_identity={@current_identity}>
      <main id="embedding-page" class="studio-page mx-auto max-w-6xl px-4 py-6 sm:px-6 lg:px-8">
        <div class="mb-6">
          <p class="text-xs font-semibold uppercase tracking-[0.2em] text-teal-700">
            🧩 Embedding fixture
          </p>
          <h1 class="mt-1 text-3xl font-semibold tracking-tight text-slate-950">
            Reusable LLM widget
          </h1>
          <p class="mt-2 max-w-3xl text-sm text-slate-600">
            Two independent in-flow instances demonstrate the host contract: each has its own controls, folds, parent events, and profile-bundle upload namespace.
          </p>
        </div>

        <div
          :if={@loading?}
          id="embedding-loading"
          role="status"
          class="rounded-2xl border border-slate-200 bg-white p-8 text-slate-600"
        >
          Loading the embedding fixture…
        </div>

        <div
          :if={@backend_state == :unavailable}
          id="embedding-unavailable"
          role="alert"
          class="rounded-2xl border border-amber-300 bg-amber-50 p-6 text-amber-950"
        >
          The backend is unavailable. Refresh when the service recovers.
        </div>

        <div :if={@backend_state == :ready} id="embedding-surface" class="studio-stack">
          <p :if={@upload_error} id="embedding-upload-error" role="alert" class="ullm-widget-error">
            {@upload_error}
          </p>
          <section
            :for={spec <- @instance_specs}
            id={"#{spec.prefix}-host"}
            aria-label={spec.label}
            class="studio-card p-0"
          >
            <% instance = Map.fetch!(@instances, spec.key) %>
            <.live_component
              module={HardenLlmWeb.ProfileWidgetComponent}
              id={spec.component_id}
              id_prefix={spec.prefix}
              profiles={@profiles}
              model_catalog={host_model_catalog(@profiles)}
              selected_profile_id={instance.selected_profile_id}
              reasoning_effort={instance.reasoning_effort}
              cache_mode={instance.cache_mode}
              model_id={instance.model_id}
              config_open={instance.config_open}
              options_open={instance.options_open}
              retry_open={instance.retry_open}
              pricing_open={instance.pricing_open}
              fold_disabled={false}
              session_handle={@session_handle}
              bundle_upload={Map.fetch!(@uploads, spec.bundle_upload)}
              escalation_bundle_upload={Map.fetch!(@uploads, spec.escalation_bundle_upload)}
            />
          </section>
        </div>
      </main>
    </Layouts.app>
    """
  end

  def status_label(:loading), do: "Checking backend"
  def status_label(:ready), do: "Backend ready"
  def status_label(:unavailable), do: "Backend unavailable"

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
