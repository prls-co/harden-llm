defmodule HardenLlmWeb.HistoryLive do
  use HardenLlmWeb, :live_view

  alias HardenLlm.LlmTraceProjection
  alias HardenLlmWeb.{APIError, Auth, HardenAPI, Observability}

  @impl true
  def mount(_params, _session, socket) do
    socket =
      socket
      |> assign(:page_title, "History")
      |> assign(:loading?, true)
      |> assign(:history_by_id, %{})
      |> assign(:next_cursor, nil)
      |> assign(:page_size, 20)
      |> assign(:page_number, 1)
      |> assign(:stats, LlmTraceProjection.stats(%{}))
      |> assign(:stats_error, nil)
      |> assign(:stats_ref, nil)
      |> assign(:expanded_history_id, nil)
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

      socket =
        socket
        |> start_async(
          :load_history,
          Observability.propagate(fn -> HardenAPI.list_history(handle, limit: 20) end)
        )
        |> refresh_stats()

      {:ok, socket}
    else
      {:ok, socket}
    end
  end

  @impl true
  def handle_params(%{"trace_id" => trace_id}, _uri, socket)
      when is_binary(trace_id) and trace_id != "" do
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

  def handle_params(_params, _uri, socket), do: {:noreply, socket}

  @impl true
  def handle_async(_operation, {:ok, {:error, %APIError{status: 401}}}, socket) do
    {:noreply, Auth.expire_live(socket)}
  end

  def handle_async(:load_history, {:ok, {:ok, page, _state}}, socket) do
    {:noreply, put_page(socket, page, true, 1)}
  end

  def handle_async(:load_history, _result, socket) do
    {:noreply,
     socket
     |> assign(:loading?, false)
     |> assign(:operation_error, "History is temporarily unavailable.")}
  end

  def handle_async(
        {:load_stats, reference},
        {:ok, {:ok, stats, _state}},
        %{assigns: %{stats_ref: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:stats, LlmTraceProjection.stats(stats))
     |> assign(:stats_error, nil)
     |> assign(:stats_ref, nil)}
  end

  def handle_async(
        {:load_stats, reference},
        _result,
        %{assigns: %{stats_ref: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:stats_error, "Aggregate stats are temporarily unavailable.")
     |> assign(:stats_ref, nil)}
  end

  def handle_async({:load_stats, _reference}, _result, socket), do: {:noreply, socket}

  def handle_async(
        {:load_more, reference},
        {:ok, {:ok, page, _state}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> put_page(page, false, socket.assigns.page_number + 1)}
  end

  def handle_async(
        {:page_size, reference},
        {:ok, {:ok, page, _state}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> put_page(page, true, 1)}
  end

  def handle_async(
        {:page_size, reference},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply, socket |> assign(:pending, nil) |> assign(:operation_error, error.message)}
  end

  def handle_async(
        {:page_size, reference},
        _result,
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:operation_error, "History page could not be loaded.")}
  end

  def handle_async(
        {:load_more, reference},
        {:ok, {:error, %APIError{} = error}},
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply, socket |> assign(:pending, nil) |> assign(:operation_error, error.message)}
  end

  def handle_async(
        {:load_more, reference},
        _result,
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:operation_error, "More history could not be loaded.")}
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
     |> assign(:expanded_history_id, nil)
     |> stream_delete(:history, item)
     |> refresh_stats()
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
     |> assign(:page_number, 1)
     |> assign(:expanded_history_id, nil)
     |> stream(:history, [], reset: true)
     |> refresh_stats()
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

  def handle_async(
        {_operation, reference, _id},
        _result,
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:operation_error, "The history operation could not be completed.")}
  end

  def handle_async(
        {:clear, reference},
        _result,
        %{assigns: %{pending: reference}} = socket
      ) do
    {:noreply,
     socket
     |> assign(:pending, nil)
     |> assign(:operation_error, "History could not be cleared.")}
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
         HardenAPI.list_history(handle, cursor: cursor, limit: socket.assigns.page_size)
       end)
     )}
  end

  def handle_event("load-more", _params, socket), do: {:noreply, socket}

  def handle_event(
        "change-page-size",
        %{"pageSize" => value},
        %{assigns: %{pending: nil}} = socket
      ) do
    case Integer.parse(to_string(value)) do
      {page_size, ""} when page_size in [10, 20, 50, 100] ->
        reference = System.unique_integer([:positive, :monotonic])
        handle = socket.assigns.session_handle

        {:noreply,
         socket
         |> assign(:pending, reference)
         |> assign(:page_size, page_size)
         |> start_async(
           {:page_size, reference},
           Observability.propagate(fn -> HardenAPI.list_history(handle, limit: page_size) end)
         )}

      _ ->
        {:noreply, socket}
    end
  end

  def handle_event("toggle-history", %{"run-id" => run_id}, socket) do
    expanded = if socket.assigns.expanded_history_id == run_id, do: nil, else: run_id
    {:noreply, assign(socket, :expanded_history_id, expanded)}
  end

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
      {:error, %APIError{status: 401}} ->
        {:noreply, Auth.expire_live(socket)}

      _ ->
        {:noreply, assign(socket, :operation_error, "This history item could not be restored.")}
    end
  end

  def observation_data(observation), do: Jason.encode!(observation["data"] || %{}, pretty: true)
  def history_json(value), do: Jason.encode!(value || %{}, pretty: true)

  def history_prompt_preview(item) do
    case get_in(item, ["request", "userPrompt"]) do
      prompt when is_binary(prompt) and prompt != "" -> String.slice(String.trim(prompt), 0, 140)
      _ -> "No prompt preview"
    end
  end

  def expanded_history(history_by_id, run_id) when is_binary(run_id), do: history_by_id[run_id]
  def expanded_history(_history_by_id, _run_id), do: nil

  defp put_page(socket, page, reset?, page_number) do
    items = page["items"] || []
    merged = Enum.reduce(items, socket.assigns.history_by_id, &Map.put(&2, &1["runId"], &1))

    socket
    |> assign(:loading?, false)
    |> assign(:history_by_id, merged)
    |> assign(:next_cursor, page["nextCursor"])
    |> assign(:page_number, page_number)
    |> stream(:history, items, reset: reset?)
  end

  defp refresh_stats(socket) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    socket
    |> assign(:stats_ref, reference)
    |> start_async(
      {:load_stats, reference},
      Observability.propagate(fn -> HardenAPI.get_stats(handle) end)
    )
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
      "schemaShorthand" => request["schemaShorthand"] || "",
      "reasoningEffort" => request["reasoningEffort"] || "lowest",
      "structuredRepair" => request["structuredRepair"] || false,
      "cacheMode" => request["cacheMode"] || "off",
      "maxAttempts" => request["maxAttempts"] || 0,
      "initialBackoffMs" => request["initialBackoffMs"] || 0,
      "maximumBackoffMs" => request["maximumBackoffMs"] || 0,
      "retryNetwork" => request["retryNetwork"],
      "retryRateLimit" => request["retryRateLimit"],
      "retryServerError" => request["retryServerError"],
      "retryEmpty" => request["retryEmpty"],
      "retryParse" => request["retryParse"],
      "repairEscalation" => request["repairEscalation"]
    }
  end

  defp history_dom_id(item), do: "history-#{item["runId"]}"
end
