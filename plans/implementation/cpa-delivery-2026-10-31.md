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

## 9. Final status acceptance additions

- Owned observer panic is rethrown after bounded unconfirmed metadata cleanup;
  cleanup never calls the provider. Watchdog stops its ticker/collector without
  inventing a terminal execution outcome or writing recovery work.
- Collector completion now joins diagnostic-counter cleanup as well as its
  ticker and metadata write.
- Both endpoints exercise typed official-SDK structured outputs/function tools,
  in addition to tracked JSON/SSE. A fixture using union JSON unmarshalling lost
  a required schema; replacing that fixture assembly with the SDK's typed schema
  builder preserved the original output/dispatch oracle.
- Real POST A/GET B now proves same UUID independence across two valid keys,
  byte-identical foreign/unknown 404s, two distinct scoped rows and exactly two
  intended provider calls. Query failure is 503/unavailable rather than missing;
  logical expiry hides rows before cleanup.
- `tmp/status-final-acceptance-sql-race.json` accepted all request-status owner
  cases against real PostgreSQL/HTTP/TCP with race detection and clean cleanup.
- `tmp/status-acceptance-audit-fast.log` accepted all ten fast tasks. The broader
  canonical integration-race task is running separately for the shared SQL
  helper and existing storage holdouts; this is not a completed gate yet.
- Hosted PR #140 at `6e0818b4782ba4b118625fec422e267ad98652ca`: both fast
  jobs, CodeQL analyses and the CodeQL security summary passed. Hosted Docker,
  browser and release tasks were skipped, not passed. Later audit additions
  need their own pushed source identity and hosted checks.

Final status gate results: canonical `make test-integration-race` accepted with
empty cleanup errors/warnings (`tmp/status-full-integration-race.log`). The
retention audit additionally proves live records and other-key expired records
survive bounded cleanup (`tmp/status-audit-retention-sql-race.json`, accepted).
These are local real-service/race results, independent of hosted skipped tasks.
The implementation has no remaining known runtime defect from the recorded
status acceptance cases; final checklist reconciliation/publication and separate
rollout identity remain explicit. Issue #134's ledger/workflow/admission work is
not covered or completed by these results.

## 10. Shared-admission ledger checkpoint

The separate `feat/cpa-shared-admission-v2` branch integrates status at `0ef61e3`
and the retained V1 batch baseline without changing the original dirty checkout.
P1 adds separate outcome/admission diagnostics: a partial-output throttle remains
terminal/unconfirmed while cooling future work without refunding its start.
Generation budget and actual deadline are optional diagnostic fields in OpenAPI.

P2 now has additive PostgreSQL pool generations, immutable reservations, fresh
invocation claims, staged observations, idempotent settlement and local-stop
revision fencing. Unknown claim COMMIT requires positive exact-owner readback;
metadata uncertainty cannot authorize another invocation. Safe automatic expiry
only settles an unclaimed reservation. Ordinary reservations hold metadata only.

- `tmp/cpa-ledger-concurrent-reserve-red.json` caught a same-permit reservation
  race; the item-lock readback now acknowledges that same reservation.
- `tmp/cpa-ledger-budget-fence-red.json` caught a reservation rewriting an accepted
  300-second generation budget. The real manifest is now checked under the item
  lock; a reservation cannot change that budget or extend the accepted job deadline.
  TEST-496 includes the cheap pure regression for this invariant.
- `tmp/cpa-ledger-adoption-final-race.json`: accepted real PostgreSQL/race tests,
  including fresh schema, repeated legacy adoption, immutable request/sentinel
  preservation, unknown claim/settlement COMMIT, incompatible index rejection,
  concurrent claims and reservations, claimed-expiry protection, and generation
  handoff blocked until positive local stop. Cleanup errors/warnings were empty.
- `tmp/cpa-ledger-owner-race.log`: batch/API owner packages passed with race detection.
- The broad `make test-fast` is still running as session 86082; its result is
  recorded separately, not inferred from focused acceptance.

The ledger is not yet connected to independent provider workflows or the shared
ordinary-request adapter. P3-P6 remain open. There is no deployment/provider call.

## 11. Independent attempt lifecycle checkpoint

P2 source `f6987867a68c992e509392ef26c19e6fb982d71f` is pushed in draft PR #141,
stacked on status PR #140. Its broad fast gate completed successfully in
`tmp/cpa-ledger-fast.log` (all ten tasks, clean cleanup).

P3 now has independent-attempt workflow/activity code, a metadata-only starter,
and identity-bound artifact recovery. The provider activity uses a fresh claim
per invocation, one native core attempt, no repair/rerun/cache, and a generation
deadline resolved once at claim. Provider activity failure or redelivery only
enters ledger/artifact readback. Known observations survive metadata retries;
unknown execution never becomes a replacement inference. Completion notification
uses the stable pool ID with no Run ID and only permit/revision references.

Starter batches contain at most 16 intents, use one 15-second total budget, and
issue starts concurrently with stable IDs and completed-ID reuse rejected. The
starter does not execute inference. Uncertain start acknowledgement remains an
unresolved intent rather than releasing/replacing its reservation.

Artifact envelopes bind pool/permit/claim/scoped item identity and validated
completion timing. Response bodies remain in the artifact, outside Temporal
activity returns. The result reader verifies the authoritative ledger and emits
the original response body; legacy plain V1 result artifacts remain supported.
Worker loss without local-stop proof settles unconfirmed and blocks generation
handoff. Later stop evidence advances the ledger revision without changing the
terminal result. Finalization shares one bounded allowance across storage/cleanup.

Recorded failures before fixes:

- `tmp/cpa-independent-invocation-red.log`, `tmp/cpa-independent-workflow-red.log`
  and `tmp/cpa-independent-starter-red.log`: executable invocation/workflow/start
  contract assertions failed before implementation; no compile/setup substitute.
- `tmp/cpa-independent-result-envelope-red.json`: real SQL/local HTTP recovery
  succeeded, but result delivery exposed the internal envelope; the reader was
  corrected while preserving the response oracle.
- `tmp/cpa-claim-generation-deadline-red.json`: a claim did not persist its own
  generation deadline. Claim now resolves the accepted budget once; dispatch
  cannot reset it. Reservation/job deadlines remain distinct.
- `tmp/cpa-independent-dispatch-cleanup-red.log`: a failed dispatch acknowledgement
  discarded the retained claim's deadline, preventing bounded positive never-sent
  cleanup. The original claim is now retained on that error.
- `tmp/cpa-independent-metadata-bound-red.log`: oversized/control-bearing upstream
  correlation metadata violated the bounded observation contract; it is omitted.

Acceptance after fixes:

- `tmp/cpa-independent-final-fast.log`: all ten fast tasks accepted, clean cleanup.
- `tmp/cpa-independent-final-owner-race.log`: batch/API owner race tests passed.
- `tmp/cpa-independent-full-integration-race.log`: canonical real-service
  integration/race gate accepted, with no cleanup errors/warnings.
- `tmp/cpa-independent-final-sql-race.json`: final focused real SQL/local HTTP/race
  acceptance, including lost activity result, dropped observation acknowledgement,
  one actual provider HTTP request, unconfirmed/local-stop fencing and unchanged
  result projection; clean cleanup.
- Canonical ownership policy and whitespace checks pass. TEST-499 owns cheap
  invocation cases; TEST-500 workflow recovery; TEST-501 starter/notice behavior;
  TEST-502 the distinct SQL/HTTP recovery boundary.

This is not P3 completion: these lifecycles are not yet selected by the running
V1 pool/worker path. Fair paged selection, pool continuation with outstanding
attempts, first-claim window anchoring, provider/control worker queues, bounded
mailbox polling, generation adoption, the ordinary adapter, and real-stack
rollover/fault/scale/environment acceptance remain open. New invocation artifact
fault cases use an owned artifact double; real Garage/Temporal fault acceptance
remains a separate P4 gate. No provider inference/deployment/browser was run.

## 12. Status implementation checklist reconciliation

Runtime at `0ef61e3` passed hosted fast checks, CodeQL analyses and security
summary. Audit of the original P0-P9 tasks and RS-01..RS-17 cases is now recorded
in the status plan. P9.6 remains explicitly conditional/unperformed: no deployment
has been undertaken. This is a local implementation/readiness conclusion only.

The final audit adds a test for the actual reserve/update/lookup entrypoints when
both connections in the owned PostgreSQL pool are held. Each earlier caller
deadline maps to unavailable; neither registration nor update commits. The
original row remains running at its original revision, and the unsuccessful
new ID remains absent. `tmp/status-final-acquisition-audit-race.json` accepts all
request-status SQL/HTTP/TCP/race cases with empty cleanup errors/warnings.
The existing cheap TEST-466 already covers the shared acquisition-budget invariant.

The remaining shared-admission work belongs to #134 and is not a status blocker.

## 13. Fair pool scheduling and real rollover checkpoint

The new V2 pool owns grants while independent attempt workflows own inference.
The running gateway's default enqueue path is still V1; production adoption is
not inferred from these new registrations or from this test bootstrap.

Implemented:

- Bounded SQL pages rotate across validated key scopes, then each scope's jobs,
  then FIFO items. Scope/job cursors persist in the existing HLLM PostgreSQL
  schema; returned pages contain references, budgets and timing only. Selection
  cannot reserve capacity or return an active/expired/unpersisted-intent item.
- Compact outstanding-permit identities and at most 64 pending start intents
  survive pool continuation. A 1,000-outstanding-permit pure-state case verifies
  a snapshot below 256 KiB; it is not a 1,000-provider V2 load result.
- Admission windows are identified separately from cooling episodes. Initial
  grants cannot mint a fresh quota window until a claim is positively observed;
  the first receipt anchors it without trusting the worker's wall clock. Only
  own-window refusals/never-sent work refund credit. Terminal throttling does not.
- Pool settlement applies once, keeps unresolved local execution in capacity,
  and carries current probe identity/restrictions through CAN. Successful probes
  keep their own new episode origin. Never-sent work uses a separate bounded
  worker-readiness gate, without halving CPA pace.
- Metadata starters run asynchronously in batches of 16, with at most four
  batches in flight. Periodic/notice-triggered bounded ledger pages catch lost
  completions; failed reads pause grants rather than replacing inference.
- Control and provider task queues run inside one worker lifecycle. The V1
  registrations remain available for explicit drain/handoff; V2 provider work
  is registered only on the provider activity queue. Startup failure rolls back
  the owned provider worker; normal Stop stops both once. The provider watches
  worker shutdown, cancels its context, and records local stop only after Execute
  returns. Metadata/artifact cleanup retains its bounded finalization allowance.

Failures retained before corrections:

- `tmp/cpa-queue-red.json`, `tmp/cpa-pool-state-red.log`, and
  `tmp/cpa-pool-workflow-red.log`: runtime assertions failed before implementation.
- Initial outage RED used the SDK inactivity timeout, which does not bound a
  workflow making endless virtual retry progress. That owned test process was
  identified by its exact test name/package cwd and terminated; no service or
  provider mutation occurred. The corrected bounded virtual-time oracle failed
  in `tmp/cpa-pool-init-outage-bounded-red.log`: initialization ignored a queued
  continuation. The pool now drains controls between bounded metadata attempts.
- `tmp/cpa-pool-recovery-timer-red.log`: empty local readiness rounded a two-second
  recovery floor to the generic five-second poll. Eligibility now arms its own
  earliest timer even when a local ready page is empty.
- `tmp/cpa-pool-probe-origin-red.log`: reopening retained the old episode's origin;
  current probe receipt is now preserved and used for the next open episode.
- `tmp/cpa-worker-stop-red.log`: shutdown did not cancel the provider context.
  The worker-stop watcher now requests cancellation and joins before finalization.
- The first broad fast/integration checks rejected a new Temporal test-file
  location. The test was moved into the already approved
  `jobs_temporal_integration_test.go`; the boundary guard was unchanged.
- A simultaneously launched second Docker track expired waiting for the daemon
  lock before test execution (`tmp/cpa-pool-full-temporal.json`). Its handle was
  terminal, with no test/provider work or cleanup error. Docker tracks were then
  run sequentially; this failure is not counted as acceptance.

Acceptance:

- `tmp/cpa-pool-boundary-fixed-fast.log`: all ten fast tasks accepted; clean cleanup.
- `tmp/cpa-pool-boundary-fixed-integration-race.log`: canonical backend real-service
  integration/race gate accepted; clean cleanup, including SQL fairness/claims,
  additive schema and retained request-status boundaries.
- `tmp/cpa-pool-workers-final-race.log`: pool state/workflows, worker ownership and
  shutdown races plus retained V1 offline history replay passed.
- `tmp/cpa-pool-full-temporal-boundary-fixed.json`: canonical real Temporal suite
  accepted with no cleanup errors/warnings. It includes the retained V1 1,000-call
  and cooldown/restart cases, plus the new V2 real rollover case. Whole-task wall
  time was 70,670 ms, CPU 25,500 ms and peak RSS 699.0 MiB, including compilation,
  setup and all cases; these are not server-only or CPA throughput measurements.
- The V2 case uses real Temporal/PostgreSQL/Garage, the production core/native
  HTTP/SSE encoder and authenticated NDJSON results. A held native connection
  survives a changed pool Run ID; another two-item job completes in the remaining
  slot while that original connection stays active. Four distinct requests and
  four validated results, peak overlap <=2, no leaked permits and sequence=4.
  Both the old and continued real pool histories replay successfully.
- The fixture explicitly attaches manifests to V2 before the unchanged job
  lifecycle enqueues them. This is an owned test bootstrap, not proof that the
  gateway's default enqueue path has switched or that V1 adoption is complete.

Next required work remains concrete: coalesced waiting/heartbeat publication,
retry-wait and monotonic-duration diagnostics, fatal worker error propagation,
explicit V1 quiesce/drain/generation handoff and compatible enqueue routing,
execution/finalization deadline contract, optional bounded recovery jitter,
the request-bound ordinary adapter, broader real lifecycle faults and mixed
1,000/10,000-call qualification, then environment and bounded CPA acceptance.
No application/provider configuration, deployment, public inference or browser
was performed. The complete #134 plan remains active.
