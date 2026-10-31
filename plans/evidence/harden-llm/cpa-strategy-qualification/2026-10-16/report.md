# Shared CPA scheduling qualification

## 1. Decision and scope

The operational choice is continuous durable submission and work-conserving
admission through one shared controller for the CPA credential pool. Use the
measured 200-start/61-second admission estimate, 128 active slots, burst 200,
launch pace 100–200/second, explicit-refusal cooldown, one recovery probe and
paced recovery. This estimate is configurable, not a published account
entitlement. New starts do not wait for every response in an earlier group.
Actual sustained-provider qualification and the final browser-free release
gate passed. Section 6 records the separate source identities and conditions.

All inference in this campaign uses CPA `gpt-6-luna`, reasoning `low`. No official
API, account rotation, shared CPA setting change, browser or deployment is part
of the qualification. The practical choice is falsifiable; these experiments do
not establish a mathematical global optimum for an undocumented changing service.

The live evidence favors a hybrid admission guard over unrestricted probing:
continuous independent-client samples incurred 631 and 635 foreground refusals,
without consistently improving complete-result time. The shared-controller
sample completed 1,000 foreground plus 200 background results without a caller
or correlated native refusal. Its foreground finished in 315.506 seconds; the
same-source independent-client control took 366.341 seconds with 109 foreground
and 74 background refusals. This is one comparison, not statistical proof.
Both share offered arrival times, but the background receives the global
policy in shared mode rather than its independent one-start/second policy.

## 2. What the measured boundary means

Earlier [doubling/bisection and throughput checks](../../cpa-throughput-search/2026-10-16/report.md)
found a cold admission boundary at 200 requests. That is not proof of a
200-simultaneous-generation ceiling. The best isolated public-endpoint drain
retained 1,000 validated results in 255.558 seconds using 128 active slots.
Increasing to 200 slots did not materially improve bulk completion and its full
completion was dominated by slow responses; the experiment does not prove that
higher concurrency caused those delays.

The [minute-window experiments](../../cpa-minute-study/2026-10-16/report.md)
found a clean request still refused at 59 seconds and accepted at 61 seconds.
The split 100-at-zero, 100-at-30-seconds, 200-at-61-seconds experiment contradicts
a strict rolling 200-in-any-60-seconds rule. Six fixed 60-second waves retained
1,183 successes and 17 fast 503 unknowns, not 1,200 successes. Exact quota scope,
phase and refill algorithm remain unisolated. Do not describe 200/minute as a
confirmed universal provider contract.

With an unused estimated budget, 1,000 short calls can use five admission
episodes around t=0,61,122,183,244 seconds, filling available slots inside each
episode. This describes the caller policy, not a completion guarantee. Competing
traffic, long generations, quota changes and final-response tails affect time.
A caller launch pace of 100–200/second is distinct from sustained account capacity.

## 3. Native comparisons and retained failures

The first four samples freeze source `2e909c7` and use windowed–continuous–
continuous–windowed order. Each offers 1,000 foreground requests at t=15 seconds,
100 background requests at t=0 and 100 more at one per second during t=45–144.
The independent background client uses 64 slots, burst 100 and one start/second.
Each case has a 90-second quiet interval; that is not proof all quotas reset.
Other account traffic and host load are uncontrolled.

Later samples use source `b866bb6`, including accrued pace credits. Do not combine
the source groups as one matched experiment. Both windowed and continuous modes
use the same controller; continuous disables only the estimated admission window.
Native calls bypass Temporal, so they do not prove durable execution throughput.
Actual batch nonce schemas use enum equality; native short nonce requests use a
string schema plus prompt equality. Content-error rates across these harnesses
must not be attributed to scheduling policy alone.

| Native case | Source | Foreground valid | Foreground terminal seconds | Foreground refusals | Background valid | All terminal seconds | Qualification |
| --- | --- | ---: | ---: | ---: | ---: | ---: | --- |
| [continuous-competing-1000-205608](continuous-competing-1000-205608/summary.json) | `2e909c7` | 1000/1000 | 424.212 | 631 | 200 | 459.474 | passed |
| [continuous-competing-1000-210538](continuous-competing-1000-210538/summary.json) | `2e909c7` | 1000/1000 | 353.216 | 635 | 200 | 438.222 | passed |
| [continuous-competing-offered-stop-1000-215923](continuous-competing-offered-stop-1000-215923/summary.json) | `b866bb6` | 1000/1000 | 378.767 | 634 | 139 | 393.767 | passed |
| [windowed-competing-1000-204638](windowed-competing-1000-204638/summary.json) | `2e909c7` | 1000/1000 | 374.620 | 358 | 200 | 466.910 | passed |
| [windowed-competing-1000-211438](windowed-competing-1000-211438/summary.json) | `2e909c7` | 999/1000 | 356.650 | 110 | 200 | 546.915 | failed |
| [windowed-competing-1000-213914](windowed-competing-1000-213914/summary.json) | `b866bb6` | 1000/1000 | 366.341 | 109 | 200 | 544.429 | passed |
| [windowed-competing-offered-stop-1000-215014](windowed-competing-offered-stop-1000-215014/summary.json) | `b866bb6` | 999/1000 | 313.448 | 175 | 175 | 328.448 | failed |
| [windowed-isolated-long-every-1-1000-220753](windowed-isolated-long-every-1-1000-220753/summary.json) | `b866bb6` | 996/1000 | 259.724 | 0 | 0 | 259.724 | failed |
| [windowed-shared-1000-213144](windowed-shared-1000-213144/summary.json) | `b866bb6` | 1000/1000 | 315.506 | 0 | 200 | 330.506 | passed |

Failed rows are not complete-results timing measurements. All independent items
are retained, including errors; no ordinary or unknown inference was replayed.
`derived-timing.json` in each case separates p99 completion from the final tail.
Native header and first-output clocks both start at dispatch. HLLM core's
first-output clock starts at SSE collection; those origins must not be mixed.

The second old-source windowed sample retained one HTTP 503
`internal_server_error` after 170 ms, without usage or a bounded correlation ID.
A bounded origin-log interval contains one 503 access entry but does not establish
the cause or whether inference ran. See its `unknown-response-investigation.json`.

The new windowed offered-stop case retained 999 qualified responses and one
HTTP 200/completed response that failed the exact-output oracle. Its bounded
measurement does not retain response text or identify the failed predicate;
it must not be labeled a quota failure. See `failed-output-investigation.json`.
The background completed all 175 requests, and 550 foreground items were admitted
after its actual completion. That establishes exercised post-background dispatch,
not a successful 1,000-result qualification.

The offered-stop profile is a separate experiment: background offers 100 at t=0
then 25 at t=67,128,189; foreground starts t=15. Background uses one attempt per
offer and explicit refusals are counted as terminal offered-load outcomes. The
foreground still retries explicit refusals. Its oracle requires all 1,000
foreground responses, no unknown/ordinary/policy errors, all 175 background
offers accounted for and at least 100 foreground admissions after actual
background completion. Existing 1,200-result cases retain their original oracle.

The continuous offered-stop case passed all 1,000 foreground outputs in
378.767 seconds, with 634 foreground refusals. Background accounted for all 175
offers as 139 valid responses and 36 explicit terminal refusals. Its actual final
completion was 278.160 seconds from engine start; 339 distinct foreground items
were admitted afterward. This directly exercises recovery after external work
finishes. Because the windowed counterpart failed exact-output qualification,
this pair cannot establish a complete-results speed winner. The different
accepted-background totals also preclude treating equal offers as equal useful
competing work. Continuous probing again shows substantial refusal amplification.

## 4. Defects found and fixed

A successful recovery probe formerly restored the full initial burst. A local
token-bucket fixture then repeatedly overshot capacity and exhausted refusal
retries. New histories reopen at the adapted pace without a fresh burst.
`controller-before.json` retains the failures; `controller-after.json` retains
all 16 fixed/sliding-window, token-refill and concurrency comparisons with the
unchanged complete 1,200-result oracle. These are simulations, not provider limits.

A real-stack local endurance check exposed another pacing defect: resetting
next-start to now plus one interval discarded elapsed launch credits between
Temporal decisions. At 100 starts/second, a 100 ms decision gap admitted one
request instead of ten. The fix accrues elapsed credits, caps idle catch-up by
the configured burst and cannot mint credits again at the same timestamp.
Both fixes have Temporal version markers and retained-history replay coverage.

The all-long native run then exposed a distinct issue. It retained 996 validated
512-word responses, one error stream and three fast HTTP 503 responses carrying
60-second Retry-After. The stream correlation contains one upstream HTTP 502;
997 correlated caller IDs contain no native 429. These observations do not
establish a TPM limit or the exact cause of the uncorrelated 503s. The first 800
responses had all qualified; final errors must not be hidden by that partial run.
Reported usage was 1,399,852 input-plus-output tokens. The original native parser
mislabeled the missing-usage error terminal as a content mismatch and omitted one
32,768-token reserve. The original summary remains unchanged; its failure
investigation and budget-correction files retain four uncertain-usage outcomes
and a corrected 131,072-token case reserve. No inference was replayed.

Local red/green regressions now distinguish error/failed/incomplete terminals
without usage from independently checked content errors. A separate production
controller regression showed that positive Retry-After hints on ordinary failures
were ignored for new admission. Source `90e42af` honors those hints while keeping
the failed inference terminal and its window reservation occupied. Only an
independent queued item becomes the recovery probe. A workflow version marker
preserves old histories; workflow/classification/race/replay checks pass. See
`failure-hint-verification.json`. This adds admission backpressure without
changing which inference outcomes may be replayed.

The ten-job local run initially exceeded its unchanged eight-minute job deadline;
the first job retained 593 successes and 407 deadline failures. After the credit
fix, the same check passed: 10,000 globally distinct validated results, exactly
10,000 local native-SSE calls, one attempt per item, two isolated bearer clients,
combined overlap at most 128 and a naturally changed dispatcher run ID. Gate wall
time was 322.620 seconds including setup and verification. This fixture disables
the provider-window estimate; it establishes orchestration capacity, not CPA
10,000-call throughput. The exact numeric peak was lost to bounded stdout
truncation; the unchanged source assertion establishes the upper bound.
See `ten-thousand-before.json`, `ten-thousand-before-identity.json` and
`ten-thousand-after.json`.

## 5. Token length and diagnostics

The earlier [output-length study](../../cpa-output-token-study/2026-10-16/report.md)
retained 65 validated outputs from 66 calls, including one content failure.
It measured 40,558 generated tokens including reasoning, no correlated native
429, short-output medians around 34 tokens/2.3 seconds, and long-output medians
around 1,165–1,187 tokens/9.5–9.8 seconds. That small study did not isolate a TPM
boundary. The final all-long and sustained mixed-output cases extend the workload;
record their actual outcomes rather than treating response length as irrelevant.

CPA's pinned translator removes `max_output_tokens`; it is not an enforced spend
bound on this path. Workload qualification instead requests independently checked,
bounded output. Reasoning is included in output tokens and is not added twice.
Campaign guards charge reported input plus output, reserve 32,768 tokens per
uncertain/active attempt, and stop on account restrictions, substantial unknowns,
changed runtime identity or spend boundary. Opaque native usage remains unknown.
The 10-million-token authorization is a ceiling, not a target to consume.

CPA still uses native retry 3. Caller attempts may hide multiple native attempts;
`cpa-log-counts.json` reports bounded correlated 429/access-log counts, not total
native attempts. Missing correlation IDs make coverage partial. No raw CPA log,
auth record, prompt, response text or credential is included in published evidence.

## 6. Actual Temporal/provider acceptance and verification

The [actual provider qualification](temporal-sustained-mixed-windowed-3000-223016/summary.json)
passed on clean `1c788aca9443a11d6fbe6b47eba1baa60eed32b4` (runtime 90e42af,
then test-diagnostic-only change). Three 1,000-item jobs from two bearer clients
shared one pool. All 3,000 distinct downloaded responses independently qualified;
300 required the exact 512-word payload. Core successes 3,000, item failures 0,
observed main caller attempts 3,000, observed peak overlap 128. Both pilots passed.
No ordinary/unknown/account errors or caller refusals were observed.

All 3,002 bounded CPA IDs (including pilots) correlated within the case's time
interval. Correlated native 429/500/502/503/504 and caller 429/503/401/403 counters
were zero. This does not reveal the total number of hidden CPA native attempts.
The [verification](temporal-sustained-mixed-windowed-3000-223016/verification.json)
retains the accepted canonical live task and clean cleanup, separately from
[derived timing](temporal-sustained-mixed-windowed-3000-223016/derived-timing.json)
and [dispatch observations](temporal-sustained-mixed-windowed-3000-223016/dispatch-timing.json).

Results were ready 1,032.482 seconds after first submission (17m 12.482s), observed
at 1,036.566 seconds, and all verified downloads finished at 1,044.037 seconds.
Main reported usage was 472,500 input plus 347,760 output tokens including reasoning;
including pilots the case reported 821,576 tokens. Median generation was 1,230.5 ms,
p95 was 5,965 ms, maximum 167,377 ms. That longest response reported 42 output tokens
and zero reasoning, headers 613 ms and first-output 166,185 ms under the core's SSE
clock. Header and first-output origins differ; do not subtract them as equal clocks.

The last admission episode released 127 calls, then the remaining 73 started at
1,023.979 seconds after first dispatch, after the long in-flight call completed.
The dispatcher must drain active activities before history rollover; the
observation is consistent with that boundary, but its precise trigger was not
retained in the bounded summary. Ordinary admission has no group-completion
barrier; run continuation does have an in-flight drain. The provider tail alone
could determine whole-job completion. Do not label the entire 17-minute duration
as rate-limit waiting or claim uninterrupted work conservation across rollover.

The provider campaign is now stopped. `campaign-budget.json` records
3,235,681 reported input-plus-output tokens and 163,840 conservatively reserved
uncertain-usage tokens against the 10-million allowance. Both pilots and the
all-long reservation correction are included. Opaque internal CPA usage is not
observable. `environment-after.json` matches the initial image/config/start/
restart identities; no shared CPA setting changed.

The final browser-free release passed all 30 canonical tasks on clean
`edaea2a6c29c744e6cfec606954ae4dc090805e1`, with no cleanup errors or warnings.
`release-final.json` records the accepted report/hash/manifest and task statuses.
`fast-final.json` records the exact ten fast-task commands accepted within that
release; it is not a separately executed fast-selector run. The only change after
the accepted provider source `1c788ac` is a test assertion's safe error context;
runtime controller source remains `90e42af`. `source-identities.json` verifies
that equivalence. Earlier accepted verification on `826757c` and `b866bb6` is
retained as `release-before-failure-hints.json` and
`fast-before-failure-hints.json`; it does not substitute for current acceptance.

Two intervening full attempts failed: the `90e42af` preset/LiveView workflow
showed a draft error for OpenAI GPT-5.4, and the `1c788ac` reference-profile
export-redaction case returned storage_unavailable during SaveProfile. The
original assertions did not retain underlying causes. Test-only diagnostic
changes preserve the same predicates, fixtures and deadlines. Both focused
checks then passed, so the causes remain unconfirmed; no product behavior fix is
claimed. Their failed aggregates and accepted focused reports are retained in
`release-failure-hint-first-attempt.json`,
`release-diagnostic-first-attempt.json`, `frontend-focused-diagnostic.json` and
`storage-focused-diagnostic.json`. Canceled tasks are not independent assertion
failures. These observations remain a local test-reliability limitation.

The accepted final gate used one concurrent CPU task on this 12-CPU host to
reduce peer compilation pressure, with GOMAXPROCS=4 and four Erlang schedulers.
The integration command remained `go test -p=3 ./... -tags=integration -count=1`.
All original cases/assertions and the 600 ms transaction / 500 ms SQL statement
limits remain unchanged. `release-host-resource-setting.json` verifies the
resource setting and unchanged SQL source. This does not prove the earlier
failure causes or qualify every host/concurrency setting. It does not change
CPA admission or the provider experiment's 128-call overlap.

An earlier fresh-worktree release failed because locked Phoenix dependencies
were missing. Installing the existing lockfile allowed the accepted `826757c`
aggregate without assertion/version changes; its failed bootstrap report is
retained. Another early fast check found public Go research-cache files inside
the scanned repository tree. The cache was moved outside the tree, preserving
the static assertions. These preflight failures are distinct from provider and
current-source acceptance.

## 7. Shared authority, waiting and rollout

Every participating client must submit `/v1/jobs` through replicas using the same
Temporal namespace, task queue and configured pool identity. Queue order is
round-robin across jobs; no per-tenant weighted fairness or client-share guarantee
is implemented. More jobs can change a client's share. Separate namespaces with
the same pool-name string do not coordinate. Ordinary HLLM `/v1/responses` calls
and direct CPA calls remain outside this batch authority. Route shared-account
work through the dispatcher to obtain its global admission guarantee.

An accepted job has authenticated status/result URLs. Status reports queued,
active, retry-waiting, succeeded and failed counts, read time, separate worker
heartbeat/progress timestamps, waiting reason and next-attempt time. The waiter
emits five-second updates while preserving a hard overall deadline. A fresh poll
is not proof of fresh worker/provider progress. Unknown responses stay item
errors; explicit refusals requeue. Account/auth/billing restrictions pause the
shared pool and require operator review/resume, not aggressive automatic retries.

The [research notes](research-notes.md) distinguish documented shared usage
allowances from measured short-term behavior. Passive management inspection
exposed Retry-After but no numeric remaining-capacity watermark. No experiment
establishes a ban-safe rate, suspension threshold, universal RPM/TPM ceiling or
six-minute guarantee for arbitrary prompts. Longer prompts/context may change
account consumption and latency; requalify representative workloads and current
runtime identity before changing the policy.

This work is a draft PR, not deployment. Rollout still requires the environment's
private Temporal namespace/network, HLLM-scoped Garage configuration, additive
schema application, API/worker images from the verified source and explicit
provider acceptance. Preserve accepted jobs and histories during rollout/rollback.
