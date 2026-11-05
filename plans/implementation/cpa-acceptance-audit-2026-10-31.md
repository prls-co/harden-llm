# CPA admission acceptance audit

Current status, 2026-11-04: application source `f1ebdc4` is deployed and qualified
in production after all 30 exact-main release tasks passed. The separate live
campaign validates 1,000 distinct results in 313.392 seconds; two ordinary
tracking pilots also pass. All 1,002 dispatches stopped/settled, pool outstanding
and start intents are zero, and retained reference data contents are unchanged.
The [production receipt](../evidence/harden-llm/cpa-admission/2026-11-04-production-cpa.json)
records images, limits and evidence boundaries. Sections below retain earlier
checkpoints and the failures/fixes, rather than retroactively relabelling them.

## 1. Scope and evidence rule

This audit tracks #134 separately from status lookup #136. It does not replace
`plans/cpa-durable-execution-architecture.md` or narrow its P0–P6 requirements.
Runtime checkpoint `b031fc9`, followed only by documentation correction
`b75d86f`, passes the local gates and both strict scale profiles. Hosted run
`41231308022` passes for `b75d86f`; hosted service/browser/release tracks were
skipped, not passed. That exact source is deployed and qualified in an isolated
loopback environment. No main merge or production promotion is claimed.

Redacted source/scale evidence:
`plans/evidence/harden-llm/cpa-admission/2026-10-31-acknowledgement-jitter.json`.
Deployment/live acceptance:
`plans/evidence/harden-llm/cpa-admission/2026-10-31-nonproduction-cpa.json`.
Test names alone are not acceptance evidence.

## 2. Delivery requirements

| Requirement | Authoritative coverage or remaining work |
| --- | --- |
| #136 ordinary status lookup, scope isolation, unchanged OpenAI JSON/SSE, stale/terminal projection | Ready-for-review PR #140, source `326767d`; its detailed P0–P9/RS acceptance audit is in `plans/request-status-lookup-implementation-plan.md`. Unmerged. Its code is included in the separately qualified combined `b75d86f` nonproduction deployment; see request-status plan section 12. |
| P0 existing accepted-job/source ownership inventory and stable authority | Delivery sections 10–16; explicit schema and V1 drain/handoff, no serving-path DDL or pool reset. |
| P1 immutable 180-second legacy / 300-second new / 900-second maximum budgets and separate finalization | `budget_test.go`, `budget_integration_test.go`, gateway OpenAPI/client fixtures; full current-source fast, SQL/race and Temporal gates pass. |
| P2 claim/start/settlement fencing and immutable artifacts | `ledger_integration_test.go`, `attempt_starter_test.go`, `attempt_execution_integration_test.go`; no inference retry on unknown outcomes. |
| P3 fairness, one probe, restriction/window continuity, worker ownership | `queue_integration_test.go`, `pool_state_test.go`, `pool_workflow_test.go`, `workers_test.go`; canonical real replay accepts current pool/attempt markers; SDK tests retain legacy/current decision branches. |
| Optional operator recovery jitter, default zero, deterministic padding once after trusted floor | Implemented by TEST-529: validated 0–1,000 ms setting, default zero, fixed SHA-256 vector, immutable recorded choice, continuation and legacy history marker. Fast/owner-race and canonical Temporal gates pass; ordinary dispatch is unpadded. |
| P4 real active-call rollover and independent eligible work | `TestTemporalBatchV2SlowCallSurvivesRolloverAndOtherJobFinishes` exercises real Temporal/PostgreSQL/Garage plus native HTTP, complete counts and history replay. Accepted in the full current-source `tmp/cpa-ack-jitter-final-temporal.json`. |
| P5 ordinary/durable shared cap/cooldown and caller compatibility | `TestTemporalBatchOrdinaryShareAdmissionAndKeepOpenAIWire`, `TestTemporalBatchOrdinaryRefusalSharesCooldownAndRetryOwnership`; preserve cancellation, cache bypass, scope boundaries and metadata-only ordinary results. |
| P5 1,000 separate native calls and complete results | Acknowledgement/jitter-default-zero four-shard candidate passes in 33.059 seconds: exactly 1,000 native calls, distinct validated results and settled attempts; exactly 800 durable attempt workflows, no never-dispatched attempts or ordinary errors. Receipt `tmp/cpa-ack-jitter-1000.json`. This is synthetic local-provider acceptance. |
| P5 10,000 mixed calls, exact counts and bounded resources | Accepted with unchanged oracles in `tmp/cpa-ack-jitter-10000.json`: 10,000 separate native calls, distinct validated results and settled ledger attempts; exactly 9,000 durable attempt workflows; zero never-sent, ordinary-error, unknown or leaked permits. Full collection/diagnostics: 474.169 seconds. Resource assertions pass: heap 145,494,752 bytes, goroutines 5,203. Earlier rejected profiles remain retained. |
| P6 selected environment, namespace/network, scoped storage, schema/images and HTTP checks | Accepted on `b75d86f`, image `sha256:e94803f3781ade366eb407b9d61b4ed582b2c4bc4a7b020dc452d7fb6c12c0ba`: two API replicas/one worker; unique limited SQL role/database, Garage bucket/key and Temporal namespace/network; schema and V2 generation 2 initialized with zero inference; health/auth/status checks pass. See section 7. |
| P6 representative bounded CPA request campaign | Accepted separately after deployment checks: 1,000 distinct short CPA `gpt-6-luna` low-reasoning results across two 500-item jobs/two APIs in 259.002 seconds including validation. All first attempt; 112,000 reported tokens; final pool/ledger empty and positively stopped. This is one qualified workload, not a universal quota or maximum-throughput result. |

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

## 6. Delivery disposition

P0–P6 implementation and the stated local/nonproduction acceptance are complete.
PR #140 owns the status-only slice; PR #141 owns shared CPA admission. Both
remain unmerged. Review, main merge and production promotion are separate.
The operational checklist remains in
`plans/implementation/cpa-nonproduction-qualification-2026-10-31.md`; section 7
records its execution. Retain accepted jobs/results and private runtime settings
for review, rather than automatically deleting them on observer completion.

## 7. P6 deployment and live acceptance

- Source: `b75d86fb606f6c5bd7819016bc13390f63b759e0` on
  `feat/cpa-shared-admission-v2`, with exact revision/version image labels.
- Environment/namespace/pool/task queue: `hllm-cpa-acceptance-20261031-9a4b47`.
  APIs: `http://127.0.0.1:33788` and `http://127.0.0.1:33787`.
- Gateway image for both APIs and worker:
  `sha256:e94803f3781ade366eb407b9d61b4ed582b2c4bc4a7b020dc452d7fb6c12c0ba`.
  No restarts/OOM kills; bounded API pools 5 each and worker pool 10.
- Only new scoped SQL/Garage/Temporal resources were provisioned. Docker's
  automatic address pool was exhausted before any API/worker started; a new
  explicitly non-overlapping owned `/28` network solved startup. No shared
  network, daemon setting, service or production dataset was reset.
- Schema command and `batch-upgrade` positively confirmed generation 2 with zero
  attempts before inference. Both APIs return health/readiness 200; missing,
  wrong and upstream CPA bearers return 401; authenticated unknown IDs return
  404. Upstream credentials are never incoming application authority.
- Two tracked ordinary CPA calls validate JSON and the existing completed-SSE
  wire, with lookup through the opposite replica, confirmed terminal metadata
  and duplicate 409 plus SDK retry suppression. Their ordinary records produce
  no reference history, profiles or cache entries.
- The separate 1,000-item campaign validates every exact nonce and response ID,
  cross-replica idempotent submission and complete NDJSON downloads. Generation
  p50/p95/max: 1,262/1,987/4,829 ms. All 1,000 items finish on attempt one;
  reported input/output tokens: 73,000/39,000. CPA internal attempts are unknown.
- Worker logs prove 1,000 distinct native-path entries and completed, positively
  stopped outcomes. Invocation-interval overlap peaks at 95 under configured
  cap 128. This measures HLLM execution intervals, not upstream internal slots.
- Final SQL proves 1,002 completed/stopped attempts (1,000 durable + two ordinary),
  no unsettled/unknown/no-stop rows, two completed tracking records and zero
  reference history/profiles/cache entries. Pool query confirms no outstanding
  permits or start intents. No inference replay occurred during validation.
- Five-second progress polling distinguishes window waits, next eligibility,
  advancing heartbeat and genuine progress. No real throttle occurred in this
  sample; deterministic tests own refusal/probe/cooldown qualification.
- Initial operator-client payload omissions were rejected with zero inference.
  A subsequent client parser expected a nested completed response; the existing
  HLLM wire is top-level. The same captured successful SSE/deltas were validated
  without another inference. This preserves the accepted wire, rather than
  claiming compatibility with every SDK completed-response accessor. Raw failures
  and private results are retained; no application assertion was weakened.
- The retained CPA translator may strip requested `max_output_tokens`; exact
  nonce schema constrained visible output. No token quota or ban-safe rate is
  inferred from this campaign. Browser layout was not checked; no frontend,
  production promotion or shared CPA configuration change occurred.

## 8. Production-readiness review — 2026-11-04

Disposition: **not yet ready for production promotion**. The P0–P6 implementation
and isolated nonproduction acceptance above remain valid. This review identifies
remaining work in the production adoption path; it does not invalidate the
recorded 1,000-request result or authorize deployment.

PR #140 remains open/ready, source `326767d`; PR #141 remains open/ready, reviewed
source `822b324`. Both hosted fast runs pass. GitHub records no approving review
for either PR: #140 has only a resolved automated security comment and #141 had
no review submissions. This is an author review of the critical auth/tracking,
admission, claim/settlement, continuation, deadline, client and release paths;
it is not an independent approval or a claim that every research artifact was
re-reviewed. No new inference or browser ran for this review.

### 8.1. Production blocker: deployment controller cannot adopt batch execution

`scripts/production-config.mjs` fixes `COMPOSE_FILE_ORDER` to root/frontend files
and `APPLICATION_SERVICES` to gateway/web. `validateDescriptor` rejects adding
`deploy/batch/compose.yml`; `validateCandidateScope` rejects the batch worker
under `--expected-release`. These were reproduced by importing the actual module,
constructing metadata-only candidates and invoking its validators with a process
runner that throws on any attempted external command:

```text
batch-overlay: invalid_configuration: descriptor composeFiles do not match the approved graph
worker-release: scope_blocked: --expected-release is limited to harden-llm-gateway and harden-llm-web
```

The current protected production descriptor has no worker or batch/Temporal
settings; the running production gateway has batch disabled. A normal gateway
image promotion alone therefore cannot deliver the feature. Add narrowly scoped
batch overlay/worker support to the existing controller, retaining its exact-image,
release, allowed-difference and shared-service guards. Qualify schema, worker and
V2 authority before enabling managed incoming CPA traffic. Do not bypass the
controller with the private nonproduction Compose file.

### 8.2. Rollout fix: worker container grace is shorter than worker drain

`deploy/batch/compose.yml` sets `stop_grace_period: 30s`.
`stateful/batch/workers.go` sets each SDK worker's `WorkerStopTimeout` to 45s and
stops provider then control sequentially. Docker may SIGKILL before even the
provider's full wait, interrupting outcome finalization during an ordinary
replacement. Unknown outcomes correctly remain unconfirmed, but a routine
rollout should not force that failure path. Align the container grace with both
bounded worker waits plus process cleanup (for example 100–120s) and cover the
configuration relationship with the lowest sufficient deterministic check.

### 8.3. Client contract gap: new deadline fields are dropped

The V2 gateway/OpenAPI return `execution_deadline` and `finalization_deadline`,
but `httpclient.JobStatus` exposes neither. `decodeWire` validates the response
then decodes into that smaller struct, so Go callers lose those fields. This does
not stop REST bulk execution; add the two optional timestamps and a client wire
regression before claiming complete typed-client deadline visibility.

### 8.4. Remaining release steps

1. Fix the scoped production-controller adoption and shutdown configuration;
   close the small typed-client deadline gap. Recheck the affected boundaries.
2. Merge #140 before #141, preserving accepted jobs, schemas and the existing
   production service/data ownership. A main merge is not a live enablement.
3. Follow `SPEC-HLLM-IMAGE-DEPLOYMENT-001`: exact merged source, successful
   browser-free release workflow/attempt, then pinned gateway/worker image build
   and protected candidate descriptor checks. Existing fast-only runs explicitly
   skipped release and do not substitute for this gate. Do not run the service-
   heavy certification on the live production daemon.
4. Prepare the production namespace/settings and schema/worker/V2 sequence;
   verify the concrete release/rollback plan before promotion. Operator approval
   of the actual production switch remains separate. No new design inputs or
   analytics dashboard are needed from the user.

## 9. Authorized production closeout fixes — 2026-11-04

The user subsequently authorized fixes, merge to main, release and production
deployment. Section 8 records the pre-fix review; it is not a new approval gate.
The scoped controller now accepts only the known optional batch overlay/worker,
checks shared image/storage/Temporal/policy authority, proves first-install
absence without hiding daemon errors, runs schema before worker startup and
keeps normal gateway HTTP health checks. Disabled worker HTTP health, physical
network aliases and actual stop grace are compared explicitly. Existing image,
ownership, allowed-difference and release guards remain active.

The overlay now allows 120 seconds for both sequential SDK stops and cleanup.
The typed Go client preserves both optional V2 deadline fields while accepting
legacy omission. TEST-234/235/260/450/478/506 cover these changes; the new
regressions failed against the prior source before implementation. Focused Go,
controller and native Compose configuration checks pass. Exact merged-source
release/deployment receipts will be recorded separately after those steps run.

## 10. Exact-main release scheduling correction

PRs #140 and #141 merged as `f86897c` and `55a7e95`. Exact-main release
`41844670099`, attempt 1, failed in `go-integration`: TEST-495's unchanged
1,000-tracked-call assertion observed six registration 503s and 994 provider
dispatches. The runner report records overlap with race/vulnerability checks
and frontend compilation. Other interrupted/unstarted tasks are not passes.
Production application images were not changed by that failed gate.

The ordinary SQL/scale integration task now uses the existing exclusive release
resource, like the already isolated integration-race task. TEST-048 rejects CPU
compiler overlap for either task; its added assertion failed before changing
the manifest. The 600/500 ms SQL budgets, 16-call generator, package cap, 1,000
count/terminal assertions, production code and cleanup rules are unchanged.
TEST-495 now reports only bounded SQL error types/codes and pool counters on
failure, never parameters, DSNs or arbitrary errors. This fixes the observed
runner scheduling overlap; a fresh exact-source release must still pass.

Source comparison also identifies frontend runtime changes in #141. This
production closeout therefore builds gateway and web, with the worker using the
gateway image, rather than retaining an out-of-date frontend image.

## 11. Certificate security compiler patch

The next exact-main release, `41848022541` attempt 1 on `db4c8e7`, was rejected
by `go-vulnerability`: GO-2026-6809 is reachable through `crypto/x509` in the
pinned Go 1.26.9 standard library. The [official Go advisory](https://pkg.go.dev/vuln/GO-2026-6809)
identifies 1.26.10 as the patched 1.26 release. The earlier failed run had
cancelled this check before it completed; this is a separate newly observed
release blocker, not a reason to retry the same source or relax the scanner.

The module minimum, hosted toolchain, gateway/synthetic provider builders and
optional browser recipe now select Go 1.26.10. The Dockerfiles retain Alpine 3.23 and pin the
official multi-platform image index
`sha256:34414731924cf2263160f0021f5cf6e1807382d85248f7cb419d4187b96d5e43`,
resolved from `docker.io/library/golang:1.26.10-alpine3.23`; its linux/amd64
manifest is `sha256:9d1e2c4fda593efb2c405e1c855c75ec17fe42dc02f6b7142a5ea7af7f386bf4`.
No TLS verification bypass, vulnerability suppression or unrelated dependency
upgrade is introduced. The unchanged vulnerability check and a fresh exact-main
release must pass before building production artifacts.
The optional browser image is not built or run as part of this closeout.

## 12. Frontend dependency audit patches

Release `41850876751` attempt 1 on `77f98d6` passed the Go vulnerability and
real Temporal tasks, then failed the Hex security audit. Ordinary integration,
integration race and other cancelled/unstarted tasks are not passes. The full
failed report is retained; no production artifact was built or promoted.

Both dependency audits were run explicitly before another release dispatch.
The locked fixes are Bandit 1.14.1, Phoenix.HTML 4.4.0, Plug.Crypto 2.2.1,
Hackney 4.9.1 and ssl_verify_fun 1.1.9. Hackney's required dependency constraints
also update certifi 2.18.0, h2 0.12.5, quic 2.1.3 and webtransport 0.4.8.
Other dependency locks and the shared `prls_web` Git revision are retained.
The host explicitly overrides that revision's exact Phoenix.HTML 4.3.0 pin;
the reusable widget requires the patched 4.4 line. This selects the security
fix without adopting a new shared-library identity/UI contract.

Primary affected-version references: [Bandit](https://cna.erlef.org/cves/CVE-2026-116078.html),
[Phoenix.HTML](https://cna.erlef.org/cves/CVE-2026-117645.html),
[Plug.Crypto](https://cna.erlef.org/cves/CVE-2026-105963.html),
[Hackney](https://cna.erlef.org/cves/CVE-2026-115693.html),
[ssl_verify_fun](https://cna.erlef.org/cves/CVE-2026-117666.html).
The unchanged `mix hex.audit` now reports no retired/advisory packages;
canonical `frontend-deps-audit` also passes. Host/widget compatibility and
production compilation remain part of the fast and exact-main release gates.
The first fast attempt exposed the standalone widget's separate lockfile still
selecting old HTML/Crypto versions while its task shares the host dependency
directory. Both locks are now updated together; the existing dependency checks
remain enabled. No assertion or offline policy was relaxed.

The subsequent fast run rejected a TEST-517 fixture whose 40 ms whole-call timer
expired before enqueue under compilation load (zero provider entries, zero
queued rows). Queued cancellation now occurs at the existing signal callback,
after positive enqueue acknowledgement: exactly one closed waiter, no provider
entry and the admission-wait projection remain mandatory. An already-expired
real context separately requires `DeadlineExceeded`, zero queue entries and
zero native entry. No production deadline or admission code is changed.

## 13. Native Compose worker waiting correction

Exact application source `f1ebdc4` passed all 30 hosted release tasks in run
`41857609943`, attempt 1, and both immutable application images were built.
The first production worker apply installed the additive schema and started
the correct worker, but Compose returned an error. Independent controller
readback confirmed equivalence, running state and zero restarts; explicit
upgrade then confirmed generation 2 `v2_ready` with zero outstanding permits.

A no-recreate diagnostic against that already-equivalent, owned worker
reproduced `container ... has no healthcheck configured`: native Compose
`--wait` rejects the intentionally disabled worker HTTP check. The controller
now omits `--wait` only for separate worker apply, retains final process/image
checks and the subsequent V2/poller readiness gate, and rejects mixed worker/
HTTP application apply. Gateway/web still use native HTTP health waiting.
TEST-260/450 regressions failed before this operations-tool correction; native
configuration coverage remains TEST-235. This changes deployment tooling, not
the qualified Go/web runtime inputs or their immutable image identities.

## 14. Production acceptance complete

PRs #140/#141 and release corrections #143/#144/#145 are merged. Runtime
images use exact source `f1ebdc41e4ea7c38b65f256b346b1f2cad3dbfb4`, qualified
by release `41857609943` attempt 1: all 30 selected tasks pass, with no cleanup
errors/warnings. Exact image IDs, local full-SHA tags and rollout identity are
in the production receipt. No registry publisher is introduced.

Production reuses the HLLM database/role and Garage bucket, existing private
networks and shared Temporal service. Namespace `harden-llm-production`, task
queue `harden-llm-batch-v1` and stable pool `cpa-production` share admission for
ordinary and durable calls. Additive schema, worker, V2 authority and application
rollout ran in that order; provider-free public HTTP checks pass. The worker's
actual stop grace is 120 seconds.

Separate provider qualification passes tracked ordinary JSON/existing SSE and
duplicate 409/retry suppression, then two 500-item jobs: 1,000 distinct validated
outputs/response IDs, zero failures, maximum HLLM item attempt 1. Five-second
status and actual Go wait/download clients report admission-window waits, fresh
heartbeats and both deadline fields. Campaign elapsed 313.392 seconds includes
validation/downloads. The two prior pilots used two initial reservations, so
the final two items waited for another local window. Campaign reported 23,000
input and 14,000 output tokens, known for all items. CPA internal attempts remain
unknown; no live throttle occurred. This is not a universal quota or long-output
performance guarantee.

SQL confirms exactly 1,002 dispatched, positively locally stopped and settled
attempts, including the pilots. Pool generation 2 has zero outstanding permits,
zero start intents, no quiescing/persistence wait. Stored row-content fingerprints
and counts for cache, credentials, drafts, hidden presets, history and profiles
match the pre-rollout checkpoint. Jobs/results and rollback images/configuration
are retained. No browser ran; layout was not checked.

Operations-only PR #146 is merged as `fa2de8b`: all ten local fast tasks, 44
controller/native configuration tests and the corrected real no-recreate command
pass. Its hosted follow-up jobs were queued without a runner at publication;
they are not borrowed as release passes. Go/frontend/deployment recipe runtime
inputs are unchanged from the accepted artifact. Production source includes
this controller fix; final controller comparison is equivalent.
