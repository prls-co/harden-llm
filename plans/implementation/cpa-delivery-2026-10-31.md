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

## 14. Runtime publication, failure ownership and V1 handoff

This checkpoint builds on `cd4c210` and keeps the full #134 goal active. Source
now has an explicit `batch-upgrade` operation; no selected environment has been
upgraded or deployed by this work.

Implemented runtime boundaries:

- Pending-job status publication is pool/generation scoped and paged (128 rows).
  Five-second liveness updates coalesce; actual gate transitions update their
  cause without inventing execution progress. Provider progress requires its
  exact invocation claim and coalesces per job. No prompt/output/history data
  enters publication. Versioned V2 publication preserves older workflow paths.
- Waiting reasons distinguish dispatch/capacity, worker readiness, account or
  provider restriction, estimated-window admission, persistence, rollover and
  unresolved execution. Unknown eligibility has no fabricated next-attempt time.
- Native durations use Go's monotonic measurement. A retained explicit refusal
  attributes the next attempt's waiting interval to retry wait, not first queue
  delay. A stale-episode refusal can refund its own current window credit without
  resetting the recovered episode; terminal/ambiguous inference still cannot.
- Fatal SDK worker errors are retained once, stop both owned queues and reach
  Start/Run owners. The gateway process returns a failure for its supervisor.
  Startup/shutdown are serialized so a fatal error during failed startup cannot
  stop a provider twice or stop a control worker that never started.
- Namespaced V1 controls quiesce grants and drain owned activities/settlement.
  Remaining jobs attach to SQL, then an additive generation fence advances into
  the V2 type using the same stable workflow ID. Controller window/restrictions
  and the monotonic sequence survive. Unresolved legacy activity failure blocks
  transfer without rewriting its outcome or replaying inference.
- Compatible Enqueue attaches a manifest before signaling, resolves the retained
  generation and cannot restart a closed V2 authority under a replacement ID.
  New V2 consumes obsolete upgrade controls crossing the boundary harmlessly.
- `batch-upgrade` queries live ownership and verifies the matching SQL generation.
  A read-only emptiness check permits initializing an empty Temporal authority
  before its marker is created; uncertain initial start retries the same ID.
  Existing state with a missing/terminated authority is not cold-started. The
  local command prints five-second progress and cannot cancel accepted work.
- Job expiry excludes current-permit items. Versioned job lifecycles expire
  unowned work once, then observe claimed finalization without scheduling inference.
  V2 exposes optional execution/finalization deadlines, retaining the old deadline
  field and its meaning. Cleanup allowance never extends provider execution.

Regression evidence:

- `tmp/cpa-worker-fatal-red.log` exposed an incomplete test fixture; it is not the
  fatal-callback regression. Corrected `tmp/cpa-worker-fatal-callback-red.log`
  proved missing lifecycle ownership; `tmp/cpa-worker-start-fatal-red.log` proved
  double-stop/unstarted-stop on fatal failed startup. Corrected race tests pass.
- `tmp/cpa-pool-wait-projection-red.log`, `tmp/cpa-pool-progress-red.json`, and
  `tmp/cpa-pool-publication-workflow-red.log` failed on absent waiting/publication
  before implementation. Real SQL now proves bounded scoped updates, exact-claim
  progress fencing, five-second coalescing and no fabricated last-progress time.
- `tmp/cpa-pool-stale-credit-red.log` proved a valid current-window refund was
  lost after episode recovery; refund and episode adaptation are now separate.
- `tmp/cpa-legacy-handoff-red.json` and
  `tmp/cpa-legacy-handoff-workflow-red.log` failed before handoff implementation.
  SQL rejects active/unresolved legacy work, preserves zero/180-second snapshots
  and artifacts, and acknowledges repeated metadata transfer without a reset.
- `tmp/cpa-finalization-red.json` proved expiry overwrote a claimed item and status
  omitted separate deadlines. `tmp/cpa-finalization-workflow-red.log` proved the
  lifecycle ended before claimed finalization. All owner/SQL regressions pass.
  The old queued-expiry fixture now reflects the actual expired-row mutation;
  the original expiry-call/final-outcome assertions are preserved and strengthened.

Final local gates:

- `tmp/cpa-runtime-complete-fast.log`: all ten fast tasks accepted, clean cleanup.
- `tmp/cpa-runtime-complete-integration-race.log`: canonical backend real-service
  integration/race accepted, clean cleanup.
- `tmp/cpa-runtime-full-temporal.json`: canonical real Temporal/PostgreSQL/Garage
  suite accepted, clean cleanup. Retained V1 1,000-call/restart cases still pass.
- The new real handoff case starts via the ordinary durable-job API, holds one
  V1 native call, observes quiescing with no extra grants, then releases it. The
  same workflow identity advances to V2, finishes remaining accepted work, reads
  mixed legacy/new result artifacts and routes a later API job into V2. Exactly
  four provider requests/results, peak overlap <=1; no test-only enqueue adapter.
- Empty authority initialization makes zero provider calls. A real V2 cooldown
  survives worker Stop/Start and completes 24 items with exactly 25 provider calls
  (one explicit refusal) and overlap <=2, retaining its eligibility timestamp.
- Real V2 rollover/replay and separate provider/control worker cases remain in
  the suite. Catalog IDs TEST-508..514 own the new distinct boundaries. Static
  ownership/registration and whitespace checks pass without weakening guards.

Operator instructions are in `docs/cpa-pool-operations.md`. No deployment,
provider configuration change, real CPA inference, browser or production
promotion occurred. New-head hosted checks remain separate from these local gates.

Remaining full-plan work includes optional bounded recovery jitter, first-claim
latency/resource qualification and remaining lost-ack/process/artifact faults,
the ordinary request-bound per-dispatch adapter and its status/cancellation/
retention integration, mixed 1,000/10,000-call verification, and the selected
environment plus explicitly bounded CPA acceptance. The batch runtime checkpoint
must not be presented as closing the ordinary-call admission bypass.

## 15. Ordinary live-waiter ownership and shared scheduling foundation

This checkpoint builds on published `611b814` and implements the metadata and
scheduler portion of P5. It does not yet install the ordinary provider adapter:
regular gateway inference still bypasses shared admission. The full #134 goal
and its original acceptance requirements remain active.

Implemented:

- Additive `batch_request_waiters` metadata records carry server request/owner/
  execution IDs, per-execution dispatch ordinal, accepted scope/deadline/budget,
  shared enqueue sequence, liveness and bounded waiting diagnostics. No prompt,
  response, batch manifest, artifact or reference-history record is created.
- Enqueue is identity-preserving on retry, serializes only its metadata cap check
  under the pool row and requires an active V2 generation. The source bounds live
  waiters at 10,000; this is not 10,000-call throughput acceptance.
- One fair selector rotates scopes, then owner/job queue sequences, then FIFO job
  items. Requests and jobs use the existing common sequence/cursor. Pending
  request intents and reservations are excluded without loading inference input.
- A reservation requires the exact accepted waiter. Claim and dispatch require
  its owner nonce and still-live, uncanceled metadata under row locks. A grant
  racing abandoned/expired waiting instead retains a positively never-dispatched
  settlement under the original permit ID, so pool capacity can settle safely.
- Cancel/expiry never marks claimed transport stopped. Refresh writes coalesce
  and cannot resurrect an expired owner. The metadata starter does not create a
  durable inference workflow for an ordinary request: its live caller must own
  execution. Successful ordinary dispatch settlement is metadata-only; job
  completion retains its existing required artifact invariant.
- Pool publication pages include waiting requests and jobs together, coalescing
  heartbeats and retaining only actual reasons/known eligibility floors.
- Ordinary metadata retention is 24 hours after the accepted deadline. Cleanup
  removes at most 128 rows per page and cannot delete an active/unknown claim.
  Settled permit deletion requires positive local stop and an acknowledgement
  scheduled only after the pool applied settlement. The bounded acknowledgement
  backlog survives continuation; failures retry metadata without inference.

Regression and final verification receipts:

- `tmp/cpa-request-owner-red.json` proved an ordinary permit could be reserved
  without live-owner metadata. `tmp/cpa-request-pool-red.log` proved the pool could
  not grant an ordinary owner without fabricating a batch identity.
- `tmp/cpa-request-owner-green.json` and
  `tmp/cpa-request-mixed-queue-green.json` passed focused real SQL/race checks for
  scope/owner fencing, cancellation/claim races, expired liveness, immutable
  metadata-only success, mixed fairness, waiting pages and retention safety.
- Review added two further RED cases: `tmp/cpa-request-ordinal-red.json` proved a
  second dispatch lost its ordinal; `tmp/cpa-request-retirement-wait-red.log`
  proved bounded retirement backpressure omitted its persistence waiting reason.
  Both are fixed and included in the final gates.
- `tmp/cpa-request-queue-final-owners.log`: all default-tag batch owner tests pass
  under the race detector, including metadata-only starter/retirement tests.
- `tmp/cpa-request-queue-final-fast.log`: all ten fast tasks accepted; cleanup
  errors/warnings empty.
- `tmp/cpa-request-queue-final-integration-race.log`: canonical backend SQL and
  real-service integration/race accepted; cleanup errors/warnings empty.
- `tmp/cpa-request-queue-final-temporal.json`: canonical real Temporal/PostgreSQL/
  Garage suite accepted, retaining V1/V2 handoff, cooldown, rollover and replay
  checks; cleanup errors/warnings empty. Ordinary HTTP adapter adoption and real
  mixed-provider execution are not established by these retained job tests.
- New catalog TEST-515/516 registration and whitespace checks pass. Prior source
  `611b814` hosted fast checks passed; hosted checks for this new checkpoint are
  a separate publication gate.

Next is the concrete request-bound dispatch adapter in stateful composition:
managed CPA target matching, exact per-dispatch permits for repair/retry calls,
original deadline/cancellation semantics, truthful admission status and native
entry diagnostics. Then complete the original lifecycle/timing faults, optional
recovery jitter, mixed 1,000/10,000-call resource/count qualification and selected
environment plus bounded CPA acceptance. No deployment, real CPA inference,
browser run or production promotion occurred in this checkpoint.

## 16. Ordinary provider admission and preserved OpenAI contracts

This checkpoint builds on `199ba15` and closes the configured ordinary-call
bypass in source. `HARDEN_LLM_BATCH_ENABLED=true` now composes ordinary managed
CPA dispatches with the same pool used by durable jobs. Serving requires the
explicit schema command and an active V2 authority; missing/V1/quiesced ownership
fails closed. No selected environment has been changed by this checkpoint.

Implemented boundaries:

- The stateful concrete executor wraps each actual dispatch, preserving caller
  model/options, native opaque request, cache operation identity and progress
  callbacks. Every repair/transport retry gets a distinct request/owner/permit
  and monotonic ordinal under one logical execution ID and original deadline.
  Cache hits, model discovery and other provider origins bypass CPA admission.
- Configured CPA origin matching covers connection aliases/paths, DNS case and
  trailing-dot/default-port spelling; it never trusts an incoming pool/scope
  selector. Service composition binds only the authenticated key scope.
- The live caller enqueues bounded metadata, wakes the existing authority,
  refreshes waiter liveness, claims its exact grant and confirms dispatch intent.
  It rechecks context/deadline before native execution. Lost claim/dispatch/wake
  acknowledgement cannot authorize inference; fresh-nonce cleanup is metadata
  only. The adapter cannot start/reset a pool or start a durable request workflow.
- Ordinary success settles metadata without artifacts/history or batch manifests.
  Local stop is asserted only on a normal executor return, never inferred from
  timeout/heartbeat/panic. Cleanup is bounded separately and cannot replay native
  inference or replace a known response. Logs separate admission queue time from
  monotonic native duration and preserve actual timing/usage uncertainty. V2
  independent-attempt logs now include their retained `retry_wait_ms` field.
- Explicit pre-output refusal retains caller retry policy and trusted floors.
  Post-entry transport failure (including absent positive header-write evidence),
  partial refusal and panic remain unconfirmed; automatic core replay is blocked.
  Compatible SDKs receive additive `x-should-retry: false` for unconfirmed outcomes,
  preserving existing OpenAI status/error shape and `Retry-After` rounding.
- Tracking native-entry evidence now begins at the concrete effect after admission,
  rather than at the earlier attempt observer. Queued cancellation is confirmed.
  `admission_wait` projects actual pool reasons with nullable eligibility; identical
  polling invents neither progress nor ETA. Existing JSON/final-only SSE remains.
- The explicit schema command recognizes the exact retained legacy tracking DDL
  before adding admission enums/checks. Accepted owner/status/deadline and unrelated
  data remain unchanged; incompatible schema still requires operator action.
- Temporal SDK ownership remains solely in the approved batch boundary. Stateful
  composition uses a two-method metadata signal port; gateway code uses public
  root types. No guard/allowlist was weakened. Profile Save/import probes retain
  their existing bounded probe and now share configured CPA admission as well.

Retained failures and final verification:

- `tmp/cpa-ordinary-adapter-red.log` proved the original bypass: one native call,
  zero queued requests despite admission being configured.
- `tmp/cpa-ordinary-adapter-first-green.log` initially contained a compile error
  from treating internal usage methods as fields; it was corrected before owner
  verification and is not a successful gate receipt.
- `tmp/cpa-ordinary-shared-temporal.json` exposed a fixture assumption that every
  request was a batch request requiring native streaming. The oracle now retains
  that exact rule for jobs and separately checks ordinary native defaults. Frontend
  SSE still projects a completed result; the runtime was not changed to force
  ordinary native streaming. `tmp/cpa-ordinary-shared-temporal-green.json` passed.
- `tmp/cpa-ordinary-final-fast.log` and the corresponding first integration/race
  log retained architecture-guard failures. SDK imports moved into batch and its
  approved real-service test file, and gateway tests use public root categories.
  `tmp/cpa-ordinary-final-boundaries.log` passes unchanged guards.
- `tmp/cpa-ordinary-verified-owners.log` and verified fast/integration receipts
  exposed a fault-test stub that did not invoke its configured wake error. The stub
  now injects that failure; zero native calls remain the unchanged oracle.
- `tmp/cpa-ordinary-published-owners.log`: focused adapter/HTTP race tests pass.
- `tmp/cpa-ordinary-published-fast.log`: all ten fast tasks accepted; cleanup empty.
- `tmp/cpa-ordinary-published-integration-race.log`: canonical full backend SQL/
  real-service/race gate accepted; cleanup errors/warnings empty.
- `tmp/cpa-ordinary-published-temporal.json`: canonical real Temporal/PostgreSQL/
  Garage suite accepted, including both new ordinary cases and retained V1/V2
  handoff/cooldown/rollover/history-replay oracles; cleanup empty.
- The shared-cap case holds one durable native call while two authenticated
  ordinary scopes wait behind the same cap. Cross-key tracking returns 404.
  A third queued call expires without native entry. Both OpenAI endpoints pass
  JSON and SSE; a warm cache hit creates no permit. Exactly eight native HTTP
  requests total, peak overlap <=1, one batch manifest/item and zero history rows;
  ordinary metadata has positive local stop/settlement without result artifacts.
- The shared-cooldown case begins with an ordinary 429, observes its retry floor
  and a durable job waiting under the same restriction, then completes both in
  exactly three native HTTP requests. The ordinary retry retains one execution
  identity with separate dispatch ordinals 1/2; peak overlap <=2.
- TEST-517..519 registration and whitespace checks pass. Hosted checks on prior
  `199ba15` passed; new-head hosted checks remain separate from local acceptance.

The full #134 goal remains active. Next: mixed 1,000/10,000-call ID/count/resource
qualification, remaining lifecycle/timing/fault cases, optional bounded recovery
jitter, and selected-environment plus bounded real CPA acceptance. These bounded
synthetic compatibility cases do not establish CPA quotas, token limits or live
throughput. No deployment, real CPA inference, browser or production promotion
occurred. Operator/status docs and the bundled OpenAPI reflect the new source.


## 17. Mixed-load qualification and bounded control overhead

The published checkpoint is `cce83eb`; this section records the subsequent local
candidate until publication is explicitly recorded. Neither local endurance
qualification nor hosted fast checks establish deployment or CPA throughput.

TEST-520 adds two explicitly selected local profiles: 800 durable plus 200
ordinary tasks, and 9,000 durable plus 1,000 ordinary tasks. Two separately
composed API instances use separate PostgreSQL pools and two bearer scopes with
one V2 authority. The TLS fixture takes 5 ms and has no external quota. The
provider cap remains 128; ordinary requests retain their original 60-second
deadlines and grants retain their five-second start lease. Each accepted profile
requires exact native/result identities and counts, no unknown/leaked permits,
no extra never-sent attempt workflows, result isolation, ordinary artifact/history
boundaries, safe continuation and bounded process resources. Individual profiles
run sequentially because heap/goroutine counters cover the entire Go process;
`-parallel=1` is machine-checked in the canonical runner contract. Client requests
and provider activities remain concurrent.

Retained failures identify distinct control costs rather than provider quotas:

- `tmp/cpa-mixed-1000-first.json`: admission failures before complete native
  execution. Stage diagnostics were added without logging prompts, keys or raw
  errors; this first run alone does not establish which operation failed.
- `tmp/cpa-mixed-1000-diagnostic.json`: all 1,000 native requests/results completed
  in about 94.5 seconds, but extra never-sent reservations/workflows failed the
  unchanged healthy-load metadata-count oracle.
- `tmp/cpa-mixed-1000-bounded-metadata.json`: all 1,000 native requests/results
  completed in about 69.4 seconds, still with excess never-sent reservations.
- `tmp/cpa-mixed-1000-pollers8.json`: accepted an earlier candidate in 46.062
  seconds, exactly 1,000 native requests, 800 attempt workflows, zero ordinary
  errors, 42,136,224-byte observed Go heap and 1,125 goroutines. Actual native
  overlap peaked at three under the 5 ms fixture. This does not qualify 128
  simultaneous real provider calls or the later source without another run.
- `tmp/cpa-mixed-10000-first.json`: all 1,000 ordinary requests failed with zero
  native calls. Diagnostics showed wake/metadata pressure, not provider refusal.
- `tmp/cpa-mixed-10000-coalesced.json`: wake coalescing and the FIFO index allowed
  native work, but ordinary errors remained. PostgreSQL canceled ready selection
  and enqueue statements. The old selector repeated its eligible-owner scan up
  to 64 times while holding the generation lock inside one 600 ms transaction.
- `tmp/cpa-mixed-page-red.json`: a focused real PostgreSQL regression with 1,000
  live ordinary waiters reproduces that transaction failure. The same unchanged
  scope/owner/FIFO oracle passes with one set-based page query in
  `tmp/cpa-mixed-page-green.json`.
- `tmp/cpa-mixed-10000-paged.json`: 995/1,000 ordinary results in about 22 seconds;
  five pre-native failures remain, so the profile is rejected.
- `tmp/cpa-claim-hint-red.log`: a blocked best-effort claim notice delayed native
  entry despite a confirmed ledger claim. `tmp/cpa-claim-hint-green.log` passes
  after the bounded request-owned sender runs alongside inference and is canceled
  and joined before finalization. It cannot grant permission or replay inference.
- `tmp/cpa-mixed-10000-claim-hint.json`: 932/1,000 ordinary results; claim/dispatch
  failures show that notification overlap alone does not remove metadata pressure.
  This is retained as failed, not retried into a passing receipt.

The candidate bounds ordinary metadata to four operations per API instance:
separate two-slot lanes for enqueue/polling and existing-permit/cancellation work.
Waiting happens before the short SQL transaction deadline, under the original
caller context, and projects `persistence_wait` with no invented ETA. No slot
spans native I/O. Wake hints coalesce overlapping RPCs and successful acknowledgments
within 50 ms; every caller still positively claims its own permit. Failed wake
acks are not cached, settlement notices remain distinct, and the existing bounded
manifest scan covers newly committed waiters. The set-based SQL page preserves
scope/owner rotation and FIFO exclusions and updates only the final owner cursor
per selected scope. The explicit schema command adds/verifies the partial FIFO
index while preserving accepted work; serving paths execute no DDL.

Workers use 2..8 control/provider pollers according to configured concurrency,
without changing the provider execution cap. A version-gated readiness change
coalesces one wave of never-sent expiries into one backoff floor; legacy histories
retain their preceding behavior. TEST-521 covers metadata bounds, caller deadline
reporting, nonblocking claim hints, wake acknowledgments and legacy/current
readiness behavior. TEST-503 retains real SQL fairness/schema/count assertions.

`tmp/cpa-mixed-10000-priority.json` is terminal and rejected: all 1,000 ordinary
calls succeeded, but the durable jobs exceeded their eight-minute execution
budget. The run recorded 7,858 native requests in 487.819 seconds; the first
waited job ended with 683 successes and 317 deadline failures. Partial result
collection is not a complete 10,000-result receipt. Cleanup errors/warnings are
empty. A read-only midpoint ledger observation showed 147 never-dispatched job
attempts; grant-to-claim latency was 1.625 seconds at p50 and 4.161 seconds at p95,
close to the unchanged five-second lease. No real CPA refusal was involved.

The next scale work is to bound unclaimed handoffs independently of active native
capacity and provide prompt claim readback for long calls. It must preserve the
128 active-permit cap, original deadlines, one-attempt inference, fencing and
unknown-outcome behavior. Increasing deadlines or dropping the exact healthy-load
count oracle would hide this bottleneck and is not an accepted fix. Final scale acceptance remains unproved; the successful semantic gates below do
not qualify this checkpoint for full-scale delivery.

Verification so far:

- `tmp/cpa-mixed-final-owners.log`: focused stateful/batch race checks pass.
- `tmp/cpa-mixed-final-fifo.json`: real PostgreSQL queue/index and independent
  uneven-owner reference oracles pass; cleanup empty.
- `tmp/cpa-mixed-final-fast.log`: all ten fast tasks accepted; cleanup empty.
- `tmp/cpa-mixed-lanes-regression.log`: the new lane probe initially omitted the
  required client connection and failed during fixture construction. The corrected
  probe uses an unused synthetic connection. `tmp/cpa-mixed-lanes-verified.log`
  passes the same lane/deadline/native-overlap oracles under the race detector.
- `tmp/cpa-mixed-final-integration-race.json`: retained failed gate. The unchanged
  exact-17-MiB history case positively saved its record but took 2.072 seconds
  against a two-second wall bound while fast checks ran concurrently. No history
  implementation, assertion or deadline was changed.
- `tmp/cpa-mixed-isolated-integration-race.json`: the same canonical full gate
  accepted after competing fast checks ended; cleanup errors/warnings empty.
  This isolated receipt does not erase the loaded-host timing failure above.
- `tmp/cpa-mixed-final-temporal.json`: the canonical full real Temporal/PostgreSQL/
  Garage suite accepted, including shared ordinary cap/cooldown, preserved wire
  behavior, V1/V2 handoff, active-call rollover and retained history replay;
  cleanup errors/warnings empty.

- `tmp/cpa-mixed-final-1000.json`: rejected by the unchanged overhead oracle.
  Exactly 1,000 native calls and distinct validated results completed in 44.009
  seconds, with zero ordinary errors and no unsettled/unknown permits. One
  never-dispatched job reservation produced 801 attempt workflows instead of 800.
  Observed Go heap peaked at 44,686,856 bytes and goroutines at 1,171. The
  unclaimed-handoff defect therefore remains relevant even at the 1,000 profile.

A redacted machine-readable receipt is retained at
`plans/evidence/harden-llm/cpa-admission/2026-10-31-control-overhead.json`.
It records source-file hashes and both failed load gates, without prompts,
credentials or native payloads. The PR remains draft; these scale failures are
not replaced by the earlier candidate's 1,000 pass.

Remaining full-scope work includes lifecycle/timing fault
acceptance, optional deterministic recovery jitter, selected-environment rollout
and explicitly selected bounded CPA acceptance. No production promotion, browser
or real CPA inference occurred in this checkpoint.


Implementation checkpoint `1d25697bee38b96fb16216ed4d77af0ee77eadec` is pushed to
`feat/cpa-shared-admission-v2` in draft PR #141. The source-file digests in the
redacted receipt identify that implementation. The small summary is explicitly
tracked despite the repository's general raw-evidence ignore rule; raw logs and
native payloads remain uncommitted. Local/tracking/remote refs were verified.
Hosted checks for this new checkpoint are separate and must be inspected before
claiming exact-head CI acceptance. The full #134 goal stays active.


## 18. Bounded unclaimed handoffs and prompt claim readback candidate

Hosted run `41210700556` passed on published checkpoint `2c3ad72`; this section
records newer local source and is not a published/qualified scale claim.

The pool now tracks positive ledger claims separately from workflow-start
acknowledgements. The first candidate admits at most 16 not-yet-claimed grants while
retaining its configured active-provider cap (128 by default). A confirmed claim
opens one handoff slot but does not release active capacity. Start acknowledgement
alone cannot open a handoff slot. Pending claims project `dispatch_pending` with
no ETA. The new gate and claimed bits survive continuation; old histories keep
zero/disabled pending-claim behavior through a dedicated Temporal version marker.
Claim tracking must be version-gated too, so legacy history decisions do not gain
new queue/progress transitions during replay.

A durable invocation sends a best-effort revision-zero notice alongside its
metadata/native work after a positive claim. The activity owns one bounded sender
and joins it on return, panic and cancellation. The notice is only a request for
SQL readback; the actor never treats it as permission, stop evidence, a released
permit or a trusted claim timestamp. Periodic ledger reads cover lost notices.
Long native calls can therefore anchor their window/recovery state promptly,
without waiting for completion or blocking their dispatch on a signal RPC.

- `tmp/cpa-handoff-bound-red.log` reproduces unlimited pending claims after
  workflow-start acknowledgement; the independent active-cap oracle remains 128.
- `tmp/cpa-handoff-bound-green.log` passes the pending/continuation/cap oracles.
  The retained 1,000-permit legacy snapshot test still exercises its original
  maximum start-intent shape, explicitly selecting legacy mode.
- `tmp/cpa-durable-claim-hint-red.log` proves the missing in-flight claim notice.
- `tmp/cpa-handoff-and-claim-owners.log` passes all stateful/batch race tests,
  including bounded/nonblocking claim-notice ownership.

`tmp/cpa-handoff-1000.json` accepted the unchanged 1,000 oracle: exactly 1,000
native requests/distinct validated results and ledger attempts, 800 durable
workflows, zero ordinary errors and zero never-dispatched attempts. Elapsed time
was 48.574 seconds, observed Go heap 36,705,440 bytes and peak goroutines 1,090.
Grant-to-claim p50/p95/max were 295.610/568.256/973.465 ms. Actual native overlap
peaked at four with the 5 ms fixture. The pool continued safely; cleanup empty.
This is the newer candidate's local synthetic acceptance, not CPA throughput.

The 16-slot 10,000 profile is running with no never-dispatched attempts at the
midpoint, but throughput remains low. A separate 32-slot candidate is prepared
using a new version branch; recorded version-one histories keep their immutable
16-slot behavior. Old histories without the marker retain disabled behavior.
The earlier 1,000 receipt qualifies 16 slots only, not this newer default. Both
new profiles and the real Temporal/replay and broad regression gates still need
qualification before publication. This candidate changes no provider cap, deadline,
start lease, inference retry rule, native count or result-identity assertion.


The 16-slot full profile `tmp/cpa-handoff-10000.json` is rejected: 7,044 native
calls in 481.817 seconds, all 1,000 ordinary calls successful, but durable jobs
expired. Its final ledger snapshot has 7,046 attempts and two never-dispatched
attempts; a midpoint zero-expiry observation is not a final zero-expiry claim.
Claim p50/p95/max were 396.932/896.658/2,299.528 ms. Cleanup is empty.

The 32-slot candidate (`bounded-pending-claims` version two; recorded version one
still means exactly 16) accepted `tmp/cpa-handoff32-1000.json`: exactly 1,000
native/results/ledger attempts and 800 durable workflows, zero ordinary errors
and never-dispatched attempts, 43.964 seconds. Claim p95/max were 910.198/1,362.238
ms; cleanup empty. The SDK version-branch test covers absent/one/two marker values,
and the legacy claim-readback test requires byte-identical historical state and
no extra queue transition.

An additional controlled poller candidate changes only workflow-task polling:
32 rather than eight at the default 128 native cap. Both activity queues retain
eight pollers, all activity execution limits stay unchanged, and the pending
handoff bound remains 32. `tmp/cpa-workflow-pollers32-red.log` records the preceding
worker-options contract; `tmp/cpa-workflow-pollers32-owners.log` passes the stateful/
batch race suite with the new option. `tmp/cpa-pollers32-1000.json` accepts all
unchanged count/identity/resource oracles in 41.804 seconds, with zero never-sent
attempts and claim p95 748.072 ms. The improvement over 43.964 seconds is modest
and is not proof that polling is the only control bottleneck. Cleanup is empty.

The corresponding 10,000 profile is running. None of these synthetic observations
establishes a CPA quota or authorizes native retries for uncertain outcomes.


The 32-handoff/32-workflow-poller full profile
`tmp/cpa-pollers32-10000.json` is rejected: 7,937 native requests, zero
never-dispatched attempts, but original job budgets expired. The missing-call
causes were not fully retained in its earlier report and must not be inferred
solely from native counts.

Further controlled diagnostics/experiments:

- `tmp/cpa-timing-1000.json` passes with retained current-pool-run timing
  aggregates and five-second state observations. Those overlapping span totals
  are not additive wall-time/CPU attribution and do not cover every CAN run.
- `tmp/cpa-short-hints-1000.json` passes but 100 ms suppression of short-call
  claim hints did not improve elapsed time (46.222 seconds). That speculative
  optimization and its feature-specific tests were removed; prompt claim hints
  remain. The native/count/deadline oracles were never weakened.
- `tmp/cpa-publication-1000.json` passes in 44.958 seconds with zero never-sent
  attempts. Version-gated coalescing reduces sampled publication activities from
  53/61 to seven. It coalesces only empty/dispatch/capacity transitions within
  five seconds; trusted floors, restrictions, persistence/worker failures and
  unresolved execution still publish immediately, including their recovery.
  `tmp/cpa-publication-red.log` and `tmp/cpa-publication-green.log` retain the
  focused regression. SDK version tests cover legacy/current combinations.
- `tmp/cpa-control32-profile-1000.json` passes with 32 control-activity pollers,
  32 workflow pollers and eight provider-activity pollers; native cap remains 128.
  This change alone does not prove a throughput gain. Its diagnostic CPU profile
  used 20.49 CPU seconds over 48.07 seconds, about 0.43 core, not app CPU saturation.
- The exact pinned Temporal image defaults to four history shards with an empty
  dynamic-config file. A read-only filtered configuration check also confirmed
  four shards on the existing shared cluster. No shared service was changed.
  A fresh owned 128-shard test cluster accepted the unchanged 1,000 profile in
  `tmp/cpa-shards128-1000.json` at 34.197 seconds. This is an experimental runtime
  comparison, not a CPA quota result or an adopted canonical fixture change.
- `tmp/cpa-shards128-10000.json` remains rejected: 9,993 native requests in
  516.576 seconds, all ordinary calls successful, final ledger 10,006 attempts
  including six never-sent attempts, with durable failures. Its old report lacks
  a full per-outcome error histogram; not every missing call has a proven cause.
  The later reporter now retains outcome/error and dispatch/stop evidence plus
  pool acquisition counters, including on failure.
- `tmp/cpa-shards128-resource-sample.json` records one owned-runtime observation:
  Temporal 53.04% CPU/168.8 MiB, PostgreSQL 96.31% CPU/225 MiB, Garage 4.52%
  CPU/5.457 MiB; ten SQL samples peaked at 70 connections, mostly idle. This is
  not a peak-resource certification. Raw sampling contains no inference payloads.

The [Temporal service configuration reference](https://docs.temporal.io/references/service-configuration)
confirms history-shard count is fixed at cluster creation. A shared-cluster
migration is a separate infrastructure-owner decision; changing an environment
value against accepted state is not a supported implementation shortcut.

A real pool regression now proves another concrete defect:
`tmp/cpa-ledger-queue-red.json` fails because connection waiting consumed the
600 ms transaction budget. `tmp/cpa-ledger-queue-green.json` passes after pgx
connection admission moves before that deadline. Acquisition has its own five-
second maximum under the original earlier caller context; transaction 600 ms
and statement 500 ms limits remain. A canceled waiter enters no SQL. This is
TEST-522, a real acquisition-trace boundary rather than a timing-only mock.

TEST-523/524 cover an uncertain claim COMMIT followed by positive same-fresh-nonce
cleanup before provider entry. Missing/foreign proof stays unconfirmed. A valid
local-stop CAS may stage known never-entry; it does not replay inference or
rewrite a terminal result. `tmp/cpa-lost-claim-cleanup-red.log` and green owner
receipt show the regression; `tmp/cpa-ledger-recovery-sql-verified.json` proves
real committed-claim/readback-loss recovery with zero native calls, no artifact
and no consumed provider retry. The initial runner invocation was rejected for
assigning one test ID to two tasks; unit TEST-523 and SQL TEST-524 now have
separate canonical owners. No test ran in that rejected invocation.

These newer source changes still require fresh scale and full semantic/replay
acceptance before publication. The canonical fixture remains four shards. See
`plans/implementation/cpa-acceptance-audit-2026-10-31.md` for the requirement/fault
matrix and concrete remaining fault cases. Optional deterministic jitter and P6
remain open; the full goal remains active.

### 18.1. Current queue/claim candidate scale outcome

Both profiles below use the canonical four-shard owned Temporal fixture and the
same source, including the separate connection-acquisition budget and positive
same-owner uncertain-claim cleanup. No provider cap, original request/job
deadline, five-second grant lease or count oracle changed.

- `tmp/cpa-ledger-admission-1000.json` is accepted. Numeric report
  `tmp/cpa-mixed-1000-398434.json` records 44.299 seconds, exactly 1,000 native
  requests/distinct results/settled completed attempts, 800 durable attempt
  workflows, zero never-dispatched attempts and zero ordinary errors. Peak
  observed native overlap is five against cap 128. Grant-to-claim p95 is 645.308
  ms and maximum 1,493.999 ms. Heap/goroutine samples peak at 43,178,552 bytes
  and 1,181. Pool acquisition duration is 4,076 ms aggregated across concurrent
  operations, not additive wall-time attribution.
- `tmp/cpa-ledger-admission-10000.json` is rejected. Numeric report
  `tmp/cpa-mixed-10000-430175.json` records 8,021 separate native requests in
  485.478 seconds. All 1,000 ordinary calls succeeded; durable work exceeded the
  unchanged eight-minute budget. The retained SQL outcome snapshot confirms
  7,021 completed durable attempts and 1,000 completed ordinary attempts, plus
  25 never-claimed expired reservations and one positively stopped never-entry
  with dispatch intent (`dispatch_deadline_exceeded`). It does not show an
  unconfirmed outcome at that snapshot. The failed profile is still incomplete:
  `DistinctResults=1000` covers collected ordinary results, not complete durable
  result validation. Unset post-success counters are not evidence of zero work.
  Grant-to-claim p95 is 1,281.586 ms and maximum 3,220.734 ms; peak native overlap
  is six. Reported heap/goroutine samples peak at 142,446,968 bytes and 5,213.
  Both runner cleanup error/warning lists are empty.

New bounded history aggregates cover only the final pool continuation run.
That run contains 206 workflow-task samples, with maximum queue delay 2,846.475
ms, and 86 `StartAttempts` execution samples with maximum 1,414.918 ms. These
observations support investigating scheduling/control latency; parallel span
totals cannot be added to explain whole-campaign wall time. This synthetic
fixture has no upstream quota and does not measure CPA concurrency or rate
limits. More identical load reruns without a demonstrated source/runtime change
would add little evidence. Next performance work should reproduce and reduce
per-item pool wake/start overhead with focused tests before another full run.

### 18.2. Checkpoint verification

`tmp/cpa-checkpoint-current-fast.log` retains a rejected fast run. Its unchanged
native-window test observed 44.172 ms between fixture entries against its 45 ms
minimum: real host scheduling delayed the first callback after the scheduler's
grant/window anchor. This is not an accepted gate. The test now runs the actual
scheduler, timers and worker goroutines inside Go's `testing/synctest` clock;
the same six calls, two-per-window policy, 50 ms window and 45 ms assertion
remain. No runtime scheduler, policy or bound changed.
`tmp/cpa-window-clock-regression.log` passes the focused race regression. The
subsequent full gate must be recorded separately; the rejected receipt remains.

`tmp/cpa-checkpoint-current-integration-race.json` accepts the full canonical
backend integration/race track (85.260 seconds).
`tmp/cpa-checkpoint-current-temporal.json` accepts the canonical real Temporal,
PostgreSQL and Garage track (43.095 seconds), including active-call rollover and
history replay on the new version branches. Both report empty cleanup errors
and warnings. These semantic gates do not turn the rejected 10,000 profile into
a pass. No browser or real CPA inference ran.

`tmp/cpa-checkpoint-virtual-clock-fast.log` accepts all ten fast tasks after the
virtual-clock regression fix. Authoritative receipt: `tmp/test-feedback/runner-1793465635000-585960-e6346c74a7f2676c.json`.
Cleanup errors/warnings are empty. Canonical test registration and whitespace
checks also pass. The redacted checkpoint metrics are in
`plans/evidence/harden-llm/cpa-admission/2026-10-31-bounded-handoff.json`; raw
provider/test logs remain private and ignored. This checkpoint is suitable for
publishing to draft #141, not for marking #134 complete.
