# Utility LLM Frontend Parity Inventory

## 1. Audit scope and source revisions

This inventory records the frontend behavior that must be represented by the
self-hosted Phoenix/Go application, subject to the self-hosted boundaries in
this repository. It is based on the read-only `utility-llm` checkout at
`5c0309e` (`chore(release): publish utility-llm 0.15.0`) and the merged
the current `harden-llm` working checkout (the previously certified baseline was
`d02bee8`).

The parity target is behavior, not React or Firebase implementation. Firebase
email/password auth, Firebase ID tokens, Firestore state, and signed Firebase
trace URLs are replaced by the existing Phoenix session, Go REST, Postgres,
and same-origin artifact boundaries. Provider execution, credential material,
cache records, trace records, and profile validation remain backend-owned.

## 2. Utility LLM frontend file inventory

| Source | Surface | Functionality |
| --- | --- | --- |
| `src/react/index.js` | Package widgets | Shared profile configuration, prompt input, output, history, trace, and stats widgets. It is the authoritative UI behavior source for the reusable controls. |
| `src/react/editable-combobox.js` | Editable combobox | Downshift-backed searchable single-value input. Focus selects the current value, typing updates the controlled value, arrows highlight options, Enter selects, blur closes the menu, and custom values remain valid. |
| `src/react/styles.css` | Widget presentation | Warm panel, field, fold, combobox, credential, result, history, trace, stats, error, focus, disabled, and responsive styles. `harden-llm` may use different CSS but must preserve the states and accessible names. |
| `src/react/index.d.ts` | Public widget contract | Controlled props and callbacks for every profile, prompt, output, history, trace, and stats control. The callback list is a coverage checklist. |
| `examples/react-trace-studio/src/App.jsx` | Reference application | Session gate, state hydration, controlled form state, persisted UI state, profile actions, run actions, history actions, schema generation/checking, output copy, and widget composition. |
| `examples/react-trace-studio/src/api/client.js` | Browser API boundary | Authenticated state, history, profile, bundle, model-refresh, and run requests. |
| `examples/react-trace-studio/src/auth/client.js` | Firebase auth boundary | Session subscription, email/password sign-in, sign-out, and ID-token lookup. Replaced by Phoenix `SessionVault` and `/api/v1/auth/*`. |
| `examples/react-trace-studio/src/download-json.js` | Browser download boundary | Pretty-prints a profile bundle, creates a JSON Blob, downloads through a temporary anchor, and always cleans up the URL/anchor. |
| `examples/react-trace-studio/src/app.css` | Reference application presentation | Warm responsive shell, login, status/error, widget stack, controls, folds, trace/resource, history, and stats states. The Phoenix app may use Tailwind classes but preserves the same visible states and accessible controls. |
| `examples/react-trace-studio/src/testUsage.js` | Deterministic UI fixture helper | Builds normalized usage/cost fixtures for output and LLM-stat tests without contacting a provider. |
| `examples/react-trace-studio/src/main.jsx` | Browser composition | Mounts the application and imports the package widget stylesheet. |
| `examples/react-trace-studio/index.html` | Document shell | Sets viewport, title, favicon, root mount, and module entrypoint. |
| `examples/react-trace-studio/src/App.test.jsx` | Application workflow tests | State hydration, fold persistence, profiles, cache, retry/repair, pricing, schema, output, history, import/export, and canonical action payloads. |
| `examples/react-trace-studio/src/react-widgets.test.jsx` | Widget tests | Every reusable widget control, combobox behavior, fallback ordering, pricing, options, trace resources, and stats rendering. |
| `examples/react-trace-studio/src/llm-stats-summary-widget.test.jsx` | Stats summary tests | Aggregate counts, token/cost/duration display, full-view link, controlled expansion, and cache-cost markers. |
| `examples/react-trace-studio/src/theme-layout.static.test.jsx` | Shell/static tests | Warm theme, vertical widget stack, package CSS ownership, and no stale split-column/dark layout. |
| `examples/react-trace-studio/server/firebase-api.test.mjs` | Reference backend tests | Auth, profile validation/probing, bundles, model discovery, run/cache/retry/repair/reasoning, pricing, traces, and paginated history. Translated behavior belongs in Go and Phoenix tests. |

## 3. Visible and interactive element inventory

### 3.1 Authentication and application shell

| Location | Element/text | Behavior |
| --- | --- | --- |
| Session pending | `Trace Studio`; `Checking session...` | Holds the page at a named loading state until auth resolves. |
| Login | `Email` email input | Controlled email value, browser email autocomplete, submitted with the login form. |
| Login | `Password` password input | Controlled password value, current-password autocomplete, submitted with the login form. |
| Login | `Sign in` submit button | Calls auth, disables while pending, changes label to `Signing in...`, and renders the returned error in an alert. Enter submits the same form. |
| Authenticated header | `Utility LLM Trace Studio` | Application identity and page heading. |
| Authenticated header | Authenticated email pill | Displays the redacted/session identity supplied by the auth layer; it is not an action. |
| Authenticated header | `Sign out` button | Revokes the Phoenix session, disables during the request, and reports failure without exposing a token. |
| Application | `Loading saved profiles...` status | Visible until the canonical backend state is hydrated. Widgets do not render against an unhydrated state. |
| Application | Request error alert | Shows safe message, stage/code/profile/provider/model details, and bounded field errors. Raw provider errors and credentials never render. |

### 3.2 Compact model/profile row

The `ModelConfigWidget` renders one compact row per model category. The
current Trace Studio passes the category name `LLM`; the escalation editor
reuses the same controls with category name `Escalation Model`.

| Element/text | Type | Behavior and state |
| --- | --- | --- |
| Category name (`LLM`, `Escalation Model`) | Text | Labels the model slot; a blank category is a programming error. |
| `LLM Profile` / visible `🤖` | Searchable editable combobox | Searches by profile name, model, endpoint, interface, and discovered models. Existing profile selection loads the profile and prompt draft through the backend state mutation. A typed custom value remains visible and can produce a field error. The escalation version writes `structuredRepairRetry.escalation.llmProfile`. |
| `Reasoning` / visible `🧠` | Select with `L`, `M`, `H` | Controls `lowest`, `middle`, or `highest`. Main profile reasoning is persisted as per-profile UI/run state; escalation reasoning is written into escalation options. |
| `💾` | Pressed cache-mode button | `Use cache` means normal cache lookup; `Overwrite cache on next run` means refresh. `aria-pressed`, title, and label reflect the state. Harden exposes the same two states and migrates legacy persisted `off` to `cache`; it does not expose a third disabled state in this widget. |
| `⚙` / `Profile config` | Disclosure button | Opens/closes the complete profile editor. Fold state is controlled and persisted. It is disabled while a UI-state save is pending so stale responses cannot overwrite the newest fold state. |

### 3.3 Profile configuration fields and actions

These controls are inside the profile disclosure. The escalation profile
editor reuses the same field set but excludes nested retry/repair controls.

| Element/text | Type | Behavior and state |
| --- | --- | --- |
| `API Inference Type` | Searchable select | Selects `Chat Completions`, `OpenAI Responses`, `Gemini Generate Content`, or `Anthropic Messages`. The backend validates and owns provider capability semantics. |
| `Base URL` | Editable searchable combobox | Offers existing profile origins, strips trailing slashes, allows a custom HTTPS endpoint, and preserves a typed value after validation failure. |
| `Endpoint credential` status | Status text and indicator | Shows `No credential stored`, `Stored key available`, or `New key staged for save`. Only write-only status and rotation metadata are displayed. |
| `Set key`, `Replace key`, `Hide key` | Disclosure button | Opens/closes the replacement credential drawer without reading a stored secret. |
| `Replacement API Key` | Text input | Local draft only until staged; autocomplete and spell correction are disabled. |
| `Clear staged key` | Danger button | Removes a newly staged unsaved key, not the stored backend credential. |
| `Cancel` | Button | Clears the local replacement draft and closes the credential drawer. |
| `Stage key` | Primary button | Copies a non-empty local draft into the write-only profile payload and closes the drawer. |
| `Refresh Models` | Button beside the first model slot | Uses the saved profile ID and stored credential only, preserves old models on failure, and updates suggestions without changing profile identity. Dirty endpoint or credential drafts require Save first; no draft request body is sent. |
| `Model ID` | Editable searchable combobox | Single selected model value. A host-owned `{id, label?}` catalog is authoritative; the Harden preset is used only when the host supplies none, and the current typed ID remains visible if omitted. Custom model IDs are allowed. |
| `Fallback LLMs` | Group | Ordered backup profile references. The current profile is excluded from its own options; custom references remain possible so the backend can return graph validation errors. |
| `Add Fallback LLM` | Button | Appends an empty ordered fallback slot. |
| `Fallback LLM N` | Editable searchable combobox | Edits the ordered profile reference at index N. |
| `Up` / `Down` | Buttons | Move one fallback reference without changing the other entries; first/last boundary buttons are disabled. |
| `Remove` | Danger button | Removes one fallback reference. |
| `Options` | Disclosure button | Opens common request options while preserving the raw JSON source of truth. |
| `Max Output Tokens` | Number input | Updates `defaultOptions.max_tokens`; empty removes the key. |
| `Temperature` | Number input | Updates `defaultOptions.temperature`; empty removes the key. |
| `Top P` | Number input | Updates `defaultOptions.top_p`, removes legacy `topP`, and accepts empty/unset. |
| `Top K` | Number input | Updates `defaultOptions.top_k`, removes legacy `topK`, and accepts empty/unset. |
| `Stop Sequences` | Multiline textarea | One stop sequence per line; blank lines are removed and the normalized array is stored in `defaultOptions.stop`. |
| `Default Options JSON` | Monospace multiline textarea | Directly edits the complete options object. Invalid JSON disables save/run as appropriate and shows a field hint/error. |
| `Retries & Repair` | Disclosure button | Opens all retry and structured-repair controls. It is present only on the primary profile editor. |
| `Structured Repair` | Checkbox | Enables semantic structured-output repair and creates normalized escalation defaults; disabling stores `structuredRepairRetry: false`. |
| `Rate Limits` | Checkbox | Controls retry on HTTP 429. Enabled is represented by omission/default true; disabled stores false. |
| `Server Errors` | Checkbox | Controls retry on HTTP 5xx. |
| `Network Errors` | Checkbox | Controls retry on transient network failures. |
| `Parse / Schema Errors` | Checkbox | Controls parse/schema retry when structured repair is disabled; it is checked and disabled when repair requires it. |
| `Max Attempts` | Number input | Total attempt budget, including initial, ordinary retry, repair, and escalation attempts. |
| `Base Delay Ms` | Number input | Initial retry backoff. |
| `Max Delay Ms` | Number input | Upper retry backoff bound. |
| `Starting Attempt` | Number input | First attempt using the escalation profile; disabled when structured repair is off. |
| Escalation `LLM Profile` | Reused searchable editable combobox | Selects the stronger profile for repair. It can create a new profile draft and has its own profile editor. |
| Escalation `Reasoning` / cache / `Profile config` | Reused controls | Same semantics as the primary row, with retries excluded from the nested editor. |
| `Pricing` | Disclosure button | Opens profile-level pricing metadata, kept outside `defaultOptions`. |
| `Input $/1M tokens` | Number input | Converts UI dollars-per-million value to stored per-token input pricing. |
| `Output $/1M tokens` | Number input | Converts output pricing. |
| `Cache read $/1M tokens` | Number input | Optional cache-read pricing. |
| `Cache write $/1M tokens` | Number input | Optional cache-creation pricing. |
| `Reasoning output $/1M tokens` | Number input | Optional separately reported reasoning-token pricing. |
| `+ New` | Button | Clears selected profile identity through the backend state path and starts a new profile draft. |
| `Import Bundle` | Button plus hidden JSON file input | Opens an `application/json` picker, parses one profile bundle, sends it to atomic backend import, and resets the input value. |
| `Export Bundle` | Button | Requests the encrypted profile bundle and downloads it as pretty JSON using one browser download boundary. |
| `Save Profile` / `Save Escalation Profile` | Primary button | Submits the complete profile, pricing, options, ordered backups, and optional replacement credential. Save is disabled for invalid JSON or missing required identity. Backend probing/validation is atomic. |
| `Delete Profile` / `Delete Escalation Profile` | Danger button | Deletes the selected profile after backend dependency validation. |

The profile row and every nested editor must preserve safe field errors next to
the corresponding control. A credential value is never repopulated from a
successful read or included in rendered state.

### 3.4 Prompt and structured-output input

| Element/text | Type | Behavior |
| --- | --- | --- |
| `Input` | Section heading | Labels the prompt widget. |
| `Prompt` | Multiline textarea | Main user prompt. `Ctrl+Enter` or `Cmd+Enter` invokes the same run action as the button. |
| `Advanced input` | Disclosure button | Shows/hides system and structured-output controls; state is persisted. |
| `System Prompt` | Multiline textarea | Optional system instruction. |
| `Schema shorthand` | Monospace multiline textarea | Compact object-like schema description, for example `{"answer":"string"}`. |
| `Generate JSON Schema` | Button | Converts shorthand to editable strict JSON Schema; invalid shorthand stays in place and reports a safe validation message. |
| `Structured Output Schema` | Monospace multiline textarea | Full JSON Schema object. It is checked explicitly and after five seconds of inactivity; invalid, pending, or failed checks block a structured run. |
| `Check Schema` | Button | Immediately validates the current schema and updates the inline status. |
| `Clear Schema` | Button | Clears shorthand and full schema only, persists that non-structured draft, and resets validation state. |
| `Clear Prompt Fields` | Button | Clears prompt, system prompt, shorthand, and schema while preserving selected profile. |
| `Run Prompt` | Primary button | Runs the selected profile with the current prompts, schema, reasoning, cache mode, default options, and retry/repair policy. Disabled without a profile, while running, or while schema validation blocks execution. |

### 3.5 Output, trace, and row-local resource controls

| Element/text | Type | Behavior |
| --- | --- | --- |
| `Output` | Section heading | Labels the latest output widget. |
| `Latest output` | Output header | Shows the latest result and interface/endpoint metadata. |
| Result body | Monospace preformatted text | Pretty-prints JSON/object results; displays an empty-state message before the first run. |
| `Copy` | Button | Copies the formatted latest result, changes to `Copied` or `Failed` briefly, and does not expose trace credentials. |
| Trace summary | Clickable summary row | Expands/collapses measured LLM stats. The `Details` button performs the same action with an accessible label. |
| Trace summary metrics | Text | Success/failure status, trace ID, model, retry count, duration, input/cache/output-plus-reasoning tokens, and known cost. Zero-token placeholders are not treated as measured stats; metric titles provide the same hover labels as utility-llm. |
| `Trace ID`, `Status`, `Used Repair`, `Attempts` | Expanded trace text/list | Shows normalized retry categories, status codes, retry delays converted from the API's canonical nanosecond durations, and repair metadata. An empty attempt list remains empty (for example, a cache-served result). |
| `View JSON Trace` | Same-origin link | Opens the authenticated History trace view in a new tab; this is the self-hosted equivalent of utility-llm's supplied JSON trace URL and uses `noopener noreferrer`. |
| `Copy cURL` | Button | Copies the safe trace request command when the backend supplied one. Disabled if unavailable. |
| `Show Request` / `Hide Request` | Button | Displays the request payload from the current LiveView form; the self-hosted run response does not require a second browser fetch. |
| `Show Response` / `Hide Response` | Button | Displays the normalized run response from the current LiveView result. Both folds reset when the parent trace details are collapsed. |
| Request/response blocks | Monospace preformatted text | Show exact available trace payloads or an explicit unavailable message; absent properties are not represented as fake empty values. |

### 3.6 History and pagination

| Element/text | Type | Behavior |
| --- | --- | --- |
| `History (N)` | Section heading | Shows the total history count returned by the backend. |
| `Delete all` | Danger button | Clears all history through the backend and resets the page/result expansion. The button is disabled for an empty history or in-flight clear. |
| `Show history` / `Hide history` | Disclosure button | Persists visibility. The workspace hydrates a bounded recent page with its state request and keeps the history panel hidden by default; the dedicated History view loads pages over the cursor contract. |
| History item header | Expand/collapse button | Shows model and prompt preview; opening displays result and trace stats/resources. |
| `Restore prompt` | Button | Restores user/system/schema fields and selected profile from one history entry, then persists the draft. |
| `Delete` | Danger button | Deletes one history entry, refreshes the current page, and closes its expansion. |
| History pagination | Pagination controls | Utility uses page/page-size callbacks and a quick-jump input. The self-hosted implementation provides bounded page-size selection and cursor-based `Load more`; arbitrary page jumps are intentionally not reproduced because the current Go contract has no offset/page-number operation. |
| `Loading history...` | Status text | Shown during lazy/page loads. |
| `No runs yet in this session.` | Empty text | Shown for an empty history. |

### 3.7 Exported standalone stats widgets

These widgets are exported by `src/react/index.js` even though the reference
Trace Studio composes trace details inside output/history.

| Widget | Elements and behavior |
| --- | --- |
| `LlmStatsWidget` | `LLM stats` heading; all supplied trace rows; status/category/status-code, attempts, duration, token groups, cache marker, cost, and row-local request/response/cURL/JSON trace resources. It does not own pagination. |
| `LlmStatsSummaryWidget` | Aggregate `LLM Stats:` line with success, failure, optional timeout, prompt/output token totals, cache-aware cost, average duration, optional full-view link `⛶`, and controlled `Expand`/`Collapse` button with detail slot. It omits empty totals and zero timeout. |

## 4. Utility action/API contract

The reference browser client exposes these actions:

| Browser action | Utility route | Self-hosted owner |
| --- | --- | --- |
| Hydrate state | `GET /api/state` | `GET /api/v1/state`, Phoenix `WorkspaceLive`, Go state store |
| Persist state/UI/draft | `POST /api/state/save` | `POST /api/v1/state`, strict state schema extension |
| List history | `GET /api/history?page&pageSize` | `GET /api/v1/history`, pagination/cursor adaptation |
| Delete/clear history | `POST /api/history/delete`, `POST /api/history/clear` | `DELETE /api/v1/history/{historyID}`, `DELETE /api/v1/history` |
| Save/delete profile | `POST /api/profile/save`, `POST /api/profile/delete` | `PUT/DELETE /api/v1/profiles/{profileID}` |
| Export/import bundle | `GET/POST /api/profile/bundle/*` | `GET/PUT /api/v1/profiles/bundle` |
| Refresh models | `POST /api/models/refresh` | `POST /api/v1/profiles/{profileID}/models:refresh` |
| Run prompt | `POST /api/run` | `POST /api/v1/run` |
| Trace JSON/resources | Signed `/api/trace-json` fetch | `GET /api/v1/traces/{traceID}` and same-origin artifact controller |

The self-hosted stack must keep the existing session-handle/token boundary,
Go provider ownership, Postgres persistence, artifact authorization, strict
OpenAPI envelopes, and redaction rules. It must not reintroduce Firebase,
Firestore, browser provider calls, plaintext credential state, or a second
runtime implementation.

## 5. Test translation inventory

### 5.1 Utility React application tests

The 26 named `App.test.jsx` cases cover:

- vertical model/input/output composition and attached trace stats;
- row-local resource controls;
- persisted profile/options/pricing/retry/repair/input/history/cache fold state;
- ordered fallback profile editing and escalation-profile reuse;
- startup gating until state hydration;
- new/selected profile persistence before form replacement;
- complete profile pricing save, bundle import/export, and canonical save/refresh/run/delete payloads;
- latest-output stats and zero-token suppression;
- schema shorthand generation, schema edit checking, schema-only clear, and run blocking;
- invalid typed profiles and redacted save-probe progress/details;
- history delete, clear, restore, and pagination refresh.

### 5.2 Utility reusable-widget tests

The 24 named `react-widgets.test.jsx` cases cover:

- compact row labels and write-only credential state;
- category validation, ordered fallback controls, and cache toggle direction;
- escalation reuse and folded configuration;
- custom Base URL/Model ID values and keyboard combobox selection;
- dollars-per-million pricing fields outside default options;
- invalid profile retention and field errors;
- common option controls as the JSON source of truth;
- controlled input/output widgets;
- hidden history, pagination callbacks, trace resources, unavailable-resource states;
- all-record stats, status/category/status-code, aggregate attempt metadata, run-level totals, cache-aware cost, and zero-token behavior.

### 5.3 Utility server/reference tests requiring self-hosted translation

The 41 named `firebase-api.test.mjs` cases cover the backend contracts that the Go
gateway must expose to the Phoenix UI: auth/envelopes, profile probing and
atomic writes, backup graph validation, credential redaction, strict bundle
import/export, model discovery, cache modes, retries/repair/escalation,
reasoning, selected-state persistence, custom endpoints, trace metadata,
pricing certainty, schema validation, and unlimited/paginated history.

The self-hosted translation covers the relevant behavior through the Go contract
tests, Phoenix LiveView tests, and browser workflow. Firebase emulator/auth
internals, Firestore-specific persistence, and signed-URL implementation tests
are represented by the self-hosted session, Postgres, and same-origin artifact
boundaries rather than copied literally.

The read-only utility checkout’s complete deterministic `npm test` passed: its
contract, boundary, core, behavior, example, React, and package gates all
passed, including 16 React/server test files and 147 Vitest tests.

## 6. Current harden-llm gap map

| Area | Current harden behavior | Required parity work |
| --- | --- | --- |
| Auth/shell | Phoenix session login/logout and protected LiveViews exist. | Preserve behavior while matching utility shell status/error semantics. |
| Profiles | Dedicated Phoenix studio with compact profile cards, provider/interface/endpoint/model, write-only credential staging, ordered backups, options, retry/repair/escalation, pricing, refresh, CRUD, bundle actions, and deep-link editing. The full editor unfolds inline below the cards. | The utility’s compact row/fold language is preserved without creating a second profile-editor owner; Firebase-specific persistence and browser provider calls remain excluded. The embedded widget now uses the same searchable/custom-value interaction for profile, API type, base URL, model, and fallbacks, and namespaces the nested bundle upload. |
| Workspace | One narrow vertical studio stack containing model, input, output, history, and stats widgets; advanced prompt/schema controls, persisted UI folds, custom profile values, actionable history rows, output copy/request/response/cURL, token/cache/cost stats, and canonical run payloads. | Keep the single Phoenix/Go path; no browser provider calls or second widget runtime. Cache is utility-compatible (`cache`/`refresh`), retry policy is projected into the gateway request boundary, and endpoint/credential/fallback identity edits require profile save before a run. |
| History | Cursor-based expandable records, restore, trace observations/artifacts, row stats, request/response/cURL copy, delete, clear confirmation, page-size selection, and load more; workspace history has the same row-local actions. | Arbitrary page-number/quick-jump behavior would require an offset contract; retain cursor semantics as the self-hosted adaptation. |
| Backend state | Go/OpenAPI state carries prompt draft, selected profile, schema shorthand, reasoning map, cache mode, retry/repair controls, and fold visibility with strict validation. | Continue adding only behavior required by the inventory and keep credentials write-only. |
| Profile schema | Go `Profile` already has pricing, default options, backups, models, reasoning map, and credential state. | Expose and test the existing fields through Phoenix; add only fields needed by utility behavior, not Firebase-specific copies. |
| History API | Go contract is cursor/limit based and the LiveView preserves that boundary. | Keep cursor navigation deterministic; do not add an offset compatibility path solely for the utility quick-jump control. |
| Trace API | Go returns trace observations/artifacts and Phoenix authorizes artifact redirects. | Normalized LLM stats, availability, request/response, and cURL behavior are now exposed without raw provider credentials. |
| Tests | Phoenix unit/live/browser and Go tests cover the translated self-hosted behavior. | Keep the utility test inventory as the regression checklist and add any newly discovered behavior to canonical `WEB-TEST-###`/`TEST-###` cases. |

## 7.1 Widget parity follow-up matrix (2026-08-25)

The following matrix is the bounded parity contract for
`PLAN-HLLM-WIDGET-PARITY-001`. Every approved requirement is classified; there
are no unclassified rows. “Adapted” means the observable behavior is retained
through the self-hosted ownership boundary. “Changed by decision” is an
intentional product or release choice recorded in the plan and ADRs.

| Requirement | Utility source surface | Hardened target surface | Status | Decision/evidence |
| --- | --- | --- | --- | --- |
| REQ-001 | `src/react/index.js:ProfileConfigControl` | `ProfileWidgetComponent.render/1` | aligned | Compact in-flow row: category, profile, reasoning, cache, config; TEST-101 |
| REQ-002 | `ProfileConfigFields`, `FoldSection` | `profile_widget_component.ex` folds | aligned | Ordinary and nested folds remain in flow; TEST-102 |
| REQ-003 | `ProfileConfigControl` cache button | `cache_label/1`, `cache_title/1`, workspace cache state | aligned | Two-state cache/refresh labels, title, and pressed semantics; TEST-101 |
| REQ-004 | `EndpointCredentialDrawer`, `ProfileActionRow` | credential drawer and staged-key handlers | adapted | Phoenix keeps stored status and write-only replacement behavior; TEST-103 |
| REQ-005 | Utility omits storage/capability metadata from ordinary editor | backend profile validation plus new-profile-only fields | changed by decision | No ordinary identity fold or credential ID/scope controls; TEST-102 |
| REQ-006 | `BackupProfilesEditor` | fallback rows and `ProfileWidgetState.move_fallback/3` | aligned | Unnumbered rows, Up/Down boundaries, custom values; TEST-104 |
| REQ-007 | `ProfileActionRow`, bundle callbacks | namespaced LiveView upload/action rows | adapted | File selection triggers one LiveView import; hardened delete confirmation remains; TEST-104 |
| REQ-008 | `OptionsEditor.updateOptions` | `ProfileWidgetState.patch_options/2` | aligned | Raw JSON is canonical and unknown top-level keys survive; TEST-105 |
| REQ-009 | `RetryRepairEditor` | `patch_options/2` plus server payload validation | aligned | Default-true omission, explicit false, nested preservation, parse-retry removal; TEST-105 |
| REQ-010 | controlled field callbacks in `App.jsx` | component-local draft forms and host messages | changed by decision | No per-keystroke host state persistence; TEST-106/EVAL-102 |
| REQ-011 | profile-save/run boundary | `WorkspaceLive.run/2` and dirty/save state | adapted | Existing saved-profile safety gate is retained and made explicit; TEST-107 |
| REQ-012 | `onRefreshModels` callback | `HardenAPI.refresh_profile_models/2` and gateway route | changed by decision | Saved-profile ID-only refresh; dirty drafts require Save; TEST-108/109 |
| REQ-013 | `EditableCombobox`, model option callbacks | host catalog assign, default preset, `client_core.mjs` | changed by decision | Host owns catalog; widget only supplies defaults when catalog is absent; TEST-110/111 |
| REQ-014 | `idPrefix` and widget callback props | `id_prefix`, upload names, parent messages | aligned | Two instances have independent IDs, uploads, folds, and action state; TEST-113 |
| REQ-015 | `styles.css` field/fold/combobox rules | `.ullm-*` scoped CSS and LiveView markup | adapted | Structural/semantic visual acceptance; no screenshot matrix; TEST-112/114 |
| REQ-016 | utility API/client ownership | Phoenix `HardenAPI`, Go/OpenAPI, write-only secrets | adapted | Firebase/browser-provider paths remain outside this self-hosted widget; TEST-103/109/115 |
| REQ-017 | browser-native combobox/file/layout boundaries | Node pure core plus targeted Wallaby tier | changed by decision | Chromium is separate and targeted; no Happy DOM/jsdom dependency; TEST-111/114/115/117 |
| REQ-018 | release/deployment identity | test tiers, immutable release evidence, staged promotion | changed by decision | Verify merged SHA and same image digest before production; TEST-116/118/EVAL-104 |

The durable data-ownership record is [ADR-HLLM-016](adr/ADR-HLLM-016-widget-draft-and-data-contract.md).

## 7.2 Implementation order

1. Extend the Go/OpenAPI state/profile/run/history projections and fixtures so
   the Phoenix UI has one canonical self-hosted contract.
2. Expand `ProfilesLive` with the compact row, full profile editor, ordered
   backup controls, credentials, options, retry/repair/escalation, pricing,
   model refresh, bundle actions, and safe field errors.
3. Expand `WorkspaceLive` with advanced prompt/schema controls, shorthand
   generation/checking, persisted UI state, reasoning/cache controls, clear
   actions, and canonical run payloads.
4. Replace the minimal result/history presentation with reusable LiveView
   components for result copy, measured stats, expandable rows, pagination,
   trace resource controls, restore, delete, and clear.
5. Translate the utility React and reference-server cases into deterministic
   Phoenix/Go tests, then run focused tests, `make verify`, Phoenix tests, and
   browser tests. Fix failures until the requirement matrix is green.

This inventory is the audit baseline; each behavior must have a current
harden-llm element, backend contract, or an explicit self-hosted boundary note.

## 8. Implemented parity slices

The parity implementation is present in the self-hosted checkout:

- `WorkspaceLive` persists prompt drafts, schema shorthand, generated contracted schemas, reasoning, cache mode, retry controls, repair escalation model settings, and UI fold state through the Go state endpoint.
- The workspace exposes advanced input, schema generation/check/clear actions, output copy, attempt/usage/cost/cache facts, a local LLM statistics summary, and a richer recent-history view. History is loaded lazily, supports restore/delete/clear, and preserves typed custom profile values.
- `ProfilesLive` exposes write-only credential staging, ordered backup editing, common provider options plus source JSON, retry/repair controls, escalation metadata, all five pricing rates, model refresh, bundle import/export, and the existing CRUD actions.
- The 2026-08-22 UI pass replaces profile and delete overlays with in-flow `#profile-editor` / `#profile-delete-panel` folds, replaces the wide profile table with responsive compact cards, and applies the utility warm-card/emoji control language to both Profiles and the single-column Workspace stack.
- The 2026-08-22 fold-event correction uses `phx-value-open` rather than the reserved `phx-value-value` key, and the real browser workflow verifies that model, advanced-input, retry, history, and output folds open through the LiveView socket.
- The 2026-08-22 workspace draft correction merges field-local `phx-change` events from the Reasoning and Cache selects into the current draft, preserving the selected profile before submit.
- The studio surfaces are intentionally component-oriented for embedding: `#workspace-page` and `#profiles-page` are single vertical stacks with stable `studio-page` / `studio-stack` / `studio-card` / `studio-fold` roots, no tabs or side rail, no fixed overlay, and in-flow folds. The canonical Workspace profile surface is the reusable `HardenLlmWeb.ProfileWidgetComponent` at `#workspace-llm-widget`; `Layouts.app` is only a route adapter.
- `ProfileWidgetComponent` provides the utility-like compact LLM row, profile/API/credential/model/fallback controls, Options, Retries & Repair, nested Escalation Model configuration, Pricing, bundle actions, and in-flow delete confirmation. Its optional `id_prefix` now namespaces every generated control/form ID, tags parent messages, and selects per-instance main/escalation upload channels when a host embeds more than one instance; the host owns routing/session orchestration through the existing message and OpenAPI boundaries. The authenticated `/embed/llm` fixture demonstrates the contract with two instances.
- `ProfileDefaults` is the single frontend source for the utility-aligned editor defaults: `max_tokens: 16000`, temperature/top-p/top-k/stop-sequence placeholders, retry and escalation placeholders, model/base-URL/profile placeholders, reasoning/cache defaults, and contextual `?` help markers. `WEB-TEST-052` covers the pure default contract; the LiveView component suite verifies the rendered fields and marker titles.
- The workspace profile picker renders the backend-owned catalog rather than a second frontend preset list. The catalog is normalized equal to utility-llm's current 28-profile source, `WEB-TEST-053` covers empty-state selection of `CPA GPT-5.6 Luna`, `WEB-TEST-054` renders every catalog entry in the LiveView combobox, and `WEB-TEST-055` verifies that selecting another preset synchronizes its model ID. These defaults/preset cases are server-rendered LiveView behavior and therefore do not add browser-test permutations; the existing browser canary remains responsible only for native LiveSocket/combobox integration.
- The workspace input widget now follows utility-llm's prompt/advanced/schema topology: utility placeholders and row counts, monospace schema fields, right-aligned action rows, a field-local schema hint, conditional advanced rendering, populated default prompt/schema/repair values, and the contracted schema keyword/enum guard. `WEB-TEST-056` and `WEB-TEST-057` cover these server-owned defaults, rendered diffs, validation, and Run gating; the `SchemaCheck` and `SchemaPending` hooks have only their pure client decisions covered by Node, so no browser permutation is added for backend invariants.
- The second widget parity pass aligns the practical control behavior with utility: profile/API/base/model/fallback values use searchable custom-value comboboxes, the cache control is the two-state `cache`/`refresh` model with legacy `off` migration, retry/repair fields are stored in utility-shaped profile defaults but projected to the gateway's top-level run policy, and a staged endpoint/credential/fallback identity cannot run until the profile is saved. WEB-TEST-041 and WEB-TEST-042 cover the cache and save boundaries.
- Main and nested escalation bundle inputs use separate LiveView upload names and DOM namespaces, so both unfolded editors can import/export independently when the component is embedded more than once. The retry/editor fold tree remains in flow; no duplicate workspace retry panel or tab shell is reintroduced.
- Phoenix-generated form-field IDs are included in the same namespace as fold and action IDs. Without this, two otherwise distinct widgets still collide on `profile_*` inputs and LiveView rejects the page; WEB-TEST-043 keeps this practical embedding boundary executable.
- The reasoning selector is capability-aware: seeded profiles expose only the levels in their `reasoningEffortMap`, while a custom profile without a map shows a disabled placeholder. `WorkspaceLive` repeats that check when building the run request so stale persisted reasoning cannot produce a provider-preparation failure before the request reaches the provider. WEB-TEST-040 covers the unmapped-profile boundary.
- The hosted run boundary is browser-safe: the primary Run Prompt submitter uses `formnovalidate` so an unused nested Escalation Model editor cannot block `phx-submit` through native required-field validation, while LiveView still validates the actual run payload. The gateway's provider-option classifier also admits utility-compatible request controls such as `max_tokens` and `max_output_tokens` while rejecting credential-shaped names. TEST-012 and the WEB-TEST-010 rendering assertion cover these boundaries; the hosted browser verified a real CPA run.
- Workspace model and escalation controls can still deep-link to the canonical `/profiles` editor; new-profile credential fields open automatically while existing-profile edits keep stored credentials behind a closed write-only drawer.
- `HistoryLive` exposes expandable request/result records, result and credential-free cURL copy, page-size controls over the cursor API, trace observations, artifact links, restore, delete, and clear.
- The Go state and run contracts now carry the prompt draft, persisted UI flags, model override, explicit bounded retry controls, repair escalation, and run timeout. OpenAPI and backend validation were updated together.
- The prompt shortcut, five-second schema debounce, model/base-URL datalists, write-only staged-key controls, output request/response folds, and per-record token/cache/cost summaries are covered by `WEB-TEST-034` through `WEB-TEST-036` and the corresponding rendering assertions.
- Workspace history rows now expose row-local restore/delete/inspect/copy/resource controls, and all history loading and mutation failures remain inline and credential-free.
- The Compose smoke harness now prints gateway/provider diagnostics on failure, and the smoke override clears inherited provider-host policy before exercising its fake provider; the deterministic Compose gate passes without weakening production endpoint policy.

The self-hosted runtime now carries an optional escalation profile ID and resolves that profile's credential and provider protocol for the repair attempt. The operation remains bounded by the same retry budget and records the effective profile on the attempt, preserving the self-hosted trace and cache ownership boundaries.

## 9. Completion audit

The remaining differences are explicit self-hosted adaptations rather than
unimplemented frontend behavior:

- The full profile editor has one owner at `/profiles`; workspace model rows
  use an editable native datalist and deep links instead of duplicating the
  nested editor in every fold. Its editor and delete confirmation are in-flow
  folds, and the profile list remains visible beside the expanded configuration
  in the same document.
- The workspace and dedicated History views use the Go cursor/limit contract;
  utility offset/page-number quick-jump controls are not reproduced because
  the self-hosted API has no offset operation.
- Utility Firebase auth, Firestore persistence, browser provider calls, signed
  URLs, and Blob-download implementation are replaced by Phoenix sessions,
  Go/Postgres state, backend execution, same-origin artifact authorization,
  and the existing profile bundle route.
- Browser serialization is part of the parity contract: deterministic
  `render_click` coverage is paired with real desktop/mobile browser clicks so
  native HTML control properties cannot silently replace LiveView payloads.
- Embedding is part of the visual contract: host applications can place the
  stable widget under their own shell without adopting tab state or a page-level
  side rail. `ProfileWidgetComponent` keeps all disclosure in flow, accepts an
  optional ID namespace, and communicates host-owned selection/UI changes through
  explicit messages while profile mutations remain on the same OpenAPI contract.
- The runtime parity follow-up is recorded in ADR-HLLM-014. The remaining
  differences are deliberate self-hosted projections: Firebase/Firestore and
  browser-provider ownership, cursor history instead of utility offset
  quick-jump, and the gateway's top-level retry request shape. The widget
  behavior itself is covered through WEB-TEST-038 through WEB-TEST-042; the
  primary submitter and provider-option run boundary are covered by WEB-TEST-010,
  TEST-012, and the authenticated hosted workflow.
- The defaults and preset follow-up is intentionally covered at the cheaper
  LiveView tier: `WEB-TEST-052` through `WEB-TEST-055` assert the default maps,
  rendered `?` help markers, all 28 backend catalog options, and selected
  profile/model synchronization. No additional browser test is required for
  those server-owned invariants.
- The input-widget follow-up is also covered at the cheaper LiveView tier:
  `WEB-TEST-056` and `WEB-TEST-057` assert utility input topology/defaults,
  contracted schema validation, and invalid-schema Run gating. Node covers the
  current-value schema-check payload and pending presentation; Chromium remains
  reserved for native LiveSocket and layout boundaries.

The implementation is complete only when the checked-in Go gates, Phoenix
LiveView suite, browser workflow, formatter/static checks, and
`git diff --check` all pass on the final checkout.

Final audit evidence for 2026-08-18:

- `make verify` passed, including regular and race-enabled Go tests and
  `govulncheck` with no called vulnerabilities.
- `make test-compose` passed after the Tempo trace-ID normalization fix.
- The pinned Phoenix suite passed with 77 tests and 3 exclusions; the desktop
  and mobile browser workflow passed with 2 tests.
- `git diff --check`, JavaScript syntax checking, and Elixir formatting checks
  passed. `main` remains aligned with `origin/main`.
- PR `#4` merged as `2c1a34f9737dd50b6af387c449f63d9299b166d1`; the gateway and
  Phoenix images carrying that release are healthy, and public `/healthz`,
  `/readyz`, and `/login` probes returned HTTP 200.
- The direct `kin-openapi` dependency is pinned to `v0.144.0`, the first patched
  release for the two GitHub security alerts discovered during publication;
  GitHub's alert records remained open at final readback pending Dependabot's
  next dependency-graph refresh.

Final audit evidence for 2026-08-22:

- PR `#16` merged as `31d3106` and PR `#17` merged as `7c55266`; the frontend
  production image was `sha256:3a8eb2bdc9096210a1c768c87d69c365fbe09b2f1b07d37c6c3d80b64263528d`,
  the gateway remained at healthy release `8f69e2b`, and all public health/login
  probes returned HTTP 200.
- Focused workspace/rendering coverage passed 20 tests; the full deterministic
  frontend suite passed 83 tests with 3 excluded; the desktop/mobile Wallaby
  workflow passed 2 tests in 99.3 seconds.
- Hosted Playwright passed on release `7c55266`: all workspace folds opened,
  Reasoning and Cache changes preserved the selected `CPA GPT-5.6 Luna` profile,
  the real prompt returned output and request/response details, all 29 profile
  cards exposed actions and metadata, and desktop/mobile surfaces had no tabs,
  horizontal overflow, fixed overlays, or page errors.
- No KER or related issue was created for P07.S11-P07.S13: these changes did
  not alter timeout, retry-budget, provider-policy, or API ownership semantics.

Final audit evidence for the reusable no-tabs widget amendment:

- `WEB-TEST-038` covers the compact row and every main/nested profile fold and
  action; `WEB-TEST-039` covers staged credentials, save, refresh, delete, and
  bundle import delegation. The focused widget suite passed 17 tests and the
  full deterministic frontend suite passed 85 tests with 3 excluded.
- `WEB-TEST-040` covers profile-aware reasoning choices and the stale-state
  guard for custom profiles without a reasoning map. The focused workspace
  suite passed 18 tests and the full deterministic frontend suite passed 86
  tests with 3 excluded.
- The pinned Chromium desktop/mobile workflow passed 2 tests in 102.4 seconds.
  It opened the main Options, Retries & Repair, Pricing, Escalation Model, and
  nested Options folds through the real LiveView socket without tabs, overlays,
  horizontal overflow, or page errors.
- The fallback chooser, option-to-JSON synchronization, nested cache control,
  and optional widget ID namespace are covered by the checked-in component and
  tests. No KER or related issue was created: this is a presentation/component
  topology change with no provider, timeout, retry-budget, or API ownership
  change.
- PR `#21` merged the profile-aware reasoning guard as `93b7362`; its hosted
  replay found that selecting a no-map profile could still persist an empty
  `reasoningByProfile` value. PR `#22` merged as `d02bee8` and removes that
  unsupported entry before state validation. WEB-TEST-040 covers both the
  disabled UI control and the persisted/run-payload boundary.
- The final frontend image is
  `sha256:a208f39bf3f61d706fdf1ad3bd17e2598795438bce92e7f2d3ab6953d7d0671f`;
  the container is healthy and frontend/API health, readiness, and login
  probes returned HTTP 200. The authenticated hosted browser check used the
  existing production credentials, ran `CurlStructured` with CPA
  `gpt-5.6-luna`, opened all eight nested folds, returned real output, omitted
  unsupported `reasoningEffort`, and reported no horizontal overflow at desktop
  or mobile widths. No KER or related issue was created because the backend
  contract, provider policy, retry budget, and timeout semantics are unchanged.

Final audit evidence for the hosted run-boundary follow-up:

- The browser initially exposed a native HTML validation defect: empty required
  fields in the optional Escalation Model fold prevented the outer Run Prompt
  submit event. PR `#25` (`80397b8`) added `formnovalidate` to the real
  submitter; the server-side run validator remains active.
- The subsequent gateway 422 was caused by the old secret-key scan rejecting
  the utility profile's normal `max_tokens` default option. PR `#26`
  (`b3a50ce`) now distinguishes request token-limit names from credential-shaped
  names, with TEST-012 regression coverage.
- The final deployed release `b3a50ce` passed `go test ./...`, CodeQL, healthy
  Compose startup, public frontend/API probes, and the authenticated Chromium
  workflow. The widget opened all main/nested folds, had no tabs or mobile
  horizontal overflow, and completed a real CPA `gpt-5.6-luna` prompt. The
  one-off browser harness and temporary evidence files were removed after the
  check; no credential or live output was added to the repository.

Final audit evidence for the multi-instance embedding amendment:

- `WEB-TEST-043` covers the checked-in `/embed/llm` host fixture with two
  instances. The deterministic LiveView case passed with unique DOM IDs,
  independent folds/cache/profile selection, and distinct main/escalation
  upload names.
- The isolated Chromium case at `test/browser/full_workflow_test.exs:43`
  passed in 50.2 seconds after opening both widget trees, scrolling the compact
  control into view, checking independent cache state, and verifying no tabs,
  duplicate IDs, or horizontal overflow.
- PR `#28` merged as `34eb380`; the frontend image
  `sha256:9da0680b31ad75f0d5ac226def6fc2c81833fb73ec90f1f763143de765cc75dd`
  is healthy in the existing `harden-llm` Compose project. Public frontend and
  API health/readiness/login probes returned HTTP 200.
- The authenticated public Chromium check passed in 51.9 seconds against
  `/embed/llm`: both widgets rendered, independent folds and cache state were
  exercised and restored, IDs were unique, tabs and horizontal overflow were
  absent, and logout returned to the login page.
- The production catalog returned 30 rows with 2 configured profiles. Bounded
  live text smokes passed for `CurlStructured` and `ShamanLiteLLM`; their smoke
  history records were deleted before logout.
- The implementation found and corrected a practical gap in the earlier
  `id_prefix` claim: generated Phoenix form IDs and parent messages were still
  global. The host now routes `{:profile_widget, prefix, message}` events and
  registers per-instance upload channels. No KER or related issue was created;
  provider behavior, credentials, retry/timeout budgets, and API ownership are
  unchanged.
