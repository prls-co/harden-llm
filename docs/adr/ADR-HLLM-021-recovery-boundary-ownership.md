# ADR-HLLM-021: Recovery Boundary Ownership

- Status: Implemented and locally certified; deployment, browser and live-provider checks remain separate.
- Date: 2026-09-15.
- Requirements: REQ-213 through REQ-224 in `plans/retries-repair-boundary-consolidation-plan.md`.
- Verification: TEST-212 through TEST-222; WEB-TEST-074 and WEB-TEST-075.
- Related: ADR-HLLM-018, ADR-HLLM-019 and ADR-HLLM-020.

## 1. Context

The recovery policy and execution loop were already consolidated, but several
boundaries still interpreted the same event differently. Preparation resolved
DNS before cache lookup, transport paths classified model and search failures
differently, Responses streaming reconstructed output from deltas, and failed
attempts could lose known accounting. The Phoenix widget could also reset a
host draft's policy and overlapping whole-state saves could leave an older
snapshot durable after a newer edit.

## 2. Decision

Keep one owner for each fact and use the existing runtime, provider, accounting,
cache and LiveView lifecycles:

- Provider errors carry one explicit category. Status, protocol terminal state,
  completion shape and bounded metadata are assigned at their source; free-form
  error text is data. Runtime alone decides whether that category may consume
  another attempt.
- `Prepare` performs static URL and credential checks without DNS. The shared
  guarded transport resolves and validates addresses inside the attempt loop.
  Model and Jina HTTP paths use the same status and `Retry-After` normalizer.
- A model attempt reports `ProviderDispatched` from the standard-library
  `httptrace.WroteHeaders` event. This is local dispatch evidence, not proof of
  remote execution or billing. Search requests never set the model fact.
- A Responses stream is accepted only when it supplies a terminal completed
  response object. Other protocols require their documented successful stop
  marker. Limits, refusals, unsupported markers, malformed envelopes and
  incomplete streams are terminal provider failures; only completed extracted
  output can reach schema repair.
- Usage and cost are normalized before output or completion failures return.
  Valid partial facts remain partial, invalid accounting is bounded and
  terminal, and cumulative provider totals retain earlier valid contributions.
  Both text and structured operations use response projection `v3`; the outer
  `operation-v2` cache namespace is unchanged and has no old-projection reader.
- The host LiveView owns the active recovery policy. The widget emits policy
  intents and sends one complete selection snapshot. Workspace state uses one
  in-flight writer and one latest pending snapshot; all existing state-save
  entrypoints use that writer and the canonical state builder.
- Backoff uses full jitter inside the capped exponential window, then applies a
  valid server delay as a lower bound. A router clock is injected only for
  deterministic HTTP-date tests.

No retry service, SDK retry owner, compatibility reader, alternate writer,
cache migration, cross-tab protocol, new dependency or public field is added.

## 3. Consequences

Malformed provider data and ambiguous transport failures are visible as bounded
terminal facts instead of being converted into semantic repair. A cache miss
may occur after the projection version change because old response-projection
keys are intentionally unreachable. Known usage/cost can be shown even when a
result is not accepted, while unknown dispatched work remains uncertain.

The policy draft remains stable through selection, restore, profile saves and
rerenders. A failed state write remains visible and is not retried implicitly;
an explicit newer edit can advance the writer. Per-LiveView ordering is
guaranteed, while cross-session last-write behavior remains outside this ADR.

## 4. Verification and rollback

The focused provider/runtime suites cover classification, transport, dispatch,
completion, accounting, cache projection and exact timing. Frontend owner and
persistence suites assert public events, request ordering, stored read-back and
reload. Existing `make test-fast`, static and browser-free release gates are
the certification controls. Browser layout and paid-provider behavior require
their existing explicit opt-ins.

Rollback is a matching whole-checkpoint restore. An older binary must not be
run against data written under the current profile/state/result contracts; no
runtime compatibility path or historical cache lookup is introduced.

