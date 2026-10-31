# CPA admission acceptance audit

## 1. Scope and evidence rule

This audit tracks #134 separately from status lookup #136. It does not replace
`plans/cpa-durable-execution-architecture.md` or narrow its P0–P6 requirements.
Published checkpoint `2c3ad72` has passing hosted fast checks and retained local
semantic gates. New handoff/publication code remains a local candidate until its
own gates and source identity are recorded. A test name below is a coverage
pointer, not proof that the current candidate has passed that test.

## 2. Delivery requirements

| Requirement | Authoritative coverage or remaining work |
| --- | --- |
| #136 ordinary status lookup, scope isolation, unchanged OpenAI JSON/SSE, stale/terminal projection | Ready-for-review PR #140, source `326767d`; its detailed P0–P9/RS acceptance audit is in `plans/request-status-lookup-implementation-plan.md`. Unmerged/undeployed. |
| P0 existing accepted-job/source ownership inventory and stable authority | Delivery sections 10–16; explicit schema and V1 drain/handoff, no serving-path DDL or pool reset. |
| P1 immutable 180-second legacy / 300-second new / 900-second maximum budgets and separate finalization | `budget_test.go`, `budget_integration_test.go`, gateway OpenAPI/client fixtures; current-candidate gate still required. |
| P2 claim/start/settlement fencing and immutable artifacts | `ledger_integration_test.go`, `attempt_starter_test.go`, `attempt_execution_integration_test.go`; no inference retry on unknown outcomes. |
| P3 fairness, one probe, restriction/window continuity, worker ownership | `queue_integration_test.go`, `pool_state_test.go`, `pool_workflow_test.go`, `workers_test.go`; new pending claims/publication version branches need real replay verification. |
| Optional operator recovery jitter, default zero, deterministic padding once after trusted floor | Missing capability. Implement the specified 0–1,000 ms policy/config contract with deterministic tests and history-safe behavior; do not add random dispatch delay. |
| P4 real active-call rollover and independent eligible work | `TestTemporalBatchV2SlowCallSurvivesRolloverAndOtherJobFinishes` exercises real Temporal/PostgreSQL/Garage plus native HTTP, complete counts and history replay. Latest candidate must rerun it. |
| P5 ordinary/durable shared cap/cooldown and caller compatibility | `TestTemporalBatchOrdinaryShareAdmissionAndKeepOpenAIWire`, `TestTemporalBatchOrdinaryRefusalSharesCooldownAndRetryOwnership`; preserve cancellation, cache bypass, scope boundaries and metadata-only ordinary results. |
| P5 1,000 separate native calls and complete results | Latest four-shard candidate passes in 44.299 seconds: 1,000 native calls, distinct results and settled attempts; 800 durable attempt workflows, no never-dispatched attempts or ordinary errors. Receipt `tmp/cpa-ledger-admission-1000.json`. This is synthetic local-provider acceptance. |
| P5 10,000 mixed calls, exact counts and bounded resources | Still rejected. The same candidate on four shards recorded 8,021 native calls in 485.478 seconds; all 1,000 ordinary calls succeeded, but durable jobs exceeded the unchanged eight-minute budget. The retained ledger snapshot has 7,021 completed durable attempts plus 26 never-dispatched attempts, not 10,000 validated results. Receipt `tmp/cpa-ledger-admission-10000.json`. |
| P6 selected environment, namespace/network, scoped storage, schema/images and HTTP checks | Not deployed/qualified. Record exact branch/SHA/images/environment and browser-free HTTP acceptance. |
| P6 representative bounded CPA request campaign | Not run in this implementation checkpoint. Explicit opt-in, independent from deployment smoke tests. Synthetic throughput is not a CPA quota measurement. |

## 3. Required fault coverage

| Named fault | Existing source-level evidence | Remaining evidence limit |
| --- | --- | --- |
| Lost start acknowledgement | `TestAttemptStarterParallelBoundedAndSameIdentity` returns lost acknowledgement after the same stable start identity; pool workflow retains unknown intents across CAN | Add a real-Temporal positive-start/lost-caller-ack test if certifying this RPC boundary end to end. |
| Provider activity redelivery | `TestInvocationClaimsOnceAndKeepsResponseOutOfReturn`, `TestIndependentAttemptClaimLoserWaitsForOriginalOwner`, real SQL competing claims | Claim/dispatch invariant covered; no claim of a real server-induced task redelivery. |
| Claim COMMIT ambiguity | `TestAttemptUnknownClaimCommit` covers committed/readback found, rollback absent, readback failure and foreign owner against real PostgreSQL | Rerun canonical SQL/race gate on the selected candidate. |
| Stale reservation expiry racing claim | `TestAttemptReservationExpiryCannotRevokeClaim`, `TestAttemptNeverSentExpiryAndLateClaim` cover both fenced states | Add a transaction-lock race case to exercise a blocked stale reader and expiry concurrently. |
| Crash after HTTP dispatch | Panic/unknown local-stop evidence, lost provider return and real SQL local-stop/generation fencing | Protocol state is covered; a process-kill boundary is not established by a panic fixture. Keep an explicit acceptance gap if claiming actual crash recovery. |
| Late result after expiry | `TestInvocationArtifactRejectsDifferentIdentityAndLateOutcome`, budget/finalization SQL cases | Check immutable terminal/late-artifact behavior together; do not treat an artifact's existence as successful inference acceptance. |
| Lost settlement acknowledgement | `TestAttemptUnknownSettlementCommitIsMetadataOnly`, `TestIndependentAttemptRetriesOnlySettlementMetadata` | Real COMMIT/readback and same retained observation covered; never rerun native inference. |
| Duplicate or boundary completion signal | Pool settlement revision/idempotence tests plus real active rollover/history replay | Latest candidate must rerun these with its new version markers. |
| Persistence outage | Failed staging preserves known observation; metadata-only SDK retries and unknown-COMMIT cases | Add a deliberate real ledger-write blockage/recovery case while a native response/artifact completes; validate one HTTP request/result and released capacity afterward. |
| Account pause across idle/CAN | Restricted state carried in `TestPoolWorkflowRetainsUnknownStartsAndRestrictionsAcrossContinuation`, classification/pause logic | Add an explicit policy-result-to-idle-to-CAN assertion rather than relying only on a pre-paused input fixture. |
| Dispatch lag across a window boundary | `TestPoolStateWindowAnchorsOnClaimAndNeverRefillsUnconfirmedStarts`; grant/claim timing recorded under load | Preserve the first confirmed anchor, no unclaimed quota refill and no later claim postponement. |
| Policy/cap changes with active work | Immutable SQL accepted-budget tests and controller policy path | Add an explicit active-cap shrink/increase/restriction continuity case through the SDK policy signal. |

## 4. Concrete next fault cases

1. Wrap the real Temporal client used by `StartAttempts`: execute the first start,
   then return a synthetic lost acknowledgement. Retry the exact permit/workflow
   identity. Assert one ledger row/ordinal, one provider HTTP request and one
   authoritative artifact/result; retain capacity until positive settlement.
2. Hold a claimed attempt row with a real PostgreSQL transaction after the local
   HTTP fixture has entered. Release the provider response while metadata writes
   are blocked. Observe a metadata activity retry, then release the row lock and
   require successful same-observation settlement, one native request and no leak.
   The block must be test-owned and must not stop shared infrastructure.
3. Use a real row lock and coordinated claim/expiry calls to exercise a stale
   reservation read after expiry wins; retain the existing claim-wins case too.
4. In SDK virtual time, apply an account-policy result, leave the queue idle,
   continue the pool and verify it remains paused. Send policy changes while
   permits are active and require that no active permit or accepted item budget
   is reset and no new work bypasses the new cap.
5. Keep a genuine process-death test distinct from logical unknown-outcome tests
   if certifying that boundary. It must kill only its own worker process after
   confirmed local HTTP entry, never replay uncertain inference, retain capacity
   without positive stop evidence, and preserve the terminal unconfirmed result
   when audited stop evidence later advances the settlement revision.

## 5. Verification and publication order

1. Focused T0/T1 owner/race regressions and unchanged architecture guards.
2. Load profiles in isolation, with exact IDs/counts, original deadlines and the
   five-second lease. Do not reduce assertions or extend deadlines to obtain a pass.
3. Full fast gate, then sequential owned SQL/race and real Temporal/replay tracks.
   Avoid competing CPU-heavy checks during resource/timing qualification.
4. Record source hashes, accepted and rejected receipts, limitations and cleanup.
   Push the verified checkpoint and update draft PR #141/#134 accurately.
5. Complete remaining fault/jitter acceptance, then P6 environment/provider gates.
   Merge/main promotion and production deployment remain separate user decisions.

## 6. Immediate implementation sequence

The design is ready; remaining questions concern executable acceptance rather
than a new architecture or CPA upgrade.

1. Publish the bounded-handoff/connection-wait/claim-cleanup checkpoint only with
   its own fast, SQL/race and Temporal/replay receipts. Keep #141 draft and retain
   both scale outcomes. #140 can proceed through review independently.
2. Reproduce control-path amplification with a cheap test before changing the
   scheduler: concurrent durable claims/settlements currently emit individual
   pool signals even though a signal only requests a read of all outstanding
   ledger entries. Measure signal count and authoritative readback behavior.
3. If that test confirms amplification, coalesce successful durable wake hints
   per worker/pool with a bounded collection interval and a mandatory trailing
   batch for arrivals after a batch seals. Keep a bounded RPC context and owned
   lifetime. A failed hint must not be cached as delivered; canceled followers
   must not discard other callers' hints. Do not change ordinary notice
   forwarding in the first patch. Restrictions, refusals and uncertain outcomes
   need prompt notification. SQL claims/settlements remain authoritative.
4. Prove all settlements are read, no permit is released from a hint alone,
   arrivals during a signal RPC produce another wake, and interruption/CAN loses
   no durable work. Provider requests remain separate; native cap, accepted
   deadlines, lease and exact result/workflow-count oracles remain unchanged.
5. Compare the unchanged 1,000 profile, then run the unchanged 10,000 profile
   after a demonstrated reduction in control overhead. Do not repeat full load
   just to obtain a favorable outcome. If the bottleneck persists, use retained
   activity/workflow/SQL timing to select the next change.
6. Finish the named lifecycle/policy fault cases and deterministic recovery-jitter
   contract, then run the remaining P6 environment and bounded provider gates.
