# CPA admission acceptance audit

## 1. Scope and evidence rule

This audit tracks #134 separately from status lookup #136. It does not replace
`plans/cpa-durable-execution-architecture.md` or narrow its P0–P6 requirements.
Published checkpoint `90c0bf0` has passing hosted hierarchy run `41221903361`
and retained local semantic gates. New bounded wake/finalization code remains a
local candidate until its own gates and source identity are recorded. A test name below is a coverage
pointer, not proof that the current candidate has passed that test.

## 2. Delivery requirements

| Requirement | Authoritative coverage or remaining work |
| --- | --- |
| #136 ordinary status lookup, scope isolation, unchanged OpenAI JSON/SSE, stale/terminal projection | Ready-for-review PR #140, source `326767d`; its detailed P0–P9/RS acceptance audit is in `plans/request-status-lookup-implementation-plan.md`. Unmerged/undeployed. |
| P0 existing accepted-job/source ownership inventory and stable authority | Delivery sections 10–16; explicit schema and V1 drain/handoff, no serving-path DDL or pool reset. |
| P1 immutable 180-second legacy / 300-second new / 900-second maximum budgets and separate finalization | `budget_test.go`, `budget_integration_test.go`, gateway OpenAPI/client fixtures; current-candidate gate still required. |
| P2 claim/start/settlement fencing and immutable artifacts | `ledger_integration_test.go`, `attempt_starter_test.go`, `attempt_execution_integration_test.go`; no inference retry on unknown outcomes. |
| P3 fairness, one probe, restriction/window continuity, worker ownership | `queue_integration_test.go`, `pool_state_test.go`, `pool_workflow_test.go`, `workers_test.go`; canonical real replay accepts current pool/attempt markers; SDK tests retain legacy/current decision branches. |
| Optional operator recovery jitter, default zero, deterministic padding once after trusted floor | Missing capability. Implement the specified 0–1,000 ms policy/config contract with deterministic tests and history-safe behavior; do not add random dispatch delay. |
| P4 real active-call rollover and independent eligible work | `TestTemporalBatchV2SlowCallSurvivesRolloverAndOtherJobFinishes` exercises real Temporal/PostgreSQL/Garage plus native HTTP, complete counts and history replay. Accepted on the newer candidate in `tmp/cpa-wake-finalizer-temporal.json`. |
| P5 ordinary/durable shared cap/cooldown and caller compatibility | `TestTemporalBatchOrdinaryShareAdmissionAndKeepOpenAIWire`, `TestTemporalBatchOrdinaryRefusalSharesCooldownAndRetryOwnership`; preserve cancellation, cache bypass, scope boundaries and metadata-only ordinary results. |
| P5 1,000 separate native calls and complete results | Combined-finalization four-shard candidate passes in 33.364 seconds: 1,000 native calls, distinct results and settled attempts; 800 durable attempt workflows, no never-dispatched attempts or ordinary errors. Receipt `tmp/cpa-combined-finalizer-1000.json`. This is synthetic local-provider acceptance. |
| P5 10,000 mixed calls, exact counts and bounded resources | Still rejected by strict control-overhead counts. Combined finalization completes all 10,000 separate native calls and validated results inside execution budgets, with no unknown/leaked permits; result collection/diagnostics finish at 492.140 seconds. Two recovered pre-entry acknowledgement failures create 10,002 ledger attempts / 9,002 attempt workflows. Receipt `tmp/cpa-combined-finalizer-10000.json`. The unchanged 10,000/9,000 count oracle remains open. |
| P6 selected environment, namespace/network, scoped storage, schema/images and HTTP checks | Not deployed/qualified. Record exact branch/SHA/images/environment and browser-free HTTP acceptance. |
| P6 representative bounded CPA request campaign | Not run in this implementation checkpoint. Explicit opt-in, independent from deployment smoke tests. Synthetic throughput is not a CPA quota measurement. |

## 3. Required fault coverage

| Named fault | Existing source-level evidence | Remaining evidence limit |
| --- | --- | --- |
| Lost start acknowledgement | TEST-526 performs a real positive start, loses only its caller acknowledgement, retries the same active reservation and validates one native/artifact result plus actual attempt-history replay | Accepted canonical real-service receipt `tmp/cpa-wake-finalizer-temporal.json`; no mock replaces Temporal/SQL/HTTP/Garage. |
| Provider activity redelivery | `TestInvocationClaimsOnceAndKeepsResponseOutOfReturn`, `TestIndependentAttemptClaimLoserWaitsForOriginalOwner`, real SQL competing claims | Claim/dispatch invariant covered; no claim of a real server-induced task redelivery. |
| Claim COMMIT ambiguity | `TestAttemptUnknownClaimCommit` covers committed/readback found, rollback absent, readback failure and foreign owner against real PostgreSQL | Accepted current canonical SQL/race receipt `tmp/cpa-wake-finalizer-integration-race.json`. |
| Stale reservation expiry racing claim | Existing claim-wins/late-claim cases plus `TestAttemptExpiryAndStaleClaimSerializeAtRealRowLock` observe actual blocked backend PIDs before expiry defeats a stale claimant | Latest full SQL/race track `tmp/cpa-wake-finalizer-integration-race.json` accepts this real row-lock boundary, with empty cleanup. |
| Crash after HTTP dispatch | Panic/unknown local-stop evidence, lost provider return and real SQL local-stop/generation fencing | Protocol state is covered; a process-kill boundary is not established by a panic fixture. Keep an explicit acceptance gap if claiming actual crash recovery. |
| Late result after expiry | `TestInvocationArtifactRejectsDifferentIdentityAndLateOutcome`, budget/finalization SQL cases | Check immutable terminal/late-artifact behavior together; do not treat an artifact's existence as successful inference acceptance. |
| Lost settlement acknowledgement | `TestAttemptUnknownSettlementCommitIsMetadataOnly`, `TestIndependentAttemptRetriesOnlySettlementMetadata` | Real COMMIT/readback and same retained observation covered; never rerun native inference. |
| Duplicate or boundary completion signal | Pool settlement revision/idempotence tests plus real active rollover/history replay | Accepted current markers in `tmp/cpa-wake-finalizer-temporal.json`, including real independent-attempt replay. |
| Persistence outage | TEST-527 holds the real invocation row after native entry, requires an actual same-owner artifact and Temporal metadata retry before unblocking, then validates one result and released pool capacity | Accepted canonical real-service receipt `tmp/cpa-wake-finalizer-temporal.json`; one native HTTP call, original claim/ordinal, no leaked permit. |
| Account pause across idle/CAN | `TestPoolPolicyResultStaysPausedAcrossIdleAndContinuation` applies an actual policy settlement, idles, continues and runs the paused successor | Accepted SDK/race receipt `tmp/cpa-wake-policy-faults-verified.log`; retain independent real rollout gates. |
| Dispatch lag across a window boundary | `TestPoolStateWindowAnchorsOnClaimAndNeverRefillsUnconfirmedStarts`; grant/claim timing recorded under load | Preserve the first confirmed anchor, no unclaimed quota refill and no later claim postponement. |
| Policy/cap changes with active work | `TestPoolPolicyChangesPreserveActivePermitsAndRestrictions` signals shrink/increase with active work; window and restrictions survive, new grants retain accepted budgets/deadlines. TEST-497 owns old accepted SQL snapshots. | Accepted SDK/race receipt `tmp/cpa-wake-policy-faults-verified.log`; no fake actor-owned SQL reservation was introduced. |

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

1. Publish the bounded wake/combined-finalization candidate only after its own
   fast, SQL/race and real Temporal/replay gates, retaining all scale failures.
   The previous checkpoint `90c0bf0` is already published and hosted-green.
   #140 can proceed through review independently; #141 remains draft.
2. Reproduce the two remaining pre-entry acknowledgement failures with focused
   real-SQL tests. A bounded extra claim readback may recover this invocation's
   own fresh nonce after a transient failed read. A positive readback of this
   invocation's newly recorded dispatch intent may resolve a lost commit reply.
   Neither operation may repeat the write/inference or authorize missing,
   foreign, terminal, already-dispatched or expired ownership. Do not weaken
   the existing no-native-entry/redelivery oracles.
3. After those root fixes, rerun unchanged scale gates: all 10,000 native/results
   already complete, but exact ledger/workflow counts remain 10,002/9,002 rather
   than 10,000/9,000. Keep native cap 128, ordinary/job deadlines, five-second
   lease, transaction/statement budgets and result/resource assertions.
4. Real start-acknowledgement and persistence-block/recovery cases (TEST-526/527)
   now pass; finish remaining process-death/late-result acceptance and
   optional deterministic recovery jitter. Do not substitute panic coverage for
   real process death or a pre-paused input for an actual policy transition.
5. Complete P6 namespace/network/scoped storage/schema/images and authenticated
   nonproduction HTTP acceptance, then the separately bounded live CPA campaign.
   No browser or production promotion is implied.
