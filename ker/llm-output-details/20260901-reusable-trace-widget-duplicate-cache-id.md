Known Error Record: Reusable trace widgets emit a duplicate cache metric DOM ID

KER slug: 20260901-reusable-trace-widget-duplicate-cache-id
Git reference: fcda74b3824fc22a517df709f0a67939b8aa0b9c (application release where observed)
Resolution status: Open
Applies to (scope): Any Phoenix page embedding more than one `LlmTraceComponents.llm_trace/1` projection; broader cross-application extraction also inherits global CSS and map-contract coupling
Tags: liveview, reusable-widget, duplicate-id, accessibility, css-scope, projection
Anomaly classification (IEEE 1044–inspired, lightweight):
  - anomaly type: defect
  - reproducibility: always
  - impact: low
  - likely cause category: code

Trigger patterns (for fast matching)
- multiple rendered elements match `#run-cache-status`
- DOM uniqueness or accessibility checks report a duplicate ID after embedding multiple trace details
- cache status lookup by ID resolves only the first widget instance

Problem Record (conceptual guidance)

Symptoms
- `LlmTraceProjection.summary/1` hard-codes `"id" => "run-cache-status"` into every cache metric.
- `llm_trace/1` correctly derives its control IDs from the caller-supplied widget ID, but metric IDs bypass that namespace.
- One current output widget does not collide; reuse on the same page deterministically creates duplicate DOM IDs.
- The component also depends on global `.ullm-*` CSS and an implicit string-key map, so reuse outside the current Phoenix asset bundle is not self-contained.

Likely causes (ranked mental model)
1) A test selector was embedded in pure presentation data rather than derived from the component instance ID.
2) The component boundary was extracted before all DOM identity and style ownership were made instance-scoped.
3) Reuse tests cover profile widget instances but do not mount multiple output trace widgets and assert global ID uniqueness.

Diagnostic decision path
1) Check: Render two traces and count IDs.
   How: Mount two `llm_trace/1` components with different root IDs and query all `[id]` values or `#run-cache-status`.
   If true: The projection injects a duplicate independent of component root IDs.
   Next step: Remove or namespace the metric ID.

2) Check: Determine whether the cache icon needs an ID.
   How: Search CSS, hooks, tests, and LiveView events for `run-cache-status`.
   If true: If it is selector-only, replace it with a class or `data-cache-status`; if functional, derive it from `@id`.
   Next step: Keep identity ownership inside the component.

3) Check: Audit extraction dependencies.
   How: Inventory `.ullm-*` selectors, required map keys, hooks, and host-owned events used by `LlmTraceComponents`.
   If true: A second application would need undocumented globals.
   Next step: Document/version the input contract and scope styles under the component root before packaging.

Evidence from this incident
- key error excerpt:
  `"id" => "run-cache-status"`
  `<span :for={metric <- list_value(@summary, "metrics")} id={value(metric, "id")}>`
- logs / files involved: No runtime log is emitted; duplicate identity is deterministic rendered DOM behavior.
- code / config areas involved: `frontend/lib/harden_llm/llm_trace_projection.ex`, `frontend/lib/harden_llm_web/components/llm_trace_components.ex`, `frontend/assets/css/app.css`, component and browser tests
- what did NOT work:
  Configurable component control IDs -> covered details/cURL/request/response elements but not IDs supplied inside summary metric data.
  Single-instance browser canary -> could not reveal duplicate IDs.

Decisions and tradeoffs
This section captures WHY choices were made, including rejected options.

Options considered
- option: Remove nonfunctional metric ID and use semantic selectors
  description: Keep class, `data-cache-status`, title, role, and aria-label; stop assigning an ID when no behavior requires one.
  pros: Smallest fix, naturally supports any number of instances, and preserves styling/testing hooks.
  cons / risks: Tests selecting the old ID must change.
  decision: accepted
  rationale: An ID should not exist solely as a global test selector.

- option: Pass an ID prefix into the pure projection
  description: Make `summary/2` derive every metric ID from the host instance.
  pros: Retains per-element IDs when genuinely required.
  cons / risks: Couples pure data projection to DOM placement and increases host obligations.
  decision: deferred
  rationale: Use only if a concrete behavior requires element identity.

- option: Package the component immediately for every frontend framework
  description: Create a cross-framework widget library now.
  pros: Maximum theoretical reuse.
  cons / risks: Premature abstraction without a second consumer and does not solve the current DOM defect by itself.
  decision: rejected
  rationale: First make the Phoenix boundary clean and prove it with a second instance; extract when a real consumer defines packaging needs.

Key constraints influencing decisions
- Multiple instances must have globally unique DOM IDs and independent event targets.
- The pure projection should remain transport-, route-, session-, and storage-independent.
- Existing CSS should preserve the unboxed cache icon and same-row wrapping behavior.

Non-obvious context
- CSS classes and `data-*` attributes are reusable selectors and do not require uniqueness.
- The current component is reusable within Phoenix but is not yet a framework-neutral package.
- Profile widget multi-instance certification does not automatically certify output widget identity.

Workarounds
- Avoid rendering more than one projected trace summary on a page.
- If embedding is unavoidable before the fix, omit the metric ID in host-normalized data; preserve the class and data attribute.

Known Error Record (what actually worked)

Root cause (best current understanding)
- DOM identity is split between the instance-aware component and instance-unaware projection data; the cache metric retained a hard-coded global selector.

Permanent fix
1) Remove `run-cache-status` from projection output unless a functional ID requirement is demonstrated.
2) Update tests and browser checks to select the cache metric by widget root plus class or `data-cache-status`.
3) Add a two-instance component/LiveView test that rejects duplicate IDs and verifies independent controls.
4) Scope/document required CSS, hooks, events, and normalized map fields as the reusable Phoenix contract.
5) Defer a separate package until a second application supplies concrete framework and distribution requirements.

Verification
How to confirm the fix:
  Render two trace components, assert every nonempty DOM ID is unique, exercise both instances independently, then run `make test-fast` and `make test-browser`.
Expected result:
  No `run-cache-status` duplicate exists, selectors remain stable through root/class/data attributes, and both trace widgets preserve independent state and controls.

Prevention / guardrails
- Add a reusable helper that fails component tests on duplicate rendered IDs.
- Keep host instance IDs out of pure domain projection unless explicitly passed as presentation context.
- Require a multi-instance test for every component described as reusable.

Principal-engineer resolution plan (2026-09-01)

Deep-RCA corrections
- The duplicate is deterministic only when two or more projected trace widgets are rendered. The current application has one `llm_trace` call at `frontend/lib/harden_llm_web/live/workspace_live.html.heex:271`, so this is a proven reuse defect rather than a present same-page production collision.
- `frontend/lib/harden_llm/llm_trace_projection.ex:16-28` mixes semantic metric data with DOM IDs, CSS classes, roles, and `data-*` attributes. `frontend/lib/harden_llm_web/components/llm_trace_components.ex:95-103` renders those fields unchanged, splitting HTML ownership across projection and component.
- Removing `run-cache-status` alone is insufficient for robust reuse. Details events carry only name/open and resource events only kind (`llm_trace_components.ex:76-79,141-153`); the host stores scalar request/response state (`workspace_live.ex:88-90`) and cannot route two direct instances independently.
- `trace_resource_block/1` has a second latent duplicate-ID contract: with default `content_id=nil`, the outer container and inner `pre` both use `@id` at `llm_trace_components.ex:297-300`. The current parent avoids it by supplying generated content IDs, but the reusable helper is unsafe by itself.
- Disclosure buttons expose `aria-expanded` without `aria-controls`. The reusable contract documentation describes resources but not complete summary/details/event/hook/style ownership.
- Existing output component/projection tests reference `WEB-TEST-036`, while the frontend specification assigns that ID to cursor pagination. This work needs the correct existing output-control ID or a new canonical test ID.

Reusable boundary and invariants
- The pure projection owns semantic view data only: metric kind, value, state, title, and spoken label.
- The component owns all HTML IDs, classes, `data-role`/`data-metric`, ARIA wiring, and descendant structure, namespaced under required root `@id`.
- The host LiveView owns state. Every host-directed event identifies both widget instance and action/resource so state can be keyed by instance.
- Browser hooks own browser-only effects such as clipboard and must be element-local with unique hook element IDs.
- Component CSS is rooted beneath `.llm-trace-item`; host theming uses documented custom properties rather than relying on accidental global selectors.
- OpenAPI, Go, PostgreSQL, Garage, and telemetry do not own DOM identity and require no change for this KER.

Target architecture
1) Change `LlmTraceProjection.summary/1` to return semantic metric fields such as `kind`, `value`, `state`, `title`, and `aria_label`; remove `id`, CSS class, role, and `data_*` keys.
2) In `llm_trace/1`, derive every required descendant ID from `@id`. The cache metric needs no element ID; expose `data-metric="cache"` and select it under the widget root.
3) Add `phx-value-widget-id={@id}` to details/request/response events. The host validates the instance and keeps per-widget disclosure/resource state while preserving the existing persisted output-details preference.
4) Add `aria-controls` to each disclosure button and ensure the target exists. Fix `trace_resource_block/1` by deriving a distinct content ID or omitting the inner ID.
5) Scope trace styles under `.llm-trace-item`, define component defaults at that root, and preserve the existing same-row wrapping and unboxed cache icon.
6) Keep this as a reusable Phoenix projection/component boundary. Do not create a Hex/npm/web-component package until a real second application supplies concrete framework and distribution constraints.

Detailed implementation sequence
1) Add a canonical frontend test record for multi-instance trace identity, event isolation, disclosure ARIA, and helper uniqueness; correct stale test-ID annotations.
2) Add a static/pure projection test that rejects DOM-specific keys and preserves semantic cache state.
3) Refactor `LlmTraceProjection.summary/1` and document/typespec the complete summary/details/resources view model.
4) Refactor `LlmTraceComponents.llm_trace/1` to own selectors and IDs; remove unnecessary granular ID overrides after every caller moves to the root-owned scheme.
5) Fix `trace_resource_block/1` default identity and add `aria-controls`/controlled panel IDs.
6) Add widget ID to event payloads and update `WorkspaceLive` handlers/state to route only known instances. Avoid separate legacy event paths.
7) Root-scope the relevant `.ullm-*`, `.trace-*`, and resource styles. Retain documented CSS custom properties and the Clipboard hook requirement.
8) Replace `#run-cache-status` and other global/fixed test selectors with `#<widget-id> [data-metric="cache"]` or root-owned control IDs in LiveView, browser, and deployed canary tests.
9) Update component documentation and the parity inventory with the full host contract and extraction boundary.

Test and production certification matrix
- T0: projection emits no DOM identity/style keys; static checks reject `run-cache-status`; CSS selectors are rooted and required semantic fields are documented.
- T1: render two widgets, assert every nonempty ID is globally unique, all `aria-controls` targets exist, helper IDs are safe by default, and details/request/response events mutate only the addressed instance.
- T2: no new test unless clipboard decision logic changes; do not add Happy DOM or jsdom.
- T3: not applicable because API and persistence are unchanged.
- T4: update the existing authenticated canary for root/data selectors and retain native Clipboard, LiveSocket patching, focus, wrapping, and computed unboxed-cache-icon checks. Do not add a redundant ordinary canary.
- T5: `make test-release`, exact pushed web image, release/health identity, authenticated deployed canary, and no browser hook/LiveView errors.

Cross-KER coordination and exit criteria
- Coordinate changes to `LlmTraceProjection` and `LlmTraceComponents` with execution identity, token semantics, cost certainty, and stats availability in one frontend contract cut. Storage KERs do not block this work.
- Migrate selectors atomically; do not leave compatibility IDs or a second event API.
- This KER closes when two instances have unique DOM/ARIA identity, independent state/events, root-scoped stable selectors/styles, and the exact deployed widget preserves the approved layout and unboxed cache icon.
