# Reusable Pagination

## 1. Purpose and boundary

The frontend provides a small in-house numbered pagination layer for lists,
cards, tables, and editors. It is intentionally a rendering and transition
contract, not a list framework. `PrlsUI.Pagination` owns markup, accessible
labels, scoped events, and compact presentation. `PrlsUI.PaginationState` owns
pure criteria transitions and metadata arithmetic.

The consuming LiveView owns records, filters, result-set identity, URL policy,
HTTP requests, async lifecycle, retries, drafts, selections, and mutations.
The component never fetches or changes collection data.

## 2. Component API

Render the component with successfully displayed metadata:

```heex
<.pagination
  id="records-pagination"
  page={@records_page}
  page_size={@records_page_size}
  total_count={@records_total_count}
  page_size_options={[10, 25, 50]}
  event="paginate-records"
  target={@myself}
  loading?={@records_loading?}
  disabled?={@records_disabled?}
/>
```

The component retains the complete control set: First, Previous, an ordered
bounded numbered window with ellipses, Next, Last, a visible page-jump input
and Go, plus the optional page-size selector. First/Last use compact glyphs
with accessible names; no navigation action is removed. All generated IDs are
namespaced below the supplied `id`.

The public presentation options are:

- `show_page_size?` hides the selector when a host does not want it. A
  selector is also hidden automatically when only one size is configured.
- `show_jump?` hides the explicit jump form for a small or constrained host.
- `sibling_count` controls how many pages appear around the current page;
  zero is valid.

`event`, `target`, `loading?`, `disabled?`, `aria_label`, `class`, and
`page_size_options` remain host-configurable. The summary, selector,
navigation, and jump form share one wrapping toolbar. The component renders
its size and jump forms beside a host editor form, never nested inside it.

Direct component assigns with impossible effective metadata raise an
`ArgumentError`; remote responses must be rejected at their wire boundary
before they reach the component.

## 3. Pure host transitions

Use `PrlsUI.PaginationState.transition/3` for event-to-criteria decisions:

```elixir
current = %{page: socket.assigns.records_page, page_size: socket.assigns.records_page_size}

intent =
  if Map.has_key?(params, "page-size"),
    do: {:page_size, params["page-size"]},
    else: {:page, params["page"]}

case PaginationState.transition(current, intent, page_size_options: [10, 25, 50]) do
  {:ok, criteria} -> load_or_patch(criteria)
  :unchanged -> :no_read
  {:error, reason} -> reject_input(reason)
end
```

The helper guarantees that page navigation preserves the selected size, a
real size change resets to page one, selecting the same size is a no-op, and
`:reset` returns page one while retaining the size. It does not know an event
name, DOM ID, route, collection, or HTTP client.

Hosts should keep requested and displayed criteria separate while a read is in
flight. Do not clamp a requested remote page using stale totals. Accept the
authoritative effective metadata returned by the service, update the URL if
the service clamps it, and keep generation/reference guards even when the
host can cancel a superseded task.

## 4. Local-list consumer

A local collection can reuse the same component and helper without an HTTP
adapter:

```elixir
def handle_event("paginate-records", params, socket) do
  current = %{page: socket.assigns.page, page_size: socket.assigns.page_size}
  intent = if Map.has_key?(params, "page-size"), do: {:page_size, params["page-size"]}, else: {:page, params["page"]}

  case PaginationState.transition(current, intent, page_size_options: [10, 25, 50]) do
    {:ok, %{page: page, page_size: page_size}} ->
      {:noreply, assign(socket, page: page, page_size: page_size)}

    :unchanged ->
      {:noreply, socket}

    {:error, _reason} ->
      {:noreply, put_flash(socket, :error, "Enter a valid page or page size.")}
  end
end
```

The host filters first, resets its own page on a filter identity change, and
slices the freshly filtered records. Drafts and selections should be keyed by
stable record ID, not by visible array index, so leaving and returning to a
page does not lose them.

## 5. Remote-list consumer

A remote host follows the same event contract but owns the request boundary:

1. Normalize initial URL criteria with `normalize_params/2` and retain any
   unrelated route parameters in the host.
2. Translate a page, size, reset, retry, or refresh action into a transition.
3. Patch the host URL for a new destination, or start the read directly for a
   retry/refresh of the current requested criteria.
4. Keep displayed rows and metadata stable until the matching response arrives.
5. Decode and validate the response, replace the displayed page atomically,
   and accept the service's effective page and size.
6. Ignore stale completions after cancellation, deletion, clear, scope change,
   or a newer request.

History is the production remote example. Its numbered response must contain
exactly `items` and `pagination`, and the item count must be zero for an empty
result or `min(page_size, total_count - (page - 1) * page_size)` otherwise.
That exact row-cardinality rule belongs to the History wire decoder, not to
the reusable component. Legacy cursor responses remain a separate mode.

## 6. Multiple instances and event ownership

Render each instance with a unique ID and route its event by
`pagination-id`:

```heex
<.pagination id="records-pagination" page={@records_page} page_size={@records_page_size} total_count={@records_total} event="paginate" />
<.pagination id="matches-pagination" page={@matches_page} page_size={@matches_page_size} total_count={@matches_total} event="paginate" />
```

The host must branch on the identifier before applying state. Sharing an event
name is safe; sharing page state is not. A pager event must not submit an
editor form, invoke a batch command, or mutate another collection's criteria.
Use distinct `target` values for LiveComponents when the host needs process
isolation.

## 7. Styling and verification

Tailwind scans `frontend/lib/prls_ui` through
`frontend/assets/css/app.css`. The release asset gate runs a fresh
`mix assets.deploy` and then `WEB-TEST-082`, which fails if the generated CSS
artifact is missing pagination width, spacing, jump-input, active, or focus
rules. Component and LiveView tests prove structure, event payloads, metadata,
forms, and lifecycle behavior; they do not certify one-row geometry, native
keyboard delivery, focus movement, or responsive layout. Those remain an
explicit real-browser boundary.

The neutral modules do not import `HardenAPI`, `WorkspaceLive`, session or
domain modules, `Req`, or task/process ownership. Extract them only when a
second real application adopts the same contract; do not copy diverging
consumer implementations into another repository.
