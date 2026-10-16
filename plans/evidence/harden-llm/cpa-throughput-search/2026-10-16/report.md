# CPA throughput search and fastest 1000-call strategy

## 1. Decision

For 1000 separate `gpt-6-luna` requests at native reasoning `low`, the best
**public CPA** run completed all 1000 in **255.558 s (4 min 15.6 s)**:

- At most **128 active HTTP requests**.
- Up to **200 admissions per 61-second window**, with immediate admission while credits remain.
- Native upstream streaming; validate the exact nonce, model, low effort and usage at `response.completed`.
- Queue all 1000 logical items immediately; quota waiting happens before HTTP dispatch.
- Keep in-flight stragglers running while independent later work proceeds.

The public run made exactly 1000 provider HTTP requests, with **zero final 429s,
zero unknown outcomes, and zero native upstream 429s in the covered CPA log
window**. Every response reported `gpt-6-luna` and reasoning `low`. No official
OpenAI API, extra identity, account rotation, shared CPA configuration change or
production deployment was used.

The fastest same-host run was **253.175 s (4 min 13.2 s)** with 200 active slots
and 60.1-second windows. It exposed seven native 429 log lines hidden by CPA's
internal retries, despite zero caller-visible rejections. The 61-second cadence
is therefore grounded in a tested boundary; more aggressive admission can add
internal waiting without materially improving whole-batch time.

These are workload/account-specific measurements, not a published universal CPA
quota or a proof of the mathematical minimum under arbitrary provider latency.

## 2. Doubling and bisection

The native test removes HLLM's 60 s operation deadline and its transport's
128-connection ceiling as calibration constraints. Its connection pool has no
active-connection limit below the configured test concurrency. The native
request budget is 180 s, with a fixed campaign deadline and five-second progress.
Neither production HLLM policy nor shared CPA settings changed.

Candidates doubled from 1 through 256. Larger independent samples were separated
by 90 s of recorded quiet time to reduce quota carryover. After the first explicit
throttle, bisection tested 192, 224, 208 and 200. An adjacent 201-call test then
resolved the practical burst boundary directly.

| Simultaneous candidate | Validated results | HTTP 429 | Unknown |
| --- | --- | --- | --- |
| 1, 2, 4, 8, 16, 32, 64, 128 | Every submitted call | 0 | 0 |
| 256 | 200 | 56 | 0 |
| 192 | 192 | 0 | 0 |
| 224 | 200 | 24 | 0 |
| 208 | 200 | 8 | 0 |
| 200 | 200 | 0 | 0 |
| 201 | 200 | 1 | 0 |

All overload samples admitted exactly 200 successful requests. The sustained
queue showed a further roughly 200 successes per minute. Together these identify
an effective **200-admission burst budget and approximately 60-second quota
window** for this CPA route/model/workload. It is an admission-rate constraint;
a static semaphore alone does not enforce it. Other clients sharing the Codex
credential pool can consume the same observed capacity; the exact underlying
account/IP/model quota scope was not independently isolated.

Only explicit rejection defines the throttle bracket. A timeout would remain a
separate unknown outcome, not be relabeled as a concurrency limit. No unknown
occurred in these new experiments. The cold-wave completion-rate maximum was at
128 slots (128 results in 3.725 s); increasing admission concurrency to 200 did
not establish a better full-batch result on the public endpoint.

## 3. Full 1000-result comparisons

All rows below completed exactly 1000 distinct IDs and nonces. No partial result
or successful HTTP status alone counted as completion. Inputs were deliberately
short synthetic nonce tasks; real application prompts/output lengths remain a
separate qualification.

| Endpoint and admission policy | Active cap | Full batch | Caller HTTP attempts | Final 429s | Unknown |
| --- | --- | --- | --- | --- | --- |
| Same host, immediately refill pool | 200 | 265.680 s | 1781 | 781 | 0 |
| Public, 200 admissions / 61 s | 200 | 360.400 s | 1000 | 0 | 0 |
| Same host, 200 admissions / 60.1 s | 200 | 253.175 s | 1000 | 0 | 0 |
| Public, 200 admissions / 61 s | 128 | **255.558 s** | **1000** | **0** | **0** |

| Policy | 950 results | 999 results | 1000 results |
| --- | --- | --- | --- |
| Same-host immediate refill | 262.491 s | 264.650 s | 265.680 s |
| Public timed, 200 active | 245.701 s | 258.618 s | 360.400 s |
| Same-host timed, 200 active | 242.304 s | 252.476 s | 253.175 s |
| Public timed, 128 active | 246.496 s | 248.784 s | 255.558 s |

The immediately refilled pool spent substantial effort sending requests before
quota recovered. The timed queues used the same admission budget in roughly
five releases at 0, 61, 122, 183 and 244 s. This approaches the observed quota
floor of roughly four minutes plus final inference time for a cold 1000-item
backlog. A flat two-starts/second limiter would leave observed burst capacity
unused and require over eight minutes before the last dispatch.

Backend/CPA-path latency has a heavy tail. The public 200-slot timed run had two
successful requests above 60 s and finished in six minutes despite zero native
rate-limit log lines. One stream opened in about 1.34 s but produced its first
output around 118.8 s. The final straggler determined the whole-batch time.
This is distinct from quota waiting. The 128-slot public run had no individual
request above 60 s. One trial per policy does not prove a causal reduction in
stalls or establish a long-run latency guarantee.

The fastest same-host run also included two requests above 60 s; they started
early enough to overlap later work. Queue advancement must not wait for every
request from an earlier release to finish.

## 4. Production structure for the 1000 calls

Use one durable admission queue shared by all producers using the same upstream
credential pool, with a rolling timestamp ledger for up to 200 admissions per roughly
60 s and a measured scheduling guard. A 61 s eligibility interval passed the
public test without upstream 429s. Reserve a credit before dispatch, refund only
an explicit rejection, and retain unknown reservations until their window ages
out. A process-local limiter does not enforce aggregate limits across instances.

Use a 128-slot pool as the currently qualified public starting point. Admit the
first 128 immediately, then fill remaining credits as slots free; wait for the
next quota window only when credits are exhausted. Keep active slow calls while
later eligible requests execute. Upstream streaming provides headers and output
timing, and the collector should finish at `response.completed`, without waiting
for the connection to close.

On a known 429, honor Retry-After and pause shared admission. If the provider
returns no hint, use bounded backoff and one recovery probe before reopening the
pool. Preserve every attempt, including CPA's opaque internal-attempt boundary.
The experiment's unknown outcomes are never replayed automatically. Its literal
1000-result runs did not use replacement tasks, grouping, or speculative copies.

Return the application's accepted job/status URL immediately. Progress should
show 1000 accepted, completed, active, queued, failed/unconfirmed and the next
eligible admission time. Emit liveness updates while waiting for quota or an
in-flight straggler. Publish the complete result after every required item has a
validated result. The benchmark demonstrates this with atomic local status and
five-second heartbeat artifacts, not a newly deployed public job API.

## 5. HLLM integration and CPA tuning boundaries

The native benchmark bypasses the ordinary HLLM execution core. HLLM's existing
60 s operation budget and ordinary provider construction were **not changed**.
The best public native run happened to keep every call below 60 s, but other
successful native runs needed longer. This evidence does not qualify an ordinary
HLLM gateway 1000-call drain or deploy a production batch queue. Provider-stream
forwarding and long-tail behavior need explicit integration/acceptance at their
owners; waiting for quota belongs outside each individual HLLM request.

Shared CPA remains pinned to the recorded image with `request-retry: 3`,
`max-retry-credentials: 1`, and `max-retry-interval: 30`. Native 429s can be hidden
by CPA retries and add tens of seconds, so observe both caller diagnostics and
CPA upstream error/cooldown logs. The aggressive 60.1 s run demonstrates this.
Reducing CPA in-request retries may be worth a separate owner experiment once
the shared durable admission authority exists; it was not tested or changed
here. More credentials or rotation were not used to obtain the measured rate.

CPA shared admission/configuration belongs to
[CPA #6](https://github.com/prls-co/CLIProxyAPI-setup/issues/6). The Firebase
producer/status boundary remains
[Query Set #69](https://github.com/prls-co/query-set/issues/69), with durable
continuation ownership in
[FIOr #8](https://github.com/prls-co/firebase-orchestration/issues/8).
HLLM #96 is separate official-API fallback work and is not used by these tests.

## 6. Reproduction and evidence

```sh
go run -tags=live ./cmd/harden-llm-cpabench --live \
  --endpoint public --action drain --concurrency 128 --calls 1000 \
  --window-burst 200 --window 61s --call-timeout 180s \
  --output /private/new-cpa-run
```

Supply only `CPA_API_KEY` through the protected environment source. The command
is live-tagged, explicit opt-in, and outside ordinary deterministic checks.
Default two-hour hard inference limits remain bounded; each run uses a new
private directory. No key, prompt body, account label or bearer material is logged.

- `summary.json`: per-phase source/configuration, validated identity counts, actual HTTP overlap, timing distributions, milestones and raw-artifact SHA256 hashes.
- `cpa-log-observation.json`: bounded safe log counters and slow timing samples, with explicit coverage limits; no raw logs or account identifiers.
- `environment.json`: unchanged image/configuration fingerprints and process health.
- `validation.json`: three local 10-task fast passes, targeted race/vet results and hosted checks.
- Private run directories: complete events, metadata and final atomic status; not committed as bulk live fixtures.

The campaign dispatched **6317 caller HTTP attempts: 5447 validated results and
870 known rejections**, including four literal 1000-result drains. There were
zero unknown outcomes in the new campaign. CPA internal native attempts are
unknown and are not equated with these caller HTTP counts. Original unknown
outcomes from earlier campaigns remain retained and were not replayed.

Sources: search/first drain `2b41ae8`, adjacent 201-call test `6bdb21a`, and timed
comparisons `952b923`; full SHAs and tracked-diff fingerprints are in the bounded
summary. All three local fast runs passed 10/10 with no cleanup errors. Focused
Go race tests, vet and whitespace checks passed; hosted hierarchy checks passed
on `952b923`. No browser tests, production deployment or shared CPA tuning ran.
