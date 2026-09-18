# ADR-HLLM-024: Bounded Recovery Stages and Request-Bound Progress

- Status: Accepted for implementation; production configuration and provider certification remain separate.
- Date: 2026-09-18.
- Requirements: REQ-301 through REQ-316 in `plans/rest-recovery-and-progress-implementation-plan.md`.
- Verification: TEST-236 through TEST-259 and WEB-TEST-083 through WEB-TEST-089.
- Supersedes: only ADR-HLLM-020's single-target-only recovery restriction. ADR-HLLM-020's single-loop, strict-admission, accounting, and ownership decisions remain in force.
- Related: ADR-HLLM-018, ADR-HLLM-019, ADR-HLLM-021, and ADR-HLLM-022.

## Decision

Structured calls may use one finite recovery plan with an original generation,
an initial JSON-repair target, an optional escalated JSON-repair target, and one
fresh rerun generation with the same repair shape. The runtime keeps one global
attempt budget and one parent deadline. Recovery targets are leaf provider
selections; their saved policies are not traversed. Transport retries repeat the
same prepared work, while only a completed invalid structured result advances to
the next semantic stage.

The original and rerun branches have independent generation cache identities.
The result records the selected generation target separately from the model that
actually produced an accepted repaired value. Repair prompts contain bounded,
flat failed-output history and validation diagnostics; rerun prompts start with
the original task and schema and never receive failed answers or repair
instructions from the original branch.

The Go runtime publishes best-effort, nonblocking diagnostic snapshots. The
gateway exposes them only through an opt-in authenticated SSE representation of
the existing `POST /api/v1/run`; JSON remains the default and there is no
detached job or polling protocol. The provider boundary measures received bytes,
parsed events, and output bytes/code points without guessing tokens, and stops a
Responses stream at a validated terminal event rather than waiting for EOF.

Request origin fields are bounded correlation metadata. They cannot alter
authentication, owner IDs, cache identity, target selection, or trace access.
Prompts, outputs, credentials, and arbitrary origin values stay out of progress
events and metric labels. Existing PostgreSQL run history, Garage artifacts,
accounting, and OpenTelemetry ownership remain authoritative; no analytics
database or per-delta SQL writer is added.

## Defaults and migration

The explicit new preset is six attempts with CPA GPT-5.6 Luna lowest/highest
repair targets and CPA GPT-6 Astra lowest generation plus lowest/highest repair
targets. Astra is an operational prerequisite: no endpoint, pricing, capability,
or reasoning mapping is invented in this repository. Existing explicit stored
budgets are not raised automatically. A transactional migration maps the old
boolean repair setting to generation-relative repair targets, preserving disabled
settings and all historical evidence; it does not enable CPA or Astra for old
callers.

## Consequences and rollback

Clients can distinguish a soft performance expectation from the immutable
execution cap using stage, elapsed, remaining-budget, wait, and stream-growth
evidence. A heartbeat or unfinished output is never treated as successful
completion or authorization to extend a timeout. The existing timeout RCA and
fast-test policies remain unchanged.

The coordinated profile/state/result/cache version transition must be deployed
with matching writers and readers. Rollback is a matching database/configuration
checkpoint, not an old image against migrated data. Browser, deployment, and
paid-provider checks remain explicit opt-ins.

## Compatibility boundary

Current v3 writers and the public default emit only the explicit `jsonRepair` /
`rerun` shape. During the rollout, Go and Phoenix readers retain a bounded
legacy read path for pre-migration documents that contain
`repairInvalidOutput`; this is needed to keep existing callers and historical
fixtures safe while the transactional migration runs. A legacy policy is
validated as an all-or-nothing alternative, is never combined with the new
plan, and is never emitted by a current writer. It follows the existing
single-loop behavior only for that explicitly legacy input. New structured
plans use only the six finite stages above, so there is no recursive or mixed
recovery path. Removing this compatibility reader is a separate cleanup after
all v2 writers have been retired.
