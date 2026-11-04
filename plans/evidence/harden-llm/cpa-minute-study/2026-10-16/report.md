# CPA admission-window timing follow-up

## 1. Question and scope

The previous cold-wave search established a 200-admission burst boundary. It did
not prove an exact reset period, a wall-clock minute bucket, or a universal CPA
entitlement. This follow-up tests the proposed six-minute schedule and separates
rate-window recovery from CPA's own cooldown/retry behavior.

All calls are separate native streaming CPA `gpt-6-luna` / `low` requests, each
with its own nonce and validated model, effort, usage and `response.completed`.
No task grouping, official API, credential rotation, shared CPA configuration
change, or ordinary HLLM execution is involved. The native per-call deadline is
180 seconds. Caller retries are disabled throughout this study. Unknown outcomes
are retained without replay; later waves contain independent requests.

## 2. Six waves of 200 at exactly 60-second intervals

Dispatch starts were scheduled against one monotonic clock at 0, 60, 120, 180,
240 and 300 seconds; launch error stayed below 3 ms. The observation continued
through six minutes. This is 1200 separate requests, with the first five waves
representing the requested 1000-item backlog.

| Wave | Dispatch offset | Valid results | Unconfirmed HTTP 503 | Last result offset |
| --- | --- | --- | --- | --- |
| 1 | 0 s | 200 | 0 | 7.554 s |
| 2 | 60 s | 200 | 0 | 77.306 s |
| 3 | 120 s | 200 | 0 | 139.653 s |
| 4 | 180 s | 200 | 0 | 194.511 s |
| 5 | 240 s | 197 | 3 | 283.651 s |
| 6 | 300 s | 186 | 14 | 314.333 s |

Result: **1183 validated completions and 17 unconfirmed HTTP 503 responses from
1200 caller HTTP attempts**. The first 1000 requests yielded 997 validated
results, not a qualified 1000-result drain. The sixth wave does not replace the
three unconfirmed items from wave five. The global 1000th completion at 301.448 s
includes sixth-wave work and must not be reported as completion of the original
1000-item backlog.

All 17 HTTP 503 responses carried `internal_server_error`, arrived quickly and
had no Retry-After. They were not client deadline expirations. The shared origin
log window contains 630 upstream 429 lines and 240 auth-unavailable lines,
despite zero caller-visible 429s. Those counters are time-window observations;
the first run predates caller trace-ID capture, so exact attribution of each
internal retry to a caller is unavailable. An origin-internal join found all 17
HTTP 503 access IDs but no matching upstream-failure or auth-unavailable lines
for those same IDs. Cooldown contention is a hypothesis, not a proved cause of
the 503s. Do not automatically replay these outcomes based on the counters.

The simple minute schedule therefore sustains substantial throughput but is
not a reliable all-results policy in this run. Caller success/status alone also
misses CPA's native throttles.

## 3. Recovery probes and hidden cooldown

After a quiet interval, a fresh 200-request seed burst began near wall-clock
second 20, away from a minute boundary. Nine additional independent one-request
probes started at relative 10, 25, 35, 45, 55, 59, 61, 65 and 75 seconds.
The seed completed 199 results promptly; one stream reached the 180-second
benchmark deadline and remains unconfirmed. It was not replayed.

| Probe start | Outcome | Caller duration | Observation |
| --- | --- | --- | --- |
| 10 s | HTTP 429 | 32.266 s | Native 429s near 10, 21 and 32 s |
| 25 s | HTTP 429 | 32.909 s | Native 429s near 25 and 40 s |
| 35 s | HTTP 429 | 21.703 s | Native 429 near 56 s |
| 45 s | HTTP 429 `model_cooldown` | 12.398 s | Retry-After: 32 s |
| 55 s | HTTP 429 | 1.745 s | Native 429 near 56 s |
| 59 s | Valid result | 31.135 s | Completed near 90.1 s |
| 61 s | Valid result | 29.689 s | Completed near 90.7 s |
| 65 s | Valid result | 25.558 s | Completed near 90.6 s |
| 75 s | Valid result | 15.216 s | Completed near 90.2 s |

Probe measurements capture only the safe eight-hex request-ID suffix from CPA's
trace header. Exact IDs join the native 429s and auth-unavailable events above
to their own probe calls; the full trace header contains credential metadata
and is never retained. Origin timestamps have one-second resolution.

The continued native refusal near 56 s excludes a simple wall-clock-minute reset
near relative 40 s in this sample, and quick continuous refill was not observed.
However, probes themselves caused repeated native failures and model cooldown.
The successful 59–75 s probes waited behind that cooldown until about 90 s.
Their success must not be used to claim either an exact 60 s provider reset or
an underlying 90 s provider quota. CPA recovery and provider admission are
separate timing boundaries.

## 4. Split-burst discrimination

The independently isolated schedule submitted 100 requests at 0 s, 100 at 30 s,
and 200 at 61 s. All **400/400** completed by 65.632 s, with zero caller
rejections, zero unknowns, and zero upstream 429 or auth-unavailable lines
correlated to the exact 400 caller request IDs. The first two bursts finished
within 3.42 s and 3.13 s; all 200 requests in the third burst finished within
4.62 s.

This includes **300 successful requests between approximately 30 and 65.6 s**,
a 35.6-second interval. A strict rolling 200-requests-in-any-60-seconds limit
would have rejected or delayed at least 100 of the third burst. That model is
therefore contradicted for the observed CPA route in this run. Discrete bucket
refill / fixed-window behavior is a better current explanation. Exact reset
phase, underlying account/IP/model scope and provider implementation remain
unisolated; the experiment does not establish a published universal quota.

This corrects the earlier recommendation to treat a rolling timestamp ledger as
an identified provider contract. Such a ledger can enforce a chosen application
budget, but it is not the measured provider algorithm.

## 5. Clean recovery bracket

Two additional isolated experiments avoid the pre-reset probe cascade:

- Full 200-call seed at 0 s, one call at 61 s, then 199 at 62 s: **400/400**
  validated results by 65.172 s, with zero native 429s correlated to the exact
  400 IDs. The 61-second probe received headers after 0.602 s and completed in
  1.115 s. The remaining 199 completed within 3.16 s of their release.
- Full 200-call seed at 0 s, only one additional probe at 59 s: **201/201**
  validated results. That probe's exact request ID has one native upstream 429
  at relative 59.215 s; CPA internally retried and the call completed after
  12.117 s, at relative 71.130 s. Its caller-visible HTTP status was 200.

These samples place the observed recovery near **60 seconds**: the clean
59-second probe was still refused upstream, while the clean 61-second probe was
accepted promptly. Arrival/network jitter and one-second origin log timestamps
prevent a sub-second exact entitlement claim. The evidence supports an
approximately minute-long discrete budget/reset on this route; it does not
establish a strict rolling window or a universal CPA quota.

The best retained public 1000-result run remains 255.558 s with 128 active
requests and 200 admissions per 61 s. The exact-60s follow-up does not replace
that acceptance with an all-green result. For a cold backlog, dispatch at roughly
0, 61, 122, 183 and 244 s is the currently measured clean strategy; keep slow
calls independent and observe both caller and native CPA retries. A shared
admission authority must account for other clients and adaptive recovery.

Next distinct measurements would vary prompt/output token budgets to separate
request-count and token-sensitive limits, and compare CPA internal retries with
an application-owned queue in an isolated owner-approved CPA environment.
Disabling shared CPA retries/cooling was not tested or performed here.

## 6. Diagnostics and reproduction

The optional `runner.py` schedules independent invocations of the existing
live-tagged native benchmark. It requires explicit `--live`, uses only the
protected CPA key source, disables caller retries, writes atomic status and
five-second heartbeat records, and stops future admission on a policy response.
Each invocation has a fresh private directory; no accepted item is replaced.

```sh
go build -tags=live -o /tmp/hllm-cpabench-minute-study ./cmd/harden-llm-cpabench
python3 plans/evidence/harden-llm/cpa-minute-study/2026-10-16/runner.py --live \
  --binary /tmp/hllm-cpabench-minute-study --endpoint public \
  --output /private/new-six-wave-run \
  --schedule '0:200,60:200,120:200,180:200,240:200,300:200' \
  --observe-for 360
```

Use independently isolated runs for recovery/split experiments; simultaneous
campaigns would consume the same quota and invalidate the comparison. Private
raw directories retain complete event/status/metadata records. Bounded
`summary.json` records outcome identity checks, timing distributions, source and
raw-event SHA256 fingerprints. `cpa-log-observation.json` records safe counters,
coverage and exact-request joins without raw log contents. The initial scheduler
source is retained privately with a verified hash; the supplied runner adds an
explicit opt-in flag and selectable benchmark binary for reproduction. The
supplied runner also writes its final summary atomically to `status.json`; the
measured scheduler left its final outcome in `summary.json` and could leave the
last heartbeat in `status.json`. Dispatch logic is unchanged, and both measured
scheduler sources are retained privately under their recorded hashes.

Shared CPA remains at its recorded image/configuration with request-retry 3,
max-retry-credentials 1 and max-retry-interval 30 s. The current ordinary HLLM
60-second deadline, transport construction, production queue and deployment
remain unchanged. These tests do not qualify an ordinary HLLM 1000-result drain.


## 7. Validation and boundaries

The five experiments made **2410 caller HTTP requests: 2387 validated results,
five explicit caller 429 refusals and 18 unconfirmed outcomes**. The latter are
17 fast HTTP 503s plus one HTTP-200 stream with headers at 0.816 s but no first
output or terminal result by 180 s. All were retained without replay. CPA's
internal upstream attempts are separate from these caller counts.

Source `cde3754` ran the six-wave experiment; `6816ed3` ran the correlated follow-ups.
The new correlation regression first failed before implementation, then focused
Go race tests, live-driver build/vet and all ten local fast tasks passed. Hosted
hierarchy checks passed on `6816ed3`. Whitespace, JSON and artifact secret scans
passed. No browser testing, shared CPA tuning, official API fallback, production
deployment or ordinary HLLM 1000-result acceptance is claimed.
