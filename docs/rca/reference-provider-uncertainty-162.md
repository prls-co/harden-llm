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

A fresh authenticated read of the original production history and trace
confirmed a 59,999ms effective timeout, 5,142ms elapsed, 42 events, 22,849
received bytes, 93 output bytes and terminal state `missing`. The request has
no explicit provider timeout override. The original observation still has
`NETWORK_ERROR`, status zero and no provider request ID. The reading gateway
remained at source `3e21d3a43d302ca43b6ec523d28f9de62b1c8827`, image
`sha256:770e4eaba638367b295fa0149f4d3960619fcf838069cf8c512e9a9e7c2e332c`,
with zero restarts; this is an evidence read, not a deployment or inference.

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

The first full release run on `122a5fb` stopped at the existing 180-second
runner-contract task budget; sibling SIGTERM/compile cancellations are not
independent failures or passes. A complete release selection with the supported
single-CPU-slot profile retained all 30 tasks and unchanged assertions/timeouts.
It failed the real Temporal 1,000-item case at 999 successes and one failure,
with 999 provider calls. That row's specific cause was lost during normal
fixture cleanup. The test now reads its existing public results before the
original fatal count assertion, preserving the actual failure cause on any
reproduction. No count is relaxed, result fabricated, or inference replayed.
Required canceled checks remain unqualified.

Final commands, immutable source/image identities and remaining release gates
belong in the accompanying evidence receipt. Browser execution and new paid
inference are outside this change's authorization. Main/production promotion
requires an explicit request under the repository policy.

## 5. Qualified candidate and handoff

Application source `596b9557bad7584f3b3883e5a90cf63cd6ed86a0` passed the
exact-source [hosted browser-free release](https://github.com/prls-co/harden-llm/actions/runs/43042256291):
all 30 tasks passed, including real PostgreSQL, Temporal, integration race,
Compose, the complete backend verifier and connected Go/Phoenix workflows.
Cleanup errors and warnings are empty. The complete redacted report and
[qualification receipt](../../plans/evidence/harden-llm/issue-162/qualification.json)
retain these results separately from the failed local runs. A subsequent fully
captured local Temporal selection also passed all cases; that finite pass does
not explain or erase the earlier count failures.

The published Go module is `v0.0.0-20261111144919-596b9557bad7`, with its actual
download checksum in the receipt. Unchanged Query Analytics and Clustering
consumer code fails the explicit-unconfirmed regression on its old `a91c35d`
pin and passes with this module. Both affected consumer packages pass under
race using isolated module files/test overlays over retained deployment-source
snapshots. No consumer checkout, deployed pin, prompt, schema or model is
modified by this qualification. Go reference consumers must adopt the new pin;
Phoenix hosts using the shared Run client need the corresponding UI revision.

Exact-source local images are prepared for review:

| Component | Image identity |
| --- | --- |
| Gateway / worker | `sha256:0a09d1cba6313d03f381a652a7c61ded2ef515545e85d0802a6ad78b856d3f05` |
| Web | `sha256:22bab01bdd231ec3495de5b682ba2513620df67413509c03b6fb8712f3683d70` |

Both image version/revision labels match the qualified source. The actual
gateway binary reports that full SHA in a network-disabled version check, and
its owned check container was removed. Images are local handoff artifacts,
not registry publication or deployment evidence. Later changes to this record
and the failed-item diagnostic only do not rebuild or relabel those images.

The candidate is ready for release review. Main/production promotion and
consumer deployment remain separate actions requiring their owners' release
steps. The original failed network hop is still unproven; browser layout and a
fresh paid-provider weather campaign are not claimed.

## 6. PR review: validate failure errors before delivery

[The PR review](https://github.com/prls-co/harden-llm/pull/175#issuecomment-6802353551)
identified a missing validation boundary in the Phoenix SSE client. The outer
event schema leaves `data` unconstrained, and the client checked only whether
the nested error was a map. Missing or numeric error codes could raise in
`String.slice/3`; incomplete errors carrying `execution_unconfirmed` could be
accepted as valid uncertainty.

The client now validates each failure error against the existing canonical
`ReferenceError` schema before notifying a receiver or storing a terminal.
Malformed errors return the existing typed protocol error. No catch-all rescue,
new error shape, dependency, provider retry or backend change is introduced.

Eight new negative tests cover missing/numeric/null codes, missing/non-string
messages, malformed diagnostics and unexpected fields. Each tests both present
and absent recording tails, no invalid receiver delivery and exactly one POST.
All eight fail before the correction. Existing valid failure, completion and
unconfirmed outcomes remain positive controls. Focused client/workspace/history
checks pass all 41 tests; the fast-gate and hosted results are recorded in the
review follow-up receipt.

The earlier 30-task release, published Go module and image identities above
remain evidence for source `596b955`. They do not include this subsequent web
client fix. Gateway and Go client inputs are unchanged; the web image must be
rebuilt from the reviewed frontend revision before deployment. No image is
relabelled and no production rollout is part of this review correction.
