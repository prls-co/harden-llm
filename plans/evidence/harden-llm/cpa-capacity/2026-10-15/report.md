# CPA Luna-low capacity campaign — 2026-10-15

## 1. Result and decision

The 1000-item backlog reached 996 validated results and four unconfirmed CPA outcomes. Acceptance is **incomplete**, not passed. No queued or active work remains. The driver did not replay uncertain calls. It exited with status 1; the observer did not time out.

The drain took 768.308 seconds (12 min 48.308 s); the complete qualification/ramp/drain campaign took 972.399 seconds (16 min 12.399 s). Conservative official API usage estimate including its qualification was $0.0148985 against the $5 cap. All 718 official API calls returned complete usage; the raw floating-point reservation remainder is approximately zero (4.34e-19 USD), not an unresolved API request. This is a usage estimate, not invoice reconciliation.

The short ramp observed no rejection at a configured ceiling of four starts/second (3.433 measured mean starts/second) and eight actually active requests. It does not establish a 16-request upstream cap or sustained safe rate. Sustained CPA traffic exposed transport failures, so production capacity remains unqualified.

## 2. Exact setup and scope

- Live source: `545a4954e232959a855b424d8f15947959ff9cbd`, clean tracked diff (SHA-256 of empty diff).
- Corrected driver: `fc95e608241d1d3af28fde07b219e76bd31fd790`; deterministic/hosted checks passed, but no second live campaign ran.
- Campaign: `20261015T214844Z`, 2026-10-15 21:48:44Z–22:04:57Z.
- Isolated loopback HLLM gateway using existing stateful composition; public CPA and official API upstreams; no profiles, database, history or cache.
- `gpt-6-luna`, native prepared effort `low`, unique nonce schema, no requested tools/search, `max_output_tokens=512`; API explicitly selected Standard tier. CPA token-cap handling is not established by the short outputs.
- HLLM one attempt, repair/rerun disabled; overall 60 s, provider response-header default 30 s. CPA internal retries remained enabled and are not counted/observable through this response.
- CPA `v7.3.8-prls.3`, digest `sha256:99bedd436cf04530451aeff67b88d3e76dfff2f2c48691dbf68d07e0c27c7288`, revision `f375487d29a06bd4cb0ad204cc19dbcf6e7dfb6d`.
- Config SHA-256 before and after: `d4a6f7ebe8004fcee3151c49e3533afdbde85a02d1c516e3eddc1e8ebf1337f8`. One enabled Codex credential; no Home/plugins; no shared setting change or CPA restart.
- Background account/ingress consumers were not isolated. HLLM selected/prepared target and effort are validated; normalized responses do not separately expose the native backend response model/effort.

## 3. Ramp and backlog evidence

| Phase | Slot ceiling | Configured starts/s | Admitted | Validated | Peak CPA active | Wall s |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| ramp-1 | 1 | 0.25 | 24 | 24 | 1 | 94.395 |
| ramp-2 | 2 | 0.5 | 24 | 24 | 2 | 48.586 |
| ramp-4 | 4 | 1 | 24 | 24 | 3 | 24.923 |
| ramp-8 | 8 | 2 | 24 | 24 | 5 | 13.262 |
| ramp-16 | 16 | 4 | 24 | 24 | 8 | 11.449 |

Measured mean start rates over each point's admission span were ramp-1: 0.248 starts/s; ramp-2: 0.491 starts/s; ramp-4: 0.970 starts/s; ramp-8: 1.909 starts/s; ramp-16: 3.433 starts/s. These include pacing and client-side slot availability, and differ from end-to-end completion throughput.

The drain accepted 1000 logical items at once, initially CPA eight slots/two starts per second, with API overflow eligible after 30 seconds of queue wait. API admission remained at four slots/one start per second. CPA pauses and reductions occurred after uncertain transport failures.

| Drain route | Attempts | Validated | Unconfirmed | HTTP p50 / p95 ms (successful) | Queue p50 / p95 ms (successful) |
| --- | ---: | ---: | ---: | --- | --- |
| cpa | 283 | 279 | 4 | 1342 / 3239 | 137501 / 291327 |
| openai | 717 | 717 | 0 | 1122 / 1593 | 397451 / 731100 |

There were 1124 HLLM attempts across all phases: 406 CPA (402 validated, four unconfirmed) and 718 API (all validated). CPA internal/native attempt counts remain unknown. No p99 is published because neither route group has 1000 successful observations. Durations are driver-to-loopback HTTP wall time; HLLM attempt durations additionally bracket provider execution. Local response-header time is not upstream TTFT. Raw stream diagnostics report `not_streaming`.

## 4. Unconfirmed outcomes and driver correction

| Logical item | HLLM error | HTTP ms | Dispatch observed |
| --- | --- | ---: | --- |
| 29 | `ETIMEDOUT` | 30002 | yes |
| 197 | `ETIMEDOUT` | 30002 | yes |
| 549 | `NETWORK_ERROR` | 10 | yes |
| 545 | `NETWORK_ERROR` | 1945 | yes |

Items 29 and 197 hit the 30-second header wait boundary. CPA request summaries contain two temporally matching 499 client cancellations. Items 545 and 549 encountered network errors at the same completion instant, after approximately 1945 ms and 10 ms respectively. Their lower-level cause remains unidentified. All four had observed header dispatch to the public CPA route, so absence of a known result cannot authorize replay. No 429 or Retry-After reached this driver; live throttle-hint behavior was not exercised.

The baseline breaker then paused CPA too conservatively: both network failures were admitted under the earlier two-slot limit, but the second failure was evaluated after the first reduced the current limit to one. This was not a measured failure of a newly admitted one-slot baseline. The fixed scheduler records each item's admitting concurrency and tests this exact concurrent-failure partition. The original campaign binary and raw evidence remain unchanged; no replacement calls or second campaign hide the missing results.

## 5. Parameters, reuse and remaining gates

CPA retries/cooling are useful, but do not implement durable backlog execution. Baseline request-retry=3, max-retry-credentials=1 and max-retry-interval=30 s stayed fixed. Source-derived quota backoff starts at one second and doubles up to 30 minutes unless provider reset hints dictate a different deadline; this is not measured in this campaign. Native retry amplification and waiting can consume the caller budget.

Investigate the 30-second HLLM header wait separately from the 60-second overall budget. CPA non-streaming keepalives may prevent a header timeout by flushing whitespace, but can commit the status before a later error. Qualify post-flush status/Retry-After/JSON behavior before changing shared settings. SSE keepalive settings apply to another path. A larger wait is not a capacity increase.

Reuse Query Set's accepted-run status URL, FIOr durable continuation and Cloud Tasks/Firestore for production admission. Keep shared provider limits, cooldown and spend durable across function instances; only undispatched items may change route. Successful outputs remain durable while unknown results remain explicitly unresolved. No production queue/status integration or consumer migration was deployed by this experiment.

- [CPA settings/admission owner #6](https://github.com/prls-co/CLIProxyAPI-setup/issues/6)
- [Query Set durable admission/status owner #69](https://github.com/prls-co/query-set/issues/69)
- [FIOr existing continuation #8](https://github.com/prls-co/firebase-orchestration/issues/8)
- [HLLM #96](https://github.com/prls-co/harden-llm/issues/96) remains open: its per-request managed-profile fallback differs from this driver's undispatched-item overflow.

Account/integration eligibility remains separate from throughput. No pacing threshold guarantees protection from warnings or suspension; do not bypass a restriction by rotating identity or clearing valid cooldowns. See the official and community source review in [the campaign guide](../../../../../docs/cpa-capacity-campaign.md).

## 6. Verification and retained evidence

All 10 local fast tasks, targeted Go race checks, go vet and whitespace checks passed. Hosted fast and CodeQL passed on the corrected driver commit. The explicit live task failed its completion oracle without timing out; cleanup reported no errors. No browser, Docker integration, release certification or production deployment ran.

Initial setup/gate failures are retained in validation.json: bundled OpenAPI synchronization, missing locked frontend dependencies, and an unexplained existing Node HTTP fixture failure that passed in isolation. The final source revision passed the complete gate; the original fixture failure's cause was not established.

`summary.json` contains exact bounded aggregates and all four non-completed measurements; `cpa-before.json`, `cpa-after.json`, `cpa-log-observation.json` and `validation.json` record readback and checks. The private local `run-545a495/` directory retains all validated synthetic nonces, heartbeats and atomic final item state. Its raw event log is not committed.

- Raw events: 2122132 bytes, SHA-256 `ee2e94eb104618f76896d67dabc29fcfec424c4d44960d2c469e640a7a3ebf00`.
- Heartbeats: 189; none extended the hard deadline.

Final review also added a deterministic billing-quota guard: actionable API billing errors stop the campaign instead of being treated as transient 429s. No billing failure occurred during this live run.

The experiment driver has been implemented and corrected, but the 1000-result live acceptance gate remains incomplete (996/1000). Production throughput and the corrected breaker still need a separate owner-approved live qualification.
