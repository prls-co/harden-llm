# ADR-HLLM-025: Complete Recursive Profile Widget with Finite Recovery Roles

- Status: Accepted and implemented locally.
- Date: 2026-09-20.
- Requirements: `PLAN-HLLM-RECURSIVE-WIDGET-001`, WEB-TEST-090 through WEB-TEST-099.
- Supersedes: the presentation portion of the target-only UI wording in the recovery implementation plan; it does not supersede any REST or runtime decision.
- Related: ADR-HLLM-014, ADR-HLLM-016, ADR-HLLM-020, ADR-HLLM-024.

## Context

Recovery targets currently reuse the compact profile row but open a reduced
target drawer. This makes model selection and reasoning look reusable while
leaving options, profile actions, and nested repair configuration in a separate
implementation. The rerun repair editor also derives its HTML name from the
rerun target name, which places `jsonRepair` below `rerun.target`; the REST
contract requires it to be a sibling of `target`.

The requested UX is one complete LLM profile widget at the original generation,
fresh rerun, and both JSON-repair stages. The runtime must nevertheless remain
finite: a selected saved profile's own recovery policy is never traversed, and
all stages share one attempt budget and parent deadline.

## Decision

Use one canonical row-plus-configuration renderer for the root and every
enabled recovery target. The stateful LiveComponent owns one canonical draft per
top-level host instance; fixed node descriptors identify the six permitted
nodes: original generation, original repair initial/escalation, rerun
generation, and rerun repair initial/escalation. Role capabilities control
which controls are rendered and accepted by handlers.

The three runtime roles are `original_generation`, `rerun_generation`, and
`json_repair`. Generation nodes may configure JSON repair; the rerun node cannot
configure another fresh rerun; repair nodes cannot configure further recovery.
Search and cache remain generation-owned, repairs never enable search, and retry
attempts/backoff/deadline remain global. Disabled branches serialize as null and
do not contribute executable child state.

Invocation overrides remain in their RecoveryTarget. Editing a saved profile is
an explicit, separately labeled profile mutation. Inherited values are not
materialized as overrides merely because a drawer opened. Shared catalog
updates do not change another node's selection or draft.

Child form bindings are explicit descriptor paths. In particular, rerun repair
fields use `recoveryPolicy.rerun.jsonRepair`, never
`recoveryPolicy.rerun.target.jsonRepair`. Unknown node/path events are rejected.

No REST schema, provider executor, database migration, cache identity, retry
budget, timeout, frontend dependency, or browser-test policy changes.

## Consequences

The root and nested targets receive the same functional option editor and
profile controls, while the role matrix prevents infinite configuration and
execution. A profile can be selected in several roles with independent
reasoning/model/provider-option overrides. An explicit saved-profile mutation
can affect all references to that profile, so the UI must say so and preserve
unsaved invocation edits.

The component has more node-aware form and async routing than the old row-only
projection. This is intentional; accepting child events as root events would
allow a nested edit to change the original request. The fixed descriptor list
keeps this routing finite and auditable instead of introducing a general graph
editor.

## Verification and rollback

Deterministic LiveView/pure-state tests WEB-TEST-090 through WEB-TEST-099 prove
paths, full editor behavior, capability guards, save/reload, async isolation,
and multi-instance embedding. The implemented local checkpoint has 89 focused
ExUnit tests, passed `make test-fast`, and passed the deterministic Go contract
suites. No browser, live provider, Docker, or production deployment was run.
Roll back the renderer/state/host changes and this ADR together if the public
REST contract or profile-save ownership is intentionally changed.
