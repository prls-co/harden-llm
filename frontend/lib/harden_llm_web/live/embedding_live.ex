defmodule HardenLlmWeb.EmbeddingLive do
  @moduledoc """
  A small authenticated host surface for the reusable LLM widget.

  This route is intentionally an ordinary in-flow page rather than a second
  application shell. It demonstrates the host contract by mounting two
  independent widget instances with distinct DOM and upload namespaces.
  """

  use HardenLlmWeb, :live_view

  alias HardenLlmUI.{Client, ProfileWidgetState}

  @instance_specs [
    %{
      key: :primary,
      label: "Primary instance",
      component_id: "embed-primary-llm-widget",
      prefix: "embed-primary",
      bundle_upload: :embed_primary_profile_bundle
    },
    %{
      key: :secondary,
      label: "Secondary instance",
      component_id: "embed-secondary-llm-widget",
      prefix: "embed-secondary",
      bundle_upload: :embed_secondary_profile_bundle
    }
  ]

  @ui_field_by_name %{
    "llmProfileConfigOpen" => :config_open,
    "modelOptionsOpen" => :options_open,
    "retryRepairOpen" => :retry_open,
    "pricingOpen" => :pricing_open,
    "credentialOpen" => :credential_open
  }

  @impl true
  def mount(_params, _session, socket) do
    socket =
      socket
      |> assign(:page_title, "Embed LLM")
      |> assign(
        :api_context,
        HardenLlmWeb.APIContext.server("workspace:" <> socket.assigns.access_context.user_id)
      )
      |> assign(:loading?, true)
      |> assign(:backend_state, :loading)
      |> assign(:profiles, [])
      |> assign(:recovery_policy_default, %{})
      |> assign(:instances, initial_instances())
      |> assign(:upload_error, nil)
      |> assign(:instance_specs, @instance_specs)
      |> allow_upload(:embed_primary_profile_bundle, bundle_upload_options())
      |> allow_upload(:embed_secondary_profile_bundle, bundle_upload_options())

    if connected?(socket) do
      context = socket.assigns.api_context

      {:ok, start_async(socket, :hydrate, Client.propagate(fn -> hydrate(context) end))}
    else
      {:ok, socket}
    end
  end

  @impl true
  def handle_async(:hydrate, {:ok, {:ok, hydration}}, socket) do
    {:noreply,
     socket
     |> assign(:loading?, false)
     |> assign(:backend_state, :ready)
     |> assign(:profiles, hydration.profiles)
     |> assign(:recovery_policy_default, hydration.recovery_policy_default)
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

  def handle_event("import-bundle", %{"widget" => prefix}, socket) do
    case upload_name(prefix) do
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
         {:ok, %{"profiles" => profiles}} <-
           Client.import_profile_bundle(socket.assigns.api_context, bundle) do
      {:noreply,
       socket
       |> assign(:profiles, profiles)
       |> assign(:upload_error, nil)
       |> put_flash(:info, "Profile bundle imported atomically.")}
    else
      _ ->
        {:noreply, assign(socket, :upload_error, "The selected bundle was rejected.")}
    end
  end

  defp route_widget_message(socket, key, {:profile_widget_ui, name, open}) do
    case Map.fetch(@ui_field_by_name, name) do
      {:ok, field} -> update_instance(socket, key, &Map.put(&1, field, truthy?(open)))
      :error -> {:noreply, socket}
    end
  end

  defp route_widget_message(socket, key, {:profile_widget_selection, selection})
       when is_map(selection) do
    update_instance(socket, key, fn instance ->
      instance
      |> Map.put(:selected_profile_id, Map.get(selection, :profile_id, ""))
      |> Map.put(:model_id, Map.get(selection, :model_id, ""))
      |> Map.put(:reasoning_effort, Map.get(selection, :reasoning_effort, "lowest"))
      |> Map.put(:recovery_policy, Map.get(selection, :recovery_policy, %{}))
      |> Map.put(:provider_options, Map.get(selection, :provider_options, %{}))
    end)
  end

  defp route_widget_message(socket, key, {:profile_widget_control, field, value})
       when field in ~w(modelId reasoningEffort cacheMode webSearch) do
    assign_key =
      %{
        "modelId" => :model_id,
        "reasoningEffort" => :reasoning_effort,
        "cacheMode" => :cache_mode,
        "webSearch" => :web_search
      }[field]

    update_instance(socket, key, &Map.put(&1, assign_key, value))
  end

  defp route_widget_message(socket, key, {:profile_widget_editor, editor}) do
    update_instance(socket, key, &Map.put(&1, :editor_draft, editor))
  end

  defp route_widget_message(socket, key, {:profile_widget_provider_options, options})
       when is_map(options) do
    update_instance(socket, key, &Map.put(&1, :provider_options, options))
  end

  defp route_widget_message(socket, key, {:profile_widget_recovery, policy})
       when is_map(policy) do
    update_instance(socket, key, fn instance ->
      current = Map.get(instance, :recovery_policy, %{})

      Map.put(
        instance,
        :recovery_policy,
        ProfileWidgetState.merge_recovery_policy(current, policy)
      )
    end)
  end

  defp route_widget_message(socket, key, {:profile_widget_profile_dirty, requires_save?}) do
    update_instance(socket, key, &Map.put(&1, :requires_save?, requires_save?))
  end

  defp route_widget_message(socket, _key, {:profile_widget_catalog, profiles})
       when is_list(profiles),
       do: {:noreply, assign(socket, :profiles, profiles)}

  defp route_widget_message(socket, _key, _message), do: {:noreply, socket}

  defp update_instance(socket, key, update) do
    {:noreply, update(socket, :instances, &Map.update!(&1, key, update))}
  end

  defp hydrate(context) do
    with {:ok, %{"payload" => state}} <- Client.get_state(context),
         {:ok, %{"profiles" => profiles}} <- Client.list_profiles(context) do
      selected =
        ProfileWidgetState.resolve_selected_profile_id(profiles, state["selectedProfileId"])

      if selected == "" do
        {:ok, %{profiles: profiles, instances: initial_instances(), recovery_policy_default: %{}}}
      else
        with {:ok, draft} <- Client.get_profile(context, selected) do
          inputs = draft["inputs"]
          policy = draft["definition"]["recoveryPolicy"]

          instances =
            Map.new(initial_instances(), fn {key, instance} ->
              {key,
               Map.merge(instance, %{
                 selected_profile_id: selected,
                 model_id: inputs["modelId"],
                 reasoning_effort: inputs["reasoningEffort"] || "lowest",
                 cache_mode: inputs["cacheMode"] || "cache",
                 web_search: inputs["webSearch"] || false,
                 recovery_policy: inputs["recoveryPolicy"],
                 provider_options: inputs["providerOptions"],
                 run_inputs: inputs,
                 editor_draft: HardenLlmUI.ProfileForm.profile_form(draft)
               })}
            end)

          {:ok, %{profiles: profiles, instances: instances, recovery_policy_default: policy}}
        end
      end
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
         web_search: false,
         run_inputs: %{},
         editor_draft: nil,
         config_open: false,
         credential_open: false,
         options_open: false,
         retry_open: false,
         pricing_open: false,
         provider_options: %{},
         recovery_policy: %{},
         requires_save?: false
       }}
    end)
  end

  defp instance_key(prefix) do
    Enum.find_value(@instance_specs, fn spec -> if spec.prefix == prefix, do: spec.key end)
  end

  defp upload_name(prefix) do
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

  defp truthy?(value), do: value in [true, "true", "on", "1"]

  @impl true
  def render(assigns) do
    ~H"""
    <Layouts.app flash={@flash} current_user={@current_user}>
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
              module={HardenLlmUI.ProfileWidgetComponent}
              id={spec.component_id}
              id_prefix={spec.prefix}
              profiles={@profiles}
              model_catalog={ProfileWidgetState.model_catalog(@profiles)}
              selected_profile_id={instance.selected_profile_id}
              reasoning_effort={instance.reasoning_effort}
              cache_mode={instance.cache_mode}
              model_id={instance.model_id}
              config_open={instance.config_open}
              credential_open={instance.credential_open}
              options_open={instance.options_open}
              retry_open={instance.retry_open}
              recovery_policy={instance.recovery_policy}
              recovery_policy_default={@recovery_policy_default}
              pricing_open={instance.pricing_open}
              fold_disabled={false}
              api_context={@api_context}
              run_inputs={instance.run_inputs}
              editor_draft={instance.editor_draft}
              web_search={instance.web_search}
              bundle_export_url={~p"/profiles/bundle"}
              bundle_upload={Map.fetch!(@uploads, spec.bundle_upload)}
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
end
