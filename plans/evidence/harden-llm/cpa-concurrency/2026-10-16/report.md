# CPA-only concurrency and pacing measurement

## 1. Experiment and attribution

Live CPA-only tests on 2026-10-16 UTC use `gpt-6-luna`, native reasoning `low`,
a fresh nonce JSON schema, no tools, cache disabled and one HLLM attempt. Calls
pass through the real stateful service and gateway handlers on a private local
HTTP listener, then `https://cpa.prls.co/v1`. No official API route is constructed.
No official API credential is supplied. Shared CPA configuration is unchanged.

Sources: `dffcc3818f7c2089006edb7af35ca737b3402cd5` for the initial ramp and
`b0de2bbc7f39e0664c26fdbdb313fe1c790c6b55` for isolated levels and public paced backlogs.
The loopback control uses `f1387fe` (full SHA in metadata/summary).
The experiment sets response-header timeout to 60 s within the ordinary 60 s
request deadline; production's default 30 s transport budget is unchanged.
`environment.json` records the unchanged CPA image/configuration and binary hash.

The workload is deliberately short and synthetic. A slot count is not a provider
entitlement. Actual HTTP interval overlap is reconstructed from call start/end
measurements; each validated result also confirms one dispatched HLLM provider
attempt. Native Codex execution concurrency and CPA internal attempt counts are
not exposed. The shared account/service is not isolated from other clients.

## 2. Observed burst boundary

Each level uses three fresh unpaced waves and five seconds between waves. A
failed wave drains in-flight work and stops escalation without replay.

| Simultaneous calls | Completed / submitted | Outcome |
| --- | --- | --- |
| 1 | 3 / 3 | All three waves passed |
| 2 | 6 / 6 | All three waves passed |
| 4 | 12 / 12 | All three waves passed |
| 8 | 24 / 24 | All three waves passed |
| 16 | 48 / 48 | All three waves passed |
| 32 | 96 / 96 | All three waves passed |
| 64 | 168 / 192 | First two waves 64/64; third 40/64, with 24 HTTP 429s |

The initial ramp submitted 381 calls: 357 validated results and 24 known
rejections, all `rate_limit_exceeded`; no unknown outcomes or API overflow.
Actual interval overlap reached every target, including 64. The failing 64-call
wave began at 01:19:56 UTC and its errors surfaced after approximately 18–26 s.
There was no Retry-After hint on these 24 final gateway errors.

CPA's own logs contain native Codex `{"detail":"Rate limit exceeded"}` failures,
followed by credential cooldown messages. This establishes a real upstream
throttle rather than the previous campaign's transport-timeout confound.
Log counters are time-correlated shared observations, not per-request native
attempt counts; do not interpret multiple log status lines as unique calls.

After more than two quiet minutes, three fresh 64-call waves all passed:
**192/192 validated results at actual overlap 64**, in approximately 24.5 s.
Consequently, neither 32 nor 64 is established as a fixed hard concurrency cap.
Recent request volume/rate affects the outcome. The failing ramp has roughly
200 successful starts in its final 30 s window; this is a descriptive observation,
not proof of an exact 200-per-30-second provider quota.

## 3. Sustained rate experiments

Starting a four-starts/second backlog immediately after the 192-call recovery
burst produced 28 successes and 12 known rejections among 40 dispatched calls.
Peak HTTP overlap was 32. Most successful calls in this contaminated phase took
28–35 s, consistent with substantial waiting/internal retry overhead. The pending
backlog was preserved and not replayed. One `model_cooldown` rejection carried a
16 s Retry-After hint; other failures were generic `rate_limit_exceeded`.

Repeating from a separate 120-second quiet period still failed: 210 validated
results and one `rate_limit_exceeded` rejection among 211 dispatched calls, with
**actual peak overlap only 12**. The rejection was item 201, started about 56 s
after the first item. This falsifies the assumption that a 32-slot cap with a
250 ms minimum start interval is a clean sustained operating point. It also
shows throttling can occur well below the demonstrated 64-call concurrency.

The next public experiment used a 120-second quiet period, a 500 ms minimum
start interval, a 16-slot cap and 1,000 fresh logical items. It stopped after
190 validated results and two simultaneous **network errors**, among 192
submitted calls. Peak overlap was six, and there was no final HTTP 429. Both
written-request failures ended at 01:30:46.459 UTC, after 148 ms and 698 ms.
CPA stayed running, with successful log entries around this time; those shared
entries do not resolve either request identity/outcome. Preserve both unknowns.
This run cannot qualify 1,000 successful calls or a lower provider throttle cap.

## 4. Same-instance loopback control

The experiment-owned TLS proxy forwards to the same existing CPA instance at
`http://127.0.0.1:8317`, using the same key, model and validated workload. This
removes the public ingress/Internet path and changes that hop to HTTP/1.1.
The exact private address and generated certificate are explicitly trusted only
inside this control. Production egress policy and shared CPA settings do not
change. The local proxy regression verifies request and Retry-After forwarding.

A fresh 128-call wave attained HTTP overlap 128 and completed 127 validated
results; one caller reached its 60 s deadline without a response. That outcome
is unknown, with no returned HLLM attempt diagnostics, and is never replayed.
No final 429 occurred. The second wave was not launched. This does not prove
128 is an upstream concurrency rejection or a fully qualified operating point.

The independent paced loopback test followed another 120-second quiet period,
used a 500 ms minimum start interval and 16 slots, and accepted a fresh 1,000-item
backlog. It stopped after 176 dispatches: **175 validated results and one 60 s
unknown outcome**, leaving 824 undispatched. Actual peak overlap was seven;
measured starts averaged 1.894/s. Successful calls had p50 1256 ms, p95 3924 ms
and maximum 9339 ms. The timed-out item started about 33 s into the experiment,
while subsequent independent calls completed. No final 429 appeared, and CPA's
logs contain no native 429 or credential-cooldown messages in this window.
A CPA 499 is time-correlated with the deadline, but cannot resolve its outcome.

Therefore, bypassing public ingress does not remove every stall. Both public
network failures and low-load 60 s stalls need separate diagnosis; reducing
concurrency alone cannot establish complete 1,000-result acceptance.

## 5. Evidence and checks

Bounded phase summaries retain result counts, outcomes, actual HTTP overlap,
successful p50/p95 durations and phase timestamps. Raw `events.ndjson`, atomic
`status.json`, launch logs and metadata remain private local artifacts in the
named run directories. Five-second heartbeats do not extend fixed deadlines.
The four unknown outcomes above remain retained without replay. A rejected item can remain queued in the
scheduler checkpoint because escalation stopped before its retry; use terminal
attempt events to count actual provider rejections.

Local deterministic checks: all ten fast tasks passed, targeted Go race checks,
`go vet` and whitespace checks passed. Hosted test hierarchy checks passed on
`f1387fe` (runs 38835956885 and 38835951864). No browser, production deployment, shared CPA tuning or production
queue integration was performed.


## 6. Decision and remaining gate

Across all seven experiments, **1,320 CPA calls were dispatched: 1,279 validated
results, 37 known 429 rejections and four unknown outcomes**. No official API
inference ran. Unknowns were not replayed; no accepted backlog was reported as
complete. Every completed phase retained an atomic final status and summary.
CPA's image/configuration hashes remained unchanged, with zero container restarts.

The highest fully passing tested burst level is **64 concurrent calls**,
including an isolated 192/192 repeat. The 128-call control returned 127 valid
results within 3.283 s, with one 60 s unknown. Repeating 64-call bursts too
quickly caused explicit native throttling. A paced run also throttled at actual
overlap 12. Consequently, there is **no measured universal concurrency ceiling**
and no evidence for a two-slot hard CPA limit.

For this short workload, the tested rate points bracket an operating region:
approximately **1.9 starts/s produced no observed native 429**, while approximately
**3.6 starts/s produced 429** even after a quiet period. This is not an exact
account quota or a guarantee: account/background load and prompt/output size
remain uncontrolled, and the lower rate still exposed separate reliability faults.

A 1,000-item producer needs one durable shared queue with both a slot cap and a
start-rate cap, shared Retry-After/cooldown handling, and explicit unknown-result
state. A provisional next operating point is at most 16 active calls and two
starts/s, with headroom below that rate for shared/background traffic. It remains
**unqualified for 1,000 clean results**. The tests do not justify launching 1,000
HTTP requests together or increasing in-request retry waits. There is no static
slot number that fixes the demonstrated network errors or 60 s stalls.

The next acceptance gate is diagnosis of the unknown outcomes with safe
transport-cause classification and CPA/upstream request correlation, followed by
another paced 1,000-result test. The shared configuration owner is
[CPA #6](https://github.com/prls-co/CLIProxyAPI-setup/issues/6); durable producer
admission/status remains [Query Set #69](https://github.com/prls-co/query-set/issues/69).
No shared tuning or cross-repository implementation was performed by these tests.
