# Pagination Hardening and Compact UX Plan

## 1. Status, objective, and scope

- Plan: `PLAN-HLLM-REUSABLE-PAGINATION-002`.
- Date: 2026-09-17.
- Status: implementation complete; final deterministic and release evidence is
  recorded as each gate completes.
- Baseline inspected: `91b2b7b7a317c9bb93f9a9d1fe2eeed831de7f1d` on `main`.
- Predecessor: [first pagination implementation plan](reusable-pagination-implementation-plan.md).
- Architecture: [ADR-HLLM-023](../docs/adr/ADR-HLLM-023-reusable-numbered-pagination.md).
- Governing verification policy: [LiveView and Go testing guidelines](../docs/liveview-go-testing-guidelines.md).

Correct the reviewed defects, make the default footer compact and single-row
when space permits, and make reuse reliable without turning pagination into a
generic list framework. Keep the small in-house Phoenix component and pure
helpers. Do not reopen dependency adoption or upgrade the pinned stack.

This is a corrective follow-up, not a new backend pagination implementation.
The owner-scoped Go count/page query, numbered REST mode, legacy cursor mode,
and current database schema remain in place. No migration, provider request,
Temporal integration, app-dev edits, or package publication is planned.

Implementation, push/deployment, and production promotion are separate release
gates below. A real browser still requires a specific browser-testing request,
independently of implementation or deployment authorization.

## 2. Review findings and evidence boundaries

| Finding | Current owner | Required correction |
| --- | --- | --- |
| Manual Refresh on an older page only marks History changed | `WorkspaceLive.maybe_refresh_history/1` | Separate explicit reads from passive run-completion notification |
| Explicit page URLs leave previously folded History closed | `WorkspaceLive` hydration and route handling | Explicit History navigation opens and loads the requested page |
| All ellipses render before all numbered buttons | `PrlsUI.Pagination` | Render the ordered page-window tokens in one pass |
| Footer always has two block rows | `PrlsUI.Pagination` | One wrapping toolbar, with no forced second navigation row |
| Tailwind does not scan `lib/prls_ui` | `frontend/assets/css/app.css` | Register the component source and verify freshly compiled CSS |
| Reuse harness resets size on the next navigation | `PaginationReuseHarness` | Shared transitions preserve omitted values and reset only on actual changes |
| Shared metadata validation accepts impossible ranges | `PrlsUI.PaginationState` | Validate effective page against count, including the empty case |
| Numbered decoder accepts incomplete pages inconsistent with exact totals | `LlmDiagnosticsWire` and fixtures | Enforce exact expected item cardinality for this REST contract |
| Buttons override native semantics with `role="listitem"` | `PrlsUI.Pagination` | Preserve button semantics; use real list wrappers only if needed |
| Reuse proof bypasses rendered editor bindings and labels local data as API data | Test-only harness and tests | Exercise valid forms and describe the actual boundary being tested |

The preceding review ran 15 existing focused tests successfully and reproduced
three failures with additional in-memory LiveView checks: older-page refresh,
folded deep links, and size retention in the harness. Those probes are now
represented by committed deterministic regressions; the original release
receipts remain historical evidence rather than proof of this checkpoint.

The inspected generated local CSS omitted component-only utilities; that is
not a fresh production-asset or visual-layout certification. Existing release
receipts remain historical evidence for their exact SHAs, not proof that these
newly discovered cases passed.

## 3. Target ownership and public contracts

### 3.1 Keep three distinct responsibilities

| Layer | Owns | Does not own |
| --- | --- | --- |
| `PrlsUI.Pagination` | Rendering, labels, scoped forms/events, current/disabled states, compact presentation | Records, HTTP, routes, async tasks, drafts, selection, persistence |
| `PrlsUI.PaginationState` | Pure input transitions, metadata validation, ranges, totals, ordered page windows | LiveView sockets, History fields, SQL, authentication, workflows |
| Consumer host | Request/display state, filters and scope, URL policy, fetching, task lifecycle, drafts and mutations | Its own copied pager or duplicate page-size transition rules |

The REST decoder validates the service contract independently of the UI
component. Do not make `LlmDiagnosticsWire` depend on `PrlsUI` or expose Phoenix
types through OpenAPI. `HardenAPI` remains the only application HTTP boundary.

### 3.2 Pure transition helper

Extend the existing `PaginationState` module; do not add a process or universal
controller. The proposed API is `transition(current, intent, options)`, where
`current` contains requested `page` and `page_size`, and intent is one of:

```elixir
{:page, value}
{:page_size, value}
:reset
```

Return `{:ok, criteria}`, `:unchanged`, or `{:error, reason}`. Hosts translate
the existing scoped Phoenix form/button payloads into these intents; no event
name, DOM ID, URL namespace, or socket enters the pure helper.

Required rules:

1. Page navigation preserves the current page size.
2. An actual size change resets the page to `1`; selecting the same size is a
   no-op, not an accidental return to the first page.
3. Host-requested result-set reset returns page `1` and retains the selected
   size. Filter/session identity itself remains host-owned.
4. Invalid explicit input returns an error without changing state. Tolerant
   URL normalization remains a separate entrypoint with documented defaults.
5. Size choices must be valid configured positive integers. History continues
   enforcing its signed-64-bit page limit and existing API size constraints.
6. Unchanged criteria do not trigger new reads, except when the host explicitly
   retries a failed request or refreshes an existing page.
7. Do not clamp a requested remote page using stale displayed totals. The
   service applies its authoritative count/clamp policy; a local collection
   host may clamp against its freshly filtered records before slicing.

### 3.3 Effective metadata is stricter than requested criteria

- `page` and `page_size` are positive integers; `total_count` is nonnegative.
- Empty results require effective page `1`, total pages `0`, and range `0–0`.
- Nonempty effective pages must not exceed `ceil(total_count / page_size)`.
- Invalid effective metadata must not render inverted or fabricated ranges.
  Treat invalid direct component assigns as a programmer error. Reject bad
  remote responses at the wire boundary and retain the last valid display.
- For the exact-count History response, require the following item count:

```text
total_count == 0: 0
otherwise: min(page_size, total_count - (page - 1) * page_size)
```

Do not put History's exact row-cardinality rule in the generic visual
component. Do not silently fill missing records, invent totals, weaken the
decoder, or fall back to cursor traversal. Fix inconsistent test fixtures
while preserving their original lifecycle/ordering assertions.

## 4. Compact UX contract

### 4.1 Default presentation

```text
21–30 of 463   [10/page ▾]   « ‹ 1 2 [3] 4 … 47 › »   Page [3] [Go]
```

This is the intended grouping, not a screenshot or measured layout result.

1. Put summary, size selector, navigation, and jump form in one wrapping
   toolbar. Do not use a separate block or top margin that forces navigation
   below it.
2. Keep the complete existing control set: First, Previous, the bounded
   numbered window, Next, Last, and Go. Boundary controls may use compact
   icon-only labels with explicit accessible names, but no navigation action or
   button category is removed.
3. Render the numbered window and ellipses in exactly the helper's order.
4. Retain a visible page-jump input and Go button. Submit explicitly, not on
   each keystroke. Initialize/reset its displayed value from the successfully
   displayed page, not a pending destination. Do not add a stale-total HTML
   `max` that prevents valid jumps into newly added pages.
5. Keep the current page visually distinct without treating it as a faded
   unavailable destination. Preserve `aria-current="page"`.
6. Preserve usable control heights, focus styling, and readable text. Compact
   means less duplication and spacing, not tiny targets or clipped controls.

### 4.2 Responsive behavior and composition

- Use available component width, not only viewport width. Prefer intrinsic
  flex wrapping and scoped CSS; no resize hook or DOM emulator is needed.
- Keep one row when the complete control set fits. On narrower containers,
  wrap logical groups in reading order without horizontal page overflow.
- Keep summary/controls usable for large totals and long page numbers. Permit
  navigation to wrap internally if necessary; do not truncate editable values
  or use a hard minimum width that exceeds its container.
- The structural contract is one toolbar, not an unconditional one-line
  promise on every viewport, at every zoom level, and for every count.
- Expose only the currently useful presentation options: `show_page_size?`,
  `show_jump?`, and `sibling_count`. Keep first/last boundaries. Retain existing
  IDs, event/target configuration, size options, loading/disabled inputs and
  outer class. Do not introduce a theme engine or speculative layout modes.
- Hide a size selector when only one size is configured. For zero results,
  show `0 items`; for zero/one-page results disable unavailable navigation and
  jump actions. Multiple size choices may remain useful on a single page.
- Keep size/jump forms scoped and adjacent to, never nested inside, a host
  editor form. Every non-submit pagination control remains `type="button"`.
- Preserve native button semantics; remove `role="listitem"` from buttons.
  Name landmarks and inputs, expose busy/error state appropriately, and keep
  DOM reading order consistent with visual order.

## 5. Execution phases and step-by-step gates

### P0 — Capture the baseline and add failing regressions

1. [x] Re-read repository guidance, inspect branch/HEAD/worktree, and fetch
   remote state before choosing the implementation branch under current
   `dev` policy. Preserve the unrelated existing change to
   `frontend/test/browser/deployed_canary_test.exs`; do not include it in this
   work or reset it.
2. [x] Translate the review probes into permanent tests under the existing
   `WEB-TEST-077` through `WEB-TEST-081` owners. Add minimal failing cases for
   every confirmed defect before its production fix.
3. [x] Assert ellipsis DOM order, not only count; assert size retention after
   subsequent Next, numbered, and jump actions, not only immediately after
   selecting a size.
4. [x] Test explicit older-page Refresh and passive older-page completion
   through the rendered changed-banner button. Test a fresh explicit page URL
   with persisted History closed, and a normal URL that must preserve lazy
   loading.
5. [x] Add rejected metadata and exact-cardinality wire cases. Inventory
   synthetic fixtures whose rows disagree with their advertised totals.
6. [x] Add structural/style-source and semantic regressions for the toolbar,
   neutral Tailwind source registration, native buttons, and valid form
   ownership. Explicitly label their non-visual scope.

Exit: each defect has an observable, deterministic failing assertion for the
right reason. Record the red baseline. Do not push a deliberately failing
checkpoint to an auto-deploy branch, relax assertions, or hide failures.

### P1 — Harden shared state and response validation

Depends on P0.

1. [x] Implement the pure transition contract in
   `frontend/lib/prls_ui/pagination_state.ex`, with table-driven tests for
   navigation, same/changed size, reset, malformed input, and no-op behavior.
2. [x] Validate effective metadata and guard range/summary derivation. Cover
   zero, one, exact multiples, partial last pages, and above-range pages.
3. [x] Validate the exposed page-window options, including zero siblings;
   preserve ordered unique boundaries and a bounded window for large totals.
4. [x] Tighten `frontend/lib/harden_llm/llm_diagnostics_wire.ex` to require
   exact History item cardinality. Keep cursor decoding unchanged.
5. [x] Correct numbered fixtures in `frontend/test/support/api_fixtures.ex`
   and affected tests with unique realistic item IDs and coherent counts.
   Preserve tests for page replacement, failures, races, and mutations; do
   not change a multi-page scenario into a one-page scenario to make it pass.
6. [x] Extend wire/API tests under `WEB-TEST-080` and `TEST-231`; verify valid
   backend-produced examples still decode and invalid responses fail closed.

Exit: pure state/wire tests pass; no inverted ranges, silent size resets, or
acceptance of inconsistent numbered pages. No Go/OpenAPI shape change or new
dependency is required.

### P2 — Implement the compact component and complete its styling

Depends on P1; may proceed independently of P3 on non-overlapping files.

1. [x] Refactor `frontend/lib/prls_ui/pagination.ex` into the toolbar defined
   in section 4, retaining scoped events and existing navigation semantics.
2. [x] Render page numbers and ellipses in one traversal. Add the three
   presentation options without duplicating History-specific rendering.
3. [x] Keep First/Last as compact icon-only boundary buttons, alongside
   Previous/Next, numbered navigation, explicit jump, and size controls. Remove
   invalid button roles while preserving labels, current-page identity, and
   real disabled/loading bindings.
4. [x] Register `../../lib/prls_ui` in the Tailwind sources. Keep any added
   CSS narrowly scoped to pagination; do not restyle all forms or buttons.
5. [x] Extend component tests for exact token order, events/targets, option
   variants, fixed size, empty/single pages, unique IDs, and busy states.
   Replace obsolete First/Last selectors with equally strong assertions that
   boundary page controls still reach pages `1` and `total_pages`.
6. [x] Add a browser-free compiled-asset check against a freshly built CSS
   artifact for component-specific width, spacing, active and focus styles.
   Integrate it with the frontend asset/build gate; a missing artifact fails
   that gate. Do not make default fast tests depend on stale generated CSS or
   secretly build/download assets in the fast loop.

Exit: component/style-source tests pass; a fresh asset build contains the
required rules. One-toolbar structure is verified; actual one-row geometry,
keyboard delivery and focus behavior remain unverified without a browser.

### P3 — Repair History refresh and explicit route behavior

Depends on P1; final integration also depends on P2.

1. [x] Use the shared transition helper in `WorkspaceLive` while retaining
   host-owned History page bounds, URL encoding and requested/displayed state.
2. [x] Separate explicit refresh from passive run-completion notification.
   Manual Refresh reads the current requested page/size; Retry retains its
   failed target. Neither implicitly returns to page one.
3. [x] Preserve passive policy: refresh idle page one, but mark older pages
   changed without moving the user's view. During reads or mutations,
   coalesce/defer refresh intent so it is not lost or duplicated.
4. [x] Detect explicit History intent on initial navigation and pagination
   route changes. A valid History page/size route opens the widget after
   hydration and loads that destination exactly once. A route without
   History intent preserves saved fold/lazy-load behavior.
5. [x] Let the user fold History after opening a deep link. Unrelated
   `trace_id` patches must not continually reopen it merely because unchanged
   History parameters remain in the URL. A later explicit History navigation
   can open it again. Do not persist an unrelated editor save just to open it.
6. [x] Ensure reopening loads changed requested criteria rather than treating
   an older loaded page as current. Preserve allowed URL parameters, and
   replace server-clamped URLs without duplicate reads or patch loops.
7. [x] Preserve generation/collection guards, identical-request coalescing,
   and bounded task ownership. Audit the existing cancellation claim; cancel
   superseded tasks through supported LiveView APIs and handle cancellation
   completions without displaying errors. Keep generation checks even when
   cancellation succeeds; do not claim remote work cancellation without proof.
8. [x] Exercise refresh, route changes and delayed completions around
   delete/clear, failure/retry and authentication expiry. Preserve rollback,
   same-page disclosure state, departed-record pruning, current Result,
   prompt/profile drafts, and the no-provider-call boundary.

Exit: lifecycle/routing tests prove actual GET parameters and observable
results, including controlled response ordering. Each manual refresh causes
the intended read; explicit page links work independently of persisted fold
state; unrelated navigation stays unchanged.

### P4 — Make reuse evidence representative

Depends on P1 and P2.

1. [x] Update both harness pagers to use the production transition helper.
   Keep record slicing, filtering, drafts, selection and batch scope local to
   the host. Remove duplicated transition logic.
2. [x] Put editable fields in valid host forms with stable record IDs. Drive
   edits/search through rendered forms in LiveViewTest, not only raw event
   injection that bypasses the bindings being claimed.
3. [x] Cover size → Next → jump → return on both pagers; list filter and batch
   scope changes must reset only their owner. Assert summary and actual row
   IDs, and ensure an invalid jump cannot silently empty a valid local page.
4. [x] Verify drafts and selection survive paging by record ID; no page action
   submits an editor save, batch command, or the other pager's form. Cover
   distinct event targets as well as two IDs sharing one event name.
5. [x] Remove or accurately rename the harness's misleading `api` mode, which
   currently only switches local fixture arrays. Use History's HardenAPI
   tests as the real HTTP adapter evidence and the harness as local/editor
   composition evidence. Do not create a pretend API to satisfy a label.
6. [x] Extend neutral-boundary tests to reject imports of HardenAPI,
   WorkspaceLive, session/domain modules, Req, or task/process ownership in
   `PrlsUI`. Preserve the existing sole-HTTP-client check.

Exit: production History and two independent test collections share the same
control/state implementation. The evidence supports composition and isolation,
not app-dev parity or a second production deployment.

### P5 — Update documentation and certify the implementation checkpoint

Depends on P1–P4.

1. [x] Add `docs/reusable-pagination.md` with the actual component/helper API,
   a minimal local-list example, a remote-host outline, two-instance usage,
   form placement, Tailwind source requirements, and state ownership rules.
   Keep examples exercised by tests where practical.
2. [x] Amend ADR-HLLM-023 and the canonical frontend test catalog for compact
   boundary controls, transition semantics, exact DOM ordering, deep-link
   intent and stronger reuse proof. Extend existing IDs rather than renumber
   or erase prior requirements. Update `TEST-231`/traceability as necessary
   for the strict-cardinality evidence; do not claim a new REST format.
3. [x] Add a corrective addendum to the original plan/release record. Preserve
   historical release receipts and identify their coverage gaps instead of
   rewriting them as if these fixes were already deployed.
4. [x] Run formatting, focused tests, repeated `make test-fast`, and the fresh
   frontend asset gate. Review the diff for forbidden dependencies, accidental
   service changes, fixture weakening and unrelated worktree modifications.
5. [x] Record exact commands, counts, SHA and evidence limitations. Do not
   equate rendered HTML/CSS assertions with visual or native-event validation.

Exit: deterministic and asset/build gates are green for the exact application
checkpoint; documentation describes the delivered API and remaining limits.

### P6 — Push, deploy and record release evidence when execution is authorized

Depends on P5. Execution is the final release gate and is recorded after the
application checkpoint is committed.

1. [ ] Push verified checkpoints through the current `dev`/trusted-branch
   policy and wait for exact-SHA browser-free CI. Use an enabled preview only;
   do not silently provision a new credential-bearing environment.
2. [ ] Rebuild/restart Phoenix only for the expected frontend-only change.
   Preserve the compatible gateway image and all data/session volumes. If
   backend changes become necessary, explicitly revise scope and gates first.
3. [ ] Perform browser-free health/readiness, served-asset and authorized
   authenticated read checks. Verify numbered and legacy History still read
   successfully. Record each component's actual image/source identity, which
   may differ when the unchanged gateway is retained.
4. [ ] For an explicitly requested production release, run `make test-release`
   and the applicable repository release procedure before promotion. Keep
   user data/accounts, shared profiles and keys unchanged; do not use profile
   saves, data deletion or real model runs as probes.
5. [ ] Retain and verify a runnable previous Phoenix image/configuration for
   rollback. Roll back the web service only if the gateway was unchanged;
   do not rewind the database or overwrite configuration. A rollback tag by
   itself is not proof that the image can start.
6. [ ] Append sanitized release evidence with branch, pushed SHA, image IDs,
   URL, CI/gate results, HTTP checks, rollback identity and known limitations.
   Distinguish dev deployment from production, and report blockers plainly.

Exit: only the authorized environment is declared deployed, at the recorded
revision, after its checks pass. Package extraction remains separate future
work: extract and version the neutral modules when a second real application
adopts them; never copy diverging implementations across repositories.

## 6. Verification matrix and commands

| Owner / IDs | Required coverage | Tier |
| --- | --- | --- |
| State/component; `WEB-TEST-077/078` | Transition table, validated effective metadata, exact window order, boundary access, presentation options, IDs/targets, forms and native semantics | T0/T1 |
| History; `WEB-TEST-078/079` | Folded explicit links, normal lazy loading, manual/passive refresh, same/changed targets, stale completion/cancellation, retry, mutation and Result isolation | T1 |
| Wire; `WEB-TEST-080`, `TEST-231` | Exact row cardinality, bounds, unknown/missing fields, coherent backend fixtures, unchanged cursor behavior | T0/T1 |
| Reuse; `WEB-TEST-081` | Size retained through later navigation, actual editable bindings, per-owner filter/scope reset, stable-ID drafts/selection, no cross-submission | T0/T1 |
| Asset; `WEB-TEST-082` | Fresh compiled CSS contains the source-owned pagination width, spacing, active, focus and jump-input utilities | Browser-free asset/build check |
| CSS source plus fresh compiled asset | Neutral source included; required rules emitted; no silent missing-file skip | Browser-free asset/build check |
| Actual layout, focus and native form delivery | One-row fit, narrow wrapping, no overflow, keyboard/LiveSocket behavior | T4, separately authorized only |

Use the pinned toolchain for implementation verification:

```bash
export PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH
(cd frontend && mix format --check-formatted)
(cd frontend && mix test test/prls_ui test/harden_llm_web/live/pagination_reuse_test.exs test/harden_llm_web/live/history_trace_test.exs test/harden_llm_web/live/workspace_live_test.exs test/harden_llm_web/harden_api_test.exs test/harden_llm_web/boundary_test.exs)
go test ./internal/gateway -run 'TestOpenAPIContract|TestRecoveryContractOpenAPI' -count=1
make test-fast
(cd frontend && mix assets.deploy)
git diff HEAD --check
```

The new compiled-asset assertion must accompany the asset build in its owning
gate. Do not treat the build command alone as an assertion that the required
selectors exist. Do not add Node tests unless actual client code is added;
no new custom client JavaScript is expected.

Use private Req ownership, unique fixtures, supervised processes, controlled
completion messages, and bounded waits. Keep tests parallel-safe. Do not add
arbitrary sleeps, blanket serialization, retries, skip markers or relaxed
oracles to conceal failures. Real Postgres checks remain required if storage
semantics change, but need not be rerun as a new benchmark for CSS/state fixes.

If browser testing is explicitly requested, keep a small deterministic canary:
wide widget, narrow widget inside a wide viewport, mobile-width container,
large page counts/zoom, and native size/jump/keyboard navigation. Verify actual
geometry, overflow, accessible names and focus/patch behavior with the shared
host's single coordinated browser workflow. Without that authorization, record
`browser layout and native-event behavior not checked`; do not launch one or
claim visual sign-off.

For this plan-only change, use Markdown/link and whitespace checks. Do not run
application builds, a release suite, deployment, browsers or providers merely
to validate planning text.

## 7. Completion checklist

- [ ] Every reviewed defect has a permanent regression and a verified fix.
- [ ] Controls use one compact wrapping toolbar with correct ordered tokens.
- [ ] Neutral styles are present in a freshly built application asset.
- [ ] Shared transitions preserve sizes; effective metadata is valid; bad
  remote pages fail closed without replacing good displayed state.
- [ ] Explicit deep links and manual Refresh work on older pages; passive
  refresh, folding, URL composition and lifecycle protections remain intact.
- [ ] Reuse is demonstrated through actual rendered form/event contracts,
  independent owners and stable-ID draft/selection preservation.
- [ ] No generic list framework, duplicate pager, new persistence path,
  dependency upgrade, schema change or provider interaction was introduced.
- [ ] Documentation and test catalogs distinguish implemented behavior,
  historical evidence, current gates and unverified browser behavior.
- [ ] For any requested release, exact-SHA CI, environment/image identities,
  authenticated read checks and runnable rollback evidence are recorded.

Do not mark this plan complete merely because pre-existing tests pass. Each
phase's new assertions and applicable gates must pass without changing their
purpose; browser and cross-project claims remain bounded by actual evidence.
