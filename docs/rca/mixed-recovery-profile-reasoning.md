# 1. Mixed recovery-profile reasoning rejection

Issue [#119](https://github.com/prls-co/harden-llm/issues/119), 2026-10-13.
Affected application: `ff5c968bc3c2263956fb1f946935395506347bfc`.

## 2. Confirmed production cause

Displayed ID `412cdbce-b438-4687-b1af-7c3929d23112` is the result's trace ID.
Its history run is `d801d0f2-93cf-4f48-af2f-b91b71fcf51b`. The stored error is
HTTP 400 / `profile_invalid`, field `reasoningEffort`. The generation and first
repair used CPA gpt-6.1-sol; escalation used CPA claude-sonnet-5-5, with no
portable reasoning map. The UI showed no available reasoning but serialized
`lowest`. Preparation failed with zero attempts and `providerInvoked=false`.
No inference was dispatched for this failed run. The stored user record is
preserved; no prompt, session, credential or raw diagnostic is published.

The full target editor substituted a global reasoning default. Generation
already used a capability-aware selection, while the recovery form did not.
The earlier matrix tested each preset as generation and tested the recovery
chain through same-profile model overrides; it missed profile capability
switches. This is an actual coverage gap, not an upstream outage.

## 3. One conversion and unchanged backend validation

`ProfileWidgetState.profile_reasoning_levels/profile_reasoning_effort` now own
the configured portable choices for generation, target editors and profile
selection. `normalize_recovery_policy` walks the existing five node descriptors,
preserves supported selections and omits reasoning for unmapped profiles. Empty
reasoning must be omitted because the optional wire field does not accept an
empty enum value. Apply the same conversion to edited/restored drafts and the
outgoing request. Unknown profile references still reach backend validation.

The regressions also exposed an inherited draft target left behind when fresh
rerun is enabled. Compile it to a profile target using the explicit current
generation model, reasoning and options. Inherited repair leaves retain their
existing semantics. This resolves the draft into the already supported contract;
no core policy, alternative API, provider fallback or recursive stage is added.

Direct API callers still receive errors for unsupported reasoning. The wrapper
now qualifies those fields with their exact recovery path. The UI displays the
validation code, field paths and run ID without reflecting the raw server error
message. Gateway rejection logs include code, run ID, result trace ID and provider
invocation status, with no prompt, credential or raw error message.

## 4. Coverage and retained failures

TEST-456 / WEB-TEST-127 switches mapped/unmapped profiles in every recovery
position through rendered controls and checks submitted reasoning. Its original
assertion failed on the deployed implementation. Pure package cases cover valid
selection preservation, limited maps, restored unsupported settings, idempotence
and fresh-rerun generation inheritance.

TEST-457 covers strict rejection and qualified fields at all five backend
positions, safe correlated logging and useful client validation messages. The
generic-message regression failed before its fix. TEST-454 / WEB-TEST-126 now
selects distinct GPT/Claude profiles at all five positions, persists them,
reloads the page and executes the six-call chain from the reloaded view. The
native fixture retains the exact provider-model sequence assertion. The actual
40-template / 80-mode matrix remains intact.

The fast gate found a fold click preceding its pending autosave acknowledgement.
The test now awaits the owned async completion before the dependent click; its
saved-options/reload/exactly-once oracle is unchanged. The failed report is kept.
No arbitrary sleep, test skip, assertion weakening, browser or additional paid
call is introduced. Evidence is under
`plans/evidence/harden-llm/mixed-recovery-profile-reasoning/`.

## 5. Release and scope

PR [#120](https://github.com/prls-co/harden-llm/pull/120) is merged at application
source `0773784111baf5858fb799a730e169dfb052a3c1`. Main fast and all three CodeQL
analyses passed. Exact-source [release 38474634696, attempt 1](https://github.com/prls-co/harden-llm/actions/runs/38474634696/attempts/1)
passed all 29 tasks, including the amended mixed-profile T3 chain, with no
cleanup errors/warnings. The uploaded release, integration and integration-race
reports are retained beside the original RED/GREEN and fast failure evidence.

Clean exact-source local builds produced these deployed identities:

| Component | Image ID | State |
| --- | --- | --- |
| Gateway | `sha256:64e3076d1ce37f052a4da9f6dfad17aa7ca268ac4a3f5c75ad2a2d494f5abf09` | healthy, zero restarts |
| Frontend | `sha256:0d918d226d54ca4ba21e65cf2341aa94715d1155f8b97f7a6df3944334a9e132` | healthy, zero restarts |

The existing operator applied only those two image/release identities, with
schema execution before application startup. Scoped and full seven-service
configuration checks are equivalent. TEST-435 public HTTP health/authentication
and release checks passed at `https://harden-llm.prls.co`. All 40 deployed preset
drafts match certified source; the same 30 have configured credentials.

The complete reported failed history record is unchanged. Protected settings,
deployment bearer, upstream metadata, all seven shared/telemetry owner identities
and Caddy are verified unchanged. No SQL correction, data reset, provisioning or
compatibility reader was needed. The protected checkpoint and schema-compatible
previous images are retained; disposable operator/build worktrees are removed.
The [deployment record](../../plans/evidence/harden-llm/mixed-recovery-profile-reasoning/deployment-acceptance.json)
contains the redacted acceptance facts. Subsequent docs-only commits do not
rebuild or relabel these images.

This correction adds no original plan phase or architectural layer. The extra
fresh-rerun conversion and async test acknowledgement are explained in sections
3–4 and retain the existing contract and assertion oracles. Browser layout,
interactive Portal and additional paid inference were not checked. Previous
live-preset qualification is retained evidence; it is not a replay of this run.
