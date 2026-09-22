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
   first task error and report cleanup errors independently. A best-effort
   Compose `down` error remains visible as a cleanup warning if exact fallback
   cleanup succeeds, final inventory is empty, and the `cleaned` receipt is
   durable. Unknown inventory, failed exact removal, foreign attachment,
   remaining resources, or receipt persistence failure remains fatal.
3. Coordinate Docker selections through a private Linux `flock` file keyed by
   the SHA-256 of the verified daemon ID. Require a local Unix-socket endpoint;
   remote contexts fail closed. Bound lock acquisition to 30 seconds, then
   fail with a distinct preflight diagnostic rather than running unlocked.
   Pass a random lease capability to nested managed children and validate the
   daemon, private lock path, boot ID, owner/holder PID, and process-start
   identities before reusing it. Stale-resource recovery requires host boot
   ID, process ID, and process-start evidence. A TTL is never proof of death;
   active, ambiguous, and corrupt receipts remain untouched and block work.
   This decision does not promise coordination for a daemon shared across
   client machines or through an opaque remote-forwarding socket proxy.
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

## Receipt protocol

On Linux, the default receipt directory is
`~/.local/state/harden-llm/test-resources`; an operator or test can select a
private alternate directory with `HARDEN_LLM_TEST_RESOURCE_DIR`. The configured
root and each per-run directory must be owned by the current user and have mode
`0700`; receipt files are created atomically with mode `0600`. Each Compose
project has one `<root>/<runId>/resource-<project>.json` receipt. Existing
records are not overwritten during
registration, and invalid paths, permissions, or schema fail before Compose
creation.

The version-1 contract is shared by
`scripts/test-resource-lifecycle.mjs` and
`internal/integrationtest/resource_receipt.go`, with the accepted JSON vector
in `internal/integrationtest/testdata/resource_receipt_valid.json`. It records
the run, exact Compose project/files, Docker daemon identity, source commit,
host boot ID, supervising runner PID/start identity, lifecycle state, and exact
resource ID lists. It does not record prompts, provider credentials, user data,
or the Compose environment. Lifecycle states are `registered`, `creating`,
`running`, `cleaning`, `cleanup-pending`, and `cleaned`; transitions are
validated and state writes are atomic.

The Node runner gives child fixture processes
`HARDEN_LLM_TEST_RUN_ID`, `HARDEN_LLM_TEST_RESOURCE_DIR`, and on Linux the
supervisor PID/start identity. Go Compose fixtures reject direct unmanaged
entrypoints when these ownership values are absent. `HARDEN_LLM_TEST_RESOURCE_RECEIPT`
is the per-pool path passed to the runner's fake/real Docker subprocess boundary;
Go-owned Compose projects register separate receipts under the same ledger.
Source identity is the checked-out `git rev-parse HEAD`; because it does not
hash uncommitted contents, a dirty-worktree flag/fingerprint remains an
explicit follow-up before local receipts can be treated as exact code snapshots.

## Initial test-harness controls

These bounds cap an opt-in test and do not establish customer SLOs or change
existing test/service timeout budgets:

- At most 2,000 offered requests and 256 in-flight requests per scenario.
- Four sequential exploratory workload points; at most three holdout samples.
- Stop generating work on any ownership/safety failure. Stop escalation after
  more than 5% unexpected failures among at least 100 launched requests or
  scheduler p95 lag above 100 ms. These stops invalidate/escalate the sample;
  they are not production availability targets.
- Stop on host available memory below 10%, Docker data-root free space below
  5 GiB, or insufficient measured image-unpack space. Mark the run inconclusive and
  retain evidence; never reclaim non-owned data to continue.
- Lifecycle collection uses bounded per-inventory (15 s), diagnostics (20 s),
  child graceful-stop (10 s), and shared post-stop cleanup (120 s per runner
  invocation) limits. The test/provider child budget is unchanged; cleanup does
  not restart or extend a test or LLM call. The runner wall-clock bound can
  therefore include the existing termination grace and one cleanup safety tail
  after a child timeout/cancellation. If that tail expires, retain
  `cleanup-pending` and fail acceptance rather than extending cleanup
  indefinitely.
- Managed runner reports expose separate Docker endpoint/daemon identification,
  daemon-lock wait, and stale-receipt recovery durations. Pure selections leave
  those fields `null` and make no Docker call.
- Report p99 only with at least 1,000 completions. Record sample counts and the
  interval method; small samples do not claim a reliable tail estimate.
- Unknown provider prices, queue metrics, costs, or host measurements remain
  null with a reason. No minimum performance improvement is assigned without
  a production SLO and a valid baseline.

The disk check reads Docker's configured data-root filesystem, not the
repository checkout filesystem. The 5 GiB floor is not an image-size forecast;
operators must still ensure measured free space covers the selected images.
The resource/headroom values above are conservative test-stop controls, not
claims measured from the production host. If an observed compatible host shows
that they prevent a safe test from starting, change them only by amending this
ADR with the captured hardware/workload evidence and rerunning the same safety
tests. Existing Compose readiness remains 300 seconds; existing smoke runner
budget remains 30 minutes. Neither is raised by this ADR.

## Consequences and rollback

Unresolved lifecycle cleanup failures now fail task acceptance and remain
recoverable from their retained receipt. A non-fatal Compose `down` warning is
not hidden: it identifies a graceful-stage error whose exact fallback and
empty final state were independently verified. A local lock prevents two local launchers from
overlapping destructive Docker tests, but cannot coordinate a remote shared
daemon. If daemon identity, receipt integrity, process ownership, or resource
attachment is ambiguous, stop cleanup and require exact operator resolution.

Capacity reports are exploratory unless a separately approved traffic/SLO
contract makes their workload representative. The capacity feature can be
removed without migrating production data. Revert a test-only release by
restoring its reviewed source revision. Any future runtime/schema change must
provide its own service-scoped artifact, compatibility, and rollback steps.
