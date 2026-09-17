defmodule HardenLlmWeb.PaginationReuseHarness do
  @moduledoc false

  use HardenLlmWeb, :live_view

  alias PrlsUI.PaginationState

  @list_page_sizes [2, 4]
  @batch_page_sizes [2, 5]

  @local_records Enum.map(1..9, fn number ->
                   %{
                     id: "local-#{number}",
                     title: "Local item #{number}",
                     value: "draft #{number}"
                   }
                 end)

  @fixture_records Enum.map(1..9, fn number ->
                     %{
                       id: "fixture-#{number}",
                       title: "Fixture item #{number}",
                       value: "draft #{number}"
                     }
                   end)

  @batch_records Enum.map(1..7, fn number ->
                   %{id: "batch-#{number}", title: "Batch item #{number}"}
                 end)

  @batch_scope_options [
    {"All matches", "all"},
    {"First three", "short"}
  ]

  @impl true
  def mount(_params, session, socket) do
    source = if session["source"] == "fixture", do: :fixture, else: :local
    records = if source == :fixture, do: @fixture_records, else: @local_records

    {:ok,
     socket
     |> assign(:source, source)
     |> assign(:records, records)
     |> assign(:filtered_records, records)
     |> assign(:search, "")
     |> assign(:list_page, 1)
     |> assign(:list_page_size, 2)
     |> assign(:list_page_sizes, @list_page_sizes)
     |> assign(:list_total_count, length(records))
     |> assign(:batch_records, @batch_records)
     |> assign(:batch_scope, "all")
     |> assign(:batch_scope_options, @batch_scope_options)
     |> assign(:batch_page, 1)
     |> assign(:batch_page_size, 2)
     |> assign(:batch_page_sizes, @batch_page_sizes)
     |> assign(:batch_total_count, length(@batch_records))
     |> assign(:drafts, Map.new(records, &{&1.id, &1.value}))
     |> assign(:selected_ids, MapSet.new())}
  end

  @impl true
  def handle_event("search", %{"search" => search}, socket) when is_binary(search) do
    filtered_records = filter_records(socket.assigns.records, search)

    current = %{
      page: socket.assigns.list_page,
      page_size: socket.assigns.list_page_size
    }

    page =
      case PaginationState.transition(current, :reset, page_size_options: @list_page_sizes) do
        {:ok, %{page: page}} -> page
        :unchanged -> current.page
      end

    {:noreply,
     socket
     |> assign(:search, search)
     |> assign(:filtered_records, filtered_records)
     |> assign(:list_page, page)
     |> assign(:list_total_count, length(filtered_records))}
  end

  def handle_event("edit-draft", %{"record-id" => record_id, "value" => value}, socket)
      when is_binary(record_id) and is_binary(value) do
    {:noreply, update(socket, :drafts, &Map.put(&1, record_id, value))}
  end

  def handle_event("change-batch-scope", %{"scope" => scope}, socket) do
    case batch_scope_records(scope) do
      records when is_list(records) ->
        %{page: page, page_size: page_size} =
          reset_criteria(
            socket.assigns.batch_page,
            socket.assigns.batch_page_size,
            @batch_page_sizes
          )

        {:noreply,
         socket
         |> assign(:batch_records, records)
         |> assign(:batch_scope, scope)
         |> assign(:batch_page, page)
         |> assign(:batch_page_size, page_size)
         |> assign(:batch_total_count, length(records))}

      nil ->
        {:noreply, socket}
    end
  end

  def handle_event("toggle-selection", %{"record-id" => record_id}, socket)
      when is_binary(record_id) do
    selected_ids =
      if MapSet.member?(socket.assigns.selected_ids, record_id) do
        MapSet.delete(socket.assigns.selected_ids, record_id)
      else
        MapSet.put(socket.assigns.selected_ids, record_id)
      end

    {:noreply, assign(socket, :selected_ids, selected_ids)}
  end

  def handle_event(
        "paginate-reuse",
        %{"pagination-id" => "reuse-list-pagination"} = params,
        socket
      ) do
    update_pagination(socket, :list_page, :list_page_size, params, @list_page_sizes)
  end

  def handle_event(
        "paginate-reuse",
        %{"pagination-id" => "reuse-batch-pagination"} = params,
        socket
      ) do
    update_pagination(socket, :batch_page, :batch_page_size, params, @batch_page_sizes)
  end

  def handle_event("paginate-reuse", _params, socket), do: {:noreply, socket}

  @impl true
  def render(assigns) do
    list_records =
      page_records(assigns.filtered_records, assigns.list_page, assigns.list_page_size)

    batch_records =
      page_records(assigns.batch_records, assigns.batch_page, assigns.batch_page_size)

    assigns =
      assigns
      |> assign(:list_records, list_records)
      |> assign(:batch_records, batch_records)

    ~H"""
    <section id="pagination-reuse-harness">
      <form id="reuse-search-form" phx-change="search">
        <label for="reuse-search">Search editable list</label>
        <input id="reuse-search" name="search" value={@search} />
      </form>

      <section id="reuse-editable-list" aria-label="Editable list">
        <p id="reuse-source">source={@source}</p>
        <div :for={record <- @list_records} id={"reuse-record-#{record.id}"}>
          <form id={"reuse-edit-form-#{record.id}"} phx-change="edit-draft">
            <input type="hidden" name="record-id" value={record.id} />
            <label for={"reuse-draft-#{record.id}"}>{record.title}</label>
            <input
              id={"reuse-draft-#{record.id}"}
              name="value"
              value={@drafts[record.id]}
            />
          </form>
          <button
            id={"reuse-select-#{record.id}"}
            type="button"
            phx-click="toggle-selection"
            phx-value-record-id={record.id}
            aria-pressed={to_string(MapSet.member?(@selected_ids, record.id))}
          >
            Select
          </button>
        </div>
        <.pagination
          id="reuse-list-pagination"
          page={@list_page}
          page_size={@list_page_size}
          total_count={@list_total_count}
          page_size_options={@list_page_sizes}
          event="paginate-reuse"
        />
      </section>

      <section id="reuse-batch-list" aria-label="Batch list">
        <form id="reuse-batch-scope-form" phx-change="change-batch-scope">
          <label for="reuse-batch-scope">Batch scope</label>
          <select id="reuse-batch-scope" name="scope">
            <option
              :for={{label, value} <- @batch_scope_options}
              value={value}
              selected={value == @batch_scope}
            >
              {label}
            </option>
          </select>
        </form>
        <div :for={record <- @batch_records} id={"reuse-batch-record-#{record.id}"}>
          {record.title}
        </div>
        <.pagination
          id="reuse-batch-pagination"
          page={@batch_page}
          page_size={@batch_page_size}
          total_count={@batch_total_count}
          page_size_options={@batch_page_sizes}
          event="paginate-reuse"
        />
      </section>
    </section>
    """
  end

  defp filter_records(records, search) do
    normalized = String.downcase(String.trim(search))

    if normalized == "" do
      records
    else
      Enum.filter(records, &String.contains?(String.downcase(&1.title), normalized))
    end
  end

  defp page_records(records, page, page_size) do
    records
    |> Enum.drop((page - 1) * page_size)
    |> Enum.take(page_size)
  end

  defp update_pagination(socket, page_key, page_size_key, params, page_size_options) do
    current = %{
      page: Map.fetch!(socket.assigns, page_key),
      page_size: Map.fetch!(socket.assigns, page_size_key)
    }

    intent =
      if Map.has_key?(params, "page-size"),
        do: {:page_size, params["page-size"]},
        else: {:page, params["page"]}

    case PaginationState.transition(current, intent, page_size_options: page_size_options) do
      {:ok, %{page: page, page_size: page_size}} ->
        {:noreply, assign(socket, [{page_key, page}, {page_size_key, page_size}])}

      :unchanged ->
        {:noreply, socket}

      {:error, _reason} ->
        {:noreply, socket}
    end
  end

  defp reset_criteria(page, page_size, page_size_options) do
    current = %{page: page, page_size: page_size}

    case PaginationState.transition(current, :reset, page_size_options: page_size_options) do
      {:ok, criteria} -> criteria
      :unchanged -> current
    end
  end

  defp batch_scope_records("all"), do: @batch_records
  defp batch_scope_records("short"), do: Enum.take(@batch_records, 3)
  defp batch_scope_records(_scope), do: nil
end
