# Reference provider uncertainty (#162)

## 1. Scope and historical evidence

The review starts from `148758ef2b9ad12536a8f04574a94492188f3162`.
Issue #162 describes two distinct incidents. PR #169 already corrected CPA's
30-second response-header cutoff by streaming upstream while retaining complete
ordinary JSON output and the accepted generation deadline. PR #170 qualified
the changed native contract. PR #172 separately recovered unavailable reads of
the same live admission waiter without replaying inference.

The original interrupted stream stopped after 5.1 seconds, with partial output
and no terminal response. Its admission observation was unconfirmed with a
positive local stop. The retained trace has `NETWORK_ERROR`, without the
underlying socket error. The CPA HTTP receipt ending later does not prove that
Harden received its result or identify which network hop failed. The retained
tunnel log is empty; a bounded exact-ID Laminar lookup returned no matching
spans in the connected project. Neither observation supplies the missing cause.
No specific proxy, provider, or timeout correction can be inferred from this
evidence.

Two later complete weather runs, raw artifact hashes and exports are recorded
in [the consumer RCA](https://github.com/prls-co/query-analytics/blob/ad285eea97e886bed98f32ce7d1176eb03c0b2de/docs/evidence/weather-production-acceptance-20261111/RCA.md).
Those retained passes are not a causal fix for the earlier network interruption
or fresh acceptance of this change.

## 2. Reproduced owner defects

The actual TLS router, runtime, reference composition and CPA admission record
one unconfirmed observation when the provider connection closes after an output
delta. Admission intentionally prohibits a retry. Three downstream projections
nevertheless discarded that fact:

1. `ProjectRunError` converted the marked error to `inference_failed`.
2. The runtime replaced a marked failure with a plain logical deadline or
   cancellation, erasing its uncertainty before the reference projection.
3. Admission's late-success guard recorded an unconfirmed observation but
   returned a plain timeout without the corresponding marker.
4. The Go and Phoenix reference clients treated every valid `run.failed`
   terminal as definitive, including one explicitly reporting an unknown
   provider outcome.

The controlled regressions fail on these assertions before the corrections.
The runtime-loop regression also fails against an isolated baseline worktree,
with one effect invocation and lost uncertainty. It exercises both runtime
paths using the existing injected clock and effect-entry cancellation, without
depending on host scheduling to expire a short fixture timer.

## 3. Minimal contract correction

Reuse the existing `ReferenceError.code` string:

```json
{"code":"execution_unconfirmed","message":"The provider outcome is unknown; do not automatically repeat this run."}
```

This safe error is shared by reference JSON, SSE and stored history. JSON
diagnostics identify `inferenceOutcome: unconfirmed`, keep the independent
recording outcome and suppress SDK retry through `x-should-retry: false`.
The runtime retains both context identity and the injected effect's uncertainty.

The Go client returns `UnknownOutcomeError` with validated problem/evidence and
received HTTP status where available. It never wraps this as a definitive
`RunFailure` or `RejectedError`. A missing recording tail does not discard an
already received unconfirmed outcome. Phoenix preserves the same ambiguity and
shows it in the running workspace and loaded history detail.

`RecordedRunOutput.status` and history/statistics continue to describe local
execution: `succeeded`, `failed` or `timeout`. A failed local run does not prove
a failed provider execution. The safe error code distinguishes provider
uncertainty, and recording uncertainty remains separate. No new statuses,
database migration, provider retry, fallback, endpoint or workflow is added.
Old stored evidence is retained unchanged.

## 4. Verification boundaries

TEST-540 covers actual partial-stream transport, confirmed-entry cancellation,
both production runtime paths, gateway projection, typed clients, unknown usage,
one dispatch, no cached partial output, known rejection and successful usage
controls. WEB-TEST-131 covers the Phoenix client, visible errors and history.
TEST-541 adds real PostgreSQL recording, scope isolation and existing statistics
to the same partial-stream fixture. The existing delayed-header regression and
Temporal admission cases remain required.

The first aggregate fast run exposed a new fixture that could expire during
setup before dispatch. Its deadline oracle moved to the controlled-clock
runtime test; the real socket and cancellation oracles remain at T1. No product
deadline or assertion was relaxed. The first integration run failed two existing
storage-budget cases (`TestProfileResources/replacement_transaction_failure_restores_original_catalog`
and `TestCacheDraftContract/corrupt_envelope_is_rejected_by_core_rather_than_a_cache_hit`),
with clean teardown. These failed results remain evidence; they are not passes
or a proven application regression.

Final commands, immutable source/image identities and remaining release gates
belong in the accompanying evidence receipt. Browser execution and new paid
inference are outside this change's authorization. Main/production promotion
requires an explicit request under the repository policy.
