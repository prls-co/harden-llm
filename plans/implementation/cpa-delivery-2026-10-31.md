# CPA and request-status delivery evidence

## 1. Scope and baseline

Implement issues #136 and #134 without changing ordinary OpenAI response formats,
resetting accepted data, or replaying uncertain inference. Tracking is separate
from durable scheduling. Deployment is recorded separately from local acceptance.

- Implementation branch: `feat/request-status-and-cpa-admission`.
- Base: `main` at `f1ace14f9b0252ce8a49f7def5b776fbf524864a`.
- `dev` was an ancestor lacking the accepted stateful boundary; the clean
  implementation branch was advanced to main before edits.
- Existing CPA branch `6fdbf4f7eec9208a7e86c961151e97eebeeee3d3` and
  unrelated dirty source remain untouched. PR #127 is unmerged; no deployed
  pool adoption is established by it.
- Status lookup does not depend on batch PR #127 or a CPA upgrade.
- TEST-490 through TEST-495 reserved for status contract, reducer, SQL,
  lifecycle/SDK, observability, and scale respectively. CPA follow-up IDs
  will be allocated when their boundaries are implemented.

## 2. Verification record

- P1 RED: `go test ./internal/gateway/httpapi -run '^TestRequestTracking' -count=1`
  failed assertions: route missing from OpenAPI; lookup returned unauthenticated
  404 without no-store instead of 401 / authenticated storage-unavailable 503.
  No provider dispatch occurred. This was an assertion failure, not a compile error.

## 3. Remaining gates

All implementation, SQL/race/scale, batch evolution, rollout and live acceptance
remain open until individually evidenced here and in the original plans.

## 4. Status implementation checkpoints

- T0/T1 header, route, observation, session, real SDK JSON/SSE, default-SDK
  duplicate retry, response-delivery failure, and observer forwarding pass.
- Focused leased PostgreSQL test passes: additive schema compatibility,
  concurrent key-scoped reservation, terminal/nonce fencing, bounded retention.
- Exact real transaction COMMIT fault hooks cover committed/missing/failed/foreign
  nonce readback with one insert and one readback; no replacement registration.
- Local synthetic cross-replica scale: 1,000 untracked calls in 158 ms with zero
  tracking SQL; 1,000 tracked calls in 1,422 ms with 14,000 SQL operations including
  lookup, generator concurrency 16. Combined focused task peak RSS 499.5 MiB,
  CPU 15.59 s includes compilation and both packages; not isolated server metrics.
  All 1,000 tracked rows completed; exactly one synthetic provider dispatch per
  request; no reference rows/prompt/output/credential sentinels persisted.
- Initial fast gate rejected stale bundled UI OpenAPI. Synced its contract copy;
  retained the static assertion unchanged. This is contract packaging only.
- Focused integration/race exposed a cancellation fixture which did not consume
  the HTTP request body before waiting for disconnect. Consume the owned body
  so Go can monitor the socket; retain the transport-stop and handler-stop oracle.
  The failed run is `tmp/status-focused-sql-scale-race.json` and is not a pass.

Current evidence is implementation-in-progress; no merge, deployment or CPA
inference has occurred. Remaining status work includes wait/cancellation holdouts,
full deterministic gates, docs and source publication. Issue #134 remains open.

## 5. Verification and acceptance map before first publication

All source remains isolated from the original dirty checkout.

| Plan cases | Executable evidence |
| --- | --- |
| RS-01/02/04/17 | TEST-490/493: strict IDs, canonical OpenAPI, real auth/no-store/error paths, invalid body/header dispatch counts |
| RS-03/11 | TEST-491: monotone source ordering, heartbeat/progress clocks, stale projection and finalization |
| RS-05/06/07/08 | TEST-492: real SQL reservation and COMMIT faults, terminal/owner/revision fencing, expiry, schema/sentinel preservation |
| RS-09/10 | TEST-493/494: both SDK JSON/SSE, structured/tools, true 429/503 scheduled waits, terminal refusal and observer forwarding |
| RS-12 | TEST-493/494: coalesced collector, blocked SQL/nonblocking callbacks, full progress channel and joined completion |
| RS-13/14/15 | TEST-495: real shared SQL across HTTP instances, cancellation, actual TCP reset/write failure, no history/output/secret storage |
| RS-16 | TEST-495: isolated synthetic comparative 1,000-call run, two keys, exact outcomes, real SQL, collector/resource measurements |

Completed local checks (logs remain under private ignored `tmp/`):

- Focused Go owner checks and race checks pass.
- `make test-integration` passed with runner-owned PostgreSQL/Garage; cleanup
  errors/warnings were empty. Subsequent new cases ran in focused SQL/race gates.
- `tmp/status-publish-sql-race.json`: all request-status cases in both owner
  packages, real SQL and real HTTP/TCP, with race detector: accepted.
- `tmp/status-test-fast-final.log`: all ten fast tasks accepted before final
  metadata acknowledgement/gauge refinements. The prepublication rerun is separate.
- Test-registration policy exposed missing Coverage gate entries for the six
  new catalog IDs; entries were added, and `node scripts/verify-test-tiers.mjs`
  accepts all ten fast-task owners. Assertions and registration remain enforced.
- RED `tmp/status-idempotent-finish-red.json` proved a different terminal body
  at the same revision was falsely acknowledged. GREEN counterpart confirms
  exact stored-field comparison, canonicalizing PostgreSQL microsecond timestamps.
- SQL rollback cleanup now shares the existing transaction deadline so metadata
  cleanup cannot silently introduce a second full rollback budget.

Isolated 1,000 versus 1,000 synthetic comparison, owned generator concurrency 16:

| Mode | Provider calls | SQL statements including polls/transactions | Wall ms | Collector peak / final | Process CPU us | Process peak RSS KiB |
| --- | --- | --- | --- | --- | --- | --- |
| Untracked | 1,000 | 0 | 433 | 0 / 0 | 1,235,518 | 68,108 |
| Tracked | 1,000 | 14,000 | 2,883 | 13 / 0 | 3,622,339 | 68,108 |

Source: `tmp/status-isolated-scale.json`. Process allocations were 94,047,984
versus 156,892,448 bytes; peak RSS is cumulative, not incremental attribution.
Runner/compiler RSS is separate (498.6 MiB). These are synthetic local request
measurements, not CPA throughput or a universal performance target.

Operational status: no deployment/provider inference. Existing running gateway
image is `sha256:3920032b73966b05f0a942de2eabb2a366c1e8615c54b92bdfb737036d7f455f`,
source label `474342a2bbf7273df9b050b386447dc0d7954ea7`. Its filtered configuration
has no enabled batch flag, Temporal address or namespace. No application
configuration or shared-service lifecycle was changed by this read-only audit.

## 6. Next implementation work

1. Publish the coherent status checkpoint and finish requirement-by-requirement
   audit of #136; leave any missing gate explicit rather than equating coverage
   registration with execution evidence.
2. Start #134 on a separate branch/worktree stacked on status. Reconcile PR #127
   into that branch without modifying the original worktree or discarding old
   workflow/schema/replay compatibility. Add the stable pool-generation fence.
3. Retain HLLM producer pacing/waiting and evaluate CPA/Home as the optional
   authoritative credential cap, not a duplicate producer queue. The prior
   audit already establishes different responsibilities; no CPA upgrade is a
   prerequisite for the current producer implementation.
4. Implement the original P1-P6 ledger/claim/independent-attempt/fairness/ordinary
   adapter/fault/scale/adoption requirements. Do not close #134 from status work.

The thread goal remains active for both #136 and #134. Production promotion is
not part of this implementation authorization.

## 7. First source publication gate

- `make test-fast` with pinned Elixir/OTP: all ten tasks accepted in
  `tmp/status-prepublish-fast.log`; no cleanup errors/warnings.
- `go test -race ./stateful ./internal/gateway/httpapi -count=1`: passed,
  `tmp/status-prepublish-owner-race.log`.
- `go test ./internal/testkit ./stateful ./internal/gateway/httpapi -count=1`:
  passed, `tmp/status-prepublish-focused.log`.
- Focused real SQL/HTTP/TCP/race and scale: accepted in
  `tmp/status-publish-sql-race.json`.
- Registration policy and whitespace checks: accepted.

This is a draft implementation checkpoint, not a deployed or merged feature.
The final #136 audit must explicitly check panic/watchdog shutdown, remaining
storage-fault variants and exact SDK/contract coverage before marking all
60 plan tasks complete. Earlier failures remain recorded, not silently retried.

## 8. Hosted security feedback

PR #140 CodeQL reported a high-severity header-to-log flow at registration.
Retain the security gate. A cheap TEST-493 regression first failed on caller
tracking-ID leakage, then logging was changed to fixed endpoint and server
call/trace correlation only. Caller header IDs remain in scoped metadata and
responses, not logs. The implementation plan and operator guide reflect this
privacy refinement. Local race checks and hosted reanalysis are recorded
separately; a successful analysis job alone does not clear an outstanding alert.

Privacy fix local acceptance: `go test -race ./internal/gateway/httpapi -run
'^TestRequestTracking' -count=1`, registration policy and whitespace passed;
`tmp/status-header-privacy-fast.log` records all ten fast tasks accepted with
no cleanup errors/warnings. Hosted CodeQL reanalysis remains a separate gate.
