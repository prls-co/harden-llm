# Harden-LLM Widget Utility-Informed Architecture Decisions

This document requests decisions needed before implementing the utility-informed
widget plan. Utility-llm is a read-only capability and failure-mode reference,
not an implementation or pixel-copy target. The current repository baseline is
the Phoenix LiveView frontend, the Go REST gateway, `api/openapi.yaml`, and the
existing resource-aware test tiers.

## Terminology

- **Utility-informed parity** means considering the reference widget's useful
  observable behaviors, failure modes, accessibility, and embedding lessons.
  It does not require copying every utility-llm feature, React/Downshift
  implementation detail, Firebase integration, provider call, or pixel value.
- **Widget draft** means the component-local profile projection held by
  `ProfileWidgetComponent` while a user edits fields. The relevant persisted and
  runtime boundaries are `POST /api/v1/state` and `POST /api/v1/run`.
- **Host application** means the page or embedding application that instantiates
  `ProfileWidgetComponent`, supplies profiles and model options, and receives
  committed widget events. In this repository, `WorkspaceLive` and
  `EmbeddingLive` are hosts. A future prompt playground, agent editor, or
  administration screen could also be a host.
- **Model refresh** means querying the provider's model-list operation through
  `POST /api/v1/profiles/{profileID}/models:refresh` using the saved profile
  and stored credential. It does not execute a prompt or mutate profile
  configuration. The current Phoenix client is
  `HardenAPI.refresh_profile_models/2`.
- **Save-before-run gate** means the current behavior in the LiveView host that
  prevents `/api/v1/run` from using endpoint, credential, fallback, inference,
  or identity changes until the profile is saved.
- **Staged credential** means a replacement key held transiently only while
  processing an explicit Save operation. It is never used as a general draft
  input for model refresh or Run, and must not be rendered, logged, persisted
  as widget state, or sent to browser JavaScript.
- **Model catalog** means the model-option list owned and supplied by the host
  application. Each option has a required canonical `id` and an optional
  display `label`. Harden-LLM defaults are used only when the host supplies no
  catalog; provider discovery belongs to the host/backend integration.
- **Targeted browser canary** means the existing `widget_canary_test.exs` native
  browser boundary for focus, LiveSocket patching, file input, responsive
  overflow, and multi-instance behavior. It is separate from cheap LiveView,
  pure Elixir, Node, and static tests.
- **Deployed canary** means `node scripts/run-deployed-browser-test.mjs`, which
  checks merged/image identity, health probes, authenticated browser behavior,
  and history cleanup against the deployed runtime.

## 1.1 Question

Should model refresh use only saved profiles, or should the system add an
unsaved draft-refresh API?

## 1.2 Context & clarification

The current refresh path is body-free and uses the saved profile and stored
credential. Utility-llm shows that refreshing edited endpoint configuration can
be convenient, but that capability is not essential to a robust reusable
widget. Adding it would create a second request mode, transient credential
rules, and more state combinations.

The key invariant is:

```text
save(draft) -> persisted profile
refresh(profile_id) -> model list from the persisted profile
refresh(profile_id) != save(profile, credential, workspace state)
```

The UI can make the boundary explicit by requiring Save before Refresh Models
when execution-affecting fields are dirty. Provider rate limits and provider
failure behavior remain external contracts, but they do not require a second
draft request contract.

## 1.3 Options

- `Option A`: Required validated draft-refresh contract
  - **Rubrics**: `Conf:60% | Invest:i | Blast:i | Reversal:i | Fit:ii | Reuse:i | Obs:i | Surface:iii | Perf:ii`
  - **Approach**: Extend `api/openapi.yaml`, the Go gateway route/service, and `HardenAPI` so an optional draft profile and write-only credential can be validated and probed without persistence. Make the widget's Refresh Models action use the current draft.
  - **Example**: `HardenAPI.refresh_profile_models/3` sends a canonical draft body; the gateway validates profile identity, endpoint origin, credential scope, and body size, then returns models without updating the saved profile.
  - **Architecture**: Adds one explicit request-body variant at the existing OpenAPI/Go/Phoenix boundary. `ProfileWidgetComponent` remains server-owned and does not call providers directly.
  - **SSoT**: The OpenAPI request schema is the contract; Go owns validation and provider probing; Phoenix owns draft projection and error display. No second persistence schema is introduced.
  - **System limits**: Request validation is linear in draft JSON size. Provider rate limits, provider timeout behavior, and credential-origin policy are `Unknown - not available in local context.`
  - **Trade-offs**: Highest utility parity and best support for edited endpoints, but it commits a broader API contract and requires the most security and integration evidence.

- `Option B`: Optional validated draft refresh with saved-only fallback
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Reuse:i | Obs:i | Surface:ii | Perf:i`
  - **Approach**: Implement the draft body only if the gateway can prove non-persistence and credential compatibility. Preserve the existing body-free saved-profile behavior as the accepted path when the body is absent or the safety contract is not available.
  - **Example**: A saved profile refresh continues to work unchanged; an edited endpoint refresh uses the draft only after local validation and returns a structured error if safe probing is unavailable.
  - **Architecture**: Extends the existing operation without creating a parallel endpoint. The optionality is at the request boundary, not duplicated widget logic.
  - **SSoT**: OpenAPI and Go remain authoritative for accepted draft fields; `HardenAPI` sends one canonical body; the saved profile remains the fallback source when no draft is supplied.
  - **System limits**: Request processing remains linear in draft JSON size. Provider rate limits, timeout behavior, and credential-origin policy are `Unknown - not available in local context.`
  - **Trade-offs**: Preserves production reliability and limits API commitment while allowing parity where verified. Some utility behavior may remain intentionally adapted.

- `Option C`: Keep saved-profile-only refresh
  - **Rubrics**: `Conf:90% | Invest:iii | Blast:iii | Reversal:iii | Fit:i | Reuse:ii | Obs:i | Surface:i | Perf:i`
  - **Approach**: Keep the current ID-only refresh contract and make the UI explicit that edited endpoint changes require Save before Refresh Models.
  - **Example**: `HardenAPI.refresh_profile_models/2` remains unchanged; the component disables or explains Refresh Models while the draft is dirty.
  - **Architecture**: Uses the current OpenAPI, Go gateway, credential, and persistence boundaries exactly.
  - **SSoT**: The saved profile is the sole refresh input; no draft request representation is added.
  - **System limits**: Uses the current gateway and provider limits. New draft-body limits do not apply; external provider limits are `Unknown - not available in local context.`
  - **Trade-offs**: Lowest implementation and security risk, but it leaves a practical difference from utility-llm and makes edited endpoint workflows less convenient.

## 1.4 Recommendation

**Decision: Option C — Keep saved-profile-only refresh.**

Treat saved-profile-only refresh as the canonical contract,
not merely a fallback. This keeps Save as the single configuration commit
boundary and avoids draft request schemas, transient refresh credentials, and
duplicated API/test paths. Revisit draft refresh only as a separately scoped
product feature if real users demonstrate that Save-before-Refresh is
insufficient.

## 2.1 Question

Should Run continue to require a saved profile, or should the system support
running the current unsaved widget draft?

## 2.2 Context & clarification

The current save-before-run gate protects the `/api/v1/run` contract: the
runtime executes a saved profile, while the widget may contain unsaved endpoint,
credential, fallback, inference, or identity changes. Utility's local draft
behavior makes editing feel immediate, but it does not by itself define a safe
server-side execution contract for this application.

An **ephemeral draft run** would mean a new, explicitly validated API boundary
that accepts the current draft for one run without persisting it. It must define
credential handling, auditability, retries, ambiguous outcomes, and history
semantics. It cannot be implemented merely by bypassing the existing dirty
check in `WorkspaceLive`.

## 2.3 Options

- `Option A`: Add a separate validated ephemeral-draft run contract
  - **Rubrics**: `Conf:60% | Invest:i | Blast:i | Reversal:i | Fit:ii | Reuse:i | Obs:i | Surface:iii | Perf:ii`
  - **Approach**: Define a separate OpenAPI operation or explicit run mode for a current draft. Validate the full draft and credential boundary, mark the run as draft-derived, and preserve the existing saved-profile run path.
  - **Example**: Run submits a draft snapshot with an explicit non-persisting mode; the response/history records that the run used an unsaved draft and the browser never receives provider credentials.
  - **Architecture**: Adds a stable external boundary while keeping `ProfileWidgetComponent` and `WorkspaceLive` as orchestrators. It requires Go runtime, audit, retry, and ambiguous-outcome changes.
  - **SSoT**: OpenAPI defines the draft-run contract; Go validates and executes it; Phoenix owns the draft snapshot. Saved profile state remains separate.
  - **System limits**: Draft payload size and run attempt limits must reuse existing gateway bounds where available. Provider rate limits, billing, and history semantics are `Unknown - not available in local context.`
  - **Trade-offs**: Best long-term UX and clear semantics, but it is a significant public contract and increases security, observability, and test scope.

- `Option B`: Retain the save-before-run gate
  - **Rubrics**: `Conf:90% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Reuse:ii | Obs:i | Surface:i | Perf:i`
  - **Approach**: Keep Run blocked while execution-affecting fields are dirty. Make the dirty state, required Save action, and saved-versus-draft distinction explicit in the widget.
  - **Example**: Editing Base URL shows a dirty/save message; Save commits the profile; Run then uses the saved profile through the existing `/api/v1/run` path.
  - **Architecture**: Fits `WorkspaceLive`, `ProfilesLive`, `HardenAPI`, and the current Go run contract without a new execution mode.
  - **SSoT**: Saved profile state remains the only run input; `ProfileWidgetComponent` owns draft state and reports committed changes to the host.
  - **System limits**: Uses existing run payload, retry, timeout, and provider limits. Exact provider limits are `Unknown - not available in local context.`
  - **Trade-offs**: Strongest correctness and security with the smallest surface, but it intentionally differs from a fully ephemeral utility-style workflow.

## 2.4 Recommendation

**Decision: Option B — Retain the save-before-run gate.**

The current runtime contract and ambiguous-run behavior are
already production-sensitive. A draft-run API should be a separate product
decision with its own requirements and tests, not an implicit consequence of
widget parity.

## 3.1 Question

May a staged replacement credential be used for draft model refresh, or must
refresh use only a stored credential?

## 3.2 Context & clarification

The widget must remain write-only: a key typed into the credential control may
be used at a server boundary but must not enter rendered HTML, LiveView assigns
that render, logs, telemetry, fixtures, or persisted draft state.

The relevant current boundary is the credential handling in
`ProfileWidgetComponent`, `HardenAPI`, and the Go gateway. The important
distinction is between **staging** a key for the explicit Save operation and
sending a credential to a provider read operation. With saved-profile-only
model refresh, there is no reason to add a transient credential to the refresh
contract.

## 3.3 Options

- `Option A`: Ephemeral server-side staged credential for refresh
  - **Rubrics**: `Conf:70% | Invest:i | Blast:ii | Reversal:ii | Fit:i | Reuse:i | Obs:i | Surface:ii | Perf:ii`
  - **Approach**: Permit the staged replacement key in the draft refresh request only. Validate endpoint origin and credential scope at the Go boundary, use it for the provider probe, then discard it.
  - **Example**: Refresh submits `{draft, credential}` through `HardenAPI`; the Go service uses the credential transiently and returns models, while the response, rendered state, and logs contain only status/error classifications.
  - **Architecture**: Uses the existing Phoenix-to-Go ownership boundary and adds no browser provider call or persistence shortcut.
  - **SSoT**: The server request is the sole location where the staged key exists outside the input event; the credential store remains authoritative for saved credentials.
  - **System limits**: One refresh consumes one provider request and must obey existing request-size and timeout bounds. Provider throttling and billing behavior are `Unknown - not available in local context.`
  - **Trade-offs**: Closest practical utility behavior with strong security if redaction and disposal are verified; requires careful secret-path tests.

- `Option B`: Stored credential only
  - **Rubrics**: `Conf:85% | Invest:ii | Blast:i | Reversal:i | Fit:i | Reuse:ii | Obs:ii | Surface:i | Perf:i`
  - **Approach**: Allow draft endpoint refresh only when it can use the already stored credential. A replacement key becomes usable only after Save.
  - **Example**: A dirty Base URL can refresh with the existing stored key if origin policy permits; a newly staged key produces an actionable “save credential first” message.
  - **Architecture**: Reuses the existing credential vault and avoids adding a transient secret field to the draft-refresh contract.
  - **SSoT**: The backend credential store is the only provider credential source; the widget never sends a newly typed key to refresh.
  - **System limits**: No additional secret lifetime is introduced. Provider limits and whether stored credentials may safely cross to a changed endpoint are `Unknown - not available in local context.`
  - **Trade-offs**: Simplest security model, but it makes newly configured providers require an extra save before discovery.

- `Option C`: Require Save before any model refresh
  - **Rubrics**: `Conf:95% | Invest:iii | Blast:iii | Reversal:iii | Fit:i | Reuse:iii | Obs:i | Surface:i | Perf:i`
  - **Approach**: Disable Refresh Models whenever endpoint or credential state is dirty. Refresh always uses the saved profile and stored credential.
  - **Example**: The widget shows Save Profile before Refresh Models after any Base URL or key change.
  - **Architecture**: Uses the current ID-only refresh operation without a transient credential path.
  - **SSoT**: Saved profile and credential state are the only inputs.
  - **System limits**: Existing provider and gateway limits apply; no new request body or secret lifetime exists. External limits are `Unknown - not available in local context.`
  - **Trade-offs**: Most conservative and easiest to audit, but least convenient and least utility-compatible.

## 3.4 Recommendation

**Decision: Option C — Require Save before any model refresh.**

Question 1 establishes that Refresh Models uses only the saved
profile and stored credential, so Options A and B are deliberately out of
scope. A replacement key may be held transiently while Save writes it through
the existing credential boundary, but it must not become a reusable draft
provider input. This gives the credential one lifecycle and avoids a second
secret-handling path.

## 4.1 Question

Should the host own the model catalog, with Harden-LLM defaults for standalone
use, or should the widget/provider discovery own it?

## 4.2 Context & clarification

The current widget has a narrower model list than utility. The useful design
lesson is to preserve the selected model and make host embedding possible, not
to create a feature-rich catalog service. A **canonical model ID** is the value
persisted and sent to the provider; an optional label is display-only.

The relevant integration surfaces are `WorkspaceLive`, `EmbeddingLive`, and
`ProfileWidgetComponent`. The minimal host payload should be a list of
`{id, label?}` options. When the host supplies a catalog, it is authoritative
and the widget must not silently merge in Harden-LLM defaults. When no catalog
is supplied, the widget may use Harden-LLM's default preset. Provider discovery,
if needed, is performed by the host/backend and then passed to the widget.

For example:

- `WorkspaceLive` can pass the model options available to the prompt workspace
  and receive the selected model when the user commits it.
- `EmbeddingLive` can pass the same catalog to each independently prefixed
  widget instance without sharing fold, search, or draft state.
- A future agent editor can pass curated options such as
  `[{id: "gpt-5.6-luna", label: "CPA Luna"}, {id: "gpt-5.5", label: "GPT-5.5"}]`
  without knowing the widget's internal combobox implementation.

The host supplies context and curated choices; it does not call the provider
or decide whether a model is valid at execution time. The gateway/provider
boundary remains authoritative for execution.

## 4.3 Options

- `Option A`: Host-owned catalog with Harden-LLM defaults fallback
  - **Rubrics**: `Conf:95% | Invest:i | Blast:i | Reversal:ii | Fit:i | Reuse:i | Obs:i | Surface:ii | Perf:i`
  - **Approach**: Accept one host-to-widget option shape with required `id` and optional `label`. Use the host catalog as authoritative when supplied; use the Harden-LLM default preset only when no host catalog is supplied. Retain a current selected ID as an unlisted/custom value rather than silently replacing it.
  - **Example**: `WorkspaceLive` passes `[{id: "gpt-5.6-luna", label: "CPA Luna"}]`; the widget shows that catalog only. A standalone instance with no catalog uses Harden-LLM defaults. If the saved profile selects an omitted ID, that value remains visible as unlisted.
  - **Architecture**: Keeps provider discovery and application policy in the host/backend while the reusable component handles display, search, selection, and draft state. Primary and escalation widgets receive independent host catalogs and state.
  - **SSoT**: The host catalog is authoritative for selectable options; Harden-LLM defaults are only a no-host fallback; persisted profile fields contain only the canonical model ID; the provider validates the ID at execution time.
  - **System limits**: Widget normalization is O(n) over the host catalog and O(1) additional state for the current value. Host catalog size, default preset size, and provider model-list limits are `Unknown - not available in local context.`
  - **Trade-offs**: Smallest reusable contract and clearest ownership, but each host that needs live provider discovery must implement or call its own discovery path.

- `Option B`: Provider discovery authoritative, host catalog as fallback metadata
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:i | Reversal:i | Fit:i | Reuse:ii | Obs:ii | Surface:i | Perf:i`
  - **Approach**: Use provider-discovered IDs as the option set; use host catalog entries only to label or supplement IDs not returned by discovery. Preserve the current custom ID.
  - **Example**: If discovery returns `gpt-5.6-luna`, that entry wins; the host may add a label but cannot add an unrelated selectable ID unless discovery is unavailable.
  - **Architecture**: Requires less new host contract while retaining the existing provider refresh as the primary source.
  - **SSoT**: Provider discovery owns selectable values; host data owns optional labels; the widget state helper owns deterministic conflict resolution.
  - **System limits**: Merge remains O(n) in the combined list. Provider catalog size and host list size are `Unknown - not available in local context.`
  - **Trade-offs**: Good provider correctness and smaller API surface, but host applications cannot reliably expose curated models unavailable from the provider probe.

- `Option C`: Current profile model only, with discovered values replacing it after refresh
  - **Rubrics**: `Conf:90% | Invest:iii | Blast:iii | Reversal:iii | Fit:i | Reuse:iii | Obs:ii | Surface:i | Perf:i`
  - **Approach**: Keep the current model list behavior and add only the minimum custom-value retention needed to avoid losing the active selection.
  - **Example**: Refresh replaces the list with provider models; the current custom ID is appended if absent; host labels and aliases are not supported.
  - **Architecture**: Leaves `WorkspaceLive`, `EmbeddingLive`, and the component with the smallest change.
  - **SSoT**: The profile's discovered model list is authoritative.
  - **System limits**: O(n) over the provider list and constant additional state for the current value. External catalog limits are `Unknown - not available in local context.`
  - **Trade-offs**: Lowest change risk but leaves an important practical difference from utility and limits embedding reuse.

## 4.4 Recommendation

**Decision: Option A — Host-owned catalog with Harden-LLM defaults fallback.**

Use `{id, label?}` and a single host-owned catalog contract. Do not merge host
options with Harden-LLM defaults: host input is authoritative, while defaults
exist only for standalone or no-catalog use. Retain an omitted current value as
an unlisted/custom selection to prevent data loss. Do not add aliases, source
metadata, ranking, or a separate catalog service until an actual embedding
consumer requires them. The provider validates the model at execution time;
the widget's catalog is a selection aid, not a second provider-authority
system.

## 5.1 Question

What level of visual parity should determine acceptance: pixel-level identity,
structural/semantic parity, or behavior-only parity?

## 5.2 Context & clarification

Utility's reference implementation uses React-oriented markup and CSS, while
Harden-LLM uses Phoenix LiveView and existing Phoenix assets. The plan already
targets utility field classes, labels, hints, errors, fold structure, ARIA
relationships, spacing hierarchy, and scoped selectors in
`ProfileWidgetComponent` and `frontend/assets/css/app.css`.

There is no current repository-wide pixel snapshot contract for this widget.
Native browser coverage exists for selected boundaries, but a full screenshot
matrix would substantially increase Chromium cost and maintenance.

## 5.3 Options

- `Option A`: Pixel-level visual snapshots
  - **Rubrics**: `Conf:60% | Invest:i | Blast:i | Reversal:i | Fit:ii | Reuse:ii | Obs:ii | Surface:iii | Perf:iii`
  - **Approach**: Establish deterministic screenshots at agreed viewport/profile/fold states and fail on approved visual diffs.
  - **Example**: A browser snapshot covers compact, API, options, retry, pricing, escalation, mobile, and two-instance states.
  - **Architecture**: Adds a visual-regression artifact and browser baseline on top of the existing Wallaby canary.
  - **SSoT**: Approved screenshot baselines plus the utility source define visual acceptance; LiveView tests still own state semantics.
  - **System limits**: Browser execution and artifact storage scale with viewport/state count. Exact screenshot runtime, image size, and acceptable diff thresholds are `Unknown - not available in local context.`
  - **Trade-offs**: Strongest visual proof, but high resource cost, brittle diffs, and greater maintenance surface.

- `Option B`: Structural, semantic, and utility-derived visual parity
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Reuse:i | Obs:i | Surface:ii | Perf:ii`
  - **Approach**: Require exact control order, labels, ARIA, fold topology, field primitives, spacing hierarchy, scoped CSS, and targeted browser checks; accept framework-specific pixel adaptation where semantics and practical appearance remain equivalent.
  - **Example**: LiveView assertions verify labels/classes/ARIA and the browser canary verifies focus, patching, overflow, and two instances; no full screenshot matrix is required.
  - **Architecture**: Fits the existing LiveView and test hierarchy without introducing a visual test framework.
  - **SSoT**: Utility source establishes the observable contract; Phoenix markup/CSS implements it; focused tests define deterministic acceptance.
  - **System limits**: Cheap suites remain CPU-bound; one browser canary uses the existing T4 resource class and 600-second task timeout. Exact host contention remains measurable through the existing benchmark harness.
  - **Trade-offs**: Strong practical parity with controlled cost, while allowing small rendering differences caused by Phoenix's component system.

- `Option C`: Behavior-only parity
  - **Rubrics**: `Conf:90% | Invest:iii | Blast:iii | Reversal:iii | Fit:i | Reuse:iii | Obs:ii | Surface:i | Perf:i`
  - **Approach**: Accept matching state transitions and payload semantics while treating markup, spacing, labels, and visual hierarchy as secondary.
  - **Example**: Tests verify cache values and fold events but do not require utility field classes, exact labels, or visual spacing.
  - **Architecture**: Uses the cheapest existing LiveView tests and minimizes CSS changes.
  - **SSoT**: Server state and API payloads are authoritative; utility markup is reference-only.
  - **System limits**: Lowest browser and artifact cost; visual regression risk is `Unknown - not available in local context.`
  - **Trade-offs**: Easy to maintain but does not satisfy the stated goal that the reusable widget should look and function like utility.

## 5.4 Recommendation

**Decision: Option B — Structural, semantic, and utility-derived visual parity.**

The user-visible widget must be practically recognizable and
accessible, but a full screenshot matrix would spend scarce Chromium resources
on fragile evidence. Exact semantics, DOM contracts, scoped styles, and one
native browser boundary provide the best correctness-to-cost balance.

## 6.1 Question

Where should the targeted browser canary run in the development and release
hierarchy?

## 6.2 Context & clarification

Chromium means the existing Wallaby/native browser task, not every LiveView
test. The current repository separates resource classes in
`test/test-tiers.json` and delegates selection through
`scripts/run-test-tier.mjs`. The browser task uses a pinned image, host
networking, Docker, and 2 GiB shared memory; it is materially more expensive
than LiveViewTest or Node tests.

The question is about scheduling and ownership, not whether native browser
behavior matters. The proposed browser-owned boundaries are focus, LiveSocket
patching, native file input, secret staging, responsive overflow, and
multi-instance isolation.

## 6.3 Options

- `Option A`: Dedicated targeted browser tier with CI/release execution
  - **Rubrics**: `Conf:90% | Invest:i | Blast:ii | Reversal:ii | Fit:i | Reuse:i | Obs:i | Surface:ii | Perf:i`
  - **Approach**: Keep one focused `widget_canary_test.exs` in the browser tier. Cheap tests run on every iteration; the browser canary runs in CI/release or when explicitly requested. The deployed canary remains release-only.
  - **Example**: `make test-fast` never starts Chromium; `make test-browser` runs the targeted native boundary; `node scripts/run-deployed-browser-test.mjs` runs only after deployment.
  - **Architecture**: Matches the existing tier runner, resource classes, pinned browser image, and cleanup ownership.
  - **SSoT**: `test/test-tiers.json` owns classification; the browser canary owns native behavior; LiveView/Node tests own server and pure decisions.
  - **System limits**: Existing T4 browser task timeout is 600 seconds, with 2 GiB shared memory and one browser resource slot. Exact concurrent host capacity is measured by the benchmark harness.
  - **Trade-offs**: Best balance of coverage and iteration speed; developers must understand that cheap green does not certify native browser behavior.

- `Option B`: Opt-in browser canary for widget changes
  - **Rubrics**: `Conf:85% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Reuse:ii | Obs:ii | Surface:ii | Perf:ii`
  - **Approach**: Run the targeted browser canary locally when widget markup, hooks, CSS, or file/focus behavior changes, while CI runs it for all related changes.
  - **Example**: A developer changes `app.js` and explicitly runs `make test-browser`; a pure `ProfileWidgetState` change uses only the cheap suites locally.
  - **Architecture**: Uses the same tier runner but relies more on path-based developer judgment.
  - **SSoT**: The manifest still owns classification; the developer decides when the expensive boundary is relevant.
  - **System limits**: Same browser task limits, but local resource contention becomes developer-dependent. Exact local wall time under concurrent work is `Unknown - not available in local context.`
  - **Trade-offs**: Faster for isolated server-state edits and more immediate for browser edits, but human classification can miss native regressions.

- `Option C`: Release-only browser validation
  - **Rubrics**: `Conf:75% | Invest:iii | Blast:iii | Reversal:iii | Fit:ii | Reuse:iii | Obs:iii | Surface:i | Perf:iii`
  - **Approach**: Keep all local and ordinary CI checks browser-free; run the targeted and deployed canaries only during release certification.
  - **Example**: A CSS or hook regression is discovered only when the release browser gate runs.
  - **Architecture**: Removes frequent Chromium contention but weakens the current targeted browser boundary in normal development.
  - **SSoT**: LiveView/static tests define most behavior; release browser tests are the only native source of truth.
  - **System limits**: Lowest iteration resource use, but defect discovery latency is bounded by release frequency. Exact release queue time is `Unknown - not available in local context.`
  - **Trade-offs**: Cheapest local workflow, but too much browser-owned risk accumulates behind the release gate.

## 6.4 Recommendation

**Decision: Option A — Dedicated targeted browser tier with CI/release execution.**

It gives the project a clear hierarchy: cheap deterministic
tests guide coding, one targeted browser test proves native boundaries, and the
deployed canary certifies the actual runtime. It also avoids requiring
Chromium for every test or every local edit.

## 7.1 Question

Should the plan use the existing direct merge/deploy workflow, introduce a
staged promotion step, or stop after implementation and local verification?

## 7.2 Context & clarification

The current release surfaces include the repository's main branch, the existing
Compose overlay in `deploy/frontend/compose.frontend.yml`, and the deployed
launcher `scripts/run-deployed-browser-test.mjs`. A **release identity** is the
relationship between the merged Git SHA, the running OCI image label/digest,
and the public runtime that the authenticated canary exercises.

The uncertainty is operational rather than architectural: the exact production
credentials, hosted availability, and deployment state are external runtime
conditions. A health probe alone does not prove that the new widget is serving.

## 7.3 Options

- `Option A`: Immutable-SHA staged promotion
  - **Rubrics**: `Conf:70% | Invest:i | Blast:ii | Reversal:ii | Fit:ii | Reuse:i | Obs:i | Surface:ii | Perf:ii`
  - **Approach**: Build and label an immutable image from the merged SHA, deploy it to a staging or verification target, run the deployed canary, then promote the same image identity to production.
  - **Example**: The staging and production runtime both report the same OCI revision label; promotion does not rebuild from a different checkout.
  - **Architecture**: Adds an operational promotion boundary around the existing Compose and deployed-canary paths.
  - **SSoT**: Merged Git SHA and image revision are the release identity; deployment metadata and canary evidence are the audit record.
  - **System limits**: Image build time, staging capacity, and promotion timing are `Unknown - not available in local context.` Public provider/API rate limits are also `Unknown - not available in local context.`
  - **Trade-offs**: Strongest release safety and rollback clarity, but requires a staging target and additional operational coordination.

- `Option B`: Direct merge, deploy, and certify current production
  - **Rubrics**: `Conf:85% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Reuse:ii | Obs:i | Surface:i | Perf:i`
  - **Approach**: Push and merge the completed branch through the established main-branch process, deploy the merged source with the current Compose overlay, and run the deployed canary against the actual public runtime.
  - **Example**: `docker compose ... up -d --build --wait --wait-timeout 300` is followed by `node scripts/run-deployed-browser-test.mjs`; completion requires SHA/image/public behavior agreement.
  - **Architecture**: Uses existing repository release commands, deployment files, health probes, and authenticated canary without a new staging abstraction.
  - **SSoT**: Merged SHA, OCI revision label, container identity, and deployed canary evidence jointly define release truth.
  - **System limits**: Existing Compose and deployed task limits apply; hosted capacity, credential validity, and public route behavior are `Unknown - not available in local context.`
  - **Trade-offs**: Fits the requested operating workflow and minimizes new infrastructure, but production is the first full environment boundary.

- `Option C`: Implementation and local verification only
  - **Rubrics**: `Conf:90% | Invest:iii | Blast:iii | Reversal:iii | Fit:ii | Reuse:iii | Obs:iii | Surface:i | Perf:i`
  - **Approach**: Complete code, deterministic tests, and local release gates, leaving merge and deployment to a later operational task.
  - **Example**: The plan closes with a clean local checkout and no claim about the public `/workspace` runtime.
  - **Architecture**: Keeps deployment outside the implementation plan but leaves the actual user-visible boundary uncertified.
  - **SSoT**: Local source and tests are authoritative only for local behavior; no deployed identity exists.
  - **System limits**: Avoids hosted runtime limits, but production confidence is `Unknown - not available in local context.`
  - **Trade-offs**: Lowest operational risk during development, but does not satisfy a production-completion objective.

## 7.4 Recommendation

**Decision: Option A — Immutable-SHA staged promotion.**

Build and label an immutable artifact from the merged SHA, validate that exact
artifact on an isolated verification/staging target, run the deployed canary,
and promote the same image identity to production without rebuilding. If the
required staging target or immutable artifact transport is unavailable, stop
and record that operational blocker rather than silently reverting to direct
production deployment.
