# LLM Stats, Output Details, and Reusable JSON Viewer Plan

- Plan ID: `PLAN-HLLM-STATS-JSON-001`
- Status: Implemented and production-verified.
- Date: 2026-09-09
- Inspected baseline: `729804cdfab9ff5c2e9dd75c64609cddd6773557`
- Scope: Correct the reviewed output-details interactions, extract a small reusable JSON viewer, simplify stats presentation, and verify the resulting release.

## 1. Outcome and architectural constraints

The output widget should remain compact: a clickable stats row, one wrapping
controls row, and inline structured content. Each control must behave
independently and visibly communicate its state. The JSON renderer should be
usable outside the LLM widget without acquiring LLM-specific dependencies.

Preserve the architecture established by
[ADR-HLLM-018](../docs/adr/ADR-HLLM-018-canonical-execution-accounting-and-recovery.md):

- `HardenAPI` is the only frontend REST client. No component performs requests.
- Existing projections own presentation of validated execution/accounting data;
  this work does not recalculate usage, cost, cache, or provider identity.
- Host LiveViews own asynchronous requests, resource availability, and pane state.
- `LiveStats` remains the shared aggregate-stats lifecycle owner.
- PostgreSQL remains authoritative for execution/history/stats; Garage holds
  immutable artifact exports. Telemetry remains downstream, not a widget data
  source. No Langfuse, Laminar, or ClickHouse integration changes are needed.
- Existing OpenAPI, storage, authorization, and redaction boundaries remain intact.

This is a frontend correction and bounded refactor, not another execution-data
migration. The resolved issues in [the existing KER collection](../ker/llm-output-details/README.md)
remain resolved; this plan does not reopen them or claim their work is repeated.

## 2. Findings and their disposition

| Finding | Evidence / affected implementation | Planned resolution |
| --- | --- | --- |
| Active buttons can look identical to inactive buttons | `app.css`: ancestor-qualified resource-button rules override `.trace-action` state rules; the preceding rendered check observed identical colors | Consolidate base styles; style disclosure state from `aria-expanded`; assert computed appearance in Chromium |
| JSON loading/failure suppresses unrelated resources | `llm_trace_components.ex`: shared loading/error gate surrounds JSON, Request, and Response | Give each pane its own visibility/content boundary; only JSON owns its fetch status |
| Closing Details also closes other panes | `WorkspaceLive.reset_output_data_if_closed/3` | Remove this coupling; the stats row is the only master disclosure |
| JSON cannot close during loading and completion forces it open | `toggle_output_trace/1` and `handle_async({:load_output_trace, ...}, ...)` | Allow closing/reopening an in-flight pane without another request; completion changes data, not user selection |
| Structured display is incomplete | Details is still rendered as paragraphs/list items | Render the existing normalized details data through the shared viewer, preserving every currently displayed fact |
| Artifact text duplicates the JSON action | Every available artifact button sends `kind="trace"` | Keep one JSON disclosure; render artifact size/state as noninteractive metadata |
| Resource rendering guesses a string's type | `resource_payload/1` calls `Jason.decode` for any string | Accept already-decoded JSON values and preserve their types; keep missing distinct from JSON `null` |
| JSON rendering is coupled to trace markup/CSS | `json_value/1` and `.trace-json-*` live in trace-specific code | Extract one transport-free Phoenix function component and scoped stylesheet |
| Trace and aggregate stats responsibilities share one module | `llm_trace/1`, `llm_stats_summary/1`, duplicate stats-key lookup definitions | Move aggregate presentation to its own module; use one ordered field definition |
| Tests miss important browser invariants | Prior canary checked labels/layout but not selected appearance; native fold preservation needs browser proof | Add cheap root-invariant tests and extend the existing two canaries only for browser-owned behavior |
| Local browser assets previously appeared stale | The browser test container was rebuilding shared digest/gzip assets concurrently with the host release task, which could leave root-owned or incomplete generated files | Remove the browser-side asset build; make the browser tier depend on the host `frontend-assets-deploy` task so it reads the release candidate's assets. The final release gate covers the ordered path |
| Prior promotion was not fully release-certified | The locked `mint` version failed the retained advisory audit | Update the compatible locked `mint`/`castore` versions, verify the HTTP dependency behavior, and require the complete release gate to pass before promotion |

The string-conversion problem is a reusable-component contract flaw. Current
OpenAPI `TraceResource.payload` is an object; do not describe this as proof that
the live REST endpoint currently returns malformed scalar payloads.

The prior audit failure and rendered observations are retained evidence, not
fresh audit/deployment results from creating this plan.

## 3. Interaction contract

### 3.1 Controls and visible content

| Element | Click behavior | State and presentation |
| --- | --- | --- |
| Stats row | Hide/show the entire controls-and-content region | Preserve selected panes and loaded data; expose `aria-expanded` and `aria-controls` |
| Details | Toggle normalized execution details only | Stable label; inline JSON; no effect on JSON/Request/Response |
| JSON | Toggle the full trace REST response | Stable label; load once per current trace as needed; show loading/error inside this pane only |
| cURL | Copy the existing prepared cURL text | Command, not a disclosure; preserve clipboard success/failure feedback; no `aria-expanded` or selected state |
| Request | Toggle the available request payload only | Stable label; inline JSON; usable while trace JSON is loading or failed |
| Response | Toggle the available response payload only | Same independent behavior as Request |
| `trace · N bytes` | No action | Compact metadata for the stored artifact, including availability/state where supplied |

Keep the current order `Details`, `JSON`, `cURL`, `Request`, `Response`, artifact
metadata. Wrapping is allowed. Do not add another Show/Hide control, force a
single unwrappable row, or open output information in a new tab.

Multiple panes may be open simultaneously. Closing and reopening the stats row
must restore the same selection, including an existing JSON error. Closing a
pane must also hide its loading/error message. Closing Details is not a reset.

Artifact `sizeBytes` describes the Garage export, not necessarily the byte count
of the REST response displayed by JSON. Keep that distinction in its accessible
description/title; do not calculate or advertise a false equivalence. Preserve
existing artifact download functionality in History; the output metadata was
not performing an artifact-specific download.

### 3.2 JSON request transitions

Keep visibility separate from request state. Use the current host-owned
assigns and a small reset/apply helper rather than introducing a resource-store
framework or a second state machine library.

| Situation | Required transition |
| --- | --- |
| Open JSON with no data and no in-flight request | Open the pane, clear its old error, start one request keyed by reference and trace identity |
| Close JSON during loading | Hide the pane; let the request finish; do not disable the toggle solely because it is loading |
| Reopen while the same request is running | Show its loading state; do not start a duplicate request |
| Current request succeeds | Cache its data, clear loading/error, preserve whether the user left the pane open or closed |
| Current request fails or exits | Set a pane-local error and clear loading; do not close, reopen, or suppress unrelated panes |
| Close then reopen a failed JSON pane | Start one fresh attempt; no background retry loop or additional Retry toolbar button |
| Reopen successfully loaded JSON | Reuse the current trace snapshot; no extra network request |
| Start another run or select another trace/conversation | Invalidate the previous trace load identity and clear trace-specific data/errors/selections using the existing lifecycle |
| Old request completes after the trace changes | Ignore success, failure, and process-exit results alike |

Retain current persisted top-level UI preference keys. Nested JSON folds and
fetched data remain transient; do not add browser storage, database columns, or
per-node persistence. On identity changes, audit all existing diagnostic-load
references that can replace `run_result`; add delayed-result regressions before
changing any additional handler. Do not fix an unproved unrelated race by
rewriting the whole workspace lifecycle.

### 3.3 State styling and accessibility

- Define the base control appearance once at `.trace-action`; keep layout rules
  on the controls container rather than ancestor selectors that override state.
- Use `aria-expanded="true"` as the single disclosure-style source. Remove
  redundant active/inactive classes and `aria-pressed` on disclosure buttons.
- Use the existing neutral/accent palette for selection, not success green.
  Include a non-color cue, such as an inset underline, without changing button
  dimensions or label text.
- Keep expanded, hovered, focused, disabled, and busy appearances distinguishable.
  Busy JSON must remain closable. Available Request/Response stay enabled.
- Give each disclosure a valid, instance-scoped `aria-controls` target. Hidden
  content and loading/error announcements must not remain in the tab order.
- Keep keyboard activation and visible focus. Check that the stats row's
  accessible name/description makes its action and key metrics discoverable;
  do not replace all metrics with an opaque button name or nest buttons in it.
- Preserve the compact cache/save indicator with no decorative badge/circle.

## 4. Component and data ownership

| Owner | Responsibility after this work | Explicit exclusions |
| --- | --- | --- |
| `HardenAPI` | Existing validated REST calls | No rendering or pane state |
| `LlmTraceProjection`, `LlmStatsProjection`, `LlmCostFormatter` | Existing semantic presentation and accounting certainty | No second accounting calculation or telemetry lookup |
| `WorkspaceLive`, `HistoryLive`, embedding hosts | Fetching, current trace identity, availability, and UI events | No JSON recursion or duplicated generic stats lifecycle |
| `LiveStats` | Existing `AsyncResult`, refresh, last-good snapshot, timestamp/reference handling | No new polling loop in components |
| `LlmTraceComponents` | Single-execution summary, controls, pane/resource composition | No aggregate-stats renderer or JSON tree implementation |
| New `LlmStatsComponents` | Aggregate stats markup and metric disclosures | No fetching, billing logic, or trace controls |
| New `JsonViewer` | Rendering decoded JSON and native folds | No LLM fields, endpoints, auth, resource availability, parsing guesses, or clipboard transport |

Add one small private trace-disclosure function component to centralize common
button markup. Keep the five controls explicit; no configurable action registry
or generalized toolbar framework is needed. The cURL command remains separate.

### 4.1 JSON viewer contract

Proposed API:

```heex
<.json_viewer id="output-details-document" data={@details} expand_depth={1} />
```

Place it at `frontend/lib/harden_llm_web/components/json_viewer.ex`, with styles
under `frontend/assets/css/json_viewer.css` imported by `app.css`.

1. Require a unique `id` and decoded JSON `data`. Default `expand_depth` to one:
   root containers open, nested containers closed. Add only a root class override
   if an actual existing host layout requires it.
2. Support string-keyed objects, arrays, strings, numbers, booleans, and `nil` as
   JSON `null`. Render strings as strings, even if their content is `"42"`,
   `"true"`, `"null"`, or serialized JSON. Remove resource-level implicit decoding.
3. Do not silently stringify arbitrary Elixir structs, tuples, or atoms with
   `inspect`. The caller supplies JSON-compatible data; unsupported internal
   input is an explicit development/test contract failure. Do not add a second
   recursive wire validator inside the component.
4. Keep unavailable resources outside the viewer. A missing payload is not an
   empty object, empty string, or `null`. A present `nil` value is real `null`.
5. Reuse native `<details>/<summary>` folding and escaped HEEx text. Show a
   visible open/closed marker, empty containers, and readable scalar roots.
   Do not leave an empty key column constraining a root scalar.
6. Namespace all tree IDs by the viewer root. Derive object-child identity from
   the key/path, not its position in a sorted map. An encoded JSON Pointer is a
   bounded option; handle `/`, `~`, quotes, Unicode, and empty keys without
   collisions. Array positions are sufficient for these immutable snapshots.
7. Keep loaded viewers mounted under hidden pane wrappers so closing a pane
   does not destroy folds. Preserve folds across unrelated LiveView patches.
   Change document identity when a different trace/run is displayed; new data
   must never be hidden by a blanket `phx-update="ignore"` on the viewer.
8. First test native DOM/LiveView behavior. Add a narrowly scoped open-state hook
   only if the browser regression proves stable IDs and ordinary patching are
   insufficient. Such a hook must preserve fold state without freezing text or
   retaining a previous document's values.
9. Bound the viewer's viewport and keep overflow inside it. It is a tree view,
   not a guarantee that copying rendered text yields a complete JSON document;
   existing Copy JSON actions continue to serialize the original data.

Native folding does not make rendering lazy: the hidden subtree is still
rendered. Record behavior on representative small and larger deterministic
fixtures, but defer virtualization, lazy child fetching, search, editing, schema
plugins, and a new JSON library until measured use demonstrates a need.

### 4.2 Adoption and stats cleanup

- Output Details uses the existing normalized `details` map, preserving trace/run
  identity, schema/capture state, selected and actual provider information,
  status, cache facts, accounting certainty, repair, and all attempt facts.
  Compare a field inventory before removing the old paragraphs.
- Output JSON uses the decoded trace response. Request and Response use their
  already-decoded resource payloads. Keep availability/error messaging outside
  the JSON component.
- Replace existing structured JSON `<pre>` displays in History records,
  request/result panels, observation data, and workspace inline history with the
  same component. Assign IDs using host scope plus record/observation identity.
  Preserve History's existing modal, navigation, copy, and download behavior.
- Leave natural-language model output as text. Do not auto-parse arbitrary model
  output just because it resembles JSON.
- Move `llm_stats_summary/1` and its aggregate-only helpers/tests into
  `LlmStatsComponents`. Preserve its existing `AsyncResult` input and host API;
  an extra lifecycle adapter is unnecessary for Phoenix reuse.
- Replace `stats_fields/0` plus the duplicate lookup map with one ordered
  atom-key field list matching `LlmStatsProjection`. Preserve current label
  overrides and DOM IDs; adapt known internal callers/tests together instead of
  retaining dual string/atom data contracts. Never create atoms from input data.
- Update imports and direct module references in the same checkpoint. Remove
  obsolete rendering/parsing helpers and unused generic resource-loading/error
  attributes after checking every caller. Do not leave forwarding compatibility
  modules for this repository-internal extraction.

This makes the viewer reusable in current Phoenix applications. A published
package, React wrapper, or framework-neutral custom element is deliberately
deferred until another application defines concrete integration requirements.

## 5. Implementation sequence and checkpoints

### 5.1 Phase A — Establish regressions and release prerequisites

1. Recheck the worktree and baseline before editing; preserve unrelated changes.
2. Update the frontend specification with the interaction contract above. Extend
   `WEB-TEST-060` through `WEB-TEST-063` as appropriate and add the canonical
   `WEB-TEST-064` and `WEB-TEST-065` coverage for the viewer and route identity.
3. Add failing deterministic tests for pane independence, request completion not
   reopening closed content, stale results, and string/null fidelity. Keep the
   old assertion strength when replacing tests of the undesired shared gate.
4. Reproduce the current selected-style failure through the existing browser
   fixture and record computed styles, not merely CSS source declarations.
5. Investigate the stale-assets observation: record requested stylesheet URL,
   `Content-Encoding`, response bytes, build ordering, digest/manifest usage, and
   compressed siblings. `assets.build` currently differs from `assets.deploy`;
   do not assume that a manifest alone explains the mismatch. This investigation
   established a shared-checkout writer race in the browser container.
6. Make the test topology perform `frontend-assets-deploy` once on the host
   before the browser container starts, and remove the browser-side asset build.
   Keep production compression/digests exercised by the release boundary. Add no
   second asset runner, broad generated-directory deletion, or manual retry.
7. Rerun the dependency audit implicated by the retained Mint failure. Check the
   actual locked version and authoritative advisory/fixed-release information
   before selecting a minimal compatible update. Verify the lockfile and affected
   HTTP behavior, then the full audit. Do not suppress the advisory, lower its
   severity, or describe a partial test set as release certification.

Audit remediation can be a separate coherent dependency commit. UI work can
continue locally while it is investigated, but production promotion waits for
the complete release gate to pass.

### 5.2 Phase B — Correct existing trace interactions

1. Implement independent pane wrappers and JSON-local loading/error rendering.
2. Remove Details-to-other-panes reset behavior. Preserve the master wrapper's
   hide/show behavior and current persisted preferences.
3. Fix JSON close/reopen/in-flight completion transitions and stale-result guards.
   Consolidate only repeated current-trace assignment/reset code where useful.
4. Consolidate button styles and disclosure markup/ARIA; retain noun labels and
   the existing command behavior for cURL.
5. Replace the duplicate output artifact action with accurate inert metadata.
6. Run cheap regressions, `make test-fast`, and the targeted browser canaries.

Checkpoint B is a working interaction fix, not a half-migrated component API.
Once its complete release gate is green, commit, push, and deploy that exact
checkpoint through the existing production path before continuing prolonged
user-visible work.

### 5.3 Phase C — Extract and adopt the reusable JSON viewer

1. Add viewer tests before extracting the current recursive renderer.
2. Implement the minimal API, type preservation, scoped styles, stable IDs,
   native markers, scalar layout, and fold-preservation behavior.
3. Replace all four output panes' structured rendering with the viewer. Confirm
   Details field completeness against the pre-change inventory.
4. Adopt the same viewer in existing structured History displays without changing
   their fetching, modal, copy, or artifact-download semantics.
5. Remove the old trace-owned tree and parsing helpers; search for duplicate JSON
   recursion and obsolete CSS after migrating every call site.
6. Run the cheap viewer/host regressions, `make test-fast`, and browser-boundary
   checks. Keep a real multi-instance case and an unrelated-patch fold case.

Checkpoint C is a complete, usable component with real non-output consumers,
not a framework extraction waiting for callers. Certify, push, and deploy it as
a coherent user-visible checkpoint.

### 5.4 Phase D — Separate aggregate stats and complete handoff

1. Move aggregate presentation/helpers and their existing assertions to
   `LlmStatsComponents`; update workspace/history imports and module references.
2. Remove duplicate field-key definitions while preserving every metric, label,
   ordering, DOM identity, empty/unknown distinction, and cost disclosure.
3. Confirm `LiveStats` still owns refresh/reference/timestamp and stale-snapshot
   behavior without an extra client, poller, cache, or subscription.
4. Update component documentation and canonical test commands for moved tests.
5. Run the final complete gates and production verification described below.

Phase D may share checkpoint C if the extraction is small and verified together.
Do not create a separate production deployment just for a mechanical module move
when it fits the same coherent checkpoint.

## 6. Verification matrix

Follow [ADR-HLLM-015](../docs/adr/ADR-HLLM-015-parallel-test-feedback-hierarchy.md)
and the [frontend test catalog](from_utility-llm/phoenix-liveview-frontend-spec.md).
Use the lowest sufficient tier and retain browser tests only for facts that
static rendering and LiveView events cannot prove.

| Invariant | Primary coverage | Browser-only proof |
| --- | --- | --- |
| Stable labels, correct ARIA targets, no duplicate artifact action | Component tests; all control states | Actual expanded/collapsed computed styles differ; focus and non-color cue remain visible |
| Master hides everything and restores selection | LiveView state and rendered `hidden` wrappers | No visible/focusable descendants while collapsed |
| Details/Request/Response independence | LiveView permutation tests with private stubs | One representative combined interaction, not every permutation |
| JSON loading/error isolation, close/reopen, retry, caching | Delayed process-owned stubs; exact request counts; success/error/exit cases | Pane content/error visibility in the existing canary |
| Old results never replace a newer trace | Deliver old success, failure, and exit after an identity switch | Not required unless a distinct browser boundary is found |
| JSON type fidelity | Objects/arrays, empty containers, numeric/boolean/null-looking strings, real numbers/booleans/null, multiline/escaped text, absent payloads | Root scalar and overflow layout |
| Viewer identity and escaping | Multiple viewers; unusual keys; stable object-child IDs after another key is inserted; unsupported internal input | Native keyboard folding; one instance cannot fold another |
| Fold preservation and data freshness | Stable document IDs and wrapper lifecycle assertions | Open nested fold, trigger unrelated LiveView patch, verify it stays open; change document and verify fresh values/default folds |
| Details retains execution facts | Projection/component field inventory including failure, cache, repaired/multiple attempts, retained not-captured state | Representative redacted fixture renders readably |
| Aggregate stats parity | All fields/label overrides; initial loading/failure, successful empty, refresh/stale, exact/partial/unknown/unavailable cost | Existing cost disclosure/focus behavior remains functional |
| History adoption does not regress functionality | History/Workspace LiveView tests; copy source and artifact link identity unchanged | Existing modal/clipboard/navigation canary coverage |
| Browser uses current build | Cheap regression for the reproduced asset cause | Assert expected computed styling from assets actually served, including normal release assets in release tests |

Use `async: true`, private Req ownership, and per-test references/fixtures where
applicable. Do not add sleeps, ambiguous retries, serialization to hide races,
jsdom, or Happy DOM. If a tiny fold hook proves necessary, put its pure decisions
under the existing dependency-free Node client-core tests and leave native DOM
patch behavior to Chromium.

Keep exactly the two ordinary browser canaries required by the current policy.
Extend their assertions instead of adding a new ordinary browser suite or moving
every JSON value/state permutation into Chromium. Add deterministic fixture
screenshots at desktop and narrow widths for the rendered change; use no live
prompt, credential, or diagnostic content in retained screenshots.

## 7. File-level implementation map

| File / area | Planned change |
| --- | --- |
| `frontend/lib/harden_llm_web/components/llm_trace_components.ex` | Independent panes; common disclosure markup; artifact metadata; viewer calls; remove generic JSON and aggregate-stats ownership |
| `frontend/lib/harden_llm_web/components/json_viewer.ex` | New minimal JSON function component |
| `frontend/lib/harden_llm_web/components/llm_stats_components.ex` | Extract aggregate presentation and single field definition |
| `frontend/lib/harden_llm_web/live/workspace_live.ex` | Trace fetch transitions, reference/reset helpers, removal of Details reset coupling |
| `frontend/lib/harden_llm_web/live/workspace_live.html.heex` | Host viewer identities and structured inline-history adoption |
| `frontend/lib/harden_llm_web/live/history_live.html.heex` | Shared JSON rendering with existing modal/copy/download behavior |
| `frontend/lib/harden_llm_web/live/history_live.ex` | Remove serialization-for-display helpers only where no longer needed; retain clipboard serialization |
| `frontend/lib/harden_llm_web.ex` | Imports for extracted components; update all direct callers |
| `frontend/assets/css/app.css`, new `json_viewer.css` | Resolve state specificity; extract scoped viewer styles; remove obsolete selectors |
| `frontend/test/harden_llm_web/components/` | Existing trace tests plus dedicated viewer and aggregate-stats tests |
| `frontend/test/harden_llm_web/live/` | Pane transitions, stale results, History adoption, existing embedding isolation |
| `frontend/test/browser/widget_canary_test.exs`, `authenticated_workflow_canary_test.exs` | Selected styling, native folds, patches, focus, overflow, multi-instance coverage within existing canaries |
| Test asset setup/config and `endpoint.ex`, only if justified | Fix the proved build/serve mismatch without changing production asset semantics |
| `frontend/mix.exs`, `frontend/mix.lock`, only as required | Minimal verified dependency remediation for the failing audit |
| Frontend specification and `docs/release-certification.md` | Updated behavior/test traceability and truthful checkpoint certification evidence |

No Go implementation, SQL migration, OpenAPI change, telemetry service change,
or sibling-repository edit is expected. If implementation finds one necessary,
stop and explain the newly discovered contract problem before expanding scope.

## 8. Release, deployment, and acceptance

### 8.1 Local and release gates

Expose the pinned toolchain before local frontend or aggregate gates:

```bash
export PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH
```

During implementation, run focused deterministic tests, formatting/compile
checks, and `make test-fast` repeatedly. For each user-visible release candidate:

```bash
make test-browser
make test-release
git diff HEAD --check
```

The full manifest-owned release gate must succeed, including dependency audits,
Docker/integration boundaries, and required release browser checks. Preserve a
failed attempt's evidence and link the later corrected candidate; do not replace
it with an unrelated retained success or certify only a subset of the gate.

### 8.2 Exact-revision promotion

1. Commit each verified coherent checkpoint with updated tests/specification;
   push it and record the full SHA and push result. If committing changes the
   release identity, ensure the recorded certification identifies the exact
   pushed tree and the built release embeds that pushed SHA.
2. Follow the documented direct Compose production path. Use the approved
   production/observability environment without printing secrets. Do not add
   staging, a registry, or a new deployment service for this refactor.
3. Build the frontend release image once, inspect its identity, and promote that
   image with the existing `--no-build --no-deps` web-service update. Do not
   rebuild or relabel an unchanged gateway to imply it shares the new web SHA.
4. Record full source SHA, frontend image ID/digest and release label, unchanged
   gateway identity, deployment result, and web/API health/readiness evidence.
5. Run the existing exact-identity authenticated deployed canary, including its
   already-approved bounded provider smoke, cleanup, and logout. Do not add
   unrelated public-provider calls for JSON renderer testing.
6. Append accurate release evidence using the existing certification convention.
   Keep credentials, session material, and unredacted live output out of it.

Before promotion, identify the compatible previous web image and rollback
command using the existing Compose path. Rollback affects only the changed web
service and preserves production data, session volumes, and environment. Do not
call the previous image fully certified if its retained audit failed. Any
available rollback candidate's known limitations must remain explicit.

### 8.3 Definition of done

- [x] Every issue in Section 2 has either a verified fix or a specifically
  documented non-reproduced finding; no browser-asset hypothesis is presented as fact.
- [x] The stats row hides all controls and their content; individual controls
  affect only their panes, including during loading/failure.
- [x] Selected controls are visibly distinct with stable compact labels and
  accessible non-color state/focus cues.
- [x] All output diagnostic panes use one type-preserving inline JSON viewer.
- [x] Viewer reuse exists in History without changing transport or download/copy behavior.
- [x] Aggregate stats are separate, with one field definition and unchanged
  accounting certainty/lifecycle behavior.
- [x] No duplicate renderer, implicit string parser, obsolete active-state
  classes, or unused compatibility/loading layer remains.
- [x] Deterministic, real-browser, and complete release gates pass for the candidate.
- [x] The exact pushed application revision is deployed and authenticated hosted
  checks pass. Test-only certification follow-up is pushed separately.
- [x] Final handoff names commit/push/deployment/image evidence and any remaining
  blocker; it does not call local-only work production-complete.

### 8.4 Completed certification evidence

The application-bearing checkpoint is `21d53f3d5fd34ceb3bc9d8c992c46d17b08a118f`.
It was pushed to `origin/main`, built once as the frontend release image, and
promoted with `--no-build --no-deps` to the existing production Compose project.
The later test-only canary robustness commit is
`1c26356c6024df1de48324e0f85d779763431edc`; it does not change the runtime
image or require a second deployment.

Final verification:

- `make test-fast`: accepted, 8 tasks, no failures or cleanup errors.
- `make test-browser`: accepted, 4 tasks, no failures or cleanup errors.
- `make test-release`: accepted, 26 tasks, no failures or cleanup errors.
- Workspace LiveView suite: 40 passed; the added regression holds output
  controls interactive during a slow UI-preference save and persists the latest
  state after the save completes.
- Frontend image: `sha256:247cfd6c1ba9c630851388720d6c7e75bf208a0e74721037629f08a8790ab371`,
  OCI release label `21d53f3d5fd34ceb3bc9d8c992c46d17b08a118f`, healthy.
- Gateway remained unchanged at release `729804cdfab9ff5c2e9dd75c64609cddd6773557`,
  image `sha256:924f573041275184ea12df883afd89610aa14d8163bfe1fae4badc0fdf10a20f`,
  healthy.
- Production `/healthz` and `/login` plus API `/healthz` and `/readyz`: HTTP 200.
- Exact-identity authenticated deployed canary passed the workspace trace
  controls, JSON/request/response panes, bounded CPA smoke, smoke-history
  cleanup, logout, and redaction checks.

The deployed canary also confirmed that persisted closed/open control state is
valid and that the canary must normalize it before asserting the interaction.

## 9. Deliberately deferred work

Do not add a cross-framework package, global state store, generic resource
manager, new telemetry path, historical accounting reconstruction, JSON editing,
search, virtualization, lazy network loading of tree nodes, automatic refresh of
immutable trace JSON, persisted tree folds, or an additional browser framework.

Revisit those only when there is a concrete second-consumer requirement,
measured payload/rendering problem, or user-requested capability. The immediate
deliverable is correct interaction, a small shared renderer, simpler ownership,
and trustworthy production verification.
