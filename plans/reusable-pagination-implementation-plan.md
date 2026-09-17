# Reusable Pagination Implementation Plan

## 1. Status, objective, and scope

- Plan: `PLAN-HLLM-REUSABLE-PAGINATION-001`.
- Date: 2026-09-16.
- Status: implementation complete through local P5 evidence; pushed/deployed
  release certification is pending.
- Direction: Petal Components 4.16.1 was evaluated and not adopted because its
  locked `websock_adapter ~> 0.5.7` requirement conflicts with the pinned
  stack's `websock_adapter 0.6.0`. A small in-house Phoenix component/state
  layer implements the narrower neutral contract without a UI framework or
  JavaScript dependency.
- First production consumer: the workspace History widget.
- Reuse validation: a test-only editable/searchable collection with an
  independently paginated batch-match panel.

Deliver numbered pagination with arbitrary page access, suitable for cards,
lists, tables, and editors. History must use the same reusable controls as
future consumers, rather than retaining a separate Load more experience.

This document records the requested implementation and its remaining release
handoff. It does not claim app-dev migration, browser layout certification, or
provider behavior. The implementation changes stay in this repository and its
existing Go/OpenAPI/Phoenix ownership boundaries.

In scope for the first implementation:

- Reusable Phoenix pagination controls and small state/URL helpers.
- Owner-scoped numbered History reads through the existing Go REST boundary.
- History integration preserving result-card actions and trace isolation.
- Deterministic reuse tests, PostgreSQL correctness checks, and bounded
  performance measurements.
- A documented extraction path for reuse during the app-dev migration.

Out of scope:

- Migrating app-dev, its Firebase data, or other projects in this change.
- Building a general-purpose UI framework, generic SQL/filter compiler,
  universal list controller, or a second application data-access layer.
- Adding Temporal to Harden LLM or starting workflows to read ordinary pages.
- Full data-table adoption, global UI restyling, virtualization, infinite
  scrolling, durable result snapshots, or speculative cursor-anchor caches.
- Changing execution, provider, artifact-retention, or authentication semantics.

## 2. Verified baseline and migration use cases

Source review used Harden LLM HEAD
`502c919e524f327391c2f07b890827b882e6515c` and app-dev HEAD
`9912430f367b2dbd0ed9c30105df6bcee2b8d0e6`. These identify inspected checkouts,
not deployed versions. Recheck affected files before implementation and
preserve unrelated worktree changes.

### 2.1 Harden LLM

- `api/openapi.yaml` currently defines `GET /api/v1/history` with `cursor` and
  `limit`, returning `items` and optional `nextCursor`.
- `internal/gateway/resources.go` requests one extra record to determine
  whether another cursor page exists.
- `internal/postgres/resources.go` uses keyset pagination ordered by
  `started_at DESC, run_id DESC`. The existing
  `llm_runs_owner_history_idx` indexes owner and these ordering keys.
- `frontend/lib/harden_llm_web/harden_api.ex` is the sole Phoenix-to-Go client.
  `frontend/lib/harden_llm/llm_diagnostics_wire.ex` strictly decodes History.
- `WorkspaceLive` fetches ten records per request and appends cursor pages.
  It owns request references, deletion rollback, and per-record trace state.
- `WorkspaceLive.handle_params/3` and `push_conversation_url/2` already own
  `trace_id` navigation. Pagination must compose with both paths.
- The existing [Result/History plan](reusable-result-widget-plan.md) describes
  cursor append behavior. Its presentation contract remains applicable;
  its pagination/refresh contract must be explicitly superseded during
  implementation, not silently treated as already changed.

### 2.2 App-dev feature inventory

Paths in this table are relative to `/home/kirill/p/app-dev`.

| Consumer | Existing source | Requirements for the reusable layer | Consumer-owned behavior |
| --- | --- | --- | --- |
| Dataset | `client/src/components/common/PaginatedList.tsx`, `client/src/components/tabs/DatasetTab.tsx` | Numbered navigation, counts, configurable fixed page size, arbitrary card content | Product search, authorization scope, collapse state, delete actions |
| Metadata editor | `client/src/components/metadata-editor/MetadataEditorFooter.tsx`, `client/src/components/common/MetadataEditor.tsx` | Direct page entry, page-size selector, page/item totals | Drafts, row identity, text search, go-to-line and search-match navigation |
| LLM traces | `client/src/components/common/LlmTraceWidget.tsx` | Numbered pages, direct page entry, page-size changes, compact footer | Expandable trace content, lazy details, request/response controls |
| Batch matches | `client/src/components/batch-edit/BatchEditPanel.tsx`, `client/src/components/batch-edit/BatchEditSession.tsx` | A second independent pager on the same screen; configurable size controls | Session scope, match counts, freeze/revert, workflow status |

These consumers reuse Ant Design controls but have different data hooks.
Translate that separation; do not translate React/Firebase internals or put
domain actions inside the shared pagination component.

The app-dev file `plans/dataset-pagination-backend-preparation-plan.md` is
explicitly planned, not implemented evidence. Its Firestore page-preparation
strategy and fixed Dataset size do not dictate PostgreSQL implementation or
the defaults for other consumers. Presence of a quick-jump control also does
not prove that an unseen page is fetched correctly.

## 3. Existing-library adoption gate and result

Evaluate the reviewed Petal Components `4.16.1` release first. Resolve and lock
the exact release actually tested; do not automatically follow a newer release
or change the pinned Phoenix/LiveView/Elixir toolchain to make it fit.

Verified upstream capabilities and constraints:

- [Petal pagination](https://petal-components.hexdocs.pm/PetalComponents.Pagination.html)
  renders a numbered window with boundary pages and accepts LiveView events
  and a target. The standalone component does not supply the complete desired
  quick-jump/page-size footer; compose those controls in our wrapper.
- [Petal table state](https://petal-components.hexdocs.pm/PetalComponents.DataTable.State.html)
  supports pagination, sorting, filtering, and URL conversion independently of
  a particular query implementation. Future table consumers can use it behind
  an adapter; do not recreate its general filter/state machinery now.
- The [package manifest](https://github.com/petalframework/petal_components/blob/main/mix.exs)
  declares MIT licensing and a `phoenix_ecto` dependency. Review the resolved
  dependency tree explicitly. A transitive dependency does not justify adding
  an Ecto Repo or frontend database access.

Adoption exit criteria:

1. Compile and render with the repository's pinned Phoenix `1.8.9`, LiveView
   `1.2.9`, Elixir `1.20.2`, and current Tailwind setup without unrelated upgrades.
2. Account for license notices, added runtime dependencies, assets, and any
   required hooks. Do not install Petal Pro or another application scaffold.
3. Import only the components needed for the first slice. Review stylesheet
   effects on existing buttons, forms, and theme variables; do not globally
   replace current components or register unused hooks.
4. Demonstrate numbered navigation, direct jumps, sizing, two independent
   instances, and disabled/busy states through public component/LiveView events.
5. Reuse upstream page-window/ellipsis rendering. Keep our additions limited
   to composition, integration, and requirements absent upstream.
6. Fetch dependencies as a setup step; preserve the offline, provider-free,
   browser-free `make test-fast` loop after setup. Library adoption must not
   introduce a network fetch or browser launch into that gate.

Use server-owned events for the first pager; custom browser JavaScript should
not be necessary. Browser-free checks cannot certify focus behavior, actual
keyboard delivery, or responsive layout. Record these as unchecked unless
the user separately requests the relevant browser canary.

The gate failed on the locked dependency graph: `petal_components 4.16.1`
requires `websock_adapter ~> 0.5.7`, while the pinned Phoenix/LiveView graph
resolves `websock_adapter 0.6.0`. No override or downgrade was made. The
bounded alternative is the neutral `PrlsUI.Pagination` and
`PrlsUI.PaginationState` pair, tested independently and documented in
`ADR-HLLM-023`; this is a component implementation, not a replacement UI
framework. Revisit Petal only as part of an explicit toolchain upgrade.

## 4. Ownership and reusable module boundary

Working names below describe the intended boundary, not existing modules.
Keep the first implementation under `frontend/` and application-independent
so it can later become one small versioned Phoenix package.

| Layer | Proposed responsibility | Must not own |
| --- | --- | --- |
| `PrlsUI.Pagination` | Stateless footer composition, labels, upstream numbered links, size/jump inputs, configurable density | Fetching, record rendering, authentication, drafts, batch actions |
| `PrlsUI.PaginationState` | Minimal requested/displayed page state, validated metadata, request generation, namespaced parameter merge | History fields, HTTP clients, SQL, a generic sorting/filtering language |
| Consumer LiveView/LiveComponent | Collection identity, query/filter state, task lifecycle, data adapter, item/draft/selection state | A copied pagination implementation |
| `HardenAPI` and History decoder | REST request parameters, strict envelopes, session/error handling | Library-specific structs on the public API |
| Go gateway and PostgreSQL store | Owner authorization, actual page/count queries, deterministic ordering | Rendering, per-widget UI state |

Initial proposed files:

- `frontend/lib/prls_ui/pagination.ex`
- `frontend/lib/prls_ui/pagination_state.ex`
- `frontend/test/prls_ui/pagination_test.exs`
- `frontend/test/prls_ui/pagination_state_test.exs`

The neutral modules must not import `HardenAPI`, `WorkspaceLive`, result/trace
types, or session storage. Use explicit component IDs, event targets and
metadata inputs. Do not add a required wrapper around the entire list: a host
can place the same pager beside cards, a table, or an editor.

One component instance owns one pagination namespace. A host may render top
and bottom controls for the same collection using distinct DOM IDs and shared
state. Two different collections must never share request generations or
parameter namespaces.

Go services remain behind OpenAPI. For later Temporal-backed applications,
ordinary page reads should query the service's authorized read model; workflow
execution and batch commands remain separate. This plan does not choose or
provision that future storage/orchestration infrastructure.

## 5. User-facing behavior

The first History implementation uses:

- First, Previous, a bounded numbered window with ellipses, Next, and Last.
- An explicitly submitted Go to page input, accepting any valid page without
  fetching all preceding pages into LiveView.
- Page-size options `10`, `25`, `50`, `100`; default `10`. Other consumers may
  configure a subset or a fixed size.
- Page/item totals and the displayed range, for example `21-30 of 463`.
- A refresh action, meaningful loading/error states, and an explicit retry.
- One displayed page of records; navigation replaces rather than appends.

Rules:

1. Page numbers are one-based. Empty results show `0 items`, no enabled
   navigation, and no misleading `Page 1 of 0` label. An empty result uses
   internal page `1`; the mathematical page count is `0`.
2. Page size and result-set changes reset to page `1`. Each consumer defines
   its result-set identity from scope, filter/search and ordering. Changing a
   batch session is a result-set change even if its page number stays the same.
3. Direct-entry validation rejects blank, fractional, negative, zero, and
   overflowing numbers. Server responses handle a valid page beyond the last
   page as specified in section 6; stale client totals must not prevent access
   to a newly available page.
4. Keep displayed metadata tied to displayed records until a replacement
   response succeeds. Show the requested destination separately while loading.
5. Failed navigation retains the displayed records and metadata; Retry targets
   the same failed request. Failure must not masquerade as an empty collection.
6. Namespace IDs, labels, forms and events. Use a named navigation landmark,
   `aria-current`, labeled inputs, real disabled controls, and a concise status
   announcement. Avoid nested forms when the consumer is an editor.
7. Compact layout can reduce the numbered window or wrap controls, but must
   retain a usable direct jump and access to page-size controls. Actual narrow
   layout and focus restoration require separately authorized browser checks.
8. Paging neither saves nor discards edits implicitly. The consumer retains
   drafts/selection by stable record ID or explicitly blocks navigation with a
   save/discard decision. Row indices are not identity.

Search and go-to-line are adjacent consumer capabilities, not implicit pager
features. A future go-to-line action may calculate a page using a known row
position; locating an arbitrary record requires a domain query. It must not
assume that a record's page is permanent.

## 6. Numbered REST contract and compatibility

### 6.1 Explicit opt-in on the existing route

Extend `GET /api/v1/history` with an optional `page` parameter. Continue to use
the existing `limit` parameter as the requested page size; do not add a second
size-parameter alias.

```http
GET /api/v1/history?page=7&limit=10
```

- Presence of `page` selects numbered mode; it must be a positive signed
  64-bit integer. Empty, repeated, fractional, negative and overflowing values
  return the existing `400 invalid_request` error shape.
- In numbered mode `limit` is an integer from `1` through `100`. Omission keeps
  the API's existing default of `20`; History explicitly requests `10`.
  Reject repeated or invalid `limit` values in this mode.
- `page` and `cursor` are mutually exclusive, including an explicitly present
  empty cursor. Do not silently prefer one.
- Without `page`, retain the existing cursor request/response behavior and
  defaults. Do not add metadata to legacy responses.
- Owner identity always comes from the authenticated principal, never a page
  parameter or component-supplied owner ID.

### 6.2 Numbered response

Keep the ordinary `state`/`result`/`error` envelope. In numbered mode, `result`
contains exactly `items` and `pagination`. The existing canonical `HistoryItem`
and `RunResult` schemas remain unchanged.

Example for an empty collection:

```json
{
  "state": {},
  "result": {
    "items": [],
    "pagination": {"page": 1, "pageSize": 10, "totalCount": 0}
  },
  "error": null
}
```

Metadata rules:

- `totalCount` is the exact owner-scoped count for this response's snapshot.
- `pageSize` is the applied `limit`; `page` is the effective page actually read.
- Clamp an above-range requested page to the last available page, or `1` for
  zero results. Count, clamping and page selection use one database snapshot.
- Derive total pages, range and navigation availability from these fields.
  Do not transmit redundant booleans or a second total-pages field.
- Numbered responses never include `nextCursor`. Validate integer bounds,
  effective page, cardinality and unknown fields; never coerce malformed
  metadata into defaults or treat missing metadata as a successful page.

Represent the two exact envelope schemas as disjoint OpenAPI alternatives.
Request-mode tests must also enforce the association between the query and
response shape, since the response union alone cannot express it.

Update together during implementation:

- `api/openapi.yaml`, including examples for both modes and empty results.
- `internal/gateway/httpapi/routes.go` query-parameter inventory and
  `internal/gateway/httpapi/resources.go` request handling.
- `internal/gateway/resources.go` and the PostgreSQL store method.
- `frontend/lib/harden_llm_web/harden_api.ex` and
  `frontend/lib/harden_llm/llm_diagnostics_wire.ex`.
- Go OpenAPI/HTTP tests and backend-validated frontend API fixtures.

Keep the existing client operation mapping to `listHistory`; add mode-aware
parameter/response validation inside that boundary. A numbered caller must
reject a legacy-only response, including from an older gateway. Do not fall
back silently to cursor traversal or client-side full-list loading.

## 7. PostgreSQL query and consistency policy

Use `COUNT(*)` plus ordered `LIMIT/OFFSET` initially. Execute count and page
selection in one short, read-only `REPEATABLE READ` transaction; release it at
the end of the request. Never hold a transaction between user interactions.
This gives both statements the same view of committed rows.
[PostgreSQL isolation documentation](https://www.postgresql.org/docs/current/transaction-iso.html#XACT-REPEATABLE-READ).

Implementation rules:

1. Use exactly the same owner/result-set predicate for count and rows. Do not
   call the aggregate Stats endpoint or maintain a separate count projection.
2. Retain `ORDER BY started_at DESC, run_id DESC` and the existing index.
3. Compute the effective page from the count before calculating an offset.
   Use checked integer arithmetic and test overflow inputs explicitly.
4. Fetch only the selected records and their existing payloads. Do not walk
   cursor pages or deserialize every preceding result in the application.
5. Preserve context cancellation, connection cleanup and safe error handling.
6. No schema/data migration is expected. Add an index only if a measured query
   plan demonstrates that the existing index is insufficient.

Large offsets still require database work; deterministic ordering does not
make them constant-time. Measure rather than promising inexpensive deep jumps.
[PostgreSQL LIMIT/OFFSET documentation](https://www.postgresql.org/docs/current/queries-limit.html).

Across separate requests the collection is live: insertions, deletions and
late-completing runs can change page membership. This plan guarantees a
self-consistent response, not a session-long snapshot, permanent page-to-record
mapping, or duplicate-free traversal of a concurrently changing dataset.
Durable record links continue to use IDs, not page positions.

## 8. History integration and lifecycle

### 8.1 URL and async state

- Use `history_page` and `history_page_size` in the workspace URL. Preserve
  `trace_id` and other allowed host parameters when changing pages, selecting
  a result, or completing a run. Update both parameter-reading and
  `push_conversation_url/2` paths; a new result must not erase paging state.
- Treat URL parameters as requested state. Keep displayed criteria separately
  until data arrives. When the server clamps a page, replace the URL with its
  effective value without a redundant fetch or a back/forward loop.
- Omitted parameters use page `1`, size `10`; malformed UI URL values normalize
  to safe defaults. The REST API remains strict as defined in section 6.
- An explicit History page link opens History. Without such a link, preserve
  the existing fold/lazy-load behavior.
- Coalesce identical in-flight requests. A newer navigation/query intent gets
  a new generation, supersedes the old one, and cancels superseded work where
  supported. Completion always checks both generation and collection identity.
- Clear, logout, scope changes and component removal invalidate pending reads.
  Task cancellation alone is not the stale-response guard.

### 8.2 Runs, refresh and deletion

- A completed run can refresh History automatically on page `1` when no
  navigation/deletion is pending. On an older displayed or requested page,
  keep the user's view and mark `History changed - Refresh` instead.
- Manual refresh fetches the current requested page and size. It never means
  an implicit return to page `1`; a separate First action does that.
- During an owned delete/clear operation, invalidate stale reads and block
  conflicting pager navigation. Preserve the existing optimistic-delete and
  rollback guarantees. On success, re-read the same page; server clamping
  handles deletion of the last item on the last page.
- Clear success resets to empty page `1`. Clear/delete failure retains or
  restores the correct displayed records; late reads cannot resurrect records
  or place rollback items into another page's state.
- Refresh failure must not change a successfully persisted mutation into a
  reported mutation failure or prompt a duplicate mutation retry.

### 8.3 Result-card isolation

Keep `LlmResultComponents` and existing restore, delete, rerun, copy, artifact
and trace behavior. Paging must not execute an LLM call, save the prompt draft,
change a selected profile, or replace the current Result panel.

Retain disclosure/trace state for IDs still present during a same-page refresh.
Prune state for departed page records, ignore their late trace completions,
and keep only the current page's cards/trace payloads. Returning to a previously
left page may reset its transient disclosures; persistent per-page trace caches
are not part of this change. Consumer-owned editor drafts in the reuse harness
are a different concern and remain keyed by record ID across page changes.

## 9. Test-first verification matrix

Follow [the complete testing guidelines](../docs/liveview-go-testing-guidelines.md)
and ADR-HLLM-015. No browser, DOM emulator, paid provider, or public deployment
is needed for the default implementation loop.

The new identifiers below were registered in the canonical catalogs during P0
and now have executable owners. Backend cases reference
`SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`; frontend cases reference
`SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001`. The plan remains a design record; the
listed test files and commands are the executable coverage.

| Proposed ID | Boundary/tier | Required oracle |
| --- | --- | --- |
| `TEST-229` | Parsing and paging arithmetic, T0/T1 | Positive page/size validation; duplicate and mixed modes; overflow; zero/one/exact-multiple counts; clamp behavior; old cursor requests unchanged |
| `TEST-230` | PostgreSQL and authorized reads, T3 | Correct unseen middle/last page; identical timestamp tie-breaks; exact count and owner isolation; concurrent insert/delete leaves count and rows in the same response snapshot; cancellation releases resources |
| `TEST-231` | OpenAPI/HTTP/fixtures, T0/T1 | Both exact envelopes, query-mode association, error responses, one canonical HistoryItem/RunResult shape; malformed metadata rejected; published examples validate |
| `TEST-232` | Bounded PostgreSQL measurement, T3 | Recorded query plans, rows examined, response size and latency for first/middle/last pages and counts at stated owner cardinalities; no preceding-page API walk |
| `WEB-TEST-077` | Component rendering/events, T1 | Boundary/numbered navigation, ellipses, direct jump, sizes, labels, empty and single-page states, real disabled bindings and independent DOM IDs |
| `WEB-TEST-078` | State and routing, T0/T1 | Requested versus displayed metadata; query/size reset; preserved trace/unrelated parameters; normalized clamp URL; duplicate coalescing; latest response wins; retry same target |
| `WEB-TEST-079` | History lifecycle, T1 | Page replacement and bounded records/state; run refresh policy; delete/clear success/failure; stale history/trace completion; current Result and editor draft unchanged; retained record actions |
| `WEB-TEST-080` | HardenAPI and strict wire boundary, T1 | Real backend-validated numbered fixtures; mode-specific decoding; auth/error handling; no fallback to cursor-only or fabricated counts |
| `WEB-TEST-081` | Reuse harness, T0/T1 | Editable searchable list plus independent batch pager; filter/size/session reset affects only its owner; draft/selection survives navigation by ID; main-page changes do not submit batch commands |

Update `WEB-TEST-069` and its specification to replace the intentional
append-list UX with page replacement while retaining its stale-response,
duplicate-request, retry and clear-all protections. Preserve the existing
Result/trace assertions, including `WEB-TEST-066/067`, at their current owners.
Legacy cursor API tests remain because that public behavior remains supported.

The reuse harness belongs under `frontend/test/support/` and is mounted only
by tests. Give it synthetic records, a local list adapter and two pagers.
Exercise actual production components/helpers and host events, not a second
test-only pagination implementation. It proves generic composition and state
isolation; it does not certify app-dev feature parity, Firebase behavior,
Temporal execution, or a second production integration.

Add cheap regressions before production changes. For database-specific facts,
retain real PostgreSQL tests; do not replace snapshot semantics with a fake.
Use process-owned fixtures/stubs and parallel-safe tests without new global
serialization or retrying ambiguous failures.

## 10. Execution phases and checkpoints

### P0 - Register the contract and adoption criteria

Depends on approval to implement.

- [x] Recheck worktree, source inventory, candidate version and unused test IDs.
- [x] Add and accept the ADR for the dependency gate, compatibility mode,
  consistency policy and extraction boundary.
- [x] Register the test IDs and requirements in the canonical frontend/backend
  catalogs and traceability files, preserving unrelated status records.
- [x] Add deterministic contract/state regressions and fixture shapes; amend
  the old Result/History pagination description explicitly.

Exit: the intended UX, exact API forms, consistency guarantees and test owners
were documented before implementation.

### P1 - Prove Petal and implement reusable controls

Depends on P0. May run independently of P2 after the contract is fixed.

- [x] Run the adoption gate in section 3 with task-scoped dependency changes;
  record the Petal lock conflict and retain the original lock.
- [x] Implement the neutral component and minimal state/parameter helpers.
- [x] Add composed jump/size/summary controls and scoped styling.
- [x] Verify controls through component and LiveView events, including two
  instances and a host form that must not be submitted by navigation.

Exit: reusable controls work independently of History, Go, sessions and domain
models; upstream rendering is reused and no custom JS framework is added.

### P2 - Add real numbered History reads

Depends on P0. May run in parallel with P1 on non-overlapping files.

- [x] Implement request validation, numbered response metadata and the
  count/clamp/query transaction; retain the existing cursor path.
- [x] Update OpenAPI, route metadata, request-mode enforcement and examples.
- [x] Update HardenAPI/decoder and shared fixtures in lockstep with the API.
- [x] Run deterministic contract tests and the focused real-PostgreSQL cases.

Exit: requesting an unseen page returns the correct owned records and count
with one count and one page query, without a cursor traversal or API loop.
Existing cursor clients still receive their original response shape.

### P3 - Integrate workspace History

Depends on P1 and P2.

- [x] Replace append state and Load more with the shared controls and one page.
- [x] Integrate namespaced navigation with both trace URL paths.
- [x] Implement requested/displayed state, retry, cancellation/generation
  guards and refresh policy.
- [x] Preserve mutation rollback and all Result/trace actions; prune departed
  record state without touching current Result or prompt/profile drafts.
- [x] Run the History, routing, wire and result-component regressions.

Exit: real numbered History navigation and direct deep jumps use the shared
component, remain bounded, and preserve unrelated workspace behavior.

### P4 - Validate reuse before stabilizing the shared API

Depends on P1; final acceptance also requires P3.

- [x] Build the test-only editable/searchable collection and batch-match host.
- [x] Demonstrate local-list and API-shaped consumers of the same UI contract.
- [x] Exercise independent scope, page-size/filter reset, drafts, selection and
  batch isolation paths.
- [x] Remove History assumptions from neutral modules and document a minimal
  consumer example without copying the whole History implementation.

Exit: the shared interface handles the app-dev-derived use cases without
coupling the pager to edit/batch commands or requiring a table representation.
Actual cross-project migration remains future work, not a claimed result.

### P5 - Measure, verify and hand off

Depends on P2-P4.

- [x] Run `make test-fast` through development and on the implementation
  checkpoint.
- [x] Run the canonical `make test-integration` PostgreSQL gate for the new
  storage/concurrency behavior.
- [x] Measure synthetic owner datasets of 1,000, 10,000 and 100,000 records,
  with first/middle/last pages and sizes 10/25/50/100. Record hardware, payload
  sizes, warm/cold conditions, count/page timings, query plans and total request
  latency. These are benchmark fixtures, not asserted production volumes.
- [x] State the measured envelope and its limitation; do not invent a latency
  budget from one host. Revisit the backend strategy if a consumer needs a
  measured stronger deep-offset guarantee.
- [x] Run formatting and whitespace checks; update canonical specifications,
  requirements, ADR and implementation status with actual evidence.
- [ ] Push the verified application checkpoint and complete the authorized
  browser-free release/deployment certification with exact SHA/image identity.
- [x] Record what is locally verified, what remains unverified, and the
  cross-project extraction follow-up. Browser layout and app-dev migration are
  intentionally separate.

Implementation and release commands used for this plan include:

```bash
export PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH
make test-fast
make test-integration
git diff HEAD --check
```

Use focused Go and `mix test` commands for the individual cases before the
aggregate gates. Skip Node additions unless actual client JavaScript changes.
Use `make test-release` for a separately authorized release/cross-system
certification scope, not as a substitute for focused regression checks.
Never run a browser-containing gate or provider smoke automatically.

## 11. Rollout, rollback, and cross-project extraction

When deployment is separately authorized, release the compatible Go contract
before or together with the new frontend. Older frontends continue to use the
unchanged cursor mode. A new frontend must not be paired with an old gateway
that lacks numbered metadata.

Rollback order: restore the old frontend first, then revert the additive API
implementation if necessary. No data rewrite is planned. Keep branch, source
SHA, component image identities, URL and browser-free checks in deployment
evidence; do not report an unrun deployment or browser layout as verified.

For subsequent projects:

1. Keep one implementation home for the neutral Phoenix modules. When a second
   application is actually approved for adoption, move them and their tests
   into a small standalone versioned package, rather than copying them into
   multiple repositories or building a multi-service monorepo.
2. Keep HardenAPI and each future service's API adapter in its owning app.
   The package depends on Phoenix/Petal and UI conventions, not a Go service,
   Temporal SDK, Ecto Repo, database credentials or provider implementation.
3. Have consumers pin released package versions. Prove both consumers against
   the same package before removing the original local implementation.
4. Revalidate app-dev's actual search, editing, go-to-line, batch selection and
   workflow semantics during its migration. Do not infer semantic parity from
   the pagination harness or replace domain search with Petal's filter grammar.
5. Assess richer Petal table/filter components when a concrete migrated screen
   needs them. Do not wrap every upstream component preemptively.

## 12. Completion criteria

The first implementation is complete when:

- The adoption gate is recorded: Petal is not adopted because of the locked
  transport dependency conflict, and the bounded neutral alternative passes.
- History supports numbered, first/last, direct-jump and size navigation
  against real owner-scoped backend pages, not a cache of visited cursors.
- Only the current History page and its transient trace state are retained.
- Strict new/old REST contracts, count/row consistency, authorization, async
  races and mutation lifecycle are covered at their actual owning boundaries.
- The independent editable-list/batch-pager harness passes using the same
  production component and helpers.
- Required deterministic and PostgreSQL checks have current results; measured
  scale limits and any remaining release/layout gaps are explicit.
- Canonical specifications, ADR and status match delivered behavior. Planned
  extraction and app-dev migration remain future work; browser layout remains
  an explicit opt-in boundary. Deployment is complete only after the exact
  pushed SHA, image identities, health/authenticated read evidence and rollback
  record are added to the release documents.
