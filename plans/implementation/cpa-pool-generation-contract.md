# CPA pool generation and adoption contract

## 1. Confirmed implementation baseline

Issue #134 builds on CPA batch PR #127, source
`6fdbf4f7eec9208a7e86c961151e97eebeeee3d3`, reconciled with status PR #140,
source `fceae693d6360fa0106d463d8735f326dc8e6dd4`.

- Stable pool workflow ID: `hllm-cpa-pool-v1/<configured pool ID>`.
- Existing workflow types: `HLLMCPADispatcherV1`, `HLLMBatchV1`.
- Batch PostgreSQL schema version 1; immutable request snapshots in batch_items.
- Existing provider activities run inside the dispatcher and drain before CAN.
- Current maximum generation snapshot: 180 seconds.
- Existing replay history stays supported; registration never authorizes replay
  of a claimed/uncertain inference.
- Reconciliation preserves the tracking route, ordinary response contracts and
  batch validation's extracted prepareResponses implementation. Unit tests for
  batch, HTTP API and gateway startup pass; this is not V2 acceptance.

## 2. Observed deployment and unknown adoption

A read-only 2026-10-31 inspection of the existing gateway found source label
`474342a2bbf7273df9b050b386447dc0d7954ea7`, image
`sha256:3920032b73966b05f0a942de2eabb2a366c1e8615c54b92bdfb737036d7f455f`.
Its filtered settings contain no enabled batch flag, Temporal address or
namespace. No running HLLM batch worker was present in the filtered container
list. No secrets, environment dump, inference or runtime mutation was used.

This does not inventory every retained research namespace or prove accepted
jobs absent everywhere. Before adoption, inspect the selected environment's
actual workflow/schema/state; preserve accepted work and stored results.

## 3. Required migration fence

Implement an additive PostgreSQL pool-generation marker and attempt ledger.
Every new grant/start validates the selected pool's generation. A stale worker
cannot create or dispatch a new attempt after authority changes. The marker is
not a substitute for preserved quota/cooldown/outstanding-permit state.

Keep the stable workflow ID through V1-to-V2 handoff. Quiesce V1 grants and drain
its owned activities with positive local-stop evidence; unresolved execution
blocks cutover. Continue the same authority into the V2 workflow/control queue,
carrying ready work, policy, quota window, restrictions, sequence and cursor.
Compatibility Enqueue must find that authority instead of starting a new V1 pool.

New independent attempt workflows use deterministic permit-based IDs, rejecting
replacement starts. They survive pool CAN, own a single-try provider activity,
and settle through real SQL claims/revisions. Metadata retries never repeat
inference. Ordinary calls acquire/claim/settle metadata permits from the same
pool while retaining HTTP request ownership; their prompts/results do not enter
Temporal history or the batch manifest/artifact tables.

## 4. Credential admission reuse

The retained CPA/Home audit distinguishes Home credential admission from HLLM
producer pacing/durable waiting. Existing Home support, if separately adopted,
can be the authoritative credential cap. HLLM still needs a bounded producer
queue, fairness, refill estimates, cooldown and outcome ownership. Do not add a
second custom credential semaphore or claim control of direct bypassing clients.
No CPA upgrade or new Home deployment is a prerequisite for producer orchestration.
Native busy refusals must be classified using qualified typed codes and trusted
retry hints. Do not infer a universal provider RPM/TPM number from local settings.

## 5. Remaining acceptance

The original architecture's P1-P6 remain open: long deadlines, claimed-attempt
ledger and exact settlement, independent workflow rollover, key/job fairness,
request-bound dispatch adapter, actual SQL/Temporal/Garage faults, 1,000/10,000
synthetic calls, retained-history replay, environment adoption and bounded CPA
acceptance. Neither this contract nor the baseline reconciliation implements them.
