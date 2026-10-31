# CPA shared pool operations

## 1. Scope and current delivery

The V2 pool schedules independent durable attempts using self-hosted Temporal,
the existing HLLM PostgreSQL pool and scoped Garage artifacts. It preserves the
stable workflow ID `hllm-cpa-pool-v1/<configured pool ID>` even after its workflow
type becomes `HLLMCPAPoolV2`. Control uses the configured task queue; provider
activities use that queue with `/provider` appended, in the same worker process.

This document describes source capabilities, not a deployed feature. Ordinary
OpenAI generation now uses the same V2 pool when batch admission is enabled and
the connection uses the configured CPA origin. Status lookup in issue #136 does
not supply durable waiting or result retrieval.
The 200-start/61-second default is an operating estimate, not a provider guarantee.
No CPA upgrade is required by this implementation.

## 2. Prepare the selected environment

1. Record the namespace, pool ID, source SHA, API/worker image identities and URL.
   Keep provider credentials in the existing approved environment; do not copy
   keys or production data into fixtures or a different environment.
2. Run the matching build's explicit `schema` command with the normal HLLM
   PostgreSQL configuration. API/worker startup and requests do not run DDL.
3. Inventory accepted jobs and the actual pool workflow. Preserve manifests,
   old zero/180-second generation budgets, results and restriction state.
4. Ensure only compatible workers poll the control queue before sending new
   upgrade controls. Drain older workers before replacement. An activity timeout
   or heartbeat expiry does not prove its local transport stopped.
5. Start the compatible worker with its existing `worker` command. It retains V1
   registrations and adds V2 workflows/provider queue handling. It does not call
   a real provider as a startup or deployment probe.
6. Enabling `HARDEN_LLM_BATCH_ENABLED=true` also enables ordinary admission for
   the configured upstream's CPA origin across aliases, paths and key scopes.
   The original selected model/settings stay intact. Upgrade to active V2 before
   serving managed ordinary calls: absent/V1/quiesced authority fails closed.
   Other provider origins and model discovery bypass this CPA pool.

## 3. Upgrade or initialize the scheduling authority

Run the matching gateway build with the selected environment's protected settings:

```sh
harden-llm-gateway batch-upgrade
```

The command has a bounded 20-minute local wait, respects an earlier caller
context and prints progress every five seconds. Stopping the command does not
cancel accepted jobs or reset the pool. Inspect the same workflow identity and
rerun the command rather than submitting replacement inference.

For an existing V1 workflow, the namespaced upgrade signal first stops new V1
grants. V1 waits for its owned provider activities and metadata settlement. It
attaches remaining manifests, validates local-stop evidence and advances the SQL
generation fence before continuing the same workflow ID into V2. Controller
window/cooldown/restrictions and the monotonic permit sequence are preserved.
An old activity failure without local-stop evidence blocks transfer. Its terminal
inference outcome must not be rewritten or replayed to make migration succeed.

Compatible Enqueue attaches a manifest before waking the pool and resolves the
current SQL generation. An enqueue crossing the handoff cannot create another V1
authority or lose its accepted manifest. V2 uses completed-workflow-ID reuse
rejection; a missing/closed authority with existing state is not cold-started.

If neither a pool workflow nor prior batch/pool state exists, the command may
start an empty V1 authority and immediately hand it off. The emptiness check is
read-only; Temporal starts before its generation marker is created. A lost start
acknowledgement therefore retries the same identity without inventing another
controller from a database-only marker. Empty initialization makes zero provider
calls. Existing state with a missing workflow is refused for operator inspection.

`v2_ready` requires a live V2 query and matching active database generation. It
confirms scheduling ownership, not provider availability or throughput acceptance.
Do not point an older binary at pending V2 workflow/activity types as rollback.
Quiesce and drain a compatible worker, preserving accepted state and evidence.

## 4. Waiting, progress and deadlines

`GET /v1/jobs/{jobID}` keeps its existing status/count/result contract. Waiting
reasons now include admission-window and provider cooldown, account restriction,
pending dispatch, worker unavailability, persistence wait, workflow rollover,
unresolved execution and occupied pool capacity.

Heartbeat and actual progress are separate. Pool publication pages update only
pending jobs and ordinary waiters in their own pool; provider progress requires
its invocation claim.
Identical updates coalesce within five seconds. Heartbeat/polling never invent
byte progress, percentage complete or a result ETA. `next_attempt_at` is a known
eligibility floor; it may be absent when no such time is established.

For V2 jobs, optional `execution_deadline` equals the existing `deadline`.
`finalization_deadline` is 30 seconds later for bounded cleanup/persistence.
Neither extends provider execution. Job expiry handles unowned work; claimed
items remain fenced to their attempts until settlement. Late or ambiguous
execution is retained as failed/unconfirmed, without automatic inference replay.
Missing positive local stop retains capacity and can pause future admission.

The original ordinary OpenAI request deadline remains 60 seconds. A tracking
lookup cannot extend it. Multi-minute callers explicitly choose durable jobs.

Ordinary requests acquire a separate permit for every concrete generation,
transport retry or repair dispatch. Cache hits bypass generation admission.
They retain ordinary JSON/SSE responses; no request becomes a job or returns
`202` automatically. Tracked queued calls expose `stage=admission_wait`, actual
pool reasons and nullable eligibility. Native entry starts only after admission.
Post-entry transport failure or partial refusal remains unconfirmed and suppresses
automatic core/compatible-SDK replay (`x-should-retry: false`). Explicit pre-output
refusals retain trusted retry floors. Local metadata cleanup is bounded separately
and cannot extend provider execution or replace a known response.

## 5. Verification and outstanding acceptance

See `plans/implementation/cpa-delivery-2026-10-31.md` for exact failure/pass
receipts and source checkpoints. Local integration evidence is distinct from
hosted checks, environment deployment and representative CPA acceptance.

Ordinary dispatch scheduling now has a metadata-only waiter record with scoped
owner/execution/dispatch IDs. It shares scope/owner rotation, the global cap,
window, cooling and probe state with jobs. A live request must claim and dispatch
its own reservation; the starter cannot create an inference workflow for it.
Cancellation and a ten-second waiter liveness expiry prevent a future dispatch,
but never prove an already claimed provider transport stopped. Waiter refreshes
coalesce and cannot resurrect an expired owner.

Waiter metadata expires 24 hours after its accepted request deadline. Cleanup
uses bounded pages and removes settled permit metadata only after the pool has
applied that settlement and positive local-stop evidence exists. Unknown active
claims remain retained rather than being freed by a metadata TTL. The ordinary
stateful provider adapter uses these records directly; it creates no durable
request inference workflow, batch manifest or result artifact.

New V2 histories also bound unclaimed handoffs separately from active native
capacity. The current source uses 32 pending claims; a positive SQL claim frees
one handoff slot without releasing its active permit. Recorded version-one
histories retain their 16-slot behavior. The default 128 native cap uses 32
workflow-task/control-activity pollers and eight provider-activity pollers. Those are control
capacities, not additional provider permissions.

Ordinary metadata uses four operation slots per API instance, split between
two enqueue/poll slots and two existing-permit/cancellation slots. Those slots
never span native I/O and do not change the shared provider cap. Coalesced wake
hints cannot grant dispatch permission. Claim notices run in a bounded sender
alongside the confirmed invocation and are joined before finalization.

Before declaring the full #134 feature delivered, complete remaining
recovery/timing qualification, mixed 1,000/10,000-call tests and selected
environment/CPA acceptance. Browser layout, public-provider capacity and deployment
are not established by these source instructions or synthetic-provider tests.
