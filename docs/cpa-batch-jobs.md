# CPA batch jobs

## 1. Execution contract

Explicit batch submission accepts up to 1,000 separate CPA generations. Ten
jobs can represent 10,000 calls and share the same dispatcher. Every item uses
the deployment's CPA Responses connection, `gpt-6-luna`, reasoning `low`, no
response cache and one generation. Function-call output is returned, not
executed. Multi-step recovery and web search are outside this release.

The root Go core executes the request and validates structured output. The
stateful wrapper owns PostgreSQL manifests/outcomes and Garage result objects.
Self-hosted Temporal owns admission, timers and worker restart recovery. The
provider activity has one attempt; explicit quota refusals return to the shared
queue. Other failures become item errors. No prompt/result or credential is
placed in Temporal histories. `/v1/jobs` does not write reference history.

The canonical interface is `api/openapi.yaml`. Storage scope comes from the
existing matched bearer, never the request body. CPA pool identity comes from
deployment configuration. All participating replicas must use the same
namespace, task queue and pool ID. Other direct CPA clients and separate
deployments remain outside this admission authority.

## 2. Self-hosted configuration

Use the existing Agent Platform Temporal service and HLLM database/role. Have
the infrastructure owner provision the environment's HLLM Temporal namespace;
the gateway does not create namespaces. Configure the existing PostgreSQL and
Garage settings, plus these non-secret settings on gateway and worker:

```dotenv
HARDEN_LLM_BATCH_ENABLED=true
HARDEN_LLM_TEMPORAL_ADDRESS=temporal:7233
HARDEN_LLM_TEMPORAL_NAMESPACE=harden-llm-dev
HARDEN_LLM_BATCH_TASK_QUEUE=harden-llm-batch-v1
HARDEN_LLM_BATCH_POOL_ID=shared-cpa-account
HARDEN_LLM_BATCH_UPSTREAM=cpa
HARDEN_LLM_BATCH_CONCURRENCY=128
HARDEN_LLM_BATCH_BURST=200
HARDEN_LLM_BATCH_START_RATE=100
HARDEN_LLM_BATCH_MAX_START_RATE=200
HARDEN_LLM_BATCH_REFILL_ESTIMATE_MS=61000
HARDEN_LLM_BATCH_WINDOW_REQUESTS=200
HARDEN_LLM_BATCH_WINDOW_MS=61000
HARDEN_LLM_BATCH_JOB_TIMEOUT_SECONDS=7200
```

`HARDEN_LLM_BATCH_UPSTREAM` selects an existing `provider: cpa`,
`protocol: responses` connection. Existing connection metadata names protected
credential environment values. Keep Temporal on the private service network;
it is not an internet-facing endpoint. No Firebase or Google Cloud service is
required. The feature is disabled unless explicitly enabled.

Run the same backend image with these commands:

```sh
harden-llm-gateway schema
harden-llm-gateway serve
harden-llm-gateway worker
```

`schema` preserves existing HLLM records and adds versioned batch tables.
Serving does not apply DDL. `serve` exposes HTTP; `worker` processes Temporal
tasks. Workers use the same HLLM pool configuration and Garage credential path.

For the root Compose deployment, explicitly add `deploy/batch/compose.yml`.
This overlay runs `harden-llm-batch-worker` from the gateway image, inherits
the protected database/Garage/provider configuration and connects both API and
worker to `HARDEN_LLM_TEMPORAL_NETWORK`, an existing private service network.
It starts no additional database or Temporal service. It is not part of the
default production or preview deployment. The current preview template needs
its approved Garage and Temporal settings provisioned before jobs can be enabled.

Rollout gates are: provision the environment's namespace/network access and
HLLM-scoped Garage access; build the verified gateway SHA; run explicit additive
`schema`; enable the overlay for API and worker together; confirm namespace
task-queue pollers, authorized status reads and image identities. Then perform
an explicitly opted-in provider canary. An HTTP health check alone does not
prove provider acceptance. Worker startup verifies storage/namespace, and its
process exit triggers restart; it serves no HTTP health endpoint. Monitor its
Temporal pollers and job heartbeats/deadlines. Preserve accepted manifests,
results and workflow histories during rollback. Disable new batch submissions
and drain accepted work before removing workers or changing workflow versions.

The dispatcher initially releases up to 200 calls, then paces starts at 100/second,
with at most 128 active calls. Every 100 unthrottled validated completions
doubles the launch rate, up to 200/second. These are caller controls, not a
claim about provider entitlement. Concurrency and start rate are independent.

The optional admission window reserves at most `WINDOW_REQUESTS` starts per
`WINDOW_MS` from the first dispatch. Fast completions do not refill this budget.
Explicit same-window refusals refund their reservation; ordinary errors retain
it. A recovery probe can run after the provider's cooldown even when the budget
is occupied. Setting `WINDOW_REQUESTS=0` disables this estimate for aggressive
probing or a differently qualified provider. This is a configurable caller
estimate based on measurements, not a declaration of universal provider RPM.
Queued jobs report `admission_window` and `next_attempt_at` while it is exhausted.
Defaults select 128 active slots, a 200-start budget and 61 seconds based on
the retained experiments. Both this estimate and the launch pace are tunable.
Each new admission window replenishes its configured burst, still bounded by
the window budget and active-call ceiling. This avoids turning a 10 ms pacing
timer into one expensive workflow task for every start when budget is available.

One explicit throttle closes pool admission. Existing calls finish; one queued
item becomes the recovery probe after `Retry-After`. Without a supplied delay,
61 seconds from the admission episode's first start is the initial recovery
estimate. Failed probes back off; a successful probe reopens admission. The
launch rate halves once per throttle episode. A successful recovery probe
reopens at that adapted pace without granting another initial burst; one
accepted request does not establish that a whole burst is available. Delayed responses from the prior
episode cannot reopen the gate or invalidate a successful recovery.

Policy persists in the dispatcher. To change it, restart workers with the new
transport/concurrency settings, then send the configured policy explicitly:

```sh
harden-llm-gateway batch-policy
```

The acknowledgement means the signal was durably accepted. Inspect the
dispatcher's `state` query in Temporal to confirm application. Updating policy
does not grant another initial burst or override cooldown/account restrictions.
After resolving an account restriction, an operator can send the dispatcher's
`resume` signal with JSON `true`. This causes one recovery probe.
Account restrictions and pending recovery survive an idle queue; completing all
jobs or expiring their deadlines does not reset the account gate.

## 3. Submission, waiting and results

Submit `POST /v1/jobs` with the existing HLLM bearer and a stable
`Idempotency-Key` header:

```json
{
  "items": [
    {
      "custom_id": "task-0001",
      "request": {
        "model": "gpt-6-luna",
        "input": "Return the requested result.",
        "reasoning": {"effort": "low"}
      }
    }
  ]
}
```

The response is `202` only after Temporal acknowledges workflow start. Retry an
uncertain submission with the same key and input; changed input with that key
returns `409`. Each item has a distinct `custom_id`. Manifests are bounded to
32 MiB, and individual requests retain the ordinary 256 KiB limit.

Poll `status_url`. Counts distinguish queued, active, retry-waiting, succeeded
and failed items. `observed_at` is the status read time;
`last_worker_heartbeat_at` reports worker activity;
`last_progress_at` reports actual state/transport progress. A fresh status read
does not imply a fresh worker heartbeat. Cooldowns expose `waiting_reason`
and `next_attempt_at`. Authentication/account/billing restrictions pause the pool.

`result_url` returns `202` while pending, then NDJSON containing one response or
error per item. Check the distinct ID count against `counts.total`. An
interrupted download is incomplete and can be repeated without inference.
`httpclient.Client` provides `SubmitJob`, `JobStatus`, `WaitJob` and
`DownloadJobResult`, validated against OpenAPI. The downloader checks IDs,
counts, per-item schemas and success/error totals.

With the existing protected `HARDEN_LLM_TOKEN` environment value:

```sh
harden-llm-gateway wait-job \
  --base-url https://harden-llm-dev.prls.co \
  --job JOB_UUID --timeout 30m --result-file results.ndjson
```

The waiter emits status every five seconds and preserves the overall deadline.
It publishes a new mode-0600 result file only after validating the download.
It exits unsuccessfully for item errors. A job's default two-hour deadline is
separate from the provider's default 180-second generation budget. An item may
lower that budget through `harden.timeout_ms`; queue/cooldown waits do not use it.

## 4. Diagnostics and CPA ownership

Attempt logs include job/item/attempt IDs, dispatch/completion timestamps,
queue/retry wait, generation duration, time to headers/first output, token
usage, safe error/status codes and the bounded CPA trace-ID suffix. Raw CPA
trace prefixes, prompts, credentials and generated text are not diagnostic
fields. `cpaInternalAttempts: null` means CPA did not expose an attempt count;
it must not be interpreted as zero. Reasoning tokens are a subset of reported
completion tokens, not an additional charge added to that total.
`headers_ms` measures request start to first response byte. The existing
`first_output_ms` starts when SSE collection begins, so it has a different
origin and must not be compared directly with `headers_ms` as an end-to-end
timestamp. Generation duration spans core execution and response collection.

The worker sets its provider transport capacity explicitly, so changing the
admission ceiling does not leave an undisclosed 128-connection transport cap.
HTTP/2 multiplexing and actual HTTP overlap are measured separately.

CPA settings remain owned by `prls-co/CLIProxyAPI-setup`, tracked in
[#6](https://github.com/prls-co/CLIProxyAPI-setup/issues/6). Qualify
`request-retry: 0` against the existing `3` before changing the shared service.
Keep cooling enabled and the existing credential/account selection. The
request-retry setting controls additional rounds, not a concurrency allowance;
native attempts still need observation. The HLLM feature does not silently
change shared CPA configuration or choose another provider.
The owner implementation is
[CPA retry controls](https://github.com/prls-co/CLIProxyAPI-setup/pull/7), with
isolated pinned-image evidence for 0 versus 3; the shared live setting remains 3.

## 5. Verification

Cheap tests cover controller timing, stragglers, fairness, API validation and
client waiting/download invariants. The canonical runner owns the isolated
Temporal/PostgreSQL/Garage integration stack and unique test namespaces:

```sh
make test-fast
node scripts/run-test-tier.mjs --task go-temporal-integration
```

The real-stack cases prove 1,000 distinct local provider requests, native SSE,
durable results, scope isolation, idempotent submission and worker restart
during cooldown. Public CPA measurements require explicit live opt-in and are
recorded separately; deterministic tests do not establish upstream capacity.

The output-token study is under
`plans/evidence/harden-llm/cpa-output-token-study/2026-10-16/`. CPA's pinned Codex
translator strips `max_output_tokens`. The study varies actual output length;
no token quota should be configured from a requested output cap alone.

The [Temporal batch qualification report](../plans/evidence/harden-llm/cpa-temporal-batch/2026-10-16/report.md)
records three matched 1,000-result windowed runs: 254.287–367.589 seconds,
median 256.557 seconds. A fresh run after merging current main qualified in
308.439 seconds. These are complete validated results; earlier network failures
and a mixed-array pilot timeout remain failures in the same report. The final
merged-source browser-free release passed all 30 tasks. This evidence does not
establish a universal provider quota or guarantee completion within six minutes.
