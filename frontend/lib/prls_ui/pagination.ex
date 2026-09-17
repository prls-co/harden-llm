defmodule PrlsUI.Pagination do
  @moduledoc """
  Reusable server-owned numbered pagination controls.

  It renders navigation, a page-size selector, a summary, and an explicit
  page-jump form. It owns no collection, request, draft, or selection state;
  the host handles the configured event and supplies the displayed metadata.
  Render it beside, rather than inside, a host editor form because the size
  and jump controls own their scoped forms.
  """

  use Phoenix.Component

  alias PrlsUI.PaginationState

  attr :id, :string, required: true
  attr :page, :integer, required: true
  attr :page_size, :integer, required: true
  attr :total_count, :integer, required: true
  attr :page_size_options, :list, default: [10, 25, 50, 100]
  attr :event, :string, default: "paginate"
  attr :target, :any, default: nil
  attr :loading?, :boolean, default: false
  attr :disabled?, :boolean, default: false
  attr :aria_label, :string, default: "Pagination"
  attr :class, :string, default: ""

  @spec pagination(map()) :: Phoenix.LiveView.Rendered.t()
  def pagination(assigns) do
    total_pages = PaginationState.total_pages(assigns.total_count, assigns.page_size)

    summary =
      PaginationState.summary(%{
        page: assigns.page,
        page_size: assigns.page_size,
        total_count: assigns.total_count
      })

    window = PaginationState.page_window(assigns.page, total_pages)
    navigation_disabled? = assigns.disabled? or assigns.loading? or total_pages <= 1

    assigns =
      assigns
      |> assign(:total_pages, total_pages)
      |> assign(:summary, summary)
      |> assign(:window, window)
      |> assign(:navigation_disabled?, navigation_disabled?)

    ~H"""
    <nav id={@id} aria-label={@aria_label} data-pagination class={@class}>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p id={"#{@id}-summary"} class="text-xs text-slate-600" aria-live="polite">
          {@summary}
        </p>
        <div class="flex flex-wrap items-center gap-3">
          <form
            id={"#{@id}-page-size-form"}
            phx-change={@event}
            phx-target={@target}
            class="flex items-center gap-1"
          >
            <input type="hidden" name="pagination-id" value={@id} />
            <label for={"#{@id}-page-size"} class="text-xs font-medium text-slate-600">Per page</label>
            <select
              id={"#{@id}-page-size"}
              name="page-size"
              aria-label="Items per page"
              disabled={@disabled? or @loading?}
              class="rounded-lg border border-slate-200 bg-white px-2 py-1 text-xs text-slate-700 disabled:opacity-50"
            >
              <option
                :for={page_size <- @page_size_options}
                value={page_size}
                selected={page_size == @page_size}
              >
                {page_size}
              </option>
            </select>
          </form>
          <form
            id={"#{@id}-jump-form"}
            phx-submit={@event}
            phx-target={@target}
            class="flex items-center gap-1"
          >
            <input type="hidden" name="pagination-id" value={@id} />
            <input
              id={"#{@id}-jump"}
              name="page"
              type="number"
              min="1"
              inputmode="numeric"
              aria-label="Go to page"
              placeholder="Page"
              class="w-20 rounded-lg border border-slate-200 px-2 py-1 text-xs text-slate-700"
              disabled={@disabled? or @loading?}
            />
            <button
              type="submit"
              disabled={@disabled? or @loading? or @total_pages == 0}
              class="rounded-lg border border-slate-200 px-2 py-1 text-xs font-semibold text-slate-700 hover:bg-slate-50 disabled:opacity-50"
            >Go</button>
          </form>
        </div>
      </div>
      <div
        id={"#{@id}-navigation"}
        class="mt-3 flex flex-wrap items-center justify-center gap-1"
        role="list"
      >
        <.pagination_button
          id={"#{@id}-first"}
          pagination_id={@id}
          label="First"
          page={1}
          event={@event}
          target={@target}
          disabled={@navigation_disabled? or @page == 1}
        />
        <.pagination_button
          id={"#{@id}-previous"}
          pagination_id={@id}
          label="Previous"
          page={max(@page - 1, 1)}
          event={@event}
          target={@target}
          disabled={@navigation_disabled? or @page == 1}
        />
        <span
          :for={{:ellipsis, index} <- Enum.with_index(@window)}
          id={"#{@id}-ellipsis-#{index}"}
          aria-hidden="true"
          class="px-1 text-xs text-slate-400"
        >…</span>
        <.pagination_button
          :for={page <- @window}
          :if={is_integer(page)}
          id={"#{@id}-page-#{page}"}
          pagination_id={@id}
          label={Integer.to_string(page)}
          page={page}
          event={@event}
          target={@target}
          current={page == @page}
          disabled={@disabled? or @loading? or page == @page}
        />
        <.pagination_button
          id={"#{@id}-next"}
          pagination_id={@id}
          label="Next"
          page={min(@page + 1, max(@total_pages, 1))}
          event={@event}
          target={@target}
          disabled={@navigation_disabled? or @page >= @total_pages}
        />
        <.pagination_button
          id={"#{@id}-last"}
          pagination_id={@id}
          label="Last"
          page={max(@total_pages, 1)}
          event={@event}
          target={@target}
          disabled={@navigation_disabled? or @total_pages == 0 or @page >= @total_pages}
        />
      </div>
    </nav>
    """
  end

  attr :id, :string, required: true
  attr :pagination_id, :string, required: true
  attr :label, :string, required: true
  attr :page, :integer, required: true
  attr :event, :string, required: true
  attr :target, :any, default: nil
  attr :current, :boolean, default: false
  attr :disabled, :boolean, default: false

  defp pagination_button(assigns) do
    ~H"""
    <button
      id={@id}
      type="button"
      role="listitem"
      phx-click={@event}
      phx-value-pagination-id={@pagination_id}
      phx-value-page={@page}
      phx-target={@target}
      aria-current={if @current, do: "page", else: nil}
      disabled={@disabled}
      class={[
        "rounded-lg border px-2.5 py-1 text-xs font-semibold disabled:opacity-50",
        if(@current,
          do: "border-teal-600 bg-teal-50 text-teal-800",
          else: "border-slate-200 text-slate-700 hover:bg-slate-50"
        )
      ]}
    >
      {@label}
    </button>
    """
  end
end
