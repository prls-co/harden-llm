# ADR-HLLM-035: Self-hosted CPA batch jobs

- Status: Accepted by user instruction on 2026-10-16; implementation qualification is recorded separately.
- Extends: ADR-HLLM-033 and ADR-HLLM-034 for explicitly submitted batch jobs.

## 1. Decision

Add key-scoped `/v1/jobs` admission and status/result reads to the existing Go
gateway. Use the existing self-hosted Temporal deployment, HLLM PostgreSQL
database/role, Garage client and diagnostic infrastructure. No Firebase or
Google Cloud service is introduced.

`stateful/batch` owns durable orchestration and persistence. The gateway command
owns Temporal connection/worker composition. Root execution and retry decisions
remain stateless and contain no Temporal dependency. The previous global SDK
ban is replaced by a machine-checked import boundary; all other excluded
dependencies remain excluded.

## 2. First release

Each item runs one generation against the configured CPA Responses connection,
`gpt-6-luna`, reasoning `low`. The core and Temporal provider activity each have
one attempt. A shared dispatcher owns retries for explicit capacity refusals,
durable cooldown timers, fair admission and one recovery probe. Ordinary errors
are item errors; no additional outcome-reconciliation service is introduced.
A positive retry hint on a failed attempt delays independent queued work without
replaying that inference or refunding its uncertain window reservation.

Batch jobs are separate from reference history. Phoenix retains Portal
admission, host routes and its existing interactive async lifecycle. Job
requests/results remain outside Temporal histories; workflows carry references
and bounded diagnostics. Incoming keys select storage scope only after bearer
validation, and no caller controls the CPA pool identity.

## 3. Limits and evidence

Concurrency, launch pace and estimated refill timing are independent controls.
The measured cold burst of 200 is not declared a universal RPM or concurrency
limit. Token usage and native CPA retries remain observable; unavailable values
stay unknown. Account restrictions pause admission.

The API is opt-in. Shared CPA configuration changes, consumer migrations,
deployment and production promotion each require their own recorded acceptance.
See [CPA batch jobs](../cpa-batch-jobs.md) for configuration and qualification.
