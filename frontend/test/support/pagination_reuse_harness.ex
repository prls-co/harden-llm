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

  @api_records Enum.map(1..9, fn number ->
                 %{id: "api-#{number}", title: "API item #{number}", value: "draft #{number}"}
               end)

  @batch_records Enum.map(1..7, fn number ->
                   %{id: "batch-#{number}", title: "Batch item #{number}"}
                 end)

  @impl true
  def mount(_params, session, socket) do
    source = if session["source"] == "api", do: :api, else: :local
    records = if source == :api, do: @api_records, else: @local_records

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

    {:noreply,
     socket
     |> assign(:search, search)
     |> assign(:filtered_records, filtered_records)
     |> assign(:list_page, 1)
     |> assign(:list_total_count, length(filtered_records))}
  end

  def handle_event("edit-draft", %{"record-id" => record_id, "value" => value}, socket)
      when is_binary(record_id) and is_binary(value) do
    {:noreply, update(socket, :drafts, &Map.put(&1, record_id, value))}
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
    page_size_change? = Map.has_key?(params, "page-size")
    page = if page_size_change?, do: 1, else: PaginationState.normalize_page(params["page"], 1)
    page_size = PaginationState.normalize_page_size(params["page-size"], @list_page_sizes, 2)

    {:noreply, assign(socket, list_page: page, list_page_size: page_size)}
  end

  def handle_event(
        "paginate-reuse",
        %{"pagination-id" => "reuse-batch-pagination"} = params,
        socket
      ) do
    page_size_change? = Map.has_key?(params, "page-size")
    page = if page_size_change?, do: 1, else: PaginationState.normalize_page(params["page"], 1)
    page_size = PaginationState.normalize_page_size(params["page-size"], @batch_page_sizes, 2)

    {:noreply, assign(socket, batch_page: page, batch_page_size: page_size)}
  end

  def handle_event("paginate-reuse", _params, socket), do: {:noreply, socket}

  @impl true
  def render(assigns) do
    list_records =
      page_records(assigns.filtered_records, assigns.list_page, assigns.list_page_size)

    batch_records = page_records(@batch_records, assigns.batch_page, assigns.batch_page_size)

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
          <label for={"reuse-draft-#{record.id}"}>{record.title}</label>
          <input
            id={"reuse-draft-#{record.id}"}
            name="value"
            value={@drafts[record.id]}
            phx-change="edit-draft"
            phx-value-record-id={record.id}
          />
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
end
