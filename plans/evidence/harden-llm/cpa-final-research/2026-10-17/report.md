# CPA final throughput research

## 1. Decision

Research is complete for choosing the architecture and an initial operating
policy. Keep one self-hosted Temporal admission controller for participating
clients sharing the CPA Codex credential pool. Keep 128 active requests and the
configurable 200-start/61-second estimate, continuous durable submission,
explicit-refusal cooldown, one recovery probe and paced recovery. This is a
qualified operating point, not the provider's published entitlement, exact
concurrency ceiling, globally optimal setting or suspension-safe rate.

The new evidence does not justify reducing that request budget for the tested
longer-input/output profiles. It does justify treating long generation deadlines
and response tails separately from rate limiting. No more provider-token budget
is needed for this decision. The next useful work is engineering and targeted
acceptance, rather than another broad search for undocumented limits.

The [previous qualification](../../cpa-strategy-qualification/2026-10-16/report.md)
remains the evidence for 1,000-result native drains, competing-client comparisons,
and actual Temporal/provider execution of 3,000 results. This pass adds native
input/output measurements and cheap production-workflow characterization. It
does not repeat or replace that real Temporal qualification.

## 2. Frozen experiment and actual outcomes

The [protocol](../../../../cpa-final-research-protocol.md) freezes the baseline
and stopping rules. All ten live cases used clean source
`b38048e336d84f4d29920063d407dbbf7987c4fd`, including the research-only context
extension; the production admission controller and workflow were unchanged.
Each case records its binary SHA-256. The shared CPA image, configuration hash,
start time and restart count stayed unchanged; compare
[before](environment-before.json) and [after](environment-after.json).

Every request targeted CPA `gpt-6-luna`, reasoning `low`. Four two-call pilots
qualified the workload profiles. Six 256-request main samples followed, each
crossing the estimated 200-start budget. Short versus longer input used ABBA
order, then output-load and combined-load samples. Each main sample had a
recorded 90-second quiet interval. This is not proof of an account-wide reset
or absence of external traffic. All main samples reached 128 caller requests
in flight. These are native streaming experiments, not Temporal throughput.

| Main sample | Median reported input tokens | Median reported output tokens | Independently valid | Observed terminal seconds | Result |
| --- | ---: | ---: | ---: | ---: | --- |
| [A: short, first](a-short-1/derived.json) | 69 | 39 | 256/256 | 63.356 | passed |
| [B: longer input, first](b-input-1/derived.json) | 5,728 | 39 | 255/256 | 180.025 | failed: one unconfirmed deadline |
| [B: longer input, second](b-input-2/derived.json) | 5,725 | 39 | 256/256 | 74.324 | passed |
| [A: short, second](a-short-2/derived.json) | 70 | 40 | 256/256 | 63.323 | passed |
| [C: output load](c-output/derived.json) | 617 | 550 | 256/256 | 70.590 | passed |
| [D: combined load](d-combined/derived.json) | 6,279.5 | 732 | 256/256 | 71.011 | passed |

A failed sample's terminal time is not a complete-results time and cannot win a
throughput comparison. Output-load samples require exactly 512 words equal to
`x` in a JSON payload and exact nonce equality. Their schema includes that
payload, adding roughly 550 input tokens too. They are workload comparisons,
not a perfectly isolated perturbation of output tokens. Reported output tokens
include reasoning; do not add reasoning tokens again.

The [matrix](matrix.json) and [verification](verification.json) retain 1,544
caller attempts: 1,536 main requests and eight pilots. There were 1,543 globally
distinct validated results and one unconfirmed result. Every item had one
caller attempt: there were no explicit refusals and no inference replay. No
account-policy errors were observed. Native attempts inside CPA remain unknown.

All 1,544 bounded CPA correlation suffixes match an HTTP 200 access entry within
the corresponding measured interval. No native 429/5xx lines were matched by the
allowlisted status patterns. This is bounded log coverage, not proof of zero
hidden retries. CPA logs use `Asia/Shanghai` (+08:00), verified from the container;
measurement timestamps carry their own offsets. Per-case `log-coverage.json`
files normalize those clocks. Raw logs, prompts, outputs, trace prefixes and
credentials are excluded.

The longer-input first sample reported no cached input. The second reported
5,504 cached tokens, about 0.38% of its 1,465,092 input tokens. Other main samples
reported zero cached input. This is predominantly uncached input evidence;
do not describe every request as uncached or infer why that one cache hit occurred.

## 3. What token length and timing establish

The tested longer-input burst offered roughly 1.14 million input tokens across
its first 200 calls. No explicit quota refusal appeared in either longer-input
sample or the combined profile. This does not establish a numerical TPM limit:
dispatch-attributed input is not an upstream accounting clock, and CPA does not
expose every internal attempt. The current request-count guard remains a
reasonable initial policy for these synthetic profiles. No new numerical
token-rate limiter is justified by this experiment.

Longer output increased median generation time: approximately 1.34–1.39 seconds
for short samples, 5.01 seconds for the output-load profile and 6.07 seconds for
combined load. Longer-input medians were 2.45 and 1.74 seconds. These descriptive
samples are not a stationary, independent statistical performance model.

The failed longer-input call received headers after 2,165 ms, first output at
179,797 ms, and reached its client deadline at 180,000 ms without a validated
terminal or usage. Two other responses in that sample eventually validated
after roughly 114 and 124 seconds. Its 255 valid results were all available by
123.571 seconds, but the 256-result oracle failed. The independent later
longer-input sample passed; it did not replay the uncertain inference.

This is an end-to-end tail observation. It does not isolate model processing,
network streaming or hidden CPA retry time as the cause. HTTP 200 and a fresh
heartbeat are not proof that a validated result or terminal usage exists.

The batch API currently caps `harden.timeout_ms` at 180,000, and `Generate` caps
provider execution at 180 seconds. Waiting longer in the job poller cannot
extend that generation. For callers willing to wait, make generation budgets
explicitly configurable and qualify a longer supported profile. That change
must update OpenAPI, validation, activity timeout and replay-aware workflow
behavior together. Do not silently increase only a benchmark timeout, replay
the uncertain request, or call an untested longer budget a qualified fix.

CPA's translator still strips `max_output_tokens`; it is not an enforced spend
ceiling on this route. Exact-output validation bounds the visible synthetic
workload, not arbitrary reasoning usage. Representative long-context, tool and
large-output production work still needs its own acceptance profile.

## 4. Jitter: coordination has the larger effect

The production controller has no jitter option. A research-only wrapper tested
0–1 second of deterministic positive recovery padding over two quota models,
shared versus independent authority, and eight fixed seeds. All 64 variants
completed the unchanged 1,200-result oracle. These are deterministic model
variants, not 64 independent provider observations.

| Model and authority | Median refusals without / with jitter | Median foreground seconds without / with jitter |
| --- | ---: | ---: |
| Fixed window, independent | 285 / 282.5 | 297.130 / 298.625 |
| Token refill, independent | 243 / 248 | 307.230 / 308.275 |
| Fixed window, shared | 0 / 0 | 294.000 / 294.000 |
| Token refill, shared | 0 / 0 | 294.000 / 294.000 |

See [all variants](jitter-simulations.json) and [group summaries](jitter-summary.json).
Small jitter is a recovery safeguard, not a demonstrated CPA speed improvement.
Keep it optional and bounded; do not introduce human-like think time or random
delay on every normal dispatch. One shared recovery probe already removes the
main synchronized-retry problem for participating clients. Any future jitter
must preserve `Retry-After` as a floor and be recorded or deterministically
derived for Temporal replay. No live jitter benefit is claimed: the new live
matrix never exercised a refusal/recovery episode.

This is consistent with the general retry guidance in the
[AWS Builders' Library](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/).
The [OpenAI API retry guide](https://developers.openai.com/api/docs/guides/rate-limits)
also recommends respecting retry hints, bounded backoff and accounting for
nested retries. Its API-tier RPM/TPM figures are not CPA subscription limits.

## 5. Temporal rollover and the critical path

The new production-workflow test exercises both history-length and server-suggested
rollover. A 120-second generation overlaps a one-second generation while a third
item waits. Rollover stops admission despite the free slot: queued work waits
119 seconds. The next run retains and executes that queued item once. The
[verification](verification.json) records both observations; no provider tokens
were needed to prove this boundary.

Rollover is a confirmed admission limitation. It is not inference loss and does
not show that current scheduling is uninterrupted across run boundaries. However,
119 seconds of blocked admission is not automatically 119 seconds of recoverable
whole-batch completion time: the slow call can remain on the batch's critical
path even if later calls finish earlier. The prior 3,000-call trace is consistent
with this mechanism, but its exact rollover trigger was not retained; do not
rewrite that historical observation as a newly proven causal trace.

Prioritize rollover changes using cross-job waiting and completion SLOs. First
expose rollover/drain waiting and oldest-active duration in diagnostics. If
independent jobs need admission to continue across rollover, separate durable
generation ownership from the pool permit manager, carry outstanding permit
identities across runs, and fence/idempotently process completions. This is a
new implementation boundary requiring acceptance. Simply continuing as new with
pending activities, clearing active counters, or replaying generation is unsafe.
The [Temporal continuation documentation](https://docs.temporal.io/develop/go/workflows/continue-as-new)
describes the fresh-run/state handoff and supports fast local characterization.

## 6. CPA options, source and community checks

The pinned [streaming executor](https://github.com/router-for-me/CLIProxyAPI/blob/c93978c4ea2e908255a2a06c37599fda3651554a/internal/runtime/executor/codex_executor_stream.go)
uses HTTP for Codex Responses, with a default Codex backend URL. It does not
launch a Codex CLI process per call. CLI-local thread settings are therefore not
this gateway's admission authority. [Source checks](source-audit.json) retain
bounded code locations and content hashes.

[Selected live settings](cpa-selected-settings.json) remain retry rounds 3,
retry credentials 1, maximum internal retry interval 30 seconds and cooling
enabled. The existing owner qualification of retry 0 versus 3 remains useful;
do not change every shared consumer's retry behavior during a capacity experiment.
Retry 0 is a rollout candidate when durable callers own retries, not a quota
increase. No shared setting changed here.

CPA also has [Home concurrency configuration](https://github.com/router-for-me/CLIProxyAPI/blob/c93978c4ea2e908255a2a06c37599fda3651554a/config.example.yaml)
and a [request-lifecycle plugin example](https://github.com/router-for-me/CLIProxyAPI/blob/c93978c4ea2e908255a2a06c37599fda3651554a/examples/plugin/request-lifecycle/README.md)
that rejects excess active requests before upstream execution. These are related
admission mechanisms, not a durable 1,000-item queue with authenticated progress
and result URLs. Home and plugins are not configured/enabled in the inspected
deployment. Do not mistake Home's configuration maximum for an account allowance
or install another coordination service when existing Temporal supplies the
needed durability. A CPA-side guard could later protect direct clients, with
source-owner qualification and an explicit shared-consumer contract.

A post-campaign [pool observation](pool-observation.json) found one Codex auth
record and no configured Codex API-key entries. No credential identities or
raw records were retained. The account pool was not frozen throughout the test,
and other users' traffic remains uncontrolled.

[Official usage guidance](https://learn.chatgpt.com/docs/pricing) distinguishes
workload-dependent allowances and current account-dashboard limits. It does not
publish this deployment's 200/61-second contract or a suspension threshold.
Community reports such as [Codex issue 35463](https://github.com/openai/codex/issues/35463)
describe unexpected allowance depletion under concurrent agent workloads; those
are anecdotal workload/account reports, not independently established CPA quota
formulas. The [CPA recovery-hint issue](https://github.com/router-for-me/CLIProxyAPI/issues/5842)
is the operator's own earlier source-level report, not independent corroboration.
No forum observation establishes a ban-safe rate. Jitter does not change that.

## 7. Spend, research efficiency and remaining ROI

[This pass's ledger](campaign-budget.json) records **5,159,409 reported input plus
output tokens**, including pilots, plus **32,768 reserved** for the uncertain
attempt. Reasoning is already included in output. There were no open attempts
at completion. Hidden native usage remains unobservable; neither reported usage
nor this reservation is a claim of exact subscription billing.

The prior campaign separately retained 3,235,681 reported and 163,840 reserved
tokens. Combined, the two retained ledgers total **8,395,090 reported plus
196,608 reserved = 8,591,698 accounted tokens**. No official API inference,
account rotation, browser, shared CPA mutation or production deployment occurred.
The 10-million-token allowance was a ceiling, not a spending target.

This pass was more focused than the earlier broad search: a frozen native
source, matched input order, explicit failures, cheap rollover/jitter experiments
and a stopping rule tied to architecture decisions. Limitations remain: only
two input samples per level, single output/combined samples, uncontrolled
external traffic, mostly synthetic content, opaque retries and imperfect
output/input isolation. Repeating these same profiles cannot produce certainty
about an undocumented changing service.

| Further work | Expected return | Additional provider-token research now |
| --- | --- | --- |
| Upstream-attempt timing/count and fresh quota-signal diagnostics | High: separates actual backpressure from hidden waits | Instrument first; no broad load campaign |
| Configurable longer generation budgets | High for callers willing to wait; avoids premature uncertain results | Cheap contract/workflow tests, then one bounded new-request acceptance case |
| Rollover visibility and independent execution/permit ownership | Useful for cross-job latency; whole-batch benefit must be measured | Local lifecycle/replay/fencing checks first |
| Actual application prompt/context profile | High once representative sizes and output oracles are supplied | One targeted qualification, cost estimated from actual workload |
| More unrestricted probing or searching for a ban-safe maximum | Low; no stable numeric guarantee follows | Do not spend more tokens on this |

There is no request for a larger budget. Reopen live research only when a new
runtime/account/model or representative workload could change the decision, or
when new diagnostics can distinguish a specific unresolved mechanism.

## 8. Review and operating algorithm

The production-facing choice remains:

1. Durably accept up to 1,000 distinct items per job with an idempotency key;
   larger workloads use multiple jobs sharing one pool authority.
2. Dispatch ready items round-robin across jobs whenever active slots and the
   measured admission budget allow. All participating replicas use the same
   Temporal namespace, task queue and pool ID. Direct CPA and ordinary HLLM
   calls remain outside this authority; adoption must route their work here.
3. Do not wait for an entire earlier group to finish. An unused budget permits
   initial 1,000-call starts around 0/61/122/183/244 seconds, subject to free
   slots. This is an admission schedule, not a five-minute completion promise.
4. On explicit rejection, close admission, honor the recovery floor, and send
   one probe. Halve launch pace once per episode and recover without a fresh
   burst; do not let unrelated late successes reopen the gate.
5. Keep unknown inference terminal, honor its retry hints for future admission,
   and pause on account restrictions. Idempotent submission and downloading do
   not imply idempotent inference. Never use jitter to conceal automation.
6. Publish queued/active/finished/failed counts, heartbeat versus real progress,
   waiting reason and next eligible time, retaining an overall job deadline.
   Round-robin across jobs does not provide weighted fairness across clients.

[Verification](verification.json) records fresh default-tag Go tests, parity and
the final focused race run, including retained-history replay and the new local
experiments. No new live Temporal, Docker integration, browser or full release
gate is claimed. Existing release/real-provider evidence remains source-bound
in the prior report. This branch contains research tooling, tests and evidence;
it does not ship jitter, longer generation budgets or a rollover redesign.
