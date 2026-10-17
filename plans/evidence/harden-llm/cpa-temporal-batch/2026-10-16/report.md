# Self-hosted CPA batch qualification

## 1. Implementation and recommendation

The gateway now accepts up to 1,000 independent CPA `gpt-6-luna`, reasoning
`low` generations per authenticated durable job. PostgreSQL stores immutable
scoped manifests and outcomes, Garage stores result artifacts, and self-hosted
Temporal owns shared admission, cooldown timers, restart recovery and deadlines.
The root execution library remains stateless. No managed cloud scheduler or
normal-call provider fallback is introduced.

The implemented default is 128 active calls, a 200-start admission budget per
61 seconds, a 200-start burst replenished at each new window, and a launch-rate
ceiling that starts at 100/second and grows to 200/second. Explicit refusals
return through one shared cooldown/recovery probe; account restrictions pause
admission until operator resume. Ordinary failures remain per-item errors.

This is a configurable caller policy based on measurements. It is not a claim
that the provider publishes a universal 200 RPM or 200-concurrent entitlement.
The earlier cold-burst boundary and refill tests did not identify such a
universal algorithm. Other traffic using the same account remains external to
the dispatcher and can consume capacity.

The [machine-readable qualification](qualification.json) retains all sampled
policies, source hashes, outcomes, counters and timing distributions, including
failed qualifications. Raw output and CPA log content are not included.

## 2. Measured short-output runs

The matched comparison uses source `426dc903d57c874c06699c67e665edfd2f8e06f4`.
Every main item has a distinct nonce and produces a distinct upstream request;
there is no grouping of multiple tasks into one provider generation. Response
caching is off. Success requires complete core validation, 1,000 downloaded
records, distinct IDs, and independent nonce/output checks. Each sample has two
separate pilot requests and a 90-second quiet interval before measurement.
Background account traffic and host load were not controlled.

| Strategy | Fully validated results | Caller attempts | Result-ready seconds | Qualification |
| --- | ---: | ---: | ---: | --- |
| Fixed cadence 1 | 1,000 | 1,000 | 257.582 | passed |
| Fixed cadence 2 | 1,000 | 1,000 | 286.592 | passed |
| Fixed cadence 3 | 948 | 1,000 | 254.879 | failed: 52 network errors |
| One job, windowed 1 | 1,000 | 1,000 | 256.557 | passed |
| One job, windowed 2 | 1,000 | 1,000 | 254.287 | passed |
| One job, windowed 3 | 1,000 | 1,000 | 367.589 | passed |

All three single-job samples qualified: median 256.557 seconds, range
254.287–367.589 seconds. The fixed-cadence comparator releases five 200-item jobs
at 0, 61, 122, 183 and 244 seconds; its burst allowance keeps those starts
eligible without a second timer-based pacer. Only two of its three samples
qualified. Its successful-sample median is 272.087 seconds; excluding the failed
sample is not evidence of a reliable throughput advantage for either strategy.
The sample size and uncontrolled conditions do not establish statistical
superiority. One 1,000-item job preserves the useful fixed-cadence throughput
while moving scheduling and waiting into durable infrastructure.

The third single-job sample contained two approximately 122-second generations
reporting only 42 output tokens each. Headers arrived in under one second;
output was delayed for about two minutes. The final result, rather than bulk
launch time or HTTP 200, determined the batch makespan. The worker/client
continued reporting heartbeat/status while the last item was active.

Result-ready timing is the final stored job-progress timestamp relative to the
first submission. Completion observation and verified-download times are
retained separately in each summary. `headers_ms` measures request start to
first response byte. The existing `first_output_ms` is measured from starting
SSE collection, so it has a different origin; it is not directly comparable to
`headers_ms` as an end-to-end timestamp. Generation duration spans core execution
and response collection. CPA native attempt totals remain unknown; correlated
native 429 log lines and caller attempts are separate measurements. Pilot and
measured-main log counters are partitioned. No native 429 log line correlated
with any main request in the six matched short samples; pilot-only refusals
remain counted separately. Native retry totals cannot be inferred from this.

After merging current `main` (`190c002`), a fresh acceptance run on clean
`fb6e4b083db1ff7839cbe836dd7cb5115e256545` also qualified 1,000 distinct
results in 308.439 seconds, with 1,000 caller attempts and no correlated main
native 429. Its source differs from the three matched samples, so it is not
included in their median. Verified download finished at 312.687 seconds.

## 3. Retained failures and exploratory runs

The third fixed-cadence sample had 51 nearly simultaneous `NETWORK_ERROR`
outcomes in its first wave and one additional network error. Of the failed
items inspected during that first interruption, 45 had bounded CPA correlation
IDs and CPA access-log HTTP 200 entries. The origin image/configuration did not
change, its container had not restarted, both health endpoints returned 200,
and no tunnel error line appeared in the inspected interval. The response or
transport interruption's root cause is unconfirmed. HTTP 200 did not establish
completed inference. All 52 items remain errors and were not replayed. See the
[bounded transport investigation](baseline-191423/transport-anomaly.json). The
campaign paused after that failed sample; only the two remaining distinct
planned samples were subsequently resumed.

Earlier exploratory code/policies are retained separately from the matched
comparison:

| Policy | Valid results | Caller attempts | Ready seconds | Observed main native 429 log lines |
| --- | ---: | ---: | ---: | ---: |
| Rolling 128 slots, start 50/s | 1,000 | 1,141 | 356.480 | 180 |
| Early producer-paced baseline | 1,000 | 1,000 | 328.443 | 0 |
| Windowed 128 before burst renewal | 1,000 | 1,001 | 312.132 | 5 |
| Windowed 200 before burst renewal | 999 | 1,001 | 307.678 | 15 |

The 200-slot run reached 192 overlapping executor calls and retained one network
failure. It did not qualify as a successful 1,000-result batch. Overlap is core
executor overlap, not a count of TCP connections. The 128-slot early windowed
run reached 119 overlapping calls. Raising a configured ceiling does not prove
a higher upstream allowance or faster complete results.

Preflight failures for an output path, unsupported schema keyword, and a busy
Docker lock made no provider calls. Those failures remain recorded. The enum
schema regression and pilot guard were added before subsequent main loads.

## 4. Output length and mixed workload

The separate [66-call ABBA output-length study](../../cpa-output-token-study/2026-10-16/report.md)
used equal 81-token inputs. All 66 returned HTTP 200; 65 passed exact content
qualification and one failed the output-length oracle. Short outputs were about
34 completion tokens and 2.3 seconds; long outputs about 1,165–1,187 tokens and
9.5–9.8 seconds. Total generated tokens were 40,558, including reasoning rather
than adding reasoning twice. No correlated native 429 was observed. This small
load did not establish a TPM ceiling or prove that no token quota exists.

The original mixed-array pilot subsequently failed: one short item succeeded,
while the 512-element array item began output shortly after headers and failed
to complete before the 180-second generation deadline. The 1,000-item mixed-array
load was not submitted. The timed-out request was not replayed. HLLM's supported
schema subset cannot enforce array length with `minItems`/`maxItems`; its exact
array oracle remains retained.

A distinct mixed bounded-text experiment uses a supported scalar enum to
require 512 words exactly equal to `x` for 10% of items. It preserves independent
nonce/word-count/value checks and does not replace the failed array experiment.
Its pilots qualified: 39 output tokens in 1.711 seconds and 554 in 3.612 seconds.
The main batch qualified all 1,000 distinct results in 261.019 seconds with
1,000 caller attempts. It reported 157,500 input tokens and 113,417 completion
tokens, including 19,797 reasoning tokens; none were added twice. This mixed
case changes input/schema as well as output length and does not isolate TPM.
The clean benchmark checkpoint is `0a10c9a7bcf9365c9ec80b10a6da83c24e9dc45a`;
no main native 429 log line correlated with this batch.

The pinned CPA Codex translator strips `max_output_tokens`; requested caps must
not be treated as an enforced output-token budget. See the output study for the
pinned source location and executable/source identities.

## 5. Verification and ownership

The final merged-source release gate passed all 30 browser-free tasks on clean
`fb6e4b083db1ff7839cbe836dd7cb5115e256545`, with no cleanup errors or warnings:
`runner-1792181982318-3316859-b38d3a19b4f897f1.json`. It used the canonical
release selector with two candidate slots and four Go/Erlang schedulers.
[Bounded final verification](release-merged-verification.json) and
[test component image identities](release-smoke-merged-images.json) retain
source and artifact boundaries. These images belong to the local test stack.

The earlier pre-merge release also passed all 30 tasks:
`runner-1792176419849-2185243-d0aa39b50c3bb5c0.json`. This includes full Go race,
real database/storage lifecycle, real Temporal execution/restart/replay,
OpenAPI/parity, deterministic Phoenix/client checks, security audits, packaged
assets/releases and synthetic-provider Compose smoke checks. No browser ran.
That earlier release used the canonical selector with `--candidate-slots 1`
after parallel cold-process startup timeouts; the assertion oracles and selected tasks were
unchanged. The private-module credential was supplied only through the existing
build-secret environment.

The original production gateway/worker checkpoint is `21e0990`. `426dc90`
adjusts only the live comparator; `0a10c9a` adds the distinct bounded-text
benchmark helper. The final combined checkpoint `fb6e4b0` merges current main,
retaining its nullable schema and original response-text contracts and moving
the reference HTTP-client test ID to `TEST-482` to avoid main's `TEST-472`.
Per-sample source/executable hashes and clean-source flags are retained.
The release audit also required Bandit 1.13.1 and Thousand Island 1.6.0, resolving
[GHSA-69m6-wcv4-rwcf](https://github.com/mtrudel/bandit/security/advisories/GHSA-69m6-wcv4-rwcf)
and [GHSA-v53v-9hvg-37jf](https://github.com/mtrudel/bandit/security/advisories/GHSA-v53v-9hvg-37jf).
Earlier failed/canceled gate reports remain available locally and are not counted
as passed certification.

CPA-owned retry controls are in
[CLIProxyAPI-setup PR #7](https://github.com/prls-co/CLIProxyAPI-setup/pull/7),
tracked by owner issue #6. The isolated pinned-image synthetic comparison showed
retry 0 returning the first 503 in 42 ms, retaining cooling/Retry-After, while
retry 3 hid two upstream attempts inside one 3.474-second client request. That
qualifies synthetic retry semantics, not live Codex capacity or every consumer.
The shared CPA runtime still uses retry 3, max retry credentials 1 and max retry
interval 30. Its image remains
`sha256:99bedd436cf04530451aeff67b88d3e76dfff2f2c48691dbf68d07e0c27c7288`;
configuration hash
`d4a6f7ebe8004fcee3151c49e3533afdbde85a02d1c516e3eddc1e8ebf1337f8`.
No account state was copied and no shared CPA setting was changed.

## 6. Rollout boundary

Implementation, local qualification and review branches are complete; this
report is not deployment acceptance. No HLLM dev/production rollout or main
promotion occurred. The environment still needs its explicitly provisioned HLLM
Temporal namespace/private network and HLLM-scoped Garage access. Enable API and
worker together only after additive schema application and image/SHA review.
The opt-in overlay reuses existing PostgreSQL, Garage, telemetry and Temporal.
Keep accepted manifests, outcomes and histories during rollback.

Use [the batch runbook](../../../../../docs/cpa-batch-jobs.md) and canonical
OpenAPI. Consumers must adopt `/v1/jobs`, preserve idempotency keys and poll
status/result URLs with a hard overall deadline. The waiter reports progress
without resubmitting inference. Provider/account restrictions pause admission;
they are not bypassed through identity rotation or another provider. A completed
batch can contain item errors, and the retained network/long-output failures
prevent any guarantee that every arbitrary 1,000-call workload succeeds within
six minutes.
