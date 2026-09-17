# ADR-HLLM-023: Reusable Numbered Pagination

- Status: Accepted and implemented.
- Date: 2026-09-16.
- Plan: `plans/reusable-pagination-implementation-plan.md`.
- Verification: `TEST-229` through `TEST-232` and `WEB-TEST-077` through `WEB-TEST-081`.
- Related: ADR-HLLM-008, ADR-HLLM-012, ADR-HLLM-015, ADR-HLLM-020, and ADR-HLLM-021.

## 1. Context

Workspace History had a cursor/Load more interaction, while the source feature
inventory also requires direct page access, page-size changes, quick jumps and
reuse by editable/searchable and batch-oriented lists. A reusable solution must
not own domain records, persistence, HTTP, editor drafts, batch commands or
provider execution. It must also keep the existing cursor API clients working.

Petal Components 4.16.1 was evaluated against the pinned Phoenix stack. Its
locked dependency range requires `websock_adapter ~> 0.5.7`, while this checkout
resolves `websock_adapter 0.6.0` for the pinned Phoenix/LiveView versions. The
dependency solver rejected the graph. Downgrading or overriding that transport
dependency would change a security-sensitive LiveView boundary without a
planned stack migration, so Petal was not adopted.

## 2. Decision

Use a small in-house, domain-neutral Phoenix component and state-helper pair:

- `PrlsUI.PaginationState` normalizes positive page/page-size values and derives
  page totals, display ranges, summaries and bounded numbered windows. It knows
  nothing about routes, HTTP, LiveView tasks or collections.
- `PrlsUI.Pagination` renders labeled summary, size, jump and navigation controls
  using Phoenix's component API. It emits the configured host event and scoped
  identity; it does not fetch, mutate, save or select records.
- Each host owns requested versus displayed metadata, result-set identity,
  URL parameters, async generations, retries, drafts and selections. Page-size
  changes reset that host's result set to page one; page navigation replaces the
  displayed page.
- The existing `GET /api/v1/history` route gains an additive `page` mode using
  the existing `limit` size. Page and cursor are mutually exclusive. The
  cursor response remains unchanged, and numbered callers reject legacy-only
  responses rather than falling back to cursor traversal.
- PostgreSQL count and ordered rows run in one short read-only
  `REPEATABLE READ` transaction with `ORDER BY started_at DESC, run_id DESC`.
  Above-range pages clamp to the last page, or page one for an empty result.
- A test-only editable/searchable and batch harness proves two independent
  consumers use the same production control/state layer. Cross-project
  app-dev migration remains a later project and is not claimed by this ADR.

No JavaScript framework, DOM emulator, Ecto schema, second persistence path,
Temporal workflow, or provider call is added.

## 3. Consequences

Direct deep History navigation no longer requires fetching preceding pages, and
other consumers can use the same controls for lists that need editing, search,
selection or batch operations. The host must explicitly decide what happens to
drafts and selection when a result-set identity changes; pagination will not
silently submit or discard them. Numbered pages are self-consistent per read,
not a session-long snapshot, so page membership can change between requests.

`LIMIT/OFFSET` count and deep-page work must be measured. The first integration
measurement records real 1k/10k/100k owner datasets, page positions, sizes,
plans, buffers, response bytes and first/warm timings. It does not establish a
universal latency SLO or pretend that pooled execution provided a cold-cache
measurement.

## 4. Verification and rollback

`TEST-229`/`TEST-231` cover request, OpenAPI and strict wire compatibility;
`TEST-230` covers real PostgreSQL ownership, ordering, snapshot and cancellation
behavior; `TEST-232` records bounded measurements. `WEB-TEST-077` covers the
neutral component, `WEB-TEST-078` requested/displayed routing, `WEB-TEST-079`
History lifecycle, `WEB-TEST-080` the HardenAPI boundary and `WEB-TEST-081`
second-consumer composition. Browser layout remains an explicitly separate
boundary and is not certified by LiveViewTest.

Rollback is code/configuration rollback to the previous cursor-only frontend
and gateway checkpoint while preserving the database: no schema migration is
introduced. A numbered caller can be disabled by removing its `page` query
usage and returning to the unchanged cursor path. If a future consumer needs a
different storage strategy, extract the neutral Phoenix layer unchanged and
replace only its host adapter; do not move domain or workflow behavior into
the shared component.
