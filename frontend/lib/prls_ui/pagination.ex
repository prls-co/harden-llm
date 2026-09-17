defmodule PrlsUI.Pagination do
  @moduledoc """
  Reusable server-owned numbered pagination controls.

  It renders navigation, an optional page-size selector, a summary, and an
  optional explicit page-jump form. It owns no collection, request, draft, or
  selection state; the host handles the configured event and supplies the
  displayed metadata. Render it beside, rather than inside, a host editor form
  because the size and jump controls own their scoped forms.
  """

  use Phoenix.Component

  alias PrlsUI.PaginationState

  attr(:id, :string, required: true)
  attr(:page, :integer, required: true)
  attr(:page_size, :integer, required: true)
  attr(:total_count, :integer, required: true)
  attr(:page_size_options, :list, default: [10, 25, 50, 100])
  attr(:show_page_size?, :boolean, default: true)
  attr(:show_jump?, :boolean, default: true)
  attr(:sibling_count, :integer, default: 1)
  attr(:event, :string, default: "paginate")
  attr(:target, :any, default: nil)
  attr(:loading?, :boolean, default: false)
  attr(:disabled?, :boolean, default: false)
  attr(:aria_label, :string, default: "Pagination")
  attr(:class, :string, default: "")

  @spec pagination(map()) :: Phoenix.LiveView.Rendered.t()
  def pagination(assigns) do
    total_pages = PaginationState.total_pages(assigns.total_count, assigns.page_size)

    if not Enum.all?(assigns.page_size_options, &(is_integer(&1) and &1 > 0)) do
      raise ArgumentError,
            "pagination page_size_options must contain positive integers: #{inspect(assigns.page_size_options)}"
    end

    metadata =
      PaginationState.validate_metadata!(%{
        page: assigns.page,
        page_size: assigns.page_size,
        total_count: assigns.total_count
      })

    summary = PaginationState.summary(metadata)

    window =
      PaginationState.page_window(assigns.page, total_pages, sibling_count: assigns.sibling_count)

    show_page_size? = assigns.show_page_size? and length(assigns.page_size_options) > 1
    navigation_disabled? = assigns.disabled? or assigns.loading? or total_pages <= 1

    assigns =
      assigns
      |> assign(:total_pages, total_pages)
      |> assign(:summary, summary)
      |> assign(:window, window)
      |> assign(:show_page_size?, show_page_size?)
      |> assign(:navigation_disabled?, navigation_disabled?)

    ~H"""
    <nav
      id={@id}
      aria-label={@aria_label}
      aria-busy={to_string(@loading?)}
      data-pagination
      class={@class}
    >
      <div id={"#{@id}-toolbar"} class="flex flex-wrap items-center gap-x-2 gap-y-1.5">
        <p
          id={"#{@id}-summary"}
          class="shrink-0 whitespace-nowrap text-xs text-slate-600"
          aria-live="polite"
        >
          {@summary}
        </p>

        <form
          :if={@show_page_size?}
          id={"#{@id}-page-size-form"}
          phx-change={@event}
          phx-target={@target}
          class="inline-flex shrink-0 items-center gap-1"
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

        <div
          id={"#{@id}-navigation"}
          class="inline-flex flex-wrap items-center gap-1"
          role="group"
          aria-label="Page navigation"
        >
          <.pagination_button
            id={"#{@id}-first"}
            pagination_id={@id}
            label="«"
            aria_label="First page"
            page={1}
            event={@event}
            target={@target}
            disabled={@navigation_disabled? or @page == 1}
          />
          <.pagination_button
            id={"#{@id}-previous"}
            pagination_id={@id}
            label="‹"
            aria_label="Previous page"
            page={max(@page - 1, 1)}
            event={@event}
            target={@target}
            disabled={@navigation_disabled? or @page == 1}
          />
          <%= for {token, index} <- Enum.with_index(@window) do %>
            <%= if token == :ellipsis do %>
              <span
                id={"#{@id}-ellipsis-#{index}"}
                aria-hidden="true"
                class="px-0.5 text-xs text-slate-400"
              >…</span>
            <% else %>
              <.pagination_button
                id={"#{@id}-page-#{token}"}
                pagination_id={@id}
                label={Integer.to_string(token)}
                aria_label={"Page #{token}"}
                page={token}
                event={@event}
                target={@target}
                current={token == @page}
                disabled={@disabled? or @loading? or token == @page}
              />
            <% end %>
          <% end %>
          <.pagination_button
            id={"#{@id}-next"}
            pagination_id={@id}
            label="›"
            aria_label="Next page"
            page={min(@page + 1, max(@total_pages, 1))}
            event={@event}
            target={@target}
            disabled={@navigation_disabled? or @page >= @total_pages}
          />
          <.pagination_button
            id={"#{@id}-last"}
            pagination_id={@id}
            label="»"
            aria_label="Last page"
            page={max(@total_pages, 1)}
            event={@event}
            target={@target}
            disabled={@navigation_disabled? or @total_pages == 0 or @page >= @total_pages}
          />
        </div>

        <form
          :if={@show_jump?}
          id={"#{@id}-jump-form"}
          phx-submit={@event}
          phx-target={@target}
          class="inline-flex shrink-0 items-center gap-1"
        >
          <input type="hidden" name="pagination-id" value={@id} />
          <label for={"#{@id}-jump"} class="sr-only">Page</label>
          <input
            id={"#{@id}-jump"}
            name="page"
            type="number"
            min="1"
            inputmode="numeric"
            aria-label="Go to page"
            placeholder="Page"
            value={@page}
            class="w-16 rounded-lg border border-slate-200 px-2 py-1 text-xs text-slate-700"
            disabled={@disabled? or @loading?}
          />
          <button
            type="submit"
            disabled={@disabled? or @loading? or @total_pages == 0}
            class="rounded-lg border border-slate-200 px-2 py-1 text-xs font-semibold text-slate-700 hover:bg-slate-50 disabled:opacity-50"
          >Go</button>
        </form>
      </div>
    </nav>
    """
  end

  attr(:id, :string, required: true)
  attr(:pagination_id, :string, required: true)
  attr(:label, :string, required: true)
  attr(:aria_label, :string, required: true)
  attr(:page, :integer, required: true)
  attr(:event, :string, required: true)
  attr(:target, :any, default: nil)
  attr(:current, :boolean, default: false)
  attr(:disabled, :boolean, default: false)

  defp pagination_button(assigns) do
    ~H"""
    <button
      id={@id}
      type="button"
      phx-click={@event}
      phx-value-pagination-id={@pagination_id}
      phx-value-page={@page}
      phx-target={@target}
      aria-label={@aria_label}
      aria-current={if @current, do: "page", else: nil}
      disabled={@disabled}
      class={[
        "min-w-8 rounded-lg border px-2 py-1 text-xs font-semibold disabled:opacity-50",
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
