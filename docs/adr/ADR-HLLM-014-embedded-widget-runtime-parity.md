# ADR-HLLM-014: Embedded Widget Runtime Parity

- Status: Accepted
- Date: 2026-08-23 (amended after hosted-run verification)
- Requirements: REQ-003, REQ-006, REQ-007, REQ-011, REQ-012, REQ-018, REQ-019 and `SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001`
- Verification: WEB-TEST-010 and WEB-TEST-038 through WEB-TEST-043, TEST-012, focused Phoenix/profile tests, full frontend tests, browser workflow, `make verify`, and deployed probes/browser verification

## Context

The second utility-llm comparison found that the earlier no-tabs widget was
visually close but still behaved differently in practical use. The material
differences were not only CSS: profile/API/base/model/fallback fields did not
share one searchable custom-value interaction, cache had an extra `off` state,
retry fields were projected into the wrong request object, nested escalation
bundle import was not independently addressable, and endpoint/credential/fallback
changes could be run before their saved profile snapshot was updated.

The utility widget is an in-flow reusable component. It presents one compact
row (`LLM`, profile, reasoning, cache, config), then reveals the complete
profile editor, nested escalation editor, options, retry/repair, pricing, and
mutation actions. Harden must retain that component boundary while keeping
provider calls, credentials, persistence, and validation on the Go contract.

The first authenticated browser run also exposed two boundary failures that
render-only tests could not see. The optional nested escalation editor is
inside the outer run form, so its empty required fields triggered native HTML
validation even when the fold was unused. After that was corrected, the
gateway rejected the utility profile's ordinary `max_tokens` provider option
because its secret-key scan treated every key containing `token` as a
credential.

## Decision

- Keep `ProfileWidgetComponent` as one in-flow visual component. It has a
  stable root and optional `id_prefix`; it uses parent messages for workspace
  selection, UI state, transient provider options, retry controls, and the
  run/save boundary. Tabs, a side rail, modal editor, fixed overlay, and
  page-level navigation state are not introduced.
- Use one reusable searchable combobox behavior for profile, API inference
  type, base URL, model ID, and fallback values. It supports filtering,
  keyboard selection, blur/escape behavior, discovered model options, and
  custom values. The same behavior is used for the nested escalation profile
  where the DOM is namespaced.
- Preserve utility cache semantics as exactly two UI states: `cache` and
  `refresh`. Legacy persisted `off` values migrate to `cache`; the widget
  toggles the normal cache/refresh meaning and does not expose a third control
  state.
- Keep utility profile `defaultOptions` as the saved source of truth. When a
  run is built, Harden projects retry/repair fields to the gateway's top-level
  request policy (`maxAttempts`, backoff, retry categories, and
  `repairEscalation`) and removes those runtime policy keys from
  `providerOptions`. This is a contract projection, not a second retry path.
- Require an explicit profile save before running if endpoint, inference type,
  credential, fallback, or profile identity changes are staged. Ordinary
  model/option/reasoning/cache edits remain transient run controls where the
  gateway contract permits them. A staged API key is sent only by the explicit
  stage/save event and is never included in ordinary form changes or rendered
  state.
- Give main and nested escalation profile bundle uploads separate LiveView
  upload names and namespaced input IDs. When `id_prefix` is supplied, every
  generated form/control ID and parent message carries that namespace, and
  the host registers matching upload channels. Both editors retain the same
  Import, Export, Save, Delete, and Refresh action semantics without colliding
  in a host page.
- Provide an authenticated `/embed/llm` host fixture that mounts two instances
  with distinct prefixes. It is a verification surface and integration
  example, not a replacement page shell: the widget remains an in-flow
  component and the host owns session, routing, and persistence orchestration.
- Keep reasoning capability filtering as an intentional backend-bound
  adaptation: the compact selector exposes only the selected profile's
  portable map, and the server removes stale unsupported reasoning before
  state/run validation.
- Mark the primary Run Prompt submitter `formnovalidate`. The browser may not
  use the unused nested editor's required fields to veto a primary run; the
  LiveView `run_payload` validation remains authoritative for profile, prompt,
  schema, retry, and escalation values.
- Classify provider-option secret keys explicitly. Allow ordinary utility
  request controls such as `max_tokens`, `max_output_tokens`, and
  `max_completion_tokens`, while rejecting credential-shaped names such as
  API keys, authorization, credentials, passwords, secrets, and bearer/access/
  session tokens. This preserves the source contract without weakening the
  credential boundary.

## Consequences

The widget now behaves like the utility control tree in the common paths that
matter to an embedder: the compact row is recognizable, every disclosure is
in flow, nested actions have independent identity, search/custom values work
consistently, cache and retry payloads are predictable, and an unsaved endpoint
or credential cannot create a run against a different saved profile.

The multi-instance contract is now operational rather than only nominal:
generated nested field IDs cannot collide, child messages identify their
prefix, and main/escalation bundle uploads are independently addressable. The
checked-in host fixture demonstrates two instances without adding tabs or a
second navigation model.

The Go gateway remains the owner of provider policy, credential storage,
profile validation, retry execution, cache identity, and persistence. The
Phoenix layer must maintain the projection rules when the REST contract
changes. Utility's offset/page-number history quick-jump and Firebase/browser
provider implementation remain the previously accepted adaptations in
ADR-HLLM-012.

The native-submit correction and the provider-option classifier are deliberate
boundary adaptations discovered by live browser verification. They do not add
a second runtime path, alter endpoint allowlisting, change timeout/retry
budgets, or persist credentials in the browser.

No KER, timeout-policy change, retry-budget change, provider-policy change,
new account, or related issue is required for this presentation and request
projection correction. The existing operator credentials remain external to
the repository and tests.

## Rollback and verification

Revert the component/WorkspaceLive changes and this ADR together if the
gateway contract is intentionally changed to accept a different retry/cache
projection. Before rollback or any follow-up contract change, rerun the
focused and full frontend suites plus the real browser workflow; do not restore
the old duplicate workspace retry fold or the unsaved-run path as a workaround.

The executable parity cases are:

- WEB-TEST-038: compact no-tabs row, main/nested folds, fallback/options
  interactions, combobox topology, and nested action controls.
- WEB-TEST-039: write-only credential staging, save/refresh/delete, and bundle
  import/export delegation.
- WEB-TEST-040: profile-aware reasoning capability and stale-state omission.
- WEB-TEST-041: two-state cache behavior and legacy `off` migration.
- WEB-TEST-042: explicit profile save required for endpoint changes before run.
- WEB-TEST-043: two-instance host routing, complete ID/upload namespaces,
  independent folds/cache/profile selection, and no-overflow embedding.
- TEST-012: provider request-boundary admission accepts utility token-limit
  option names and rejects credential-shaped option names.
- WEB-TEST-010 plus the authenticated hosted browser workflow: the primary Run
  Prompt control carries `formnovalidate`, the optional nested fold remains
  usable, and a real CPA run reaches the gateway successfully.

## Amendment evidence: 2026-08-23

PR `#25` (`80397b8`) fixed the native form-validation boundary. PR `#26`
(`b3a50ce`) fixed the false-positive `max_tokens` rejection and added TEST-012
coverage. The deployed `b3a50ce` frontend and gateway were healthy; public
frontend/API probes returned HTTP 200; the authenticated browser opened the
compact widget and every nested fold, passed desktop/mobile overflow checks,
and completed a real CPA `gpt-5.6-luna` Run Prompt. No KER or related issue was
created because timeout, retry-budget, provider endpoint, and ownership
semantics did not change.

## Amendment evidence: multi-instance embedding

The implementation found that the earlier optional `id_prefix` only covered
some control IDs. Phoenix-generated nested form IDs remained global, and child
messages/upload events were not attributable to a host instance. The fix adds
complete field-ID scoping, tagged parent messages, per-instance upload names,
and the authenticated `/embed/llm` two-instance fixture. WEB-TEST-043 covers
the deterministic and Chromium paths. No KER or related issue was created:
this changes only component integration boundaries, not provider policy,
timeouts, retry budgets, API ownership, or credential handling.
