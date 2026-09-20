# Complete Recursive LLM Profile Widget Implementation Plan

- Plan ID: `PLAN-HLLM-RECURSIVE-WIDGET-001`
- Status: Implemented locally; deterministic certification completed on the
  feature branch. No browser, live-provider, Docker, deployment, or push gate
  was authorized or run.
- Date: 2026-09-20.
- Inspected source: `main`, `de7c859bb60b21ab99913f1d4f03698417dedb04`.
- Audience: An implementing model such as GPT-5.6 Luna. Follow the phases in order; do not substitute a smaller feature for the stated acceptance criteria.
- Scope: Reuse the complete root LLM profile widget for original repair, escalated repair, fresh rerun, and rerun repair, with explicit role restrictions and correct persistence.

## 1. Outcome, authority, and non-goals

The user must see the **same complete widget** at every enabled model target:
the compact LLM/profile/reasoning row, its functional configuration gear, the
full applicable configuration editor, and the existing profile actions. A gear
that opens only explanatory text or recovery checkboxes is not completion.
Sharing only `profile_row/1`, CSS, or individual inputs is not completion either.

The original root must use the same canonical renderer as the nested targets.
Internal subcomponents for options, credentials, or pricing are encouraged;
separate root/repair/rerun implementations of the complete editor are not.

This document refines the UI design in Sections 11 and 12.9 of the
[REST recovery plan](rest-recovery-and-progress-implementation-plan.md).
It replaces that plan's target-only presentation with full-widget presentation.
It does **not** replace its REST schemas, defaults, recovery stages, cache
ownership, progress protocol, or execution limits.

Preserve [ADR-HLLM-024](../docs/adr/ADR-HLLM-024-bounded-recovery-and-progress.md):
UI composition can be recursive; execution remains a finite plan with one
attempt budget and one deadline. Never execute a selected profile's own recovery
policy while using it as a recovery target.

Non-goals:

- No new REST fields, database migration, provider behavior, or retry executor.
- No per-target retry budget, independent rerun search setting, or repair cache.
- No model catalog, pricing, reasoning-map, or CPA deployment changes.
- No new dependency, frontend framework, general form engine, or package publication.
- No changes to diagnostics, timeout thresholds, test purposes, or test runner architecture.
- No production promotion, browser execution, paid inference, or external issue creation authorized by this planning request.

The implementation request was subsequently authorized. This document now
records the completed local implementation and its deterministic evidence;
deployment and production promotion remain outside this task.

## 2. Required reading and current evidence

Before implementation, read these files completely where they contain instructions:

1. [Repository instructions](../AGENTS.md).
2. [LiveView and Go testing guidelines](../docs/liveview-go-testing-guidelines.md).
3. [Widget draft ownership ADR](../docs/adr/ADR-HLLM-016-widget-draft-and-data-contract.md).
4. [Bounded recovery ADR](../docs/adr/ADR-HLLM-024-bounded-recovery-and-progress.md).
5. This plan, including acceptance tests and handoff requirements.

Use the following source map. Line numbers will move; locate the named symbols.

| Owner / file | Current evidence | Intended change |
| --- | --- | --- |
| `frontend/lib/harden_llm_web/live/profile_widget_component.ex` | `render/1` uses full `profile_editor/1` for root; `recovery_target_fields/1` uses a row plus a different `target_profile_config/1` | One complete renderer, invoked for every role, and one stateful owner per top-level widget |
| Same file, `recovery_target_fields/1` and `rerun_plan_fields/1` | `nested_repair_name` appends `[jsonRepair]` to the target name, producing `rerun[target][jsonRepair]` | Explicit independent target and repair-plan bindings |
| `frontend/lib/harden_llm_web/profile_widget_state.ex` | `serialize_recovery_target/1` retains only leaf fields; misplaced nested repair edits are dropped | Validate correct binding and retain only the contract's legitimate leaf data |
| `frontend/lib/harden_llm_web/live/workspace_live.ex` | Widget message handlers ignore `_prefix` and update root state | Route only messages belonging to this top-level instance; child edits remain path-aware inside its owner |
| `frontend/lib/harden_llm_web/live/profiles_live.{ex,html.heex}` | Separate editor markup and handlers; only recovery fields are currently shared | Mount the same complete widget controller/editor inside the existing Profiles page |
| `frontend/lib/harden_llm_web/live/embedding_live.ex` | Two top-level widgets have prefix routing and independent uploads | Extend this existing verification surface to nested roles; preserve host ownership |
| `frontend/lib/harden_llm_web/live/profiles_live.ex` | `profile_form/1`, `profile_payload/1`, and helpers are used by the reusable widget | Move these pure helpers out of the page module without changing their validation |
| `api/openapi.yaml` | `RecoveryTarget`, `RepairPlan`, and `RerunPlan` are finite, distinct shapes | Preserve the public contract; test emitted JSON against it |
| `internal/retry/retry.go`, `internal/runtime/recovery_execute.go` | Leaf targets; shared budgets; rerun inherits original search; repair search is off | No planned execution changes |

These findings are source-inspection evidence, not proof of current deployed
browser behavior. Recheck the source revision and worktree before implementation.

## 3. Fixed UX and capability contract

### 3.1 Complete widget tree

```text
Original generation: complete LLM widget
  Gear
    Existing model / options / profile configuration / actions
    Retries & Repair
      Shared attempt budget, retry categories, backoff
      [ ] LLM JSON repair
        Initial repair: complete LLM widget
          Gear: full applicable configuration; no further recovery
        [ ] Escalated JSON repair
          Escalated repair: same complete LLM widget
            Gear: full applicable configuration; no further recovery
      [ ] Fresh rerun
        Rerun generation: same complete LLM widget
          Gear
            Existing model / options / profile configuration / actions
            Inherited retry-budget summary
            [ ] LLM JSON repair
              Initial repair: same complete LLM widget
              [ ] Escalated JSON repair
                Escalated repair: same complete LLM widget
            No Fresh rerun control
```

Disabled branch checkboxes remain visible at their parent. Their child widgets
are not rendered and must not contribute values to an executable payload.
Opening a gear or selecting a profile must never enable a branch implicitly.

### 3.2 Role matrix

Use exactly three semantic roles, not a growing collection of caller-supplied
`hide_*` booleans. Initial and escalated repair share the `json_repair` role.

| Behavior | `original_generation` | `rerun_generation` | `json_repair` |
| --- | --- | --- | --- |
| Profile picker, reasoning, gear, full applicable editor | Yes | Yes | Yes |
| Model and provider-option overrides | Yes | Yes | Yes |
| Explicit saved-profile actions | Yes | Yes | Yes |
| Configure JSON-repair children | Yes | Yes | No |
| Configure a fresh rerun | Yes | No | No |
| Editable total retry policy | Yes | No: inherited summary | No: inherited summary |
| Web search | Existing run control | Same run control, explicitly labeled shared | Off, unavailable |
| Cache | Existing run control | Inherited generation cache mode, read-only | Not applicable |

The Profiles page has an additional host context, `profile_definition`, rather
than a fourth recovery role. In that context, root edits define a saved profile;
request-only search/cache controls are unavailable because they are not saved
profile fields. It still uses the same complete renderer and capability rules.

Derive capabilities from role and host context in one pure function. Capability
restrictions apply to event handling as well as rendering: forged events must
not turn on repair search, add another rerun, or edit an inherited retry budget.
Use server-owned role descriptors; never trust a role supplied by the browser.

### 3.3 Defaults and inherited targets

- Load the preset from `listProfiles`'s `result.defaults.recoveryPolicy`.
- The existing full preset is original repair CPA Luna L then H, fresh rerun
  CPA Astra L, and rerun repair CPA Astra L then H. Do not duplicate these
  profile names or reasoning maps in new production frontend code.
- Preserve explicit existing settings, including a four-attempt budget, null
  branches, zero delays, and generation-relative migrated targets.
- `{"source":"generation"}` remains exactly that wire value. Render the full
  widget using inherited effective values, mark them inherited, and disable
  per-target overrides until the user explicitly selects a profile target.
- Selecting a concrete profile transitions to `source: "profile"`. Do not
  silently convert inherited targets on mount, disclosure, or save.
- Obtain L/M/H choices from the selected profile's `reasoningEffortMap`.
  Keep portable wire values `lowest`, `middle`, `highest`; do not put provider
  values such as `low` or `max` into the portable field.
- An absent profile remains visibly unresolved with a field error; do not
  substitute a working model to make the configuration appear valid.

## 4. Data ownership and precise bindings

### 4.1 Six fixed nodes, not an arbitrary graph

Paths below are relative to the run/profile document, not to the visual parent.
Use a fixed descriptor table. Do not construct a child's API path by blindly
appending fields to its parent's target path.

| Node ID | Role | Target binding | Child repair binding |
| --- | --- | --- | --- |
| `original` | `original_generation` | Existing root selection/model/options fields | `recoveryPolicy.jsonRepair` |
| `original-repair-initial` | `json_repair` | `recoveryPolicy.jsonRepair.initial` | None |
| `original-repair-escalation` | `json_repair` | `recoveryPolicy.jsonRepair.escalation` | None |
| `rerun` | `rerun_generation` | `recoveryPolicy.rerun.target` | `recoveryPolicy.rerun.jsonRepair` |
| `rerun-repair-initial` | `json_repair` | `recoveryPolicy.rerun.jsonRepair.initial` | None |
| `rerun-repair-escalation` | `json_repair` | `recoveryPolicy.rerun.jsonRepair.escalation` | None |

For example, the existing profile-form adapter must produce:

```text
profile[recoveryPolicy][rerun][target][profileId]
profile[recoveryPolicy][rerun][jsonRepair][initial][profileId]
profile[recoveryPolicy][rerun][jsonRepair][escalation][providerOptions]
```

It must never produce `profile[recoveryPolicy][rerun][target][jsonRepair]`.
An internal per-node form namespace is also acceptable, but its adapter must
emit these exact semantic paths and be exercised through rendered form events.

DOM identity is `(top-level instance prefix, node ID, control ID)`, never just
`profileId`. The same saved profile can occupy several roles simultaneously.
Keep root IDs compatible where practical; namespace all nested input IDs,
labels, combobox listboxes, errors, disclosures, action buttons, and uploads.

### 4.2 State controller and complete renderer

Keep `ProfileWidgetComponent` as the public stateful wrapper, mounted once per
top-level widget. Do not recursively mount its current stateful implementation.
It owns one canonical draft and a finite map of node-local editing state.

The canonical pure renderer is kept beside the stateful wrapper in
`ProfileWidgetComponent` so it can share the existing private field/ID helpers
without a second state owner or a circular component dependency. The public
wrapper still exposes one `profile_widget_node/1` call site for root and target
roles; this is the minimal equivalent of the originally proposed separate
`HardenLlmWeb.ProfileWidgetComponents` module and avoids duplicating the editor.
The implementation deliberately did not add a façade module whose only job
would be delegation.

```elixir
# Intended responsibilities, not code to paste without adapting existing types.
widget_node(assigns)     # Complete row, gear, errors, and complete editor.
profile_editor(assigns)  # Existing complete editor, capability-aware.
repair_plan(assigns)     # Initial + optional escalation; both call widget_node/1.
recovery_plan(assigns)   # Shared budget, original repair, optional rerun node.
```

Both root and rerun use `widget_node/1`. Its editor renders the appropriate
plan, which calls `widget_node/1` for permitted children. Termination comes
from the fixed role matrix, not a configurable depth limit.

Reuse/move the existing row, combobox, options, pricing, and credential markup;
do not recreate visually similar inputs. The renderer has no API calls, process
messages, catalog discovery, persistence, or separately owned copies of values.

Extend `ProfileWidgetState` with pure functions for fixed descriptors,
capabilities, target patches, effective display values, serialization, and node
dirty-state reconciliation. Example function responsibilities:

```elixir
node_descriptor(node_id)                   # Fetch from the fixed allowlist.
capabilities(role, host_context)           # No profile-specific hardcoding.
patch_node(draft, node_id, field_patch)     # Correct target only; validated keys.
effective_node(draft, profiles, node_id)    # Display values, not persisted defaults.
serialize_widget(draft)                    # Existing REST shape, no UI-only fields.
```

Node-local state includes folds, invalid form text, errors, dirty fields,
staged-key state, revision, operation reference, and model refresh result.
Do not store these in `providerOptions`, run state, bundles, or database records.

Child events carry an allowlisted node ID and route to the wrapper. The wrapper
updates the canonical draft and emits root-scoped policy/control changes to its
host. A child selection is not a root `profile_widget_selection` message.
Reject unknown instance prefixes in hosts instead of accepting `_prefix`.
Do not interpret a missing or malformed child binding as an edit to `original`.

Separate catalog notifications from selection notifications. Introduce a
catalog-only `{:profile_widget_catalog, profiles}` payload inside the existing
`{:profile_widget, instance_prefix, payload}` envelope and handle it in all
hosts. A nested Save/Refresh/Import may update that catalog, but must not send
its selected profile ID as the workspace's root selection. Root selection
changes continue through the explicit selection message. Migrate old
`profile_widget_profiles` callers without retaining ambiguous child-selection
semantics.

Preserve ADR-HLLM-016's draft boundary: ordinary profile typing does not issue
`POST /api/v1/state` on every keystroke. Reuse existing explicit commit/run and
ordered state-write behavior. Hosts receive current valid runtime projections
and dirty status, not ownership of all widget form internals. A delayed host
snapshot must not overwrite newer local edits.

### 4.3 Option overrides and explicit shared-profile saves

There are two different persisted objects: the containing run/recovery plan and
the selected saved profile. Keep their drafts and save destinations explicit.

1. Selecting a profile, changing reasoning, or editing model/options changes
   this invocation's draft. For nested nodes, its wire home is that node's
   `RecoveryTarget`, not the original call's options or the catalog entry.
2. Saving the containing configuration persists those target overrides without
   silently saving or mutating the referenced profiles.
3. The existing explicit profile-save action remains available in every
   concrete-profile widget, labeled **Save shared profile** with an explanation
   that it affects other uses. Preserve existing root semantics: this explicit
   action can save explicitly edited model/options as profile defaults as well as
   endpoint, credential, metadata, and pricing edits. Do not perform it on blur.
   Untouched inherited fields retain the saved definition; do not promote a
   display-only effective-value snapshot into new defaults.
4. A nested shared-profile save starts from that selected profile's definition.
   It must preserve that profile's own saved recovery policy, not replace it with
   the containing root policy or rerun's repair subtree. Root saved-profile editing
   can intentionally save the root's own recovery policy.
5. Preserve explicit per-node overrides after a shared-profile save. Other
   nodes inherit changed defaults only for fields they have not overridden.
   Do not reconstruct sibling overrides from their old effective values.
6. Keep New/Import/Export/Delete/Refresh behavior with the same shared-profile
   scope and existing confirmation/validation. New selects the newly saved
   profile only in the initiating node. Do not invent automatic profile copying.

Reuse option parsing/alias handling. Preserve unknown allowed provider options,
explicit false/zero, and valid JSON values. Raw options must decode to an object;
invalid JSON/numbers retain visible input and block commit/run with a node-scoped
error. Do not discard malformed input and execute the previous value silently.

Omitting an override means inherit, not freeze today's default. Clearing a
scalar override removes that key; it does not delete the saved profile default.
Display inherited values and offer a clear reset-to-profile-value action.
Do not introduce a new generic deep merge: the backend's existing provider
option merge behavior remains authoritative.

When the selected profile actually changes, remove the previous node's explicit
model/reasoning/provider overrides and load the new profile's effective defaults.
Explain this reset in the picker help; selecting the same profile is a no-op.
Discard staged credentials and invalidate pending node revisions on that change.
Do not clear overrides on catalog refresh or unrelated host updates.

Forbidden leaf fields remain forbidden: credentials, recovery subtrees, retry
budgets, cache mode, and web-search controls cannot hide inside provider options.
Portable reasoning uses `reasoningEffort`; native reasoning conflicts remain
subject to the existing contract validation rather than a new bypass.

### 4.4 Shared limits, disabled branches, and dirty drafts

- Only the root changes the total attempt budget, retry categories, and backoff.
  Nested summaries say these are shared, not independent budgets.
- The rerun search control edits the same request-level `webSearch` setting as
  root, with an explicit shared-setting label. Do not suggest an independent
  rerun search value or persist one in `RecoveryTarget`.
- Rerun cache mode is inherited and shown read-only. Repairs have no cache action.
- Disable a branch by serializing its canonical value as `null`; remove child
  values from the executable draft. Re-enable from the backend preset, matching
  current toggle behavior. Do not add a hidden persisted disabled subtree.
- Clearing a branch invalidates its local pending revisions and clears staged
  secrets. A late save result may update the shared catalog but must not revive
  the branch or change selection.
- Unsaved endpoint/credential/identity changes or invalid invocation options in
  any enabled node block run, with the responsible role named. Merely closing
  its gear does not make it safe to run. Disabled nodes do not block execution.
- Display configured optional stages truthfully even when the global budget
  may end first; do not increase the budget when a branch is enabled.

### 4.5 Async actions, credentials, and uploads

Keep API operations in the existing stateful wrapper/host through `HardenAPI`.
Each action records node ID, selected profile ID, local revision, and request
reference. A completion may update the saved catalog, but must not overwrite a
newer node draft, apply to another node, or reopen a removed branch.

Allow unrelated profile actions independently. For two nodes editing the same
saved profile within one top-level widget, allow only one outstanding mutation
of that profile; visibly disable a conflicting save/delete/refresh and preserve
both drafts. Do not claim new cross-session concurrency guarantees without
backend support. No distributed locking system is part of this task.

Reuse `SecretStager` and the existing write-only key flow. Nested key staging
must route to its node, never to root. Keys must not appear in ordinary change
events, HTML, parent state messages, exported diagnostics, or form persistence.
Refresh remains a saved-profile-ID-only request; dirty endpoint/credential
drafts require explicit Save first. Never refresh using staged credentials.

Hosts register distinct bundle upload channels for the fixed six roles per
top-level instance. Define a finite mapping with compile-time atoms; never
create atoms from user-supplied IDs. Keep current root upload names compatible.
Pass each node its upload handle and validate `(instance, node)` on import.
Reuse existing encrypted bundle import/export endpoints and size limits.
Imported catalog changes do not implicitly select a profile in other nodes.

No nested HTML forms: renderer functions emit controls, not owning forms.
Hosts retain form/submit ownership and existing `formnovalidate` run semantics.
Scope bundle controls within the existing permitted host form structure.

## 5. File ownership and change limits

| File(s) | Planned responsibility |
| --- | --- |
| `frontend/lib/harden_llm_web/live/profile_widget_component.ex` | Stateful wrapper, canonical draft, node events, API actions; remove duplicate target-only rendering after migration |
| New `frontend/lib/harden_llm_web/components/profile_widget_components.ex` | Complete shared recursive renderer and existing reusable field markup |
| `frontend/lib/harden_llm_web/profile_widget_state.ex` | Fixed roles/bindings, pure edits, effective-value projection and serialization |
| New `frontend/lib/harden_llm_web/profile_form.ex` | Move existing pure saved-profile form/payload functions out of `ProfilesLive`, including their validation helpers |
| `frontend/lib/harden_llm_web/profile_defaults.ex` | Reuse current defaults; change only if extraction needs a pure helper, not to add model presets |
| `frontend/lib/harden_llm_web/live/workspace_live.{ex,html.heex}` | Preserve run/state orchestration, aggregate enabled-node dirty status, enforce prefix routing, supply uploads |
| `frontend/lib/harden_llm_web/live/profiles_live.{ex,html.heex}` | Preserve catalog page, routes, list actions, import/export; mount shared editor and remove duplicate editor handlers |
| `frontend/lib/harden_llm_web/live/embedding_live.ex` | Shared widget host with two independent instance trees and upload routing |
| `frontend/assets/css/app.css` | Only necessary scoped layout adjustments; no separate recovery-editor style system |
| `frontend/assets/js/app.js`, `client_core.mjs` | Change only if existing hook routing requires it; no direct provider calls or new browser state store |
| Existing component/state/workspace/profiles/embedding tests | Extend assertion oracles using deterministic fixtures and public events |
| Frontend specification, REST plan Section 11/P08, new ADR and ADR index | Record full-widget composition without changing the execution contract |

Keep the existing public `ProfileWidgetComponent` entrypoint. If callers need
compatibility for `target_only`, map it to the full `json_repair` role with no
recursive execution; do not retain a second limited renderer. Remove obsolete
attributes only after an `rg` caller inventory and tests demonstrate no use.

Move `ProfilesLive.profile_form/1`, `profile_payload/1`, and related pure helpers
to `ProfileForm`; keep thin delegates during migration where required. Do not
maintain two validation implementations. This is extraction, not a new contract.

## 6. Implementation phases

Every phase follows: add/identify regression, observe the intended failure,
implement, run focused tests, run `make test-fast`, inspect diff, record evidence.
Do not stack multiple unverified phases. Do not push a deliberately failing
test-only checkpoint; keep regression and implementation together when committing.
Register future test groups in P00, but add their executable cases in the owning
phase rather than leaving unimplemented future-phase tests failing. A test group
can gain cases across phases; phase exits below identify the required subset.

### 6.1 P00 — baseline, contract inventory, and test registration

1. Inspect `git status`, current branch/SHA, applicable instructions, and local
   user changes. Use the repository's `dev`/feature-branch policy for implementation;
   do not promote the currently inspected `main` checkout implicitly.
2. Read Section 2 and inspect all callers of `ProfileWidgetComponent`,
   `recovery_fields`, `profile_row`, `profile_editor`, and `target_only`.
3. Run the existing focused frontend suites and `make test-fast`; record exact
   failures and elapsed times. Diagnose baseline failures separately.
4. Recheck identifier collisions. Reserve `WEB-TEST-090` through `WEB-TEST-099`
   for Section 7 in the canonical frontend specification. They were unused at
   the inspected revision. Retain existing relevant test IDs.
5. Add an ADR, using the next available number, recording this finite full-widget
   composition, profile-save scopes, and unchanged REST/execution contract.
   Amend the REST plan's target-only wording and WEB-TEST-084 presentation oracle.
   Retain its forbidden recursive-execution/search/cache assertions.

Exit: exact baseline recorded; current requirements no longer instruct an
implementer to build a separate reduced target editor. No application rewrite yet.

### 6.2 P01 — prove and fix the rerun repair field-path defect

1. Add a component/LiveView regression that opens root gear, enables Fresh rerun,
   opens its gear, enables its JSON repair, changes the initial repair profile
   using the rendered input, and captures the resulting state/save payload.
2. Assert the edit lands at `rerun.jsonRepair.initial.profileId`; assert the
   original repair, rerun target, and escalation remain unchanged.
3. Repeat for escalated rerun repair. Assert no `jsonRepair` key exists inside
   `rerun.target`. Include a save/reload assertion using a stateful private stub.
4. Add pure serialization coverage for both correct paths and the existing leaf
   field whitelist. Do not legalize the incorrect path in the API or serializer.
5. Fix current helper binding by passing the repair-plan form name separately
   from the generation target form name. This small fix precedes extraction.

Exit: WEB-TEST-090 proves a real rendered edit survives serialization and reload.
Other fields remain unchanged; focused tests and the fast gate pass.

### 6.3 P02 — pure role, path, and draft semantics

1. Implement the fixed six-node descriptors and the three-role matrix in
   `ProfileWidgetState`, with table-driven tests before implementation.
2. Implement node-specific patches and validation; distinguish absent fields,
   explicit null branches, valid zero/false, and invalid input text.
3. Implement display-only inheritance and explicit override tracking. Reuse
   existing option parsing and reasoning capability checks.
4. Implement concrete-profile switching/reset and generation-relative targets
   exactly as Section 3.3/4.3 specifies. Test identical-profile no-op separately.
5. Implement enabled-node dirty aggregation and disabled-node invalidation.
6. Extract `ProfileForm` without behavioral changes and update callers/tests.

Exit: WEB-TEST-091/092 and applicable WEB-TEST-083/085/040/042 assertions pass.
No API/schema change and no UI-only state in serialization.

### 6.4 P03 — extract the complete renderer and migrate root first

1. Add characterization coverage for root row, gear, model selection, Options,
   credential staging controls, Pricing, retry controls, and profile actions.
2. Move the existing complete presentation into `ProfileWidgetComponents`.
   Parameterize binding, role, and state; preserve existing root selectors where
   possible. Do not copy the root HTML into a second file and leave both active.
3. Make root `ProfileWidgetComponent.render/1` invoke `widget_node/1`.
4. Keep current event/action behavior working while preparing node ID routing.
5. Check rendered IDs, labels, `aria-controls`/`aria-expanded`, combobox hooks,
   explicit non-submit gear/fold buttons, and no nested forms.

Exit: root behavior is unchanged and now uses the canonical renderer. Existing
root tests plus WEB-TEST-093's root cases pass. Its nested cases belong to P04.
No dependency or browser is required.

### 6.5 P04 — wire every recovery role through that renderer

1. Add tests opening each enabled node's gear and changing Max Output Tokens
   through its actual input; capture its specific target's `providerOptions`.
2. Replace the repair-specific drawer and row-only target rendering with calls
   to `widget_node/1`. Initial and escalated repair share exactly one role.
3. Wire rerun's repair-plan editor inside its gear using explicit Section 4.1
   bindings. Do not render rerun descendants when Fresh rerun is disabled.
4. Route child edits/folds/options to the wrapper's node state. Convert root
   events to node-aware handling where needed; never emit a child root selection.
   Route rendered shared-profile action buttons by node as well; an intermediate
   nested Save/Refresh/Delete must never operate on the root profile. Complete
   their lifecycle and race coverage in P05 before treating the feature as ready.
5. Apply role guards in handlers, not only HTML. Include malicious/unknown path
   events and disabled-branch events in deterministic tests.
6. Show shared budget/search/cache ownership as Section 3.2 specifies. Retain
   root/rerun search synchronization and repair search exclusion.
7. Expose node-scoped invalid-input and unsaved-profile errors; block run from
   enabled invalid nodes without changing global timeouts or budgets.

Exit: WEB-TEST-093/094/095 pass for all six nodes. A nested gear can actually edit
model/options; it is not a message-only drawer. No recovery API shape changes.

### 6.6 P05 — complete nested profile actions and race isolation

1. Add tests for nested shared-profile Save, model Refresh, New, Delete, and
   encrypted bundle import/export routing using existing private API fixtures.
2. Generalize root action handlers to node-aware operation references and
   revisions. Keep one saved-profile payload implementation through `ProfileForm`.
3. Test and implement explicit save scope: invocation-only changes do not save
   the selected catalog profile; explicit shared save does; nested saves never
   replace the selected profile's saved recovery policy with their parent plan.
4. Add delayed-result tests: edit during Save, switch profile during Refresh,
   disable a branch during Save, and edit the same profile in two nodes.
   Use test-controlled messages, not sleeps or increased assertion timeouts.
5. Extend credential staging and fixed upload maps to every role. Test that
   one node cannot consume another node's upload or key; redact all failure data.
6. On successful shared mutations, refresh catalog-derived display values
   without destroying explicit overrides or newer dirty drafts. Surface missing
   references after deletion; do not silently substitute another profile.

Exit: WEB-TEST-096/097 and retained mutation/security tests pass. Every enabled
node has applicable, functioning profile actions—not placeholder buttons.

### 6.7 P06 — migrate all hosts and prove persistence parity

1. Tighten WorkspaceLive prefix handling and enabled-node run/save blocking.
   Preserve its one-in-flight/latest-pending state writer and run orchestration.
2. Mount the same widget on ProfilesLive in `profile_definition` context.
   Preserve `?new=1`, `?edit=...`, catalog streams, page-level import/export,
   error handling, and selected-profile return behavior. Remove duplicated
   editor markup and editor-only recovery handlers after callers migrate.
3. Extend EmbeddingLive's two existing instances to all nested nodes. Keep
   independent drafts/folds/selections/pending actions and upload channels.
   Shared catalog updates are allowed; invocation drafts stay independent.
4. Assert no draft typing sends state/profile writes per keystroke. Test that
   commits and delayed state acknowledgments preserve the newest configuration.
5. Prove edited target model, reasoning, options, and null branch values survive
   containing-profile save/reload, workspace state reload, generated run payload,
   generated cURL, and encrypted bundle import/export through existing boundaries.
   Do not simulate encryption in the UI; use the existing backend bundle tests
   for cryptography and UI tests for the exact submitted/returned policy.
6. Remove obsolete target-only implementation paths after `rg` confirms no
   callers. Update legacy display assertions to the new full-widget requirement,
   preserving their safety assertions rather than deleting the tests.

Exit: WEB-TEST-098/099 and existing workspace/profiles/embedding suites pass.
All three surfaces use one full-widget implementation and equivalent REST data.

### 6.8 P07 — final validation and implementation handoff

1. Run all Section 8 required commands on the final source; fix causes, not
   assertions or budgets. Record failures and investigations if a gate blocks.
2. Inspect the diff for duplicated renderers, unknown paths, unscoped events,
   secret exposure, nested forms, or accidental changes outside the plan.
3. Confirm no production fixture/catalog/pricing, lockfile, OpenAPI schema,
   provider logic, or database migration changed without a separately explained need.
4. Update phase status and evidence in this plan or a linked small handoff record:
   source SHA, commands, durations/results, remaining limitations, and test IDs.
5. Commit/push verified implementation checkpoints under the approved repository
   workflow. This plan does not itself authorize pushing or promoting production.
   Keep incomplete refactoring checkpoints on the implementation branch; do not
   publish the replacement editor as ready before P06's functional parity passes.
   If dev deployment occurs under that later workflow, record exact branch/SHA,
   frontend image identity, URL, and browser-free HTTP/auth checks. Do not save
   profiles or run inference as a deployment smoke test.
6. Explicitly state that browser layout/native events and real provider behavior
   were not verified unless separately authorized and actually exercised.

Exit: all required deterministic gates pass on the final implementation; the
handoff distinguishes local code, pushed revision, deployment, and unrun boundaries.

## 7. Required test matrix and exact oracles

Register these proposed IDs in the canonical frontend specification during P00.
Use `SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001` in test annotations. Reuse existing files
and private fixture helpers; do not add a second test framework.

| ID | Tier / primary files | Required oracle |
| --- | --- | --- |
| WEB-TEST-090 | T0/T1; `profile_widget_state_test.exs`, `profile_widget_component_test.exs` | Rendered rerun repair initial/escalation edits reach `rerun.jsonRepair`, survive save/reload, and never add `jsonRepair` beneath `rerun.target` |
| WEB-TEST-091 | T0; `profile_widget_state_test.exs` | Exactly six descriptors and three roles; correct separate bindings; unknown node/path and disallowed field/operation rejected; no arbitrary recursive nodes |
| WEB-TEST-092 | T0/T1; state/component tests | Same profile in several roles has independent overrides; missing vs explicit zero/false/null preserved; inheritance not materialized; invalid input blocked; profile switch resets only that node; capabilities follow the selected profile |
| WEB-TEST-093 | T1; component tests | Root and all recovery nodes expose the same functional gear/editor; model/options change the correct payload; applicable credential/pricing/actions present; shared renderer proven by source call-site review as well as behavioral tests |
| WEB-TEST-094 | T1; component/workspace tests | Repair cannot add recovery/search/cache controls or bypass via forged events; rerun can add repair but not another rerun; one shared retry budget; search synchronized with root; cache ownership unchanged |
| WEB-TEST-095 | T1; component/workspace tests | Off means descendants absent and canonical null; no silent re-enable; closed enabled invalid/dirty node blocks run; disabled dirty node does not; no budget increase on toggles |
| WEB-TEST-096 | T1; component/profiles tests | Ordinary invocation edits do not mutate catalog; explicit shared Save uses correct profile and affects defaults intentionally; preserves nested selected profile's own recovery policy and sibling overrides; missing/deleted references remain errors |
| WEB-TEST-097 | T1; component/embedding tests | Node-scoped keys/uploads/actions; write-only credentials; saved-ID-only Refresh; delayed completion cannot overwrite newer draft/selection or revive branch; same-profile conflicting mutation guarded locally; unrelated nodes remain usable |
| WEB-TEST-098 | T1; embedding/workspace tests | Two instance trees have unique IDs, labels, listboxes and upload bindings; edits/messages never cross instances or modify root accidentally; ordinary typing causes no state POST; latest pending state wins |
| WEB-TEST-099 | T1 plus existing Go contracts; profiles/workspace/embedding tests | Run/state/saved-profile/cURL/bundle policy values agree after nested edits, preserve disabled branches and overrides, and reload through actual handlers rather than a prefilled expected stub |

Paths for the named Elixir suites are under
`frontend/test/harden_llm_web/live/`. Add a focused `ProfileForm` test file only
if existing `profiles_live_test.exs`/state tests cannot cleanly retain the extracted
pure helper assertions; do not duplicate the same matrix in both places.

Mandatory test construction rules:

1. Use `element/2`, `form/3`, `render_change`, `render_click`, and captured API
   payloads. Calling a serializer with a hand-built correct map does not prove
   the rendered field's name or routing is correct.
2. A save/reload stub must store the payload it actually received and return it
   on reload, not return the expected fixture regardless of the saved request.
3. Compare untouched siblings and root fields explicitly, not just the changed field.
4. Test at least one non-reasoning profile as well as portable L/M/H fixtures.
5. Guard full-widget reuse through behavior plus a human-readable call-site
   review. A CSS class, gear glyph, or brittle source-string test alone is insufficient.
6. Retain the security, zero/null, reasoning, single-budget, and instance-isolation
   purposes of earlier tests. Replace only assertions tied to the superseded
   reduced presentation, and document that mapping in the spec.
7. Keep `async: true` and private Req ownership where feasible. New serialization
   of a suite requires a named shared resource and repository-policy justification.
8. If hook code changes, use existing dependency-free Node tests for its pure
   decisions. Do not claim these prove native focus/upload/LiveSocket behavior.

## 8. Verification commands and failure policy

Run from the indicated working directory. Expose the pinned toolchain first:

```bash
export PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH
```

From `frontend/`, use the relevant subset while coding and all these suites at
the final focused checkpoint:

```bash
mix test test/harden_llm_web/live/profile_widget_state_test.exs \
  test/harden_llm_web/live/profile_widget_component_test.exs \
  test/harden_llm_web/live/workspace_live_test.exs \
  test/harden_llm_web/live/profiles_live_test.exs \
  test/harden_llm_web/live/embedding_live_test.exs \
  test/harden_llm_web/profile_defaults_test.exs \
  test/harden_llm_web/profile_widget_style_test.exs
mix format --check-formatted
```

From repository root, run the broad cheap gate at each completed coding phase:

```bash
make test-fast
git diff HEAD --check
```

For final explicit payload-contract confirmation, run the existing deterministic
backend suites; no providers or Docker are required for these default-tag packages:

```bash
go test ./internal/retry ./internal/runtime ./internal/profiles ./internal/gateway -count=1
```

The broad fast gate already includes frontend and client-core coverage; do not
build another aggregate runner. Any new test file must be included in that gate.

If a failure appears, identify its phase, field path/event, received payload,
and expected invariant. For async failures capture the relevant operation and
revision order with bounded, nonsecret test evidence. Fix the cause. Do not
raise `render_async`/test deadlines, add sleeps/retries, weaken assertions, skip
cases, or run real models to work around a deterministic failure.

`make verify`/T3 integration is required only if the implementation actually
changes a database/service boundary; this plan anticipates none. Full release
certification is not a substitute for the focused tests. Browsers, DOM emulator
installation, public provider calls, and production promotion require separate
authority under `AGENTS.md` and are not part of deterministic completion.

For the completed implementation, validate Markdown/local links and
`git diff HEAD --check` in addition to the application gates above. This
handoff reports only deterministic checks actually run; browser layout/native
events, public providers, Docker, deployment, and production promotion remain
unverified.

## 9. Completion checklist and progress record

The implementing model must not mark the task complete until all are true:

- [x] Every enabled target uses the same complete row-plus-editor renderer as root.
- [x] Every nested gear offers functioning model/options configuration and applicable profile actions.
- [x] Rerun repair form edits persist under the correct sibling path.
- [x] Exactly the allowed finite recovery shape reaches REST; no selected-profile recovery traversal exists.
- [x] Local overrides, shared-profile saves, and global retry/search/cache scope are distinguishable and correct.
- [x] Inheritance, explicit null/zero/false, profile capabilities, and backend presets survive round trips.
- [x] Credentials, uploads, dirty state, and late async results remain isolated by instance and node.
- [x] Workspace, Profiles page, and embedding example share the implementation and serialization.
- [x] No duplicate reduced renderer, per-keystroke persistence, new dependency, or timeout increase remains.
- [x] Focused tests, final contract checks, formatting, and `make test-fast` pass on the final source.
- [x] Documentation states the new presentation and unchanged execution boundaries.
- [x] Handoff identifies exactly what was changed, tested, pushed/deployed, and not verified.

| Phase | Status | Implementation SHA / evidence |
| --- | --- | --- |
| P00 baseline and specification | Complete | `566ff8a`; ADR/spec/plan updates |
| P01 rerun repair path regression | Complete | `566ff8a`; WEB-TEST-090 rendered sibling-path regression passes |
| P02 pure role/path/draft contract | Complete | `566ff8a`; WEB-TEST-091/092 plus fixed-node/capability serialization tests pass |
| P03 complete root renderer extraction | Complete | `566ff8a`; canonical `profile_widget_node/1` and full editor coverage pass |
| P04 recursive role integration | Complete | `566ff8a`; six-node rendering, correct bindings, node-id event guards, and nested option edits pass |
| P05 profile actions and async isolation | Complete for applicable actions | `566ff8a`; explicit nested shared-profile Save/catalog isolation passes; target-only New/Delete/Refresh/credential mutations remain intentionally disabled by role |
| P06 hosts and persistence parity | Complete | `566ff8a`; Workspace, Profiles, and two-instance Embedding suites pass |
| P07 final validation and handoff | Complete locally | `566ff8a`; 89 focused ExUnit tests, `make test-fast`, Go contract suites, formatting, and diff checks pass; no browser/live-provider/deploy gate run |

If completion would require new REST capabilities, unsupported profile-save
semantics, or materially broader infrastructure, stop that expansion, explain
the precise conflict, and request a decision. Do not invent an API field or
silently drop a requested widget capability to claim completion.
