defmodule HardenLlmWeb.WorkspaceLive do
  use HardenLlmWeb, :live_view

  alias HardenLlm.Reference
  alias HardenLlm.Reference.Draft
  alias HardenLlmWeb.{APIError, HardenAPI, WorkspaceRequest, WorkspaceSchema}

  @history_page_size 10
  @form_keys [
    "model",
    "systemPrompt",
    "userPrompt",
    "structured",
    "schema",
    "recoveryJson",
    "reasoningEffort"
  ]

  @impl true
  def mount(params, _session, socket) do
    page = positive_page(params["page"])

    socket =
      socket
      |> assign(:page_title, "Harden-LLM reference workspace")
      |> assign(:models, [])
      |> assign(:models_status, :loading)
      |> assign(:history, %{items: [], page: page, page_size: @history_page_size, total_count: 0})
      |> assign(:history_loading?, true)
      |> assign(:history_error, nil)
      |> assign(:statistics, nil)
      |> assign(:draft_error, nil)
      |> assign(:draft_saving?, false)
      |> assign(:draft_revision, 0)
      |> assign(:draft_pending, nil)
      |> assign(:form_edited?, false)
      |> assign(:form, to_form(WorkspaceRequest.default_form(), as: :run))
      |> assign(:run_ref, nil)
      |> assign(:recording?, false)
      |> assign(:submission_id, nil)
      |> assign(:run_result, nil)
      |> assign(:run_error, nil)
      |> assign(:recording_error, nil)
      |> assign(:schema_check, %{status: :idle, message: ""})

    if connected?(socket) do
      user_id = socket.assigns.access_context.user_id

      socket =
        socket
        |> start_async(:models, &HardenAPI.models/0)
        |> start_async(:history, fn -> Reference.list_calls(page, @history_page_size) end)
        |> start_async(:statistics, &Reference.statistics/0)
        |> start_async(:draft, fn -> Reference.get_draft(user_id) end)

      {:ok, socket}
    else
      {:ok, socket}
    end
  end

  @impl true
  def handle_event("save-draft", %{"run" => params}, socket) do
    form = normalize_form(params)
    socket = assign(socket, :form, to_form(form, as: :run))
    {:noreply, save_draft(socket, form)}
  end

  def handle_event(
        "run",
        %{"run" => params},
        %{assigns: %{run_ref: nil, recording?: false}} = socket
      ) do
    form = normalize_form(params)
    socket = assign(socket, :form, to_form(form, as: :run))

    case WorkspaceRequest.build(form) do
      {:ok, payload} ->
        submission_id = Reference.new_call_id()

        socket =
          socket
          |> assign(:run_error, nil)
          |> assign(:run_result, nil)
          |> assign(:recording_error, nil)
          |> assign(:submission_id, submission_id)
          |> assign(:run_payload, payload)
          |> assign(:run_ref, :pending)
          |> assign(:form_edited?, true)
          |> start_async(:run, fn -> HardenAPI.responses(payload) end)

        {:noreply, socket}

      {:error, message} ->
        {:noreply, assign(socket, :run_error, message)}
    end
  end

  def handle_event("run", _params, socket), do: {:noreply, socket}

  def handle_event("check-schema", _params, socket) do
    schema = socket.assigns.form.params["schema"] || ""

    case WorkspaceSchema.check(schema) do
      {:ok, _schema, message} ->
        {:noreply, assign(socket, :schema_check, %{status: :ok, message: message})}

      {:error, message} ->
        {:noreply, assign(socket, :schema_check, %{status: :error, message: message})}
    end
  end

  def handle_event("restore", %{"id" => id}, socket) do
    with %HardenLlm.Reference.Call{} = call <- Reference.get_call(id) do
      form = WorkspaceRequest.restore(%{"request" => call.request})

      socket =
        socket
        |> assign(:form, to_form(form, as: :run))
        |> assign(:form_edited?, true)
        |> assign(:run_error, nil)
        |> assign(:run_result, nil)
        |> save_draft(form)

      {:noreply, socket}
    else
      _ -> {:noreply, put_flash(socket, :error, "That recorded request is no longer available.")}
    end
  end

  def handle_event("delete-call", %{"id" => id}, socket) do
    {:noreply,
     start_async(socket, :delete_call, fn ->
       {Reference.delete_call(id), id}
     end)}
  end

  def handle_event("clear-history", _params, socket) do
    {:noreply, start_async(socket, :clear_history, &Reference.clear_history/0)}
  end

  def handle_event("history-page", %{"page" => page}, socket) do
    page = positive_page(page)
    {:noreply, load_history(assign(socket, :history_loading?, true), page)}
  end

  @impl true
  def handle_async(:models, {:ok, {:ok, %{"data" => models}}}, socket) when is_list(models) do
    {:noreply, socket |> assign(:models, models) |> assign(:models_status, :ready)}
  end

  def handle_async(:models, _result, socket) do
    {:noreply, assign(socket, :models_status, :unavailable)}
  end

  def handle_async(:history, {:ok, %{items: _items} = history}, socket) do
    {:noreply,
     socket
     |> assign(:history, history)
     |> assign(:history_loading?, false)
     |> assign(:history_error, nil)}
  end

  def handle_async(:history, _result, socket) do
    {:noreply,
     socket
     |> assign(:history_loading?, false)
     |> assign(
       :history_error,
       "Shared history is unavailable. The workspace and inference remain available."
     )}
  end

  def handle_async(:statistics, {:ok, stats}, socket) when is_map(stats) do
    {:noreply, assign(socket, :statistics, stats)}
  end

  def handle_async(:statistics, _result, socket), do: {:noreply, assign(socket, :statistics, nil)}

  def handle_async(:draft, {:ok, %Draft{} = draft}, %{assigns: %{form_edited?: false}} = socket) do
    {:noreply,
     socket
     |> assign(:draft_revision, draft.revision)
     |> assign(:form, to_form(draft.state, as: :run))}
  end

  def handle_async(:draft, {:ok, %Draft{} = draft}, socket) do
    {:noreply, assign(socket, :draft_revision, draft.revision)}
  end

  def handle_async(:draft, {:ok, nil}, socket), do: {:noreply, socket}

  def handle_async(:draft, _result, socket) do
    {:noreply,
     assign(socket, :draft_error, "Draft storage is unavailable; edits remain in this page.")}
  end

  def handle_async(:run, {:ok, {:ok, response}}, socket) do
    id = socket.assigns.submission_id
    payload = socket.assigns.run_payload

    socket =
      socket
      |> assign(:run_ref, nil)
      |> assign(:run_result, response)
      |> assign(:run_error, nil)
      |> assign(:recording?, true)

    {:noreply,
     start_async(socket, :record_call, fn ->
       Reference.record_call(%{
         id: id,
         endpoint: "/v1/responses",
         request: payload,
         outcome: response,
         outcome_kind: "succeeded"
       })
     end)}
  end

  def handle_async(:run, {:ok, {:error, %APIError{} = error}}, socket) do
    id = socket.assigns.submission_id
    payload = socket.assigns.run_payload
    outcome_kind = if error.ambiguous?, do: "unknown", else: "failed"
    outcome = api_error_record(error)

    socket =
      socket
      |> assign(:run_ref, nil)
      |> assign(:run_error, error.message)
      |> assign(:run_result, nil)
      |> assign(:recording?, true)

    {:noreply,
     start_async(socket, :record_call, fn ->
       Reference.record_call(%{
         id: id,
         endpoint: "/v1/responses",
         request: payload,
         outcome: outcome,
         outcome_kind: outcome_kind
       })
     end)}
  end

  def handle_async(:run, _result, socket) do
    {:noreply,
     socket
     |> assign(:run_ref, nil)
     |> assign(:run_error, "The request ended without a usable response.")
     |> assign(:recording?, false)}
  end

  def handle_async(:record_call, {:ok, {:ok, _call}}, socket) do
    socket =
      socket
      |> assign(:recording?, false)
      |> assign(:recording_error, nil)
      |> load_history(socket.assigns.history.page)
      |> start_async(:statistics, &Reference.statistics/0)

    {:noreply, socket}
  end

  def handle_async(:record_call, _result, socket) do
    {:noreply,
     socket
     |> assign(:recording?, false)
     |> assign(:recording_error, "The result is shown, but shared history could not save it.")}
  end

  def handle_async(:delete_call, {:ok, {{:ok, _count}, _id}}, socket) do
    {:noreply,
     socket
     |> load_history(socket.assigns.history.page)
     |> start_async(:statistics, &Reference.statistics/0)}
  end

  def handle_async(:delete_call, _result, socket) do
    {:noreply, put_flash(socket, :error, "The history entry could not be deleted.")}
  end

  def handle_async(:clear_history, {:ok, {:ok, _count}}, socket) do
    {:noreply,
     socket
     |> load_history(1)
     |> start_async(:statistics, &Reference.statistics/0)}
  end

  def handle_async(:clear_history, _result, socket) do
    {:noreply, put_flash(socket, :error, "Shared history could not be cleared.")}
  end

  def handle_async(:save_draft, {:ok, :ok}, socket) do
    case socket.assigns.draft_pending do
      nil ->
        {:noreply, assign(socket, :draft_saving?, false)}

      {revision, form} ->
        socket =
          socket
          |> assign(:draft_pending, nil)
          |> assign(:draft_revision, revision)

        {:noreply, save_draft(socket, form, revision)}
    end
  end

  def handle_async(:save_draft, {:ok, {:error, _reason}}, socket) do
    case socket.assigns.draft_pending do
      nil ->
        {:noreply,
         socket
         |> assign(:draft_saving?, false)
         |> assign(:draft_error, "Draft storage is unavailable; edits remain in this page.")}

      {revision, form} ->
        socket =
          socket
          |> assign(:draft_pending, nil)
          |> assign(:draft_revision, revision)

        {:noreply, save_draft(socket, form, revision)}
    end
  end

  def handle_async(:save_draft, _result, socket) do
    {:noreply,
     socket
     |> assign(:draft_saving?, false)
     |> assign(:draft_error, "Draft storage is unavailable; edits remain in this page.")}
  end

  @impl true
  def handle_info({:prls_revalidate_access, _}, socket), do: {:noreply, socket}

  def status_text(:ready), do: "Proxy reachable"
  def status_text(:loading), do: "Checking proxy"
  def status_text(:unavailable), do: "Proxy unavailable; enter a model ID to retry"

  def call_input(%{request: %{"input" => input}}) when is_binary(input), do: input

  def call_input(%{request: request}),
    do: Jason.encode!(request["input"] || request, pretty: true)

  def call_output(%{outcome_kind: "succeeded", outcome: outcome}),
    do: outcome["output_text"] || Jason.encode!(outcome, pretty: true)

  def call_output(%{outcome: %{"error" => %{"message" => message}}}), do: message
  def call_output(%{outcome: outcome}), do: Jason.encode!(outcome, pretty: true)

  defp save_draft(socket, form, revision \\ nil) do
    socket = assign(socket, :form_edited?, true)
    revision = revision || socket.assigns.draft_revision + 1
    user_id = socket.assigns.access_context.user_id

    if socket.assigns.draft_saving? do
      assign(socket, :draft_pending, {revision, form})
    else
      socket
      |> assign(:draft_saving?, true)
      |> assign(:draft_error, nil)
      |> assign(:draft_revision, revision)
      |> assign(:draft_pending, nil)
      |> start_async(:save_draft, fn -> Reference.save_draft(user_id, revision, form) end)
    end
  end

  defp load_history(socket, page) do
    socket
    |> assign(:history_loading?, true)
    |> start_async(:history, fn -> Reference.list_calls(page, @history_page_size) end)
  end

  defp normalize_form(params) do
    params = Map.take(params, @form_keys)
    form = Map.merge(WorkspaceRequest.default_form(), params)

    Map.put(
      form,
      "structured",
      if(form["structured"] in ["true", "on", "1"], do: "true", else: "false")
    )
  end

  defp api_error_record(%APIError{} = error) do
    %{
      "error" => %{
        "type" => Atom.to_string(error.category),
        "status" => error.status,
        "code" => error.code,
        "message" => error.message,
        "ambiguous" => error.ambiguous?
      }
    }
  end

  defp positive_page(value) when is_integer(value) and value > 0, do: min(value, 1_000_000)

  defp positive_page(value) when is_binary(value) do
    case Integer.parse(value) do
      {page, ""} when page > 0 -> min(page, 1_000_000)
      _ -> 1
    end
  end

  defp positive_page(_), do: 1
end
