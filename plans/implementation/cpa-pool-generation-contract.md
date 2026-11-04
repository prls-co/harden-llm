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

## 6. P1 checkpoint evidence (implementation in progress)

TEST-496 first failed on the legacy 180-second clamp, rejection of 300/900-second
new requests, and authorization to requeue a partial-output 429. Targeted GREEN
now covers immutable old budgets, 300/900-second new snapshots, exact parent/job
context deadlines, and terminal unknown partial-output outcomes. The retained
V1 replay still passes using a workflow version marker for longer activity
budgets. A virtual 190-second generation succeeds once within 300 seconds.

TEST-497 first failed when a new default invalidated an accepted idempotency
key. Real PostgreSQL GREEN now resumes the accepted job without rewriting its
snapshot, rejects explicit different budgets/custom IDs, handles JSONB schema
key order, and compares large JSON integers without float rounding.

The defaulted-timeout marker is internal manifest metadata, not a new HTTP
request field. Ordinary request maximum remains 60 seconds. Batch native header
budget follows the configured maximum while each generation has its own shorter
snapshot/job/parent deadline.

A parallel fast run exposed the fixed frontend HTTP port 4002. TEST-049 gained a
cheap configuration regression; browser-free frontend tasks now request port 0
from the existing runtime configuration. Browser/Compose opt-ins are unchanged.
The first policy test title violated the existing backend dependency-name guard;
rename the shared-runner test generically, keep that guard unchanged. A full fast
rerun with both corrections is recorded separately; prior failures are not passes.

Remaining P1/P3 boundary: item outcome and pool admission action must be separate.
In particular, partial-output throttling cannot replay/refund the item, but must
still cool future admission, including without a server delay hint. Add this to
the V2 controller/settlement tests rather than treating terminal uncertainty as
absence of a quota signal. Longer-job finalization grace, ledger claims, local-stop
proof, independent workflows and the ordinary adapter are still outstanding.

## 7. Continuation checkpoint

- Status PR #140 has a further qualified lifecycle/retention checkpoint at
  `0ef61e3` on its independent source branch; integrate that branch into this
  stacked branch after the current P1 source gate finishes.
- This branch's P1 edits are not yet committed. The current full fast process
  is unified-exec session 44869, with output redirected to
  `tmp/cpa-budget-port-policy-fixed-fast.log`. Poll that exact handle; do not
  replace a running gate on the basis of quiet output.
- `tmp/cpa-budget-adoption-exact-numbers.json` is accepted against real PG.
  `tmp/cpa-budget-owners.log` passes batch/API/startup checks including retained
  V1 replay, a virtual 190-second success and exact context-deadline tests.
- Preserve the original worktree's unrelated dirty files. Current original
  CPA feature worktree and status worktree are separate from this branch.
- Before claiming P1 complete, reconcile the remaining outcome/admission-action
  separation above, generation deadline diagnostics and later finalization-grace
  contract. P2-P6 are still required. Keep the overall thread goal active.

P1 checkpoint gate: `tmp/cpa-budget-port-policy-fixed-fast.log` records all ten
fast tasks accepted with no cleanup errors/warnings. This includes process-owned
frontend HTTP ports and the unchanged dependency-boundary guard. Focused real SQL
exact-number adoption and batch/API/startup/replay checks above also passed.
The remaining P1/P3 boundary and P2-P6 work are not marked complete.
