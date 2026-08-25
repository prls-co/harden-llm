# ADR-HLLM-016: Widget Draft, Refresh, and Model-Catalog Data Contract

- Status: Accepted
- Date: 2026-08-25
- Requirements: REQ-008 through REQ-013, REQ-016, and `PLAN-HLLM-WIDGET-PARITY-001`
- Verification: TEST-105 through TEST-111; release/browser evidence is recorded separately

## Context

The utility-llm comparison identified three ownership boundaries that materially
affect reuse of the Harden-LLM widget: profile edits must not cause a host state
write for every keystroke, model refresh must not submit an unsaved endpoint or
transient credential, and provider model discovery must not become a hidden
catalog owned by the reusable component. The widget also needs to retain
provider-specific options and the currently selected model when a host catalog
does not list it.

## Decision

- `ProfileWidgetComponent` owns ordinary profile draft forms, option/retry
  projections, fallback order, fold state, staged-key state, and dirty
  classification until an explicit commit boundary.
- `WorkspaceLive` and other hosts receive only committed runtime controls and
  dirty/save status. Ordinary text, numeric, JSON, pricing, and fallback edits
  do not call `POST /api/v1/state` per keystroke.
- `HardenAPI.refresh_profile_models/2` remains an authenticated ID-only POST to
  `/api/v1/profiles/{profileID}/models:refresh`. The gateway reads the saved
  profile and stored credential. A dirty endpoint or credential disables refresh
  and reports that Save is required; no draft request body or transient refresh
  credential path is introduced.
- The host owns the model catalog as normalized `{id, label?}` values and passes
  it into the widget. When the host supplies no catalog, the widget may use its
  small built-in default preset. A supplied host catalog replaces that default;
  the current selected model ID is appended as an unlabelled custom value when
  necessary. Provider discovery remains a host/backend responsibility.
- `ProfileWidgetState` is the single pure transformation surface for option
  aliases, unknown-key preservation, retry defaults/false values, structured
  repair nesting, fallback movement, cache normalization, and model-option
  construction. `ProfilesLive.profile_payload/1` remains the server validation
  and REST payload boundary.
- Main and escalation mutation actions use independent pending keys. A mutation
  in one editor must not relabel or disable unrelated actions in the other
  editor or another namespaced widget instance.
- The client combobox decision core remains dependency-free and DOM-free. Native
  focus, LiveSocket patching, file inputs, and layout remain browser boundaries;
  no Happy DOM, jsdom, React, or second frontend runtime is added.

## Consequences

This keeps the widget embeddable: a host can supply its own catalog and page
state without adopting the widget's persistence or provider implementation.
Saved-profile refresh has one auditable credential path, and unknown provider
options do not disappear when visible controls change. The selected custom
model remains usable even when a host catalog is stale or intentionally narrow.

The host must refresh or replace its catalog when provider discovery is desired.
The built-in preset is a fallback, not a provider inventory. A custom selected
ID can still be rejected by backend profile/run validation; retaining it in the
combobox is not a claim that the provider supports it.

## Rollback and verification

Rollback reverts this ADR and the widget state/catalog changes together. Do not
restore per-keystroke host persistence or draft-refresh requests as a rollback
shortcut. Verify TEST-105 through TEST-111, the existing OpenAPI contract,
`git diff --check`, and the targeted browser boundary before accepting a later
ownership change.
