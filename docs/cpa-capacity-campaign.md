# CPA capacity campaign

## 1. Scope and authorization

`cmd/harden-llm-loadtest` is an explicit live experiment, not a production job
queue or a new gateway job API. It assembles the existing stateful service and
HTTP handlers on a private loopback listener, without profiles, PostgreSQL,
history or cache. Shared CPA configuration is unchanged. The existing local-only
synthetic capacity harness retains its endpoint restrictions.

Paid requests explicitly select `service_tier: default` so project-level auto
settings cannot opt the reservation calculation into a higher-priced tier.

The fixed workload uses `gpt-6-luna`, native reasoning `low`, a unique nonce JSON
schema, no tools/search, and `max_output_tokens: 512`. Each HLLM request has one
attempt, no repair/rerun and a 60-second deadline. CPA can still retry internally;
its internal attempt count is unknown. The CPA Codex translator may remove the
output token limit; the official API request retains it. This synthetic workload
does not qualify the application's larger prompts or output distributions.

## 2. Running and observing

After explicitly authorizing paid inference, supply `CPA_API_KEY` and the separate
`OPENAI_API_KEY` in the process environment using the owner's protected credential
source. Never place keys in flags, shell history, committed files or diagnostics.
Set `HARDEN_LLM_CAPACITY_OUTPUT` to a new directory beneath an existing private
parent. The manifest task runs only when explicitly selected:

```sh
node scripts/run-test-tier.mjs --task live-cpa-capacity
```

For a shorter, lower-budget explicitly authorized experiment:

```sh
go run ./cmd/harden-llm-loadtest --live --output /private/new-campaign --duration 30m --api-budget 1
```

The hard inference budget is at most two hours; the paid API reservation budget
is at most $5, including qualifications. Heartbeats every five seconds indicate
liveness and never extend the deadline. `events.ndjson` contains phase changes,
terminal attempt measurements and heartbeats; `status.json` is an atomic latest
checkpoint. Both survive process completion. A heartbeat is not a completion.
The artifact directory and files are private. No payloads, keys or account labels
are logged. Reports carry execution/trace IDs, native effort, usage, HLLM attempt
diagnostics, waiting reason, eligibility timestamps, admission settings and spend.

The test runner has a separate fixed 122-minute outer timeout, allowing cleanup
and artifact writing after inference stops at two hours. Watching the NDJSON or
checkpoint does not require keeping a caller HTTP request open. These are local
artifacts, not a durable public status URL; production jobs must use their owner's
existing authenticated status/result routes.

## 3. Admission policy

Qualify three sequential CPA calls and one direct API call. Then try at most 24
calls at each `(concurrency, starts/second)` point: `(1,.25)`, `(2,.5)`, `(4,1)`,
`(8,2)`, `(16,4)`. Stop escalation on the first failed point, drain in-flight work
and preserve its cooldown before the drain. Passing all points means only that
no limit was observed through this load.

The final phase accepts 1000 logical items into its backlog at once. CPA starts
at half the last clean concurrency and start rate, with a minimum concurrency of
one. A known throttle/local credential-unavailable rejection pauses all CPA
admission until its Retry-After plus bounded jitter and reduces concurrency and
start rate. Without a hint, use bounded exponential backoff. A failure at one
active slot pauses the route for operator investigation. The admitting concurrency
is retained per item: multiple old in-flight failures cannot be mistaken for a
failed single-slot baseline after an earlier failure reduces the limit. Known rejections may
retry up to three attempts on the same route. Uncertain responses are retained
as unconfirmed and never replayed. Uncertain failures also slow the affected route.

Only undispatched items may overflow to the official API when queue age reaches
30 seconds or CPA advertises a cooldown longer than 30 seconds. API admission
has four slots and one start/second, its own cooldown and conservative reservation
before every attempt. Outstanding unknown reservations remain charged against
the budget. Pricing verified on 2026-10-15: standard global GPT-6 Luna input
$0.10/M, cache writes $0.125/M and output $0.50/M. Reserve 8192 input tokens at
the higher cache-write rate plus all 512 output tokens for a fixed request under
2048 bytes. Settled estimates also ignore cache discounts. They are conservative
usage estimates, not a billing reconciliation. Refresh code/prices before a later
campaign if the provider changes pricing or token semantics.

At deadline or cancellation, stop new dispatch, drain/cancel owned requests and
checkpoint queued, completed, failed and unconfirmed items. A 1000-call success
requires 1000 validated results; no heartbeat, HTTP 200 alone or partial drain can
pass. Checkpoints preserve evidence but are not automatically resumed: replaying
an interrupted request can duplicate inference and charges.

## 4. Timing interpretation

`localGatewayHTTPMs` and `localGatewayHeadersMs` measure the driver-to-loopback
HTTP request, which includes HLLM and upstream time. HLLM attempt `duration` is in
nanoseconds and brackets the provider operation. Queue and retry waits are
separate milliseconds. Stream first-event/output fields, when present, begin
after upstream headers; they are not whole-request upstream TTFT. Ordinary `/v1`
responses do not currently forward client stream selection upstream, so these
fields can be absent. Unknown values must remain absent/null. Report p50/p95 by
route; publish p99 only with at least 1000 successful observations in that group.

## 5. Production reuse and account restrictions

CPA's existing retries/cooldowns and optional Home credential concurrency or
request-lifecycle plugin admission are useful limits, but none provides a durable
1000-item inference queue. Avoid increasing retry waits past HLLM's 60-second
request budget. Waiting belongs between requests in the application job owner.

For the Firebase pipeline, reuse Query Set's accepted-run/status contract and
FIOr's external-job continuation work, with Cloud Tasks concurrency/start-rate
controls. A single shared admission authority must cover all function instances;
a process-local semaphore cannot bound aggregate load. Keep credential/model
scope, logical item IDs, lease/attempt state, next-eligible times and route spend
reservations durable. Poll the existing authenticated job status URL; publish the
result only after the accepted contract is complete. Progress should distinguish
waiting, active, completed, failed and unconfirmed counts and the next retry time.
This integration requires owner-repository changes and remains a handoff.

Treat account/security/policy rejections as a campaign stop. Do not rotate
identities, IPs or tokens to bypass a restriction. There is no published safe
requests/second threshold guaranteeing immunity from warnings or suspension.
Confirm the account and hosted-service integration is permitted before production
rollout. Official API use is separately authenticated and budgeted; it does not
make prohibited subscription automation eligible.

## 6. Sources

- [Pinned CPA cooldown implementation](https://github.com/router-for-me/CLIProxyAPI/blob/v7.3.8/sdk/cliproxy/auth/conductor_cooldown.go)
- [CPA request lifecycle concurrency example](https://github.com/router-for-me/CLIProxyAPI/blob/v7.3.8/examples/plugin/request-lifecycle/README.md)
- [CPA Home](https://github.com/router-for-me/CLIProxyAPIHome/blob/main/docs/README.md)
- [Official model contract](https://developers.openai.com/api/docs/models/gpt-6-luna)
- [Official pricing announcement](https://developers.openai.com/api/docs/changelog#september-2026)
- [Firebase task functions](https://firebase.google.com/docs/functions/task-functions)
- [Cloud Tasks retry and ramp behavior](https://docs.cloud.google.com/tasks/docs/common-pitfalls)
- [OpenAI account deactivation guidance](https://help.openai.com/en/articles/10562188-why-was-my-openai-account-deactivated)
- [OpenAI terms](https://openai.com/policies/terms-of-use/)

## 7. CPA operator parameters and owner boundaries

Baseline readback on 2026-10-15: pinned `v7.3.8-prls.3`, one enabled Codex
credential, Home disabled and no lifecycle plugin. No configuration tuning is
part of this baseline. Defaults below are source-derived when the YAML omits a
key; they are not measured upstream quotas.

| Setting | Baseline | Effect and proposed use |
| --- | --- | --- |
| `request-retry` | 3 | CPA can multiply an HLLM attempt. Keep it fixed during the measurement; qualify a smaller value separately if retry amplification is material. It does not drain a durable backlog. |
| `max-retry-credentials` | 1 | With one Codex credential there is no alternate credential capacity. Increasing this does not create entitlement. |
| `max-retry-interval` | 30 s | Bounds in-request waiting between retry rounds, not the lifetime of stored upstream cooldowns. Do not raise it past HLLM's request budget as a substitute for delayed jobs. |
| `transient-error-cooldown-seconds` | omitted; source default 60 s | Temporary credential exclusion on transient errors. Respect the hint in application admission; do not disable cooling to keep sending calls. |
| `disable-cooling` | omitted; false | Preserve provider quota protection. |
| `routing.strategy` | round-robin | Selects among eligible credentials. One credential gives no parallel capacity gain from changing strategy. |
| `routing.session-affinity` | false | Session routing is not a work queue or quota increase. Leave fixed for independent synthetic calls. |
| `passthrough-headers` | false | Avoid enabling broad header passthrough solely for diagnostics; the deployed CPA patch and HLLM error path preserve the relevant Retry-After hint. |
| `save-cooldown-status` | omitted; false | Investigate documented persistence and restart behavior with the owner. Do not reset an upstream restriction to evade it. |
| Home `max_in_flight`, `max_in_flight_by_model` | Home disabled | Optional credential-scoped admission authority with shared counters. Useful for multiple CPA instances; requires owner deployment and rejection/release qualification. It still needs a durable producer backlog. |
| Lifecycle plugin `max_concurrency` example | not enabled | Admission guard rejects with 429/Retry-After; it is not delayed durable execution. Qualify installation and counter release before enabling. |

The CPA setup owner must own shared configuration, image changes and native
credential counters. Query Set owns pipeline work admission and accepted status
semantics; FIOr owns durable continuation/notification primitives. HLLM owns the
single-call contract and diagnostics. This experiment does not close HLLM #96:
that issue requests a separate per-request managed-profile fallback, whereas
this driver moves only undispatched backlog items between explicit routes.

## 8. Measured timeout boundary and follow-up qualification

The 2026-10-15 baseline at source `545a495` exposed a separate 30-second
response-header deadline in `internal/providers/endpoint.go`, before the 60-second
overall execution deadline. Its non-streaming CPA calls produced four written-
request timeouts during the drain. Treat these as unconfirmed outcomes; a matching
CPA 499 is a client cancellation, not proof of an upstream concurrency rejection.

`stateful.ClientOptions.EndpointPolicy.ResponseHeaderTimeout` can explicitly
select the transport header budget for an embedding application. It is not a CPA
setting and is not currently a gateway environment knob. Changing it requires a
separate qualification within the overall request budget. The baseline does not
establish that a larger header budget resolves the native cause.

CPA also has `nonstream-keepalive-interval` (omitted/disabled in this deployment)
and SSE `streaming.keepalive-seconds` (a separate path). Non-streaming keepalives
write/flush blank lines while waiting. They may avoid a header wait timeout, but
can commit HTTP status before a later provider error. Qualify post-flush errors,
Retry-After propagation, JSON parsing and the unchanged overall deadline before
enabling them. No shared keepalive or retry configuration was changed here.
See [CPA owner handoff #6](https://github.com/prls-co/CLIProxyAPI-setup/issues/6).

The baseline also found a driver breaker defect: multiple old requests could
fail after a concurrency reduction and falsely look like a failed one-slot
baseline. The corrected driver records the admitting concurrency on each item.
A deterministic regression reproduces eight old failures without inventing a
one-slot failure; a genuinely admitted one-slot rejection still pauses the route.
The original campaign remains on `545a495`; it is not a live qualification of this
correction. A local real TLS response-header timeout regression also proves that
a written request stays unconfirmed, shared admission is reduced, and only an
undispatched sibling can overflow.

## 9. Official usage guidance and community signals

CPA's Codex executor directly calls the ChatGPT Codex backend. It does not spawn
Codex CLI, so CLI thread limits are not CPA admission settings. Official usage
varies by plan, model, task complexity, reasoning and shared allowance windows;
it is not a fixed request count. Current guidance gives GPT-6 Luna estimates of
350–3000 local messages per five-hour window on Plus/Standard Business, expressly
without guaranteeing those counts. These are neither requests/second nor
concurrency guarantees, and the deployed credential's entitlement is not inferred
from them. [Official usage guidance](https://help.openai.com/en/articles/20001516-managing-usage-with-gpt-6-astra-in-work-and-codex)

Firsthand reports describe a third concurrent desktop chat receiving 429, CLI
429s with three active sessions and 69% allowance remaining, and account-wide
429s with four to five child agents on a different model. These reports do not
establish a universal two-, three- or five-call cap, and session/agent counts are
not backend in-flight request counts. Our short Luna ramp passing eight measured
active requests does not invalidate those different workloads or guarantee future
capacity. [Desktop report](https://github.com/openai/codex/issues/54017),
[CLI report](https://github.com/openai/codex/issues/54010),
[child-agent report](https://github.com/openai/codex/issues/41702).

Account eligibility is a separate gate from pacing. OpenAI documents supported
plan usage in participating apps through Sign in with ChatGPT, and distinguishes
that from app-server authentication for commercial/hosted services. A working
CPA response does not establish eligibility for a hosted product. Confirm the
supported integration rather than treating a lower request rate as a permission
or a guarantee against warnings.
[Supported app plan usage](https://help.openai.com/en/articles/20001542-using-your-chatgpt-plan-in-other-apps-and-sites),
[app-server authentication guidance](https://learn.chatgpt.com/docs/app-server#auth-endpoints).
