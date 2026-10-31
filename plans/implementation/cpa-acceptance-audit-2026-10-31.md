# CPA admission acceptance audit

## 1. Scope and evidence rule

This audit tracks #134 separately from status lookup #136. It does not replace
`plans/cpa-durable-execution-architecture.md` or narrow its P0–P6 requirements.
Published checkpoint `9fddf33` has passing hosted run `41225460846`. The next
acknowledgement/jitter checkpoint passes all local gates and both strict scale
profiles; its immutable source and hosted result must be recorded at publication.
Redacted evidence is in
`plans/evidence/harden-llm/cpa-admission/2026-10-31-acknowledgement-jitter.json`.
P6 deployment and live-provider acceptance remain open. Test names alone are not
acceptance evidence.

## 2. Delivery requirements

| Requirement | Authoritative coverage or remaining work |
| --- | --- |
| #136 ordinary status lookup, scope isolation, unchanged OpenAI JSON/SSE, stale/terminal projection | Ready-for-review PR #140, source `326767d`; its detailed P0–P9/RS acceptance audit is in `plans/request-status-lookup-implementation-plan.md`. Unmerged/undeployed. |
| P0 existing accepted-job/source ownership inventory and stable authority | Delivery sections 10–16; explicit schema and V1 drain/handoff, no serving-path DDL or pool reset. |
| P1 immutable 180-second legacy / 300-second new / 900-second maximum budgets and separate finalization | `budget_test.go`, `budget_integration_test.go`, gateway OpenAPI/client fixtures; full current-source fast, SQL/race and Temporal gates pass. |
| P2 claim/start/settlement fencing and immutable artifacts | `ledger_integration_test.go`, `attempt_starter_test.go`, `attempt_execution_integration_test.go`; no inference retry on unknown outcomes. |
| P3 fairness, one probe, restriction/window continuity, worker ownership | `queue_integration_test.go`, `pool_state_test.go`, `pool_workflow_test.go`, `workers_test.go`; canonical real replay accepts current pool/attempt markers; SDK tests retain legacy/current decision branches. |
| Optional operator recovery jitter, default zero, deterministic padding once after trusted floor | Implemented by TEST-529: validated 0–1,000 ms setting, default zero, fixed SHA-256 vector, immutable recorded choice, continuation and legacy history marker. Fast/owner-race and canonical Temporal gates pass; ordinary dispatch is unpadded. |
| P4 real active-call rollover and independent eligible work | `TestTemporalBatchV2SlowCallSurvivesRolloverAndOtherJobFinishes` exercises real Temporal/PostgreSQL/Garage plus native HTTP, complete counts and history replay. Accepted in the full current-source `tmp/cpa-ack-jitter-final-temporal.json`. |
| P5 ordinary/durable shared cap/cooldown and caller compatibility | `TestTemporalBatchOrdinaryShareAdmissionAndKeepOpenAIWire`, `TestTemporalBatchOrdinaryRefusalSharesCooldownAndRetryOwnership`; preserve cancellation, cache bypass, scope boundaries and metadata-only ordinary results. |
| P5 1,000 separate native calls and complete results | Acknowledgement/jitter-default-zero four-shard candidate passes in 33.059 seconds: exactly 1,000 native calls, distinct validated results and settled attempts; exactly 800 durable attempt workflows, no never-dispatched attempts or ordinary errors. Receipt `tmp/cpa-ack-jitter-1000.json`. This is synthetic local-provider acceptance. |
| P5 10,000 mixed calls, exact counts and bounded resources | Accepted with unchanged oracles in `tmp/cpa-ack-jitter-10000.json`: 10,000 separate native calls, distinct validated results and settled ledger attempts; exactly 9,000 durable attempt workflows; zero never-sent, ordinary-error, unknown or leaked permits. Full collection/diagnostics: 474.169 seconds. Resource assertions pass: heap 145,494,752 bytes, goroutines 5,203. Earlier rejected profiles remain retained. |
| P6 selected environment, namespace/network, scoped storage, schema/images and HTTP checks | Not deployed/qualified. Record exact branch/SHA/images/environment and browser-free HTTP acceptance. |
| P6 representative bounded CPA request campaign | Not run in this implementation checkpoint. Explicit opt-in, independent from deployment smoke tests. Synthetic throughput is not a CPA quota measurement. |

## 3. Required fault coverage

| Named fault | Existing source-level evidence | Remaining evidence limit |
| --- | --- | --- |
| Lost start acknowledgement | TEST-526 performs a real positive start, loses only its caller acknowledgement, retries the same active reservation and validates one native/artifact result plus actual attempt-history replay | Accepted canonical real-service receipt `tmp/cpa-ack-jitter-final-temporal.json`; no mock replaces Temporal/SQL/HTTP/Garage. |
| Provider activity redelivery | `TestInvocationClaimsOnceAndKeepsResponseOutOfReturn`, `TestIndependentAttemptClaimLoserWaitsForOriginalOwner`, real SQL competing claims | Claim/dispatch invariant covered; no claim of a real server-induced task redelivery. |
| Claim COMMIT ambiguity | `TestAttemptUnknownClaimCommit` covers committed/readback found, rollback absent, readback failure and foreign owner against real PostgreSQL | TEST-528 additionally proves lost claim/dispatch acknowledgement plus one aborted read transaction, bounded positive recovery and negative ownership/deadline/cancellation cases. Canonical SQL/race accepts; native entry stays forbidden without positive same-invocation proof. |
| Stale reservation expiry racing claim | Existing claim-wins/late-claim cases plus `TestAttemptExpiryAndStaleClaimSerializeAtRealRowLock` observe actual blocked backend PIDs before expiry defeats a stale claimant | Latest full SQL/race track `tmp/cpa-ack-jitter-final-integration-race.json` accepts this real row-lock boundary, with empty cleanup. |
| Crash after HTTP dispatch | TEST-530 kills only its owned worker after actual native HTTP entry, verifies SIGKILL/Process.Wait, restarts compatible control and requires terminal unconfirmed without replay | Focused and full canonical Temporal gates accept. Capacity stays retained until explicit owner stop backed by OS exit evidence; terminal bytes remain immutable. |
| Late result after expiry | TEST-531 holds a real Garage write after validated HTTP completion, expires only the owned SQL attempt and requires unconfirmed settlement before storage release | Focused and full canonical Temporal gates accept. Genuine same-owner late artifact cannot replace terminal inference; stop advances revision only, one native call remains. |
| Lost settlement acknowledgement | `TestAttemptUnknownSettlementCommitIsMetadataOnly`, `TestIndependentAttemptRetriesOnlySettlementMetadata` | Real COMMIT/readback and same retained observation covered; never rerun native inference. |
| Duplicate or boundary completion signal | Pool settlement revision/idempotence tests plus real active rollover/history replay | Accepted current markers in `tmp/cpa-ack-jitter-final-temporal.json`, including real independent-attempt replay. |
| Persistence outage | TEST-527 holds the real invocation row after native entry, requires an actual same-owner artifact and Temporal metadata retry before unblocking, then validates one result and released pool capacity | Accepted canonical real-service receipt `tmp/cpa-ack-jitter-final-temporal.json`; one native HTTP call, original claim/ordinal, no leaked permit. |
| Account pause across idle/CAN | `TestPoolPolicyResultStaysPausedAcrossIdleAndContinuation` applies an actual policy settlement, idles, continues and runs the paused successor | Accepted SDK/race receipt `tmp/cpa-wake-policy-faults-verified.log`; retain independent real rollout gates. |
| Dispatch lag across a window boundary | `TestPoolStateWindowAnchorsOnClaimAndNeverRefillsUnconfirmedStarts`; grant/claim timing recorded under load | Preserve the first confirmed anchor, no unclaimed quota refill and no later claim postponement. |
| Policy/cap changes with active work | `TestPoolPolicyChangesPreserveActivePermitsAndRestrictions` signals shrink/increase with active work; window and restrictions survive, new grants retain accepted budgets/deadlines. TEST-497 owns old accepted SQL snapshots. | Accepted SDK/race receipt `tmp/cpa-wake-policy-faults-verified.log`; no fake actor-owned SQL reservation was introduced. |

## 4. Current checkpoint verification

- Fast: all ten tasks accepted in
  `tmp/test-feedback/runner-1793470883541-1218466-a01d002bcd6f969d.json`.
- Canonical SQL/race: `tmp/cpa-ack-jitter-final-integration-race.json`,
  accepted, 87.739 seconds, no cleanup errors/warnings.
- Canonical real Temporal/PostgreSQL/Garage and replay:
  `tmp/cpa-ack-jitter-final-temporal.json`, accepted, 54.031 seconds,
  no cleanup errors/warnings. Includes TEST-526/527/530/531.
- Strict separate-request profiles: `tmp/cpa-ack-jitter-1000.json` and
  `tmp/cpa-ack-jitter-10000.json`, both accepted, no cleanup errors/warnings.
- Frozen runtime/load hashes predate only the later physical-fault test helpers;
  runtime bytes are unchanged. Receipt records that distinction.
- Native overlap peaked at six against cap 128 in these short local-provider
  profiles. They prove the stated synthetic workload, not CPA concurrency or
  production throughput. No live provider, deployment or browser ran here.

## 5. Verification and publication order

1. Focused T0/T1 owner/race regressions and unchanged architecture guards.
2. Load profiles in isolation, with exact IDs/counts, original deadlines and the
   five-second lease. Do not reduce assertions or extend deadlines to obtain a pass.
3. Full fast gate, then sequential owned SQL/race and real Temporal/replay tracks.
   Avoid competing CPU-heavy checks during resource/timing qualification.
4. Record source hashes, accepted and rejected receipts, limitations and cleanup.
   Push the verified checkpoint and update draft PR #141/#134 accurately.
5. Complete P6 environment/provider gates after source publication and hosted checks.
   Merge/main promotion and production deployment remain separate user decisions.

## 6. Immediate delivery sequence

The design and P0–P5 source acceptance are ready. Remaining work concerns
publication and P6 qualification, rather than a new architecture or CPA upgrade.

1. Publish the verified acknowledgement/jitter/process-fault checkpoint and check
   the exact PR head and hosted result. Retain all earlier failures. #140 can
   proceed through review independently; #141 stays draft until P6 acceptance.
2. Follow `plans/implementation/cpa-nonproduction-qualification-2026-10-31.md`:
   isolate database/role, Garage bucket/key and Temporal namespace; build one
   pinned gateway image for two API replicas and a worker; run explicit schema
   and V2 authority initialization, then authenticated HTTP checks without inference.
3. Run the separately bounded real CPA campaign through those deployed APIs.
   Validate every result and caller progress, actual retries/usage, terminal
   uncertainty and final permits. Keep ordinary calls request-bound and use
   existing durable jobs for multi-minute waits.
4. Record exact SHA/images/environment/receipts and reconcile P0–P6 before
   declaring delivery. Main merge and production promotion remain separate.
