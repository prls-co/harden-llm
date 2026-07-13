defmodule HardenLlmWeb.HistoryLive do
  use HardenLlmWeb, :live_view

  alias HardenLlmWeb.{APIError, HardenAPI, Observability}

  @impl true
  def mount(_params, _session, socket) do
    socket =
      socket
      |> assign(:page_title, "History")
      |> assign(:loading?, true)
      |> assign(:history_by_id, %{})
      |> assign(:next_cursor, nil)
      |> assign(:selected_trace_id, nil)
      |> assign(:trace, nil)
      |> assign(:trace_state, :idle)
      |> assign(:operation_error, nil)
      |> assign(:clear_confirm?, false)
      |> assign(:pending, nil)
      |> stream_configure(:history, dom_id: &history_dom_id/1)
      |> stream(:history, [])

    if connected?(socket) do
      handle = socket.assigns.session_handle

      {:ok,
       start_async(
         socket,
         :load_history,
         Observability.propagate(fn -> HardenAPI.list_history(handle, limit: 20) end)
       )}
    else
      {:ok, socket}
    end
  end

  @impl true
  def handle_async(:load_history, {:ok, {:ok, page, _state}}, socket) do
    {:noreply, put_page(socket, page, true)}
  end

  def handle_async(:load_history, _result, socket) do
    {:noreply,
     socket
     |> assign(:loading?, false)
     |> assign(:operation_error, "History is temporarily unavailable.")}
  end

  def handle_async(
        {:load_more, reference},
        {:ok, {:ok, page, _state}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply, socket |> assign(:pending, nil) |> put_page(page, false)}
  end

  def handle_async(
        {:trace, reference, trace_id},
        {:ok, {:ok, trace, _state}},
        %{assigns: %{pending: reference, selected_trace_id: trace_id}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:trace_state, :ready)
     |> assign(:trace, trace)}
  end

  def handle_async(
        {:trace, reference, trace_id},
        _result,
        %{assigns: %{pending: reference, selected_trace_id: trace_id}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:trace_state, :error)
     |> assign(:trace, nil)}
  end

  def handle_async({:trace, _reference, _trace_id}, _result, socket), do: {:noreply, socket}

  def handle_async(
        {:delete, reference, run_id},
        {:ok, {:ok, _result, _state}},
        %{assigns: %{pending: reference}} = socket
      ) do
    item = socket.assigns.history_by_id[run_id]

    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:history_by_id, Map.delete(socket.assigns.history_by_id, run_id))
     |> stream_delete(:history, item)
     |> put_flash(:info, "History item deleted.")}
  end

  def handle_async(
        {:clear, reference},
        {:ok, {:ok, _result, _state}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:clear_confirm?, false)
     |> assign(:history_by_id, %{})
     |> assign(:next_cursor, nil)
     |> stream(:history, [], reset: true)
     |> put_flash(:info, "History cleared.")}
  end

  def handle_async(
        {_operation, reference, _id},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply, socket |> assign(:pending, nil) |> assign(:operation_error, error.message)}
  end

  def handle_async(
        {:clear, reference},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply, socket |> assign(:pending, nil) |> assign(:operation_error, error.message)}
  end

  def handle_async(_operation, _result, socket), do: {:noreply, socket}

  @impl true
  def handle_event(
        "load-more",
        _params,
        %{assigns: %{pending: nil, next_cursor: cursor}} = socket
      )
      when is_binary(cursor) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    {:noreply,
     socket
     |> assign(:pending, reference)
     |> start_async(
       {:load_more, reference},
       Observability.propagate(fn ->
         HardenAPI.list_history(handle, cursor: cursor, limit: 20)
       end)
     )}
  end

  def handle_event("load-more", _params, socket), do: {:noreply, socket}

  def handle_event("open-trace", %{"trace-id" => trace_id}, socket) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    {:noreply,
     socket
     |> assign(:pending, reference)
     |> assign(:selected_trace_id, trace_id)
     |> assign(:trace, nil)
     |> assign(:trace_state, :loading)
     |> start_async(
       {:trace, reference, trace_id},
       Observability.propagate(fn -> HardenAPI.get_trace(handle, trace_id) end)
     )}
  end

  def handle_event("close-trace", _params, socket) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:selected_trace_id, nil)
     |> assign(:trace, nil)
     |> assign(:trace_state, :idle)}
  end

  def handle_event("delete", %{"run-id" => run_id}, %{assigns: %{pending: nil}} = socket) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    {:noreply,
     socket
     |> assign(:pending, reference)
     |> start_async(
       {:delete, reference, run_id},
       Observability.propagate(fn -> HardenAPI.delete_history(handle, run_id) end)
     )}
  end

  def handle_event("delete", _params, socket), do: {:noreply, socket}

  def handle_event("confirm-clear", _params, socket),
    do: {:noreply, assign(socket, :clear_confirm?, true)}

  def handle_event("cancel-clear", _params, socket),
    do: {:noreply, assign(socket, :clear_confirm?, false)}

  def handle_event("clear", _params, %{assigns: %{pending: nil}} = socket) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    {:noreply,
     socket
     |> assign(:pending, reference)
     |> start_async(
       {:clear, reference},
       Observability.propagate(fn -> HardenAPI.clear_history(handle) end)
     )}
  end

  def handle_event("clear", _params, socket), do: {:noreply, socket}

  def handle_event("restore", %{"run-id" => run_id}, socket) do
    with item when is_map(item) <- socket.assigns.history_by_id[run_id],
         request when is_map(request) <- item["request"],
         state <- restore_state(request, item),
         {:ok, _result, _state} <- HardenAPI.save_state(socket.assigns.session_handle, state) do
      {:noreply, push_navigate(socket, to: ~p"/workspace")}
    else
      _ ->
        {:noreply, assign(socket, :operation_error, "This history item could not be restored.")}
    end
  end

  def observation_data(observation), do: Jason.encode!(observation["data"] || %{}, pretty: true)

  defp put_page(socket, page, reset?) do
    items = page["items"] || []
    merged = Enum.reduce(items, socket.assigns.history_by_id, &Map.put(&2, &1["runId"], &1))

    socket
    |> assign(:loading?, false)
    |> assign(:history_by_id, merged)
    |> assign(:next_cursor, page["nextCursor"])
    |> stream(:history, items, reset: reset?)
  end

  defp restore_state(request, item) do
    %{
      "schemaVersion" => 1,
      "selectedProfileId" => request["profileId"] || item["profileId"],
      "modelId" => request["modelId"] || "",
      "systemPrompt" => request["systemPrompt"] || "",
      "userPrompt" => request["userPrompt"] || "",
      "callType" => request["callType"] || "text",
      "schema" => request["schema"],
      "structuredRepair" => request["structuredRepair"] || false,
      "cacheMode" => request["cacheMode"] || "off"
    }
  end

  defp history_dom_id(item), do: "history-#{item["runId"]}"
end
