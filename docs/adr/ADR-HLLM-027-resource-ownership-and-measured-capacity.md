# ADR-HLLM-027: Test Resource Ownership and Measured Capacity

- Status: Accepted for the scoped test-harness implementation.
- Date: 2026-09-21.
- Plan: `plans/production-scale-efficiency-plan.md`.
- Requirements: REQ-341 through REQ-352.
- Verification: TEST-271 through TEST-280; EVAL-008 through EVAL-012.
- Scope: local managed Docker lifecycle and opt-in synthetic capacity evidence.

## Context

The existing tier runner owns task selection and per-task Postgres/Garage
service pools. Its exclusivity is invocation-local. The full-stack Compose
smoke creates a unique project but performs a pre-start cleanup before
registering its cleanup callback, logs final cleanup errors, and loses the
runner's temporary directory at invocation end. Independent CLI processes
sharing one local daemon are not coordinated. These gaps can leak test
resources and make later test timing difficult to interpret.

The runner also has a benchmark adapter and the gateway has bounded asynchronous
telemetry plus canonical execution records. A capacity test can reuse these
owners. No production traffic distribution/SLO was supplied, so local synthetic
results cannot certify production scale or justify a service migration by
themselves.

## Decisions

1. Register an atomic, private receipt before each managed fixture mutation.
   Record exact run/project/daemon/source identity and resource IDs. Receipts
   and failure reports live outside disposable runner scratch. Reconcile
   resources by exact project identity and existing Compose project labels;
   add a custom label only where the current Compose resource definition
   supports it.
2. The supervising runner is the final owner of child process termination,
   cleanup acceptance, and final inventory. Delete only exact resources whose
   receipt, labels, project identity, and attachment state agree. Preserve the
   first task error and report cleanup errors independently. Unknown inventory
   is a failed/unknown cleanup result.
3. Coordinate invocations through a local daemon-specific Linux file lock.
   Stale-resource recovery requires host boot ID, process ID, and process-start
   evidence. A TTL is never proof of death. This decision does not promise
   coordination for a daemon shared across client machines.
4. Keep current per-task service pools and mutable-state isolation. Do not
   introduce a second test scheduler, janitor service, lock database, or
   production resource.
5. Add capacity code only under an explicit opt-in. Exercise actual gateway
   assembly, stores, provider adapters, auth, and terminal REST/SSE behavior
   with synthetic data and a local scripted provider. Use the existing full
   Compose fixture only for the distinct telemetry/system boundary.
6. Keep canonical execution, accounting, and artifacts authoritative during
   telemetry failure. Export is bounded/best-effort; no unconditional trace
   delivery or automatic replay of an uncertain provider request is promised.
7. A no-change decision and an insufficient-evidence result are valid. A
   runtime remedy needs its own concrete requirement, failing test, release and
   rollback procedure, and equivalent before/after workload evidence.

## Initial test-harness controls

These bounds cap an opt-in test and do not establish customer SLOs or change
existing test/service timeout budgets:

- At most 2,000 offered requests and 256 in-flight requests per scenario.
- Four sequential exploratory workload points; at most three holdout samples.
- Stop generating work on any ownership/safety failure. Stop escalation after
  more than 5% unexpected failures among at least 100 launched requests or
  scheduler p95 lag above 100 ms. These stops invalidate/escalate the sample;
  they are not production availability targets.
- Stop on host available memory below 10%, filesystem free space below 5 GiB,
  or insufficient measured image-unpack space. Mark the run inconclusive and
  retain evidence; never reclaim non-owned data to continue.
- Lifecycle collection uses bounded per-inventory (15 s), diagnostics (20 s),
  child graceful-stop (10 s), and total cleanup (120 s) limits. They consume
  the existing enclosing command budget and are not added after its deadline.
- Report p99 only with at least 1,000 completions. Record sample counts and the
  interval method; small samples do not claim a reliable tail estimate.
- Unknown provider prices, queue metrics, costs, or host measurements remain
  null with a reason. No minimum performance improvement is assigned without
  a production SLO and a valid baseline.

The resource/headroom values above are conservative test-stop controls, not
claims measured from the production host. If an observed compatible host shows
that they prevent a safe test from starting, change them only by amending this
ADR with the captured hardware/workload evidence and rerunning the same safety
tests. Existing Compose readiness remains 300 seconds; existing smoke runner
budget remains 30 minutes. Neither is raised by this ADR.

## Consequences and rollback

Lifecycle cleanup failures now fail task acceptance and remain recoverable from
their retained receipt. A local lock prevents two local launchers from
overlapping destructive Docker tests, but cannot coordinate a remote shared
daemon. If daemon identity, receipt integrity, process ownership, or resource
attachment is ambiguous, stop cleanup and require exact operator resolution.

Capacity reports are exploratory unless a separately approved traffic/SLO
contract makes their workload representative. The capacity feature can be
removed without migrating production data. Revert a test-only release by
restoring its reviewed source revision. Any future runtime/schema change must
provide its own service-scoped artifact, compatibility, and rollback steps.
