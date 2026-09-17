# ADR-HLLM-023: Reusable Numbered Pagination

- Status: Accepted and implemented; corrective hardening from the 2026-09-17
  review is implemented in this checkpoint.
- Date: 2026-09-16.
- Plan: `plans/reusable-pagination-implementation-plan.md`.
- Follow-up: [pagination hardening and compact UX plan](../../plans/pagination-hardening-and-compact-ux-plan.md).
- Usage: [reusable pagination guide](../reusable-pagination.md).
- Verification: `TEST-229` through `TEST-232` and `WEB-TEST-077` through `WEB-TEST-082`.
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
- The default presentation keeps First, Previous, ordered numbered pages,
  Next, Last, jump, and size controls in one wrapping toolbar when space
  permits. Boundary glyphs are compact but retain accessible names and native
  button semantics.
- `PrlsUI.PaginationState.transition/3` is the shared pure event contract:
  page navigation retains size, an actual size change resets to page one, and
  reset retains size. Effective metadata is validated before rendering.
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
History lifecycle, `WEB-TEST-080` the HardenAPI boundary, `WEB-TEST-081`
second-consumer composition, and `WEB-TEST-082` the fresh compiled asset.
Browser layout remains an explicitly separate boundary and is not certified by
LiveViewTest.

Rollback is code/configuration rollback to the previous cursor-only frontend
and gateway checkpoint while preserving the database: no schema migration is
introduced. A numbered caller can be disabled by removing its `page` query
usage and returning to the unchanged cursor path. If a future consumer needs a
different storage strategy, extract the neutral Phoenix layer unchanged and
replace only its host adapter; do not move domain or workflow behavior into
the shared component.

## 5. Corrective hardening checkpoint

The 2026-09-17 review found defects in the first deployed implementation:
ordered token rendering, forced two-row layout, missing Tailwind source
coverage, silent size reset in the reuse harness, impossible metadata,
incomplete numbered pages, explicit folded deep links, and manual refresh of
older pages. This checkpoint adds deterministic regressions and fixes those
defects without changing the REST shape or adopting a UI dependency.

History now distinguishes passive run-completion refresh policy from explicit
Refresh/Retry requests, opens only when the route explicitly requests History,
and cancels superseded reads while retaining reference guards. The remote wire
decoder rejects exact-cardinality violations; local consumers use the same
transition helper and real rendered editor forms. The asset gate verifies a
fresh stylesheet, while browser layout and native-event behavior remain
uncertified unless a browser run is explicitly authorized.

The original production receipt remains historical evidence for its recorded
SHA and coverage. It is not retroactively treated as proof of this corrective
checkpoint; the current release receipt must identify its own source SHA,
checks, deployment identity, and limitations.
